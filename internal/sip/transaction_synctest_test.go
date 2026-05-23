package sip

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/proto"
)

func stateNIST(t *testing.T, tx *NonInviteTransaction) NISTState {
	t.Helper()
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.state
}

func stateIST(t *testing.T, tx *InviteTransaction) ISTState {
	t.Helper()
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.state
}

// Timer J (reliable = 0s)
func TestSynctestNISTTimerJReliable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodOPTIONS, "st-nist-j", true)
		trans := &mockTransport{}
		tm := NewTransactionManager()
		tx := &NonInviteTransaction{
			branch:    "st-nist-j",
			method:    proto.SIPMethodOPTIONS,
			state:     NISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(t),
		}
		tm.mu.Lock()
		tm.serverTxs["st-nist-j"] = tx
		tm.mu.Unlock()

		tx.Respond(proto.NewResponse(req, 200, "OK"))

		synctest.Wait()

		if got := stateNIST(t, tx); got != NISTTerminated {
			t.Fatalf("expected Terminated, got %s", got)
		}

		tm.mu.Lock()
		_, exists := tm.serverTxs["st-nist-j"]
		tm.mu.Unlock()
		if exists {
			t.Fatal("expected Timer J to remove from manager")
		}
	})
}

// Timer J (unreliable = 32s)
func TestSynctestNISTTimerJUnreliable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodOPTIONS, "st-nist-j-udp", false)
		trans := &mockTransport{}
		tm := NewTransactionManager()
		tx := &NonInviteTransaction{
			branch:    "st-nist-j-udp",
			method:    proto.SIPMethodOPTIONS,
			state:     NISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  false,
			logger:    logutil.NewTestLogger(t),
		}
		tm.mu.Lock()
		tm.serverTxs["st-nist-j-udp"] = tx
		tm.mu.Unlock()

		tx.Respond(proto.NewResponse(req, 200, "OK"))

		time.Sleep(64 * T1)
		synctest.Wait()

		if got := stateNIST(t, tx); got != NISTTerminated {
			t.Fatalf("expected Terminated after 32s, got %s", got)
		}
	})
}

// Timer H (32s): IST 300+ → Completed → Timer H → Terminated (no ACK).
func TestSynctestISTTimerHFires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodINVITE, "st-ist-h", true)
		trans := &mockTransport{}
		tm := NewTransactionManager()
		tx := &InviteTransaction{
			branch:    "st-ist-h",
			state:     ISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(t),
		}
		tm.mu.Lock()
		tm.serverTxs["st-ist-h"] = tx
		tm.mu.Unlock()

		tx.Respond(proto.NewResponse(req, 404, "Not Found"))
		if got := stateIST(t, tx); got != ISTCompleted {
			t.Fatalf("expected Completed, got %s", got)
		}

		time.Sleep(64 * T1)
		synctest.Wait()

		if got := stateIST(t, tx); got != ISTTerminated {
			t.Fatalf("expected Terminated after Timer H, got %s", got)
		}

		tm.mu.Lock()
		_, exists := tm.serverTxs["st-ist-h"]
		tm.mu.Unlock()
		if exists {
			t.Fatal("expected Timer H to remove from manager")
		}
	})
}

// Timer I (5s for unreliable)
func TestSynctestISTTimerIUnreliable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodINVITE, "st-ist-i-udp", false)
		trans := &mockTransport{}
		tm := NewTransactionManager()
		tx := &InviteTransaction{
			branch:    "st-ist-i-udp",
			state:     ISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  false,
			logger:    logutil.NewTestLogger(t),
		}
		tm.mu.Lock()
		tm.serverTxs["st-ist-i-udp"] = tx
		tm.mu.Unlock()

		tx.Respond(proto.NewResponse(req, 404, "Not Found"))
		tx.ackReceived()
		if got := stateIST(t, tx); got != ISTConfirmed {
			t.Fatalf("expected Confirmed, got %s", got)
		}

		time.Sleep(T4)
		synctest.Wait()

		if got := stateIST(t, tx); got != ISTTerminated {
			t.Fatalf("expected Terminated after Timer I, got %s", got)
		}
	})
}

// Timer I (0s for reliable)
func TestSynctestISTTimerIReliable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodINVITE, "st-ist-i-tcp", true)
		trans := &mockTransport{}
		tm := NewTransactionManager()
		tx := &InviteTransaction{
			branch:    "st-ist-i-tcp",
			state:     ISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(t),
		}
		tm.mu.Lock()
		tm.serverTxs["st-ist-i-tcp"] = tx
		tm.mu.Unlock()

		tx.Respond(proto.NewResponse(req, 404, "Not Found"))
		tx.ackReceived()

		time.Sleep(0)
		synctest.Wait()

		if got := stateIST(t, tx); got != ISTTerminated {
			t.Fatalf("expected Terminated, got %s", got)
		}
	})
}

