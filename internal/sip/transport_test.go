package sip

import (
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/proto"
)

var validSIP = "OPTIONS sip:server SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKtest-branch\r\n" +
	"From: <sip:test@localhost>;tag=test-tag\r\n" +
	"To: <sip:server@localhost>\r\n" +
	"Call-ID: test-call-id\r\n" +
	"CSeq: 1 OPTIONS\r\n" +
	"Content-Length: 0\r\n\r\n"

func dialTCP(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	require.NoError(t, err, "TCP dial")
	return conn
}

func readEvent(t *testing.T, ch <-chan MessageEvent, timeout time.Duration) MessageEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for message event after %v", timeout)
	}
	return MessageEvent{}
}

func expectNoEvent(t *testing.T, ch <-chan MessageEvent, timeout time.Duration) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %s", ev.Msg.StartLine())
	case <-time.After(timeout):
	}
}

// --- UDP Transport ---

func TestUDPBasicSendReceive(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15070")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer client.Close()

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15070")
	_, err = client.WriteToUDP([]byte(validSIP), serverAddr)
	require.NoError(t, err)

	ev := readEvent(t, transport.Receive(), 3*time.Second)
	assert.Equal(t, proto.SIPMethodOPTIONS, ev.Msg.Method())
	assert.True(t, ev.Msg.IsRequest())
	assert.NotNil(t, ev.Target.Addr)
}

func TestUDPOrderPreserved(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15071")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer client.Close()

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15071")
	n := 5
	for i := range n {
		msg := buildSIPRequest(i)
		client.WriteToUDP([]byte(msg), serverAddr)
	}

	for i := range n {
		ev := readEvent(t, transport.Receive(), 3*time.Second)
		assert.Equal(t, proto.SIPMethodOPTIONS, ev.Msg.Method(), "event %d", i)
	}
}

func TestUDPInvalidDatagramDropped(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15072")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer client.Close()

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15072")

	client.WriteToUDP([]byte("garbage data\r\n"), serverAddr)
	client.WriteToUDP([]byte("NOT SIP/2.0\r\n\r\n"), serverAddr)
	client.WriteToUDP([]byte(validSIP), serverAddr)

	ev := readEvent(t, transport.Receive(), 3*time.Second)
	assert.Equal(t, proto.SIPMethodOPTIONS, ev.Msg.Method())
}

func TestUDPCloseWhileIdle(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15073")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()

	require.NoError(t, transport.Close())

	_, ok := <-transport.Receive()
	assert.False(t, ok, "expected closed channel")

	assert.NoError(t, transport.Close(), "Close (idempotent)")
}

func TestUDPRespondBack(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15074")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer client.Close()

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15074")
	client.WriteToUDP([]byte(validSIP), serverAddr)

	ev := readEvent(t, transport.Receive(), 3*time.Second)

	res := proto.NewResponse(ev.Msg, 200, "OK")
	target := &ev.Target
	require.NoError(t, transport.Send(res, target))

	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := client.ReadFromUDP(buf)
	require.NoError(t, err)
	msg, err := proto.UnmarshalSIPDatagram(buf[:n])
	require.NoError(t, err)
	assert.Equal(t, 200, msg.StatusCode())
}

// --- TCP Transport ---

func TestTCPBasicSendReceive(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15080")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15080")
	defer conn.Close()

	_, err = conn.Write([]byte(validSIP))
	require.NoError(t, err)

	ev := readEvent(t, transport.Receive(), 3*time.Second)
	assert.Equal(t, proto.SIPMethodOPTIONS, ev.Msg.Method())
	assert.NotNil(t, ev.Target.Conn)
}

func TestTCPMultipleMessagesSameConn(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15081")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15081")
	defer conn.Close()

	n := 5
	for i := range n {
		msg := buildSIPRequest(i)
		_, err := conn.Write([]byte(msg))
		require.NoError(t, err, "Write %d", i)
	}

	for i := range n {
		ev := readEvent(t, transport.Receive(), 3*time.Second)
		assert.Equal(t, proto.SIPMethodOPTIONS, ev.Msg.Method(), "event %d", i)
	}
}

