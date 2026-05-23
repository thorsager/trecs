package media_test

import (
	"net"
	"testing"
	"time"

	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/proto"
)

func mustRTP(t *testing.T) *media.RTPConn {
	t.Helper()
	c, err := media.NewRTPConn()
	if err != nil {
		t.Fatalf("NewRTPConn: %v", err)
	}
	return c
}

func v4Addr(t *testing.T, conn *media.RTPConn) *net.UDPAddr {
	t.Helper()
	port := conn.LocalAddr().(*net.UDPAddr).Port
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
}

func TestBridgeForwardsBidirectional(t *testing.T) {
	serverA := mustRTP(t)
	defer serverA.Close()
	serverB := mustRTP(t)
	defer serverB.Close()

	clientA := mustRTP(t)
	defer clientA.Close()
	clientB := mustRTP(t)
	defer clientB.Close()

	bridge := media.NewBridge(t.Context(), serverA, serverB)
	bridge.SetARemote(v4Addr(t, clientA))
	bridge.SetBRemote(v4Addr(t, clientB))
	bridge.Start()
	defer bridge.Stop()

	// Client A sends RTP to Server A → bridge forwards to Client B
	go func() {
		pkt := &proto.RTPPacket{
			Header: proto.RTPHeader{
				Version: 2, PayloadType: 0,
				SequenceNumber: 100, Timestamp: 1000, SSRC: 1001,
			},
			Payload: []byte{0x01, 0x02, 0x03},
		}
		if err := clientA.WriteRTP(pkt, v4Addr(t, serverA)); err != nil {
			t.Errorf("clientA → serverA: %v", err)
		}
	}()

	// Client B sends RTP to Server B → bridge forwards to Client A
	go func() {
		pkt := &proto.RTPPacket{
			Header: proto.RTPHeader{
				Version: 2, PayloadType: 8,
				SequenceNumber: 200, Timestamp: 2000, SSRC: 2002,
			},
			Payload: []byte{0x04, 0x05, 0x06},
		}
		if err := clientB.WriteRTP(pkt, v4Addr(t, serverB)); err != nil {
			t.Errorf("clientB → serverB: %v", err)
		}
	}()

	// Client B should receive the forwarded A→B packet
	clientB.SetReadDeadline(time.Now().Add(5 * time.Second))
	pktFromA, _, err := clientB.ReadRTP()
	if err != nil {
		t.Fatalf("clientB ReadRTP (expecting A→B): %v", err)
	}
	if string(pktFromA.Payload) != string([]byte{0x01, 0x02, 0x03}) {
		t.Fatalf("A→B payload mismatch: got %v", pktFromA.Payload)
	}
	if pktFromA.Header.PayloadType != 0 {
		t.Fatalf("A→B PT mismatch: got %d, want 0", pktFromA.Header.PayloadType)
	}

	// Client A should receive the forwarded B→A packet
	clientA.SetReadDeadline(time.Now().Add(5 * time.Second))
	pktFromB, _, err := clientA.ReadRTP()
	if err != nil {
		t.Fatalf("clientA ReadRTP (expecting B→A): %v", err)
	}
	if string(pktFromB.Payload) != string([]byte{0x04, 0x05, 0x06}) {
		t.Fatalf("B→A payload mismatch: got %v", pktFromB.Payload)
	}
	if pktFromB.Header.PayloadType != 8 {
		t.Fatalf("B→A PT mismatch: got %d, want 8", pktFromB.Header.PayloadType)
	}
}

func TestBridgeStartWithoutAddresses(t *testing.T) {
	a := mustRTP(t)
	defer a.Close()
	b := mustRTP(t)
	defer b.Close()

	bridge := media.NewBridge(t.Context(), a, b)
	bridge.Start()
	bridge.Stop()
}

func TestBridgeStopCancels(t *testing.T) {
	clientA := mustRTP(t)
	defer clientA.Close()
	clientB := mustRTP(t)
	defer clientB.Close()
	serverA := mustRTP(t)
	defer serverA.Close()
	serverB := mustRTP(t)
	defer serverB.Close()

	bridge := media.NewBridge(t.Context(), serverA, serverB)
	bridge.SetARemote(v4Addr(t, clientA))
	bridge.SetBRemote(v4Addr(t, clientB))
	bridge.Start()

	stopped := make(chan struct{})
	go func() {
		bridge.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge.Stop() did not return within 2s")
	}
}
