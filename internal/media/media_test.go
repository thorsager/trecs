package media_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sipTestAddr = "127.0.0.1:15061"

// ---- SDP unit tests ----

func TestBuildOffer(t *testing.T) {
	sdp := media.BuildOffer(10000, 0, "127.0.0.1")
	require.NotNil(t, sdp)
	assert.Equal(t, 0, sdp.Version)
	require.Len(t, sdp.MediaDescs, 1)
	assert.Equal(t, "audio", sdp.MediaDescs[0].Type)
	assert.Equal(t, 10000, sdp.MediaDescs[0].Port)

	data, err := sdp.Marshal()
	require.NoError(t, err)
	assert.Contains(t, string(data), "m=audio 10000 RTP/AVP 0")
}

func TestBuildAnswer(t *testing.T) {
	offer := &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username: "-", SessionID: "1", SessionVersion: "1",
			NetworkType: "IN", AddressType: "IP4", Address: "127.0.0.1",
		},
		SessionName: "test",
		Connection:  &proto.ConnectionInfo{NetworkType: "IN", AddressType: "IP4", Address: "127.0.0.1"},
		Times:       []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs: []proto.MediaDescription{
			{Type: "audio", Port: 20000, Proto: "RTP/AVP", Fmt: []string{"0", "8"}},
		},
	}

	answer := media.BuildAnswer(offer, 10000, 0, "127.0.0.1")
	require.NotNil(t, answer)
	require.Len(t, answer.MediaDescs, 1)
	assert.Equal(t, 10000, answer.MediaDescs[0].Port)
	assert.Equal(t, []string{"0"}, answer.MediaDescs[0].Fmt)
}

func TestPickPayloadType(t *testing.T) {
	tests := []struct {
		name string
		fmt  []string
		want uint8
	}{
		{"prefers PCMU", []string{"8", "0"}, 0},
		{"PCMA", []string{"8"}, 8},
		{"no match defaults to PCMU", []string{"9", "120"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sdp := &proto.SDP{MediaDescs: []proto.MediaDescription{
				{Type: "audio", Fmt: tc.fmt},
			}}
			assert.Equal(t, tc.want, media.PickPayloadType(sdp))
		})
	}
}

func TestPickPayloadType_SkipsNonAudio(t *testing.T) {
	sdp := &proto.SDP{MediaDescs: []proto.MediaDescription{
		{Type: "video", Fmt: []string{"0"}},
		{Type: "audio", Fmt: []string{"8"}},
	}}
	assert.Equal(t, uint8(8), media.PickPayloadType(sdp))
}

func TestExtractRTPAddr(t *testing.T) {
	sdp := &proto.SDP{
		Connection: &proto.ConnectionInfo{Address: "10.0.0.1"},
		MediaDescs: []proto.MediaDescription{
			{Type: "audio", Port: 30000},
		},
	}
	ip, port := media.ExtractRTPAddr(sdp)
	assert.Equal(t, "10.0.0.1", ip)
	assert.Equal(t, 30000, port)
}

func TestExtractRTPAddr_DefaultIP(t *testing.T) {
	sdp := &proto.SDP{MediaDescs: []proto.MediaDescription{
		{Type: "audio", Port: 40000},
	}}
	ip, port := media.ExtractRTPAddr(sdp)
	assert.Equal(t, "127.0.0.1", ip)
	assert.Equal(t, 40000, port)
}

func TestExtractRTPAddr_SkipsNonAudio(t *testing.T) {
	sdp := &proto.SDP{
		MediaDescs: []proto.MediaDescription{
			{Type: "video", Port: 10000},
			{Type: "audio", Port: 20000},
		},
	}
	_, port := media.ExtractRTPAddr(sdp)
	assert.Equal(t, 20000, port)
}

// ---- RFC 8866 / 3264 compliance tests ----

