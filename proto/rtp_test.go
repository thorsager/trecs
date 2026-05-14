package proto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustBuildRTP(t *testing.T, marker, pad bool, pt uint8, seq uint16, ts uint32, ssrc uint32, csrc []uint32, payload []byte) []byte {
	t.Helper()
	cc := len(csrc)
	if cc > 15 {
		t.Fatal("too many CSRCs")
	}
	buf := make([]byte, 12+cc*4+len(payload))
	buf[0] = (2 << 6) | byte(cc)
	if pad {
		buf[0] |= 1 << 5
	}
	buf[1] = pt
	if marker {
		buf[1] |= 1 << 7
	}
	putU16(buf[2:4], seq)
	putU32(buf[4:8], ts)
	putU32(buf[8:12], ssrc)
	for i, c := range csrc {
		putU32(buf[12+i*4:], c)
	}
	copy(buf[12+cc*4:], payload)
	return buf
}

func putU16(b []byte, v uint16) {
	b[0] = byte(v >> 8)
	b[1] = byte(v)
}

func putU32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

func putU32BEAppend(buf []byte, v uint32) []byte {
	return append(buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func putU64BEAppend(buf []byte, v uint64) []byte {
	return append(buf,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v),
	)
}

func TestParseRTP_Minimal(t *testing.T) {
	data := mustBuildRTP(t, false, false, 0, 1, 100, 0xDEADBEEF, nil, nil)
	pkt, err := ParseRTP(data)
	require.NoError(t, err)
	require.NotNil(t, pkt)
	assert.Equal(t, uint8(2), pkt.Header.Version)
	assert.Equal(t, uint8(0), pkt.Header.PayloadType)
	assert.Equal(t, uint16(1), pkt.Header.SequenceNumber)
	assert.Equal(t, uint32(100), pkt.Header.Timestamp)
	assert.Equal(t, uint32(0xDEADBEEF), pkt.Header.SSRC)
	assert.Empty(t, pkt.Header.CSRC)
	assert.False(t, pkt.Header.Marker)
	assert.False(t, pkt.Header.Padding)
	assert.False(t, pkt.Header.Extension)
	assert.Empty(t, pkt.Payload)
}

func TestParseRTP_WithPayload(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03, 0x04}
	data := mustBuildRTP(t, false, false, 8, 42, 12345, 0x55555555, nil, payload)
	pkt, err := ParseRTP(data)
	require.NoError(t, err)
	assert.Equal(t, uint8(8), pkt.Header.PayloadType)
	assert.Equal(t, uint16(42), pkt.Header.SequenceNumber)
	assert.Equal(t, payload, pkt.Payload)
}

func TestParseRTP_MarkerBit(t *testing.T) {
	data := mustBuildRTP(t, true, false, 96, 1, 0, 0x12345678, nil, []byte{0xFF})
	pkt, err := ParseRTP(data)
	require.NoError(t, err)
	assert.True(t, pkt.Header.Marker)
	assert.Equal(t, uint8(96), pkt.Header.PayloadType)
}

func TestParseRTP_CSRC(t *testing.T) {
	csrc := []uint32{0x11111111, 0x22222222}
	data := mustBuildRTP(t, false, false, 0, 0, 0, 0xAAAAAAAA, csrc, []byte{0x00})
	pkt, err := ParseRTP(data)
	require.NoError(t, err)
	assert.Equal(t, csrc, pkt.Header.CSRC)
	assert.Equal(t, []byte{0x00}, pkt.Payload)
}

func TestParseRTP_Padding(t *testing.T) {
	payload := []byte{0x01, 0x02}
	padSize := byte(4)
	data := mustBuildRTP(t, false, true, 0, 0, 0, 0, nil, payload)
	data = append(data, make([]byte, padSize)...)
	data[len(data)-1] = padSize

	pkt, err := ParseRTP(data)
	require.NoError(t, err)
	assert.True(t, pkt.Header.Padding)
	assert.Equal(t, padSize, pkt.Header.PaddingSize)
	assert.Equal(t, payload, pkt.Payload)
}

