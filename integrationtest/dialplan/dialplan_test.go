package dialplan

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
	"github.com/thorsager/trecs/internal/dialplan"
	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/proto"
)

func TestIntegration_DialplanEcho(t *testing.T) {
	dp := dialplan.NewStatic(map[string]dialplan.Entry{
		"echo": {Action: dialplan.ActionEcho},
	})

	t.Run("UDP_EarlyOffer", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", dp)
		defer ts.Stop()

		runEchoTest(t, ts, "udp", true)
	})

	t.Run("UDP_DelayedOffer", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", dp)
		defer ts.Stop()

		runEchoTest(t, ts, "udp", false)
	})

	t.Run("TCP_EarlyOffer", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", dp)
		defer ts.Stop()

		runEchoTest(t, ts, "tcp", true)
	})

	t.Run("TCP_DelayedOffer", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", dp)
		defer ts.Stop()

		runEchoTest(t, ts, "tcp", false)
	})
}

func TestIntegration_DialplanPlayback(t *testing.T) {
	wavPath := generateTestWav(t)
	dp := dialplan.NewStatic(map[string]dialplan.Entry{
		"play": {Action: dialplan.ActionPlay, File: wavPath},
	})

	t.Run("UDP_EarlyOffer", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", dp)
		defer ts.Stop()

		runPlaybackTest(t, ts, "udp", true)
	})

	t.Run("UDP_DelayedOffer", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", dp)
		defer ts.Stop()

		runPlaybackTest(t, ts, "udp", false)
	})

	t.Run("TCP_EarlyOffer", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", dp)
		defer ts.Stop()

		runPlaybackTest(t, ts, "tcp", true)
	})

	t.Run("TCP_DelayedOffer", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", dp)
		defer ts.Stop()

		runPlaybackTest(t, ts, "tcp", false)
	})
}

func runEchoTest(t *testing.T, ts *integrationtest.TestServer, transport string, earlyOffer bool) {
	t.Helper()

	ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)

	clientRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer clientRTP.Close()
	rtpPort := clientRTP.LocalAddr().(*net.UDPAddr).Port

	callID := fmt.Sprintf("echo-%s-%s", transport, t.Name())
	fromTag := "echo-from-123"

	invite := buildEchoInvite(ts.Domain, getPort(ts, transport), callID, fromTag, transport, rtpPort, earlyOffer)

	if transport == "tcp" {
		invite.SetTransport("TCP")
	}

	res, err := client.Do(t.Context(), invite)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "expected 200 OK for echo INVITE")

	serverTag := extractToTag(res)
	require.NotEmpty(t, serverTag, "To header should have server tag")

	require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
	require.NoError(t, err)
	serverIP, serverRTPPort := extractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

	ack := buildEchoACK(ts.Domain, getPort(ts, transport), callID, fromTag, serverTag, transport, !earlyOffer, rtpPort)
	if transport == "tcp" {
		ack.SetTransport("TCP")
	}
	err = client.WriteRequest(ack)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}
	testPayload := []byte{0xde, 0xad, 0xbe, 0xef}
	sendPkt := buildRTPPacket(1, 0, 9999, testPayload)

	buf, err := sendPkt.Marshal()
	require.NoError(t, err)
	_, err = clientRTP.WriteToUDP(buf, serverRTPAddr)
	require.NoError(t, err)

	clientRTP.SetReadDeadline(time.Now().Add(3 * time.Second))
	recvBuf := make([]byte, 1500)
	n, _, err := clientRTP.ReadFromUDP(recvBuf)
	require.NoError(t, err, "should receive echoed RTP")

	var echoed proto.RTPPacket
	err = proto.UnmarshalRTPTo(recvBuf[:n], &echoed)
	require.NoError(t, err)
	assert.Equal(t, testPayload, echoed.Payload, "echoed payload should match")
	assert.Equal(t, uint8(0), echoed.Header.PayloadType, "payload type should be PCMU")

	bye := buildEchoBYE(ts.Domain, getPort(ts, transport), callID, fromTag, serverTag, transport)
	if transport == "tcp" {
		bye.SetTransport("TCP")
	}
	byeRes, err := client.Do(t.Context(), bye)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")

	client.Close()
	ua.Close()
}

