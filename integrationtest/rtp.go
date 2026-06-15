package integrationtest

import (
	"crypto/rand"
	"math/big"

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