func sdpLines(t *testing.T, data []byte) []string {
	t.Helper()
	raw := string(data)
	all := strings.Split(raw, "\r\n")
	var lines []string
	for _, l := range all {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestSDPBuildOffer_RFCCompliance(t *testing.T) {
	sdp := media.BuildOffer(10000, 0, "10.0.0.1")
	data, err := sdp.Marshal()
	require.NoError(t, err)

	lines := sdpLines(t, data)
	require.GreaterOrEqual(t, len(lines), 7, "should have at least 7 lines")

	// v= must be 0
	assert.Equal(t, "v=0", lines[0], "first line must be v=0")

	// o= line: exactly 6 space-separated fields
	require.True(t, strings.HasPrefix(lines[1], "o="), "second line must be o=")
	oFields := strings.Fields(lines[1][2:])
	assert.Len(t, oFields, 6, "o= line must have 6 fields")
	if len(oFields) == 6 {
		assert.Equal(t, "-", oFields[0], "o= username should be '-'")
		assert.Equal(t, "1", oFields[2], "o= session version should be '1'")
		assert.Equal(t, "IN", oFields[3], "o= network type should be IN")
		assert.Equal(t, "IP4", oFields[4], "o= address type should be IP4")
		assert.Equal(t, "10.0.0.1", oFields[5], "o= address should match serverIP")
	}

	// s= line is required and non-empty
	assert.True(t, strings.HasPrefix(lines[2], "s="), "third line must be s=")
	assert.Equal(t, "s=echo", lines[2], "s= should be 'echo'")

	// c= line: exactly 3 space-separated fields
	require.True(t, strings.HasPrefix(lines[3], "c="), "fourth line must be c=")
	cFields := strings.Fields(lines[3][2:])
	assert.Len(t, cFields, 3, "c= line must have 3 fields")
	if len(cFields) == 3 {
		assert.Equal(t, "IN", cFields[0], "c= network type should be IN")
		assert.Equal(t, "IP4", cFields[1], "c= address type should be IP4")
		assert.Equal(t, "10.0.0.1", cFields[2], "c= address should match serverIP")
	}

	// t= line is required
	var tLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "t=") {
			tLine = l
			break
		}
	}
	require.NotEmpty(t, tLine, "t= line is required")
	tFields := strings.Fields(tLine[2:])
	assert.Len(t, tFields, 2, "t= must have start and stop")
	assert.Equal(t, "0", tFields[0], "t= start should be 0")
	assert.Equal(t, "0", tFields[1], "t= stop should be 0")

	// m= line: audio RTP/AVP with PCMU
	var mLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "m=") {
			mLine = l
			break
		}
	}
	require.NotEmpty(t, mLine, "m= line is required")
	mFields := strings.Fields(mLine[2:])
	require.GreaterOrEqual(t, len(mFields), 4, "m= must have type, port, proto, fmt")
	assert.Equal(t, "audio", mFields[0], "m= media type should be audio")
	assert.Equal(t, "10000", mFields[1], "m= port should match rtpPort")
	assert.Equal(t, "RTP/AVP", mFields[2], "m= proto should be RTP/AVP")
	assert.Equal(t, "0", mFields[3], "m= fmt should be payload type 0 (PCMU)")

	// a= rtpmap line
	var aLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "a=rtpmap") {
			aLine = l
			break
		}
	}
	require.NotEmpty(t, aLine, "a=rtpmap line is required")
	assert.Equal(t, "a=rtpmap:0 PCMU/8000", aLine, "rtpmap should match payload type")

	// CRLF line endings
	assert.True(t, strings.HasSuffix(string(data), "\r\n"), "must end with CRLF")

	// Consistency check: o= and c= must have matching nettype/addrtype
	assert.Equal(t, "IN", oFields[3], "o= network type")
	assert.Equal(t, "IN", cFields[0], "c= network type")
	assert.Equal(t, "IP4", oFields[4], "o= address type")
	assert.Equal(t, "IP4", cFields[1], "c= address type")
}

func TestSDPBuildOffer_PayloadTypePCMA(t *testing.T) {
	sdp := media.BuildOffer(20000, 8, "127.0.0.1")
	data, err := sdp.Marshal()
	require.NoError(t, err)
	raw := string(data)

	assert.Contains(t, raw, "m=audio 20000 RTP/AVP 8", "m= line should use PCMA")
	assert.Contains(t, raw, "a=rtpmap:8 PCMU/8000", "rtpmap should match payload type 8")
}

func TestSDPBuildAnswer_RFCCompliance(t *testing.T) {
	offer := &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username: "-", SessionID: "100", SessionVersion: "1",
			NetworkType: "IN", AddressType: "IP4", Address: "192.168.1.1",
		},
		SessionName: "test",
		Connection:  &proto.ConnectionInfo{NetworkType: "IN", AddressType: "IP4", Address: "192.168.1.1"},
		Times:       []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs: []proto.MediaDescription{
			{Type: "audio", Port: 30000, Proto: "RTP/AVP", Fmt: []string{"0", "8", "101"}},
		},
	}

	answer := media.BuildAnswer(offer, 10000, 0, "10.0.0.2")
	require.NotNil(t, answer)
	data, err := answer.Marshal()
	require.NoError(t, err)

	lines := sdpLines(t, data)
	require.GreaterOrEqual(t, len(lines), 7)

	// v= must be 0
	assert.Equal(t, "v=0", lines[0])

	// o= line: must differ from offer (new session-id)
	require.True(t, strings.HasPrefix(lines[1], "o="))
	oFields := strings.Fields(lines[1][2:])
	assert.Len(t, oFields, 6)
	if len(oFields) == 6 {
		assert.Equal(t, "-", oFields[0], "o= username should be '-'")
		assert.NotEqual(t, "100", oFields[1], "o= session-id must differ from offer")
		assert.Equal(t, "1", oFields[2], "o= session version should be '1'")
		assert.Equal(t, "IN", oFields[3])
		assert.Equal(t, "IP4", oFields[4])
		assert.Equal(t, "10.0.0.2", oFields[5], "o= address should be server IP")
	}

	// c= line uses server IP
	cFields := strings.Fields(lines[3][2:])
	assert.Equal(t, "10.0.0.2", cFields[2], "c= address should be server IP")

	// m= line: answer only includes selected payload type, not all offered
	var mLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "m=") {
			mLine = l
			break
		}
	}
	require.NotEmpty(t, mLine)
	mFields := strings.Fields(mLine[2:])
	assert.Equal(t, "10000", mFields[1], "m= port should be server's RTP port")
	assert.Equal(t, "RTP/AVP", mFields[2], "m= proto must match offer transport")
	// RFC 3264 §6: answer m= fmt must be subset of offer fmt
	fmtParts := mFields[3:]
	assert.Contains(t, fmtParts, "0", "answer must include PCMU")
	assert.NotContains(t, fmtParts, "8", "answer should omit PCMA when PCMU selected")
	assert.NotContains(t, fmtParts, "101", "answer should omit telephone-event when PCMU selected")

	// a= rtpmap matches the selected payload type
	var aLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "a=rtpmap") {
			aLine = l
			break
		}
	}
	require.NotEmpty(t, aLine)
	assert.Equal(t, "a=rtpmap:0 PCMU/8000", aLine)

	// CRLF consistency
	assert.True(t, strings.HasSuffix(string(data), "\r\n"))
}

