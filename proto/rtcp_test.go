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

func TestRTCPMarshal_SenderReport_Minimal(t *testing.T) {
	sr := &SenderReport{
		SSRC:        0x902f9e2e,
		NTPTime:     0xdf3cf7581c604540,
		RTPTime:     0x11223344,
		PacketCount: 17,
		OctetCount:  3400,
	}
	data, err := sr.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 28)

	assert.Equal(t, byte(0x80), data[0], "V=2 P=0 RC=0")
	assert.Equal(t, byte(0xC8), data[1], "PT=200 (SR)")
	assert.Equal(t, []byte{0x00, 0x06}, data[2:4], "Length=6 (28/4-1)")

	var expected []byte
	expected = putU32BEAppend(expected, 0x902f9e2e)
	expected = putU64BEAppend(expected, 0xdf3cf7581c604540)
	expected = putU32BEAppend(expected, 0x11223344)
	expected = putU32BEAppend(expected, 17)
	expected = putU32BEAppend(expected, 3400)

	got := make([]byte, 0, len(data)-4)
	got = append(got, data[4:]...)
	assert.Equal(t, expected, got, "SR body (SSRC+NTP+RTP+Pkts+Octets)")

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	sr2, ok := parsed[0].(*SenderReport)
	require.True(t, ok)
	assert.Equal(t, sr.SSRC, sr2.SSRC)
	assert.Equal(t, sr.NTPTime, sr2.NTPTime)
	assert.Equal(t, sr.RTPTime, sr2.RTPTime)
	assert.Equal(t, sr.PacketCount, sr2.PacketCount)
	assert.Equal(t, sr.OctetCount, sr2.OctetCount)
	assert.Empty(t, sr2.Reports)
}

func TestRTCPMarshal_SenderReport_WithReports(t *testing.T) {
	sr := &SenderReport{
		SSRC:        0x902f9e2e,
		NTPTime:     0xdf3cf7581c604540,
		RTPTime:     0x11223344,
		PacketCount: 17,
		OctetCount:  3400,
		Reports: []ReceptionReport{
			{
				SSRC:               0xbc5e9a40,
				FractionLost:       0,
				TotalLost:          0,
				LastSequenceNumber: 0x46e1,
				Jitter:             273,
				LastSenderReport:   0x9f36432,
				Delay:              150137,
			},
		},
	}
	data, err := sr.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 52)

	assert.Equal(t, byte(0x81), data[0], "V=2 P=0 RC=1")
	assert.Equal(t, byte(0xC8), data[1], "PT=200 (SR)")
	assert.Equal(t, []byte{0x00, 0x0C}, data[2:4], "Length=12 (52/4-1)")

	assert.Equal(t, uint32(0x902f9e2e), binary.BigEndian.Uint32(data[4:8]))
	assert.Equal(t, uint64(0xdf3cf7581c604540), binary.BigEndian.Uint64(data[8:16]))
	assert.Equal(t, uint32(0x11223344), binary.BigEndian.Uint32(data[16:20]))
	assert.Equal(t, uint32(17), binary.BigEndian.Uint32(data[20:24]))
	assert.Equal(t, uint32(3400), binary.BigEndian.Uint32(data[24:28]))

	off := 28
	assert.Equal(t, uint32(0xbc5e9a40), binary.BigEndian.Uint32(data[off:off+4]))
	assert.Equal(t, byte(0), data[off+4], "fraction lost")
	assert.Equal(t, byte(0), data[off+5], "total lost[23:16]")
	assert.Equal(t, byte(0), data[off+6], "total lost[15:8]")
	assert.Equal(t, byte(0), data[off+7], "total lost[7:0]")
	assert.Equal(t, uint32(0x46e1), binary.BigEndian.Uint32(data[off+8:off+12]))
	assert.Equal(t, uint32(273), binary.BigEndian.Uint32(data[off+12:off+16]))
	assert.Equal(t, uint32(0x9f36432), binary.BigEndian.Uint32(data[off+16:off+20]))
	assert.Equal(t, uint32(150137), binary.BigEndian.Uint32(data[off+20:off+24]))

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	sr2 := parsed[0].(*SenderReport)
	require.Len(t, sr2.Reports, 1)
	assert.Equal(t, sr.Reports[0], sr2.Reports[0])
}

