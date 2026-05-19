package media_test

import (
	"net"
	"testing"
	"time"

	"gitub.com/thorsager/trec/internal/media"
	"gitub.com/thorsager/trec/proto"
)

func BenchmarkRTPConnReadWrite(b *testing.B) {
	a, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer a.Close()
	c, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()

	payload := make([]byte, 160)
	sent := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 0,
			SequenceNumber: 1, Timestamp: 100, SSRC: 0xDEAD,
		},
		Payload: payload,
	}

	cPort := c.LocalAddr().(*net.UDPAddr).Port
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: cPort}

	b.ReportAllocs()
	b.ResetTimer()

	// Write before each read to ensure data is available.
	for i := 0; i < b.N; i++ {
		if err := a.WriteRTP(sent, addr); err != nil {
			b.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(time.Second))
		_, _, err := c.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBridgeForward(b *testing.B) {
	serverA, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer serverA.Close()
	serverB, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer serverB.Close()
	clientA, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer clientA.Close()
	clientB, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer clientB.Close()

	bridge := media.NewBridge(serverA, serverB)

	aPort := clientA.LocalAddr().(*net.UDPAddr).Port
	bPort := clientB.LocalAddr().(*net.UDPAddr).Port
	bridge.SetARemote(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: aPort})
	bridge.SetBRemote(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: bPort})
	bridge.Start()
	defer bridge.Stop()

	payload := make([]byte, 160)
	pkt := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 0,
			SequenceNumber: 1, Timestamp: 100, SSRC: 0xDEAD,
		},
		Payload: payload,
	}

	sAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: serverA.LocalAddr().(*net.UDPAddr).Port}
	go func() {
		for {
			if err := clientA.WriteRTP(pkt, sAddr); err != nil {
				return
			}
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		clientB.SetReadDeadline(time.Now().Add(time.Second))
		_, _, err := clientB.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
	}
}