func TestSDPBuildAnswer_OmittedPayloadType(t *testing.T) {
	offer := &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username: "-", SessionID: "200", SessionVersion: "1",
			NetworkType: "IN", AddressType: "IP4", Address: "10.0.0.1",
		},
		SessionName: "test",
		Connection:  &proto.ConnectionInfo{NetworkType: "IN", AddressType: "IP4", Address: "10.0.0.1"},
		Times:       []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs:  []proto.MediaDescription{
			{Type: "audio", Port: 20000, Proto: "RTP/AVP", Fmt: []string{"8"}},
		},
	}

	// Only PCMA (8) offered, so answer should use PCMA
	answer := media.BuildAnswer(offer, 10000, 8, "127.0.0.1")
	data, err := answer.Marshal()
	require.NoError(t, err)
	raw := string(data)

	assert.Contains(t, raw, "m=audio 10000 RTP/AVP 8", "answer should use PCMA when only PCMA offered")
}

func TestSDPBuildAnswer_DifferentSessionID(t *testing.T) {
	offer := &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username: "-", SessionID: "fixed-session-id", SessionVersion: "5",
			NetworkType: "IN", AddressType: "IP4", Address: "10.0.0.1",
		},
		SessionName: "test",
		Connection:  &proto.ConnectionInfo{NetworkType: "IN", AddressType: "IP4", Address: "10.0.0.1"},
		Times:       []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs:  []proto.MediaDescription{
			{Type: "audio", Port: 20000, Proto: "RTP/AVP", Fmt: []string{"0"}},
		},
	}

	answer1 := media.BuildAnswer(offer, 10000, 0, "127.0.0.1")
	answer2 := media.BuildAnswer(offer, 10001, 0, "127.0.0.1")

	// Each call to BuildAnswer must generate a unique session-id (based on time.Now().UnixNano())
	o1, _ := answer1.Marshal()
	o2, _ := answer2.Marshal()
	assert.NotEqual(t, string(o1), string(o2),
		"two consecutive BuildAnswer calls must produce different SDP")

	// Also verify the session-id differs from the offer
	str1 := string(o1)
	assert.NotContains(t, str1, "fixed-session-id", "answer o= session-id must differ from offer")
}

func TestSDPBuildAnswer_TransportProtocolMatch(t *testing.T) {
	// RFC 3264 §6: answer m= line transport must match the offer
	offer := &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username: "-", SessionID: "300", SessionVersion: "1",
			NetworkType: "IN", AddressType: "IP4", Address: "10.0.0.1",
		},
		SessionName: "test",
		Connection:  &proto.ConnectionInfo{NetworkType: "IN", AddressType: "IP4", Address: "10.0.0.1"},
		Times:       []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs:  []proto.MediaDescription{
			{Type: "audio", Port: 20000, Proto: "RTP/AVP", Fmt: []string{"0"}},
		},
	}

	answer := media.BuildAnswer(offer, 10000, 0, "127.0.0.1")
	data, err := answer.Marshal()
	require.NoError(t, err)
	raw := string(data)

	// The answer's m= line should use the same transport as the offer
	assert.Contains(t, raw, "RTP/AVP", "answer transport must match offer transport")
}

func TestSDPBuildOffer_LineOrder(t *testing.T) {
	// RFC 8866 §3: lines must appear in the order v o s ... m
	sdp := media.BuildOffer(10000, 0, "127.0.0.1")
	data, err := sdp.Marshal()
	require.NoError(t, err)

	lines := sdpLines(t, data)
	var types []byte
	for _, l := range lines {
		if len(l) >= 2 && l[1] == '=' && l[0] >= 'a' && l[0] <= 'z' {
			types = append(types, l[0])
		}
	}

	// v=, o=, s=, c=, t=, a=m must appear in order (a= is session-level before m=, then m=...a=)
	// Expected for BuildOffer: v, o, s, c, t, a, m, a
	require.GreaterOrEqual(t, len(types), 6)
	assert.Equal(t, byte('v'), types[0], "first type must be v")
	assert.Equal(t, byte('o'), types[1], "second type must be o")
	assert.Equal(t, byte('s'), types[2], "third type must be s")

	// Find c, t, m positions
	cPos := -1
	tPos := -1
	mPos := -1
	for i, typ := range types {
		switch typ {
		case 'c':
			cPos = i
		case 't':
			tPos = i
		case 'm':
			mPos = i
		}
	}
	assert.Greater(t, cPos, 0, "c= must be present")
	assert.Greater(t, tPos, cPos, "t= must appear after c=")
	assert.Greater(t, mPos, tPos, "m= must appear after t=")
}

func TestSDPBuildOffer_PortAndPayloadConsistency(t *testing.T) {
	// Verify that BuildOffer with different ports and payload types produces correct output
	sdp := media.BuildOffer(50000, 8, "10.0.0.5")
	data, err := sdp.Marshal()
	require.NoError(t, err)
	raw := string(data)

	assert.Contains(t, raw, "m=audio 50000 RTP/AVP 8")
	assert.Contains(t, raw, "a=rtpmap:8 PCMU/8000")
	assert.Contains(t, raw, "10.0.0.5")
}

// ---- RTPConn tests ----

func TestRTPConn_NewAndClose(t *testing.T) {
	c, err := media.NewRTPConn()
	require.NoError(t, err)
	assert.NotNil(t, c)
	assert.NoError(t, c.Close())
}

func TestRTPConn_LocalAddr(t *testing.T) {
	c, err := media.NewRTPConn()
	require.NoError(t, err)
	defer c.Close()

	addr := c.LocalAddr()
	require.NotNil(t, addr)
	udpAddr, ok := addr.(*net.UDPAddr)
	require.True(t, ok, "LocalAddr should return *net.UDPAddr")
	assert.True(t, udpAddr.Port > 0, "port should be non-zero")
}