func TestTCPRespondBack(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15082")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15082")
	defer conn.Close()

	_, err = conn.Write([]byte(validSIP))
	require.NoError(t, err)

	ev := readEvent(t, transport.Receive(), 3*time.Second)

	res := proto.NewResponse(ev.Msg, 200, "OK")
	target := &ev.Target
	require.NoError(t, transport.Send(res, target))

	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	require.NoError(t, err)
	msg, err := proto.UnmarshalSIPDatagram(buf[:n])
	require.NoError(t, err)
	assert.Equal(t, 200, msg.StatusCode())
}

func TestTCPRespondOnWrongConnFails(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15083")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15083")
	defer conn.Close()

	_, err = conn.Write([]byte(validSIP))
	require.NoError(t, err)

	ev := readEvent(t, transport.Receive(), 3*time.Second)

	res := proto.NewResponse(ev.Msg, 200, "OK")
	fakeTarget := &Target{Addr: ev.Target.Addr, Conn: nil}
	assert.Error(t, transport.Send(res, fakeTarget), "expected error sending with nil conn")
}

func TestTCPCloseWaitsForHandlers(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15084")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()

	conn := dialTCP(t, "127.0.0.1:15084")
	defer conn.Close()

	_, err = conn.Write([]byte(validSIP))
	require.NoError(t, err)

	ev := readEvent(t, transport.Receive(), 3*time.Second)
	require.NotNil(t, ev.Msg)

	done := make(chan struct{})
	go func() {
		transport.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		assert.Fail(t, "Close did not return within timeout")
	}

	_, ok := <-transport.Receive()
	assert.False(t, ok, "expected closed channel after Close")
}

func TestTCPGracefulCloseNoConns(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15085")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()

	require.NoError(t, transport.Close())

	_, ok := <-transport.Receive()
	assert.False(t, ok, "expected closed channel")

	assert.NoError(t, transport.Close(), "Close (idempotent)")
}

func TestTCPInvalidSIPDisconnects(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15086")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15086")
	defer conn.Close()

	_, err = conn.Write([]byte("garbage data\r\n\r\n"))
	require.NoError(t, err)

	expectNoEvent(t, transport.Receive(), 2*time.Second)

	buf := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = conn.Read(buf)
	assert.Error(t, err, "expected connection to be closed after invalid SIP")
}

func TestTCPCloseWithActiveSenders(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:0")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()

	addr := transport.LocalAddr().String()

	n := 3
	conns := make([]net.Conn, n)
	for i := range n {
		conns[i] = dialTCP(t, addr)
		// Bound each write so a blocked sender returns to the select and
		// observes the stop signal instead of hanging on a full TCP buffer.
		_ = conns[i].SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	}

	// Continuously send SIP messages from multiple clients while closing.
	var sendWg sync.WaitGroup
	stop := make(chan struct{})
	for i, c := range conns {
		sendWg.Add(1)
		go func(conn net.Conn, idx int) {
			defer sendWg.Done()
			msg := []byte(buildSIPRequest(idx))
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = conn.Write(msg)
				}
			}
		}(c, i)
	}

	// Let the senders run briefly to ensure startReader goroutines are busy.
	time.Sleep(50 * time.Millisecond)

	// Close must complete without racing concurrent sends to t.messages.
	require.NoError(t, transport.Close())

	close(stop)
	// Close client connections before waiting so any blocked writes are
	// unblocked and the goroutines can exit promptly.
	for _, c := range conns {
		c.Close()
	}
	sendWg.Wait()

	// Close must have closed the receive channel. Drain any events that
	// were buffered before shutdown, then confirm closure.
	drained := false
	for !drained {
		select {
		case _, ok := <-transport.Receive():
			if !ok {
				drained = true
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for receive channel to close")
		}
	}
}

// --- Concurrent connections ---

func TestTCPConcurrentConnections(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15087")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	var wg sync.WaitGroup
	var received atomic.Int32
	n := 10

	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", "127.0.0.1:15087", 3*time.Second)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.Write([]byte(validSIP))
		}()
	}

	go func() {
		for ev := range transport.Receive() {
			if ev.Msg != nil {
				received.Add(1)
			}
		}
	}()

	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	assert.Positive(t, received.Load(), "expected at least one received message")
}

// --- TargetFromContact ---

func TestTargetFromContactUDP(t *testing.T) {
	target, transport, err := TargetFromContact("sip:alice@192.168.1.5:5060")
	require.NoError(t, err)
	assert.Equal(t, "UDP", transport)
	assert.NotNil(t, target.Addr)
	assert.Nil(t, target.Conn)
}