func TestParseRTP_InvalidVersion(t *testing.T) {
	data := mustBuildRTP(t, false, false, 0, 0, 0, 0, nil, nil)
	data[0] = 0
	_, err := ParseRTP(data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestParseRTP_TooShort(t *testing.T) {
	_, err := ParseRTP([]byte{0x80, 0x00, 0x00})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestParseRTP_EmptyPadding(t *testing.T) {
	data := mustBuildRTP(t, false, true, 0, 0, 0, 0, nil, nil)
	data = append(data, 0x00)
	_, err := ParseRTP(data)
	assert.Error(t, err)
}

func TestParseRTP_OneByteExtensions(t *testing.T) {
	extDataSize := 8
	buf := make([]byte, 12+4+extDataSize)
	buf[0] = 0x90
	buf[1] = 10
	putU16(buf[2:4], 1)
	putU32(buf[4:8], 100)
	putU32(buf[8:12], 0xCAFEBABE)
	putU16(buf[12:14], ExtensionProfileOneByte)
	putU16(buf[14:16], uint16(extDataSize/4))
	buf[16] = 0x31 // ID=3, payload_len-1=1 -> 2 bytes
	buf[17] = 0xAA
	buf[18] = 0xBB
	buf[19] = 0x52 // ID=5, payload_len-1=2 -> 3 bytes
	buf[20] = 0xCC
	buf[21] = 0xDD
	buf[22] = 0xEE
	buf[23] = 0x00 // padding

	pkt, err := ParseRTP(buf)
	require.NoError(t, err)
	assert.True(t, pkt.Header.Extension)
	assert.Equal(t, uint16(ExtensionProfileOneByte), pkt.Header.ExtensionProfile)
	require.Len(t, pkt.Header.Extensions, 2)
	assert.Equal(t, uint8(3), pkt.Header.Extensions[0].ID)
	assert.Equal(t, []byte{0xAA, 0xBB}, pkt.Header.Extensions[0].Payload)
	assert.Equal(t, uint8(5), pkt.Header.Extensions[1].ID)
	assert.Equal(t, []byte{0xCC, 0xDD, 0xEE}, pkt.Header.Extensions[1].Payload)
}

func TestParseRTP_TwoByteExtensions(t *testing.T) {
	extDataSize := 12
	buf := make([]byte, 12+4+extDataSize)
	buf[0] = 0x90
	buf[1] = 10
	putU16(buf[2:4], 1)
	putU32(buf[4:8], 100)
	putU32(buf[8:12], 0xCAFEBABE)
	putU16(buf[12:14], ExtensionProfileTwoByte)
	putU16(buf[14:16], uint16(extDataSize/4))
	buf[16] = 0x05       // ID=5
	buf[17] = 0x03       // len=3
	buf[18] = 0xAA       // payload byte 1
	buf[19] = 0xBB       // payload byte 2
	buf[20] = 0xCC       // payload byte 3
	buf[21] = 0x01       // ID=1
	buf[22] = 0x02       // len=2
	buf[23] = 0xDD       // payload byte 1
	buf[24] = 0xEE       // payload byte 2
	buf[25] = 0x00       // padding
	buf[26] = 0x00
	buf[27] = 0x00

	pkt, err := ParseRTP(buf)
	require.NoError(t, err)
	require.Len(t, pkt.Header.Extensions, 2)
	assert.Equal(t, uint8(5), pkt.Header.Extensions[0].ID)
	assert.Equal(t, []byte{0xAA, 0xBB, 0xCC}, pkt.Header.Extensions[0].Payload)
	assert.Equal(t, uint8(1), pkt.Header.Extensions[1].ID)
	assert.Equal(t, []byte{0xDD, 0xEE}, pkt.Header.Extensions[1].Payload)
}

func TestParseRTP_RFC3550Extension(t *testing.T) {
	extPayload := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	buf := make([]byte, 12+4+len(extPayload))
	buf[0] = 0x90
	buf[1] = 10
	putU16(buf[2:4], 1)
	putU32(buf[4:8], 100)
	putU32(buf[8:12], 0xCAFEBABE)
	putU16(buf[12:14], 0x4321)
	putU16(buf[14:16], uint16(len(extPayload)/4))
	copy(buf[16:], extPayload)

	pkt, err := ParseRTP(buf)
	require.NoError(t, err)
	assert.True(t, pkt.Header.Extension)
	assert.Equal(t, uint16(0x4321), pkt.Header.ExtensionProfile)
	require.Len(t, pkt.Header.Extensions, 1)
	assert.Equal(t, uint8(0), pkt.Header.Extensions[0].ID)
	assert.Equal(t, extPayload, pkt.Header.Extensions[0].Payload)
}

func TestRTP_RoundTrip(t *testing.T) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			Marker:         true,
			Padding:        false,
			PayloadType:    0,
			SequenceNumber: 42,
			Timestamp:      12345,
			SSRC:           0xDEADBEEF,
			CSRC:           []uint32{0x11111111},
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}
	data, err := orig.Marshal()
	require.NoError(t, err)

	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	assert.Equal(t, orig.Header.Version, parsed.Header.Version)
	assert.Equal(t, orig.Header.Marker, parsed.Header.Marker)
	assert.Equal(t, orig.Header.PayloadType, parsed.Header.PayloadType)
	assert.Equal(t, orig.Header.SequenceNumber, parsed.Header.SequenceNumber)
	assert.Equal(t, orig.Header.Timestamp, parsed.Header.Timestamp)
	assert.Equal(t, orig.Header.SSRC, parsed.Header.SSRC)
	assert.Equal(t, orig.Header.CSRC, parsed.Header.CSRC)
	assert.Equal(t, orig.Payload, parsed.Payload)
}

func TestRTP_RoundTripWithExtensions(t *testing.T) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:    2,
			PayloadType: 96,
			SequenceNumber: 1,
			Timestamp:  100,
			SSRC:       0x12345678,
			Extension:  true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0xAA}},
				{ID: 2, Payload: []byte{0xBB, 0xCC}},
			},
		},
		Payload: []byte{0xFF},
	}
	data, err := orig.Marshal()
	require.NoError(t, err)
	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	assert.True(t, parsed.Header.Extension)
	assert.Equal(t, orig.Header.ExtensionProfile, parsed.Header.ExtensionProfile)
	require.Len(t, parsed.Header.Extensions, 2)
	assert.Equal(t, orig.Header.Extensions[0].ID, parsed.Header.Extensions[0].ID)
	assert.Equal(t, orig.Header.Extensions[0].Payload, parsed.Header.Extensions[0].Payload)
	assert.Equal(t, orig.Header.Extensions[1].ID, parsed.Header.Extensions[1].ID)
	assert.Equal(t, orig.Header.Extensions[1].Payload, parsed.Header.Extensions[1].Payload)
	assert.Equal(t, orig.Payload, parsed.Payload)
}

