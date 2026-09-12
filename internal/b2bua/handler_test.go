package b2bua

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/internal/trunk"
	"github.com/thorsager/trecs/proto"
)

type mockB2BUATx struct {
	mu        sync.Mutex
	responses []*proto.SIPMessage
}

func (m *mockB2BUATx) Respond(res *proto.SIPMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = append(m.responses, res)
}

// snapshot returns a copy of the responses recorded so far; safe for tests
// where the handler responds from a goroutine.
func (m *mockB2BUATx) snapshot() []*proto.SIPMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*proto.SIPMessage, len(m.responses))
	copy(out, m.responses)
	return out
}

func (m *mockB2BUATx) Target() sip.Target       { return sip.Target{} }
func (m *mockB2BUATx) Transport() sip.Transport { return nil }

func cancelRequest(t *testing.T, toWithTag bool) *proto.SIPMessage {
	t.Helper()
	to := "<sip:bob@localhost>"
	if toWithTag {
		to = "<sip:bob@localhost>;tag=bob-tag"
	}
	raw := "CANCEL sip:bob@localhost SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKcancel-test\r\n" +
		"From: <sip:alice@localhost>;tag=alice\r\n" +
		"To: " + to + "\r\n" +
		"Call-ID: cancel-test-call-id\r\n" +
		"CSeq: 1 CANCEL\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalSIPDatagram: %v", err)
	}
	return msg
}

func TestResolveClientAddr_LoopbackWithNAT(t *testing.T) {
	h := NewHandler(Config{NATAddress: "host.docker.internal"})

	sdp := &proto.SDP{
		Connection: &proto.ConnectionInfo{Address: "127.0.0.1"},
		MediaDescs: []proto.MediaDescription{
			{Type: "audio", Port: 7078},
		},
	}

	ip, port := h.resolveClientAddr(sdp)
	assert.Equal(t, 7078, port)
	if resolved := net.ParseIP(ip); resolved != nil {
		assert.False(t, resolved.IsLoopback(), "expected non-loopback IP when NATAddress is set")
	} else {
		t.Logf("NATAddress hostname did not resolve, got raw: %s", ip)
	}
}

func TestResolveClientAddr_LoopbackNoNAT(t *testing.T) {
	h := NewHandler(Config{})

	sdp := &proto.SDP{
		Connection: &proto.ConnectionInfo{Address: "127.0.0.1"},
		MediaDescs: []proto.MediaDescription{
			{Type: "audio", Port: 7078},
		},
	}

	ip, port := h.resolveClientAddr(sdp)
	assert.Equal(t, "127.0.0.1", ip)
	assert.Equal(t, 7078, port)
}

func TestResolveClientAddr_NonLoopback(t *testing.T) {
	h := NewHandler(Config{NATAddress: "host.docker.internal"})

	sdp := &proto.SDP{
		Connection: &proto.ConnectionInfo{Address: "192.168.1.100"},
		MediaDescs: []proto.MediaDescription{
			{Type: "audio", Port: 7078},
		},
	}

	ip, port := h.resolveClientAddr(sdp)
	assert.Equal(t, "192.168.1.100", ip)
	assert.Equal(t, 7078, port)
}

func TestHandleCancel_Sends487WhenNoEarlyCall(t *testing.T) {
	h := NewHandler(Config{})

	tx := &mockB2BUATx{}
	h.HandleCancel(t.Context(), cancelRequest(t, false), tx)

	if len(tx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(tx.responses))
	}

	res := tx.responses[0]
	if res.StatusCode() != 487 {
		t.Fatalf("expected 487, got %d", res.StatusCode())
	}
	if res.CSeq.Method != proto.SIPMethodINVITE {
		t.Fatalf("expected CSeq method INVITE, got %s", res.CSeq.Method)
	}

	to := res.Headers.GetFirst("To")
	if !strings.Contains(to, "tag=") {
		t.Fatalf("expected To header to contain a tag, got %s", to)
	}
}

func TestHandleCancel_PreservesExistingToTag(t *testing.T) {
	h := NewHandler(Config{})

	tx := &mockB2BUATx{}
	h.HandleCancel(t.Context(), cancelRequest(t, true), tx)

	if len(tx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(tx.responses))
	}

	to := tx.responses[0].Headers.GetFirst("To")
	if !strings.Contains(to, "tag=bob-tag") {
		t.Fatalf("expected existing To tag to be preserved, got %s", to)
	}
}

// reInviteRequest builds a minimal in-dialog re-INVITE for testing. The
// supported argument sets the Supported header value ("" omits it entirely);
// se and minSE add the corresponding session-timer headers when non-empty.
func reInviteRequest(t *testing.T, callID, supported, se, minSE string) *proto.SIPMessage {
	t.Helper()
	raw := "INVITE sip:bob@localhost SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKreinvite-test\r\n" +
		"From: <sip:alice@localhost>;tag=alice-tag\r\n" +
		"To: <sip:bob@localhost>;tag=bob-tag\r\n" +
		"Call-ID: " + callID + "\r\n" +
		"CSeq: 2 INVITE\r\n" +
		"Max-Forwards: 70\r\n"
	if supported != "" {
		raw += "Supported: " + supported + "\r\n"
	}
	if se != "" {
		raw += "Session-Expires: " + se + "\r\n"
	}
	if minSE != "" {
		raw += "Min-SE: " + minSE + "\r\n"
	}
	raw += "Content-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalSIPDatagram: %v", err)
	}
	return msg
}

// newReInviteCall builds a call where Alice->Bob re-INVITEs forward to Bob's
// capture transport.
func newReInviteCall(t *testing.T, bobTransport sip.Transport) *Call {
	t.Helper()
	bobDialog := sip.NewDialog(
		sip.DialogID{CallID: "bob-call", LocalTag: "server", RemoteTag: "bob-remote"},
		"sip:server@127.0.0.1:5060", "sip:bob@localhost", "sip:bob@localhost:9998",
	)
	aliceDialog := sip.NewDialog(
		sip.DialogID{CallID: "alice-call", LocalTag: "server", RemoteTag: "alice-remote"},
		"sip:server@127.0.0.1:5060", "sip:alice@localhost", "sip:alice@localhost:9999",
	)
	return &Call{
		AliceCallID:     "alice-call",
		BobCallID:       "bob-call",
		AliceDialog:     aliceDialog,
		BobDialog:       bobDialog,
		AliceTransport:  &captureTransport{},
		BobTransport:    bobTransport,
		AliceTarget:     &sip.Target{},
		BobTarget:       &sip.Target{},
		AliceContactURI: "sip:alice@localhost:9999",
		BobContactURI:   "sip:bob@localhost:9998",
	}
}

