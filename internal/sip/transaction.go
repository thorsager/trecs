package sip

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/proto"
)

var (
	T1 = 500 * time.Millisecond
	T2 = 4 * time.Second
	T4 = 5 * time.Second
)

// Transaction is the common interface for server transaction state machines.
type Transaction interface {
	Respond(res *proto.SIPMessage)
	Target() Target
	Transport() Transport
}

// RequestHandler processes a SIP request within a transaction.
type RequestHandler func(ctx context.Context, req *proto.SIPMessage, tx Transaction)

func TransportName(t Transport) string {
	switch t.(type) {
	case *TCPTransport:
		return "TCP"
	case *UDPTransport:
		return "UDP"
	default:
		return "?"
	}
}

// ---- NIST (Non-INVITE Server Transaction, RFC 3261 §17.2.3) ----

// NISTState is the state of a non-INVITE server transaction.
type NISTState int

const (
	NISTTrying NISTState = iota
	NISTProceeding
	NISTCompleted
	NISTTerminated
)

func (s NISTState) String() string {
	switch s {
	case NISTTrying:
		return "Trying"
	case NISTProceeding:
		return "Proceeding"
	case NISTCompleted:
		return "Completed"
	case NISTTerminated:
		return "Terminated"
	default:
		return "Unknown"
	}
}

// NonInviteTransaction implements the NIST state machine.
type NonInviteTransaction struct {
	transport Transport
	lastResp  *proto.SIPMessage
	manager   *TransactionManager
	timerJ    *time.Timer
	logger    *slog.Logger
	target    Target
	branch    string
	method    proto.SIPMethod
	state     NISTState
	mu        sync.Mutex
	reliable  bool
}

// Respond implements Transaction per RFC 3261 §17.2.3.
func (tx *NonInviteTransaction) Respond(res *proto.SIPMessage) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.state == NISTTerminated {
		return
	}

	sc := res.StatusCode()
	tx.logger.Debug("NIST responding", "statusCode", sc, "reason", res.Status(), "state", tx.state)

	switch {
	case sc >= 100 && sc < 200:
		if tx.state == NISTTrying {
			tx.state = NISTProceeding
			tx.logger.Debug("NIST state transition", "state", "Proceeding", "status", "1xx")
		}
		tx.doSend(res)

	case sc >= 200:
		tx.lastResp = res
		prev := tx.state
		tx.state = NISTCompleted
		tx.logger.Debug("NIST state transition", "state", "Completed", "statusCode", sc, "from", prev)
		tx.doSend(res)

		if !tx.reliable {
			logger := tx.logger
			tx.timerJ = time.AfterFunc(64*T1, func() {
				tx.mu.Lock()
				tx.state = NISTTerminated
				tx.mu.Unlock()

				tx.manager.mu.Lock()
				delete(tx.manager.serverTxs, tx.branch)
				tx.manager.mu.Unlock()
				logger.Debug("NIST terminated", "reason", "Timer J")
			})
		} else {
			tx.timerJ = time.AfterFunc(0, func() {
				tx.mu.Lock()
				tx.state = NISTTerminated
				tx.mu.Unlock()
				tx.manager.mu.Lock()
				delete(tx.manager.serverTxs, tx.branch)
				tx.manager.mu.Unlock()
			})
		}
	}
}

func (tx *NonInviteTransaction) stopTimers() {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.timerJ != nil {
		tx.timerJ.Stop()
		tx.timerJ = nil
	}
}

func (tx *NonInviteTransaction) doSend(res *proto.SIPMessage) {
	if err := tx.transport.Send(res, &tx.target); err != nil {
		tx.logger.Error("Send error", "error", err)
	}
}

func (tx *NonInviteTransaction) Target() Target {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.target
}

func (tx *NonInviteTransaction) Transport() Transport {
	return tx.transport
}

// ---- IST (INVITE Server Transaction, RFC 3261 §17.2.1) ----

// ISTState is the state of an INVITE server transaction.
type ISTState int

const (
	ISTTrying ISTState = iota
	ISTProceeding
	ISTCompleted
	ISTConfirmed
	ISTTerminated
)

func (s ISTState) String() string {
	switch s {
	case ISTTrying:
		return "Trying"
	case ISTProceeding:
		return "Proceeding"
	case ISTCompleted:
		return "Completed"
	case ISTConfirmed:
		return "Confirmed"
	case ISTTerminated:
		return "Terminated"
	default:
		return "Unknown"
	}
}

