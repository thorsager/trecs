package proto

import (
	"encoding/binary"
	"fmt"
)

const (
	rtpVersion        = 2
	rtpFixedHeaderLen = 12

	// ExtensionProfileOneByte is the RTP One Byte Header Extension Profile, defined in RFC 8285.
	ExtensionProfileOneByte uint16 = 0xBEDE
	// ExtensionProfileTwoByte is the RTP Two Byte Header Extension Profile, defined in RFC 8285.
	ExtensionProfileTwoByte uint16 = 0x1000

	// Payload type constants for common audio codecs (RFC 3551).
	PCMU             = 0
	PCMA             = 8
	TelephoneEvent   = 101
)

// RTPExtension represents a single RTP header extension element.
type RTPExtension struct {
	ID      uint8
	Payload []byte
}

// RTPHeader represents the fixed and variable parts of an RTP packet header.
type RTPHeader struct {
	Version          uint8
	Padding          bool
	Marker           bool
	PayloadType      uint8
	SequenceNumber   uint16
	Timestamp        uint32
	SSRC             uint32
	CSRC             []uint32
	Extension        bool
	ExtensionProfile uint16
	Extensions       []RTPExtension
	PaddingSize      byte
}

// RTPPacket represents a parsed RTP data packet.
type RTPPacket struct {
	Header  RTPHeader
	Payload []byte
}

func (p RTPPacket) String() string {
	return fmt.Sprintf("RTP v=%d pt=%d seq=%d ts=%d ssrc=%x marker=%t pad=%t payload=%d",
		p.Header.Version, p.Header.PayloadType, p.Header.SequenceNumber,
		p.Header.Timestamp, p.Header.SSRC, p.Header.Marker, p.Header.Padding, len(p.Payload))
}

// UnmarshalRTP parses a single RTP packet from a byte slice.
func UnmarshalRTP(data []byte) (RTPPacket, error) {
	if len(data) < rtpFixedHeaderLen {
		return RTPPacket{}, UnmarshalErrorf("rtp: packet too short: %d < %d", len(data), rtpFixedHeaderLen)
	}

	var h RTPHeader
	n := rtpFixedHeaderLen

	h.Version = data[0] >> 6 & 0x03
	if h.Version != rtpVersion {
		return RTPPacket{}, UnmarshalErrorf("rtp: unsupported version %d", h.Version)
	}

	h.Padding = (data[0]>>5)&0x01 != 0
	hasExtension := (data[0]>>4)&0x01 != 0
	cc := int(data[0] & 0x0F)

	h.Marker = (data[1]>>7)&0x01 != 0
	h.PayloadType = data[1] & 0x7F
	h.SequenceNumber = binary.BigEndian.Uint16(data[2:4])
	h.Timestamp = binary.BigEndian.Uint32(data[4:8])
	h.SSRC = binary.BigEndian.Uint32(data[8:12])

	if cc > 0 {
		h.CSRC = make([]uint32, cc)
		for i := 0; i < cc; i++ {
			off := 12 + i*4
			h.CSRC[i] = binary.BigEndian.Uint32(data[off:])
			n += 4
		}
	}

	if hasExtension {
		if len(data) < n+4 {
			return RTPPacket{}, UnmarshalErrorf("rtp: header too short for extension header")
		}
		h.Extension = true
		h.ExtensionProfile = binary.BigEndian.Uint16(data[n:])
		extLen := int(binary.BigEndian.Uint16(data[n+2:])) * 4
		n += 4
		if len(data) < n+extLen {
			return RTPPacket{}, UnmarshalErrorf("rtp: header too short for extension data")
		}
		var extErr error
		h.Extensions, extErr = unmarshalExtensions(h.ExtensionProfile, data[n:n+extLen])
		if extErr != nil {
			return RTPPacket{}, extErr
		}
		n += extLen
	}

	end := len(data)
	if h.Padding {
		if end <= n {
			return RTPPacket{}, UnmarshalErrorf("rtp: padding flag set but no room for pad byte")
		}
		h.PaddingSize = data[end-1]
		if h.PaddingSize == 0 {
			return RTPPacket{}, UnmarshalErrorf("rtp: invalid padding size 0")
		}
		if int(h.PaddingSize) > end-n {
			return RTPPacket{}, UnmarshalErrorf("rtp: padding size %d exceeds payload length %d", h.PaddingSize, end-n)
		}
		end -= int(h.PaddingSize)
	}

	return RTPPacket{
		Header:  h,
		Payload: data[n:end],
	}, nil
}

// unmarshalExtensions parses RTP header extension data based on the profile.
func unmarshalExtensions(profile uint16, data []byte) ([]RTPExtension, error) {
	switch profile {
	case ExtensionProfileOneByte:
		return unmarshalOneByteExtensions(data)
	case ExtensionProfileTwoByte:
		return unmarshalTwoByteExtensions(data)
	default:
		if len(data) == 0 {
			return nil, nil
		}
		return []RTPExtension{{ID: 0, Payload: data}}, nil
	}
}

func unmarshalOneByteExtensions(data []byte) ([]RTPExtension, error) {
	exts := make([]RTPExtension, 0, len(data)/2)
	for i := 0; i < len(data); {
		if data[i] == 0x00 {
			i++
			continue
		}
		id := data[i] >> 4
		if id == 0x0F {
			break
		}
		payloadLen := int(data[i]&0x0F) + 1
		i++
		if i+payloadLen > len(data) {
			return exts, UnmarshalErrorf("rtp: one-byte extension truncated")
		}
		exts = append(exts, RTPExtension{ID: id, Payload: data[i : i+payloadLen]})
		i += payloadLen
	}
	return exts, nil
}

