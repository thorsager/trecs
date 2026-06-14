package integrationtest

import "github.com/thorsager/trecs/proto"

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