func TestTargetFromContactUDPDefaultPort(t *testing.T) {
	target, transport, err := TargetFromContact("sip:alice@192.168.1.5")
	require.NoError(t, err)
	assert.Equal(t, "UDP", transport)
	udpAddr := target.Addr.(*net.UDPAddr)
	assert.Equal(t, 5060, udpAddr.Port)
}

func TestTargetFromContactUDPNoDialNeeded(t *testing.T) {
	target, transport, err := TargetFromContact("sip:alice@192.168.1.5:5060")
	require.NoError(t, err)
	assert.Equal(t, "UDP", transport)
	assert.NotNil(t, target.Addr)
}

func TestTargetFromContactTCPWithListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	_, port, _ := net.SplitHostPort(listener.Addr().String())
	target, transport, err := TargetFromContact("sip:alice@127.0.0.1:" + port + ";transport=tcp")
	require.NoError(t, err)
	assert.Equal(t, "TCP", transport)
	assert.NotNil(t, target.Conn)
	target.Conn.Close()
}

func TestTargetFromContactTCPDialRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	_, _, err = TargetFromContact("sip:alice@127.0.0.1:" + strconv.Itoa(port) + ";transport=tcp")
	assert.Error(t, err, "expected error dialing closed TCP port")
}

func TestTargetFromContactPreservesTransportParam(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}()

	_, port, _ := net.SplitHostPort(listener.Addr().String())
	target, transport, err := TargetFromContact("sip:alice@127.0.0.1:" + port + ";transport=tcp;ob;lr")
	require.NoError(t, err)
	assert.Equal(t, "TCP", transport)
	assert.NotNil(t, target.Conn)
	target.Conn.Close()
}

func TestTargetFromContactDefaultTransportUDP(t *testing.T) {
	_, transport, err := TargetFromContact("sip:alice@192.168.1.5:5060;ob;lr")
	require.NoError(t, err)
	assert.Equal(t, "UDP", transport)
}

// --- RFC 3261 §18.1.1: UDP message size limit ---

func TestUDPTransport_Send_Exceeds1300Bytes(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15090")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	largeBody := make([]byte, 1400)
	for i := range largeBody {
		largeBody[i] = 'x'
	}
	msg := proto.NewRequest(proto.SIPMethodINVITE, "sip:user@host")
	msg.Headers.Set("Via", []string{"SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKtest"})
	msg.Headers.Set("From", []string{"<sip:test@localhost>;tag=abc"})
	msg.Headers.Set("To", []string{"<sip:user@host>"})
	msg.Headers.Set("Call-ID", []string{"test-call-id"})
	msg.Body = largeBody

	err = transport.Send(msg, &Target{Addr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999}})
	require.Error(t, err, "expected error for UDP message exceeding 1300 bytes")
	assert.Contains(t, err.Error(), "1300")
}

func TestUDPTransport_Send_WithinLimit(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15091")
	require.NoError(t, err)
	transport.SetLogger(logutil.NewTestLogger(t))
	transport.Start()
	defer transport.Close()

	msg := proto.NewRequest(proto.SIPMethodOPTIONS, "sip:server")
	msg.Headers.Set("Via", []string{"SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKtest"})
	msg.Headers.Set("From", []string{"<sip:test@localhost>;tag=abc"})
	msg.Headers.Set("To", []string{"<sip:server@localhost>"})
	msg.Headers.Set("Call-ID", []string{"test-call-id"})

	err = transport.Send(msg, &Target{Addr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9999}})
	if err != nil {
		assert.NotContains(t, err.Error(), "1300", "small message should not trigger 1300-byte limit error")
	}
}

// --- helpers ---

func buildSIPRequest(seq int) string {
	return "OPTIONS sip:server SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bK" + itoa(seq) + "\r\n" +
		"From: <sip:test@localhost>;tag=tag" + itoa(seq) + "\r\n" +
		"To: <sip:server@localhost>\r\n" +
		"Call-ID: call-" + itoa(seq) + "\r\n" +
		"CSeq: " + itoa(seq) + " OPTIONS\r\n" +
		"Content-Length: 0\r\n\r\n"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}
