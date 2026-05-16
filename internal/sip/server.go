package sip

import (
	"log"
	"sync"

	"gitub.com/thorsager/trec/proto"
)

// Server is a SIP server that listens on UDP and TCP, manages transactions,
// and dispatches requests to registered method handlers.
type Server struct {
	udpTransport *UDPTransport
	tcpTransport *TCPTransport
	txMgr        *TransactionManager
	handlers     map[proto.SIPMethod]RequestHandler
	mu           sync.Mutex
	wg           sync.WaitGroup
	started      bool
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
	log.Printf("SIP server listening on UDP and TCP")
}

// dispatch reads messages from a transport and routes them.
func (s *Server) dispatch(transport Transport) {
	defer s.wg.Done()
	for ev := range transport.Receive() {
		s.route(ev, transport)
	}
}

// route processes one incoming message.
func (s *Server) route(ev MessageEvent, transport Transport) {
	if !ev.Msg.IsRequest() {
		return
	}

	method := ev.Msg.Method()

	// ACK for non-2xx is routed to the matching INVITE transaction.
	if method == proto.SIPMethodACK {
		s.txMgr.HandleACK(ev, transport)
		return
	}

	handler := s.handlers[method]
	if handler == nil {
		handler = s.defaultHandler
	}

	s.txMgr.HandleRequest(ev, transport, handler)
}

// defaultHandler responds with 501 Not Implemented for unregistered methods.
func (s *Server) defaultHandler(req *proto.SIPMessage, tx Transaction) {
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
	log.Printf("SIP server stopped")
	return err
}
