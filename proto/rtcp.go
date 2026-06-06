package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const rtcpVersion = 2

// RTCPPacketType identifies the type of an RTCP packet.
type RTCPPacketType uint8

const (
	RTCPTypeSR   RTCPPacketType = 200
	RTCPTypeRR   RTCPPacketType = 201
	RTCPTypeSDES RTCPPacketType = 202
	RTCPTypeBYE  RTCPPacketType = 203
	RTCPTypeAPP  RTCPPacketType = 204
)

func (t RTCPPacketType) String() string {
	switch t {
	case RTCPTypeSR:
		return "SR"
	case RTCPTypeRR:
		return "RR"
	case RTCPTypeSDES:
		return "SDES"
	case RTCPTypeBYE:
		return "BYE"
	case RTCPTypeAPP:
		return "APP"
	default:
		return fmt.Sprintf("PT=%d", uint8(t))
	}
}

// RTCPHeader is the common header shared by all RTCP packets (4 bytes).
type RTCPHeader struct {
	Padding bool
	Count   uint8
	Type    RTCPPacketType
	Length  uint16
}

func (h RTCPHeader) String() string {
	return fmt.Sprintf("RTCP %s count=%d len=%d pad=%t", h.Type, h.Count, h.Length, h.Padding)
}

// RTCPPacket is the interface implemented by all RTCP packet types.
type RTCPPacket interface {
	Header() RTCPHeader
	DestinationSSRC() []uint32
	MarshalTo([]byte) (int, error)
	MarshalSize() int
}

// ReceptionReport conveys reception statistics for a single source (24 bytes).
type ReceptionReport struct {
	SSRC               uint32
	FractionLost       uint8
	TotalLost          uint32
	LastSequenceNumber uint32
	Jitter             uint32
	LastSenderReport   uint32
	Delay              uint32
}

// SenderReport (SR, PT=200) provides transmission and reception statistics.
type SenderReport struct {
	Reports           []ReceptionReport
	ProfileExtensions []byte
	NTPTime           uint64
	SSRC              uint32
	RTPTime           uint32
	PacketCount       uint32
	OctetCount        uint32
	hdr               RTCPHeader
}

func (p *SenderReport) Header() RTCPHeader { return p.hdr }
func (p *SenderReport) DestinationSSRC() []uint32 {
	out := make([]uint32, len(p.Reports)+1)
	for i, v := range p.Reports {
		out[i] = v.SSRC
	}
	out[len(p.Reports)] = p.SSRC
	return out
}

func (p SenderReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "SR ssrc=%x ntp=%d rtp=%d pkts=%d octets=%d reports=%d",
		p.SSRC, p.NTPTime, p.RTPTime, p.PacketCount, p.OctetCount, len(p.Reports))
	return b.String()
}

// ReceiverReport (RR, PT=201) provides reception statistics.
type ReceiverReport struct {
	Reports           []ReceptionReport
	ProfileExtensions []byte
	SSRC              uint32
	hdr               RTCPHeader
}

func (p *ReceiverReport) Header() RTCPHeader { return p.hdr }
func (p *ReceiverReport) DestinationSSRC() []uint32 {
	out := make([]uint32, len(p.Reports))
	for i, v := range p.Reports {
		out[i] = v.SSRC
	}
	return out
}

func (p ReceiverReport) String() string {
	return fmt.Sprintf("RR ssrc=%x reports=%d", p.SSRC, len(p.Reports))
}

// SDESType identifies an SDES item type.
type SDESType uint8

const (
	SDESEnd      SDESType = 0
	SDESCNAME    SDESType = 1
	SDESName     SDESType = 2
	SDESEmail    SDESType = 3
	SDESPhone    SDESType = 4
	SDESLocation SDESType = 5
	SDESTool     SDESType = 6
	SDESNote     SDESType = 7
	SDESPrivate  SDESType = 8
)

