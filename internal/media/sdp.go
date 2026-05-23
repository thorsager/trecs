package media

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/thorsager/trecs/proto"
)

// BuildOffer creates an SDP offer from the server's perspective for a
// delayed-offer (SDP in 200 OK) exchange.
func BuildOffer(rtpPort int, payloadType uint8, serverIP string) *proto.SDP {
	return &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username:       "-",
			SessionID:      fmt.Sprintf("%d", time.Now().UnixNano()),
			SessionVersion: "1",
			NetworkType:    "IN",
			AddressType:    "IP4",
			Address:        serverIP,
		},
		SessionName: "echo",
		Connection: &proto.ConnectionInfo{
			NetworkType: "IN",
			AddressType: "IP4",
			Address:     serverIP,
		},
		Times: []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs: []proto.MediaDescription{
			{
				Type:  "audio",
				Port:  rtpPort,
				Proto: "RTP/AVP",
				Fmt:   []string{fmt.Sprintf("%d", payloadType)},
				Attributes: []proto.Attribute{
					{Key: "rtpmap", Value: fmt.Sprintf("%d PCMU/8000", payloadType)},
				},
			},
		},
	}
}

// BuildAnswer creates an SDP answer that mirrors the client's offer back
// but substitutes the server's RTP port and address.
func BuildAnswer(offer *proto.SDP, rtpPort int, payloadType uint8, serverIP string) *proto.SDP {
	return &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username:       "-",
			SessionID:      fmt.Sprintf("%d", time.Now().UnixNano()),
			SessionVersion: "1",
			NetworkType:    "IN",
			AddressType:    "IP4",
			Address:        serverIP,
		},
		SessionName: "echo",
		Connection: &proto.ConnectionInfo{
			NetworkType: "IN",
			AddressType: "IP4",
			Address:     serverIP,
		},
		Times: []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs: []proto.MediaDescription{
			{
				Type:  "audio",
				Port:  rtpPort,
				Proto: "RTP/AVP",
				Fmt:   []string{fmt.Sprintf("%d", payloadType)},
				Attributes: []proto.Attribute{
					{Key: "rtpmap", Value: fmt.Sprintf("%d PCMU/8000", payloadType)},
				},
			},
		},
	}
}

// PickPayloadType selects a supported audio payload type from an SDP offer.
// It prefers PCMU (0) over PCMA (8). Returns PCMU if nothing matches.
func PickPayloadType(offer *proto.SDP) uint8 {
	best := uint8(math.MaxUint8)
	for _, m := range offer.MediaDescs {
		if m.Type != "audio" {
			continue
		}
		for _, f := range m.Fmt {
			pt, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil {
				continue
			}
			if pt == proto.PCMU {
				return proto.PCMU
			}
			if pt == proto.PCMA {
				best = proto.PCMA
			}
		}
	}
	if best != math.MaxUint8 {
		return best
	}
	return proto.PCMU
}


// ExtractRTPAddr extracts the IP address and port from an SDP for the first
// audio media line.
func ExtractRTPAddr(sdp *proto.SDP) (ip string, port int) {
	ip = "127.0.0.1"
	if sdp.Connection != nil && sdp.Connection.Address != "" {
		ip = sdp.Connection.Address
	}
	for _, m := range sdp.MediaDescs {
		if m.Type != "audio" {
			continue
		}
		port = m.Port
		break
	}
	return
}
