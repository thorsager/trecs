package proto

import (
	"encoding/binary"
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
	SSRC             uint32
	NTPTime          uint64
	RTPTime          uint32
	PacketCount      uint32
	OctetCount       uint32
	Reports          []ReceptionReport
	ProfileExtensions []byte
	hdr              RTCPHeader
}

func (p *SenderReport) Header() RTCPHeader       { return p.hdr }
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
	SSRC              uint32
	Reports           []ReceptionReport
	ProfileExtensions []byte
	hdr               RTCPHeader
}

func (p *ReceiverReport) Header() RTCPHeader       { return p.hdr }
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
	Type SDESType
	Text string
}

// SourceDescriptionChunk groups items for a single source.
type SourceDescriptionChunk struct {
	Source uint32
	Items  []SourceDescriptionItem
}

// SourceDescription (SDES, PT=202) describes one or more sources.
type SourceDescription struct {
	Chunks []SourceDescriptionChunk
	hdr    RTCPHeader
}

func (p *SourceDescription) Header() RTCPHeader       { return p.hdr }
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
	Sources []uint32
	Reason  string
	hdr     RTCPHeader
}

func (p *Goodbye) Header() RTCPHeader       { return p.hdr }
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
	SubType uint8
	SSRC    uint32
	Name    string
	Data    []byte
	hdr     RTCPHeader
}

func (p *ApplicationDefined) Header() RTCPHeader      { return p.hdr }
func (p *ApplicationDefined) DestinationSSRC() []uint32 { return []uint32{p.SSRC} }

func (p ApplicationDefined) String() string {
	return fmt.Sprintf("APP subtype=%d ssrc=%x name=%q data=%d bytes", p.SubType, p.SSRC, p.Name, len(p.Data))
}

// RawRTCP is a fallback for unknown RTCP packet types.
type RawRTCP struct {
	HeaderField RTCPHeader
	Data        []byte
}

func (p *RawRTCP) Header() RTCPHeader           { return p.HeaderField }
func (p *RawRTCP) DestinationSSRC() []uint32     { return nil }

func parseRTCPHeader(data []byte) (RTCPHeader, error) {
	if len(data) < 4 {
		return RTCPHeader{}, ParseError("rtcp: packet too short for header")
	}
	version := data[0] >> 6 & 0x03
	if version != rtcpVersion {
		return RTCPHeader{}, ParseError("rtcp: bad version %d", version)
	}
	return RTCPHeader{
		Padding: (data[0]>>5)&0x01 != 0,
		Count:   data[0] & 0x1F,
		Type:    RTCPPacketType(data[1]),
		Length:  binary.BigEndian.Uint16(data[2:4]),
	}, nil
}

// ParseRTCP parses a compound RTCP packet from a byte slice, returning all
// individual RTCP packets contained within.
func ParseRTCP(data []byte) ([]RTCPPacket, error) {
	packets := make([]RTCPPacket, 0, 4)
	for len(data) > 0 {
		if len(data) < 4 {
			return packets, ParseError("rtcp: truncated compound packet")
		}
		hdr, err := parseRTCPHeader(data)
		if err != nil {
			return packets, err
		}
		pktLen := (int(hdr.Length) + 1) * 4
		if pktLen > len(data) {
			return packets, ParseError("rtcp: packet length %d exceeds remaining data %d", pktLen, len(data))
		}
		pktData := data[:pktLen]

		var pkt RTCPPacket
		switch hdr.Type {
		case RTCPTypeSR:
			pkt, err = parseSenderReport(hdr, pktData)
		case RTCPTypeRR:
			pkt, err = parseReceiverReport(hdr, pktData)
		case RTCPTypeSDES:
			pkt, err = parseSourceDescription(hdr, pktData)
		case RTCPTypeBYE:
			pkt, err = parseGoodbye(hdr, pktData)
		case RTCPTypeAPP:
			pkt, err = parseApplicationDefined(hdr, pktData)
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
		return nil, ParseError("rtcp: empty compound packet")
	}
	return packets, nil
}

func parseSenderReport(hdr RTCPHeader, data []byte) (*SenderReport, error) {
	if len(data) < 28 {
		return nil, ParseError("rtcp: sender report too short")
	}
	p := &SenderReport{hdr: hdr, Reports: make([]ReceptionReport, hdr.Count)}
	body := data[4:]
	p.SSRC = binary.BigEndian.Uint32(body[0:4])
	p.NTPTime = binary.BigEndian.Uint64(body[4:12])
	p.RTPTime = binary.BigEndian.Uint32(body[12:16])
	p.PacketCount = binary.BigEndian.Uint32(body[16:20])
	p.OctetCount = binary.BigEndian.Uint32(body[20:24])

	off := 24
	for i := 0; i < int(hdr.Count); i++ {
		if off+24 > len(body) {
			return nil, ParseError("rtcp: sender report truncated in report block %d", i)
		}
		rr, err := parseReceptionReport(body[off:])
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

func parseReceiverReport(hdr RTCPHeader, data []byte) (*ReceiverReport, error) {
	if len(data) < 8 {
		return nil, ParseError("rtcp: receiver report too short")
	}
	p := &ReceiverReport{hdr: hdr, Reports: make([]ReceptionReport, hdr.Count)}
	body := data[4:]
	p.SSRC = binary.BigEndian.Uint32(body[0:4])

	off := 4
	for i := 0; i < int(hdr.Count); i++ {
		if off+24 > len(body) {
			return nil, ParseError("rtcp: receiver report truncated in report block %d", i)
		}
		rr, err := parseReceptionReport(body[off:])
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

func parseReceptionReport(data []byte) (ReceptionReport, error) {
	if len(data) < 24 {
		return ReceptionReport{}, ParseError("rtcp: reception report block too short")
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

func parseSourceDescription(hdr RTCPHeader, data []byte) (*SourceDescription, error) {
	p := &SourceDescription{hdr: hdr, Chunks: make([]SourceDescriptionChunk, 0, hdr.Count)}
	body := data[4:]
	off := 0
	for i := 0; i < int(hdr.Count); i++ {
		if off+4 > len(body) {
			return nil, ParseError("rtcp: sdes chunk %d too short for source", i)
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
				return nil, ParseError("rtcp: sdes item %d truncated at header", len(chunk.Items))
			}
			item := SourceDescriptionItem{
				Type: SDESType(body[off]),
			}
			itemLen := int(body[off+1])
			off += 2
			if off+itemLen > len(body) {
				return nil, ParseError("rtcp: sdes item %d truncated at text", len(chunk.Items))
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

func parseGoodbye(hdr RTCPHeader, data []byte) (*Goodbye, error) {
	p := &Goodbye{hdr: hdr}
	body := data[4:]
	p.Sources = make([]uint32, hdr.Count)
	for i := 0; i < int(hdr.Count); i++ {
		if i*4+4 > len(body) {
			return nil, ParseError("rtcp: bye truncated at source %d", i)
		}
		p.Sources[i] = binary.BigEndian.Uint32(body[i*4:])
	}
	off := int(hdr.Count) * 4
	if off < len(body) {
		reasonLen := int(body[off])
		off++
		if off+reasonLen > len(body) {
			return nil, ParseError("rtcp: bye truncated at reason")
		}
		p.Reason = string(body[off : off+reasonLen])
	}
	return p, nil
}

func parseApplicationDefined(hdr RTCPHeader, data []byte) (*ApplicationDefined, error) {
	if len(data) < 12 {
		return nil, ParseError("rtcp: app packet too short")
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