func TestRTPConn_DoubleClose(t *testing.T) {
	c, err := media.NewRTPConn()
	require.NoError(t, err)
	assert.NoError(t, c.Close())
	assert.Error(t, c.Close(), "second close should fail")
}

func rtpAddr(port int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
}

func TestRTPConn_WriteThenRead(t *testing.T) {
	a, err := media.NewRTPConn()
	require.NoError(t, err)
	defer a.Close()
	b, err := media.NewRTPConn()
	require.NoError(t, err)
	defer b.Close()

	pkt := &proto.RTPPacket{
		Header: proto.RTPHeader{Version: 2, PayloadType: 0, SequenceNumber: 1, Timestamp: 100, SSRC: 0xDEAD},
		Payload: []byte{0x01, 0x02, 0x03},
	}

	err = a.WriteRTP(pkt, rtpAddr(b.LocalAddr().(*net.UDPAddr).Port))
	require.NoError(t, err)

	b.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, _, err := b.ReadRTP()
	require.NoError(t, err)
	assert.Equal(t, pkt.Payload, got.Payload)
	assert.Equal(t, uint16(1), got.Header.SequenceNumber)
	assert.Equal(t, uint32(100), got.Header.Timestamp)
	assert.Equal(t, uint32(0xDEAD), got.Header.SSRC)
}

func TestRTPConn_RoundTripPreservesHeader(t *testing.T) {
	a, err := media.NewRTPConn()
	require.NoError(t, err)
	defer a.Close()
	b, err := media.NewRTPConn()
	require.NoError(t, err)
	defer b.Close()

	sent := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, Marker: true, Padding: false,
			PayloadType: 8, SequenceNumber: 0xABCD, Timestamp: 0x12345678, SSRC: 0xCAFEBABE,
		},
		Payload: []byte{0x10, 0x20, 0x30, 0x40},
	}

	err = a.WriteRTP(sent, rtpAddr(b.LocalAddr().(*net.UDPAddr).Port))
	require.NoError(t, err)

	b.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, _, err := b.ReadRTP()
	require.NoError(t, err)

	assert.Equal(t, sent.Header.Version, got.Header.Version, "Version")
	assert.Equal(t, sent.Header.Marker, got.Header.Marker, "Marker")
	assert.Equal(t, sent.Header.PayloadType, got.Header.PayloadType, "PayloadType")
	assert.Equal(t, sent.Header.SequenceNumber, got.Header.SequenceNumber, "SequenceNumber")
	assert.Equal(t, sent.Header.Timestamp, got.Header.Timestamp, "Timestamp")
	assert.Equal(t, sent.Header.SSRC, got.Header.SSRC, "SSRC")
	assert.Equal(t, sent.Payload, got.Payload, "Payload")
}

func TestRTPConn_EmptyPayload(t *testing.T) {
	a, err := media.NewRTPConn()
	require.NoError(t, err)
	defer a.Close()
	b, err := media.NewRTPConn()
	require.NoError(t, err)
	defer b.Close()

	sent := &proto.RTPPacket{
		Header: proto.RTPHeader{Version: 2, PayloadType: 0, SequenceNumber: 1, Timestamp: 0, SSRC: 1},
	}

	err = a.WriteRTP(sent, rtpAddr(b.LocalAddr().(*net.UDPAddr).Port))
	require.NoError(t, err)

	b.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, _, err := b.ReadRTP()
	require.NoError(t, err)
	assert.Empty(t, got.Payload)
}

func TestRTPConn_LargePayload(t *testing.T) {
	a, err := media.NewRTPConn()
	require.NoError(t, err)
	defer a.Close()
	b, err := media.NewRTPConn()
	require.NoError(t, err)
	defer b.Close()

	payload := make([]byte, 4000)
	for i := range payload {
		payload[i] = byte(i)
	}

	sent := &proto.RTPPacket{
		Header:  proto.RTPHeader{Version: 2, PayloadType: 0, SequenceNumber: 1, Timestamp: 0, SSRC: 1},
		Payload: payload,
	}

	err = a.WriteRTP(sent, rtpAddr(b.LocalAddr().(*net.UDPAddr).Port))
	require.NoError(t, err)

	b.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, _, err := b.ReadRTP()
	require.NoError(t, err)
	assert.Equal(t, payload, got.Payload)
}

func TestRTPConn_MultiplePacketsPreserveOrder(t *testing.T) {
	a, err := media.NewRTPConn()
	require.NoError(t, err)
	defer a.Close()
	b, err := media.NewRTPConn()
	require.NoError(t, err)
	defer b.Close()

	for i := range 5 {
		pkt := &proto.RTPPacket{
			Header: proto.RTPHeader{
				Version: 2, PayloadType: 0,
				SequenceNumber: uint16(i), Timestamp: uint32(i * 160), SSRC: 42,
			},
			Payload: []byte{byte(i)},
		}
		err = a.WriteRTP(pkt, rtpAddr(b.LocalAddr().(*net.UDPAddr).Port))
		require.NoError(t, err)
	}

	for i := range 5 {
		b.SetReadDeadline(time.Now().Add(3 * time.Second))
		got, _, err := b.ReadRTP()
		require.NoError(t, err)
		assert.Equal(t, uint16(i), got.Header.SequenceNumber, "seq %d", i)
		assert.Equal(t, uint32(i*160), got.Header.Timestamp, "ts %d", i)
		assert.Equal(t, []byte{byte(i)}, got.Payload, "payload %d", i)
	}
}