func TestRTP_RoundTripWithTwoByteExtensions(t *testing.T) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:    2,
			PayloadType: 96,
			SequenceNumber: 1,
			Timestamp:  100,
			SSRC:       0x12345678,
			Extension:  true,
			ExtensionProfile: ExtensionProfileTwoByte,
			Extensions: []RTPExtension{
				{ID: 5, Payload: []byte{0xDE, 0xAD}},
			},
		},
	}
	data, err := orig.Marshal()
	require.NoError(t, err)
	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	assert.Equal(t, ExtensionProfileTwoByte, parsed.Header.ExtensionProfile)
	require.Len(t, parsed.Header.Extensions, 1)
	assert.Equal(t, uint8(5), parsed.Header.Extensions[0].ID)
}

func TestRTP_PaddingRoundTrip(t *testing.T) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:     2,
			Padding:     true,
			PaddingSize: 4,
			PayloadType: 0,
			SequenceNumber: 1,
			Timestamp:   0,
			SSRC:        0x12345678,
		},
		Payload: []byte{0x01, 0x02},
	}
	data, err := orig.Marshal()
	require.NoError(t, err)
	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	assert.True(t, parsed.Header.Padding)
	assert.Equal(t, byte(4), parsed.Header.PaddingSize)
	assert.Equal(t, []byte{0x01, 0x02}, parsed.Payload)
}

func TestRTPMarshal_Minimal(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 1,
			Timestamp:      100,
			SSRC:           0xDEADBEEF,
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 12)

	assert.Equal(t, byte(0x80), data[0], "V=2 P=0 X=0 CC=0")
	assert.Equal(t, byte(0x00), data[1], "M=0 PT=0")
	assert.Equal(t, []byte{0x00, 0x01}, data[2:4], "seq=1")
	assert.Equal(t, []byte{0x00, 0x00, 0x00, 0x64}, data[4:8], "ts=100")
	assert.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, data[8:12], "ssrc=0xDEADBEEF")
}

func TestRTPMarshal_Marker(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			Marker:         true,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	assert.Equal(t, byte(0x80), data[0])
	assert.Equal(t, byte(0x80), data[1], "M=1 PT=0")
}

func TestRTPMarshal_PayloadType(t *testing.T) {
	for _, pt := range []uint8{0, 1, 96, 127} {
		pkt := &RTPPacket{
			Header: RTPHeader{
				Version:        2,
				PayloadType:    pt,
				SequenceNumber: 0,
				Timestamp:      0,
				SSRC:           0,
			},
		}
		data, err := pkt.Marshal()
		require.NoError(t, err)
		assert.Equal(t, pt, data[1], "PT=%d", pt)
	}
}

func TestRTPMarshal_Padding(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:     2,
			Padding:     true,
			PaddingSize: 4,
			PayloadType: 0,
			SequenceNumber: 1,
			Timestamp:   0,
			SSRC:        0x12345678,
		},
		Payload: []byte{0x01, 0x02},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 18)

	assert.Equal(t, byte(0xA0), data[0], "V=2 P=1 X=0 CC=0")
	assert.Equal(t, byte(4), data[len(data)-1], "last byte = padding size")
	assert.Equal(t, []byte{0x01, 0x02}, data[12:14], "payload")
	assert.Equal(t, []byte{0x00, 0x00, 0x00}, data[14:17], "padding zeros")
	assert.Equal(t, byte(4), data[17], "pad count")
}

func TestRTPMarshal_CSRC(t *testing.T) {
	csrc := []uint32{0x11111111, 0x22222222}
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 42,
			Timestamp:      5000,
			SSRC:           0xAAAAAAAA,
			CSRC:           csrc,
		},
		Payload: []byte{0xFF},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 21)

	assert.Equal(t, byte(0x82), data[0], "V=2 P=0 X=0 CC=2")
	for i, c := range csrc {
		off := 12 + i*4
		assert.Equal(t, byte(c>>24), data[off+0], "CSRC[%d] byte 0", i)
		assert.Equal(t, byte(c>>16), data[off+1], "CSRC[%d] byte 1", i)
		assert.Equal(t, byte(c>>8),  data[off+2], "CSRC[%d] byte 2", i)
		assert.Equal(t, byte(c),     data[off+3], "CSRC[%d] byte 3", i)
	}
	assert.Equal(t, []byte{0xFF}, data[20:], "payload")
}

