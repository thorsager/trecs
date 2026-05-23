package media_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/proto"
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
		got, _, err := c.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
		c.Release(got)
	}
}

func BenchmarkRTPConn_ReadRTP(b *testing.B) {
	sender, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer sender.Close()

	receiver, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer receiver.Close()

	payload := make([]byte, 160)
	pkt := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 0,
			SequenceNumber: 1, Timestamp: 100, SSRC: 0xDEAD,
		},
		Payload: payload,
	}

	rAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: receiver.LocalAddr().(*net.UDPAddr).Port}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Send before each read to ensure data is available.
		sender.WriteRTP(pkt, rAddr)
		receiver.SetReadDeadline(time.Now().Add(time.Second))
		got, _, err := receiver.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
		receiver.Release(got)
	}
}

func BenchmarkRTPConn_WriteRTP(b *testing.B) {
	sender, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer sender.Close()

	receiver, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer receiver.Close()

	payload := make([]byte, 160)
	pkt := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 0,
			SequenceNumber: 1, Timestamp: 100, SSRC: 0xDEAD,
		},
		Payload: payload,
	}

	rAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: receiver.LocalAddr().(*net.UDPAddr).Port}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := sender.WriteRTP(pkt, rAddr); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPConn_EchoLoop(b *testing.B) {
	conn, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()

	client, err := media.NewRTPConn()
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()

	payload := make([]byte, 160)
	pkt := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 0,
			SequenceNumber: 1, Timestamp: 100, SSRC: 0xDEAD,
		},
		Payload: payload,
	}

	serverAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: conn.LocalAddr().(*net.UDPAddr).Port}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		media.RunEcho(ctx, conn, 0)
	}()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := client.WriteRTP(pkt, serverAddr); err != nil {
			b.Fatal(err)
		}
		client.SetReadDeadline(time.Now().Add(time.Second))
		got, _, err := client.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
		client.Release(got)
	}

	b.StopTimer()
	cancel()
	wg.Wait()
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
		got, _, err := clientB.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
		clientB.Release(got)
	}
}

func BenchmarkBridge_ForwardLoop(b *testing.B) {
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

	// Pre-fill to ensure bridge goroutine is pumping.
	for i := 0; i < 10; i++ {
		clientA.WriteRTP(pkt, sAddr)
		time.Sleep(5 * time.Millisecond)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		clientA.WriteRTP(pkt, sAddr)
		clientB.SetReadDeadline(time.Now().Add(time.Second))
		got, _, err := clientB.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
		clientB.Release(got)
	}
}

func BenchmarkRTPConnReadWrite_SmallPayload(b *testing.B) {
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

	sent := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 0,
			SequenceNumber: 1, Timestamp: 100, SSRC: 0xDEAD,
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: c.LocalAddr().(*net.UDPAddr).Port}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := a.WriteRTP(sent, addr); err != nil {
			b.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(time.Second))
		got, _, err := c.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
		c.Release(got)
	}
}

func BenchmarkRTPConnReadWrite_LargePayload(b *testing.B) {
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

	sent := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 96,
			SequenceNumber: 1, Timestamp: 100, SSRC: 0xDEAD,
		},
		Payload: make([]byte, 1200),
	}

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: c.LocalAddr().(*net.UDPAddr).Port}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := a.WriteRTP(sent, addr); err != nil {
			b.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(time.Second))
		got, _, err := c.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
		c.Release(got)
	}
}

func BenchmarkRTPConnReadWrite_WithExtensions(b *testing.B) {
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

	sent := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version:          2,
			PayloadType:      96,
			SequenceNumber:   1,
			Timestamp:        100,
			SSRC:             0xDEAD,
			Extension:        true,
			ExtensionProfile: proto.ExtensionProfileOneByte,
			Extensions: []proto.RTPExtension{
				{ID: 1, Payload: []byte{0xAA}},
				{ID: 2, Payload: []byte{0xBB, 0xCC}},
			},
		},
		Payload: make([]byte, 160),
	}

	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: c.LocalAddr().(*net.UDPAddr).Port}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := a.WriteRTP(sent, addr); err != nil {
			b.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(time.Second))
		got, _, err := c.ReadRTP()
		if err != nil {
			b.Fatal(err)
		}
		c.Release(got)
	}
}
