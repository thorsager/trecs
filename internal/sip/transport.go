package sip

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/proto"
)

const (
	readBufSize  = 65535
	tcpKeepAlive = 5 * time.Minute
	tcpMaxConns  = 1000
)

// Target identifies where to send a SIP response.
type Target struct {
	Addr   net.Addr
	Conn   net.Conn
	FlowID string
}

// syncWriteConn wraps a net.Conn with a mutex to serialize concurrent writes.
type syncWriteConn struct {
	net.Conn
	mu sync.Mutex
}

func (c *syncWriteConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.Write(b)
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
	logger    *slog.Logger
	closeOnce sync.Once
}

// SetLogger sets the logger for trace output.
func (t *UDPTransport) SetLogger(l *slog.Logger) {
	t.logger = l
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
		if t.logger != nil {
			logutil.Trace(t.logger, "SIP message received",
				"protocol", "UDP",
				"local", t.conn.LocalAddr().String(),
				"remote", addr.String(),
				"payload", string(buf[:n]),
			)
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
	if len(data) > 1300 {
		return fmt.Errorf("sip: UDP message size %d exceeds RFC 3261 limit of 1300 bytes", len(data))
	}
	udpAddr, ok := target.Addr.(*net.UDPAddr)
	if !ok {
		return net.InvalidAddrError("UDP transport requires a UDP address")
	}
	if t.logger != nil {
		logutil.Trace(t.logger, "SIP message sent",
			"protocol", "UDP",
			"local", t.conn.LocalAddr().String(),
			"remote", udpAddr.String(),
			"payload", string(data),
		)
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
	keepaliveCtx      context.Context
	listener          *net.TCPListener
	messages          chan MessageEvent
	done              chan struct{}
	connSem           chan struct{}
	pool              *FlowPool
	keepaliveCancel   context.CancelFunc
	logger            *slog.Logger
	wg                sync.WaitGroup
	keepaliveInterval time.Duration
	closeOnce         sync.Once
}

// SetLogger sets the logger for trace output.
func (t *TCPTransport) SetLogger(l *slog.Logger) {
	t.logger = l
}

// LocalAddr returns the bound TCP address.
func (t *TCPTransport) LocalAddr() net.Addr {
	return t.listener.Addr()
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
	ctx, cancel := context.WithCancel(context.Background())
	t := &TCPTransport{
		listener:          listener,
		messages:          make(chan MessageEvent, 100),
		done:              make(chan struct{}),
		connSem:           make(chan struct{}, tcpMaxConns),
		pool:              NewFlowPool(nil),
		keepaliveInterval: DefaultKeepaliveInterval,
		keepaliveCtx:      ctx,
		keepaliveCancel:   cancel,
	}
	return t, nil
}

func (t *TCPTransport) Pool() *FlowPool {
	return t.pool
}

func (t *TCPTransport) SetOnDead(fn func(string)) {
	t.pool.SetOnDead(fn)
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
		tcpConn.SetKeepAlive(true)               //nolint:errcheck
		tcpConn.SetKeepAlivePeriod(tcpKeepAlive) //nolint:errcheck
		t.wg.Add(1)
		go t.handleConnection(conn)
	}
}

func (t *TCPTransport) handleConnection(conn net.Conn) {
	defer t.wg.Done()
	defer conn.Close()
	<-t.connSem

	swc := &syncWriteConn{Conn: conn}
	fc := t.pool.Register(swc)
	flowID := fc.Key.String()

	t.startReader(conn, swc, fc, flowID)
}

// HandleOutbound registers a client-dialed TCP connection with the flow pool
// and starts reader/keepalive goroutines so that responses are routed through
// the transport's Receive channel. The wrapped connection is returned so the
// caller can update its Target.Conn.
func (t *TCPTransport) HandleOutbound(conn net.Conn) (net.Conn, error) {
	swc := &syncWriteConn{Conn: conn}
	fc := t.pool.Register(swc)
	flowID := fc.Key.String()

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.startReader(conn, swc, fc, flowID)
	}()

	return swc, nil
}

