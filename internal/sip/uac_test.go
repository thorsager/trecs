package sip

import (
	"context"
	"testing"
	"time"

	"gitub.com/thorsager/trec/proto"
)

func TestUAC_CallingToProceeding(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = true

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	sc := mock.sentCount()
	if sc != 1 {
		t.Fatalf("expected 1 sent, got %d", sc)
	}

	// 100 Trying
	uac.HandleResponse(proto.NewResponse(req, 100, "Trying"))
	if uac.state != UACStateProceeding {
		t.Fatalf("expected Proceeding, got %s", uac.state)
	}

	resp := <-uac.Responses
	if resp.StatusCode() != 100 {
		t.Fatalf("expected 100, got %d", resp.StatusCode())
	}
}

func TestUAC_CallingToCompleted2xx(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = true

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	uac.HandleResponse(proto.NewResponse(req, 200, "OK"))
	if uac.state != UACStateCompleted {
		t.Fatalf("expected Completed, got %s", uac.state)
	}

	resp := <-uac.Responses
	if resp.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode())
	}
}

func TestUAC_CallingToCompleted3xx(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = true

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	uac.HandleResponse(proto.NewResponse(req, 486, "Busy Here"))
	if uac.state != UACStateCompleted {
		t.Fatalf("expected Completed, got %s", uac.state)
	}

	resp := <-uac.Responses
	if resp.StatusCode() != 486 {
		t.Fatalf("expected 486, got %d", resp.StatusCode())
	}
}

func TestUAC_ProceedingToCompleted2xx(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = true

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	uac.HandleResponse(proto.NewResponse(req, 180, "Ringing"))
	if uac.state != UACStateProceeding {
		t.Fatalf("expected Proceeding, got %s", uac.state)
	}
	<-uac.Responses // drain 180

	uac.HandleResponse(proto.NewResponse(req, 200, "OK"))
	if uac.state != UACStateCompleted {
		t.Fatalf("expected Completed, got %s", uac.state)
	}

	resp := <-uac.Responses
	if resp.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode())
	}
}

func TestUAC_TerminatedIgnoresResponses(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = true

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	uac.Cancel()
	if uac.state != UACStateTerminated {
		t.Fatalf("expected Terminated, got %s", uac.state)
	}

	// Should not panic or change state
	uac.HandleResponse(proto.NewResponse(req, 200, "OK"))
	if uac.state != UACStateTerminated {
		t.Fatalf("expected Terminated after cancel, got %s", uac.state)
	}
}

func TestUAC_RetransmitOnUDP(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = false

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for first retransmit (Timer A = T1)
	time.Sleep(T1 * 110 / 100)

	uac.stateMu.Lock()
	retx := uac.retxCount
	uac.stateMu.Unlock()

	if retx < 1 {
		t.Fatalf("expected at least 1 retransmit, got %d", retx)
	}

	// Stop retransmit with provisional response
	uac.HandleResponse(proto.NewResponse(req, 180, "Ringing"))
	time.Sleep(T1 * 110 / 100)

	uac.stateMu.Lock()
	countAfter := uac.retxCount
	uac.stateMu.Unlock()

	if countAfter != retx {
		t.Fatalf("retransmit count increased after 1xx: was %d, now %d", retx, countAfter)
	}

	uac.Cancel()
}

func TestUAC_NoRetransmitOnTCP(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = true

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	time.Sleep(T1 * 11 / 10)

	if mock.sentCount() != 1 {
		t.Fatalf("expected only initial send on TCP, got %d sends", mock.sentCount())
	}

	uac.Cancel()
}