func (t SDESType) String() string {
	switch t {
	case SDESEnd:
		return "END"
	case SDESCNAME:
		return "CNAME"
	case SDESName:
		return "NAME"
	case SDESEmail:
		return "EMAIL"
	case SDESPhone:
		return "PHONE"
	case SDESLocation:
		return "LOC"
	case SDESTool:
		return "TOOL"
	case SDESNote:
		return "NOTE"
	case SDESPrivate:
		return "PRIV"
	default:
		return fmt.Sprintf("SDES=%d", uint8(t))
	}
}

// SourceDescriptionItem is a single SDES item describing an attribute of a source.
type SourceDescriptionItem struct {
	Text string
	Type SDESType
}

// SourceDescriptionChunk groups items for a single source.
type SourceDescriptionChunk struct {
	Items  []SourceDescriptionItem
	Source uint32
}

// SourceDescription (SDES, PT=202) describes one or more sources.
type SourceDescription struct {
	Chunks []SourceDescriptionChunk
	hdr    RTCPHeader
}

func (p *SourceDescription) Header() RTCPHeader { return p.hdr }
func (p *SourceDescription) DestinationSSRC() []uint32 {
	out := make([]uint32, len(p.Chunks))
	for i, v := range p.Chunks {
		out[i] = v.Source
	}
	return out
}

func (p SourceDescription) String() string {
	return fmt.Sprintf("SDES chunks=%d", len(p.Chunks))
}

// Goodbye (BYE, PT=203) indicates that one or more sources are no longer active.
type Goodbye struct {
	Reason  string
	Sources []uint32
	hdr     RTCPHeader
}

func (p *Goodbye) Header() RTCPHeader { return p.hdr }
func (p *Goodbye) DestinationSSRC() []uint32 {
	out := make([]uint32, len(p.Sources))
	copy(out, p.Sources)
	return out
}

func (p Goodbye) String() string {
	return fmt.Sprintf("BYE sources=%d reason=%q", len(p.Sources), p.Reason)
}

// ApplicationDefined (APP, PT=204) carries application-specific data.
type ApplicationDefined struct {
	Name    string
	Data    []byte
	SSRC    uint32
	hdr     RTCPHeader
	SubType uint8
}

func (p *ApplicationDefined) Header() RTCPHeader        { return p.hdr }
func (p *ApplicationDefined) DestinationSSRC() []uint32 { return []uint32{p.SSRC} }

func (p ApplicationDefined) String() string {
	return fmt.Sprintf("APP subtype=%d ssrc=%x name=%q data=%d bytes", p.SubType, p.SSRC, p.Name, len(p.Data))
}

// RawRTCP is a fallback for unknown RTCP packet types.
type RawRTCP struct {
	Data        []byte
	HeaderField RTCPHeader
}

func (p *RawRTCP) Header() RTCPHeader        { return p.HeaderField }
func (p *RawRTCP) DestinationSSRC() []uint32 { return nil }

func marshalHeaderTo(buf []byte, padding bool, count uint8, typ RTCPPacketType, length uint16) {
	buf[0] = (rtcpVersion << 6) | (count & 0x1F)
	if padding {
		buf[0] |= 1 << 5
	}
	buf[1] = byte(typ)
	binary.BigEndian.PutUint16(buf[2:4], length)
}

func (p *SenderReport) MarshalSize() int {
	sz := 4 + 4 + 8 + 4 + 4 + 4 // hdr + SSRC + NTP + RTPTime + PktCount + OctetCount
	sz += len(p.Reports) * 24
	sz += len(p.ProfileExtensions)
	return ((sz + 3) / 4) * 4
}

func (p *SenderReport) MarshalTo(buf []byte) (int, error) {
	sz := p.MarshalSize()
	if len(buf) < sz {
		return 0, errors.New("rtcp: buffer too small for sender report")
	}
	count := uint8(len(p.Reports))
	marshalHeaderTo(buf, p.hdr.Padding, count, RTCPTypeSR, uint16(sz/4-1))

	off := 4
	binary.BigEndian.PutUint32(buf[off:], p.SSRC)
	off += 4
	binary.BigEndian.PutUint64(buf[off:], p.NTPTime)
	off += 8
	binary.BigEndian.PutUint32(buf[off:], p.RTPTime)
	off += 4
	binary.BigEndian.PutUint32(buf[off:], p.PacketCount)
	off += 4
	binary.BigEndian.PutUint32(buf[off:], p.OctetCount)
	off += 4

	for i := range p.Reports {
		marshalReceptionReportTo(buf[off:], &p.Reports[i])
		off += 24
	}
	if len(p.ProfileExtensions) > 0 {
		off += copy(buf[off:], p.ProfileExtensions)
	}
	clear(buf[off:sz])
	return sz, nil
}