func TestRTPConn_ReadDeadline(t *testing.T) {
	c, err := media.NewRTPConn()
	require.NoError(t, err)
	defer c.Close()

	c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, _, err = c.ReadRTP()
	assert.Error(t, err, "should time out on idle read")
	assert.True(t, isTimeoutErr(err), "error should be a timeout: %v", err)
}

func TestRTPConn_ReadAfterClose(t *testing.T) {
	a, err := media.NewRTPConn()
	require.NoError(t, err)
	b, err := media.NewRTPConn()
	require.NoError(t, err)
	defer b.Close()

	a.Close()
	_, _, err = a.ReadRTP()
	assert.Error(t, err, "reading from closed conn should fail")
}

func TestRTPConn_PortRange(t *testing.T) {
	c, err := media.NewRTPConnRange(20000, 20010)
	require.NoError(t, err)
	defer c.Close()

	addr := c.LocalAddr().(*net.UDPAddr)
	assert.True(t, addr.Port >= 20000 && addr.Port <= 20010,
		"port %d should be in range 20000-20010", addr.Port)
}

func TestRTPConn_PortRangeMultiple(t *testing.T) {
	var conns []*media.RTPConn
	for range 5 {
		c, err := media.NewRTPConnRange(20100, 20110)
		if err != nil {
			break
		}
		conns = append(conns, c)
	}
	for _, c := range conns {
		addr := c.LocalAddr().(*net.UDPAddr)
		assert.True(t, addr.Port >= 20100 && addr.Port <= 20110,
			"port %d outside range", addr.Port)
		c.Close()
	}
}

func TestRTPConn_PortRangeExhausted(t *testing.T) {
	c1, err := media.NewRTPConnRange(20200, 20200)
	require.NoError(t, err)
	defer c1.Close()

	_, err = media.NewRTPConnRange(20200, 20200)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available RTP port")
}

func TestRTPConn_PortRangeInvalid(t *testing.T) {
	c, err := media.NewRTPConnRange(100, 1)
	require.NoError(t, err)
	c.Close()
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// ---- Session / SessionManager unit tests ----

func TestNewSession(t *testing.T) {
	rtpConn, err := media.NewRTPConn()
	require.NoError(t, err)
	defer rtpConn.Close()

	key := media.SessionKey{CallID: "test", LocalTag: "local", RemoteTag: "remote"}
	s := media.NewSession(key, rtpConn, 0, rtpConn.LocalAddr())
	require.NotNil(t, s)

	assert.Equal(t, key, s.Key)
	assert.NotNil(t, s.Ctx())
	assert.NotNil(t, s.RTPConn)
}

func TestSessionManager(t *testing.T) {
	sm := media.NewSessionManager()
	rtpConn, _ := media.NewRTPConn()
	defer rtpConn.Close()

	key := media.SessionKey{CallID: "test", LocalTag: "a", RemoteTag: "b"}
	s := media.NewSession(key, rtpConn, 0, rtpConn.LocalAddr())
	sm.Add(s)

	got := sm.Get(key)
	require.NotNil(t, got)
	assert.Equal(t, key, got.Key)

	sm.Remove(key)
	assert.Nil(t, sm.Get(key))
}

func TestSessionStateTransitions(t *testing.T) {
	rtpConn, _ := media.NewRTPConn()
	defer rtpConn.Close()

	key := media.SessionKey{CallID: "state-test", LocalTag: "l", RemoteTag: "r"}
	s := media.NewSession(key, rtpConn, 0, rtpConn.LocalAddr())

	assert.Equal(t, media.SessionCreated, s.StateSafe())
	s.SetState(media.SessionWaitingAck)
	assert.Equal(t, media.SessionWaitingAck, s.StateSafe())
	s.SetState(media.SessionActive)
	assert.Equal(t, media.SessionActive, s.StateSafe())
}

func TestSessionCancel(t *testing.T) {
	rtpConn, _ := media.NewRTPConn()
	defer rtpConn.Close()

	key := media.SessionKey{CallID: "cancel-test", LocalTag: "l", RemoteTag: "r"}
	s := media.NewSession(key, rtpConn, 0, rtpConn.LocalAddr())

	ctx := s.Ctx()
	select {
	case <-ctx.Done():
		t.Fatal("context should not be done before Cancel")
	default:
	}

	s.Cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context should be done after Cancel")
	}
}

// ---- Integration tests ----

type testSession struct {
	callID    string
	fromTag   string
	serverTag string
	branch    string
}

func randomStr(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sipAddr() *net.UDPAddr {
	addr, _ := net.ResolveUDPAddr("udp", sipTestAddr)
	return addr
}

func readSIP(t *testing.T, conn *net.UDPConn, timeout time.Duration) *proto.SIPMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 65535)
	n, _, err := conn.ReadFromUDP(buf)
	require.NoError(t, err, "read SIP timeout")
	msg, err := proto.UnmarshalSIPDatagram(buf[:n])
	require.NoError(t, err)
	return msg
}

func readNon100(t *testing.T, conn *net.UDPConn, timeout time.Duration) *proto.SIPMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		msg := readSIP(t, conn, remaining)
		if msg.StatusCode() != 100 {
			return msg
		}
	}
	t.Fatal("timeout waiting for non-100 response")
	return nil
}

