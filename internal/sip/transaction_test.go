package sip

import (
	"sync"
	"testing"
	"time"

	"gitub.com/thorsager/trec/proto"
)

// mockTransport records sent messages for verification.
type mockTransport struct {
	mu   sync.Mutex
	sent []*proto.SIPMessage
}

func (m *mockTransport) Send(msg *proto.SIPMessage, target *Target) error {
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return nil
}

func (m *mockTransport) Receive() <-chan MessageEvent { return nil }
func (m *mockTransport) Close() error                 { return nil }

func (m *mockTransport) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func (m *mockTransport) lastSent() *proto.SIPMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		return nil
	}
	return m.sent[len(m.sent)-1]
}

// testRequest builds a SIP request with the given method and Via branch.
func testRequest(t testing.TB, method proto.SIPMethod, branch string, reliable bool) *proto.SIPMessage {
	t.Helper()
	protoVal := "UDP"
	if reliable {
		protoVal = "TCP"
	}
	raw := string(method) + " sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/" + protoVal + " 127.0.0.1;branch=" + branch + "\r\n" +
		"From: <sip:a>;tag=tag1\r\n" +
		"To: <sip:b>\r\n" +
		"Call-ID: test-call\r\n" +
		"CSeq: 1 " + string(method) + "\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalSIPDatagram: %v", err)
	}
	return msg
}

// testEvent creates a MessageEvent from a SIP request for HandleRequest.
func testEvent(t testing.TB, method proto.SIPMethod, branch string, reliable bool) MessageEvent {
	t.Helper()
	msg := testRequest(t, method, branch, reliable)
	return MessageEvent{Msg: msg, Target: Target{}}
}

// awaitTerminated waits up to timeout for a NIST transaction to reach Terminated.
func awaitNISTTerminated(t *testing.T, tx *NonInviteTransaction, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tx.mu.Lock()
		state := tx.state
		tx.mu.Unlock()
		if state == NISTTerminated {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("NIST did not reach Terminated within timeout")
}

// awaitISTTerminated waits up to timeout for an IST transaction to reach Terminated.
func awaitISTTerminated(t *testing.T, tx *InviteTransaction, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tx.mu.Lock()
		state := tx.state
		tx.mu.Unlock()
		if state == ISTTerminated {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("IST did not reach Terminated within timeout")
}

// ============================================================================
// NIST — RFC 3261 §17.2.3
// ============================================================================

func TestNISTInitialState(t *testing.T) {
	tm := NewTransactionManager()
	tx := &NonInviteTransaction{
		branch:    "test",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying,
		transport: &mockTransport{},
		manager:   tm,
		reliable:  true,
	}
	if tx.state != NISTTrying {
		t.Fatalf("expected Trying, got %s", tx.state)
	}
}

func TestNIST100Trying(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-100", true)
	trans := &mockTransport{}
	tx := &NonInviteTransaction{
		branch:    "nist-100",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 100, "Trying"))

	if tx.state != NISTProceeding {
		t.Fatalf("expected Proceeding after 100, got %s", tx.state)
	}
	if trans.sentCount() != 1 {
		t.Fatalf("expected 1 sent, got %d", trans.sentCount())
	}
}

func TestNISTProvisionalToProceeding(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-180", true)
	trans := &mockTransport{}
	tx := &NonInviteTransaction{
		branch:    "nist-180",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 180, "Ringing"))

	if tx.state != NISTProceeding {
		t.Fatalf("expected Proceeding after 180, got %s", tx.state)
	}
	if trans.sentCount() != 1 {
		t.Fatalf("expected 1 sent, got %d", trans.sentCount())
	}
}

func TestNISTMultipleProvisional(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-multi", true)
	trans := &mockTransport{}
	tx := &NonInviteTransaction{
		branch:    "nist-multi",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 100, "Trying")) // Trying → Proceeding
	tx.Respond(proto.NewResponse(req, 180, "Ringing")) // stays Proceeding

	if tx.state != NISTProceeding {
		t.Fatalf("expected Proceeding, got %s", tx.state)
	}
	if trans.sentCount() != 2 {
		t.Fatalf("expected 2 sent, got %d", trans.sentCount())
	}
}

