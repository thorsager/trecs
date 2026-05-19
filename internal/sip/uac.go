package sip

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"gitub.com/thorsager/trec/proto"
)

func GenerateBranch() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "z9hG4bKtrec00000000"
	}
	return "z9hG4bK" + hex.EncodeToString(b)
}

func GenerateCallID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "trec-default-call-id"
	}
	return hex.EncodeToString(b)
}

type UACState int

const (
	UACStateCalling    UACState = iota
	UACStateProceeding
	UACStateCompleted
	UACStateTerminated
)

func (s UACState) String() string {
	switch s {
	case UACStateCalling:
		return "Calling"
	case UACStateProceeding:
		return "Proceeding"
	case UACStateCompleted:
		return "Completed"
	case UACStateTerminated:
		return "Terminated"
	default:
		return "Unknown"
	}
}

type UACTransaction struct {
	Branch    string
	Method    proto.SIPMethod
	Responses chan *proto.SIPMessage
	Errors    chan error

	state     UACState
	stateMu   sync.Mutex
	request   *proto.SIPMessage
	transport Transport
	target    *Target
	reliable  bool
	timerA    *time.Timer
	timerB    *time.Timer
	timerD    *time.Timer
	retxCount int

	manager *UACManager

	ctx    context.Context
	cancel context.CancelFunc
}

func newUACTransaction(ctx context.Context, method proto.SIPMethod, transport Transport, target *Target) *UACTransaction {
	ctx, cancel := context.WithCancel(ctx)
	return &UACTransaction{
		Branch:    GenerateBranch(),
		Method:    method,
		Responses: make(chan *proto.SIPMessage, 4),
		Errors:    make(chan error, 1),
		state:     UACStateCalling,
		transport: transport,
		target:    target,
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (u *UACTransaction) Send(req *proto.SIPMessage) error {
	u.stateMu.Lock()
	u.request = req
	u.stateMu.Unlock()

	if err := u.transport.Send(req, u.target); err != nil {
		return err
	}
	log.Printf("[%s] UAC %s → Calling (sent %s)", TransportName(u.transport), u.Branch, u.Method)

	if !u.reliable {
		u.startTimerA()
	}
	u.startTimerB()

	return nil
}

func (u *UACTransaction) HandleResponse(msg *proto.SIPMessage) {
	u.stateMu.Lock()
	defer u.stateMu.Unlock()

	if u.state == UACStateTerminated {
		return
	}

	sc := msg.StatusCode()

	switch {
	case sc >= 100 && sc < 200:
		if u.state == UACStateCalling {
			u.state = UACStateProceeding
			if u.timerA != nil {
				u.timerA.Stop()
				u.timerA = nil
			}
			log.Printf("[%s] UAC %s → Proceeding (%d)", TransportName(u.transport), u.Branch, sc)
		}
		select {
		case u.Responses <- msg:
		default:
		}

	case sc >= 200 && sc < 300:
		if u.state == UACStateCalling || u.state == UACStateProceeding {
			u.transitionToCompleted()
			log.Printf("[%s] UAC %s → Completed (%d)", TransportName(u.transport), u.Branch, sc)
		}
		select {
		case u.Responses <- msg:
		default:
		}

	case sc >= 300:
		if u.state == UACStateCalling || u.state == UACStateProceeding {
			u.transitionToCompleted()
			log.Printf("[%s] UAC %s → Completed (%d)", TransportName(u.transport), u.Branch, sc)
			if u.Method == proto.SIPMethodINVITE {
				u.sendACK(msg)
			}
		}
		select {
		case u.Responses <- msg:
		default:
		}
	}
}

func (u *UACTransaction) transitionToCompleted() {
	u.state = UACStateCompleted
	if u.timerA != nil {
		u.timerA.Stop()
		u.timerA = nil
	}
	if u.timerB != nil {
		u.timerB.Stop()
		u.timerB = nil
	}

	var d time.Duration
	if !u.reliable {
		d = 32 * time.Second
	}
	u.timerD = time.AfterFunc(d, func() {
		u.stateMu.Lock()
		u.state = UACStateTerminated
		u.stateMu.Unlock()
		log.Printf("[%s] UAC %s terminated (Timer D)", TransportName(u.transport), u.Branch)
		if u.manager != nil {
			u.manager.Deregister(u.Branch)
		}
	})
}

func (u *UACTransaction) startTimerA() {
	u.retxCount = 0
	u.scheduleTimerA()
}

func (u *UACTransaction) scheduleTimerA() {
	interval := T1 << uint(u.retxCount)
	if interval > T2 {
		interval = T2
	}
	u.timerA = time.AfterFunc(interval, func() {
		u.stateMu.Lock()
		if u.state != UACStateCalling {
			u.stateMu.Unlock()
			return
		}
		if err := u.transport.Send(u.request, u.target); err != nil {
			log.Printf("[%s] UAC retransmit error on %s: %v", TransportName(u.transport), u.Branch, err)
			u.stateMu.Unlock()
			return
		}
		log.Printf("[%s] UAC %s retransmit #%d", TransportName(u.transport), u.Branch, u.retxCount+1)
		u.retxCount++
		u.stateMu.Unlock()
		u.scheduleTimerA()
	})
}

func (u *UACTransaction) startTimerB() {
	u.timerB = time.AfterFunc(64*T1, func() {
		u.stateMu.Lock()
		u.state = UACStateTerminated
		if u.timerA != nil {
			u.timerA.Stop()
			u.timerA = nil
		}
		u.stateMu.Unlock()
		log.Printf("[%s] UAC %s terminated (Timer B)", TransportName(u.transport), u.Branch)
		select {
		case u.Errors <- TimeoutError{Method: u.Method}:
		default:
		}
		if u.manager != nil {
			u.manager.Deregister(u.Branch)
		}
	})
}

// sendACK generates an ACK for a 300-699 final response to INVITE
// per RFC 3261 §17.1.1.3.
func (u *UACTransaction) sendACK(resp *proto.SIPMessage) {
	ackURI := u.request.RequestURI()
	if contact := resp.Headers.GetFirst("Contact"); contact != "" {
		ackURI = StripBrackets(contact)
	}

	ack := proto.NewRequest(proto.SIPMethodACK, ackURI)
	ack.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=%s",
		TransportName(u.transport), u.request.ViaSentBy(), u.Branch))
	if fromVals := u.request.Headers["From"]; len(fromVals) > 0 {
		ack.Headers.Add("From", fromVals[0])
	}
	if toVals := u.request.Headers["To"]; len(toVals) > 0 {
		ack.Headers.Add("To", toVals[0])
	}
	ack.Headers.Add("Call-ID", u.request.Headers.GetFirst("Call-ID"))
	ack.CSeq = proto.CSeq{Method: proto.SIPMethodACK, Seq: u.request.CSeq.Seq}
	ack.Headers.Add("Max-Forwards", "70")
	ack.Headers.Add("Content-Length", "0")

	if err := u.transport.Send(ack, u.target); err != nil {
		log.Printf("[%s] UAC %s ACK send error: %v", TransportName(u.transport), u.Branch, err)
	} else {
		log.Printf("[%s] UAC %s ACK sent for %d response", TransportName(u.transport), u.Branch, resp.StatusCode())
	}
}

