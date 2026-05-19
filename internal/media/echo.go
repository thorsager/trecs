package media

import (
	"context"
	"math/rand"
	"time"

	"gitub.com/thorsager/trec/proto"
)

const (
	echoReadTimeout = 1 * time.Second
	samplesPerFrame = 160 // 20ms of PCMU at 8000 Hz
)

// RunEcho loops reading RTP packets from conn and echoing the payload back
// to the sender with fresh headers. It exits when ctx is cancelled or conn
// encounters a read error.
func RunEcho(ctx context.Context, conn *RTPConn, payloadType uint8) {
	serverSSRC := rand.Uint32()
	var seq uint16
	var timestamp uint32
	out := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: payloadType, SSRC: serverSSRC,
		},
	}
	marshalBuf := make([]byte, 1500)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(echoReadTimeout)); err != nil {
			return
		}

		pkt, addr, err := conn.ReadRTP()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}

		out.Header.SequenceNumber = seq
		out.Header.Timestamp = timestamp
		out.Payload = pkt.Payload

		sz := out.MarshalSize()
		if sz > len(marshalBuf) {
			marshalBuf = make([]byte, sz)
		}
		n, err := out.MarshalTo(marshalBuf)
		if err != nil {
			return
		}
		if _, err := conn.conn.WriteTo(marshalBuf[:n], addr); err != nil {
			return
		}
		seq++
		timestamp += samplesPerFrame
	}
}