func TestRTCPMarshal_SenderReport_ProfileExtensions(t *testing.T) {
	sr := &SenderReport{
		SSRC:              0x11111111,
		NTPTime:           0x1234567890ABCDEF,
		RTPTime:           5000,
		PacketCount:       42,
		OctetCount:        64000,
		ProfileExtensions: []byte{0xDE, 0xAD},
	}
	data, err := sr.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 32, "28 + 2 ext bytes + 2 padding = 32")

	assert.Equal(t, byte(0x80), data[0], "RC=0")
	assert.Equal(t, byte(0xC8), data[1])
	assert.Equal(t, []byte{0x00, 0x07}, data[2:4], "Length=7 (32/4-1)")

	assert.Equal(t, []byte{0xDE, 0xAD}, data[28:30], "profile extensions")
	assert.Equal(t, []byte{0x00, 0x00}, data[30:32], "padding to 4 bytes")

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	sr2 := parsed[0].(*SenderReport)
	assert.Equal(t, []byte{0xDE, 0xAD, 0x00, 0x00}, sr2.ProfileExtensions, "profile extensions include alignment padding")
}

func TestRTCPMarshal_ReceiverReport_Minimal(t *testing.T) {
	rr := &ReceiverReport{
		SSRC: 0x11111111,
	}
	data, err := rr.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 8)

	assert.Equal(t, byte(0x80), data[0], "V=2 P=0 RC=0")
	assert.Equal(t, byte(0xC9), data[1], "PT=201 (RR)")
	assert.Equal(t, []byte{0x00, 0x01}, data[2:4], "Length=1 (8/4-1)")
	assert.Equal(t, uint32(0x11111111), binary.BigEndian.Uint32(data[4:8]))

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	rr2 := parsed[0].(*ReceiverReport)
	assert.Equal(t, rr.SSRC, rr2.SSRC)
	assert.Empty(t, rr2.Reports)
}

func TestRTCPMarshal_ReceiverReport_WithReports(t *testing.T) {
	rr := &ReceiverReport{
		SSRC: 0x11111111,
		Reports: []ReceptionReport{
			{SSRC: 0xaaaaaaaa, FractionLost: 5, TotalLost: 10, LastSequenceNumber: 54321, Jitter: 100, LastSenderReport: 0xffffffff, Delay: 200},
			{SSRC: 0xbbbbbbbb, FractionLost: 0, TotalLost: 0, LastSequenceNumber: 9876, Jitter: 50, LastSenderReport: 0, Delay: 0},
		},
	}
	data, err := rr.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 56)

	assert.Equal(t, byte(0x82), data[0], "V=2 P=0 RC=2")
	assert.Equal(t, byte(0xC9), data[1], "PT=201 (RR)")
	assert.Equal(t, []byte{0x00, 0x0D}, data[2:4], "Length=13 (56/4-1)")

	off := 8
	for i, r := range rr.Reports {
		assert.Equal(t, r.SSRC, binary.BigEndian.Uint32(data[off:off+4]), "RR[%d] SSRC", i)
		assert.Equal(t, r.FractionLost, data[off+4], "RR[%d] fraction lost", i)
		assert.Equal(t, byte(r.TotalLost>>16), data[off+5], "RR[%d] total lost[23:16]", i)
		assert.Equal(t, byte(r.TotalLost>>8), data[off+6], "RR[%d] total lost[15:8]", i)
		assert.Equal(t, byte(r.TotalLost), data[off+7], "RR[%d] total lost[7:0]", i)
		assert.Equal(t, r.LastSequenceNumber, binary.BigEndian.Uint32(data[off+8:off+12]))
		off += 24
	}

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	rr2 := parsed[0].(*ReceiverReport)
	require.Len(t, rr2.Reports, 2)
	assert.Equal(t, rr.Reports[0], rr2.Reports[0])
	assert.Equal(t, rr.Reports[1], rr2.Reports[1])
}