func (p *SenderReport) Marshal() ([]byte, error) {
	buf := make([]byte, p.MarshalSize())
	_, err := p.MarshalTo(buf)
	return buf, err
}

func (p *ReceiverReport) MarshalSize() int {
	sz := 4 + 4 // hdr + SSRC
	sz += len(p.Reports) * 24
	sz += len(p.ProfileExtensions)
	return ((sz + 3) / 4) * 4
}

func (p *ReceiverReport) MarshalTo(buf []byte) (int, error) {
	sz := p.MarshalSize()
	if len(buf) < sz {
		return 0, errors.New("rtcp: buffer too small for receiver report")
	}
	count := uint8(len(p.Reports))
	marshalHeaderTo(buf, p.hdr.Padding, count, RTCPTypeRR, uint16(sz/4-1))

	off := 4
	binary.BigEndian.PutUint32(buf[off:], p.SSRC)
	off += 4

	for i := range p.Reports {
		marshalReceptionReportTo(buf[off:], &p.Reports[i])
		off += 24
	}
	if len(p.ProfileExtensions) > 0 {
		off += copy(buf[off:], p.ProfileExtensions)
	}
	clear(buf[off:sz])
	return sz, nil
}

func (p *ReceiverReport) Marshal() ([]byte, error) {
	buf := make([]byte, p.MarshalSize())
	_, err := p.MarshalTo(buf)
	return buf, err
}

func marshalReceptionReportTo(buf []byte, rr *ReceptionReport) {
	binary.BigEndian.PutUint32(buf[0:4], rr.SSRC)
	buf[4] = rr.FractionLost
	buf[5] = byte(rr.TotalLost >> 16)
	buf[6] = byte(rr.TotalLost >> 8)
	buf[7] = byte(rr.TotalLost)
	binary.BigEndian.PutUint32(buf[8:12], rr.LastSequenceNumber)
	binary.BigEndian.PutUint32(buf[12:16], rr.Jitter)
	binary.BigEndian.PutUint32(buf[16:20], rr.LastSenderReport)
	binary.BigEndian.PutUint32(buf[20:24], rr.Delay)
}

func (p *SourceDescription) MarshalSize() int {
	sz := 4 // hdr
	for _, chunk := range p.Chunks {
		sz += 4 // SSRC
		for _, item := range chunk.Items {
			sz += 2 + len(item.Text) // type + length + text
		}
		sz += 1            // END marker (0x00)
		sz = (sz + 3) &^ 3 // align chunk to 4 bytes
	}
	return sz
}

func (p *SourceDescription) MarshalTo(buf []byte) (int, error) {
	sz := p.MarshalSize()
	if len(buf) < sz {
		return 0, errors.New("rtcp: buffer too small for source description")
	}
	count := uint8(len(p.Chunks))
	marshalHeaderTo(buf, p.hdr.Padding, count, RTCPTypeSDES, uint16(sz/4-1))

	off := 4
	for ci := range p.Chunks {
		chunk := &p.Chunks[ci]
		binary.BigEndian.PutUint32(buf[off:], chunk.Source)
		off += 4

		for ii := range chunk.Items {
			item := &chunk.Items[ii]
			buf[off] = byte(item.Type)
			off++
			buf[off] = byte(len(item.Text))
			off++
			off += copy(buf[off:], item.Text)
		}
		buf[off] = 0 // END marker
		off++
		off = (off + 3) &^ 3 // align to 4 bytes
	}
	return sz, nil
}