func TestNISTFinalResponseToCompleted(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-200", true)
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &NonInviteTransaction{
		branch:    "nist-200",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying,
		transport: trans,
		manager:   tm,
		reliable:  true,
	}

	// Add to manager so we can verify cleanup.
	tm.mu.Lock()
	tm.serverTxs["nist-200"] = tx
	tm.mu.Unlock()

	tx.Respond(proto.NewResponse(req, 200, "OK"))

	if tx.state != NISTCompleted {
		t.Fatalf("expected Completed after 200 for reliable transport, got %s", tx.state)
	}
	if trans.lastSent().StatusCode() != 200 {
		t.Fatalf("expected sent 200, got %d", trans.lastSent().StatusCode())
	}

	// Timer J = 0 for reliable → terminates asynchronously.
	awaitNISTTerminated(t, tx, 500*time.Millisecond)

	// Verify removed from manager.
	tm.mu.Lock()
	_, exists := tm.serverTxs["nist-200"]
	tm.mu.Unlock()
	if exists {
		t.Fatal("expected transaction removed from manager after Timer J")
	}
}

func TestNISTRetransmissionInCompleted(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-retrans", true)
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &NonInviteTransaction{
		branch:    "nist-retrans",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTCompleted, // simulate already completed
		transport: trans,
		manager:   tm,
		reliable:  true,
		lastResp:  proto.NewResponse(req, 200, "OK"),
	}
	tm.mu.Lock()
	tm.serverTxs["nist-retrans"] = tx
	tm.mu.Unlock()

	// Simulate retransmission via manager.
	tm.handleRetransmission(tx)

	if trans.sentCount() != 1 {
		t.Fatalf("expected 1 retransmit, got %d", trans.sentCount())
	}
}

func TestNISTRetransmissionInTryingDropped(t *testing.T) {
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &NonInviteTransaction{
		branch:    "nist-drop",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying, // not completed
		transport: trans,
		manager:   tm,
		reliable:  true,
	}

	tm.handleRetransmission(tx)

	if trans.sentCount() != 0 {
		t.Fatalf("expected 0 sends from Trying retransmission, got %d", trans.sentCount())
	}
}

func TestNISTRespondAfterTerminated(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-done", true)
	trans := &mockTransport{}
	tx := &NonInviteTransaction{
		branch:    "nist-done",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTerminated,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 200, "OK"))

	if trans.sentCount() != 0 {
		t.Fatalf("expected 0 sends after Terminated, got %d", trans.sentCount())
	}
}

func TestNISTCompletedFromProceeding(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-proc-200", true)
	trans := &mockTransport{}
	tx := &NonInviteTransaction{
		branch:    "nist-proc-200",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTProceeding,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 200, "OK"))

	if tx.state != NISTCompleted {
		t.Fatalf("expected Completed from Proceeding, got %s", tx.state)
	}
	if trans.lastSent().StatusCode() != 200 {
		t.Fatalf("expected sent 200, got %d", trans.lastSent().StatusCode())
	}
}

func TestNISTStatusCode300Plus(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-300", false)
	trans := &mockTransport{}
	tx := &NonInviteTransaction{
		branch:    "nist-300",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  false,
	}

	for _, code := range []int{300, 404, 500, 603} {
		tx.Respond(proto.NewResponse(req, code, "Status"))
		if tx.state != NISTCompleted {
			t.Fatalf("expected Completed after %d, got %s", code, tx.state)
		}
	}
	// Stop Timer J (32s) to avoid spurious log output after the test.
	tx.mu.Lock()
	if tx.timerJ != nil {
		tx.timerJ.Stop()
	}
	tx.mu.Unlock()
}