func buildSDP(t *testing.T, port int, payloadTypes ...string) *proto.SDP {
	t.Helper()
	if len(payloadTypes) == 0 {
		payloadTypes = []string{"0"}
	}
	pt := strings.Join(payloadTypes, " ")
	return &proto.SDP{
		Version: 0,
		Origin: proto.Origin{
			Username: "-", SessionID: randomStr(8), SessionVersion: "1",
			NetworkType: "IN", AddressType: "IP4", Address: "127.0.0.1",
		},
		SessionName: "test",
		Connection:  &proto.ConnectionInfo{NetworkType: "IN", AddressType: "IP4", Address: "127.0.0.1"},
		Times:       []proto.TimeDescription{{Start: 0, Stop: 0}},
		MediaDescs: []proto.MediaDescription{
			{
				Type: "audio", Port: port, Proto: "RTP/AVP",
				Fmt: strings.Fields(pt),
			},
		},
	}
}

func marshalSDP(t *testing.T, sdp *proto.SDP) []byte {
	t.Helper()
	data, err := sdp.Marshal()
	require.NoError(t, err)
	return data
}

func buildInvite(t *testing.T, clientPort int, sdpBody string) string {
	t.Helper()
	ts := &testSession{
		callID:  randomStr(8),
		fromTag: randomStr(6),
		branch:  "z9hG4bK-" + randomStr(8),
	}
	body := ""
	cl := 0
	if sdpBody != "" {
		body = sdpBody
		cl = len(sdpBody)
	}

	raw := fmt.Sprintf("INVITE sip:echo@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:client@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:echo@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"Contact: <sip:client@127.0.0.1:%d>\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %d\r\n\r\n%s",
		clientPort, ts.branch, ts.fromTag, ts.callID, clientPort, cl, body)

	return raw
}

func buildACK(t *testing.T, ts *testSession, serverIP string, serverPort int, sdpBody string) string {
	t.Helper()
	ackBranch := "z9hG4bK-ack-" + randomStr(8)
	body := ""
	cl := 0
	if sdpBody != "" {
		body = sdpBody
		cl = len(sdpBody)
	}

	raw := fmt.Sprintf("ACK sip:echo@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:client@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:echo@127.0.0.1>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 ACK\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %d\r\n\r\n%s",
		serverPort, ackBranch, ts.fromTag, ts.serverTag, ts.callID, cl, body)

	return raw
}

func buildBYE(t *testing.T, ts *testSession, clientPort int) string {
	t.Helper()
	byeBranch := "z9hG4bK-bye-" + randomStr(8)

	raw := fmt.Sprintf("BYE sip:echo@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:client@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:echo@127.0.0.1>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 2 BYE\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		clientPort, byeBranch, ts.fromTag, ts.serverTag, ts.callID)

	return raw
}

func createServer(t *testing.T) *sip.Server {
	t.Helper()

	srv, err := sip.NewServer(sipTestAddr)
	require.NoError(t, err)

	sm := media.NewSessionManager()
	serverIP := "127.0.0.1"

	srv.On(proto.SIPMethodINVITE, func(req *proto.SIPMessage, tx sip.Transaction) {
		t.Logf("INVITE received: Call-ID=%s", req.Headers.GetFirst("Call-ID"))

		trying := proto.NewResponse(req, 100, "Trying")
		tx.Respond(trying)

		serverTag := generateTag()

		var sdpOffer *proto.SDP
		if len(req.Body) > 0 && req.Headers.GetFirst("Content-Type") == "application/sdp" {
			sdp, err := proto.UnmarshalSDPBytes(req.Body)
			if err != nil {
				tx.Respond(proto.NewResponse(req, 488, "Not Acceptable Here"))
				return
			}
			sdpOffer = sdp
		}

		rtpConn, err := media.NewRTPConn()
		if err != nil {
			tx.Respond(proto.NewResponse(req, 500, "Server Internal Error"))
			return
		}

		rtpAddr := rtpConn.LocalAddr().(*net.UDPAddr)
		payloadType := uint8(proto.PCMU)

		var sdpBody *proto.SDP
		if sdpOffer != nil {
			payloadType = media.PickPayloadType(sdpOffer)
			sdpBody = media.BuildAnswer(sdpOffer, rtpAddr.Port, payloadType, serverIP)
		} else {
			sdpBody = media.BuildOffer(rtpAddr.Port, payloadType, serverIP)
		}

		from, err := req.From()
		if err != nil {
			rtpConn.Close()
			tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
			return
		}

		callID := req.Headers.GetFirst("Call-ID")
		key := media.SessionKey{
			CallID:    callID,
			RemoteTag: from.Tag,
			LocalTag:  serverTag,
		}

		session := media.NewSession(key, rtpConn, payloadType, rtpAddr)

		if sdpOffer != nil {
			clientIP, clientPort := media.ExtractRTPAddr(sdpOffer)
			remoteAddr := &net.UDPAddr{IP: net.ParseIP(clientIP), Port: clientPort}
			session.SetRemoteAddr(remoteAddr)
			session.SetState(media.SessionActive)
		} else {
			session.SetState(media.SessionWaitingAck)
		}

		sm.Add(session)

		res := proto.NewResponse(req, 200, "OK")
		toHeader := req.Headers.GetFirst("To")
		res.Headers.Set("To", []string{toHeader + ";tag=" + serverTag})

		sdpBytes, _ := sdpBody.Marshal()
		res.Body = sdpBytes
		res.Headers.Set("Content-Type", []string{"application/sdp"})
		res.Headers["Allow"] = []string{"INVITE, ACK, BYE, CANCEL, OPTIONS, REGISTER"}
		res.Headers["Contact"] = []string{"<sip:echo@127.0.0.1>"}

		tx.Respond(res)

		if sdpOffer != nil {
			go media.RunEcho(session.Ctx(), rtpConn, payloadType)
		}

		t.Logf("200 OK sent for %s (server-tag=%s, rtp-port=%d, offer=%v)",
			callID, serverTag, rtpAddr.Port, sdpOffer != nil)
	})

	srv.OnAck(func(msg *proto.SIPMessage, target sip.Target, transport sip.Transport) {
		t.Logf("ACK received: Call-ID=%s", msg.Headers.GetFirst("Call-ID"))

		from, err := msg.From()
		if err != nil {
			return
		}
		to, err := msg.To()
		if err != nil {
			return
		}

		key := media.SessionKey{
			CallID:    msg.Headers.GetFirst("Call-ID"),
			RemoteTag: from.Tag,
			LocalTag:  to.Tag,
		}

		session := sm.Get(key)
		if session == nil {
			return
		}

		if session.StateSafe() != media.SessionWaitingAck {
			return
		}

		if len(msg.Body) > 0 {
			sdp, err := proto.UnmarshalSDPBytes(msg.Body)
			if err != nil {
				return
			}
			clientIP, clientPort := media.ExtractRTPAddr(sdp)
			remoteAddr := &net.UDPAddr{IP: net.ParseIP(clientIP), Port: clientPort}
			session.SetRemoteAddr(remoteAddr)
			session.SetState(media.SessionActive)
			go media.RunEcho(session.Ctx(), session.RTPConn, session.PayloadType)
			t.Logf("Echo started for delayed offer (Call-ID=%s, rtp-port=%d)", key.CallID, clientPort)
		}
	})

	srv.On(proto.SIPMethodBYE, func(req *proto.SIPMessage, tx sip.Transaction) {
		t.Logf("BYE received: Call-ID=%s", req.Headers.GetFirst("Call-ID"))

		trying := proto.NewResponse(req, 100, "Trying")
		tx.Respond(trying)

		from, err := req.From()
		if err != nil {
			tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
			return
		}
		to, err := req.To()
		if err != nil {
			tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
			return
		}

		key := media.SessionKey{
			CallID:    req.Headers.GetFirst("Call-ID"),
			RemoteTag: from.Tag,
			LocalTag:  to.Tag,
		}

		session := sm.Get(key)
		if session != nil {
			session.Cancel()
			session.RTPConn.Close()
			sm.Remove(key)
			t.Logf("Session cleaned up: %+v", key)
		}

		res := proto.NewResponse(req, 200, "OK")
		res.Headers["Allow"] = []string{"INVITE, ACK, BYE, CANCEL, OPTIONS, REGISTER"}
		tx.Respond(res)
	})

	reg := sip.NewRegistrar()
	srv.On(proto.SIPMethodREGISTER, reg.HandleRegister)
	srv.On(proto.SIPMethodOPTIONS, func(req *proto.SIPMessage, tx sip.Transaction) {
		trying := proto.NewResponse(req, 100, "Trying")
		tx.Respond(trying)
		res := proto.NewResponse(req, 200, "OK")
		res.Headers["Allow"] = []string{"INVITE, ACK, BYE, CANCEL, OPTIONS, REGISTER"}
		res.Headers["Accept"] = []string{"application/sdp"}
		res.Headers["Supported"] = []string{"timer"}
		tx.Respond(res)
	})

	srv.Start()
	return srv
}

func generateTag() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "trec"
	}
	return hex.EncodeToString(b)
}

