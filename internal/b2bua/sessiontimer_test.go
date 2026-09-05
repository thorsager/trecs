package b2bua

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

// captureTransport records sent messages for verification.
type captureTransport struct {
	sent []*proto.SIPMessage
	mu   sync.Mutex
}

func (c *captureTransport) Send(msg *proto.SIPMessage, target *sip.Target) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	return nil
}

func (c *captureTransport) Receive() <-chan sip.MessageEvent { return nil }
func (c *captureTransport) Close() error                     { return nil }

func (c *captureTransport) lastSent() *proto.SIPMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		return nil
	}
	return c.sent[len(c.sent)-1]
}

func (c *captureTransport) sentCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

// distinctBranches counts transactions (unique Via branches) seen, ignoring
// retransmissions of the same request.
func (c *captureTransport) distinctBranches() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]struct{}{}
	for _, m := range c.sent {
		seen[viaBranch(m.Headers.GetFirst("Via"))] = struct{}{}
	}
	return len(seen)
}

// newTestHandler returns a handler with a UAC manager, suitable for
// exercising outbound request construction.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	return NewHandler(Config{
		ServerIP:   "127.0.0.1",
		ServerAddr: "127.0.0.1:5060",
		UACManager: sip.NewUACManager(),
	})
}

func TestParseSessionExpires(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantSE  time.Duration
		wantRef string
	}{
		{
			name:    "empty header",
			header:  "",
			wantSE:  0,
			wantRef: "uac",
		},
		{
			name:    "seconds only",
			header:  "1800",
			wantSE:  1800 * time.Second,
			wantRef: "uac",
		},
		{
			name:    "with refresher uac",
			header:  "3600;refresher=uac",
			wantSE:  3600 * time.Second,
			wantRef: "uac",
		},
		{
			name:    "with refresher uas",
			header:  "900;refresher=uas",
			wantSE:  900 * time.Second,
			wantRef: "uas",
		},
		{
			name:    "with refresher and extra params",
			header:  "1800;refresher=uac;other=value",
			wantSE:  1800 * time.Second,
			wantRef: "uac",
		},
		{
			name:    "invalid number",
			header:  "invalid",
			wantSE:  0,
			wantRef: "uac",
		},
		{
			name:    "uppercase refresher param",
			header:  "1800;REFRESHER=uas",
			wantSE:  1800 * time.Second,
			wantRef: "uas",
		},
		{
			name:    "mixed-case refresher with spaces",
			header:  "900; Refresher = Uac ",
			wantSE:  900 * time.Second,
			wantRef: "uac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSE, gotRef := ParseSessionExpires(tt.header)
			if gotSE != tt.wantSE {
				t.Errorf("ParseSessionExpires(%q) interval = %v, want %v", tt.header, gotSE, tt.wantSE)
			}
			if gotRef != tt.wantRef {
				t.Errorf("ParseSessionExpires(%q) refresher = %q, want %q", tt.header, gotRef, tt.wantRef)
			}
		})
	}
}