func runPlaybackTest(t *testing.T, ts *integrationtest.TestServer, transport string, earlyOffer bool) {
	t.Helper()

	ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)

	clientRTP, err := media.NewRTPConn()
	require.NoError(t, err)
	defer clientRTP.Close()

	callID := fmt.Sprintf("play-%s-%s", transport, t.Name())
	fromTag := "play-from-123"

	invite := buildPlaybackInvite(ts.Domain, getPort(ts, transport), callID, fromTag, transport, clientRTP.LocalAddr().(*net.UDPAddr).Port, earlyOffer)

	if transport == "tcp" {
		invite.SetTransport("TCP")
	}

	res, err := client.Do(t.Context(), invite)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "expected 200 OK for playback INVITE")

	serverTag := extractToTag(res)
	require.NotEmpty(t, serverTag, "To header should have server tag")

	require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
	require.NoError(t, err)
	_, serverRTPPort := extractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

	ack := buildPlaybackACK(ts.Domain, getPort(ts, transport), callID, fromTag, serverTag, transport, !earlyOffer, clientRTP.LocalAddr().(*net.UDPAddr).Port)
	if transport == "tcp" {
		ack.SetTransport("TCP")
	}
	err = client.WriteRequest(ack)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	var receivedPackets int
	var serverSSRC uint32

	clientRTP.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		pkt, _, err := clientRTP.ReadRTP()
		if err != nil {
			break
		}
		receivedPackets++
		if receivedPackets == 1 {
			serverSSRC = pkt.Header.SSRC
		}
		if receivedPackets >= 10 {
			break
		}
	}

	assert.Greater(t, receivedPackets, 0, "should receive at least one RTP packet from playback")
	if receivedPackets > 0 {
		assert.NotZero(t, serverSSRC, "server should generate unique SSRC")
	}

	time.Sleep(200 * time.Millisecond)

	bye := buildPlaybackBYE(ts.Domain, getPort(ts, transport), callID, fromTag, serverTag, transport)
	if transport == "tcp" {
		bye.SetTransport("TCP")
	}
	byeRes, err := client.Do(t.Context(), bye)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")

	client.Close()
	ua.Close()
}

func buildEchoInvite(domain string, port int, callID, fromTag, transport string, rtpPort int, earlyOffer bool) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.INVITE, sipgo_sip.Uri{
		User: "echo",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:caller@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:echo@%s>", domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 INVITE"))
	req.AppendHeader(sipgo_sip.NewHeader("Contact", fmt.Sprintf("<sip:caller@127.0.0.1:%d;transport=%s>", port, transport)))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))

	if earlyOffer {
		req.AppendHeader(sipgo_sip.NewHeader("Content-Type", "application/sdp"))
		sdp := buildSDPOffer(rtpPort, "127.0.0.1")
		sdpBytes, _ := sdp.Marshal()
		req.SetBody(sdpBytes)
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", fmt.Sprintf("%d", len(sdpBytes))))
	} else {
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	}

	return req
}

func buildEchoACK(domain string, port int, callID, fromTag, serverTag, transport string, includeSDP bool, rtpPort int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.ACK, sipgo_sip.Uri{
		User: "echo",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:caller@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:echo@%s>;tag=%s", domain, serverTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 ACK"))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))

	if includeSDP {
		sdp := buildSDPOffer(rtpPort, "127.0.0.1")
		sdpBytes, _ := sdp.Marshal()
		req.SetBody(sdpBytes)
		req.AppendHeader(sipgo_sip.NewHeader("Content-Type", "application/sdp"))
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", fmt.Sprintf("%d", len(sdpBytes))))
	} else {
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	}

	return req
}