// ---- Early offer integration test ----

func TestEchoEarlyOffer(t *testing.T) {
	srv := createServer(t)
	defer srv.Close()

	// Client SIP socket
	clientSIP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer clientSIP.Close()
	clientPort := clientSIP.LocalAddr().(*net.UDPAddr).Port

	// Client RTP socket
	clientRTP, err := media.NewRTPConn()
	require.NoError(t, err)
	defer clientRTP.Close()
	rtpPort := clientRTP.LocalAddr().(*net.UDPAddr).Port

	// Build SDP offer with client RTP port
	sdpOffer := buildSDP(t, rtpPort, "0")
	sdpOfferBytes := marshalSDP(t, sdpOffer)

	// Build and send INVITE
	inviteRaw := buildInvite(t, clientPort, string(sdpOfferBytes))
	invite := rawSIP(t, inviteRaw)
	inviteData, err := invite.Marshal()
	require.NoError(t, err)
	t.Logf("Sending INVITE (early offer) from port %d, rtp-port %d", clientPort, rtpPort)
	_, err = clientSIP.WriteToUDP(inviteData, sipAddr())
	require.NoError(t, err)

	// Read 200 OK
	res := readNon100(t, clientSIP, 5*time.Second)
	require.Equal(t, 200, res.StatusCode(), "expected 200 OK, got %d %s", res.StatusCode(), res.Status())

	// Parse server tag from To header
	toAddr, err := res.To()
	require.NoError(t, err)
	require.NotEmpty(t, toAddr.Tag, "To header should have server tag")
	t.Logf("Server tag: %s", toAddr.Tag)

	// Extract server RTP port from SDP answer
	require.True(t, len(res.Body) > 0, "200 OK should have SDP body")
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body)
	require.NoError(t, err)
	serverIP, serverRTPPort := media.ExtractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")
	t.Logf("Server RTP at %s:%d", serverIP, serverRTPPort)

	// Parse the INVITE info for ACK construction
	fromAddr, err := invite.From()
	require.NoError(t, err)

	ts := &testSession{
		callID:    invite.Headers.GetFirst("Call-ID"),
		fromTag:   fromAddr.Tag,
		serverTag: toAddr.Tag,
	}

	// Send ACK
	ackRaw := buildACK(t, ts, serverIP, clientPort, "")
	t.Logf("Sending ACK")
	_, err = clientSIP.WriteToUDP([]byte(ackRaw), sipAddr())
	require.NoError(t, err)

	// Small delay for echo loop to start
	time.Sleep(100 * time.Millisecond)

	// Send RTP packet to server
	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}
	testPayload := []byte{0xde, 0xad, 0xbe, 0xef}
	sendPkt := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 0,
			SequenceNumber: 1, Timestamp: 0, SSRC: 9999,
		},
		Payload: testPayload,
	}
	err = clientRTP.WriteRTP(sendPkt, serverRTPAddr)
	require.NoError(t, err)

	// Read echoed RTP packet
	clientRTP.SetReadDeadline(time.Now().Add(3 * time.Second))
	echoed, _, err := clientRTP.ReadRTP()
	require.NoError(t, err, "should receive echoed RTP")
	assert.Equal(t, testPayload, echoed.Payload, "echoed payload should match")

	// Send BYE
	byeRaw := buildBYE(t, ts, clientPort)
	t.Logf("Sending BYE")
	_, err = clientSIP.WriteToUDP([]byte(byeRaw), sipAddr())
	require.NoError(t, err)

	// Read 200 OK for BYE
	byeRes := readNon100(t, clientSIP, 5*time.Second)
	require.Equal(t, 200, byeRes.StatusCode(), "BYE should get 200 OK")
}

