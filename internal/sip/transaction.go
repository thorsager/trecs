package sip

import (
	"log"
	"sync"
	"time"

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
type RequestHandler func(req *proto.SIPMessage, tx Transaction)

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
	NISTTrying     NISTState = iota
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
	mu        sync.Mutex
	branch    string
	method    proto.SIPMethod
	state     NISTState
	lastResp  *proto.SIPMessage
	target    Target
	transport Transport
	manager   *TransactionManager
	timerJ    *time.Timer
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

	switch {
	case sc >= 100 && sc < 200:
		if tx.state == NISTTrying {
			tx.state = NISTProceeding
			log.Printf("[%s] NIST %s → Proceeding (1xx)", TransportName(tx.transport), tx.branch)
		}
		tx.doSend(res)

	case sc >= 200:
		tx.lastResp = res
		prev := tx.state
		tx.state = NISTCompleted
		log.Printf("[%s] NIST %s → Completed (%d) [from %s]", TransportName(tx.transport), tx.branch, sc, prev)
		tx.doSend(res)

		if !tx.reliable {
			tx.timerJ = time.AfterFunc(64*T1, func() {
				tx.mu.Lock()
				tx.state = NISTTerminated
				tx.mu.Unlock()

				tx.manager.mu.Lock()
				delete(tx.manager.serverTxs, tx.branch)
				tx.manager.mu.Unlock()
				log.Printf("[%s] NIST %s terminated (Timer J)", TransportName(tx.transport), tx.branch)
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

func (tx *NonInviteTransaction) doSend(res *proto.SIPMessage) {
	if err := tx.transport.Send(res, &tx.target); err != nil {
		log.Printf("[%s] Send error on %s: %v", TransportName(tx.transport), tx.branch, err)
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
	ISTTrying     ISTState = iota
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
	mu        sync.Mutex
	branch    string
	lastResp  *proto.SIPMessage
	target    Target
	transport Transport
	manager   *TransactionManager
	state     ISTState
	timerH    *time.Timer
	timerI    *time.Timer
	timerG    *time.Timer
	reliable  bool
	gCount    int
}

// Respond implements Transaction per RFC 3261 §17.2.1.
func (tx *InviteTransaction) Respond(res *proto.SIPMessage) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	if tx.state == ISTTerminated || tx.state == ISTConfirmed {
		return
	}

	sc := res.StatusCode()

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
			log.Printf("[%s] IST %s → Proceeding (1xx)", TransportName(tx.transport), tx.branch)
			tx.doSend(res)
		}

	case sc >= 200 && sc < 300:
		tx.state = ISTTerminated
		log.Printf("[%s] IST %s → Terminated (2xx)", TransportName(tx.transport), tx.branch)
		tx.doSend(res)

		tx.manager.mu.Lock()
		delete(tx.manager.serverTxs, tx.branch)
		tx.manager.mu.Unlock()

	case sc >= 300:
		tx.lastResp = res
		if tx.state != ISTCompleted {
			tx.state = ISTCompleted
			log.Printf("[%s] IST %s → Completed (%d)", TransportName(tx.transport), tx.branch, sc)
			tx.doSend(res)

			tx.timerH = time.AfterFunc(64*T1, func() {
				tx.mu.Lock()
				tx.state = ISTTerminated
				tx.stopTimers()
				tx.mu.Unlock()

				tx.manager.mu.Lock()
				delete(tx.manager.serverTxs, tx.branch)
				tx.manager.mu.Unlock()
				log.Printf("[%s] IST %s terminated (Timer H)", TransportName(tx.transport), tx.branch)
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
		log.Printf("[%s] IST %s → Confirmed (ACK)", TransportName(tx.transport), tx.branch)

	if !tx.reliable {
		tx.timerI = time.AfterFunc(T4, func() {
			tx.mu.Lock()
			tx.state = ISTTerminated
			tx.mu.Unlock()

			tx.manager.mu.Lock()
			delete(tx.manager.serverTxs, tx.branch)
			tx.manager.mu.Unlock()
			log.Printf("[%s] IST %s terminated (Timer I)", TransportName(tx.transport), tx.branch)
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
		log.Printf("[%s] Send error on %s: %v", TransportName(tx.transport), tx.branch, err)
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
	mu        sync.Mutex
	serverTxs map[string]Transaction
}

func NewTransactionManager() *TransactionManager {
	return &TransactionManager{
		serverTxs: make(map[string]Transaction),
	}
}

// HandleRequest processes an incoming request by Via branch match. If an
// existing transaction is found in a retransmittable state, the last response
// is re-sent. Otherwise a new transaction is created (IST for INVITE, NIST
// for all others) and the handler is called.
func (tm *TransactionManager) HandleRequest(ev MessageEvent, transport Transport, handler RequestHandler) {
	branch := ev.Msg.ViaBranch()
	if branch == "" {
		log.Printf("Dropping request: missing branch in Via header")
		return
	}

	tm.mu.Lock()
	existing, exists := tm.serverTxs[branch]
	tm.mu.Unlock()

	if exists {
		tm.handleRetransmission(existing)
		return
	}

	reliable := ev.Msg.IsReliableTransport()

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
		}
		log.Printf("[%s] New INVITE transaction [%s] → Trying", TransportName(transport), branch)
	default:
		tx = &NonInviteTransaction{
			branch:    branch,
			method:    ev.Msg.Method(),
			target:    ev.Target,
			transport: transport,
			manager:   tm,
			state:     NISTTrying,
			reliable:  reliable,
		}
		log.Printf("[%s] New %s transaction [%s] → Trying", TransportName(transport), ev.Msg.Method(), branch)
	}

	tm.mu.Lock()
	tm.serverTxs[branch] = tx
	tm.mu.Unlock()

	if handler != nil {
		handler(ev.Msg, tx)
	}
}

func (tm *TransactionManager) handleRetransmission(tx Transaction) {
	switch t := tx.(type) {
	case *NonInviteTransaction:
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state == NISTCompleted && t.lastResp != nil {
			log.Printf("[%s] Retransmission of %s, re-sending final response", TransportName(t.transport), t.method)
			t.transport.Send(t.lastResp, &t.target)
		}
	case *InviteTransaction:
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state == ISTCompleted && t.lastResp != nil {
			log.Printf("[%s] Retransmission of INVITE, re-sending final response", TransportName(t.transport))
			t.transport.Send(t.lastResp, &t.target)
		}
	}
}

// HandleACK processes an incoming ACK. Non-2xx ACKs are matched to the
// corresponding INVITE transaction by Via branch. 2xx ACKs use a different
// branch and won't match a transaction, but are still delivered to the
// application layer via the ackCallback in Server.route().
func (tm *TransactionManager) HandleACK(ev MessageEvent, transport Transport) {
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
