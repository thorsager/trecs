package sip

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/proto"
)

// AckCallback is invoked for every incoming ACK after transaction handling.
type AckCallback func(ctx context.Context, msg *proto.SIPMessage, target Target, transport Transport)

// ResponseHandler is invoked for incoming SIP responses that don't match
// an existing server transaction.
type ResponseHandler func(ctx context.Context, msg *proto.SIPMessage, target Target, transport Transport)

// Server is a SIP server that listens on UDP and TCP, manages transactions,
// and dispatches requests to registered method handlers.
type Server struct {
	udpTransport    *UDPTransport
	tcpTransport    *TCPTransport
	txMgr           *TransactionManager
	handlers        map[proto.SIPMethod]RequestHandler
	ackCallback     AckCallback
	responseHandler ResponseHandler
	mu              sync.Mutex
	wg              sync.WaitGroup
	started         bool
}

// NewServer creates a SIP server listening on addr for both UDP and TCP.
func NewServer(addr string) (*Server, error) {
	udp, err := NewUDPTransport(addr)
	if err != nil {
		return nil, err
	}
	tcp, err := NewTCPTransport(addr)
	if err != nil {
		udp.Close()
		return nil, err
	}
	udp.SetLogger(slog.Default())
	tcp.SetLogger(slog.Default())
	return &Server{
		udpTransport: udp,
		tcpTransport: tcp,
		txMgr:        NewTransactionManager(),
		handlers:     make(map[proto.SIPMethod]RequestHandler),
	}, nil
}

// On registers a handler for the given SIP method.
func (s *Server) On(method proto.SIPMethod, fn RequestHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = fn
}

// AllowMethods returns the comma-separated list of SIP methods that have
// handlers registered on this server. ACK is included since it is handled
// via OnAck. The list is sorted for deterministic output.
func (s *Server) AllowMethods() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	methods := make([]string, 0, len(s.handlers)+1)
	for m := range s.handlers {
		methods = append(methods, string(m))
	}
	methods = append(methods, string(proto.SIPMethodACK))
	sort.Strings(methods)
	return strings.Join(methods, ", ")
}

// OnAck registers a callback for incoming ACK requests. The callback is
// invoked after the transaction manager has handled non-2xx acknowledgments.
func (s *Server) OnAck(fn AckCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ackCallback = fn
}

// OnResponse registers a handler for incoming SIP responses.
func (s *Server) OnResponse(fn ResponseHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responseHandler = fn
}

// SetFlowDeadCallback sets the callback for when a TCP flow dies.
func (s *Server) SetFlowDeadCallback(fn func(string)) {
	s.tcpTransport.SetOnDead(fn)
}

// Pool returns the TCP flow pool.
func (s *Server) Pool() *FlowPool {
	return s.tcpTransport.Pool()
}

// UDPTransport returns the UDP transport.
func (s *Server) UDPTransport() *UDPTransport {
	return s.udpTransport
}

// TCPTransport returns the TCP transport.
func (s *Server) TCPTransport() *TCPTransport {
	return s.tcpTransport
}

// Start begins listening on both transports and starts the dispatch loop.
func (s *Server) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return
	}

	s.udpTransport.Start()
	s.tcpTransport.Start()

	s.wg.Add(2)
	go s.dispatch(s.udpTransport)
	go s.dispatch(s.tcpTransport)

	s.started = true
	slog.Info("SIP server listening on UDP and TCP")
}

// dispatch reads messages from a transport and routes them.
func (s *Server) dispatch(transport Transport) {
	defer s.wg.Done()
	for ev := range transport.Receive() {
		ctx := context.Background()
		ctx = logutil.NewContext(ctx, slog.Default().With("transport", TransportName(transport)))
		s.route(ctx, ev, transport)
	}
}

// route processes one incoming message.
func (s *Server) route(ctx context.Context, ev MessageEvent, transport Transport) {
	if !ev.Msg.IsRequest() {
		if s.responseHandler != nil {
			s.responseHandler(ctx, ev.Msg, ev.Target, transport)
		}
		return
	}

	method := ev.Msg.Method()

	// ACK for non-2xx is routed to the matching INVITE transaction.
	// The AckCallback (if set) receives every ACK including 2xx variants.
	if method == proto.SIPMethodACK {
		s.txMgr.HandleACK(ctx, ev, transport)
		if s.ackCallback != nil {
			s.ackCallback(ctx, ev.Msg, ev.Target, transport)
		}
		return
	}

	// Max-Forwards check per RFC 3261 §8.1.3.2.
	if mf := ev.Msg.Headers.GetFirst("Max-Forwards"); mf != "" {
		maxFwds, err := strconv.Atoi(mf)
		if err == nil {
			if maxFwds <= 0 {
				res := proto.NewResponse(ev.Msg, 483, "Too Many Hops")
				transport.Send(res, &ev.Target) //nolint:errcheck
				return
			}
			ev.Msg.Headers.Set("Max-Forwards", []string{strconv.Itoa(maxFwds - 1)})
		}
	}

	handler := s.handlers[method]
	if handler == nil {
		handler = s.defaultHandler
	}

	s.txMgr.HandleRequest(ctx, ev, transport, handler)
}

// defaultHandler responds with 501 Not Implemented for unregistered methods.
func (s *Server) defaultHandler(ctx context.Context, req *proto.SIPMessage, tx Transaction) {
	res := proto.NewResponse(req, 501, "Not Implemented")
	tx.Respond(res)
}

// Close gracefully shuts down the server.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	var err error
	if e := s.udpTransport.Close(); e != nil {
		err = e
	}
	if e := s.tcpTransport.Close(); e != nil {
		err = e
	}
	s.wg.Wait()
	s.started = false
	slog.Info("SIP server stopped")
	return err
}