// startReader runs the SIP read loop for a TCP connection in the caller's
// goroutine. It returns when the connection dies or is shut down. Used by
// both inbound (handleConnection) and outbound (HandleOutbound) paths.
func (t *TCPTransport) startReader(conn net.Conn, swc *syncWriteConn, fc *FlowConn, flowID string) {
	log := slog.With("flowID", flowID)

	ctx, cancel := context.WithCancel(t.keepaliveCtx)
	fc.cancel = cancel

	kt := NewKeepaliveTracker(t.keepaliveInterval, log)
	go kt.Run(ctx, swc, flowID)

	defer cancel()
	defer t.pool.RemoveDead(flowID)
	defer conn.Close()

	br := bufio.NewReaderSize(conn, readBufSize)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.done:
			return
		default:
		}

		if err := t.drainCRLFKeepalive(br, conn, kt); err != nil {
			log.Error("TCP flow read error", "error", err)
			return
		}

		conn.SetReadDeadline(time.Now().Add(tcpReadTimeout)) //nolint:errcheck //nolint:errcheck
		msg, err := proto.UnmarshalSIP(br)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return
		}
		kt.UpdateActivity()
		fc.LastUsed = time.Now()

		if t.logger != nil {
			logutil.Trace(t.logger, "SIP message received",
				"protocol", "TCP",
				"local", conn.LocalAddr().String(),
				"remote", swc.RemoteAddr().String(),
				"flowID", flowID,
				"payload", msg.String(),
			)
		}

		select {
		case t.messages <- MessageEvent{Msg: msg, Target: Target{Addr: swc.RemoteAddr(), Conn: swc, FlowID: flowID}}:
		case <-t.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (t *TCPTransport) drainCRLFKeepalive(br *bufio.Reader, conn net.Conn, kt *KeepaliveTracker) error {
	conn.SetReadDeadline(time.Now().Add(tcpReadTimeout)) //nolint:errcheck
	for {
		b, err := br.ReadByte()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) {
				return nil
			}
			return err
		}
		if b != '\r' {
			br.UnreadByte() //nolint:errcheck
			return nil
		}
		next, err := br.ReadByte()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) {
				br.UnreadByte() //nolint:errcheck
				return nil
			}
			return err
		}
		if next != '\n' {
			return nil
		}
		// Read second \r to distinguish keepalive (\r\n\r\n) from stray CRLF (\r\n).
		b, err = br.ReadByte()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) {
				br.UnreadByte() //nolint:errcheck // unread the \n
				return nil
			}
			return err
		}
		if b != '\r' {
			br.UnreadByte() //nolint:errcheck
			return nil
		}
		next, err = br.ReadByte()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) {
				br.UnreadByte() //nolint:errcheck // unread the \r
				return nil
			}
			return err
		}
		if next != '\n' {
			return nil
		}
		// Full double-CRLF keepalive. Per RFC 5626 §5.4, respond with pong.
		conn.SetWriteDeadline(time.Now().Add(tcpReadTimeout)) //nolint:errcheck
		conn.Write([]byte("\r\n"))                            //nolint:errcheck
		conn.SetWriteDeadline(time.Time{})                    //nolint:errcheck
		kt.UpdateActivity()
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
	if t.logger != nil {
		logutil.Trace(t.logger, "SIP message sent",
			"protocol", "TCP",
			"local", target.Conn.LocalAddr().String(),
			"remote", target.Conn.RemoteAddr().String(),
			"payload", string(data),
		)
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
		t.keepaliveCancel()
		err = t.listener.Close()
	})
	t.wg.Wait()
	return err
}

func TargetFromContact(contactURI string) (*Target, string, error) {
	host, port, transport := extractSIPURI(contactURI)

	if transport == "TCP" {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(context.Background(), "tcp", addr)
		if err != nil {
			return nil, "", err
		}
		slog.Info("Outbound TCP connection", "addr", addr)
		return &Target{Conn: conn}, "TCP", nil
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, "", err
	}
	return &Target{Addr: udpAddr}, "UDP", nil
}