// InviteTransaction implements the IST state machine per RFC 3261 §17.2.1.
type InviteTransaction struct {
	transport    Transport
	timerH       *time.Timer
	logger       *slog.Logger
	lastResp     *proto.SIPMessage
	cancelOKResp *proto.SIPMessage // 200 OK for CANCEL, stored for retransmission
	timerG       *time.Timer
	manager      *TransactionManager
	timerI       *time.Timer
	target       Target
	branch       string
	state        ISTState
	gCount       int
	mu           sync.Mutex
	reliable     bool
}

// Respond implements Transaction per RFC 3261 §17.2.1.
func (tx *InviteTransaction) Respond(res *proto.SIPMessage) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.state == ISTTerminated || tx.state == ISTConfirmed {
		return
	}

	sc := res.StatusCode()
	tx.logger.Debug("IST responding", "statusCode", sc, "reason", res.Status(), "state", tx.state)

	switch {
	case sc >= 100 && sc < 200:
		if sc == 100 {
			if tx.state == ISTTrying || tx.state == ISTProceeding {
				tx.doSend(res)
			}
			return
		}
		if tx.state == ISTTrying || tx.state == ISTProceeding {
			tx.state = ISTProceeding
			tx.logger.Debug("IST state transition", "state", "Proceeding", "status", "1xx")
			tx.doSend(res)
		}

	case sc >= 200 && sc < 300:
		tx.state = ISTTerminated
		tx.logger.Debug("IST state transition", "state", "Terminated", "status", "2xx")
		tx.doSend(res)

		tx.manager.mu.Lock()
		delete(tx.manager.serverTxs, tx.branch)
		tx.manager.mu.Unlock()

	case sc >= 300:
		tx.lastResp = res
		if tx.state != ISTCompleted {
			tx.state = ISTCompleted
			tx.logger.Debug("IST state transition", "state", "Completed", "statusCode", sc)
			tx.doSend(res)

			logger := tx.logger
			tx.timerH = time.AfterFunc(64*T1, func() {
				tx.mu.Lock()
				tx.state = ISTTerminated
				tx.stopTimers()
				tx.mu.Unlock()

				tx.manager.mu.Lock()
				delete(tx.manager.serverTxs, tx.branch)
				tx.manager.mu.Unlock()
				logger.Debug("IST terminated", "reason", "Timer H")
			})

			if !tx.reliable {
				tx.startTimerG()
			}
		}
	}
}

// ackReceived handles an ACK matching this INVITE (non-2xx final response).
// Transitions Completed → Confirmed per RFC 3261 §17.2.1.
func (tx *InviteTransaction) ackReceived() {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.state != ISTCompleted {
		return
	}

	tx.state = ISTConfirmed
	tx.stopTimers()
	tx.logger.Debug("IST state transition", "state", "Confirmed", "reason", "ACK")

	if !tx.reliable {
		logger := tx.logger
		tx.timerI = time.AfterFunc(T4, func() {
			tx.mu.Lock()
			tx.state = ISTTerminated
			tx.mu.Unlock()

			tx.manager.mu.Lock()
			delete(tx.manager.serverTxs, tx.branch)
			tx.manager.mu.Unlock()
			logger.Debug("IST terminated", "reason", "Timer I")
		})
	} else {
		tx.state = ISTTerminated
		tx.manager.mu.Lock()
		delete(tx.manager.serverTxs, tx.branch)
		tx.manager.mu.Unlock()
	}
}

func (tx *InviteTransaction) stopTimers() {
	if tx.timerH != nil {
		tx.timerH.Stop()
	}
	if tx.timerI != nil {
		tx.timerI.Stop()
	}
	if tx.timerG != nil {
		tx.timerG.Stop()
	}
}

func (tx *InviteTransaction) startTimerG() {
	tx.gCount = 0
	tx.scheduleTimerG()
}

func (tx *InviteTransaction) scheduleTimerG() {
	interval := T1 << uint(tx.gCount)
	if interval > T2 {
		interval = T2
	}
	tx.timerG = time.AfterFunc(interval, func() {
		tx.mu.Lock()
		defer tx.mu.Unlock()

		if tx.state != ISTCompleted {
			return
		}
		tx.doSend(tx.lastResp)
		tx.gCount++
		tx.scheduleTimerG()
	})
}

