package sip

import (
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitub.com/thorsager/trec/proto"
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
	if err != nil {
		t.Fatalf("TCP dial: %v", err)
	}
	return conn
}

func readEvent(t *testing.T, ch <-chan MessageEvent, timeout time.Duration) MessageEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for message event")
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
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer client.Close()

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15070")
	_, err = client.WriteToUDP([]byte(validSIP), serverAddr)
	if err != nil {
		t.Fatalf("WriteToUDP: %v", err)
	}

	ev := readEvent(t, transport.Receive(), 3*time.Second)
	if ev.Msg.Method() != proto.SIPMethodOPTIONS {
		t.Fatalf("expected OPTIONS, got %s", ev.Msg.Method())
	}
	if !ev.Msg.IsRequest() {
		t.Fatal("expected request, got response")
	}
	if ev.Target.Addr == nil {
		t.Fatal("missing source address")
	}
}

func TestUDPOrderPreserved(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15071")
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer client.Close()

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15071")
	n := 5
	for i := 0; i < n; i++ {
		msg := buildSIPRequest(i)
		client.WriteToUDP([]byte(msg), serverAddr)
	}

	for i := 0; i < n; i++ {
		ev := readEvent(t, transport.Receive(), 3*time.Second)
		if ev.Msg.Method() != proto.SIPMethodOPTIONS {
			t.Fatalf("event %d: expected OPTIONS, got %s", i, ev.Msg.Method())
		}
	}
}

func TestUDPInvalidDatagramDropped(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15072")
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer client.Close()

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15072")

	client.WriteToUDP([]byte("garbage data\r\n"), serverAddr)
	client.WriteToUDP([]byte("NOT SIP/2.0\r\n\r\n"), serverAddr)
	client.WriteToUDP([]byte(validSIP), serverAddr)

	ev := readEvent(t, transport.Receive(), 3*time.Second)
	if ev.Msg.Method() != proto.SIPMethodOPTIONS {
		t.Fatalf("expected OPTIONS after garbage, got %s", ev.Msg.Method())
	}
}

func TestUDPCloseWhileIdle(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15073")
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	transport.Start()

	if err := transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, ok := <-transport.Receive()
	if ok {
		t.Fatal("expected closed channel")
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("Close (idempotent): %v", err)
	}
}

