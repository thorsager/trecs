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
