package sip

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/thorsager/trecs/proto"
)

func TestSendReliableAddsHeaders(t *testing.T) {
	mgr := NewReliableProvisionalManager()
	tx := &mockReliableTx{}
	req := testRequest(t, proto.SIPMethodINVITE, "z9hG4bKtest", false)
	res := proto.NewResponse(req, 180, "Ringing")

	mgr.SendReliable(context.Background(), tx, res, "call-1", nil)

	rseq := res.Headers.GetFirst("RSeq")
	if rseq == "" {
		t.Fatal("expected RSeq header")
	}
	if rseq != "1" {
		t.Fatalf("expected RSeq=1, got %s", rseq)
	}

	require := res.Headers.GetFirst("Require")
	if require != "100rel" {
		t.Fatalf("expected Require=100rel, got %s", require)
	}
}

func TestSendReliableIncrementsRSeq(t *testing.T) {
	mgr := NewReliableProvisionalManager()
	tx := &mockReliableTx{}
	req := testRequest(t, proto.SIPMethodINVITE, "z9hG4bKtest", false)

	res1 := proto.NewResponse(req, 180, "Ringing")
	mgr.SendReliable(context.Background(), tx, res1, "call-1", nil)
	if rseq := res1.Headers.GetFirst("RSeq"); rseq != "1" {
		t.Fatalf("first SendReliable: expected RSeq=1, got %s", rseq)
	}

	res2 := proto.NewResponse(req, 183, "Session Progress")
	mgr.SendReliable(context.Background(), tx, res2, "call-1", nil)
	if rseq := res2.Headers.GetFirst("RSeq"); rseq != "2" {
		t.Fatalf("second SendReliable: expected RSeq=2, got %s", rseq)
	}
}

func TestSendReliableDifferentCallIDs(t *testing.T) {
	mgr := NewReliableProvisionalManager()
	tx := &mockReliableTx{}
	req := testRequest(t, proto.SIPMethodINVITE, "z9hG4bKtest", false)

	res1 := proto.NewResponse(req, 180, "Ringing")
	mgr.SendReliable(context.Background(), tx, res1, "call-A", nil)
	if rseq := res1.Headers.GetFirst("RSeq"); rseq != "1" {
		t.Fatalf("call-A: expected RSeq=1, got %s", rseq)
	}

	res2 := proto.NewResponse(req, 180, "Ringing")
	mgr.SendReliable(context.Background(), tx, res2, "call-B", nil)
	if rseq := res2.Headers.GetFirst("RSeq"); rseq != "1" {
		t.Fatalf("call-B: expected RSeq=1, got %s", rseq)
	}
}

func TestHandlePRACKWithMatch(t *testing.T) {
	mgr := NewReliableProvisionalManager()
	tx := &mockReliableTx{}
	req := testRequest(t, proto.SIPMethodINVITE, "z9hG4bKtest", false)
	res := proto.NewResponse(req, 180, "Ringing")

	mgr.SendReliable(context.Background(), tx, res, "call-1", nil)
	rseq := res.Headers.GetFirst("RSeq")

	prack := buildPRACK(t, proto.SIPMethodPRACK, "z9hG4bKprack", "call-1", rseq, "1", "INVITE")
	prackTx := &mockReliableTx{}

	mgr.HandlePRACK(context.Background(), prack, prackTx)

	if len(prackTx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(prackTx.responses))
	}
	if sc := prackTx.responses[0].StatusCode(); sc != 200 {
		t.Fatalf("expected 200 OK, got %d", sc)
	}
}

func TestHandlePRACKNoMatch(t *testing.T) {
	mgr := NewReliableProvisionalManager()

	prack := buildPRACK(t, proto.SIPMethodPRACK, "z9hG4bKprack", "unknown-call", "1", "1", "INVITE")
	prackTx := &mockReliableTx{}

	mgr.HandlePRACK(context.Background(), prack, prackTx)

	if len(prackTx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(prackTx.responses))
	}
	if sc := prackTx.responses[0].StatusCode(); sc != 481 {
		t.Fatalf("expected 481, got %d", sc)
	}
}

func TestHandlePRACKWrongRSeq(t *testing.T) {
	mgr := NewReliableProvisionalManager()
	tx := &mockReliableTx{}
	req := testRequest(t, proto.SIPMethodINVITE, "z9hG4bKtest", false)
	res := proto.NewResponse(req, 180, "Ringing")

	mgr.SendReliable(context.Background(), tx, res, "call-1", nil)

	prack := buildPRACK(t, proto.SIPMethodPRACK, "z9hG4bKprack", "call-1", "999", "1", "INVITE")
	prackTx := &mockReliableTx{}

	mgr.HandlePRACK(context.Background(), prack, prackTx)

	if len(prackTx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(prackTx.responses))
	}
	if sc := prackTx.responses[0].StatusCode(); sc != 481 {
		t.Fatalf("expected 481, got %d", sc)
	}
}