func TestRTCPMarshal_ReceptionReport_BitFields(t *testing.T) {
	var buf [24]byte
	rr := &ReceptionReport{
		SSRC:               0xAABBCCDD,
		FractionLost:       0xAB,
		TotalLost:          0x123456,
		LastSequenceNumber: 0xDEADBEEF,
		Jitter:             0xCAFEBABE,
		LastSenderReport:   0x87654321,
		Delay:              0x01020304,
	}
	marshalReceptionReportTo(buf[:], rr)

	assert.Equal(t, byte(0xAA), buf[0], "SSRC[0]")
	assert.Equal(t, byte(0xBB), buf[1], "SSRC[1]")
	assert.Equal(t, byte(0xCC), buf[2], "SSRC[2]")
	assert.Equal(t, byte(0xDD), buf[3], "SSRC[3]")
	assert.Equal(t, byte(0xAB), buf[4], "fraction lost")
	assert.Equal(t, byte(0x12), buf[5], "total lost MSB")
	assert.Equal(t, byte(0x34), buf[6], "total lost mid")
	assert.Equal(t, byte(0x56), buf[7], "total lost LSB")
	assert.Equal(t, uint32(0xDEADBEEF), binary.BigEndian.Uint32(buf[8:12]))
	assert.Equal(t, uint32(0xCAFEBABE), binary.BigEndian.Uint32(buf[12:16]))
	assert.Equal(t, uint32(0x87654321), binary.BigEndian.Uint32(buf[16:20]))
	assert.Equal(t, uint32(0x01020304), binary.BigEndian.Uint32(buf[20:24]))
}

func TestRTCPMarshal_ReceptionReport_TotalLostBoundaries(t *testing.T) {
	for _, totalLost := range []uint32{0, 1, 0x800000, 0xFFFFFF} {
		rr := &ReceptionReport{TotalLost: totalLost}
		var buf [24]byte
		marshalReceptionReportTo(buf[:], rr)
		got := uint32(buf[5])<<16 | uint32(buf[6])<<8 | uint32(buf[7])
		assert.Equal(t, totalLost, got, "TotalLost=0x%X", totalLost)
	}
}

func TestRTCPMarshal_SDES_SingleChunk(t *testing.T) {
	sdes := &SourceDescription{
		Chunks: []SourceDescriptionChunk{
			{
				Source: 0x902f9e2e,
				Items: []SourceDescriptionItem{
					{Type: SDESCNAME, Text: "test@host"},
				},
			},
		},
	}
	data, err := sdes.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 20)

	assert.Equal(t, byte(0x81), data[0], "V=2 RC=1")
	assert.Equal(t, byte(0xCA), data[1], "PT=202 (SDES)")
	assert.Equal(t, []byte{0x00, 0x04}, data[2:4], "Length=4 (20/4-1)")

	assert.Equal(t, uint32(0x902f9e2e), binary.BigEndian.Uint32(data[4:8]))
	assert.Equal(t, byte(0x01), data[8], "SDES type=CNAME")
	assert.Equal(t, byte(0x09), data[9], "SDES text length=9")
	assert.Equal(t, []byte("test@host"), data[10:19], "SDES text")
	assert.Equal(t, byte(0x00), data[19], "padding/termination")

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	sdes2 := parsed[0].(*SourceDescription)
	require.Len(t, sdes2.Chunks, 1)
	assert.Equal(t, sdes.Chunks[0].Source, sdes2.Chunks[0].Source)
	require.Len(t, sdes2.Chunks[0].Items, 1)
	assert.Equal(t, sdes.Chunks[0].Items[0].Type, sdes2.Chunks[0].Items[0].Type)
	assert.Equal(t, sdes.Chunks[0].Items[0].Text, sdes2.Chunks[0].Items[0].Text)
}

