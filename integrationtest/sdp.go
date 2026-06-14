package integrationtest

import "github.com/thorsager/trecs/proto"

// BuildSDPOffer creates a simple SDP offer for testing.
func BuildSDPOffer(rtpPort int, ip string) *proto.SDP {
	return &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username:       "-",
			SessionID:      "1",
			SessionVersion: "1",
			NetworkType:    "IN",
			AddressType:    "IP4",
			Address:        ip,
		},
		SessionName: "test",
		Connection:  &proto.ConnectionInfo{NetworkType: "IN", AddressType: "IP4", Address: ip},
		Times:       []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs: []proto.MediaDescription{
			{Type: "audio", Port: rtpPort, Proto: "RTP/AVP", Fmt: []string{"0", "8"}},
		},
	}
}

// ExtractRTPAddr returns the IP and port from the first audio media description in an SDP.
func ExtractRTPAddr(sdp *proto.SDP) (ip string, port int) {
	ip = "127.0.0.1"
	if sdp.Connection != nil && sdp.Connection.Address != "" {
		ip = sdp.Connection.Address
	}
	for _, m := range sdp.MediaDescs {
		if m.Type == "audio" {
			return ip, m.Port
		}
	}
	return ip, 0
}