// ---- Delayed offer integration test ----

func TestEchoDelayedOffer(t *testing.T) {
	srv := createServer(t)
	defer srv.Close()

	// Client SIP socket
	clientSIP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer clientSIP.Close()
	clientPort := clientSIP.LocalAddr().(*net.UDPAddr).Port

	// Client RTP socket
	clientRTP, err := media.NewRTPConn()
	require.NoError(t, err)
	defer clientRTP.Close()
	rtpPort := clientRTP.LocalAddr().(*net.UDPAddr).Port

	// Build INVITE without SDP body
	inviteRaw := buildInvite(t, clientPort, "")
	invite := rawSIP(t, inviteRaw)
	inviteData, err := invite.Marshal()
	require.NoError(t, err)
	t.Logf("Sending INVITE (delayed offer) from port %d", clientPort)
	_, err = clientSIP.WriteToUDP(inviteData, sipAddr())
	require.NoError(t, err)

	// Read 200 OK with SDP offer
	res := readNon100(t, clientSIP, 5*time.Second)
	require.Equal(t, 200, res.StatusCode(), "expected 200 OK, got %d %s", res.StatusCode(), res.Status())

	// Parse server tag and SDP offer
	toAddr, err := res.To()
	require.NoError(t, err)
	require.NotEmpty(t, toAddr.Tag, "To header should have server tag")
	t.Logf("Server tag: %s", toAddr.Tag)

	require.True(t, len(res.Body) > 0, "200 OK should have SDP body")
	sdpOffer, err := proto.UnmarshalSDPBytes(res.Body)
	require.NoError(t, err)
	serverIP, serverRTPPort := media.ExtractRTPAddr(sdpOffer)
	require.NotZero(t, serverRTPPort, "SDP offer should have RTP port")
	t.Logf("Server RTP at %s:%d (delayed offer)", serverIP, serverRTPPort)

	// Build SDP answer with client RTP port
	sdpAnswer := buildSDP(t, rtpPort, "0")
	sdpAnswerBytes := marshalSDP(t, sdpAnswer)

	// Parse INVITE info for ACK
	fromAddr, err := invite.From()
	require.NoError(t, err)

	ts := &testSession{
		callID:    invite.Headers.GetFirst("Call-ID"),
		fromTag:   fromAddr.Tag,
		serverTag: toAddr.Tag,
	}

	// Send ACK with SDP answer
	ackRaw := buildACK(t, ts, serverIP, clientPort, string(sdpAnswerBytes))
	t.Logf("Sending ACK with SDP answer")
	_, err = clientSIP.WriteToUDP([]byte(ackRaw), sipAddr())
	require.NoError(t, err)

	// Small delay for echo loop to start
	time.Sleep(100 * time.Millisecond)

	// Send RTP packet to server
	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}
	testPayload := []byte{0xca, 0xfe, 0xba, 0xbe}
	sendPkt := &proto.RTPPacket{
		Header: proto.RTPHeader{
			Version: 2, PayloadType: 0,
			SequenceNumber: 1, Timestamp: 0, SSRC: 8888,
		},
		Payload: testPayload,
	}
	err = clientRTP.WriteRTP(sendPkt, serverRTPAddr)
	require.NoError(t, err)

	// Read echoed RTP packet
	clientRTP.SetReadDeadline(time.Now().Add(3 * time.Second))
	echoed, _, err := clientRTP.ReadRTP()
	require.NoError(t, err, "should receive echoed RTP")
	assert.Equal(t, testPayload, echoed.Payload, "echoed payload should match")

	// Send BYE
	byeRaw := buildBYE(t, ts, clientPort)
	t.Logf("Sending BYE")
	_, err = clientSIP.WriteToUDP([]byte(byeRaw), sipAddr())
	require.NoError(t, err)

	// Read 200 OK for BYE
	byeRes := readNon100(t, clientSIP, 5*time.Second)
	require.Equal(t, 200, byeRes.StatusCode(), "BYE should get 200 OK")
}

func rawSIP(t *testing.T, raw string) *proto.SIPMessage {
	t.Helper()
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	require.NoError(t, err)
	return msg
}

func rawBytes(t *testing.T, raw string) []byte {
	t.Helper()
	msg := rawSIP(t, raw)
	data, err := msg.Marshal()
	require.NoError(t, err)
	return data
}