func TestHandleReInvite_ViaBranchMatchesUAC(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newReInviteCall(t, bobTP)

	tx := &mockB2BUATx{}
	req := reInviteRequest(t, call.AliceCallID, "timer", "900;refresher=uas", "90")
	h.handleReInvite(t.Context(), req, tx, call)

	fwd := bobTP.lastSent()
	if fwd == nil {
		t.Fatal("expected forwarded re-INVITE to be sent to Bob")
	}
	via := fwd.Headers.GetFirst("Via")
	if !strings.Contains(via, "branch=") {
		t.Fatalf("forwarded re-INVITE Via missing branch: %q", via)
	}
	// The branch must come from a registered UAC transaction (RFC 3261 §17.1.3),
	// so a response carrying it is routable back to the UAC.
	if h.uacMgr.Get(viaBranch(via)) == nil {
		t.Errorf("Via branch %q not found in UACManager; responses would not reach the re-INVITE UAC", viaBranch(via))
	}
}

// viaBranch extracts the branch value from a Via header value.
func viaBranch(via string) string {
	if i := strings.Index(via, "branch="); i != -1 {
		return via[i+len("branch="):]
	}
	return ""
}

// dispatchReInvite builds an in-dialog INVITE with explicit tags for
// dispatch-level (HandleInvite) tests.
func dispatchReInvite(t *testing.T, callID, fromTag, toTag, se string) *proto.SIPMessage {
	t.Helper()
	raw := "INVITE sip:bob@localhost SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKdispatch\r\n" +
		"From: <sip:alice@localhost>;tag=" + fromTag + "\r\n" +
		"To: <sip:bob@localhost>;tag=" + toTag + "\r\n" +
		"Call-ID: " + callID + "\r\n" +
		"CSeq: 2 INVITE\r\n" +
		"Max-Forwards: 70\r\n"
	if se != "" {
		raw += "Session-Expires: " + se + "\r\n"
	}
	raw += "Content-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalSIPDatagram: %v", err)
	}
	return msg
}

// newDispatchCall returns a call with consistent per-leg dialog tags and
// stores it in the handler's store, so HandleInvite routes in-dialog
// INVITEs through the re-INVITE path.
func newDispatchCall(t *testing.T, h *Handler, bobTransport sip.Transport) *Call {
	t.Helper()
	call := newReInviteCall(t, bobTransport)
	call.AliceFromTag = "alice-tag"
	call.AliceServerTag = "server-alice"
	call.BobRemoteTag = "bob-tag"
	call.BobCalleeTag = "server-bob"
	h.store.Store(call)
	return call
}

// TestHandleInvite_ReInviteTagMismatch481 verifies in-dialog INVITEs whose
// From/To tags do not match the leg's dialog are rejected with 481 instead
// of being forwarded (RFC 3261 §12.2 dialog identification).
func TestHandleInvite_ReInviteTagMismatch481(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newDispatchCall(t, h, bobTP)

	tests := []struct {
		name    string
		callID  string
		fromTag string
		toTag   string
	}{
		{name: "wrong To tag on alice leg", callID: call.AliceCallID, fromTag: "alice-tag", toTag: "forged"},
		{name: "wrong From tag on alice leg", callID: call.AliceCallID, fromTag: "forged", toTag: "server-alice"},
		{name: "cross-leg tags on alice Call-ID", callID: call.AliceCallID, fromTag: "bob-tag", toTag: "server-bob"},
		{name: "cross-leg tags on bob Call-ID", callID: call.BobCallID, fromTag: "alice-tag", toTag: "server-alice"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := &mockB2BUATx{}
			h.HandleInvite(t.Context(), dispatchReInvite(t, tc.callID, tc.fromTag, tc.toTag, ""), tx)
			resp := waitForRelayedResponse(t, tx)
			if resp.StatusCode() != 481 {
				t.Fatalf("status = %d, want 481", resp.StatusCode())
			}
			if got := bobTP.sentCount(); got != 0 {
				t.Errorf("mismatched re-INVITE was forwarded (%d sends), want 0", got)
			}
		})
	}
}

// TestHandleInvite_ReInviteMatchingTagsForwarded verifies a legitimately
// tagged in-dialog INVITE still routes to the other leg after the tag
// validation was introduced.
func TestHandleInvite_ReInviteMatchingTagsForwarded(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newDispatchCall(t, h, bobTP)

	tx := &mockB2BUATx{}
	h.HandleInvite(t.Context(), dispatchReInvite(t, call.AliceCallID, "alice-tag", "server-alice", ""), tx)

	if bobTP.lastSent() == nil {
		t.Fatal("matching re-INVITE was not forwarded to Bob")
	}
}

// TestHandleInvite_ReInviteBelowMinSE422 verifies in-dialog refreshes with a
// Session-Expires below the negotiated minimum are rejected with 422 and the
// mandatory Min-SE header (RFC 4028 §7.2), using the leg's negotiated Min-SE
// when one exists and the global floor otherwise.
func TestHandleInvite_ReInviteBelowMinSE422(t *testing.T) {
	tests := []struct {
		name      string
		legTimer  *SessionTimer
		wantMinSE string
	}{
		{name: "leg negotiated Min-SE applies", legTimer: &SessionTimer{Interval: 600 * time.Second, MinSE: 300 * time.Second, Refresher: "uac"}, wantMinSE: "300"},
		{name: "global floor without leg timer", legTimer: nil, wantMinSE: "90"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bobTP := &captureTransport{}
			h := newTestHandler(t)
			h.uacMgr = sip.NewUACManager()
			call := newDispatchCall(t, h, bobTP)
			call.AliceSessionTimer = tc.legTimer

			tx := &mockB2BUATx{}
			h.HandleInvite(t.Context(), dispatchReInvite(t, call.AliceCallID, "alice-tag", "server-alice", "60"), tx)
			resp := waitForRelayedResponse(t, tx)
			if resp.StatusCode() != 422 {
				t.Fatalf("status = %d, want 422", resp.StatusCode())
			}
			if got := resp.Headers.GetFirst("Min-SE"); got != tc.wantMinSE {
				t.Errorf("Min-SE = %q, want %q", got, tc.wantMinSE)
			}
			if got := bobTP.sentCount(); got != 0 {
				t.Errorf("below-minimum re-INVITE was forwarded (%d sends), want 0", got)
			}
		})
	}
}

// staticPasswordStore is a minimal PasswordStore for auth dispatch tests.
type staticPasswordStore struct{}

func (staticPasswordStore) Realm() string             { return "127.0.0.1" }
func (staticPasswordStore) Algorithm() string         { return "SHA-256" }
func (staticPasswordStore) HA1(string) (string, bool) { return "aa", true }
func (staticPasswordStore) AORs(string) ([]string, bool) {
	return []string{"sip:alice@localhost"}, true
}