func TestNISTTimerJReliable(t *testing.T) {
	// Timer J = 0 for reliable → should fire and clean up.
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-timer-j", true)
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &NonInviteTransaction{
		branch:    "nist-timer-j",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying,
		transport: trans,
		manager:   tm,
		reliable:  true,
	}
	tm.mu.Lock()
	tm.serverTxs["nist-timer-j"] = tx
	tm.mu.Unlock()

	tx.Respond(proto.NewResponse(req, 200, "OK"))

	awaitNISTTerminated(t, tx, 500*time.Millisecond)

	tm.mu.Lock()
	_, exists := tm.serverTxs["nist-timer-j"]
	tm.mu.Unlock()
	if exists {
		t.Fatal("expected Timer J to remove transaction from manager")
	}
}

func TestNISTUnreliableTimerJ(t *testing.T) {
	// Timer J = 32s for unreliable → verify timer is set.
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-udp-timer", false)
	trans := &mockTransport{}
	tx := &NonInviteTransaction{
		branch:    "nist-udp-timer",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  false,
	}

	tx.Respond(proto.NewResponse(req, 200, "OK"))

	if tx.state != NISTCompleted {
		t.Fatalf("expected Completed, got %s", tx.state)
	}
	tx.mu.Lock()
	if tx.timerJ == nil {
		t.Fatal("expected Timer J to be set for unreliable transport")
	}
	tx.mu.Unlock()
}

func TestNISTProceedingKeepSending1xx(t *testing.T) {
	req := testRequest(t, proto.SIPMethodOPTIONS, "nist-proc-keep", true)
	trans := &mockTransport{}
	tx := &NonInviteTransaction{
		branch:    "nist-proc-keep",
		method:    proto.SIPMethodOPTIONS,
		state:     NISTProceeding,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	for i := 0; i < 3; i++ {
		tx.Respond(proto.NewResponse(req, 183, "Session Progress"))
	}

	if tx.state != NISTProceeding {
		t.Fatalf("expected Proceeding after multiple 1xx, got %s", tx.state)
	}
	if trans.sentCount() != 3 {
		t.Fatalf("expected 3 sends, got %d", trans.sentCount())
	}
}

// ============================================================================
// IST — RFC 3261 §17.2.1
// ============================================================================

func TestISTInitialState(t *testing.T) {
	tx := &InviteTransaction{
		branch:   "ist-init",
		state:    ISTTrying,
		manager:  NewTransactionManager(),
		reliable: true,
	}
	if tx.state != ISTTrying {
		t.Fatalf("expected Trying, got %s", tx.state)
	}
}

func TestIST100Trying(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-100", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-100",
		state:     ISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 100, "Trying"))

	if tx.state != ISTTrying {
		t.Fatalf("expected Trying after 100, got %s", tx.state)
	}
	if trans.sentCount() != 1 {
		t.Fatalf("expected 1 sent, got %d", trans.sentCount())
	}
}

func TestISTProvisionalToProceeding(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-180", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-180",
		state:     ISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 180, "Ringing"))

	if tx.state != ISTProceeding {
		t.Fatalf("expected Proceeding after 180, got %s", tx.state)
	}
	if trans.lastSent().StatusCode() != 180 {
		t.Fatalf("expected sent 180, got %d", trans.lastSent().StatusCode())
	}
}

func TestIST100DoesNotTransition(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-100-only", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-100-only",
		state:     ISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 100, "Trying"))
	if tx.state != ISTTrying {
		t.Fatalf("100 should not leave Trying, got %s", tx.state)
	}

	// Second 100 should also be sent.
	tx.Respond(proto.NewResponse(req, 100, "Trying"))
	if trans.sentCount() != 2 {
		t.Fatalf("expected 2 sends (two 100s), got %d", trans.sentCount())
	}
}