func TestParseMinSE(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty header", "", 0},
		{"valid seconds", "90", 90 * time.Second},
		{"large value", "3600", 3600 * time.Second},
		{"with generic param", "90;foo=bar", 90 * time.Second},
		{"invalid", "abc", 0},
		{"invalid with param", "abc;foo=bar", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMinSE(tt.header)
			if got != tt.want {
				t.Errorf("ParseMinSE(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestHasTimerSupport(t *testing.T) {
	tests := []struct {
		name string
		msg  *proto.SIPMessage
		want bool
	}{
		{
			name: "no Supported header",
			msg:  &proto.SIPMessage{Headers: make(proto.SIPHeaders)},
			want: false,
		},
		{
			name: "Supported without timer",
			msg: &proto.SIPMessage{Headers: proto.SIPHeaders{
				"Supported": []string{"100rel"},
			}},
			want: false,
		},
		{
			name: "Supported with timer",
			msg: &proto.SIPMessage{Headers: proto.SIPHeaders{
				"Supported": []string{"timer"},
			}},
			want: true,
		},
		{
			name: "Supported with timer and other tags",
			msg: &proto.SIPMessage{Headers: proto.SIPHeaders{
				"Supported": []string{"100rel, timer"},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasTimerSupport(tt.msg)
			if got != tt.want {
				t.Errorf("HasTimerSupport() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatSessionExpires(t *testing.T) {
	got := FormatSessionExpires(1800, "uac")
	want := "1800;refresher=uac"
	if got != want {
		t.Errorf("FormatSessionExpires(1800, uac) = %q, want %q", got, want)
	}
}

func TestFormatMinSE(t *testing.T) {
	got := FormatMinSE(90)
	want := "90"
	if got != want {
		t.Errorf("FormatMinSE(90) = %q, want %q", got, want)
	}
}

func TestDurationToSeconds(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want int
	}{
		{0, 0},
		{90 * time.Second, 90},
		{1800 * time.Second, 1800},
		{90500 * time.Millisecond, 91}, // rounds up
	}

	for _, tt := range tests {
		got := DurationToSeconds(tt.d)
		if got != tt.want {
			t.Errorf("DurationToSeconds(%v) = %d, want %d", tt.d, got, tt.want)
		}
	}
}

func TestSendSessionRefresh_HeadersAndBranch(t *testing.T) {
	tport := &captureTransport{}
	h := newTestHandler(t)

	dlg := sip.NewDialog(
		sip.DialogID{CallID: "alice-call", LocalTag: "local", RemoteTag: "remote"},
		"sip:trec@127.0.0.1:5060", "sip:alice@localhost", "sip:alice@localhost:9999",
	)

	call := &Call{
		AliceCallID:     "alice-call",
		AliceDialog:     dlg,
		AliceTransport:  tport,
		AliceContactURI: "sip:alice@localhost:9999",
		AliceTarget:     &sip.Target{},
		AliceSessionTimer: &SessionTimer{
			Interval:  900 * time.Second,
			MinSE:     120 * time.Second,
			Refresher: "uas",
		},
	}

	uac, err := h.sendSessionRefresh(t.Context(), call, "alice", call.AliceSessionTimer)
	if err != nil {
		t.Fatalf("sendSessionRefresh: %v", err)
	}
	// No response arrives in this test; tear the transaction down so its
	// retransmit timers do not keep firing.
	defer uac.Cancel()

	req := tport.lastSent()
	if req == nil {
		t.Fatal("expected a refresh re-INVITE to be sent")
	}

	// The Via branch must match the UAC transaction's registered branch so the
	// response can be routed back (RFC 3261 §17.1.3).
	via := req.Headers.GetFirst("Via")
	if !strings.Contains(via, ";branch=") {
		t.Fatalf("Via header missing branch: %q", via)
	}
	// A re-INVITE should carry Session-Expires = max(Min-SE, interval) and Min-SE
	// (RFC 4028 §7.4).
	if se := req.Headers.GetFirst("Session-Expires"); se == "" {
		t.Error("refresh re-INVITE missing Session-Expires header (RFC 4028 §7.4)")
	}
	if ms := req.Headers.GetFirst("Min-SE"); ms == "" {
		t.Error("refresh re-INVITE missing Min-SE header (RFC 4028 §7.4)")
	} else if ms != "120" {
		t.Errorf("Min-SE = %q, want 120", ms)
	}
	// CSeq must be 2 after the initial INVITE used 1 (RFC 3261 §12.2.1.1).
	if req.CSeq.Seq != 2 {
		t.Errorf("refresh re-INVITE CSeq = %d, want 2 (contiguous after initial INVITE)", req.CSeq.Seq)
	}
}

// TestRefresherTimerLoop_NoOverlappingRefreshes verifies the refresher keeps
// exactly one refresh transaction in flight: no second re-INVITE is sent
// while the first is unanswered (RFC 3261 §12.2.1 glare), and the refresh
// cadence resumes after the peer accepts.
func TestRefresherTimerLoop_NoOverlappingRefreshes(t *testing.T) {
	tport := &captureTransport{}
	h := newTestHandler(t)

	dlg := sip.NewDialog(
		sip.DialogID{CallID: "alice-call", LocalTag: "local", RemoteTag: "remote"},
		"sip:trec@127.0.0.1:5060", "sip:alice@localhost", "sip:alice@localhost:9999",
	)
	call := &Call{
		AliceCallID:     "alice-call",
		AliceDialog:     dlg,
		AliceTransport:  tport,
		AliceContactURI: "sip:alice@localhost:9999",
		AliceTarget:     &sip.Target{},
		AliceSessionTimer: &SessionTimer{
			Interval:  time.Second,
			MinSE:     90 * time.Second,
			Refresher: "uas",
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h.StartSessionTimer(ctx, call, "alice")

	waitForRefreshes := func(n int) *proto.SIPMessage {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if tport.distinctBranches() >= n {
				return tport.lastSent()
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d refresh transactions, got %d (%d sends)",
			n, tport.distinctBranches(), tport.sentCount())
		return nil
	}

	first := waitForRefreshes(1)

	// The peer stays silent. The next refresh must not fire while the first
	// transaction is still in flight: at 1.2x the half-interval after the
	// first refresh, the pre-fix loop would already have sent a second
	// INVITE (the first transaction's own deadline is a full interval away).
	// Count Via branches, not sends: Timer A retransmits the first request.
	time.Sleep(600 * time.Millisecond)
	if got := tport.distinctBranches(); got != 1 {
		t.Fatalf("started %d refresh transactions while the first is in flight, want 1 (RFC 3261 §12.2.1)", got)
	}

	// Accept the refresh: waitForRefreshResponse resets the timer (new loop
	// generation), so the next refresh resumes at half an interval later.
	uac := h.uacMgr.Get(viaBranch(first.Headers.GetFirst("Via")))
	if uac == nil {
		t.Fatal("refresh transaction not registered in UAC manager")
	}
	uac.Responses <- trunk200OK(t, "")
	uac.Cancel() // production stops retransmit timers on final delivery; test bypasses that path

	waitForRefreshes(2)

	// Canceling the leg context stops the loop and the in-flight refresh.
	cancel()
	time.Sleep(700 * time.Millisecond)
	if got := tport.distinctBranches(); got != 2 {
		t.Errorf("started %d refresh transactions after context cancel, want 2 (loop stopped)", got)
	}
}

// TestResetSessionTimer_KeepsParentContextLifecycle verifies that a timer
// restarted via ResetSessionTimer stays attached to the context it was
// originally started with: it keeps refreshing, and it stops when that
// context is canceled (no orphaned refreshes on context.Background()).
func TestResetSessionTimer_KeepsParentContextLifecycle(t *testing.T) {
	tport := &captureTransport{}
	h := newTestHandler(t)

	dlg := sip.NewDialog(
		sip.DialogID{CallID: "alice-call", LocalTag: "local", RemoteTag: "remote"},
		"sip:trec@127.0.0.1:5060", "sip:alice@localhost", "sip:alice@localhost:9999",
	)

	call := &Call{
		AliceCallID:     "alice-call",
		AliceDialog:     dlg,
		AliceTransport:  tport,
		AliceContactURI: "sip:alice@localhost:9999",
		AliceTarget:     &sip.Target{},
		AliceSessionTimer: &SessionTimer{
			Interval:  200 * time.Millisecond,
			MinSE:     90 * time.Second,
			Refresher: "uas",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h.StartSessionTimer(ctx, call, "alice")
	h.ResetSessionTimer(call, "alice")

	// The refresher loop fires at half the interval — expect a refresh
	// re-INVITE to be sent even after the reset.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tport.sentCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if tport.sentCount() == 0 {
		t.Fatal("no refresh re-INVITE sent after timer reset")
	}

	// Canceling the original parent context must stop the restarted timer.
	cancel()
	time.Sleep(50 * time.Millisecond) // let any in-flight loop iteration settle
	baseline := tport.sentCount()
	time.Sleep(600 * time.Millisecond) // several intervals' worth
	if got := tport.sentCount(); got != baseline {
		t.Errorf("timer kept refreshing after parent ctx cancel: %d sends (baseline %d)", got, baseline)
	}
}
