package sip

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/proto"
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
	UACStateCalling UACState = iota
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
	transport  Transport
	ctx        context.Context
	timerD     *time.Timer
	Errors     chan error
	cancel     context.CancelFunc
	manager    *UACManager
	request    *proto.SIPMessage
	Responses  chan *proto.SIPMessage
	target     *Target
	logger     *slog.Logger
	timerA     *time.Timer
	timerB     *time.Timer
	Branch     string
	Method     proto.SIPMethod
	retxCount  int
	state      UACState
	stateMu    sync.Mutex
	reliable   bool
	t1Override time.Duration // for tests; zero means use global T1
}

func newUACTransaction(ctx context.Context, method proto.SIPMethod, transport Transport, target *Target) *UACTransaction {
	ctx, cancel := context.WithCancel(ctx)
	u := &UACTransaction{
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
	u.logger = logutil.FromContext(ctx).With("branch", u.Branch, "transport", TransportName(transport))
	return u
}

func (u *UACTransaction) Send(req *proto.SIPMessage) error {
	u.stateMu.Lock()
	u.request = req
	u.stateMu.Unlock()

	u.logger.Debug("UAC sending request",
		"method", string(req.Method()),
		"requestURI", req.RequestURI(),
		"callID", req.Headers.GetFirst("Call-ID"))

	if err := u.transport.Send(req, u.target); err != nil {
		return err
	}
	u.logger.Debug("UAC state transition", "state", "Calling", "method", string(u.Method))

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
	u.logger.Debug("UAC response received", "statusCode", sc, "reason", msg.Status())

	switch {
	case sc >= 100 && sc < 200:
		if u.state == UACStateCalling {
			u.state = UACStateProceeding
			if u.timerA != nil {
				u.timerA.Stop()
				u.timerA = nil
			}
			u.logger.Debug("UAC state transition", "state", "Proceeding", "statusCode", sc)
		}
		select {
		case u.Responses <- msg:
		default:
		}

	case sc >= 200 && sc < 300:
		if u.state == UACStateCalling || u.state == UACStateProceeding {
			u.transitionToCompleted()
			u.logger.Debug("UAC state transition", "state", "Completed", "statusCode", sc)
		}
		select {
		case u.Responses <- msg:
		default:
		}

	case sc >= 300:
		if u.state == UACStateCalling || u.state == UACStateProceeding {
			u.transitionToCompleted()
			u.logger.Debug("UAC state transition", "state", "Completed", "statusCode", sc)
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
		u.logger.Debug("UAC terminated", "reason", "Timer D")
		if u.manager != nil {
			u.manager.Deregister(u.Branch)
		}
	})
}

func (u *UACTransaction) startTimerA() {
	u.stateMu.Lock()
	u.retxCount = 0
	u.stateMu.Unlock()
	u.scheduleTimerA()
}

func (u *UACTransaction) scheduleTimerA() {
	u.stateMu.Lock()
	t1 := u.t1Override
	retx := u.retxCount
	u.stateMu.Unlock()
	if t1 == 0 {
		t1 = T1
	}
	interval := t1 << uint(retx)
	if interval > T2 {
		interval = T2
	}
	u.stateMu.Lock()
	u.timerA = time.AfterFunc(interval, func() {
		u.stateMu.Lock()
		if u.state != UACStateCalling {
			u.stateMu.Unlock()
			return
		}
		if err := u.transport.Send(u.request, u.target); err != nil {
			u.logger.Error("UAC retransmit error", "error", err)
			u.stateMu.Unlock()
			return
		}
		u.logger.Debug("UAC retransmit", "count", u.retxCount+1)
		u.retxCount++
		u.stateMu.Unlock()
		u.scheduleTimerA()
	})
	u.stateMu.Unlock()
}

func (u *UACTransaction) startTimerB() {
	u.stateMu.Lock()
	t1 := u.t1Override
	u.stateMu.Unlock()
	if t1 == 0 {
		t1 = T1
	}
	d := 64 * t1
	u.stateMu.Lock()
	u.timerB = time.AfterFunc(d, func() {
		u.stateMu.Lock()
		u.state = UACStateTerminated
		if u.timerA != nil {
			u.timerA.Stop()
			u.timerA = nil
		}
		u.stateMu.Unlock()
		u.logger.Debug("UAC terminated", "reason", "Timer B")
		select {
		case u.Errors <- TimeoutError{Method: u.Method}:
		default:
		}
		if u.manager != nil {
			u.manager.Deregister(u.Branch)
		}
	})
	u.stateMu.Unlock()
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
		u.logger.Error("UAC ACK send error", "error", err)
	} else {
		u.logger.Debug("UAC ACK sent", "statusCode", resp.StatusCode())
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

// SendCancel constructs and sends a CANCEL request matching the original
// INVITE per RFC 3261 §9.1.
// The CANCEL uses the same Via branch, Call-ID, From, To, and CSeq sequence
// number as the INVITE. It is only valid in Calling or Proceeding state.
func (u *UACTransaction) SendCancel() error {
	u.stateMu.Lock()
	if u.state != UACStateCalling && u.state != UACStateProceeding {
		u.stateMu.Unlock()
		return fmt.Errorf("cannot cancel %s transaction in state %v", u.Method, u.state)
	}
	u.stateMu.Unlock()

	cancel := proto.NewRequest(proto.SIPMethodCANCEL, u.request.RequestURI())

	viaTransport := TransportName(u.transport)
	cancel.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=%s",
		viaTransport, u.request.ViaSentBy(), u.Branch))

	if fromVals := u.request.Headers["From"]; len(fromVals) > 0 {
		cancel.Headers.Add("From", fromVals[0])
	}
	if toVals := u.request.Headers["To"]; len(toVals) > 0 {
		cancel.Headers.Add("To", toVals[0])
	}
	cancel.Headers.Add("Call-ID", u.request.Headers.GetFirst("Call-ID"))
	cancel.CSeq = proto.CSeq{Method: proto.SIPMethodCANCEL, Seq: u.request.CSeq.Seq}
	cancel.Headers.Add("Max-Forwards", "70")
	cancel.Headers.Add("Content-Length", "0")

	u.logger.Debug("UAC sending CANCEL",
		"callID", u.request.Headers.GetFirst("Call-ID"),
		"branch", u.Branch,
		"cseq", cancel.CSeq.Seq)

	return u.transport.Send(cancel, u.target)
}

type TimeoutError struct {
	Method proto.SIPMethod
}

func (e TimeoutError) Error() string {
	return string(e.Method) + " transaction timed out"
}

type UACManager struct {
	pending map[string]*UACTransaction
	mu      sync.Mutex
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
	tx.logger.Debug("UACManager registered", "method", string(tx.Method))
}

func (m *UACManager) Deregister(branch string) {
	m.mu.Lock()
	delete(m.pending, branch)
	m.mu.Unlock()
	slog.Debug("UACManager deregistered", "branch", branch)
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

	sc := msg.StatusCode()
	slog.Debug("UACManager response received",
		"branch", branch,
		"statusCode", sc,
		"reason", msg.Status(),
		"callID", msg.Headers.GetFirst("Call-ID"))

	m.mu.Lock()
	tx, exists := m.pending[branch]
	m.mu.Unlock()

	if !exists {
		return
	}

	tx.HandleResponse(msg)
}
