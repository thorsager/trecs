package proto

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func putRTCPHeader(pad bool, count uint8, typ RTCPPacketType, length uint16) []byte {
	buf := make([]byte, 4)
	buf[0] = (rtcpVersion << 6) | (count & 0x1F)
	if pad {
		buf[0] |= 1 << 5
	}
	buf[1] = byte(typ)
	binary.BigEndian.PutUint16(buf[2:4], length)
	return buf
}

func TestParseRTCP_SenderReport(t *testing.T) {
	body := make([]byte, 0, 52)
	body = append(body, putRTCPHeader(false, 1, RTCPTypeSR, 12)...)
	body = putU32BEAppend(body, 0x902f9e2e)
	body = putU64BEAppend(body, 0xdf3cf7581c604540)
	body = putU32BEAppend(body, 0x11223344)
	body = putU32BEAppend(body, 17)
	body = putU32BEAppend(body, 3400)
	body = append(body, makeReceptionReport(0xbc5e9a40, 0, 0, 0x46e1, 273, 0x9f36432, 150137)...)

	packets, err := ParseRTCP(body)
	require.NoError(t, err)
	require.Len(t, packets, 1)

	sr, ok := packets[0].(*SenderReport)
	require.True(t, ok)
	assert.Equal(t, uint32(0x902f9e2e), sr.SSRC)
	assert.Equal(t, uint64(0xdf3cf7581c604540), sr.NTPTime)
	assert.Equal(t, uint32(0x11223344), sr.RTPTime)
	assert.Equal(t, uint32(17), sr.PacketCount)
	assert.Equal(t, uint32(3400), sr.OctetCount)
	require.Len(t, sr.Reports, 1)
	assert.Equal(t, uint32(0xbc5e9a40), sr.Reports[0].SSRC)
	assert.Equal(t, uint32(0x46e1), sr.Reports[0].LastSequenceNumber)
	assert.Equal(t, uint32(273), sr.Reports[0].Jitter)
}

func TestParseRTCP_ReceiverReport(t *testing.T) {
	body := make([]byte, 0, 8)
	body = append(body, putRTCPHeader(false, 0, RTCPTypeRR, 1)...)
	body = putU32BEAppend(body, 0x11111111)

	packets, err := ParseRTCP(body)
	require.NoError(t, err)
	require.Len(t, packets, 1)

	rr, ok := packets[0].(*ReceiverReport)
	require.True(t, ok)
	assert.Equal(t, uint32(0x11111111), rr.SSRC)
	assert.Empty(t, rr.Reports)
}

func TestParseRTCP_ReceiverReportWithReports(t *testing.T) {
	body := make([]byte, 0, 8+48)
	body = append(body, putRTCPHeader(false, 2, RTCPTypeRR, 13)...)
	body = putU32BEAppend(body, 0x11111111)
	body = append(body, makeReceptionReport(0xaaaaaaaa, 5, 10, 54321, 100, 0xffffffff, 200)...)
	body = append(body, makeReceptionReport(0xbbbbbbbb, 0, 0, 9876, 50, 0, 0)...)

	packets, err := ParseRTCP(body)
	require.NoError(t, err)
	require.Len(t, packets, 1)
	rr := packets[0].(*ReceiverReport)
	require.Len(t, rr.Reports, 2)
	assert.Equal(t, uint32(0xaaaaaaaa), rr.Reports[0].SSRC)
	assert.Equal(t, uint8(5), rr.Reports[0].FractionLost)
	assert.Equal(t, uint32(10), rr.Reports[0].TotalLost)
	assert.Equal(t, uint32(0xbbbbbbbb), rr.Reports[1].SSRC)
}

func makeReceptionReport(ssrc uint32, fractionLost uint8, totalLost uint32, lastSeq, jitter, lsr, delay uint32) []byte {
	buf := make([]byte, 24)
	binary.BigEndian.PutUint32(buf[0:4], ssrc)
	buf[4] = fractionLost
	buf[5] = byte(totalLost >> 16)
	buf[6] = byte(totalLost >> 8)
	buf[7] = byte(totalLost)
	binary.BigEndian.PutUint32(buf[8:12], lastSeq)
	binary.BigEndian.PutUint32(buf[12:16], jitter)
	binary.BigEndian.PutUint32(buf[16:20], lsr)
	binary.BigEndian.PutUint32(buf[20:24], delay)
	return buf
}