func TestRTPMarshal_CSRCMaxCount(t *testing.T) {
	csrc := make([]uint32, 15)
	for i := range csrc {
		csrc[i] = uint32(i + 1)
	}
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
			CSRC:           csrc,
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 12+15*4)

	assert.Equal(t, byte(0x8F), data[0], "V=2 P=0 X=0 CC=15")
}

func TestRTPMarshal_ExceedsCSRCLimit(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
			CSRC:           make([]uint32, 16),
		},
	}
	_, err := pkt.Marshal()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CSRC")
}

func TestRTPMarshal_ExtensionOneByte(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      96,
			SequenceNumber:   1,
			Timestamp:        100,
			SSRC:             0x12345678,
			Extension:        true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0xAA}},
				{ID: 2, Payload: []byte{0xBB, 0xCC}},
			},
		},
		Payload: []byte{0xFF},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)

	assert.Equal(t, byte(0x90), data[0], "V=2 P=0 X=1 CC=0")

	extOff := 12
	assert.Equal(t, byte(0xBE), data[extOff+0], "ext profile MSB")
	assert.Equal(t, byte(0xDE), data[extOff+1], "ext profile LSB")
	assert.Equal(t, byte(0x00), data[extOff+2], "ext length MSB = 2 (words-1)")
	assert.Equal(t, byte(0x02), data[extOff+3], "ext length LSB = 2")

	assert.Equal(t, byte(0x10), data[extOff+4], "ext[0]: ID=1 len-1=0 -> 0x10")
	assert.Equal(t, byte(0xAA), data[extOff+5], "ext[0] payload")
	assert.Equal(t, byte(0x21), data[extOff+6], "ext[1]: ID=2 len-1=1 -> 0x21")
	assert.Equal(t, byte(0xBB), data[extOff+7], "ext[1] payload[0]")
	assert.Equal(t, byte(0xCC), data[extOff+8], "ext[1] payload[1]")

	paddedEnd := extOff + 4 + 2*4
	require.Len(t, data, paddedEnd+1)
	for i := extOff + 9; i < paddedEnd; i++ {
		assert.Equal(t, byte(0), data[i], "padding at offset %d", i)
	}

	assert.Equal(t, byte(0xFF), data[paddedEnd], "payload after extension")
}

func TestRTPMarshal_ExtensionTwoByte(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      96,
			SequenceNumber:   1,
			Timestamp:        100,
			SSRC:             0x12345678,
			Extension:        true,
			ExtensionProfile: ExtensionProfileTwoByte,
			Extensions: []RTPExtension{
				{ID: 5, Payload: []byte{0xDE, 0xAD}},
			},
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)

	extOff := 12
	assert.Equal(t, byte(0x10), data[extOff+0], "ext profile MSB")
	assert.Equal(t, byte(0x00), data[extOff+1], "ext profile LSB")
	assert.Equal(t, byte(0x00), data[extOff+2], "ext length MSB = 1 (words-1)")
	assert.Equal(t, byte(0x01), data[extOff+3], "ext length LSB = 1")

	assert.Equal(t, byte(0x05), data[extOff+4], "ext ID=5")
	assert.Equal(t, byte(0x02), data[extOff+5], "ext len=2")
	assert.Equal(t, byte(0xDE), data[extOff+6], "ext payload[0]")
	assert.Equal(t, byte(0xAD), data[extOff+7], "ext payload[1]")

	require.Len(t, data, extOff+4+4)
}

func TestRTPMarshal_ExtensionDefinedByProfile(t *testing.T) {
	extPayload := []byte{0x11, 0x22, 0x33, 0x44}
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      10,
			SequenceNumber:   1,
			Timestamp:        100,
			SSRC:             0xCAFEBABE,
			Extension:        true,
			ExtensionProfile: 0x4321,
			Extensions: []RTPExtension{
				{ID: 0, Payload: extPayload},
			},
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)

	extOff := 12
	assert.Equal(t, byte(0x43), data[extOff+0], "ext profile MSB")
	assert.Equal(t, byte(0x21), data[extOff+1], "ext profile LSB")
	assert.Equal(t, byte(0x00), data[extOff+2], "ext length MSB = 1 (words-1)")
	assert.Equal(t, byte(0x01), data[extOff+3], "ext length LSB = 1")
	assert.Equal(t, extPayload, data[extOff+4:extOff+8], "raw extension data")

	require.Len(t, data, extOff+4+4)
}

func TestRTPMarshal_ExtensionZeroLength(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      0,
			SequenceNumber:   0,
			Timestamp:        0,
			SSRC:             0,
			Extension:        true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions:       nil,
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 16)

	extOff := 12
	assert.Equal(t, byte(0xBE), data[extOff+0], "profile MSB")
	assert.Equal(t, byte(0xDE), data[extOff+1], "profile LSB")
	assert.Equal(t, byte(0x00), data[extOff+2], "ext length MSB")
	assert.Equal(t, byte(0x00), data[extOff+3], "ext length LSB = 0")
}