func TestRTCPMarshal_SDES_MultipleChunks(t *testing.T) {
	sdes := &SourceDescription{
		Chunks: []SourceDescriptionChunk{
			{Source: 0x11111111, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "user1"}}},
			{Source: 0x22222222, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "user2"}}},
		},
	}
	data, err := sdes.Marshal()
	require.NoError(t, err)

	assert.Equal(t, byte(0x82), data[0], "RC=2")

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	sdes2 := parsed[0].(*SourceDescription)
	require.Len(t, sdes2.Chunks, 2)
	assert.Equal(t, uint32(0x11111111), sdes2.Chunks[0].Source)
	assert.Equal(t, "user1", sdes2.Chunks[0].Items[0].Text)
	assert.Equal(t, uint32(0x22222222), sdes2.Chunks[1].Source)
	assert.Equal(t, "user2", sdes2.Chunks[1].Items[0].Text)
}

func TestRTCPMarshal_SDES_MultipleItems(t *testing.T) {
	sdes := &SourceDescription{
		Chunks: []SourceDescriptionChunk{
			{
				Source: 0x12345678,
				Items: []SourceDescriptionItem{
					{Type: SDESCNAME, Text: "user"},
					{Type: SDESName, Text: "John"},
				},
			},
		},
	}
	data, err := sdes.Marshal()
	require.NoError(t, err)

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	sdes2 := parsed[0].(*SourceDescription)
	require.Len(t, sdes2.Chunks, 1)
	require.Len(t, sdes2.Chunks[0].Items, 2)
	assert.Equal(t, SDESCNAME, sdes2.Chunks[0].Items[0].Type)
	assert.Equal(t, "user", sdes2.Chunks[0].Items[0].Text)
	assert.Equal(t, SDESName, sdes2.Chunks[0].Items[1].Type)
	assert.Equal(t, "John", sdes2.Chunks[0].Items[1].Text)
}

func TestRTCPMarshal_SDES_4ByteAlignment(t *testing.T) {
	sdes := &SourceDescription{
		Chunks: []SourceDescriptionChunk{
			{Source: 0x11111111, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "a"}}},
			{Source: 0x22222222, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "b"}}},
		},
	}
	data, err := sdes.Marshal()
	require.NoError(t, err)

	assert.Equal(t, 0, len(data)%4, "SDES must be 4-byte aligned, got %d bytes", len(data))

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	require.Len(t, parsed[0].(*SourceDescription).Chunks, 2)
}

func TestRTCPMarshal_BYE_Minimal(t *testing.T) {
	bye := &Goodbye{
		Sources: []uint32{0xDEADBEEF},
	}
	data, err := bye.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 8)

	assert.Equal(t, byte(0x81), data[0], "V=2 RC=1")
	assert.Equal(t, byte(0xCB), data[1], "PT=203 (BYE)")
	assert.Equal(t, []byte{0x00, 0x01}, data[2:4], "Length=1 (8/4-1)")
	assert.Equal(t, uint32(0xDEADBEEF), binary.BigEndian.Uint32(data[4:8]))

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	bye2 := parsed[0].(*Goodbye)
	assert.Equal(t, bye.Sources, bye2.Sources)
	assert.Empty(t, bye2.Reason)
}

