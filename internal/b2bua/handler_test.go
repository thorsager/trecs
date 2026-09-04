package b2bua

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/internal/trunk"
	"github.com/thorsager/trecs/proto"
)

type mockB2BUATx struct {
	responses []*proto.SIPMessage
}

func (m *mockB2BUATx) Respond(res *proto.SIPMessage) {
	m.responses = append(m.responses, res)
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

// reInviteRequest builds a minimal in-dialog re-INVITE for testing.
func reInviteRequest(t *testing.T, callID string, se, minSE string) *proto.SIPMessage {
	t.Helper()
	raw := "INVITE sip:bob@localhost SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKreinvite-test\r\n" +
		"From: <sip:alice@localhost>;tag=alice-tag\r\n" +
		"To: <sip:bob@localhost>;tag=bob-tag\r\n" +
		"Call-ID: " + callID + "\r\n" +
		"CSeq: 2 INVITE\r\n" +
		"Max-Forwards: 70\r\n" +
		"Supported: timer\r\n"
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
	req := reInviteRequest(t, call.AliceCallID, "900;refresher=uas", "90")
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

func TestHandleReInvite_ForwardsSessionHeaders(t *testing.T) {
	bobTP := &captureTransport{}
	h := newTestHandler(t)
	h.uacMgr = sip.NewUACManager()
	call := newReInviteCall(t, bobTP)

	tx := &mockB2BUATx{}
	req := reInviteRequest(t, call.AliceCallID, "600;refresher=uas", "120")
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

func TestTrunk422Retry_NewTransactionAndBranchNotMutatingHandlerMinSE(t *testing.T) {
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
	defer rtpA.Close()
	rtpB, err := media.NewRTPConn()
	if err != nil {
		t.Fatalf("NewRTPConn B: %v", err)
	}
	defer rtpB.Close()

	bobInvite := proto.NewRequest(proto.SIPMethodINVITE, "sip:bob@trunk.invalid")
	bobInvite.Headers.Add("Call-ID", "bob-call")
	bobInvite.CSeq = proto.CSeq{Method: proto.SIPMethodINVITE, Seq: 1}
	bobInvite.Headers.Add("Session-Expires", "1800;refresher=uac")

	uac := h.uacMgr.NewTransaction(t.Context(), proto.SIPMethodINVITE, tport, &sip.Target{})
	bobInvite.Headers.Add("Via", "SIP/2.0/UDP 127.0.0.1:5060;branch="+uac.Branch)

	cc := &callCtx{
		req:           proto.NewRequest(proto.SIPMethodINVITE, "sip:alice@localhost"),
		tx:            &mockB2BUATx{},
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
	defer cancel()

	// Track the pending call so the CANCEL path can be exercised: the stored
	// EarlyCall must follow the retry onto the new transaction.
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
		// sessionExpires large enough, minSE 90; the peer's 422 raises Min-SE to 300.
		h.trunkResponseLoop(ctx, cc, bobInvite, "trunk1", "sip:bob@trunk.invalid", 1800*time.Second, h.minSE, "127.0.0.1")
	}()

	// Feed the 422 response into the initial UAC's response channel.
	cc.uac.Responses <- trunk422Response(t, "300")

	// The retry must be sent on a new transaction with a different branch.
	deadline := time.After(2 * time.Second)
	for {
		sent := tport.sentCount()
		if sent >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for 422 retry INVITE to be sent")
		case <-time.After(10 * time.Millisecond):
		}
	}

	retry := tport.lastSent()
	via := retry.Headers.GetFirst("Via")
	if !strings.Contains(via, "branch=") {
		t.Fatalf("retry INVITE Via missing branch: %q", via)
	}
	if strings.Contains(via, uac.Branch) {
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
	if h.minSE != 90*time.Second {
		t.Errorf("handler minSE mutated to %v after 422; want unchanged 90s", h.minSE)
	}
	// The stored early call must point at the retry transaction so CANCEL
	// reaches the live INVITE (RFC 3261 §9.1).
	if early := h.store.GetEarly(cc.callID); early == nil {
		t.Error("expected the early call to still be tracked after the 422 retry")
	} else if early.BobTx == uac {
		t.Error("early call BobTx not updated to the retry transaction after 422")
	} else if h.uacMgr.Get(viaBranch(retry.Headers.GetFirst("Via"))) != early.BobTx {
		t.Error("retry Via branch not registered to the early call's transaction")
	}

	cancel()
	<-done
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

	const interval = 1800 * time.Second
	const minSE = 300 * time.Second

	tests := []struct {
		name           string
		respHeaders    string
		sessionExpires time.Duration
		want           *SessionTimer
	}{
		{
			name:           "peer negotiated timer",
			respHeaders:    "Supported: timer\r\nSession-Expires: 600;refresher=uas\r\n",
			sessionExpires: interval,
			want:           &SessionTimer{Interval: 600 * time.Second, MinSE: minSE, Refresher: "uas"},
		},
		{
			name:           "peer supports timer but omitted Session-Expires",
			respHeaders:    "Supported: timer\r\n",
			sessionExpires: 600 * time.Second,
			want:           &SessionTimer{Interval: 600 * time.Second, MinSE: minSE, Refresher: "uac"},
		},
		{
			name:           "peer did not negotiate timer",
			respHeaders:    "",
			sessionExpires: interval,
			want:           &SessionTimer{Interval: interval, MinSE: minSE, Refresher: "uac"},
		},
		{
			name:           "no interval configured",
			respHeaders:    "",
			sessionExpires: 0,
			want:           nil,
		},
		{
			name:           "timers disabled with peer claiming timer support",
			respHeaders:    "Supported: timer\r\n",
			sessionExpires: 0,
			want:           nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := h.negotiateBobSessionTimer(trunk200OK(t, tc.respHeaders), tc.sessionExpires, minSE)
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
	req := reInviteRequest(t, call.AliceCallID, "900;refresher=uas", "90")
	h.handleReInvite(t.Context(), req, tx, call)

	if len(tx.responses) != 1 || tx.responses[0].StatusCode() != 502 {
		t.Fatalf("expected a single 502 response, got %+v", tx.responses)
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