func TestRTPMarshal_OneByteExt_SingleBytePadding(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      0,
			SequenceNumber:   0,
			Timestamp:        0,
			SSRC:             0,
			Extension:        true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0xAA}},
			},
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)

	extOff := 12
	extHdrSize := 4
	extDataSize := 2 // ID=1, len-1=0 (1 byte payload), + 1 data byte = 2
	roundedExtSize := ((extDataSize + 3) / 4) * 4
	expectedTotalExt := extHdrSize + roundedExtSize

	assert.Equal(t, byte(0x00), data[extOff+2], "ext length MSB")
	assert.Equal(t, byte(roundedExtSize/4), data[extOff+3], "ext length LSB = words-1")
	assert.Equal(t, expectedTotalExt, len(data)-12, "total extension + header size")

	payloadOff := extOff + expectedTotalExt
	assert.Equal(t, byte(0x10), data[extOff+4], "ext[0]: ID=1 len-1=0")
	assert.Equal(t, byte(0xAA), data[extOff+5], "ext payload byte")
	assert.Equal(t, byte(0x00), data[extOff+6], "padding byte")
	assert.Equal(t, byte(0x00), data[extOff+7], "padding byte")
	assert.Len(t, data, payloadOff, "no payload after extension")

	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	assert.True(t, parsed.Header.Extension)
	require.Len(t, parsed.Header.Extensions, 1)
	assert.Equal(t, uint8(1), parsed.Header.Extensions[0].ID)
	assert.Equal(t, []byte{0xAA}, parsed.Header.Extensions[0].Payload)
}

func TestRTPMarshal_ExtensionInterleavedPadding(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      0,
			SequenceNumber:   0,
			Timestamp:        0,
			SSRC:             0,
			Extension:        true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0xAA}},
				{ID: 2, Payload: []byte{0xBB}},
			},
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)

	extOff := 12
	assert.Equal(t, byte(0x10), data[extOff+4], "ext[0]: ID=1 len-1=0")
	assert.Equal(t, byte(0xAA), data[extOff+5], "ext[0] payload")
	assert.Equal(t, byte(0x20), data[extOff+6], "ext[1]: ID=2 len-1=0")
	assert.Equal(t, byte(0xBB), data[extOff+7], "ext[1] payload")

	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	require.Len(t, parsed.Header.Extensions, 2)
}

func TestRTPMarshal_AllFeaturesCombined(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			Padding:          true,
			PaddingSize:      4,
			Marker:           true,
			PayloadType:      96,
			SequenceNumber:   65535,
			Timestamp:        0xFFFFFFFF,
			SSRC:             0xCAFEBABE,
			CSRC:             []uint32{0x11111111, 0x22222222, 0x33333333},
			Extension:        true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0xAA}},
				{ID: 2, Payload: []byte{0xBB, 0xCC}},
			},
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)

	assert.Equal(t, byte(0xB3), data[0], "V=2 P=1 X=1 CC=3")
	assert.Equal(t, byte(0xE0), data[1], "M=1 PT=96")

	csrcOff := 12
	for i, c := range []uint32{0x11111111, 0x22222222, 0x33333333} {
		assert.Equal(t, byte(c>>24), data[csrcOff+0], "CSRC[%d] byte 0", i)
		assert.Equal(t, byte(c>>16), data[csrcOff+1], "CSRC[%d] byte 1", i)
		assert.Equal(t, byte(c>>8),  data[csrcOff+2], "CSRC[%d] byte 2", i)
		assert.Equal(t, byte(c),     data[csrcOff+3], "CSRC[%d] byte 3", i)
		csrcOff += 4
	}

	extOff := 12 + 3*4
	assert.Equal(t, byte(0xBE), data[extOff+0], "ext profile MSB")
	assert.Equal(t, byte(0xDE), data[extOff+1], "ext profile LSB")
	assert.Equal(t, byte(0x00), data[extOff+2], "ext length MSB")
	assert.Equal(t, byte(0x02), data[extOff+3], "ext length LSB = 2 (8 bytes)")

	payloadOff := extOff + 4 + 2*4

	assert.Equal(t, []byte{0x01, 0x02, 0x03}, data[payloadOff:payloadOff+3], "payload")

	assert.Equal(t, byte(4), data[len(data)-1], "padding size in last byte")
	assert.Len(t, data, payloadOff+3+4, "total length")

	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	assert.Equal(t, pkt.Header.Version, parsed.Header.Version)
	assert.Equal(t, pkt.Header.Padding, parsed.Header.Padding)
	assert.Equal(t, pkt.Header.PaddingSize, parsed.Header.PaddingSize)
	assert.Equal(t, pkt.Header.Marker, parsed.Header.Marker)
	assert.Equal(t, pkt.Header.PayloadType, parsed.Header.PayloadType)
	assert.Equal(t, pkt.Header.SequenceNumber, parsed.Header.SequenceNumber)
	assert.Equal(t, pkt.Header.Timestamp, parsed.Header.Timestamp)
	assert.Equal(t, pkt.Header.SSRC, parsed.Header.SSRC)
	assert.Equal(t, pkt.Header.CSRC, parsed.Header.CSRC)
	assert.True(t, parsed.Header.Extension)
	assert.Equal(t, pkt.Header.ExtensionProfile, parsed.Header.ExtensionProfile)
	require.Len(t, parsed.Header.Extensions, 2)
	assert.Equal(t, pkt.Header.Extensions[0].ID, parsed.Header.Extensions[0].ID)
	assert.Equal(t, pkt.Header.Extensions[0].Payload, parsed.Header.Extensions[0].Payload)
	assert.Equal(t, pkt.Header.Extensions[1].ID, parsed.Header.Extensions[1].ID)
	assert.Equal(t, pkt.Header.Extensions[1].Payload, parsed.Header.Extensions[1].Payload)
	assert.Equal(t, pkt.Payload, parsed.Payload)
}