func TestRTCPMarshal_BYE_WithReason(t *testing.T) {
	bye := &Goodbye{
		Sources: []uint32{0xDEADBEEF},
		Reason:  "camera off",
	}
	data, err := bye.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 20)

	assert.Equal(t, byte(0x81), data[0], "RC=1")
	assert.Equal(t, byte(0xCB), data[1], "PT=203")
	assert.Equal(t, []byte{0x00, 0x04}, data[2:4], "Length=4 (20/4-1)")
	assert.Equal(t, uint32(0xDEADBEEF), binary.BigEndian.Uint32(data[4:8]))
	assert.Equal(t, byte(10), data[8], "reason length")
	assert.Equal(t, []byte("camera off"), data[9:19], "reason text")
	assert.Equal(t, byte(0x00), data[19], "padding")

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	bye2 := parsed[0].(*Goodbye)
	assert.Equal(t, bye.Sources, bye2.Sources)
	assert.Equal(t, bye.Reason, bye2.Reason)
}

func TestRTCPMarshal_BYE_MultipleSources(t *testing.T) {
	bye := &Goodbye{
		Sources: []uint32{0xAAAAAAAA, 0xBBBBBBBB, 0xCCCCCCCC},
	}
	data, err := bye.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 16)
	assert.Equal(t, byte(0x83), data[0], "RC=3")
	assert.Equal(t, []byte{0x00, 0x03}, data[2:4], "Length=3 (16/4-1)")

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	bye2 := parsed[0].(*Goodbye)
	assert.Equal(t, bye.Sources, bye2.Sources)
}

func TestRTCPMarshal_BYE_NoSources(t *testing.T) {
	bye := &Goodbye{}
	data, err := bye.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 4)
	assert.Equal(t, byte(0x80), data[0], "RC=0")
	assert.Equal(t, []byte{0x00, 0x00}, data[2:4], "Length=0")
}

func TestRTCPMarshal_APP_Minimal(t *testing.T) {
	app := &ApplicationDefined{
		SubType: 0,
		SSRC:    0x55555555,
		Name:    "TEST",
	}
	data, err := app.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 12)

	assert.Equal(t, byte(0x80), data[0], "subtype=0")
	assert.Equal(t, byte(0xCC), data[1], "PT=204 (APP)")
	assert.Equal(t, []byte{0x00, 0x02}, data[2:4], "Length=2 (12/4-1)")
	assert.Equal(t, uint32(0x55555555), binary.BigEndian.Uint32(data[4:8]))
	assert.Equal(t, []byte("TEST"), data[8:12], "name")

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	app2 := parsed[0].(*ApplicationDefined)
	assert.Equal(t, app.SubType, app2.SubType)
	assert.Equal(t, app.SSRC, app2.SSRC)
	assert.Equal(t, app.Name, app2.Name)
	assert.Empty(t, app2.Data)
}

func TestRTCPMarshal_APP_WithData(t *testing.T) {
	app := &ApplicationDefined{
		SubType: 3,
		SSRC:    0x55555555,
		Name:    "TEST",
		Data:    []byte{0x01, 0x02, 0x03, 0x04},
	}
	data, err := app.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 16)

	assert.Equal(t, byte(0x83), data[0], "subtype=3")
	assert.Equal(t, byte(0xCC), data[1], "PT=204")
	assert.Equal(t, []byte{0x00, 0x03}, data[2:4], "Length=3 (16/4-1)")
	assert.Equal(t, []byte("TEST"), data[8:12], "name")
	assert.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, data[12:16], "app data")

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	app2 := parsed[0].(*ApplicationDefined)
	assert.Equal(t, app.SubType, app2.SubType)
	assert.Equal(t, app.Data, app2.Data)
}

func TestRTCPMarshal_APP_NamePadding(t *testing.T) {
	app := &ApplicationDefined{
		SubType: 0,
		SSRC:    0,
		Name:    "AB",
	}
	data, err := app.Marshal()
	require.NoError(t, err)
	assert.Equal(t, []byte{'A', 'B', 0, 0}, data[8:12], "name padded to 4 bytes")

	app3 := &ApplicationDefined{
		SubType: 0,
		SSRC:    0,
		Name:    "ABCDEF",
	}
	data3, err := app3.Marshal()
	require.NoError(t, err)
	assert.Equal(t, []byte("ABCD"), data3[8:12], "name truncated to 4 bytes")
}