// TestHandleInvite_ReInviteRequiresAuth verifies an unauthenticated in-dialog
// INVITE is challenged (407) when proxy auth is configured, instead of being
// forwarded — previously the re-INVITE path bypassed authentication entirely.
func TestHandleInvite_ReInviteRequiresAuth(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	h.SetProxyPasswordStore(staticPasswordStore{}, t.Context())
	call := newDispatchCall(t, h, bobTP)

	tx := &mockB2BUATx{}
	h.HandleInvite(t.Context(), dispatchReInvite(t, call.AliceCallID, "alice-tag", "server-alice", ""), tx)
	resp := waitForRelayedResponse(t, tx)
	if resp.StatusCode() != 407 {
		t.Fatalf("status = %d, want 407 (Proxy Authentication Required)", resp.StatusCode())
	}
	if got := bobTP.sentCount(); got != 0 {
		t.Errorf("unauthenticated re-INVITE was forwarded (%d sends), want 0", got)
	}
}

// TestApplyRelayedTimerNegotiation verifies a relayed re-INVITE 200 OK
// updates both legs' timer state and rewrites the dialog-relative refresher
// parameter for the originating dialog (RFC 4028 §7.2, §8).
func TestApplyRelayedTimerNegotiation(t *testing.T) {
	h := newTestHandler(t)

	mkResp := func(headers string) *proto.SIPMessage {
		return trunk200OK(t, headers)
	}

	t.Run("updates both legs and rewrites refresher", func(t *testing.T) {
		call := &Call{AliceCallID: "alice-call", BobCallID: "bob-call"}
		call.AliceSessionTimer = &SessionTimer{Interval: 600 * time.Second, MinSE: 90 * time.Second, Refresher: "uac"}
		call.BobSessionTimer = &SessionTimer{Interval: 600 * time.Second, MinSE: 90 * time.Second, Refresher: "uac"}

		origReq := reInviteRequest(t, call.AliceCallID, "timer", "600;refresher=uac", "90")
		resp := mkResp("Session-Expires: 900;refresher=uas\r\nMin-SE: 300\r\n")
		okResp := proto.NewResponse(origReq, 200, "OK")
		copyTimerNegotiationHeaders(resp, okResp)

		h.applyRelayedTimerNegotiation(call, true, origReq, resp, okResp)

		// Forwarded (Bob) leg takes the responder's values verbatim.
		if got := call.BobSessionTimer; got.Interval != 900*time.Second || got.Refresher != "uas" || got.MinSE != 300*time.Second {
			t.Errorf("bob timer = %+v, want {900s uas minSE 300s}", got)
		}
		// Originating (Alice) leg keeps the refresher role it requested.
		if got := call.AliceSessionTimer; got.Interval != 900*time.Second || got.Refresher != "uac" || got.MinSE != 300*time.Second {
			t.Errorf("alice timer = %+v, want {900s uac minSE 300s}", got)
		}
		// The relayed header must match the originating dialog's role.
		if got := okResp.Headers.GetFirst("Session-Expires"); got != "900;refresher=uac" {
			t.Errorf("relayed Session-Expires = %q, want 900;refresher=uac", got)
		}
		if got := okResp.Headers.GetFirst("Min-SE"); got != "300" {
			t.Errorf("relayed Min-SE = %q, want 300", got)
		}
	})

	t.Run("creates timer for leg without one", func(t *testing.T) {
		call := &Call{AliceCallID: "alice-call", BobCallID: "bob-call"}
		// Refresh originated on the Bob leg, forwarded to Alice.
		origReq := reInviteRequest(t, call.BobCallID, "timer", "600;refresher=uac", "90")
		resp := mkResp("Session-Expires: 1200;refresher=uac\r\n")
		okResp := proto.NewResponse(origReq, 200, "OK")
		copyTimerNegotiationHeaders(resp, okResp)

		h.applyRelayedTimerNegotiation(call, false, origReq, resp, okResp)

		// Forwarded (Alice) leg: responder's values verbatim.
		if got := call.AliceSessionTimer; got == nil || got.Interval != 1200*time.Second || got.Refresher != "uac" {
			t.Errorf("alice timer = %+v, want {1200s uac}", got)
		}
		// Originating (Bob) leg: created with the originator's requested role.
		if got := call.BobSessionTimer; got == nil || got.Interval != 1200*time.Second || got.Refresher != "uac" {
			t.Errorf("bob timer = %+v, want {1200s uac}", got)
		}
		if got := okResp.Headers.GetFirst("Session-Expires"); got != "1200;refresher=uac" {
			t.Errorf("relayed Session-Expires = %q, want 1200;refresher=uac", got)
		}
	})

	t.Run("no Session-Expires in response leaves state untouched", func(t *testing.T) {
		call := &Call{AliceCallID: "alice-call", BobCallID: "bob-call"}
		call.AliceSessionTimer = &SessionTimer{Interval: 600 * time.Second, MinSE: 90 * time.Second, Refresher: "uas"}
		call.BobSessionTimer = &SessionTimer{Interval: 600 * time.Second, MinSE: 90 * time.Second, Refresher: "uac"}

		origReq := reInviteRequest(t, call.AliceCallID, "timer", "600;refresher=uas", "90")
		resp := mkResp("")
		okResp := proto.NewResponse(origReq, 200, "OK")

		h.applyRelayedTimerNegotiation(call, true, origReq, resp, okResp)

		if got := call.AliceSessionTimer; got.Interval != 600*time.Second || got.Refresher != "uas" {
			t.Errorf("alice timer changed without negotiated Session-Expires: %+v", got)
		}
		if got := okResp.Headers.GetFirst("Session-Expires"); got != "" {
			t.Errorf("relayed Session-Expires invented without peer negotiation: %q", got)
		}
	})
}

func TestHandleReInvite_ForwardsSessionHeaders(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newReInviteCall(t, bobTP)

	tx := &mockB2BUATx{}
	req := reInviteRequest(t, call.AliceCallID, "timer", "600;refresher=uas", "120")
	h.handleReInvite(t.Context(), req, tx, call)

	fwd := bobTP.lastSent()
	if fwd == nil {
		t.Fatal("expected forwarded re-INVITE to be sent to Bob")
	}
	// Session-timer negotiation headers must propagate to the other leg
	// (RFC 4028 §8.1/§9).
	if se := fwd.Headers.GetFirst("Session-Expires"); se != "600;refresher=uas" {
		t.Errorf("forwarded Session-Expires = %q, want %q", se, "600;refresher=uas")
	}
	if ms := fwd.Headers.GetFirst("Min-SE"); ms != "120" {
		t.Errorf("forwarded Min-SE = %q, want 120", ms)
	}
}