func TestRTPMarshalSize_Minimal(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
		},
	}
	assert.Equal(t, 12, pkt.MarshalSize())
}

func TestRTPMarshalSize_WithPayload(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
		},
		Payload: make([]byte, 160),
	}
	assert.Equal(t, 12+160, pkt.MarshalSize())
}

func TestRTPMarshalSize_WithPadding(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:     2,
			Padding:     true,
			PaddingSize: 8,
			PayloadType: 0,
			SequenceNumber: 0,
			Timestamp:   0,
			SSRC:        0,
		},
		Payload: []byte{0x01},
	}
	assert.Equal(t, 12+1+8, pkt.MarshalSize())
}

func TestRTPMarshalSize_WithCSRC(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
			CSRC:           make([]uint32, 4),
		},
	}
	assert.Equal(t, 12+4*4, pkt.MarshalSize())
}

func TestRTPMarshalSize_WithExtensions(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      0,
			SequenceNumber:   0,
			Timestamp:        0,
			SSRC:             0,
			Extension:        true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0xAA, 0xBB}},
			},
		},
	}
	sz := pkt.MarshalSize()
	data, err := pkt.Marshal()
	require.NoError(t, err)
	assert.Equal(t, len(data), sz, "MarshalSize must match actual marshaled length")
}

func TestRTPMarshalSize_MatchesActual(t *testing.T) {
	pkts := []*RTPPacket{
		{Header: RTPHeader{Version: 2, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0}},
		{Header: RTPHeader{Version: 2, PayloadType: 96, SequenceNumber: 42, Timestamp: 100, SSRC: 0xDEADBEEF}, Payload: []byte{0x01}},
		{Header: RTPHeader{Version: 2, Padding: true, PaddingSize: 4, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0}, Payload: []byte{0x01}},
		{Header: RTPHeader{Version: 2, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0, CSRC: []uint32{1, 2, 3}}},
		{Header: RTPHeader{Version: 2, Extension: true, ExtensionProfile: ExtensionProfileOneByte, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0, Extensions: []RTPExtension{{ID: 1, Payload: []byte{0xAA}}}}},
		{Header: RTPHeader{Version: 2, Extension: true, ExtensionProfile: ExtensionProfileTwoByte, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0, Extensions: []RTPExtension{{ID: 5, Payload: []byte{0xBB, 0xCC}}}}},
	}
	for i, pkt := range pkts {
		data, err := pkt.Marshal()
		require.NoError(t, err)
		assert.Equal(t, len(data), pkt.MarshalSize(), "pkts[%d] MarshalSize mismatch", i)
	}
}

func TestRTPMarshalTo_BufferTooSmall(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
		},
	}
	_, err := pkt.MarshalTo([]byte{0, 0, 0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buffer too small")
}

func TestRTPMarshalTo_WritesCorrectBytes(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			Marker:         true,
			PayloadType:    8,
			SequenceNumber: 42,
			Timestamp:      12345,
			SSRC:           0xDEADBEEF,
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}
	buf := make([]byte, pkt.MarshalSize())
	n, err := pkt.MarshalTo(buf)
	require.NoError(t, err)
	assert.Equal(t, len(buf), n)

	expected, err := pkt.Marshal()
	require.NoError(t, err)
	assert.Equal(t, expected, buf[:n])
}

func TestRTPMarshalTo_MultipleCalls(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 1,
			Timestamp:      100,
			SSRC:           0xDEADBEEF,
		},
		Payload: []byte{0x01},
	}
	buf := make([]byte, pkt.MarshalSize())

	n1, err := pkt.MarshalTo(buf)
	require.NoError(t, err)
	result1 := make([]byte, n1)
	copy(result1, buf[:n1])

	n2, err := pkt.MarshalTo(buf)
	require.NoError(t, err)
	assert.Equal(t, n1, n2)
	assert.Equal(t, result1, buf[:n2])
}