func (p *SourceDescription) Marshal() ([]byte, error) {
	buf := make([]byte, p.MarshalSize())
	_, err := p.MarshalTo(buf)
	return buf, err
}

func (p *Goodbye) MarshalSize() int {
	sz := 4 // hdr
	sz += len(p.Sources) * 4
	if p.Reason != "" {
		sz += 1 + len(p.Reason)
	}
	return ((sz + 3) / 4) * 4
}

func (p *Goodbye) MarshalTo(buf []byte) (int, error) {
	sz := p.MarshalSize()
	if len(buf) < sz {
		return 0, errors.New("rtcp: buffer too small for goodbye")
	}
	count := uint8(len(p.Sources))
	marshalHeaderTo(buf, p.hdr.Padding, count, RTCPTypeBYE, uint16(sz/4-1))

	off := 4
	for i := range p.Sources {
		binary.BigEndian.PutUint32(buf[off:], p.Sources[i])
		off += 4
	}
	if p.Reason != "" {
		buf[off] = byte(len(p.Reason))
		off++
		off += copy(buf[off:], p.Reason)
	}
	clear(buf[off:sz])
	return sz, nil
}

func (p *Goodbye) Marshal() ([]byte, error) {
	buf := make([]byte, p.MarshalSize())
	_, err := p.MarshalTo(buf)
	return buf, err
}

func (p *ApplicationDefined) MarshalSize() int {
	sz := 4 + 4 + 4 // hdr + SSRC + name
	sz += len(p.Data)
	return ((sz + 3) / 4) * 4
}

func (p *ApplicationDefined) MarshalTo(buf []byte) (int, error) {
	sz := p.MarshalSize()
	if len(buf) < sz {
		return 0, errors.New("rtcp: buffer too small for application defined")
	}
	marshalHeaderTo(buf, p.hdr.Padding, p.SubType, RTCPTypeAPP, uint16(sz/4-1))

	off := 4
	binary.BigEndian.PutUint32(buf[off:], p.SSRC)
	off += 4
	off += copy(buf[off:], p.Name[:min(len(p.Name), 4)])
	for i := len(p.Name); i < 4; i++ {
		buf[off] = 0
		off++
	}
	off += copy(buf[off:], p.Data)
	clear(buf[off:sz])
	return sz, nil
}

func (p *ApplicationDefined) Marshal() ([]byte, error) {
	buf := make([]byte, p.MarshalSize())
	_, err := p.MarshalTo(buf)
	return buf, err
}

func (p *RawRTCP) MarshalSize() int {
	return ((4 + len(p.Data)) + 3) / 4 * 4
}

func (p *RawRTCP) MarshalTo(buf []byte) (int, error) {
	sz := p.MarshalSize()
	if len(buf) < sz {
		return 0, errors.New("rtcp: buffer too small for raw packet")
	}
	h := p.HeaderField
	h.Length = uint16(sz/4 - 1)
	marshalHeaderTo(buf, h.Padding, h.Count, h.Type, h.Length)
	off := 4
	off += copy(buf[off:], p.Data)
	clear(buf[off:sz])
	return sz, nil
}

func (p *RawRTCP) Marshal() ([]byte, error) {
	buf := make([]byte, p.MarshalSize())
	_, err := p.MarshalTo(buf)
	return buf, err
}

// MarshalRTCP serializes a compound RTCP packet into a single byte slice.
// RFC 3550 §6.1 mandates that RTCP packets MUST be sent as compound packets;
// ParseRTCP likewise expects compound input, so this provides the symmetric
// operation to produce valid compound output.
func MarshalRTCP(packets []RTCPPacket) ([]byte, error) {
	if len(packets) == 0 {
		return nil, UnmarshalErrorf("rtcp: no packets to marshal")
	}
	var total int
	for _, pkt := range packets {
		total += pkt.MarshalSize()
	}
	buf := make([]byte, total)
	n := 0
	for _, pkt := range packets {
		nn, err := pkt.MarshalTo(buf[n:])
		if err != nil {
			return nil, err
		}
		n += nn
	}
	return buf[:n], nil
}