// TestHandleReInvite_TimerAdvertisement verifies the forwarded re-INVITE only
// advertises RFC 4028 timer support when timers are actually in play for the
// dialog.
func TestHandleReInvite_TimerAdvertisement(t *testing.T) {
	tests := []struct {
		name      string
		supported string
		se        string
		minSE     string
		setup     func(*Call)
		wantAdved bool
	}{
		{
			name:      "unrelated re-INVITE, no negotiated timers",
			supported: "100rel",
			wantAdved: false,
		},
		{
			name:      "peer engages timer via Supported",
			supported: "timer",
			wantAdved: true,
		},
		{
			name:      "peer engages timer via Session-Expires only",
			se:        "600;refresher=uas",
			wantAdved: true,
		},
		{
			name:      "timers negotiated on a leg, headers omitted in refresh",
			setup:     func(c *Call) { c.AliceSessionTimer = &SessionTimer{Interval: 600 * time.Second, Refresher: "uas"} },
			wantAdved: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bobTP := &captureTransport{}
			h := newTestHandler(t)
			h.uacMgr = sip.NewUACManager()
			call := newReInviteCall(t, bobTP)
			if tc.setup != nil {
				tc.setup(call)
			}

			tx := &mockB2BUATx{}
			req := reInviteRequest(t, call.AliceCallID, tc.supported, tc.se, tc.minSE)
			h.handleReInvite(t.Context(), req, tx, call)

			fwd := bobTP.lastSent()
			if fwd == nil {
				t.Fatal("expected forwarded re-INVITE to be sent to Bob")
			}
			if got := HasOptionTag(fwd, "Supported", "timer"); got != tc.wantAdved {
				t.Errorf("forwarded Supported: timer present = %v, want %v (Supported=%q)",
					got, tc.wantAdved, fwd.Headers.GetFirst("Supported"))
			}
		})
	}
}

// TestReInviteResponseLoop_RelaysTimerHeadersOn200 verifies that the 200 OK
// relayed back to the re-INVITE originator carries the peer's RFC 4028
// negotiation headers.
func TestReInviteResponseLoop_RelaysTimerHeadersOn200(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newReInviteCall(t, bobTP)

	tx := &mockB2BUATx{}
	req := reInviteRequest(t, call.AliceCallID, "timer", "600;refresher=uas", "90")
	h.handleReInvite(t.Context(), req, tx, call)

	fwd := bobTP.lastSent()
	if fwd == nil {
		t.Fatal("expected forwarded re-INVITE to be sent to Bob")
	}
	uac := h.uacMgr.Get(viaBranch(fwd.Headers.GetFirst("Via")))
	if uac == nil {
		t.Fatal("forwarded re-INVITE transaction not registered in UAC manager")
	}

	uac.Responses <- trunk200OK(t, "Supported: timer\r\nSession-Expires: 600;refresher=uas\r\nRequire: timer\r\n")

	relayed := waitForRelayedResponse(t, tx)
	if relayed.StatusCode() != 200 {
		t.Fatalf("relayed status = %d, want 200", relayed.StatusCode())
	}
	if se := relayed.Headers.GetFirst("Session-Expires"); se != "600;refresher=uas" {
		t.Errorf("relayed Session-Expires = %q, want %q", se, "600;refresher=uas")
	}
	if !HasOptionTag(relayed, "Require", "timer") {
		t.Error("relayed 200 OK missing Require: timer (RFC 4028 §5)")
	}
	if !HasOptionTag(relayed, "Supported", "timer") {
		t.Error("relayed 200 OK missing Supported: timer")
	}
}

// TestReInviteResponseLoop_Relays422WithMinSE verifies a peer 422 is relayed
// with its mandatory Min-SE header so the originator can retry (RFC 4028 §5).
func TestReInviteResponseLoop_Relays422WithMinSE(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newReInviteCall(t, bobTP)

	tx := &mockB2BUATx{}
	req := reInviteRequest(t, call.AliceCallID, "timer", "30;refresher=uas", "90")
	h.handleReInvite(t.Context(), req, tx, call)

	fwd := bobTP.lastSent()
	if fwd == nil {
		t.Fatal("expected forwarded re-INVITE to be sent to Bob")
	}
	uac := h.uacMgr.Get(viaBranch(fwd.Headers.GetFirst("Via")))
	if uac == nil {
		t.Fatal("forwarded re-INVITE transaction not registered in UAC manager")
	}

	uac.Responses <- trunk422Response(t, "300")

	relayed := waitForRelayedResponse(t, tx)
	if relayed.StatusCode() != 422 {
		t.Fatalf("relayed status = %d, want 422", relayed.StatusCode())
	}
	if ms := relayed.Headers.GetFirst("Min-SE"); ms != "300" {
		t.Errorf("relayed 422 Min-SE = %q, want 300 (mandatory per RFC 4028 §5)", ms)
	}
}

// waitForRelayedResponse waits for at least one response on the mock
// transaction, which the relay loop sends from its own goroutine.
func waitForRelayedResponse(t *testing.T, tx *mockB2BUATx) *proto.SIPMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rs := tx.snapshot(); len(rs) > 0 {
			return rs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for relayed response")
	return nil
}

// TestReInviteResponseLoop_TimerResetOnlyOnConfirmedRefresh verifies a
// forwarded re-INVITE extends session timers only when it is confirmed with
// a 200 OK (RFC 4028 §7.2). A rejected refresh must leave both legs' timers
// untouched, so the non-refresher can still expire a session that was never
// actually refreshed.
func TestReInviteResponseLoop_TimerResetOnlyOnConfirmedRefresh(t *testing.T) {
	run := func(t *testing.T, feed func(t *testing.T) *proto.SIPMessage, wantReset bool, wantStatus int) {
		t.Helper()
		bobTP := &captureTransport{}
		h := newTestHandler(t)
		h.uacMgr = sip.NewUACManager()
		call := newReInviteCall(t, bobTP)
		// Long intervals: the loops must not fire refreshes on their own.
		call.AliceSessionTimer = &SessionTimer{Interval: 600 * time.Second, MinSE: 90 * time.Second, Refresher: "uas"}
		call.BobSessionTimer = &SessionTimer{Interval: 600 * time.Second, MinSE: 90 * time.Second, Refresher: "uac"}
		h.StartSessionTimer(t.Context(), call, "alice")
		h.StartSessionTimer(t.Context(), call, "bob")
		startA, startB := call.AliceSessionTimer.StartTime, call.BobSessionTimer.StartTime

		tx := &mockB2BUATx{}
		req := reInviteRequest(t, call.AliceCallID, "timer", "600;refresher=uas", "90")
		h.handleReInvite(t.Context(), req, tx, call)

		fwd := bobTP.lastSent()
		if fwd == nil {
			t.Fatal("expected forwarded re-INVITE to be sent to Bob")
		}
		uac := h.uacMgr.Get(viaBranch(fwd.Headers.GetFirst("Via")))
		if uac == nil {
			t.Fatal("forwarded re-INVITE transaction not registered in UAC manager")
		}
		uac.Responses <- feed(t)
		uac.Cancel() // bypasses production's final-delivery timer cleanup

		relayed := waitForRelayedResponse(t, tx)
		if got := relayed.StatusCode(); got != wantStatus {
			t.Fatalf("relayed status = %d, want %d", got, wantStatus)
		}
		// The relay happens after any timer reset in the 200 branch, so the
		// timer fields are stable here (ordered via the mock tx mutex).
		advanced := call.AliceSessionTimer.StartTime.After(startA) || call.BobSessionTimer.StartTime.After(startB)
		if wantReset && !advanced {
			t.Error("timers not reset after confirmed 200 OK refresh")
		}
		if !wantReset && advanced {
			t.Error("timers extended by a re-INVITE that was rejected; failed refresh must not renew the session")
		}
		if !wantReset {
			if got := bobTP.sentCount(); got != 1 {
				t.Errorf("captured %d forwards, want 1", got)
			}
		}
	}

	t.Run("rejected_refresh_does_not_extend_session", func(t *testing.T) {
		run(t, func(t *testing.T) *proto.SIPMessage {
			return trunk422Response(t, "300") // any non-2xx final response behaves alike
		}, false, 422)
	})
	t.Run("accepted_refresh_extends_both_legs", func(t *testing.T) {
		run(t, func(t *testing.T) *proto.SIPMessage {
			return trunk200OK(t, "")
		}, true, 200)
	})
}