func TestRTPMarshal_RoundTripAllVariants(t *testing.T) {
	variants := []*RTPPacket{
		{Header: RTPHeader{Version: 2, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0}},
		{Header: RTPHeader{Version: 2, Marker: true, PayloadType: 127, SequenceNumber: 65535, Timestamp: 0xFFFFFFFF, SSRC: 0xFFFFFFFF}, Payload: []byte{0x01, 0x02}},
		{Header: RTPHeader{Version: 2, Padding: true, PaddingSize: 8, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0}, Payload: []byte{0xFF}},
		{Header: RTPHeader{Version: 2, PayloadType: 96, SequenceNumber: 1, Timestamp: 100, SSRC: 0x12345678, CSRC: []uint32{0x11111111, 0x22222222}}},
		{Header: RTPHeader{Version: 2, Extension: true, ExtensionProfile: ExtensionProfileOneByte, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0, Extensions: []RTPExtension{{ID: 1, Payload: []byte{0xAA}}}}},
		{Header: RTPHeader{Version: 2, Extension: true, ExtensionProfile: ExtensionProfileTwoByte, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0, Extensions: []RTPExtension{{ID: 5, Payload: []byte{0xBB, 0xCC}}}}},
		{Header: RTPHeader{Version: 2, Extension: true, ExtensionProfile: 0x4321, PayloadType: 0, SequenceNumber: 0, Timestamp: 0, SSRC: 0, Extensions: []RTPExtension{{ID: 0, Payload: []byte{0x11, 0x22, 0x33, 0x44}}}}},
	}
	for i, pkt := range variants {
		data, err := pkt.Marshal()
		require.NoError(t, err, "variants[%d] Marshal", i)

		parsed, err := ParseRTP(data)
		require.NoError(t, err, "variants[%d] ParseRTP", i)

		assert.Equal(t, pkt.Header.Version, parsed.Header.Version, "variants[%d] Version", i)
		assert.Equal(t, pkt.Header.Padding, parsed.Header.Padding, "variants[%d] Padding", i)
		assert.Equal(t, pkt.Header.PaddingSize, parsed.Header.PaddingSize, "variants[%d] PaddingSize", i)
		assert.Equal(t, pkt.Header.Marker, parsed.Header.Marker, "variants[%d] Marker", i)
		assert.Equal(t, pkt.Header.PayloadType, parsed.Header.PayloadType, "variants[%d] PayloadType", i)
		assert.Equal(t, pkt.Header.SequenceNumber, parsed.Header.SequenceNumber, "variants[%d] SequenceNumber", i)
		assert.Equal(t, pkt.Header.Timestamp, parsed.Header.Timestamp, "variants[%d] Timestamp", i)
		assert.Equal(t, pkt.Header.SSRC, parsed.Header.SSRC, "variants[%d] SSRC", i)
		assert.Equal(t, pkt.Header.CSRC, parsed.Header.CSRC, "variants[%d] CSRC", i)
		assert.Equal(t, pkt.Header.Extension, parsed.Header.Extension, "variants[%d] Extension", i)
		if pkt.Header.Extension {
			assert.Equal(t, pkt.Header.ExtensionProfile, parsed.Header.ExtensionProfile, "variants[%d] ExtensionProfile", i)
			require.Len(t, parsed.Header.Extensions, len(pkt.Header.Extensions), "variants[%d] Extensions count", i)
			for j := range pkt.Header.Extensions {
				assert.Equal(t, pkt.Header.Extensions[j].ID, parsed.Header.Extensions[j].ID, "variants[%d] ext[%d] ID", i, j)
				assert.Equal(t, pkt.Header.Extensions[j].Payload, parsed.Header.Extensions[j].Payload, "variants[%d] ext[%d] Payload", i, j)
			}
		}
		assert.Equal(t, len(pkt.Payload), len(parsed.Payload), "variants[%d] Payload len", i)
		for j := range pkt.Payload {
			assert.Equal(t, pkt.Payload[j], parsed.Payload[j], "variants[%d] Payload[%d]", i, j)
		}
	}
}

func TestRTPMarshal_ZeroPayload(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	require.Len(t, data, 12)

	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	assert.Empty(t, parsed.Payload)
}

func TestRTPMarshal_LargeSequenceNumber(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 65535,
			Timestamp:      0,
			SSRC:           0,
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	assert.Equal(t, byte(0xFF), data[2], "seq MSB")
	assert.Equal(t, byte(0xFF), data[3], "seq LSB")
}

func TestRTPMarshal_LargeTimestamp(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0xFEEDFACE,
			SSRC:           0,
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	assert.Equal(t, byte(0xFE), data[4])
	assert.Equal(t, byte(0xED), data[5])
	assert.Equal(t, byte(0xFA), data[6])
	assert.Equal(t, byte(0xCE), data[7])
}

func TestRTPMarshal_ZeroSSRC(t *testing.T) {
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 0,
			Timestamp:      0,
			SSRC:           0,
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)
	assert.Equal(t, []byte{0x00, 0x00, 0x00, 0x00}, data[8:12], "zero SSRC")
}

func TestRTPMarshal_OneByteExtMaxPayload(t *testing.T) {
	payload := make([]byte, 16)
	for i := range payload {
		payload[i] = byte(i)
	}
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      0,
			SequenceNumber:   0,
			Timestamp:        0,
			SSRC:             0,
			Extension:        true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 15, Payload: payload},
			},
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)

	extOff := 12
	assert.Equal(t, byte(0xFF), data[extOff+4], "ID=15 len-1=15")
	assert.Equal(t, payload, data[extOff+5:extOff+21], "16-byte extension payload")
}

