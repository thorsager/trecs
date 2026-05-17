package sip

import (
	"bufio"
	"net"
	"sync"
	"time"

	"gitub.com/thorsager/trec/proto"
)

const (
	readBufSize    = 65535
	tcpKeepAlive   = 5 * time.Minute
	tcpMaxConns    = 1000
)

// Target identifies where to send a SIP response.
type Target struct {
	Addr net.Addr
	Conn net.Conn
}

// MessageEvent carries a parsed SIP message with source info.
type MessageEvent struct {
	Msg    *proto.SIPMessage
	Target Target
}

// Transport is the interface for SIP message I/O.
type Transport interface {
	Send(msg *proto.SIPMessage, target *Target) error
	Receive() <-chan MessageEvent
	Close() error
}

// UDPTransport implements Transport over UDP.
type UDPTransport struct {
	conn      *net.UDPConn
	messages  chan MessageEvent
	closeOnce sync.Once
}

// NewUDPTransport resolves addr and binds a UDP socket.
func NewUDPTransport(addr string) (*UDPTransport, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	t := &UDPTransport{
		conn:     conn,
		messages: make(chan MessageEvent, 100),
	}
	return t, nil
}

// LocalAddr returns the bound UDP address.
func (t *UDPTransport) LocalAddr() net.Addr {
	return t.conn.LocalAddr()
}

// Start begins reading UDP datagrams in a background goroutine.
func (t *UDPTransport) Start() {
	go t.readLoop()
}

func (t *UDPTransport) readLoop() {
	buf := make([]byte, readBufSize)
	defer close(t.messages)
	for {
		n, addr, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		msg, err := proto.UnmarshalSIPDatagram(buf[:n])
		if err != nil {
			continue
		}
		t.messages <- MessageEvent{Msg: msg, Target: Target{Addr: addr}}
	}
}

// Receive returns a channel of parsed SIP messages from UDP.
func (t *UDPTransport) Receive() <-chan MessageEvent {
	return t.messages
}

// Send marshals msg and writes it as a UDP datagram to target.Addr.
func (t *UDPTransport) Send(msg *proto.SIPMessage, target *Target) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	udpAddr, ok := target.Addr.(*net.UDPAddr)
	if !ok {
		return net.InvalidAddrError("UDP transport requires a UDP address")
	}
	_, err = t.conn.WriteToUDP(data, udpAddr)
	return err
}

// Close stops the UDP transport and closes the receive channel.
func (t *UDPTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		err = t.conn.Close()
	})
	return err
}

const tcpReadTimeout = 1 * time.Second

// TCPTransport implements Transport over TCP.
type TCPTransport struct {
	listener  *net.TCPListener
	messages  chan MessageEvent
	wg        sync.WaitGroup
	closeOnce sync.Once
	done      chan struct{}
	connSem   chan struct{}
}

// NewTCPTransport resolves addr and starts a TCP listener.
func NewTCPTransport(addr string) (*TCPTransport, error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return nil, err
	}
	t := &TCPTransport{
		listener: listener,
		messages: make(chan MessageEvent, 100),
		done:     make(chan struct{}),
		connSem:  make(chan struct{}, tcpMaxConns),
	}
	return t, nil
}

// Start begins accepting TCP connections in a background goroutine.
func (t *TCPTransport) Start() {
	go t.acceptLoop()
}

func (t *TCPTransport) acceptLoop() {
	defer close(t.messages)
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			return
		}
		select {
		case t.connSem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		tcpConn := conn.(*net.TCPConn)
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(tcpKeepAlive)
		t.wg.Add(1)
		go t.handleConnection(conn)
	}
}

func (t *TCPTransport) handleConnection(conn net.Conn) {
	defer t.wg.Done()
	defer conn.Close()
	<-t.connSem

	br := bufio.NewReaderSize(conn, readBufSize)
	for {
		conn.SetReadDeadline(time.Now().Add(tcpReadTimeout))
		msg, err := proto.UnmarshalSIP(br)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				select {
				case <-t.done:
					return
				default:
					continue
				}
			}
			return
		}
		select {
		case t.messages <- MessageEvent{Msg: msg, Target: Target{Addr: conn.RemoteAddr(), Conn: conn}}:
		case <-t.done:
			return
		}
	}
}

// Receive returns a channel of parsed SIP messages from TCP.
func (t *TCPTransport) Receive() <-chan MessageEvent {
	return t.messages
}

// Send marshals msg and writes it to target.Conn.
func (t *TCPTransport) Send(msg *proto.SIPMessage, target *Target) error {
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	if target.Conn == nil {
		return net.InvalidAddrError("TCP transport requires a connection")
	}
	_, err = target.Conn.Write(data)
	return err
}

// Close stops accepting, signals all connection handlers, waits for
// them to finish, and closes the receive channel.
func (t *TCPTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.done)
		err = t.listener.Close()
	})
	t.wg.Wait()
	return err
}