func TestIST2xxTerminatesImmediately(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-2xx", true)
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &InviteTransaction{
		branch:    "ist-2xx",
		state:     ISTTrying,
		transport: trans,
		manager:   tm,
		reliable:  true,
	}
	tm.mu.Lock()
	tm.serverTxs["ist-2xx"] = tx
	tm.mu.Unlock()

	tx.Respond(proto.NewResponse(req, 200, "OK"))

	if tx.state != ISTTerminated {
		t.Fatalf("expected Terminated after 2xx, got %s", tx.state)
	}
	if trans.lastSent().StatusCode() != 200 {
		t.Fatalf("expected sent 200, got %d", trans.lastSent().StatusCode())
	}

	// Must be removed from manager synchronously.
	tm.mu.Lock()
	_, exists := tm.serverTxs["ist-2xx"]
	tm.mu.Unlock()
	if exists {
		t.Fatal("expected 2xx to remove transaction from manager synchronously")
	}
}

func TestIST300PlusToCompleted(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-300", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-300",
		state:     ISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 404, "Not Found"))

	if tx.state != ISTCompleted {
		t.Fatalf("expected Completed after 404, got %s", tx.state)
	}
	if trans.lastSent().StatusCode() != 404 {
		t.Fatalf("expected sent 404, got %d", trans.lastSent().StatusCode())
	}
}

func TestISTMultipleFinalResponsesStayCompleted(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-double", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-double",
		state:     ISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 404, "Not Found"))
	if tx.state != ISTCompleted {
		t.Fatalf("expected Completed after first 4xx, got %s", tx.state)
	}

	firstSent := trans.sentCount()

	// Second final response should NOT trigger another state transition or send.
	tx.Respond(proto.NewResponse(req, 500, "Server Error"))

	if tx.state != ISTCompleted {
		t.Fatalf("expected still Completed, got %s", tx.state)
	}
	if trans.sentCount() != firstSent {
		t.Fatalf("expected no additional sends, got %d", trans.sentCount())
	}
}

func TestISTAckInCompletedToConfirmed(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-ack-good", true)
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &InviteTransaction{
		branch:    "ist-ack-good",
		state:     ISTTrying,
		transport: trans,
		manager:   tm,
		reliable:  true,
	}
	tm.mu.Lock()
	tm.serverTxs["ist-ack-good"] = tx
	tm.mu.Unlock()

	// Get to Completed.
	tx.Respond(proto.NewResponse(req, 404, "Not Found"))
	if tx.state != ISTCompleted {
		t.Fatalf("expected Completed, got %s", tx.state)
	}

	// ACK.
	tx.ackReceived()

	if tx.state != ISTTerminated {
		t.Fatalf("expected Terminated after ACK for reliable transport, got %s", tx.state)
	}

	// Timer I = 0 for reliable → terminates synchronously.

	tm.mu.Lock()
	_, exists := tm.serverTxs["ist-ack-good"]
	tm.mu.Unlock()
	if exists {
		t.Fatal("expected transaction removed from manager after Timer I")
	}
}

func TestISTAckInWrongStateNoop(t *testing.T) {
	tx := &InviteTransaction{
		branch:   "ist-ack-wrong",
		state:    ISTTrying,
		manager:  NewTransactionManager(),
		reliable: true,
	}

	tx.ackReceived()
	if tx.state != ISTTrying {
		t.Fatalf("ACK in Trying should be noop, got %s", tx.state)
	}

	tx.state = ISTProceeding
	tx.ackReceived()
	if tx.state != ISTProceeding {
		t.Fatalf("ACK in Proceeding should be noop, got %s", tx.state)
	}
}

func TestISTRespondAfterTerminatedNoop(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-after-term", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-after-term",
		state:     ISTTerminated,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 200, "OK"))
	if trans.sentCount() != 0 {
		t.Fatalf("expected 0 sends after Terminated, got %d", trans.sentCount())
	}

	tx.Respond(proto.NewResponse(req, 404, "Not Found"))
	if trans.sentCount() != 0 {
		t.Fatalf("expected 0 sends after Terminated, got %d", trans.sentCount())
	}
}