// TestSendInDialogACK verifies the 2xx-ACK helper builds a compliant
// in-dialog ACK per RFC 3261 §13.2.2.4: same Call-ID, tags, and CSeq sequence
// number as the INVITE, ACK method, and the dialog's remote target as
// Request-URI.
func TestSendInDialogACK(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.serverIP = "127.0.0.1"
	h.serverPort = "5060"
	call := newReInviteCall(t, bobTP)

	if !h.sendInDialogACK(call, "bob", 7) {
		t.Fatal("sendInDialogACK(bob) returned false")
	}
	ack := bobTP.lastSent()
	if ack == nil {
		t.Fatal("no ACK captured on Bob transport")
	}
	if ack.Method() != proto.SIPMethodACK {
		t.Errorf("method = %s, want ACK", ack.Method())
	}
	if ack.CSeq.Seq != 7 || ack.CSeq.Method != proto.SIPMethodACK {
		t.Errorf("CSeq = %v, want {ACK 7} (must repeat the INVITE's CSeq)", ack.CSeq)
	}
	if got := ack.Headers.GetFirst("Call-ID"); got != "bob-call" {
		t.Errorf("Call-ID = %q, want bob-call", got)
	}
	from := ack.Headers.GetFirst("From")
	to := ack.Headers.GetFirst("To")
	if !strings.Contains(from, "tag=server") || !strings.Contains(to, "tag=bob-remote") {
		t.Errorf("tags wrong: From=%q To=%q (want local=server, remote=bob-remote)", from, to)
	}
	if got := ack.Headers.GetFirst("Via"); !strings.Contains(got, "branch=") {
		t.Errorf("ACK Via missing branch: %q", got)
	}

	// The Alice leg uses its own dialog identity.
	aliceTP := call.AliceTransport.(*captureTransport)
	if !h.sendInDialogACK(call, "alice", 3) {
		t.Fatal("sendInDialogACK(alice) returned false")
	}
	ackA := aliceTP.lastSent()
	if ackA == nil {
		t.Fatal("no ACK captured on Alice transport")
	}
	if ackA.CSeq.Seq != 3 {
		t.Errorf("alice ACK CSeq.Seq = %d, want 3", ackA.CSeq.Seq)
	}
	if got := ackA.Headers.GetFirst("To"); !strings.Contains(got, "tag=alice-remote") {
		t.Errorf("alice ACK To tag = %q, want alice-remote", got)
	}

	// A terminated dialog must not be ACKed.
	call.BobDialog.SetState(sip.DialogStateTerminated)
	if h.sendInDialogACK(call, "bob", 8) {
		t.Error("sendInDialogACK on terminated dialog returned true")
	}
}

// TestReInviteResponseLoop_AcksForwarded2xx verifies the 200 OK for a
// forwarded re-INVITE is ACKed on the forwarded leg before being relayed
// (RFC 3261 §13.2.2.4) — the UAC transaction does not ACK 2xx responses
// itself, and an unACKed 200 OK gets retransmitted until Timer H.
func TestReInviteResponseLoop_AcksForwarded2xx(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newReInviteCall(t, bobTP)

	tx := &mockB2BUATx{}
	req := reInviteRequest(t, call.AliceCallID, "timer", "600;refresher=uas", "90")
	h.handleReInvite(t.Context(), req, tx, call)

	fwd := bobTP.lastSent()
	if fwd == nil {
		t.Fatal("expected forwarded re-INVITE to be sent to Bob")
	}
	uac := h.uacMgr.Get(viaBranch(fwd.Headers.GetFirst("Via")))
	if uac == nil {
		t.Fatal("forwarded re-INVITE transaction not registered in UAC manager")
	}
	ok := trunk200OK(t, "")
	uac.Responses <- ok
	uac.Cancel() // bypasses production's final-delivery timer cleanup

	waitForRelayedResponse(t, tx) // relay confirms the 200 branch ran

	// The ACK must carry the 200 OK's CSeq sequence number.
	var ack *proto.SIPMessage
	for _, m := range bobTP.snapshotAll() {
		if m.Method() == proto.SIPMethodACK {
			ack = m
		}
	}
	if ack == nil {
		t.Fatal("no ACK sent on the forwarded (Bob) leg for the re-INVITE 200 OK")
	}
	if ack.CSeq.Seq != ok.CSeq.Seq || ack.CSeq.Method != proto.SIPMethodACK {
		t.Errorf("ACK CSeq = %v, want {ACK %d} (same seq as the INVITE)", ack.CSeq, ok.CSeq.Seq)
	}
}