func TestParseRTCP_SDES(t *testing.T) {
	body := make([]byte, 0, 4+4+2+9+1)
	body = append(body, putRTCPHeader(false, 1, RTCPTypeSDES, 4)...)
	body = putU32BEAppend(body, 0x902f9e2e)
	body = append(body, 0x01, 0x09)
	body = append(body, []byte("test@host")...)
	body = append(body, 0x00)

	packets, err := ParseRTCP(body)
	require.NoError(t, err)
	require.Len(t, packets, 1)

	sdes, ok := packets[0].(*SourceDescription)
	require.True(t, ok)
	require.Len(t, sdes.Chunks, 1)
	assert.Equal(t, uint32(0x902f9e2e), sdes.Chunks[0].Source)
	require.Len(t, sdes.Chunks[0].Items, 1)
	assert.Equal(t, SDESCNAME, sdes.Chunks[0].Items[0].Type)
	assert.Equal(t, "test@host", sdes.Chunks[0].Items[0].Text)
}

func TestParseRTCP_SDES_MultipleItems(t *testing.T) {
	body := make([]byte, 0, 4)
	body = append(body, putRTCPHeader(false, 1, RTCPTypeSDES, 5)...)
	body = putU32BEAppend(body, 0x12345678)
	body = append(body, 0x01, 0x04)
	body = append(body, []byte("user")...)
	body = append(body, 0x02, 0x04)
	body = append(body, []byte("John")...)
	body = append(body, 0x00)
	body = padTo4(body)
	body[2] = 0
	body[3] = byte(len(body)/4 - 1)

	packets, err := ParseRTCP(body)
	require.NoError(t, err)
	require.Len(t, packets, 1)

	sdes := packets[0].(*SourceDescription)
	require.Len(t, sdes.Chunks, 1)
	require.Len(t, sdes.Chunks[0].Items, 2)
	assert.Equal(t, SDESCNAME, sdes.Chunks[0].Items[0].Type)
	assert.Equal(t, "user", sdes.Chunks[0].Items[0].Text)
	assert.Equal(t, SDESName, sdes.Chunks[0].Items[1].Type)
	assert.Equal(t, "John", sdes.Chunks[0].Items[1].Text)
}

func TestParseRTCP_BYE(t *testing.T) {
	body := make([]byte, 0, 8)
	body = append(body, putRTCPHeader(false, 1, RTCPTypeBYE, 1)...)
	body = putU32BEAppend(body, 0xDEADBEEF)

	packets, err := ParseRTCP(body)
	require.NoError(t, err)
	require.Len(t, packets, 1)

	bye, ok := packets[0].(*Goodbye)
	require.True(t, ok)
	assert.Equal(t, []uint32{0xDEADBEEF}, bye.Sources)
	assert.Empty(t, bye.Reason)
}

func TestParseRTCP_BYE_WithReason(t *testing.T) {
	reason := "camera off"
	body := make([]byte, 0, 8+1+len(reason))
	body = append(body, putRTCPHeader(false, 1, RTCPTypeBYE, 3)...)
	body = putU32BEAppend(body, 0xDEADBEEF)
	body = append(body, byte(len(reason)))
	body = append(body, []byte(reason)...)
	body = padTo4(body)
	pktLen := len(body)
	binary.BigEndian.PutUint16(body[2:4], uint16(pktLen/4-1))

	packets, err := ParseRTCP(body)
	require.NoError(t, err)
	require.Len(t, packets, 1)

	bye := packets[0].(*Goodbye)
	assert.Equal(t, reason, bye.Reason)
}

func TestParseRTCP_APP(t *testing.T) {
	appData := []byte{0x01, 0x02, 0x03, 0x04}
	pktLen := 12 + len(appData)
	body := make([]byte, 0, pktLen)
	body = append(body, putRTCPHeader(false, 3, RTCPTypeAPP, uint16(pktLen/4-1))...)
	body = putU32BEAppend(body, 0x55555555)
	body = append(body, []byte("TEST")...)
	body = append(body, appData...)

	packets, err := ParseRTCP(body)
	require.NoError(t, err)
	require.Len(t, packets, 1)

	app, ok := packets[0].(*ApplicationDefined)
	require.True(t, ok)
	assert.Equal(t, uint8(3), app.SubType)
	assert.Equal(t, uint32(0x55555555), app.SSRC)
	assert.Equal(t, "TEST", app.Name)
	assert.Equal(t, appData, app.Data)
}