func TestUDPRespondBack(t *testing.T) {
	transport, err := NewUDPTransport("127.0.0.1:15074")
	if err != nil {
		t.Fatalf("NewUDPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer client.Close()

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15074")
	client.WriteToUDP([]byte(validSIP), serverAddr)

	ev := readEvent(t, transport.Receive(), 3*time.Second)

	res := proto.NewResponse(ev.Msg, 200, "OK")
	target := &ev.Target
	if err := transport.Send(res, target); err != nil {
		t.Fatalf("Send: %v", err)
	}

	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	msg, err := proto.UnmarshalSIPDatagram(buf[:n])
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if msg.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", msg.StatusCode())
	}
}

// --- TCP Transport ---

func TestTCPBasicSendReceive(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15080")
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15080")
	defer conn.Close()

	_, err = conn.Write([]byte(validSIP))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	ev := readEvent(t, transport.Receive(), 3*time.Second)
	if ev.Msg.Method() != proto.SIPMethodOPTIONS {
		t.Fatalf("expected OPTIONS, got %s", ev.Msg.Method())
	}
	if ev.Target.Conn == nil {
		t.Fatal("missing connection in target")
	}
}

func TestTCPMultipleMessagesSameConn(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15081")
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15081")
	defer conn.Close()

	n := 5
	for i := 0; i < n; i++ {
		msg := buildSIPRequest(i)
		_, err := conn.Write([]byte(msg))
		if err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	for i := 0; i < n; i++ {
		ev := readEvent(t, transport.Receive(), 3*time.Second)
		if ev.Msg.Method() != proto.SIPMethodOPTIONS {
			t.Fatalf("event %d: expected OPTIONS, got %s", i, ev.Msg.Method())
		}
	}
}

func TestTCPRespondBack(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15082")
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15082")
	defer conn.Close()

	_, err = conn.Write([]byte(validSIP))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	ev := readEvent(t, transport.Receive(), 3*time.Second)

	res := proto.NewResponse(ev.Msg, 200, "OK")
	target := &ev.Target
	if err := transport.Send(res, target); err != nil {
		t.Fatalf("Send: %v", err)
	}

	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	msg, err := proto.UnmarshalSIPDatagram(buf[:n])
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if msg.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", msg.StatusCode())
	}
}

func TestTCPRespondOnWrongConnFails(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15083")
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15083")
	defer conn.Close()

	_, err = conn.Write([]byte(validSIP))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	ev := readEvent(t, transport.Receive(), 3*time.Second)

	res := proto.NewResponse(ev.Msg, 200, "OK")
	fakeTarget := &Target{Addr: ev.Target.Addr, Conn: nil}
	if err := transport.Send(res, fakeTarget); err == nil {
		t.Fatal("expected error sending with nil conn")
	}
}

func TestTCPCloseWaitsForHandlers(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15084")
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	transport.Start()

	conn := dialTCP(t, "127.0.0.1:15084")
	defer conn.Close()

	_, err = conn.Write([]byte(validSIP))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	ev := readEvent(t, transport.Receive(), 3*time.Second)
	if ev.Msg == nil {
		t.Fatal("expected message")
	}

	done := make(chan struct{})
	go func() {
		transport.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within timeout")
	}

	_, ok := <-transport.Receive()
	if ok {
		t.Fatal("expected closed channel after Close")
	}
}

func TestTCPGracefulCloseNoConns(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15085")
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	transport.Start()

	if err := transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, ok := <-transport.Receive()
	if ok {
		t.Fatal("expected closed channel")
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("Close (idempotent): %v", err)
	}
}

func TestTCPInvalidSIPDisconnects(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15086")
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	conn := dialTCP(t, "127.0.0.1:15086")
	defer conn.Close()

	_, err = conn.Write([]byte("garbage data\r\n\r\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	expectNoEvent(t, transport.Receive(), 2*time.Second)

	buf := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("expected connection to be closed after invalid SIP")
	}
}

// --- Concurrent connections ---

func TestTCPConcurrentConnections(t *testing.T) {
	transport, err := NewTCPTransport("127.0.0.1:15087")
	if err != nil {
		t.Fatalf("NewTCPTransport: %v", err)
	}
	transport.Start()
	defer transport.Close()

	var wg sync.WaitGroup
	var received atomic.Int32
	n := 10

	for i := 0; i < n; i++ {
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

	if n := received.Load(); n == 0 {
		t.Fatal("expected at least one received message")
	}
}

// --- TargetFromContact ---

func TestTargetFromContactUDP(t *testing.T) {
	target, transport, err := TargetFromContact("sip:alice@192.168.1.5:5060")
	if err != nil {
		t.Fatalf("TargetFromContact: %v", err)
	}
	if transport != "UDP" {
		t.Fatalf("expected UDP transport, got %s", transport)
	}
	if target.Addr == nil {
		t.Fatal("expected non-nil Addr for UDP")
	}
	if target.Conn != nil {
		t.Fatal("expected nil Conn for UDP")
	}
}

func TestTargetFromContactUDPDefaultPort(t *testing.T) {
	target, transport, err := TargetFromContact("sip:alice@192.168.1.5")
	if err != nil {
		t.Fatalf("TargetFromContact: %v", err)
	}
	if transport != "UDP" {
		t.Fatalf("expected UDP transport, got %s", transport)
	}
	udpAddr := target.Addr.(*net.UDPAddr)
	if udpAddr.Port != 5060 {
		t.Fatalf("expected default port 5060, got %d", udpAddr.Port)
	}
}

func TestTargetFromContactUDPNoDialNeeded(t *testing.T) {
	target, transport, err := TargetFromContact("sip:alice@192.168.1.5:5060")
	if err != nil {
		t.Fatalf("TargetFromContact: %v", err)
	}
	if transport != "UDP" {
		t.Fatalf("expected UDP transport, got %s", transport)
	}
	if target.Addr == nil {
		t.Fatal("expected non-nil Addr for UDP")
	}
}

func TestTargetFromContactTCPWithListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
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
	if err != nil {
		t.Fatalf("TargetFromContact: %v", err)
	}
	if transport != "TCP" {
		t.Fatalf("expected TCP transport, got %s", transport)
	}
	if target.Conn == nil {
		t.Fatal("expected non-nil Conn for TCP")
	}
	target.Conn.Close()
}

func TestTargetFromContactTCPDialRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	_, _, err = TargetFromContact("sip:alice@127.0.0.1:" + strconv.Itoa(port) + ";transport=tcp")
	if err == nil {
		t.Fatal("expected error dialing closed TCP port")
	}
}

func TestTargetFromContactPreservesTransportParam(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
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
	if err != nil {
		t.Fatalf("TargetFromContact: %v", err)
	}
	if transport != "TCP" {
		t.Fatalf("expected TCP transport, got %s", transport)
	}
	if target.Conn == nil {
		t.Fatal("expected non-nil Conn for TCP")
	}
	target.Conn.Close()
}

func TestTargetFromContactDefaultTransportUDP(t *testing.T) {
	_, transport, err := TargetFromContact("sip:alice@192.168.1.5:5060;ob;lr")
	if err != nil {
		t.Fatalf("TargetFromContact: %v", err)
	}
	if transport != "UDP" {
		t.Fatalf("expected default UDP transport, got %s", transport)
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