func unmarshalTwoByteExtensions(data []byte) ([]RTPExtension, error) {
	exts := make([]RTPExtension, 0, len(data)/3)
	for i := 0; i < len(data); {
		if data[i] == 0x00 {
			i++
			continue
		}
		id := data[i]
		i++
		if i >= len(data) {
			return exts, UnmarshalErrorf("rtp: two-byte extension truncated at length")
		}
		payloadLen := int(data[i])
		i++
		if i+payloadLen > len(data) {
			return exts, UnmarshalErrorf("rtp: two-byte extension truncated")
		}
		exts = append(exts, RTPExtension{ID: id, Payload: data[i : i+payloadLen]})
		i += payloadLen
	}
	return exts, nil
}

// Marshal serializes the RTP packet back into bytes.
func (p *RTPPacket) Marshal() ([]byte, error) {
	size := p.MarshalSize()
	buf := make([]byte, size)
	n, err := p.MarshalTo(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// MarshalSize returns the size of the marshaled RTP packet.
func (p *RTPPacket) MarshalSize() int {
	sz := rtpFixedHeaderLen + len(p.Header.CSRC)*4
	if p.Header.Extension {
		extSize := 4
		for _, ext := range p.Header.Extensions {
			switch p.Header.ExtensionProfile {
			case ExtensionProfileOneByte:
				extSize += 1 + len(ext.Payload)
			case ExtensionProfileTwoByte:
				extSize += 2 + len(ext.Payload)
			default:
				extSize += len(ext.Payload)
			}
		}
		extSize = ((extSize + 3) / 4) * 4
		sz += extSize
	}
	sz += len(p.Payload) + int(p.Header.PaddingSize)
	return sz
}

// MarshalTo serializes the RTP packet into the provided buffer.
func (p *RTPPacket) MarshalTo(buf []byte) (int, error) {
	if len(buf) < p.MarshalSize() {
		return 0, fmt.Errorf("rtp: buffer too small for marshal")
	}

	cc := len(p.Header.CSRC)
	if cc > 15 {
		return 0, UnmarshalErrorf("rtp: too many CSRCs: %d", cc)
	}

	buf[0] = (p.Header.Version << 6) | byte(cc)
	if p.Header.Padding {
		buf[0] |= 1 << 5
	}
	if p.Header.Extension {
		buf[0] |= 1 << 4
	}

	buf[1] = p.Header.PayloadType
	if p.Header.Marker {
		buf[1] |= 1 << 7
	}

	binary.BigEndian.PutUint16(buf[2:4], p.Header.SequenceNumber)
	binary.BigEndian.PutUint32(buf[4:8], p.Header.Timestamp)
	binary.BigEndian.PutUint32(buf[8:12], p.Header.SSRC)

	n := 12
	for _, csrc := range p.Header.CSRC {
		binary.BigEndian.PutUint32(buf[n:n+4], csrc)
		n += 4
	}

	if p.Header.Extension {
		extHeaderStart := n
		binary.BigEndian.PutUint16(buf[n:n+2], p.Header.ExtensionProfile)
		n += 4
		extDataStart := n

		switch p.Header.ExtensionProfile {
		case ExtensionProfileOneByte:
			for _, ext := range p.Header.Extensions {
				if ext.ID == 0 || ext.ID >= 15 {
					return 0, UnmarshalErrorf("rtp: one-byte extension ID must be 1-14")
				}
				if len(ext.Payload) > 16 {
					return 0, UnmarshalErrorf("rtp: one-byte extension payload max 16 bytes")
				}
				buf[n] = (ext.ID << 4) | (uint8(len(ext.Payload)) - 1)
				n++
				n += copy(buf[n:], ext.Payload)
			}
		case ExtensionProfileTwoByte:
			for _, ext := range p.Header.Extensions {
				if ext.ID == 0 {
					return 0, UnmarshalErrorf("rtp: two-byte extension ID must be 1-255")
				}
				if len(ext.Payload) > 255 {
					return 0, UnmarshalErrorf("rtp: two-byte extension payload max 255 bytes")
				}
				buf[n] = ext.ID
				n++
				buf[n] = uint8(len(ext.Payload))
				n++
				n += copy(buf[n:], ext.Payload)
			}
		default:
			if len(p.Header.Extensions) > 1 {
				return 0, UnmarshalErrorf("rtp: profile-defined extension supports only one extension block")
			}
			if len(p.Header.Extensions) > 0 {
				n += copy(buf[n:], p.Header.Extensions[0].Payload)
			}
		}

		extSize := n - extDataStart
		roundedExtSize := ((extSize + 3) / 4) * 4
		binary.BigEndian.PutUint16(buf[extHeaderStart+2:extHeaderStart+4], uint16(roundedExtSize/4))

		padCount := roundedExtSize - extSize
		if padCount > 0 {
			if p.Header.ExtensionProfile == ExtensionProfileOneByte {
				buf[n] = 0xF0
				n++
				for i := 0; i < padCount-1; i++ {
					buf[n] = 0
					n++
				}
			} else {
				for i := 0; i < padCount; i++ {
					buf[n] = 0
					n++
				}
			}
		}
	}

	n += copy(buf[n:], p.Payload)

	if p.Header.Padding {
		buf[len(buf)-1] = p.Header.PaddingSize
		n = len(buf)
	}

	return n, nil
}