func buildEchoBYE(domain string, port int, callID, fromTag, serverTag, transport string) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.BYE, sipgo_sip.Uri{
		User: "echo",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:caller@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:echo@%s>;tag=%s", domain, serverTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "2 BYE"))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func buildPlaybackInvite(domain string, port int, callID, fromTag, transport string, rtpPort int, earlyOffer bool) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.INVITE, sipgo_sip.Uri{
		User: "play",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:listener@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:play@%s>", domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 INVITE"))
	req.AppendHeader(sipgo_sip.NewHeader("Contact", fmt.Sprintf("<sip:listener@127.0.0.1:%d;transport=%s>", rtpPort, transport)))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))

	if earlyOffer {
		req.AppendHeader(sipgo_sip.NewHeader("Content-Type", "application/sdp"))
		sdp := buildSDPOffer(rtpPort, "127.0.0.1")
		sdpBytes, _ := sdp.Marshal()
		req.SetBody(sdpBytes)
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", fmt.Sprintf("%d", len(sdpBytes))))
	} else {
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	}

	return req
}

func buildPlaybackACK(domain string, port int, callID, fromTag, serverTag, transport string, includeSDP bool, rtpPort int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.ACK, sipgo_sip.Uri{
		User: "play",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:listener@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:play@%s>;tag=%s", domain, serverTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 ACK"))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))

	if includeSDP {
		sdp := buildSDPOffer(rtpPort, "127.0.0.1")
		sdpBytes, _ := sdp.Marshal()
		req.SetBody(sdpBytes)
		req.AppendHeader(sipgo_sip.NewHeader("Content-Type", "application/sdp"))
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", fmt.Sprintf("%d", len(sdpBytes))))
	} else {
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	}

	return req
}

func buildPlaybackBYE(domain string, port int, callID, fromTag, serverTag, transport string) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.BYE, sipgo_sip.Uri{
		User: "play",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:listener@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:play@%s>;tag=%s", domain, serverTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "2 BYE"))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func buildSDPOffer(rtpPort int, ip string) *proto.SDP {
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

func buildRTPPacket(seq uint16, ts uint32, ssrc uint32, payload []byte) *proto.RTPPacket {
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

func getPort(ts *integrationtest.TestServer, transport string) int {
	if transport == "tcp" {
		return ts.TCPPort
	}
	return ts.UDPPort
}

func extractRTPAddr(sdp *proto.SDP) (ip string, port int) {
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

func extractToTag(res *sipgo_sip.Response) string {
	to := res.GetHeader("To")
	if to == nil {
		return ""
	}
	val := to.Value()
	if idx := strings.Index(val, ";tag="); idx != -1 {
		return val[idx+5:]
	}
	return ""
}

func generateTestWav(t *testing.T) string {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "trec_test_*.wav")
	require.NoError(t, err)

	sampleRate := uint32(8000)
	bitsPerSample := uint16(16)
	numChannels := uint16(1)
	duration := 1
	numSamples := int(sampleRate) * duration
	dataSize := numSamples * int(numChannels) * int(bitsPerSample/8)

	writeWavHeader(f, sampleRate, bitsPerSample, numChannels, uint32(dataSize))

	for i := 0; i < numSamples; i++ {
		sample := int16(16383)
		f.Write([]byte{byte(sample), byte(sample >> 8)})
	}

	f.Close()
	return f.Name()
}

func writeWavHeader(f *os.File, sampleRate uint32, bitsPerSample uint16, numChannels uint16, dataSize uint32) {
	byteRate := sampleRate * uint32(numChannels) * uint32(bitsPerSample/8)
	blockAlign := numChannels * bitsPerSample / 8
	totalSize := dataSize + 36

	f.WriteString("RIFF")
	binary.Write(f, binary.LittleEndian, uint32(totalSize))
	f.WriteString("WAVE")

	f.WriteString("fmt ")
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, numChannels)
	binary.Write(f, binary.LittleEndian, sampleRate)
	binary.Write(f, binary.LittleEndian, byteRate)
	binary.Write(f, binary.LittleEndian, blockAlign)
	binary.Write(f, binary.LittleEndian, bitsPerSample)

	f.WriteString("data")
	binary.Write(f, binary.LittleEndian, dataSize)
}
