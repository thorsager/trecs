package sip

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/proto"
)

// ReliableProvisionalManager handles RFC 3262 reliable provisional responses
// for the UAS side. It manages retransmission of 1xx responses and matching
// of incoming PRACK requests.
type ReliableProvisionalManager struct {
	mu      sync.Mutex
	pending map[string]*pendingReliable
}

type pendingReliable struct {
	callID     string
	rseqsSent  map[int]bool
	latestRSeq int
	latestResp *proto.SIPMessage
	timer      *time.Timer
	retxC      int
	transport  Transport
	target     Target
	done       bool
	onTimeout  func()
	logger     *slog.Logger
	counter    int
	manager    *ReliableProvisionalManager
	cseq       int
	method     string
}

func NewReliableProvisionalManager() *ReliableProvisionalManager {
	return &ReliableProvisionalManager{
		pending: make(map[string]*pendingReliable),
	}
}

func copyTarget(t Target) Target {
	return Target{
		Addr:   t.Addr,
		Conn:   t.Conn,
		FlowID: t.FlowID,
	}
}

func (m *ReliableProvisionalManager) SendReliable(
	ctx context.Context,
	tx Transaction,
	res *proto.SIPMessage,
	callID string,
	onTimeout func(),
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pr := m.pending[callID]
	if pr == nil {
		pr = &pendingReliable{
			callID:    callID,
			rseqsSent: make(map[int]bool),
			transport: tx.Transport(),
			target:    copyTarget(tx.Target()),
			logger:    logutil.FromContext(ctx).With("callID", callID),
			onTimeout: onTimeout,
			manager:   m,
			cseq:      res.CSeq.Seq,
			method:    string(res.CSeq.Method),
		}
		m.pending[callID] = pr
	}

	pr.counter++
	rseq := pr.counter
	pr.rseqsSent[rseq] = true
	pr.latestRSeq = rseq
	pr.done = false

	res.Headers.Set("RSeq", []string{strconv.Itoa(rseq)})
	res.Headers.Add("Require", "100rel")

	pr.latestResp = res

	pr.logger.Debug("sending reliable provisional",
		"rseq", rseq,
		"statusCode", res.StatusCode(),
		"reason", res.Status())

	tx.Respond(res)

	pr.stopTimer()
	pr.retxC = 0
	pr.startTimer()
}

func (m *ReliableProvisionalManager) HandlePRACK(ctx context.Context, prack *proto.SIPMessage, tx Transaction) {
	callID := prack.Headers.GetFirst("Call-ID")
	if callID == "" {
		tx.Respond(proto.NewResponse(prack, 400, "Bad Request"))
		return
	}
	rackStr := prack.Headers.GetFirst("RAck")
	if rackStr == "" {
		tx.Respond(proto.NewResponse(prack, 400, "Bad Request"))
		return
	}

	parts := strings.Fields(rackStr)
	if len(parts) < 3 {
		tx.Respond(proto.NewResponse(prack, 400, "Bad Request"))
		return
	}
	rseq, err := strconv.Atoi(parts[0])
	if err != nil {
		tx.Respond(proto.NewResponse(prack, 400, "Bad Request"))
		return
	}
	rackCSeq, err := strconv.Atoi(parts[1])
	if err != nil {
		tx.Respond(proto.NewResponse(prack, 400, "Bad Request"))
		return
	}
	rackMethod := parts[2]

	ctx = logutil.WithValues(ctx, "callID", callID, "rackRSeq", rseq)
	log := logutil.FromContext(ctx)

	m.mu.Lock()
	pr := m.pending[callID]
	if pr == nil || pr.done {
		m.mu.Unlock()
		log.Warn("PRACK for unknown/expired reliable provisional")
		tx.Respond(proto.NewResponse(prack, 481, "Call/Transaction Does Not Exist"))
		return
	}

	if !pr.rseqsSent[rseq] {
		m.mu.Unlock()
		log.Warn("PRACK with unknown RSeq", "rseq", rseq, "sentRSeqs", pr.rseqsSent)
		tx.Respond(proto.NewResponse(prack, 481, "Call/Transaction Does Not Exist"))
		return
	}

	if rackCSeq != pr.cseq || !strings.EqualFold(rackMethod, pr.method) {
		m.mu.Unlock()
		log.Warn("PRACK with mismatched CSeq or method",
			"rackCSeq", rackCSeq, "rackMethod", rackMethod,
			"expectedCSeq", pr.cseq, "expectedMethod", pr.method)
		tx.Respond(proto.NewResponse(prack, 481, "Call/Transaction Does Not Exist"))
		return
	}

	delete(pr.rseqsSent, rseq)
	if rseq == pr.latestRSeq {
		pr.stopTimer()
	}

	log.Info("PRACK matched reliable provisional", "rseq", rseq)
	m.mu.Unlock()

	res := proto.NewResponse(prack, 200, "OK")
	res.Headers.Set("Content-Length", []string{"0"})
	tx.Respond(res)
}

func (m *ReliableProvisionalManager) Cancel(callID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pr := m.pending[callID]
	if pr == nil {
		return
	}
	pr.done = true
	pr.stopTimer()
	delete(m.pending, callID)
}

func (pr *pendingReliable) stopTimer() {
	if pr.timer != nil {
		pr.timer.Stop()
		pr.timer = nil
	}
}

func (pr *pendingReliable) startTimer() {
	pr.scheduleRetransmit()
}

func (pr *pendingReliable) scheduleRetransmit() {
	interval := T1 << uint(pr.retxC)
	if interval > T2 {
		interval = T2
	}

	pr.timer = time.AfterFunc(interval, func() {
		m := pr.manager
		m.mu.Lock()
		defer m.mu.Unlock()

		if pr.done {
			return
		}

		pr.retxC++
		if pr.retxC >= 7 {
			pr.logger.Error("reliable provisional timed out, no PRACK received",
				"rseq", pr.latestRSeq,
				"retxCount", pr.retxC)
			pr.done = true
			cb := pr.onTimeout
			delete(m.pending, pr.callID)
			if cb != nil {
				cb()
			}
			return
		}

		if pr.transport == nil {
			pr.logger.Warn("retransmit skipped: nil transport")
			return
		}

		pr.logger.Debug("retransmitting reliable provisional",
			"rseq", pr.latestRSeq,
			"retxCount", pr.retxC)

		if err := pr.transport.Send(pr.latestResp, &pr.target); err != nil {
			pr.logger.Error("retransmit error", "error", err)
		}

		pr.scheduleRetransmit()
	})
}
