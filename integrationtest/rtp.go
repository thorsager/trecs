package integrationtest

import (
	"crypto/rand"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/thorsager/trecs/proto"
)

// BuildRTPPacket creates a simple RTP packet for testing.
func BuildRTPPacket(seq uint16, ts uint32, ssrc uint32, payload []byte) *proto.RTPPacket {
	return &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: seq,
			Timestamp:      ts,
			SSRC:           ssrc,
		},
		Payload: payload,
	}
}

// RandomSSRC returns a random non-zero SSRC value suitable for RTP testing.
func RandomSSRC() uint32 {
	for {
		n, err := rand.Int(rand.Reader, big.NewInt(1<<31-1))
		if err != nil {
			return 1
		}
		ssrc := uint32(n.Int64())
		if ssrc != 0 {
			return ssrc
		}
	}
}

// SendRTPPackets sends 10 RTP packets from the given connection to the target address.
func SendRTPPackets(t *testing.T, rtpConn *net.UDPConn, targetAddr *net.UDPAddr, ssrc uint32) {
	t.Helper()

	for i := range 10 {
		pkt := BuildRTPPacket(
			uint16(100+i),
			uint32(i*160),
			ssrc,
			[]byte{byte(i), byte(i), byte(i), byte(i)},
		)
		buf, _ := pkt.Marshal()
		_, _ = rtpConn.WriteToUDP(buf, targetAddr)
		if i < 9 {
			time.Sleep(20 * time.Millisecond)
		}
	}
}