func TestISTRespondAfterConfirmedNoop(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-after-conf", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-after-conf",
		state:     ISTConfirmed,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 200, "OK"))
	if trans.sentCount() != 0 {
		t.Fatalf("expected 0 sends after Confirmed, got %d", trans.sentCount())
	}
}

func TestIST100InProceeding(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-proc-100", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-proc-100",
		state:     ISTProceeding,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 100, "Trying"))
	if tx.state != ISTProceeding {
		t.Fatalf("100 in Proceeding should stay in Proceeding, got %s", tx.state)
	}
	if trans.sentCount() != 1 {
		t.Fatalf("expected 1 send for 100 in Proceeding, got %d", trans.sentCount())
	}
}

func TestIST2xxFromProceeding(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-proc-2xx", true)
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &InviteTransaction{
		branch:    "ist-proc-2xx",
		state:     ISTProceeding,
		transport: trans,
		manager:   tm,
		reliable:  true,
	}
	tm.mu.Lock()
	tm.serverTxs["ist-proc-2xx"] = tx
	tm.mu.Unlock()

	tx.Respond(proto.NewResponse(req, 200, "OK"))

	if tx.state != ISTTerminated {
		t.Fatalf("expected Terminated after 2xx from Proceeding, got %s", tx.state)
	}
}

func TestIST300FromProceeding(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-proc-300", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-proc-300",
		state:     ISTProceeding,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 486, "Busy Here"))

	if tx.state != ISTCompleted {
		t.Fatalf("expected Completed after 486 from Proceeding, got %s", tx.state)
	}
}

func TestISTTimerHSetOnCompleted(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-timer-h", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-timer-h",
		state:     ISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 404, "Not Found"))

	tx.mu.Lock()
	if tx.timerH == nil {
		t.Fatal("expected Timer H to be set after entering Completed")
	}
	tx.mu.Unlock()
}

func TestISTTimerGNotStartedOnReliable(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-no-timer-g", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-no-timer-g",
		state:     ISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 404, "Not Found"))

	tx.mu.Lock()
	if tx.timerG != nil {
		t.Fatal("expected Timer G to NOT be started for reliable transport")
	}
	tx.mu.Unlock()
}

func TestISTAckStopsTimerH(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-stop-h", true)
	trans := &mockTransport{}
	tx := &InviteTransaction{
		branch:    "ist-stop-h",
		state:     ISTTrying,
		transport: trans,
		manager:   NewTransactionManager(),
		reliable:  true,
	}

	tx.Respond(proto.NewResponse(req, 404, "Not Found"))

	tx.mu.Lock()
	h := tx.timerH
	tx.mu.Unlock()
	if h == nil {
		t.Fatal("expected Timer H")
	}

	tx.ackReceived()

	// After ACK, Timer H should be stopped (stopTimers called).
	if h.Stop() {
		t.Fatal("expected Timer H to already be stopped after ACK")
	}
}

func TestISTRetransmissionInCompleted(t *testing.T) {
	req := testRequest(t, proto.SIPMethodINVITE, "ist-retrans", true)
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &InviteTransaction{
		branch:    "ist-retrans",
		state:     ISTCompleted,
		transport: trans,
		manager:   tm,
		reliable:  true,
		lastResp:  proto.NewResponse(req, 404, "Not Found"),
	}
	tm.mu.Lock()
	tm.serverTxs["ist-retrans"] = tx
	tm.mu.Unlock()

	tm.handleRetransmission(tx)

	if trans.sentCount() != 1 {
		t.Fatalf("expected 1 retransmit, got %d", trans.sentCount())
	}
}

func TestISTRetransmissionInTryingDropped(t *testing.T) {
	trans := &mockTransport{}
	tm := NewTransactionManager()
	tx := &InviteTransaction{
		branch:    "ist-drop",
		state:     ISTTrying,
		transport: trans,
		manager:   tm,
		reliable:  true,
	}

	tm.handleRetransmission(tx)

	if trans.sentCount() != 0 {
		t.Fatalf("expected 0 sends from Trying, got %d", trans.sentCount())
	}
}