func unmarshalRTCPHeader(data []byte) (RTCPHeader, error) {
	if len(data) < 4 {
		return RTCPHeader{}, UnmarshalErrorf("rtcp: packet too short for header")
	}
	version := data[0] >> 6 & 0x03
	if version != rtcpVersion {
		return RTCPHeader{}, UnmarshalErrorf("rtcp: bad version %d", version)
	}
	return RTCPHeader{
		Padding: (data[0]>>5)&0x01 != 0,
		Count:   data[0] & 0x1F,
		Type:    RTCPPacketType(data[1]),
		Length:  binary.BigEndian.Uint16(data[2:4]),
	}, nil
}

// UnmarshalRTCP parses a compound RTCP packet from a byte slice, returning all
// individual RTCP packets contained within.
func UnmarshalRTCP(data []byte) ([]RTCPPacket, error) {
	packets := make([]RTCPPacket, 0, 4)
	for len(data) > 0 {
		if len(data) < 4 {
			return packets, UnmarshalErrorf("rtcp: truncated compound packet")
		}
		hdr, err := unmarshalRTCPHeader(data)
		if err != nil {
			return packets, err
		}
		pktLen := (int(hdr.Length) + 1) * 4
		if pktLen > len(data) {
			return packets, UnmarshalErrorf("rtcp: packet length %d exceeds remaining data %d", pktLen, len(data))
		}
		pktData := data[:pktLen]

		var pkt RTCPPacket
		switch hdr.Type {
		case RTCPTypeSR:
			pkt, err = unmarshalSenderReport(hdr, pktData)
		case RTCPTypeRR:
			pkt, err = unmarshalReceiverReport(hdr, pktData)
		case RTCPTypeSDES:
			pkt, err = unmarshalSourceDescription(hdr, pktData)
		case RTCPTypeBYE:
			pkt, err = unmarshalGoodbye(hdr, pktData)
		case RTCPTypeAPP:
			pkt, err = unmarshalApplicationDefined(hdr, pktData)
		default:
			pkt = &RawRTCP{HeaderField: hdr, Data: pktData[4:]}
		}
		if err != nil {
			return packets, err
		}
		packets = append(packets, pkt)
		data = data[pktLen:]
	}
	if len(packets) == 0 {
		return nil, UnmarshalErrorf("rtcp: empty compound packet")
	}
	return packets, nil
}

func unmarshalSenderReport(hdr RTCPHeader, data []byte) (*SenderReport, error) {
	if len(data) < 28 {
		return nil, UnmarshalErrorf("rtcp: sender report too short")
	}
	p := &SenderReport{hdr: hdr, Reports: make([]ReceptionReport, hdr.Count)}
	body := data[4:]
	p.SSRC = binary.BigEndian.Uint32(body[0:4])
	p.NTPTime = binary.BigEndian.Uint64(body[4:12])
	p.RTPTime = binary.BigEndian.Uint32(body[12:16])
	p.PacketCount = binary.BigEndian.Uint32(body[16:20])
	p.OctetCount = binary.BigEndian.Uint32(body[20:24])

	off := 24
	for i := range hdr.Count {
		if off+24 > len(body) {
			return nil, UnmarshalErrorf("rtcp: sender report truncated in report block %d", i)
		}
		rr, err := unmarshalReceptionReport(body[off:])
		if err != nil {
			return nil, err
		}
		p.Reports[i] = rr
		off += 24
	}
	if off < len(body) {
		p.ProfileExtensions = body[off:]
	}
	return p, nil
}

func unmarshalReceiverReport(hdr RTCPHeader, data []byte) (*ReceiverReport, error) {
	if len(data) < 8 {
		return nil, UnmarshalErrorf("rtcp: receiver report too short")
	}
	p := &ReceiverReport{hdr: hdr, Reports: make([]ReceptionReport, hdr.Count)}
	body := data[4:]
	p.SSRC = binary.BigEndian.Uint32(body[0:4])

	off := 4
	for i := range hdr.Count {
		if off+24 > len(body) {
			return nil, UnmarshalErrorf("rtcp: receiver report truncated in report block %d", i)
		}
		rr, err := unmarshalReceptionReport(body[off:])
		if err != nil {
			return nil, err
		}
		p.Reports[i] = rr
		off += 24
	}
	if off < len(body) {
		p.ProfileExtensions = body[off:]
	}
	return p, nil
}