func TestUAC_TimerBTimeout(t *testing.T) {
	oldT1 := T1
	T1 = 10 * time.Millisecond
	defer func() { T1 = oldT1 }()

	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = true

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case err := <-uac.Errors:
		if _, ok := err.(TimeoutError); !ok {
			t.Fatalf("expected TimeoutError, got %T: %v", err, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected Timer B timeout within 2s")
	}

	if uac.state != UACStateTerminated {
		t.Fatalf("expected Terminated after Timer B, got %s", uac.state)
	}
}

func TestUAC_TimerDCompleted(t *testing.T) {
	oldT1 := T1
	T1 = 10 * time.Millisecond
	defer func() { T1 = oldT1 }()

	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = true

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	uac.HandleResponse(proto.NewResponse(req, 200, "OK"))
	if uac.state != UACStateCompleted {
		t.Fatalf("expected Completed, got %s", uac.state)
	}

	// Timer D with zero duration (reliable=true) should fire immediately
	time.Sleep(100 * time.Millisecond)

	if uac.state != UACStateTerminated {
		t.Fatalf("expected Terminated after Timer D, got %s", uac.state)
	}
}

func TestUACManager_BranchRouting(t *testing.T) {
	mgr := NewUACManager()

	uac1 := mgr.NewTransaction(context.Background(), proto.SIPMethodINVITE, &mockTransport{}, &Target{})
	uac2 := mgr.NewTransaction(context.Background(), proto.SIPMethodOPTIONS, &mockTransport{}, &Target{})

	req1 := testRequest(t, proto.SIPMethodINVITE, uac1.Branch, false)
	req2 := testRequest(t, proto.SIPMethodOPTIONS, uac2.Branch, false)

	uac1.Send(req1)
	uac2.Send(req2)

	// Send responses via manager
	resp1 := proto.NewResponse(req1, 200, "OK")
	resp1.Headers.Set("Via", []string{"SIP/2.0/UDP 127.0.0.1;branch=" + uac1.Branch})
	mgr.HandleResponse(resp1)

	resp2 := proto.NewResponse(req2, 200, "OK")
	resp2.Headers.Set("Via", []string{"SIP/2.0/UDP 127.0.0.1;branch=" + uac2.Branch})
	mgr.HandleResponse(resp2)

	select {
	case r := <-uac1.Responses:
		if r.StatusCode() != 200 {
			t.Fatalf("uac1 expected 200, got %d", r.StatusCode())
		}
	case <-time.After(time.Second):
		t.Fatal("uac1 timeout waiting for response")
	}

	select {
	case r := <-uac2.Responses:
		if r.StatusCode() != 200 {
			t.Fatalf("uac2 expected 200, got %d", r.StatusCode())
		}
	case <-time.After(time.Second):
		t.Fatal("uac2 timeout waiting for response")
	}
}

func TestUAC_CancelStopsTimers(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = false

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)
	if err := uac.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	uac.Cancel()

	// Wait a bit to ensure no timers fire
	time.Sleep(T1 * 12 / 10)

	if sent := mock.sentCount(); sent != 1 {
		t.Fatalf("expected only initial send after cancel, got %d", sent)
	}

	if uac.state != UACStateTerminated {
		t.Fatalf("expected Terminated after cancel, got %s", uac.state)
	}
}

func TestUAC_ProceedingStopsRetransmit(t *testing.T) {
	mock := &mockTransport{}
	target := &Target{}
	uac := newUACTransaction(context.Background(), proto.SIPMethodINVITE, mock, target)
	uac.reliable = false

	req := testRequest(t, proto.SIPMethodINVITE, uac.Branch, false)

	uac.Send(req)

	// Wait for one retransmit
	time.Sleep(T1 * 110 / 100)

	uac.HandleResponse(proto.NewResponse(req, 180, "Ringing"))

	sentBefore := mock.sentCount()

	// Wait enough for another potential retransmit
	time.Sleep(T1 * 15 / 10)

	sentAfter := mock.sentCount()
	if sentAfter != sentBefore {
		t.Fatalf("retransmits continued after 1xx: before=%d, after=%d", sentBefore, sentAfter)
	}

	uac.Cancel()
}