func TestRTCPMarshal_RawRTCP(t *testing.T) {
	raw := &RawRTCP{
		HeaderField: RTCPHeader{
			Padding: false,
			Count:   0,
			Type:    255,
			Length:  0,
		},
		Data: []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}
	data, err := raw.Marshal()
	require.NoError(t, err)
	assert.Equal(t, byte(0x80), data[0], "V=2 RC=0")
	assert.Equal(t, byte(255), data[1], "PT=255")
	assert.Len(t, data, 8)
	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, data[4:8])

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	_, ok := parsed[0].(*RawRTCP)
	require.True(t, ok)
}

func TestRTCPMarshal_MarshalSize_MatchesActual(t *testing.T) {
	packets := []RTCPPacket{
		&SenderReport{SSRC: 0, NTPTime: 0, RTPTime: 0, PacketCount: 0, OctetCount: 0},
		&SenderReport{SSRC: 0x11111111, NTPTime: 1, RTPTime: 2, PacketCount: 3, OctetCount: 4, Reports: []ReceptionReport{{SSRC: 0x22222222}}},
		&ReceiverReport{SSRC: 0x11111111},
		&ReceiverReport{SSRC: 0x11111111, Reports: []ReceptionReport{{SSRC: 0x22222222}, {SSRC: 0x33333333}}},
		&SourceDescription{Chunks: []SourceDescriptionChunk{{Source: 0, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "x"}}}}},
		&Goodbye{Sources: []uint32{0xAAAAAAAA}},
		&Goodbye{Sources: []uint32{0xAAAAAAAA, 0xBBBBBBBB}, Reason: "bye"},
		&ApplicationDefined{SubType: 0, SSRC: 0, Name: "TEST", Data: []byte{0x01}},
	}
	for i, pkt := range packets {
		data, err := pkt.(interface {
			Marshal() ([]byte, error)
			MarshalSize() int
		}).Marshal()
		require.NoError(t, err)
		size := pkt.(interface{ MarshalSize() int }).MarshalSize()
		assert.Equal(t, len(data), size, "packets[%d] size mismatch", i)
		assert.Equal(t, 0, len(data)%4, "packets[%d] must be 4-byte aligned, got %d bytes", i, len(data))
	}
}

func TestRTCPMarshalTo_BufferTooSmall(t *testing.T) {
	tests := []struct {
		name string
		pkt  interface {
			MarshalSize() int
			MarshalTo([]byte) (int, error)
		}
	}{
		{"SenderReport", &SenderReport{SSRC: 0}},
		{"ReceiverReport", &ReceiverReport{SSRC: 0}},
		{"SourceDescription", &SourceDescription{Chunks: []SourceDescriptionChunk{{Source: 0, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "x"}}}}}},
		{"Goodbye", &Goodbye{Sources: []uint32{0}}},
		{"ApplicationDefined", &ApplicationDefined{SSRC: 0, Name: "TEST"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.pkt.MarshalTo(nil)
			require.Error(t, err)
			_, err = tt.pkt.MarshalTo(make([]byte, tt.pkt.MarshalSize()-1))
			require.Error(t, err)
		})
	}
}

func TestRTCPMarshalTo_WritesCorrectBytes(t *testing.T) {
	sr := &SenderReport{SSRC: 0x11111111, NTPTime: 0x1234567890ABCDEF, RTPTime: 5000, PacketCount: 42, OctetCount: 64000}
	buf := make([]byte, sr.MarshalSize())
	n, err := sr.MarshalTo(buf)
	require.NoError(t, err)
	assert.Equal(t, len(buf), n)

	expected, err := sr.Marshal()
	require.NoError(t, err)
	assert.Equal(t, expected, buf)
}