func (tx *InviteTransaction) doSend(res *proto.SIPMessage) {
	if err := tx.transport.Send(res, &tx.target); err != nil {
		tx.logger.Error("Send error", "error", err)
	}
}

func (tx *InviteTransaction) Target() Target {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.target
}

func (tx *InviteTransaction) Transport() Transport {
	return tx.transport
}

// ---- TransactionManager ----

// TransactionManager tracks active server transactions via Via branch.
type TransactionManager struct {
	serverTxs map[string]Transaction
	mu        sync.Mutex
}

func NewTransactionManager() *TransactionManager {
	return &TransactionManager{
		serverTxs: make(map[string]Transaction),
	}
}

// Stop stops all pending transactions. Callers must not use the
// TransactionManager after calling Stop.
func (tm *TransactionManager) Stop() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for branch, tx := range tm.serverTxs {
		switch t := tx.(type) {
		case *InviteTransaction:
			t.stopTimers()
		case *NonInviteTransaction:
			t.stopTimers()
		}
		delete(tm.serverTxs, branch)
	}
}

// HandleRequest processes an incoming request by Via branch match. If an
// existing transaction is found in a retransmittable state, the last response
// is re-sent. Otherwise a new transaction is created (IST for INVITE, NIST
// for all others) and the handler is called.
func (tm *TransactionManager) HandleRequest(ctx context.Context, ev MessageEvent, transport Transport, handler RequestHandler) {
	branch := ev.Msg.ViaBranch()
	if branch == "" {
		slog.Warn("Dropping request: missing branch in Via header")
		return
	}

	tm.mu.Lock()
	existing, exists := tm.serverTxs[branch]
	tm.mu.Unlock()

	if exists {
		if ev.Msg.Method() == proto.SIPMethodCANCEL {
			tm.handleCancelRequest(ctx, ev, transport, existing, handler)
			return
		}
		tm.handleRetransmission(existing)
		return
	}

	if ev.Msg.Method() == proto.SIPMethodCANCEL {
		logutil.FromContext(ctx).Warn("CANCEL received with no matching INVITE transaction",
			"branch", branch, "callID", ev.Msg.Headers.GetFirst("Call-ID"))
		res := proto.NewResponse(ev.Msg, 481, "Call Leg/Transaction Does Not Exist")
		transport.Send(res, &ev.Target) //nolint:errcheck
		return
	}

	reliable := ev.Msg.IsReliableTransport()
	txnLogger := logutil.FromContext(ctx).With("branch", branch)

	var tx Transaction
	switch ev.Msg.Method() {
	case proto.SIPMethodINVITE:
		tx = &InviteTransaction{
			branch:    branch,
			target:    ev.Target,
			transport: transport,
			manager:   tm,
			state:     ISTTrying,
			reliable:  reliable,
			logger:    txnLogger,
		}
		txnLogger.Debug("New INVITE transaction", "state", "Trying")
	default:
		tx = &NonInviteTransaction{
			branch:    branch,
			method:    ev.Msg.Method(),
			target:    ev.Target,
			transport: transport,
			manager:   tm,
			state:     NISTTrying,
			reliable:  reliable,
			logger:    txnLogger,
		}
		txnLogger.Debug("New transaction", "method", string(ev.Msg.Method()), "state", "Trying")
	}

	tm.mu.Lock()
	tm.serverTxs[branch] = tx
	tm.mu.Unlock()

	ctx = logutil.NewContext(ctx, txnLogger)
	if handler != nil {
		handler(ctx, ev.Msg, tx)
	}
}

func (tm *TransactionManager) handleRetransmission(tx Transaction) {
	switch t := tx.(type) {
	case *NonInviteTransaction:
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state == NISTCompleted && t.lastResp != nil {
			t.logger.Debug("Retransmission of non-INVITE, re-sending final response", "method", string(t.method))
			t.transport.Send(t.lastResp, &t.target) //nolint:errcheck
		}
	case *InviteTransaction:
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state == ISTCompleted && t.lastResp != nil {
			t.logger.Debug("Retransmission of INVITE, re-sending final response")
			t.transport.Send(t.lastResp, &t.target) //nolint:errcheck
		}
	}
}

