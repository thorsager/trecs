package sip

import (
	"net"
	"testing"
	"time"
)

func TestFlowKeyFromConn(t *testing.T) {
	local := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060}
	remote := &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 54321}

	conn := &mockTCPAddrConn{
		local:  local,
		remote: remote,
	}

	key := FlowKeyFromConn(conn)
	if key.SourceIP != "192.168.1.5" {
		t.Fatalf("expected SourceIP 192.168.1.5, got %s", key.SourceIP)
	}
	if key.SourcePort != 54321 {
		t.Fatalf("expected SourcePort 54321, got %d", key.SourcePort)
	}
	if key.DestIP != "10.0.0.1" {
		t.Fatalf("expected DestIP 10.0.0.1, got %s", key.DestIP)
	}
	if key.DestPort != 5060 {
		t.Fatalf("expected DestPort 5060, got %d", key.DestPort)
	}
	if key.Transport != "TCP" {
		t.Fatalf("expected Transport TCP, got %s", key.Transport)
	}

	id := key.String()
	expected := "TCP:192.168.1.5:54321→10.0.0.1:5060"
	if id != expected {
		t.Fatalf("expected %q, got %q", expected, id)
	}
}

func TestFlowPoolRegisterAndGet(t *testing.T) {
	pool := NewFlowPool(nil)

	conn := &mockTCPAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060},
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 54321},
	}
	key := FlowKeyFromConn(conn)

	fc := pool.Register(conn)
	if fc == nil {
		t.Fatal("Register returned nil")
	}
	if fc.Key.String() != key.String() {
		t.Fatalf("expected key %s, got %s", key.String(), fc.Key.String())
	}

	got := pool.Get(key)
	if got == nil {
		t.Fatal("Get returned nil for registered flow")
	}
	if got.Key.String() != key.String() {
		t.Fatalf("Get returned wrong key: %s", got.Key.String())
	}

	pool.Unregister(key)
	got = pool.Get(key)
	if got != nil {
		t.Fatal("Get should return nil after Unregister")
	}
}

func TestFlowPoolGetByFlowID(t *testing.T) {
	pool := NewFlowPool(nil)

	conn := &mockTCPAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060},
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 54321},
	}
	fc := pool.Register(conn)

	got := pool.GetByFlowID(fc.Key.String())
	if got == nil {
		t.Fatal("GetByFlowID returned nil for registered flow")
	}
	if got.Key.String() != fc.Key.String() {
		t.Fatalf("expected key %s, got %s", fc.Key.String(), got.Key.String())
	}

	got = pool.GetByFlowID("nonexistent-flow")
	if got != nil {
		t.Fatal("GetByFlowID should return nil for unknown flow")
	}
}

func TestFlowPoolGetByAddr(t *testing.T) {
	pool := NewFlowPool(nil)

	conn := &mockTCPAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060},
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 54321},
	}

	pool.Register(conn)

	fc := pool.GetByAddr("192.168.1.5", 54321)
	if fc == nil {
		t.Fatal("GetByAddr returned nil for matching address")
	}

	fc = pool.GetByAddr("192.168.1.5", 9999)
	if fc != nil {
		t.Fatal("GetByAddr should return nil for non-matching port")
	}

	fc = pool.GetByAddr("10.0.0.99", 54321)
	if fc != nil {
		t.Fatal("GetByAddr should return nil for non-matching host")
	}
}

func TestFlowPoolRemoveDead(t *testing.T) {
	var deadFlowID string
	pool := NewFlowPool(func(flowID string) {
		deadFlowID = flowID
	})

	conn := &mockTCPAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060},
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 54321},
	}
	fc := pool.Register(conn)

	pool.RemoveDead(fc.Key.String())
	if deadFlowID != fc.Key.String() {
		t.Fatalf("expected onDead callback with %s, got %s", fc.Key.String(), deadFlowID)
	}

	if pool.Get(fc.Key) != nil {
		t.Fatal("flow should be removed after RemoveDead")
	}
}

func TestFlowPoolLen(t *testing.T) {
	pool := NewFlowPool(nil)
	if pool.Len() != 0 {
		t.Fatalf("expected Len 0, got %d", pool.Len())
	}

	conn1 := &mockTCPAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060},
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 54321},
	}
	conn2 := &mockTCPAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060},
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.6"), Port: 54322},
	}

	pool.Register(conn1)
	pool.Register(conn2)

	if pool.Len() != 2 {
		t.Fatalf("expected Len 2, got %d", pool.Len())
	}
}

func TestFlowPoolSetOnDead(t *testing.T) {
	called := false
	pool := NewFlowPool(nil)

	pool.SetOnDead(func(flowID string) {
		called = true
	})

	conn := &mockTCPAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060},
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 54321},
	}
	fc := pool.Register(conn)
	pool.RemoveDead(fc.Key.String())

	if !called {
		t.Fatal("SetOnDead callback was not called")
	}
}

type mockTCPAddrConn struct {
	local  net.Addr
	remote net.Addr
}

func (m *mockTCPAddrConn) Read(b []byte) (int, error)         { return 0, nil }
func (m *mockTCPAddrConn) Write(b []byte) (int, error)        { return len(b), nil }
func (m *mockTCPAddrConn) Close() error                       { return nil }
func (m *mockTCPAddrConn) LocalAddr() net.Addr                { return m.local }
func (m *mockTCPAddrConn) RemoteAddr() net.Addr               { return m.remote }
func (m *mockTCPAddrConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockTCPAddrConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockTCPAddrConn) SetWriteDeadline(t time.Time) error { return nil }