func TestRTCPMarshal_RoundTripAllTypes(t *testing.T) {
	tests := []struct {
		name string
		pkt  RTCPPacket
	}{
		{"SR-minimal", &SenderReport{SSRC: 0xAAAAAAAA, NTPTime: 0x1234567890ABCDEF, RTPTime: 5000, PacketCount: 42, OctetCount: 64000}},
		{"SR-1report", &SenderReport{SSRC: 0xAAAAAAAA, NTPTime: 0x1234567890ABCDEF, RTPTime: 5000, PacketCount: 42, OctetCount: 64000, Reports: []ReceptionReport{{SSRC: 0xBBBBBBBB, FractionLost: 1, TotalLost: 2, LastSequenceNumber: 3, Jitter: 4, LastSenderReport: 5, Delay: 6}}}},
		{"SR-ext", &SenderReport{SSRC: 0x11111111, NTPTime: 0, RTPTime: 0, PacketCount: 0, OctetCount: 0, ProfileExtensions: []byte{0xDE, 0xAD}}},
		{"RR-minimal", &ReceiverReport{SSRC: 0xCCCCCCCC}},
		{"RR-2reports", &ReceiverReport{SSRC: 0xCCCCCCCC, Reports: []ReceptionReport{{SSRC: 0x11111111}, {SSRC: 0x22222222}}}},
		{"SDES-single", &SourceDescription{Chunks: []SourceDescriptionChunk{{Source: 0xAAAAAAAA, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "host.example.com"}}}}}},
		{"SDES-multi", &SourceDescription{Chunks: []SourceDescriptionChunk{{Source: 0x11111111, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "u1"}, {Type: SDESName, Text: "User One"}}}, {Source: 0x22222222, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "u2"}}}}}},
		{"BYE-single", &Goodbye{Sources: []uint32{0xDEADBEEF}}},
		{"BYE-reason", &Goodbye{Sources: []uint32{0xDEADBEEF}, Reason: "camera off"}},
		{"BYE-multi", &Goodbye{Sources: []uint32{0x11111111, 0x22222222, 0x33333333}}},
		{"APP-minimal", &ApplicationDefined{SubType: 0, SSRC: 0x55555555, Name: "TEST"}},
		{"APP-data", &ApplicationDefined{SubType: 3, SSRC: 0x55555555, Name: "TEST", Data: []byte{0x01, 0x02, 0x03, 0x04}}},
		{"APP-names", &ApplicationDefined{SubType: 0, SSRC: 0, Name: "AB"}},
		{"APP-truncated", &ApplicationDefined{SubType: 0, SSRC: 0, Name: "LONGNAME"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.pkt.(interface {
				Marshal() ([]byte, error)
			}).Marshal()
			require.NoError(t, err)

			parsed, err := ParseRTCP(data)
			require.NoError(t, err)
			require.Len(t, parsed, 1)

			switch want := tt.pkt.(type) {
			case *SenderReport:
				got := parsed[0].(*SenderReport)
				assert.Equal(t, want.SSRC, got.SSRC)
				assert.Equal(t, want.NTPTime, got.NTPTime)
				assert.Equal(t, want.RTPTime, got.RTPTime)
				assert.Equal(t, want.PacketCount, got.PacketCount)
				assert.Equal(t, want.OctetCount, got.OctetCount)
				assert.Equal(t, len(want.Reports), len(got.Reports))
				for i := range want.Reports {
					assert.Equal(t, want.Reports[i], got.Reports[i])
				}
				if len(want.ProfileExtensions) > 0 {
					assert.Equal(t, want.ProfileExtensions, got.ProfileExtensions[:len(want.ProfileExtensions)])
				}
			case *ReceiverReport:
				got := parsed[0].(*ReceiverReport)
				assert.Equal(t, want.SSRC, got.SSRC)
				assert.Equal(t, len(want.Reports), len(got.Reports))
				for i := range want.Reports {
					assert.Equal(t, want.Reports[i], got.Reports[i])
				}
			case *SourceDescription:
				got := parsed[0].(*SourceDescription)
				require.Equal(t, len(want.Chunks), len(got.Chunks))
				for i := range want.Chunks {
					assert.Equal(t, want.Chunks[i].Source, got.Chunks[i].Source)
					assert.Equal(t, want.Chunks[i].Items, got.Chunks[i].Items)
				}
			case *Goodbye:
				got := parsed[0].(*Goodbye)
				assert.Equal(t, want.Sources, got.Sources)
				assert.Equal(t, want.Reason, got.Reason)
			case *ApplicationDefined:
				got := parsed[0].(*ApplicationDefined)
				assert.Equal(t, want.SubType, got.SubType)
				assert.Equal(t, want.SSRC, got.SSRC)
				assert.Equal(t, len(want.Data), len(got.Data))
				for i := range want.Data {
					assert.Equal(t, want.Data[i], got.Data[i])
				}
			}
		})
	}
}