func unmarshalReceptionReport(data []byte) (ReceptionReport, error) {
	if len(data) < 24 {
		return ReceptionReport{}, UnmarshalErrorf("rtcp: reception report block too short")
	}
	rr := ReceptionReport{
		SSRC:               binary.BigEndian.Uint32(data[0:4]),
		FractionLost:       data[4],
		LastSequenceNumber: binary.BigEndian.Uint32(data[8:12]),
		Jitter:             binary.BigEndian.Uint32(data[12:16]),
		LastSenderReport:   binary.BigEndian.Uint32(data[16:20]),
		Delay:              binary.BigEndian.Uint32(data[20:24]),
	}
	rr.TotalLost = uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])
	return rr, nil
}

func unmarshalSourceDescription(hdr RTCPHeader, data []byte) (*SourceDescription, error) {
	p := &SourceDescription{hdr: hdr, Chunks: make([]SourceDescriptionChunk, 0, hdr.Count)}
	body := data[4:]
	off := 0
	for i := range hdr.Count {
		if off+4 > len(body) {
			return nil, UnmarshalErrorf("rtcp: sdes chunk %d too short for source", i)
		}
		var chunk SourceDescriptionChunk
		chunk.Source = binary.BigEndian.Uint32(body[off:])
		off += 4

		chunk.Items = make([]SourceDescriptionItem, 0, 2)

		for off < len(body) {
			if body[off] == 0 {
				off++
				break
			}
			if off+2 > len(body) {
				return nil, UnmarshalErrorf("rtcp: sdes item %d truncated at header", len(chunk.Items))
			}
			item := SourceDescriptionItem{
				Type: SDESType(body[off]),
			}
			itemLen := int(body[off+1])
			off += 2
			if off+itemLen > len(body) {
				return nil, UnmarshalErrorf("rtcp: sdes item %d truncated at text", len(chunk.Items))
			}
			item.Text = string(body[off : off+itemLen])
			off += itemLen
			chunk.Items = append(chunk.Items, item)
		}
		off = (off + 3) &^ 3
		p.Chunks = append(p.Chunks, chunk)
	}
	return p, nil
}

func unmarshalGoodbye(hdr RTCPHeader, data []byte) (*Goodbye, error) {
	p := &Goodbye{hdr: hdr}
	body := data[4:]
	p.Sources = make([]uint32, hdr.Count)
	for i := range hdr.Count {
		if int(i)*4+4 > len(body) {
			return nil, UnmarshalErrorf("rtcp: bye truncated at source %d", i)
		}
		p.Sources[i] = binary.BigEndian.Uint32(body[int(i)*4:])
	}
	off := int(hdr.Count) * 4
	if off < len(body) {
		reasonLen := int(body[off])
		off++
		if off+reasonLen > len(body) {
			return nil, UnmarshalErrorf("rtcp: bye truncated at reason")
		}
		p.Reason = string(body[off : off+reasonLen])
	}
	return p, nil
}

func unmarshalApplicationDefined(hdr RTCPHeader, data []byte) (*ApplicationDefined, error) {
	if len(data) < 12 {
		return nil, UnmarshalErrorf("rtcp: app packet too short")
	}
	p := &ApplicationDefined{hdr: hdr}
	p.SubType = hdr.Count
	p.SSRC = binary.BigEndian.Uint32(data[4:8])
	p.Name = string(data[8:12])
	p.Data = data[12:]
	if hdr.Padding && len(p.Data) > 0 {
		padSize := int(p.Data[len(p.Data)-1])
		if padSize > 0 && padSize <= len(p.Data) {
			p.Data = p.Data[:len(p.Data)-padSize]
		}
	}
	return p, nil
}