func TestRTPMarshal_TwoByteExtMaxPayload(t *testing.T) {
	payload := make([]byte, 255)
	for i := range payload {
		payload[i] = byte(i)
	}
	pkt := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      0,
			SequenceNumber:   0,
			Timestamp:        0,
			SSRC:             0,
			Extension:        true,
			ExtensionProfile: ExtensionProfileTwoByte,
			Extensions: []RTPExtension{
				{ID: 255, Payload: payload},
			},
		},
	}
	data, err := pkt.Marshal()
	require.NoError(t, err)

	extOff := 12
	assert.Equal(t, byte(0xFF), data[extOff+4], "ID=255")
	assert.Equal(t, byte(0xFF), data[extOff+5], "len=255")
	assert.Equal(t, payload, data[extOff+6:extOff+6+255], "255-byte extension payload")
}

func TestRTPMarshal_OneByteExtRoundTrip(t *testing.T) {
	for _, payloadLen := range []int{1, 2, 3, 4, 8, 16} {
		payload := make([]byte, payloadLen)
		for i := range payload {
			payload[i] = byte(i)
		}
		orig := &RTPPacket{
			Header: RTPHeader{
				Version:          2,
				PayloadType:      96,
				SequenceNumber:   1,
				Timestamp:        100,
				SSRC:             0x12345678,
				Extension:        true,
				ExtensionProfile: ExtensionProfileOneByte,
				Extensions: []RTPExtension{
					{ID: 1, Payload: payload},
				},
			},
		}
		data, err := orig.Marshal()
		require.NoError(t, err)
		parsed, err := ParseRTP(data)
		require.NoError(t, err)
		require.Len(t, parsed.Header.Extensions, 1)
		assert.Equal(t, payload, parsed.Header.Extensions[0].Payload, "payloadLen=%d", payloadLen)
	}
}

func TestRTPMarshal_MultipleExtensionsRoundTrip(t *testing.T) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      96,
			SequenceNumber:   1,
			Timestamp:        100,
			SSRC:             0x12345678,
			Extension:        true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0x01}},
				{ID: 2, Payload: []byte{0x02, 0x03}},
				{ID: 3, Payload: []byte{0x04, 0x05, 0x06}},
				{ID: 4, Payload: []byte{0x07, 0x08, 0x09, 0x0A}},
			},
		},
	}
	data, err := orig.Marshal()
	require.NoError(t, err)
	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	require.Len(t, parsed.Header.Extensions, 4)
	for i := range orig.Header.Extensions {
		assert.Equal(t, orig.Header.Extensions[i].ID, parsed.Header.Extensions[i].ID)
		assert.Equal(t, orig.Header.Extensions[i].Payload, parsed.Header.Extensions[i].Payload)
	}
}

func TestRTPMarshal_TwoByteExtMultiple(t *testing.T) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:          2,
			PayloadType:      96,
			SequenceNumber:   1,
			Timestamp:        100,
			SSRC:             0x12345678,
			Extension:        true,
			ExtensionProfile: ExtensionProfileTwoByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0xAA}},
				{ID: 2, Payload: []byte{0xBB, 0xCC}},
			},
		},
	}
	data, err := orig.Marshal()
	require.NoError(t, err)

	extOff := 12
	assert.Equal(t, byte(0x10), data[extOff+0], "profile MSB")
	assert.Equal(t, byte(0x00), data[extOff+1], "profile LSB")
	extData := data[extOff+4 : extOff+4+4] // 2 elements (3+4=7, rounded to 8)
	_ = extData

	parsed, err := ParseRTP(data)
	require.NoError(t, err)
	require.Len(t, parsed.Header.Extensions, 2)
	assert.Equal(t, uint8(1), parsed.Header.Extensions[0].ID)
	assert.Equal(t, []byte{0xAA}, parsed.Header.Extensions[0].Payload)
	assert.Equal(t, uint8(2), parsed.Header.Extensions[1].ID)
	assert.Equal(t, []byte{0xBB, 0xCC}, parsed.Header.Extensions[1].Payload)
}

func TestRTPMarshal_Idempotent(t *testing.T) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			Marker:         true,
			Padding:        true,
			PaddingSize:    4,
			PayloadType:    96,
			SequenceNumber: 42,
			Timestamp:      12345,
			SSRC:           0xDEADBEEF,
			CSRC:           []uint32{0x11111111},
			Extension:      true,
			ExtensionProfile: ExtensionProfileOneByte,
			Extensions: []RTPExtension{
				{ID: 1, Payload: []byte{0xAA}},
			},
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}
	data1, err := orig.Marshal()
	require.NoError(t, err)
	data2, err := orig.Marshal()
	require.NoError(t, err)
	data3, err := orig.Marshal()
	require.NoError(t, err)

	assert.Equal(t, data1, data2, "first and second Marshal must match")
	assert.Equal(t, data2, data3, "second and third Marshal must match")
}