// handleCancelRequest processes a CANCEL request matching an existing
// INVITE server transaction per RFC 3261 §9.2.
// If the IST is in Trying or Proceeding, it sends 200 OK for the CANCEL
// and delegates 487 generation to the handler (which uses ist.Respond()
// for correct transport, To tag, and state machine).
// If no handler is provided, it sends 487 directly (backward compat).
// On retransmitted CANCEL in Completed state, re-sends the stored 200 OK.
// Otherwise it sends 481.
func (tm *TransactionManager) handleCancelRequest(ctx context.Context, ev MessageEvent, transport Transport, existing Transaction, handler RequestHandler) {
	ist, ok := existing.(*InviteTransaction)
	if !ok {
		logutil.FromContext(ctx).Warn("CANCEL matched non-INVITE transaction")
		res := proto.NewResponse(ev.Msg, 481, "Call Leg/Transaction Does Not Exist")
		transport.Send(res, &ev.Target) //nolint:errcheck
		return
	}

	ist.mu.Lock()

	switch {
	case ist.state == ISTTrying || ist.state == ISTProceeding:
		// Send 200 OK for the CANCEL itself.
		okResp := proto.NewResponse(ev.Msg, 200, "OK")
		ist.logger.Debug("CANCEL: sending 200 OK for CANCEL, IST in state", "istState", ist.state)
		if err := transport.Send(okResp, &ev.Target); err != nil {
			ist.logger.Error("CANCEL: 200 OK send error", "error", err)
		}
		ist.cancelOKResp = okResp

		if handler != nil {
			// Delegate 487 generation to the handler, which uses
			// ist.Respond() to send via the correct transport and set
			// the To tag per RFC 3261 §12.1.1.
			ist.mu.Unlock()
			handler(ctx, ev.Msg, ist)
			return
		}

		// No handler: send 487 directly and manage state machine.
		inviteResp := proto.NewResponse(ev.Msg, 487, "Request Terminated")
		inviteResp.CSeq = proto.CSeq{Method: proto.SIPMethodINVITE, Seq: ev.Msg.CSeq.Seq}
		ist.lastResp = inviteResp
		if err := ist.transport.Send(inviteResp, &ist.target); err != nil {
			ist.logger.Error("CANCEL: 487 send error", "error", err)
		}

		prev := ist.state
		ist.state = ISTCompleted
		ist.stopTimers()
		ist.logger.Debug("IST state transition due to CANCEL",
			"from", prev, "to", "Completed")

		logger := ist.logger
		branch := ist.branch
		ist.timerH = time.AfterFunc(64*T1, func() {
			ist.mu.Lock()
			ist.state = ISTTerminated
			ist.stopTimers()
			ist.mu.Unlock()

			tm.mu.Lock()
			delete(tm.serverTxs, branch)
			tm.mu.Unlock()
			logger.Debug("IST terminated", "reason", "Timer H (after CANCEL)")
		})

		if !ist.reliable {
			ist.gCount = 0
			ist.scheduleTimerG()
		}

		ist.mu.Unlock()

	case ist.state == ISTCompleted && ist.cancelOKResp != nil:
		// CANCEL retransmission after successful cancel: re-send 200 OK.
		ist.logger.Debug("CANCEL: retransmission, re-sending 200 OK")
		transport.Send(ist.cancelOKResp, &ev.Target) //nolint:errcheck
		ist.mu.Unlock()

	default:
		// IST is already in Completed (not from cancel), Confirmed, or
		// Terminated — CANCEL is too late.
		state := ist.state
		ist.mu.Unlock()
		ist.logger.Debug("CANCEL ignored: IST already in state", "state", state)
		res := proto.NewResponse(ev.Msg, 481, "Call Leg/Transaction Does Not Exist")
		transport.Send(res, &ev.Target) //nolint:errcheck
	}
}

// HandleACK processes an incoming ACK. Non-2xx ACKs are matched to the
// corresponding INVITE transaction by Via branch. 2xx ACKs use a different
// branch and won't match a transaction, but are still delivered to the
// application layer via the ackCallback in Server.route().
func (tm *TransactionManager) HandleACK(ctx context.Context, ev MessageEvent, transport Transport) {
	branch := ev.Msg.ViaBranch()
	if branch == "" {
		return
	}

	tm.mu.Lock()
	existing, exists := tm.serverTxs[branch]
	tm.mu.Unlock()

	if !exists {
		return
	}

	ist, ok := existing.(*InviteTransaction)
	if !ok {
		return
	}

	ist.ackReceived()
}