func TestParseRTCP_Compound(t *testing.T) {
	var compound []byte

	srBody := make([]byte, 0, 28)
	srBody = append(srBody, putRTCPHeader(false, 0, RTCPTypeSR, 6)...)
	srBody = putU32BEAppend(srBody, 0xAAAAAAAA)
	srBody = putU64BEAppend(srBody, 0x1234567890ABCDEF)
	srBody = putU32BEAppend(srBody, 5000)
	srBody = putU32BEAppend(srBody, 42)
	srBody = putU32BEAppend(srBody, 64000)
	compound = append(compound, srBody...)

	sdesBody := make([]byte, 0, 4+4+2+4+1)
	sdesBody = append(sdesBody, putRTCPHeader(false, 1, RTCPTypeSDES, 3)...)
	sdesBody = putU32BEAppend(sdesBody, 0xAAAAAAAA)
	sdesBody = append(sdesBody, 0x01, 0x04)
	sdesBody = append(sdesBody, []byte("test")...)
	sdesBody = append(sdesBody, 0x00)
	sdesBody = padTo4(sdesBody)
	binary.BigEndian.PutUint16(sdesBody[2:4], uint16(len(sdesBody)/4-1))
	compound = append(compound, sdesBody...)

	byeBody := make([]byte, 0, 8)
	byeBody = append(byeBody, putRTCPHeader(false, 1, RTCPTypeBYE, 1)...)
	byeBody = putU32BEAppend(byeBody, 0xAAAAAAAA)
	compound = append(compound, byeBody...)

	packets, err := ParseRTCP(compound)
	require.NoError(t, err)
	require.Len(t, packets, 3)

	_, ok := packets[0].(*SenderReport)
	require.True(t, ok)

	_, ok = packets[1].(*SourceDescription)
	require.True(t, ok)

	_, ok = packets[2].(*Goodbye)
	require.True(t, ok)
}

func TestParseRTCP_Empty(t *testing.T) {
	_, err := ParseRTCP(nil)
	assert.Error(t, err)

	_, err = ParseRTCP([]byte{})
	assert.Error(t, err)
}

func TestParseRTCP_BadVersion(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00}
	_, err := ParseRTCP(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestParseRTCP_HeaderTooShort(t *testing.T) {
	_, err := ParseRTCP([]byte{0x80, 0xC8})
	assert.Error(t, err)
}

func TestParseRTCP_Truncated(t *testing.T) {
	data := putRTCPHeader(false, 0, RTCPTypeRR, 10)
	_, err := ParseRTCP(data)
	assert.Error(t, err)
}

func TestParseRTCP_RawPacket(t *testing.T) {
	data := putRTCPHeader(false, 0, RTCPPacketType(255), 0)
	packets, err := ParseRTCP(data)
	require.NoError(t, err)
	require.Len(t, packets, 1)
	_, ok := packets[0].(*RawRTCP)
	require.True(t, ok)
}

func padTo4(buf []byte) []byte {
	r := len(buf) % 4
	if r != 0 {
		buf = append(buf, make([]byte, 4-r)...)
	}
	return buf
}

func TestRTCP_DestinationSSRC_SR(t *testing.T) {
	sr := &SenderReport{
		SSRC: 0x11111111,
		Reports: []ReceptionReport{
			{SSRC: 0x22222222},
			{SSRC: 0x33333333},
		},
	}
	ssrcs := sr.DestinationSSRC()
	assert.Equal(t, []uint32{0x22222222, 0x33333333, 0x11111111}, ssrcs)
}

func TestRTCP_DestinationSSRC_RR(t *testing.T) {
	rr := &ReceiverReport{
		SSRC: 0x11111111,
		Reports: []ReceptionReport{
			{SSRC: 0x22222222},
		},
	}
	ssrcs := rr.DestinationSSRC()
	assert.Equal(t, []uint32{0x22222222}, ssrcs)
}

func TestRTCP_DestinationSSRC_BYE(t *testing.T) {
	bye := &Goodbye{Sources: []uint32{0xAAAAAAAA, 0xBBBBBBBB}}
	ssrcs := bye.DestinationSSRC()
	assert.Equal(t, []uint32{0xAAAAAAAA, 0xBBBBBBBB}, ssrcs)
}

func TestRTCP_StringFormats(t *testing.T) {
	assert.Equal(t, "SR", RTCPTypeSR.String())
	assert.Equal(t, "RR", RTCPTypeRR.String())
	assert.Equal(t, "SDES", RTCPTypeSDES.String())
	assert.Equal(t, "BYE", RTCPTypeBYE.String())
	assert.Equal(t, "APP", RTCPTypeAPP.String())
	assert.Contains(t, RTCPPacketType(255).String(), "255")

	assert.Equal(t, "CNAME", SDESCNAME.String())
	assert.Equal(t, "END", SDESEnd.String())
	assert.Equal(t, "NAME", SDESName.String())
	assert.Contains(t, SDESType(99).String(), "99")
}