// releases the allocated RTP ports: there is no retry mechanism there, so the
// setup is failing and leaving the conns open would leak sockets until exit.
func TestB2BUAResponseLoop_ClosesRTPOn422(t *testing.T) {
	h := newTestHandler(t)
	h.serverIP = "127.0.0.1"
	h.serverPort = "5060"

	tport := &captureTransport{}
	rtpA, err := media.NewRTPConn()
	if err != nil {
		t.Fatalf("NewRTPConn A: %v", err)
	}
	defer rtpA.Close()
	rtpB, err := media.NewRTPConn()
	if err != nil {
		t.Fatalf("NewRTPConn B: %v", err)
	}
	defer rtpB.Close()

	uac := h.uacMgr.NewTransaction(t.Context(), proto.SIPMethodINVITE, tport, &sip.Target{})
	bobInvite := proto.NewRequest(proto.SIPMethodINVITE, "sip:bob@localhost")
	bobInvite.Headers.Add("Call-ID", "bob-call")
	bobInvite.CSeq = proto.CSeq{Method: proto.SIPMethodINVITE, Seq: 1}
	bobInvite.Headers.Add("Via", "SIP/2.0/UDP 127.0.0.1:5060;branch="+uac.Branch)

	tx := &mockB2BUATx{}
	cc := &callCtx{
		req:           proto.NewRequest(proto.SIPMethodINVITE, "sip:alice@localhost"),
		tx:            tx,
		target:        &sip.Target{},
		transportImpl: tport,
		uac:           uac,
		rtpConnA:      rtpA,
		rtpConnB:      rtpB,
		callID:        "alice-call",
		bobCallID:     "bob-call",
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.b2buaResponseLoop(ctx, cc, bobInvite, &sip.Binding{ContactURI: "sip:bob@127.0.0.1:9999"}, false)
	}()

	uac.Responses <- trunk422Response(t, "300")
	uac.Cancel()

	relayed := waitForRelayedResponse(t, tx)
	if relayed.StatusCode() != 422 {
		t.Fatalf("relayed status = %d, want 422", relayed.StatusCode())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("response loop did not return after relaying 422")
	}

	// A closed socket errors immediately even with a far deadline; an open
	// one would block until the deadline expires.
	assertClosed := func(r *media.RTPConn, name string) {
		t.Helper()
		_ = r.SetReadDeadline(time.Now().Add(10 * time.Second))
		start := time.Now()
		if _, _, err := r.ReadRTP(); err == nil {
			t.Errorf("%s: ReadRTP succeeded, want closed", name)
		} else if time.Since(start) > time.Second {
			t.Errorf("%s: still open after 422 relay (blocked %v)", name, time.Since(start))
		}
	}
	assertClosed(rtpA, "rtpConnA")
	assertClosed(rtpB, "rtpConnB")
}

func TestSendBye_CSeqContiguous(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.serverIP = "127.0.0.1"
	h.serverPort = "5060"
	call := newReInviteCall(t, bobTP)

	// Simulate a re-INVITE already having incremented Bob's dialog LocalSeq
	// (initial INVITE=1, re-INVITE=2).
	seq := call.BobDialog.IncrementLocalSeq()
	if seq != 2 {
		t.Fatalf("expected re-INVITE CSeq 2, got %d", seq)
	}

	h.sendBye(call, false, "test")

	bye := bobTP.lastSent()
	if bye == nil {
		t.Fatal("expected BYE to be sent to Bob")
	}
	// BYE must be strictly higher than the last in-dialog request (RFC 3261
	// §15.1.1, §12.2.1.1), so it must be 3, not the hardcoded 2.
	if bye.CSeq.Seq != 3 {
		t.Errorf("BYE CSeq = %d, want 3 (contiguous after re-INVITE)", bye.CSeq.Seq)
	}
}

// trunk422Response builds a 422 response carrying a Min-SE header.
func trunk422Response(t *testing.T, minSE string) *proto.SIPMessage {
	t.Helper()
	raw := "SIP/2.0 422 Session Interval Too Small\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKresp\r\n" +
		"From: <sip:alice@localhost>;tag=a\r\n" +
		"To: <sip:bob@localhost>;tag=b\r\n" +
		"Call-ID: bob-call\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Min-SE: " + minSE + "\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalSIPDatagram: %v", err)
	}
	return msg
}

// trunk422Rig wires a minimal trunk call setup so trunkResponseLoop can be
// driven response-by-response in tests.
type trunk422Rig struct {
	h          *Handler
	tport      *captureTransport
	cc         *callCtx
	tx         *mockB2BUATx
	initialUAC *sip.UACTransaction
	trunkMgr   *trunk.TrunkManager
	cancel     context.CancelFunc
	done       chan struct{}
}