func TestHandlePRACKBadRAck(t *testing.T) {
	mgr := NewReliableProvisionalManager()
	prack := testRequest(t, proto.SIPMethodPRACK, "z9hG4bKprack", false)
	prack.Headers.Set("Call-ID", []string{"call-1"})
	prack.Headers.Set("RAck", []string{"not-a-number"})
	prackTx := &mockReliableTx{}

	mgr.HandlePRACK(context.Background(), prack, prackTx)

	if len(prackTx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(prackTx.responses))
	}
	if sc := prackTx.responses[0].StatusCode(); sc != 400 {
		t.Fatalf("expected 400, got %d", sc)
	}
}

func TestHandlePRACKMissingRAck(t *testing.T) {
	mgr := NewReliableProvisionalManager()
	prack := testRequest(t, proto.SIPMethodPRACK, "z9hG4bKprack", false)
	prack.Headers.Set("Call-ID", []string{"call-1"})
	prackTx := &mockReliableTx{}

	mgr.HandlePRACK(context.Background(), prack, prackTx)

	if len(prackTx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(prackTx.responses))
	}
	if sc := prackTx.responses[0].StatusCode(); sc != 400 {
		t.Fatalf("expected 400, got %d", sc)
	}
}

func TestCancel(t *testing.T) {
	mgr := NewReliableProvisionalManager()
	tx := &mockReliableTx{}
	req := testRequest(t, proto.SIPMethodINVITE, "z9hG4bKtest", false)
	res := proto.NewResponse(req, 180, "Ringing")

	mgr.SendReliable(context.Background(), tx, res, "call-1", nil)

	mgr.Cancel("call-1")

	prack := buildPRACK(t, proto.SIPMethodPRACK, "z9hG4bKprack", "call-1", "1", "1", "INVITE")
	prackTx := &mockReliableTx{}
	mgr.HandlePRACK(context.Background(), prack, prackTx)

	if len(prackTx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(prackTx.responses))
	}
	if sc := prackTx.responses[0].StatusCode(); sc != 481 {
		t.Fatalf("expected 481 after Cancel, got %d", sc)
	}
}

func TestPRACKTimeout(t *testing.T) {
	savedT1 := T1
	T1 = time.Millisecond
	defer func() { T1 = savedT1 }()

	timeoutCalled := make(chan struct{}, 1)
	mgr := NewReliableProvisionalManager()

	req := testRequest(t, proto.SIPMethodINVITE, "z9hG4bKtest", false)
	res := proto.NewResponse(req, 180, "Ringing")

	mockTrans := &mockTransport{}
	tx := &mockReliableTx{transport: mockTrans, target: Target{Addr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999}}}

	mgr.SendReliable(context.Background(), tx, res, "call-1", func() {
		timeoutCalled <- struct{}{}
	})

	select {
	case <-timeoutCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout callback not called within 5s")
	}

	mgr.mu.Lock()
	_, exists := mgr.pending["call-1"]
	mgr.mu.Unlock()
	if exists {
		t.Fatal("pending entry should have been removed after timeout")
	}
}

// --- helpers ---

// mockReliableTx implements Transaction for reliable provisional tests.
type mockReliableTx struct {
	responses []*proto.SIPMessage
	transport Transport
	target    Target
	mu        sync.Mutex
}

func (tx *mockReliableTx) Respond(res *proto.SIPMessage) {
	tx.mu.Lock()
	tx.responses = append(tx.responses, res)
	tx.mu.Unlock()
}

func (tx *mockReliableTx) Target() Target {
	return tx.target
}

func (tx *mockReliableTx) Transport() Transport {
	return tx.transport
}

func buildPRACK(t testing.TB, method proto.SIPMethod, branch, callID, rseq, cseq, ackMethod string) *proto.SIPMessage {
	t.Helper()
	msg := testRequest(t, method, branch, false)
	msg.Headers.Set("Call-ID", []string{callID})
	msg.Headers.Set("RAck", []string{rseq + " " + cseq + " " + ackMethod})
	return msg
}

func init() {
	// Ensure T1 has a reasonable value for tests that don't override it.
	if T1 == 0 {
		T1 = 500 * time.Millisecond
	}
}