// ACK stops Timer H: advance past Timer H window, verify Confirmed persists.
func TestSynctestISTAckPreventsTimerH(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodINVITE, "st-ist-ack-h", true)
		trans := &mockTransport{}
		tm := NewTransactionManager()
		tx := &InviteTransaction{
			branch:    "st-ist-ack-h",
			state:     ISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  true,
			logger:    logutil.NewTestLogger(t),
		}
		tm.mu.Lock()
		tm.serverTxs["st-ist-ack-h"] = tx
		tm.mu.Unlock()

		tx.Respond(proto.NewResponse(req, 404, "Not Found"))
		tx.ackReceived()

		if got := stateIST(t, tx); got != ISTTerminated {
			t.Fatalf("expected Terminated after ACK (reliable), got %s", got)
		}

		// Advance past Timer H window — should be a no-op.
		time.Sleep(64 * T1)
		synctest.Wait()

		if got := stateIST(t, tx); got != ISTTerminated {
			t.Fatalf("expected Terminated, got %s", got)
		}
	})
}

// Timer G (UDP): exponential backoff retransmission.
func TestSynctestISTTimerGRetransmits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodINVITE, "st-ist-g", false)
		trans := &mockTransport{}
		tx := &InviteTransaction{
			branch:    "st-ist-g",
			state:     ISTTrying,
			transport: trans,
			manager:   NewTransactionManager(),
			reliable:  false,
			logger:    logutil.NewTestLogger(t),
		}

		tx.Respond(proto.NewResponse(req, 404, "Not Found"))
		if got := stateIST(t, tx); got != ISTCompleted {
			t.Fatalf("expected Completed, got %s", got)
		}

		time.Sleep(T1)
		synctest.Wait()
		if trans.sentCount() != 2 { // initial + 1 retransmit
			t.Fatalf("after T1: expected 2 sends, got %d", trans.sentCount())
		}

		time.Sleep(2 * T1)
		synctest.Wait()
		if trans.sentCount() != 3 {
			t.Fatalf("after 3*T1: expected 3 sends, got %d", trans.sentCount())
		}

		time.Sleep(4 * T1)
		synctest.Wait()
		if trans.sentCount() != 4 {
			t.Fatalf("after 7*T1: expected 4 sends, got %d", trans.sentCount())
		}

		time.Sleep(8 * T1)
		synctest.Wait()
		if trans.sentCount() != 5 {
			t.Fatalf("after 15*T1: expected 5 sends, got %d", trans.sentCount())
		}

		tx.ackReceived()
		prevCount := trans.sentCount()

		time.Sleep(8 * T1)
		synctest.Wait()
		if trans.sentCount() != prevCount {
			t.Fatal("no retransmits after ACK")
		}
	})
}

// Full IST lifecycle with virtual time.
func TestSynctestISTFullLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodINVITE, "st-ist-full", false)
		trans := &mockTransport{}
		tm := NewTransactionManager()
		tx := &InviteTransaction{
			branch:    "st-ist-full",
			state:     ISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  false,
			logger:    logutil.NewTestLogger(t),
		}
		tm.mu.Lock()
		tm.serverTxs["st-ist-full"] = tx
		tm.mu.Unlock()

		// Trying → Proceeding.
		tx.Respond(proto.NewResponse(req, 180, "Ringing"))
		if got := stateIST(t, tx); got != ISTProceeding {
			t.Fatalf("expected Proceeding, got %s", got)
		}

		// Proceeding → Completed.
		tx.Respond(proto.NewResponse(req, 404, "Not Found"))
		if got := stateIST(t, tx); got != ISTCompleted {
			t.Fatalf("expected Completed, got %s", got)
		}

		// Timer G: one retransmit.
		time.Sleep(T1)
		synctest.Wait()
		if trans.sentCount() != 3 { // 180 + 404 + retransmit
			t.Fatalf("expected 3 sends, got %d", trans.sentCount())
		}

		// Completed → Confirmed.
		tx.ackReceived()
		if got := stateIST(t, tx); got != ISTConfirmed {
			t.Fatalf("expected Confirmed, got %s", got)
		}

		prevCount := trans.sentCount()

		// Timer G should be stopped.
		time.Sleep(2 * T1)
		synctest.Wait()
		if trans.sentCount() != prevCount {
			t.Fatal("no retransmits after ACK")
		}

		// Confirmed → Terminated via Timer I.
		time.Sleep(T4)
		synctest.Wait()
		if got := stateIST(t, tx); got != ISTTerminated {
			t.Fatalf("expected Terminated, got %s", got)
		}
	})
}

// NIST unreliable full lifecycle with virtual time.
func TestSynctestNISTFullLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req := testRequest(t, proto.SIPMethodOPTIONS, "st-nist-udp", false)
		trans := &mockTransport{}
		tm := NewTransactionManager()
		tx := &NonInviteTransaction{
			branch:    "st-nist-udp",
			method:    proto.SIPMethodOPTIONS,
			state:     NISTTrying,
			transport: trans,
			manager:   tm,
			reliable:  false,
			logger:    logutil.NewTestLogger(t),
		}
		tm.mu.Lock()
		tm.serverTxs["st-nist-udp"] = tx
		tm.mu.Unlock()

		tx.Respond(proto.NewResponse(req, 100, "Trying"))
		if got := stateNIST(t, tx); got != NISTProceeding {
			t.Fatalf("expected Proceeding, got %s", got)
		}

		tx.Respond(proto.NewResponse(req, 200, "OK"))
		if got := stateNIST(t, tx); got != NISTCompleted {
			t.Fatalf("expected Completed, got %s", got)
		}

		time.Sleep(64 * T1)
		synctest.Wait()

		if got := stateNIST(t, tx); got != NISTTerminated {
			t.Fatalf("expected Terminated, got %s", got)
		}
	})
}