// ============================================================================
// TransactionManager
// ============================================================================

func TestManagerCreatesNISTForNonInvite(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodOPTIONS, "mgr-nist", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	var created Transaction
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		created = tx
	})

	if created == nil {
		t.Fatal("expected transaction to be created")
	}
	if _, ok := created.(*NonInviteTransaction); !ok {
		t.Fatal("expected NonInviteTransaction for OPTIONS")
	}
}

func TestManagerCreatesISTForInvite(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodINVITE, "mgr-ist", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	var created Transaction
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		created = tx
	})

	if created == nil {
		t.Fatal("expected transaction to be created")
	}
	if _, ok := created.(*InviteTransaction); !ok {
		t.Fatal("expected InviteTransaction for INVITE")
	}
}

func TestManagerMissingBranchDropped(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}

	raw := "OPTIONS sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1\r\n" + // no branch
		"From: <sip:a>;tag=1\r\n" +
		"To: <sip:b>\r\n" +
		"Call-ID: no-branch\r\n" +
		"CSeq: 1 OPTIONS\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, _ := proto.UnmarshalSIPDatagram([]byte(raw))
	ev := MessageEvent{Msg: msg, Target: Target{}}

	handlerCalled := false
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		handlerCalled = true
	})

	if handlerCalled {
		t.Fatal("handler should not be called for request without branch")
	}
}

func TestManagerRetransmissionNIST(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodOPTIONS, "mgr-retrans-nist", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	// First request: create and complete.
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		tx.Respond(proto.NewResponse(r, 200, "OK"))
	})

	firstSent := trans.sentCount()

	// Second request with same branch: retransmission.
	tm.HandleRequest(ev, trans, nil)

	// Should have re-sent the last response.
	if trans.sentCount() <= firstSent {
		t.Fatal("expected retransmission to send another response")
	}
}

func TestManagerRetransmissionIST(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodINVITE, "mgr-retrans-ist", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	// First request: create and send 300+ to reach Completed.
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		tx.Respond(proto.NewResponse(r, 404, "Not Found"))
	})

	firstSent := trans.sentCount()

	// Second request with same branch: retransmission.
	tm.HandleRequest(ev, trans, nil)

	if trans.sentCount() <= firstSent {
		t.Fatal("expected retransmission to send another response")
	}
}

func TestManagerHandleACKMatchesIST(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodINVITE, "mgr-ack-ist", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	// Create IST in Completed.
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		tx.Respond(proto.NewResponse(r, 404, "Not Found"))
	})

	// Build ACK with same branch.
	ackRaw := "ACK sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1;branch=mgr-ack-ist\r\n" +
		"From: <sip:a>;tag=tag1\r\n" +
		"To: <sip:b>;tag=tag2\r\n" +
		"Call-ID: test-call\r\n" +
		"CSeq: 1 ACK\r\n" +
		"Content-Length: 0\r\n\r\n"
	ack, _ := proto.UnmarshalSIPDatagram([]byte(ackRaw))
	ackEv := MessageEvent{Msg: ack, Target: Target{}}

	tm.HandleACK(ackEv, trans)

	// Timer I = 0 for reliable → terminates synchronously.
	tm.mu.Lock()
	_, exists := tm.serverTxs["mgr-ack-ist"]
	tm.mu.Unlock()
	if exists {
		t.Fatal("expected IST removed from manager after ACK for reliable transport")
	}
}

func TestManagerHandleACKNoMatch(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}

	ackRaw := "ACK sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1;branch=no-such-tx\r\n" +
		"From: <sip:a>;tag=1\r\n" +
		"To: <sip:b>;tag=2\r\n" +
		"Call-ID: no-match\r\n" +
		"CSeq: 1 ACK\r\n" +
		"Content-Length: 0\r\n\r\n"
	ack, _ := proto.UnmarshalSIPDatagram([]byte(ackRaw))
	ackEv := MessageEvent{Msg: ack, Target: Target{}}

	// Should not panic.
	tm.HandleACK(ackEv, trans)
}