func TestRTCPMarshal_Idempotent(t *testing.T) {
	sr := &SenderReport{SSRC: 0x11111111, NTPTime: 0x1234567890ABCDEF, RTPTime: 5000, PacketCount: 42, OctetCount: 64000}
	data1, err := sr.Marshal()
	require.NoError(t, err)
	data2, err := sr.Marshal()
	require.NoError(t, err)
	data3, err := sr.Marshal()
	require.NoError(t, err)
	assert.Equal(t, data1, data2)
	assert.Equal(t, data2, data3)
}

func TestRTCPMarshal_PaddingFlag(t *testing.T) {
	sr := &SenderReport{
		SSRC:        0x11111111,
		NTPTime:     0,
		RTPTime:     0,
		PacketCount: 0,
		OctetCount:  0,
	}
	sr.hdr.Padding = true
	data, err := sr.Marshal()
	require.NoError(t, err)
	assert.Equal(t, byte(0xA0), data[0], "V=2 P=1 RC=0")
}

func TestRTCPMarshal_Compound(t *testing.T) {
	sr := &SenderReport{
		SSRC:        0xAAAAAAAA,
		NTPTime:     0x1234567890ABCDEF,
		RTPTime:     5000,
		PacketCount: 42,
		OctetCount:  64000,
	}
	sdes := &SourceDescription{
		Chunks: []SourceDescriptionChunk{
			{Source: 0xAAAAAAAA, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "test"}}},
		},
	}
	bye := &Goodbye{
		Sources: []uint32{0xAAAAAAAA},
	}
	packets := []RTCPPacket{sr, sdes, bye}
	data, err := MarshalRTCP(packets)
	require.NoError(t, err)

	assert.Equal(t, byte(0x80), data[0], "SR V=2 RC=0")
	assert.Equal(t, byte(0xC8), data[1], "SR PT=200")

	sdesOffset := 28
	assert.Equal(t, byte(0x81), data[sdesOffset+0], "SDES V=2 RC=1")
	assert.Equal(t, byte(0xCA), data[sdesOffset+1], "SDES PT=202")

	byeOffset := sdesOffset + 16
	assert.Equal(t, byte(0x81), data[byeOffset+0], "BYE V=2 RC=1")
	assert.Equal(t, byte(0xCB), data[byeOffset+1], "BYE PT=203")

	require.Len(t, data, byeOffset+8)

	parsed, err := ParseRTCP(data)
	require.NoError(t, err)
	require.Len(t, parsed, 3)

	_, ok := parsed[0].(*SenderReport)
	require.True(t, ok)
	_, ok = parsed[1].(*SourceDescription)
	require.True(t, ok)
	_, ok = parsed[2].(*Goodbye)
	require.True(t, ok)
}

func TestRTCPMarshal_ZeroLenCompound(t *testing.T) {
	_, err := MarshalRTCP(nil)
	require.Error(t, err)
	_, err = MarshalRTCP([]RTCPPacket{})
	require.Error(t, err)
}