func startTrunk422Rig(t *testing.T) *trunk422Rig {
	t.Helper()
	h := newTestHandler(t)
	h.serverIP = "127.0.0.1"
	h.serverPort = "5060"
	h.minSE = 90 * time.Second

	tm, err := trunk.NewTrunkManager(&trunk.TrunkConfig{
		Trunks: []trunk.Trunk{{Name: "trunk1", Host: "127.0.0.1", Port: 5061, Transport: "udp"}},
		Routes: []trunk.OutboundRoute{{Name: "r", Pattern: ".*", TrunkName: "trunk1"}},
	}, "127.0.0.1", "127.0.0.1:5060")
	if err != nil {
		t.Fatalf("NewTrunkManager: %v", err)
	}
	h.trunkMgr = tm
	if !tm.AcquireChannel("trunk1") {
		t.Fatal("expected to acquire channel on trunk1")
	}

	tport := &captureTransport{}

	rtpA, err := media.NewRTPConn()
	if err != nil {
		t.Fatalf("NewRTPConn A: %v", err)
	}
	t.Cleanup(func() { rtpA.Close() })
	rtpB, err := media.NewRTPConn()
	if err != nil {
		t.Fatalf("NewRTPConn B: %v", err)
	}
	t.Cleanup(func() { rtpB.Close() })

	bobInvite := proto.NewRequest(proto.SIPMethodINVITE, "sip:bob@trunk.invalid")
	bobInvite.Headers.Add("Call-ID", "bob-call")
	bobInvite.CSeq = proto.CSeq{Method: proto.SIPMethodINVITE, Seq: 1}
	bobInvite.Headers.Add("Session-Expires", "1800;refresher=uac")

	uac := h.uacMgr.NewTransaction(t.Context(), proto.SIPMethodINVITE, tport, &sip.Target{})
	bobInvite.Headers.Add("Via", "SIP/2.0/UDP 127.0.0.1:5060;branch="+uac.Branch)

	tx := &mockB2BUATx{}
	cc := &callCtx{
		req:           proto.NewRequest(proto.SIPMethodINVITE, "sip:alice@localhost"),
		tx:            tx,
		target:        &sip.Target{},
		transportImpl: tport,
		uac:           uac,
		rtpConnA:      rtpA,
		rtpConnB:      rtpB,
		from:          &proto.SIPAddress{URI: "sip:alice@localhost", Tag: "a"},
		callID:        "alice-call",
		to:            &proto.SIPAddress{URI: "sip:bob@localhost"},
	}

	ctx, cancel := context.WithCancel(t.Context())
	h.store.StoreEarly(&EarlyCall{
		AliceCallID:    "alice-call",
		BobCallID:      "bob-call",
		AliceServerTag: "server-tag",
		BobTx:          uac,
		RTPConnA:       rtpA,
		RTPConnB:       rtpB,
		Cancel:         cancel,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		// sessionExpires large enough, minSE 90; the peer's 422 raises Min-SE.
		h.trunkResponseLoop(ctx, cc, bobInvite, "trunk1", "sip:bob@trunk.invalid", 1800*time.Second, h.minSE, "127.0.0.1")
	}()

	return &trunk422Rig{h: h, tport: tport, cc: cc, tx: tx, initialUAC: uac, trunkMgr: tm, cancel: cancel, done: done}
}

// feed422 delivers a 422 for the newest transaction (empty minSE simulates a
// missing/unparsable header) and stops its retransmit timers, which
// production does on final-response delivery — bypassed here.
func (r *trunk422Rig) feed422(t *testing.T, minSE string) {
	t.Helper()
	r.waitForSends(t, 1) // the loop sends the initial INVITE on its own goroutine
	last := r.tport.lastSent()
	if last == nil {
		t.Fatal("no INVITE captured yet")
	}
	uac := r.h.uacMgr.Get(viaBranch(last.Headers.GetFirst("Via")))
	if uac == nil {
		t.Fatal("last captured INVITE has no registered transaction")
	}
	uac.Responses <- trunk422Response(t, minSE)
	uac.Cancel()
}

func (r *trunk422Rig) waitForSends(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.tport.sentCount() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d INVITE sends, got %d", n, r.tport.sentCount())
}

func (r *trunk422Rig) waitForRelay(t *testing.T) *proto.SIPMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rs := r.tx.snapshot(); len(rs) > 0 {
			return rs[len(rs)-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for relayed response")
	return nil
}

func TestTrunk422Retry_NewTransactionAndBranchNotMutatingHandlerMinSE(t *testing.T) {
	r := startTrunk422Rig(t)

	// Feed the 422 response into the initial UAC's response channel.
	r.feed422(t, "300")

	// The retry must be sent on a new transaction with a different branch.
	r.waitForSends(t, 2)

	retry := r.tport.lastSent()
	via := retry.Headers.GetFirst("Via")
	if !strings.Contains(via, "branch=") {
		t.Fatalf("retry INVITE Via missing branch: %q", via)
	}
	if strings.Contains(via, r.initialUAC.Branch) {
		t.Errorf("retry INVITE reused the previous transaction branch; want a new branch (RFC 4028 §7.3)")
	}
	// Retry CSeq must be one higher than the previous request.
	if retry.CSeq.Seq != 2 {
		t.Errorf("retry INVITE CSeq = %d, want 2 (one higher, RFC 4028 §7.3)", retry.CSeq.Seq)
	}
	// The retry must carry the peer's (raised) Min-SE and matching Session-Expires.
	if ms := retry.Headers.GetFirst("Min-SE"); ms != "300" {
		t.Errorf("retry INVITE Min-SE = %q, want 300", ms)
	}
	// The handler-wide minSE MUST NOT have been mutated (RFC 4028 §7.4 per-call scope).
	if r.h.minSE != 90*time.Second {
		t.Errorf("handler minSE mutated to %v after 422; want unchanged 90s", r.h.minSE)
	}
	// The stored early call must point at the retry transaction so CANCEL
	// reaches the live INVITE (RFC 3261 §9.1).
	if early := r.h.store.GetEarly(r.cc.callID); early == nil {
		t.Error("expected the early call to still be tracked after the 422 retry")
	} else if early.BobTx == r.initialUAC {
		t.Error("early call BobTx not updated to the retry transaction after 422")
	} else if r.h.uacMgr.Get(viaBranch(retry.Headers.GetFirst("Via"))) != early.BobTx {
		t.Error("retry Via branch not registered to the early call's transaction")
	}

	r.cancel()
	<-r.done
}

// TestTrunk422Retry_UnusableMinSEFailsCall verifies that a 422 carrying no
// usable Min-SE is not retried: resending the identical offer would loop
// until the call context dies. The call must fail and resources release.
func TestTrunk422Retry_UnusableMinSEFailsCall(t *testing.T) {
	r := startTrunk422Rig(t)

	r.feed422(t, "") // empty/unparsable Min-SE → nothing to renegotiate

	relayed := r.waitForRelay(t)
	if relayed.StatusCode() != 488 {
		t.Fatalf("relayed status = %d, want 488 (unretryable 422 fails the call)", relayed.StatusCode())
	}
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("trunk response loop did not return after unretryable 422")
	}
	time.Sleep(100 * time.Millisecond)
	if got := r.tport.sentCount(); got != 1 {
		t.Errorf("captured %d INVITE sends after unretryable 422, want 1 (no retry loop)", got)
	}
	if !r.trunkMgr.AcquireChannel("trunk1") {
		t.Error("trunk channel not released after failing call on unretryable 422")
	}
}

// TestTrunk422Retry_Capped verifies a peer that keeps raising Min-SE cannot
// drive an unbounded retry chain: after max422Retries renegotiations the
// call fails cleanly.
func TestTrunk422Retry_Capped(t *testing.T) {
	r := startTrunk422Rig(t)

	for i, minSE := range []string{"1801", "1802", "1803"} {
		r.feed422(t, minSE)
		r.waitForSends(t, i+2) // retry i+1 goes out
	}

	// The fourth 422 exceeds the cap even though it re-raises Min-SE.
	r.feed422(t, "1804")
	relayed := r.waitForRelay(t)
	if relayed.StatusCode() != 488 {
		t.Fatalf("relayed status = %d, want 488 after %d retries", relayed.StatusCode(), max422Retries)
	}
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("trunk response loop did not return after capped 422 retries")
	}
	time.Sleep(100 * time.Millisecond)
	if got := r.tport.sentCount(); got != 4 {
		t.Errorf("captured %d INVITE sends, want 4 (1 initial + %d capped retries)", got, max422Retries)
	}
}

func TestNewHandler_MinSEValidation(t *testing.T) {
	mkCfg := func(minSE time.Duration) Config {
		return Config{
			ServerIP:       "127.0.0.1",
			ServerAddr:     "127.0.0.1:5060",
			UACManager:     sip.NewUACManager(),
			SessionExpires: 1800 * time.Second,
			MinSE:          minSE,
		}
	}

	// Sub-default Min-SE values must be rejected and fall back to DefaultMinSE
	// (RFC 4028 §5: minimum acceptable value is 90s).
	h := NewHandler(mkCfg(30 * time.Second))
	if h.minSE != DefaultMinSE {
		t.Errorf("sub-default Min-SE: got %v, want %v (DefaultMinSE)", h.minSE, DefaultMinSE)
	}

	// Zero Min-SE also falls back.
	h = NewHandler(mkCfg(0))
	if h.minSE != DefaultMinSE {
		t.Errorf("zero Min-SE: got %v, want %v", h.minSE, DefaultMinSE)
	}

	// A value at or above the default is honored.
	h = NewHandler(mkCfg(300 * time.Second))
	if h.minSE != 300*time.Second {
		t.Errorf("valid Min-SE: got %v, want 300s", h.minSE)
	}
}

// failingCaptureTransport records the message passed to Send but always
// returns an error, simulating an unreachable peer.
type failingCaptureTransport struct {
	captureTransport
}

func (f *failingCaptureTransport) Send(msg *proto.SIPMessage, target *sip.Target) error {
	f.captureTransport.Send(msg, target)
	return errors.New("send failed")
}