func (u *UACTransaction) StopTimers() {
	u.stateMu.Lock()
	defer u.stateMu.Unlock()
	if u.timerA != nil {
		u.timerA.Stop()
		u.timerA = nil
	}
	if u.timerB != nil {
		u.timerB.Stop()
		u.timerB = nil
	}
	if u.timerD != nil {
		u.timerD.Stop()
		u.timerD = nil
	}
}

func (u *UACTransaction) Cancel() {
	u.StopTimers()
	u.stateMu.Lock()
	u.state = UACStateTerminated
	u.stateMu.Unlock()
	u.cancel()
	if u.manager != nil {
		u.manager.Deregister(u.Branch)
	}
}

type TimeoutError struct {
	Method proto.SIPMethod
}

func (e TimeoutError) Error() string {
	return string(e.Method) + " transaction timed out"
}

type UACManager struct {
	mu      sync.Mutex
	pending map[string]*UACTransaction
}

func NewUACManager() *UACManager {
	return &UACManager{
		pending: make(map[string]*UACTransaction),
	}
}

func (m *UACManager) NewTransaction(ctx context.Context, method proto.SIPMethod, transport Transport, target *Target) *UACTransaction {
	tx := newUACTransaction(ctx, method, transport, target)
	tx.manager = m
	tx.reliable = isReliableTransport(transport)
	m.Register(tx.Branch, tx)
	return tx
}

func isReliableTransport(t Transport) bool {
	switch t.(type) {
	case *TCPTransport:
		return true
	default:
		return false
	}
}

func (m *UACManager) Register(branch string, tx *UACTransaction) {
	m.mu.Lock()
	m.pending[branch] = tx
	m.mu.Unlock()
	log.Printf("UACManager: registered %s [%s]", tx.Method, branch)
}

func (m *UACManager) Deregister(branch string) {
	m.mu.Lock()
	delete(m.pending, branch)
	m.mu.Unlock()
	log.Printf("UACManager: deregistered [%s]", branch)
}

func (m *UACManager) Get(branch string) *UACTransaction {
	m.mu.Lock()
	tx := m.pending[branch]
	m.mu.Unlock()
	return tx
}

func (m *UACManager) HandleResponse(msg *proto.SIPMessage) {
	branch := msg.ViaBranch()
	if branch == "" {
		return
	}

	m.mu.Lock()
	tx, exists := m.pending[branch]
	m.mu.Unlock()

	if !exists {
		return
	}

	tx.HandleResponse(msg)
}