func TestManagerHandleACKNonInviteNoop(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodOPTIONS, "mgr-ack-nist", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		tx.Respond(proto.NewResponse(r, 200, "OK"))
	})

	ackRaw := "ACK sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1;branch=mgr-ack-nist\r\n" +
		"From: <sip:a>;tag=1\r\n" +
		"To: <sip:b>;tag=2\r\n" +
		"Call-ID: test-call\r\n" +
		"CSeq: 1 ACK\r\n" +
		"Content-Length: 0\r\n\r\n"
	ack, _ := proto.UnmarshalSIPDatagram([]byte(ackRaw))
	tm.HandleACK(MessageEvent{Msg: ack}, trans)
	// Should not panic, transaction should remain NIST.
}

func TestManagerHandlerCalledForNewRequest(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodOPTIONS, "mgr-handler", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	called := false
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		called = true
		if r != req {
			t.Fatal("expected same request pointer")
		}
	})

	if !called {
		t.Fatal("handler should be called for new request")
	}
}

func TestManagerHandlerNotCalledForRetransmission(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodOPTIONS, "mgr-no-handler", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	handlerCount := 0
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		handlerCount++
		tx.Respond(proto.NewResponse(r, 200, "OK"))
	})

	// Retransmission: handler should NOT be called.
	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		handlerCount++
	})

	if handlerCount != 1 {
		t.Fatalf("expected handler called once, got %d", handlerCount)
	}
}

func TestManagerConcurrentRequests(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}

	var wg sync.WaitGroup
	n := 20
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			branch := "mgr-concurrent-" + itoa(i)
			req := testRequest(t, proto.SIPMethodOPTIONS, branch, true)
			ev := MessageEvent{Msg: req, Target: Target{}}
			tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
				tx.Respond(proto.NewResponse(r, 200, "OK"))
			})
		}(i)
	}
	wg.Wait()

	if trans.sentCount() < n {
		t.Fatalf("expected at least %d sends, got %d", n, trans.sentCount())
	}
}

func TestManagerHandleACKEmptyBranch(t *testing.T) {
	tm := NewTransactionManager()
	raw := "ACK sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1\r\n" + // no branch
		"From: <sip:a>;tag=1\r\n" +
		"To: <sip:b>\r\n" +
		"Call-ID: empty-branch\r\n" +
		"CSeq: 1 ACK\r\n" +
		"Content-Length: 0\r\n\r\n"
	ack, _ := proto.UnmarshalSIPDatagram([]byte(raw))
	tm.HandleACK(MessageEvent{Msg: ack}, &mockTransport{})
	// Should not panic.
}

func TestACKAfterTerminatedNoop(t *testing.T) {
	tm := NewTransactionManager()
	trans := &mockTransport{}
	req := testRequest(t, proto.SIPMethodINVITE, "ist-ack-after-term", true)
	ev := MessageEvent{Msg: req, Target: Target{}}

	tm.HandleRequest(ev, trans, func(r *proto.SIPMessage, tx Transaction) {
		tx.Respond(proto.NewResponse(r, 200, "OK"))
	})

	// Transaction is already Terminated and removed (2xx).
	ackRaw := "ACK sip:test SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1;branch=ist-ack-after-term\r\n" +
		"From: <sip:a>;tag=tag1\r\n" +
		"To: <sip:b>;tag=tag2\r\n" +
		"Call-ID: test-call\r\n" +
		"CSeq: 1 ACK\r\n" +
		"Content-Length: 0\r\n\r\n"
	ack, _ := proto.UnmarshalSIPDatagram([]byte(ackRaw))
	ackEv := MessageEvent{Msg: ack}
	tm.HandleACK(ackEv, trans)
	// Should not panic, ACK silently dropped.
}