// trunk200OK builds a minimal 200 OK response for a trunk call.
func trunk200OK(t *testing.T, headers string) *proto.SIPMessage {
	t.Helper()
	raw := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKresp\r\n" +
		"From: <sip:trec@127.0.0.1>;tag=a\r\n" +
		"To: <sip:bob@localhost>;tag=b\r\n" +
		"Call-ID: bob-call\r\n" +
		"CSeq: 1 INVITE\r\n" +
		headers +
		"Content-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalSIPDatagram: %v", err)
	}
	return msg
}

func TestNegotiateBobSessionTimer(t *testing.T) {
	h := newTestHandler(t)

	const minSE = 300 * time.Second

	tests := []struct {
		name        string
		respHeaders string
		want        *SessionTimer
	}{
		{
			name:        "peer negotiated timer",
			respHeaders: "Supported: timer\r\nSession-Expires: 600;refresher=uas\r\n",
			want:        &SessionTimer{Interval: 600 * time.Second, MinSE: minSE, Refresher: "uas"},
		},
		{
			// RFC 4028 §7.1/§7.3: the UAS engages the timer by copying
			// Session-Expires into the 2xx. A 200 OK without it means no
			// session timer applies, even if we offered one and the peer
			// claims timer support.
			name:        "peer supports timer but omitted Session-Expires",
			respHeaders: "Supported: timer\r\n",
			want:        nil,
		},
		{
			name:        "peer did not negotiate timer",
			respHeaders: "",
			want:        nil,
		},
		{
			name:        "Session-Expires without Supported header still engages",
			respHeaders: "Session-Expires: 900\r\n",
			want:        &SessionTimer{Interval: 900 * time.Second, MinSE: minSE, Refresher: "uac"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := h.negotiateBobSessionTimer(trunk200OK(t, tc.respHeaders), minSE)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil, want %+v", tc.want)
			}
			if got.Interval != tc.want.Interval {
				t.Errorf("Interval = %v, want %v", got.Interval, tc.want.Interval)
			}
			if got.MinSE != tc.want.MinSE {
				t.Errorf("MinSE = %v, want %v (per-call negotiated minimum)", got.MinSE, tc.want.MinSE)
			}
			if got.Refresher != tc.want.Refresher {
				t.Errorf("Refresher = %q, want %q", got.Refresher, tc.want.Refresher)
			}
		})
	}
}

func TestNegotiateAliceSessionTimer(t *testing.T) {
	h := newTestHandler(t) // sessionExpires defaults to DefaultSessionExpires (1800s)

	mkReq := func(headers proto.SIPHeaders) *proto.SIPMessage {
		return &proto.SIPMessage{Headers: headers}
	}

	// Timer support without an explicit Session-Expires must keep the
	// configured default interval and the UAS (server) as refresher —
	// a stale 1800s fallback from the parser used to override both.
	st := h.negotiateAliceSessionTimer(mkReq(proto.SIPHeaders{
		"Supported": []string{"timer"},
	}))
	if st == nil {
		t.Fatal("expected a session timer when Alice supports timer")
	}
	if st.Interval != DefaultSessionExpires || st.Refresher != "uas" {
		t.Errorf("absent SE: got (%v, %q), want (%v, \"uas\")", st.Interval, st.Refresher, DefaultSessionExpires)
	}

	h2 := NewHandler(Config{
		ServerIP:       "127.0.0.1",
		ServerAddr:     "127.0.0.1:5060",
		UACManager:     sip.NewUACManager(),
		SessionExpires: 600 * time.Second,
	})
	st = h2.negotiateAliceSessionTimer(mkReq(proto.SIPHeaders{
		"Supported": []string{"timer"},
	}))
	if st == nil {
		t.Fatal("expected a session timer")
	}
	if st.Interval != 600*time.Second || st.Refresher != "uas" {
		t.Errorf("configured default: got (%v, %q), want (600s, \"uas\")", st.Interval, st.Refresher)
	}

	// Alice's offered interval and uac preference are honored.
	st = h2.negotiateAliceSessionTimer(mkReq(proto.SIPHeaders{
		"Supported":       []string{"timer"},
		"Session-Expires": []string{"300;refresher=uac"},
	}))
	if st == nil || st.Interval != 300*time.Second || st.Refresher != "uac" {
		t.Errorf("offered 300/uac: got %+v, want interval 300s refresher uac", st)
	}

	// No timer support advertised → no timer.
	if st := h2.negotiateAliceSessionTimer(mkReq(proto.SIPHeaders{})); st != nil {
		t.Errorf("no timer support: got %+v, want nil", st)
	}

	// Engagement must mirror the 422 gate in HandleInvite: a bare
	// Session-Expires offer engages timers even without Supported: timer
	// (RFC 4028 §4/§7.2), and its default refresher is "uac".
	st = h2.negotiateAliceSessionTimer(mkReq(proto.SIPHeaders{
		"Session-Expires": []string{"300"},
	}))
	if st == nil || st.Interval != 300*time.Second || st.Refresher != "uac" {
		t.Errorf("SE-only offer: got %+v, want interval 300s refresher uac", st)
	}
	if st := h2.negotiateAliceSessionTimer(mkReq(proto.SIPHeaders{
		"Require": []string{"timer"},
	})); st == nil {
		t.Error("Require: timer did not engage session timers")
	}

	// Globally disabled → nil even with a fully-featured offer.
	hOff := NewHandler(Config{
		ServerIP:             "127.0.0.1",
		ServerAddr:           "127.0.0.1:5060",
		UACManager:           sip.NewUACManager(),
		SessionTimerDisabled: true,
	})
	if hOff.sessionExpires != 0 {
		t.Errorf("disabled handler: sessionExpires = %v, want 0", hOff.sessionExpires)
	}
	if st := hOff.negotiateAliceSessionTimer(mkReq(proto.SIPHeaders{
		"Supported":       []string{"timer"},
		"Session-Expires": []string{"600;refresher=uac"},
	})); st != nil {
		t.Errorf("disabled: got %+v, want nil", st)
	}
}

func TestHandleReInvite_UACCancelledOnSendFailure(t *testing.T) {
	bobTP := &failingCaptureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newReInviteCall(t, bobTP)

	tx := &mockB2BUATx{}
	req := reInviteRequest(t, call.AliceCallID, "timer", "900;refresher=uas", "90")
	h.handleReInvite(t.Context(), req, tx, call)

	resp := tx.snapshot()
	if len(resp) != 1 || resp[0].StatusCode() != 502 {
		t.Fatalf("expected a single 502 response, got %+v", resp)
	}

	sent := bobTP.lastSent()
	if sent == nil {
		t.Fatal("expected the re-INVITE to reach the transport")
	}
	branch := viaBranch(sent.Headers.GetFirst("Via"))
	if branch == "" {
		t.Fatal("forwarded re-INVITE Via missing branch")
	}
	// The failed UAC transaction must be deregistered so it cannot leak in
	// the manager's pending map.
	if h.uacMgr.Get(branch) != nil {
		t.Error("UAC transaction not canceled after send failure; branch still registered")
	}
}
