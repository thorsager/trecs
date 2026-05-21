package proto

import (
	"bufio"
	"encoding/binary"
	"strings"
	"testing"
)

func BenchmarkParseSIP_Invite(b *testing.B) {
	input := "INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds\r\n" +
		"Max-Forwards: 70\r\n" +
		"To: Bob <sip:bob@biloxi.com>\r\n" +
		"From: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Contact: <sip:alice@pc33.atlanta.com>\r\n" +
		"Content-Type: application/sdp\r\n" +
		"Content-Length: 137\r\n" +
		"\r\n" +
		"v=0\r\n" +
		"o=alice 2890844526 2890844526 IN IP4 pc33.atlanta.com\r\n" +
		"s=-\r\n" +
		"c=IN IP4 pc33.atlanta.com\r\n" +
		"t=0 0\r\n" +
		"m=audio 49172 RTP/AVP 0\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := newTestReader(input)
		msg, err := UnmarshalSIP(r)
		if err != nil {
			b.Fatal(err)
		}
		if msg == nil {
			b.Fatal("nil message")
		}
	}
}

func BenchmarkParseSIP_Register(b *testing.B) {
	input := "REGISTER sip:registrar.biloxi.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP bobspc.biloxi.com:5060;branch=z9hG4bKnashds7\r\n" +
		"Max-Forwards: 70\r\n" +
		"To: Bob <sip:bob@biloxi.com>\r\n" +
		"From: Bob <sip:bob@biloxi.com>;tag=456248\r\n" +
		"Call-ID: 843817637684230@998sdasdh09\r\n" +
		"CSeq: 1826 REGISTER\r\n" +
		"Contact: <sip:bob@bobspc.biloxi.com>\r\n" +
		"Expires: 7200\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := newTestReader(input)
		msg, err := UnmarshalSIP(r)
		if err != nil {
			b.Fatal(err)
		}
		if msg == nil {
			b.Fatal("nil message")
		}
	}
}

func BenchmarkParseSIP_Response(b *testing.B) {
	input := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds;received=192.0.2.1\r\n" +
		"To: Bob <sip:bob@biloxi.com>;tag=a6c85cf\r\n" +
		"From: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Contact: <sip:bob@biloxi.com>\r\n" +
		"Content-Type: application/sdp\r\n" +
		"Content-Length: 131\r\n" +
		"\r\n" +
		"v=0\r\n" +
		"o=bob 2890844527 2890844527 IN IP4 biloxi.com\r\n" +
		"s=-\r\n" +
		"c=IN IP4 biloxi.com\r\n" +
		"t=0 0\r\n" +
		"m=audio 3456 RTP/AVP 0\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := newTestReader(input)
		msg, err := UnmarshalSIP(r)
		if err != nil {
			b.Fatal(err)
		}
		if msg == nil {
			b.Fatal("nil message")
		}
	}
}

func BenchmarkParseSIP_Minimal(b *testing.B) {
	input := "OPTIONS sip:example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP client.example.com:5060;branch=z9hG4bK1\r\n" +
		"Max-Forwards: 70\r\n" +
		"To: <sip:example.com>\r\n" +
		"From: <sip:client@example.com>;tag=1\r\n" +
		"Call-ID: 1@client.example.com\r\n" +
		"CSeq: 1 OPTIONS\r\n" +
		"Accept: application/sdp\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := newTestReader(input)
		msg, err := UnmarshalSIP(r)
		if err != nil {
			b.Fatal(err)
		}
		if msg == nil {
			b.Fatal("nil message")
		}
	}
}

func BenchmarkParseSIP_ManyHeaders(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("INVITE sip:bob@biloxi.com SIP/2.0\r\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("X-Extension-Header-")
		sb.WriteString(string(rune('A' + i%26)))
		sb.WriteString(": value-")
		sb.WriteString(string(rune('0' + i%10)))
		sb.WriteString("\r\n")
	}
	sb.WriteString("To: Bob <sip:bob@biloxi.com>\r\n")
	sb.WriteString("From: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n")
	sb.WriteString("Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n")
	sb.WriteString("CSeq: 314159 INVITE\r\n")
	sb.WriteString("Content-Length: 0\r\n")
	sb.WriteString("\r\n")
	input := sb.String()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := newTestReader(input)
		msg, err := UnmarshalSIP(r)
		if err != nil {
			b.Fatal(err)
		}
		if msg == nil {
			b.Fatal("nil message")
		}
	}
}

func BenchmarkParseSIPAddress_NameAddr(b *testing.B) {
	raw := `"Alice" <sip:alice@atlanta.com>;tag=1928301774`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalSIPAddress(raw)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseSIPAddress_AddrSpec(b *testing.B) {
	raw := `sip:alice@atlanta.com;tag=887s`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalSIPAddress(raw)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseSIPAddress_BareURI(b *testing.B) {
	raw := `sip:alice@atlanta.com`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalSIPAddress(raw)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCanonicalHeaderKey(b *testing.B) {
	keys := []string{
		"content-type", "call-id", "cseq", "from", "to",
		"via", "max-forwards", "contact", "www-authenticate",
		"authorization", "content-length", "user-agent",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			_ = canonicalHeaderKey(k)
		}
	}
}

func BenchmarkParseCSeq(b *testing.B) {
	values := []string{"1 INVITE", "314159 INVITE", "1 REGISTER", "101 OPTIONS", "2147483647 INVITE"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, v := range values {
			_, err := unmarshalCSeq([]string{v})
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkParseMethod(b *testing.B) {
	methods := []string{"INVITE", "REGISTER", "OPTIONS", "BYE", "CANCEL", "ACK", "PRACK", "SUBSCRIBE",
		"NOTIFY", "PUBLISH", "INFO", "REFER", "MESSAGE", "UPDATE"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, m := range methods {
			_, err := unmarshalMethod(m)
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkReadLine(b *testing.B) {
	input := "Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds\r\n"
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := bufio.NewReader(strings.NewReader(input))
		line, err := readLine(r)
		if err != nil {
			b.Fatal(err)
		}
		if line == "" {
			b.Fatal("empty line")
		}
	}
}

func BenchmarkSIPMessage_GetFirst(b *testing.B) {
	input := "INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds\r\n" +
		"Max-Forwards: 70\r\n" +
		"To: Bob <sip:bob@biloxi.com>\r\n" +
		"From: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Contact: <sip:alice@pc33.atlanta.com>\r\n" +
		"Content-Type: application/sdp\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n"
	r := newTestReader(input)
	msg, err := UnmarshalSIP(r)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := msg.Headers.GetFirst("From")
		if h == "" {
			b.Fatal("empty header")
		}
	}
}

func BenchmarkSIPMessage_Method(b *testing.B) {
	input := "INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds\r\n" +
		"Max-Forwards: 70\r\n" +
		"To: Bob <sip:bob@biloxi.com>\r\n" +
		"From: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n"
	r := newTestReader(input)
	msg, err := UnmarshalSIP(r)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		m := msg.Method()
		if m == "" {
			b.Fatal("zero method")
		}
	}
}

func BenchmarkParseRTP_Minimal(b *testing.B) {
	data := make([]byte, 12)
	data[0] = 0x80
	data[1] = 0
	binary.BigEndian.PutUint16(data[2:4], 1)
	binary.BigEndian.PutUint32(data[4:8], 100)
	binary.BigEndian.PutUint32(data[8:12], 0xDEADBEEF)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRTP_WithPayload(b *testing.B) {
	payload := make([]byte, 160)
	data := make([]byte, 12+len(payload))
	data[0] = 0x80
	data[1] = 0
	binary.BigEndian.PutUint16(data[2:4], 1)
	binary.BigEndian.PutUint32(data[4:8], 100)
	binary.BigEndian.PutUint32(data[8:12], 0xDEADBEEF)
	copy(data[12:], payload)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRTP_WithCSRC(b *testing.B) {
	csrc := []uint32{0x11111111, 0x22222222, 0x33333333}
	data := make([]byte, 12+4*len(csrc))
	data[0] = 0x80 | byte(len(csrc))
	data[1] = 96
	binary.BigEndian.PutUint16(data[2:4], 42)
	binary.BigEndian.PutUint32(data[4:8], 5000)
	binary.BigEndian.PutUint32(data[8:12], 0xAAAAAAAA)
	for i, c := range csrc {
		binary.BigEndian.PutUint32(data[12+i*4:], c)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRTP_OneByteExtension(b *testing.B) {
	extData := []byte{0x31, 0xAA, 0xBB, 0x52, 0xCC, 0xDD, 0xEE, 0x00}
	data := make([]byte, 12+4+len(extData))
	data[0] = 0x90
	data[1] = 96
	binary.BigEndian.PutUint16(data[2:4], 1)
	binary.BigEndian.PutUint32(data[4:8], 100)
	binary.BigEndian.PutUint32(data[8:12], 0xCAFEBABE)
	binary.BigEndian.PutUint16(data[12:14], 0xBEDE)
	binary.BigEndian.PutUint16(data[14:16], uint16(len(extData)/4))
	copy(data[16:], extData)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRTCP_Compound(b *testing.B) {
	var compound []byte

	srHdr := putRTCPHeader(false, 1, RTCPTypeSR, 12)
	compound = append(compound, srHdr...)
	compound = putU32BEAppend(compound, 0x902f9e2e)
	compound = putU64BEAppend(compound, 0xdf3cf7581c604540)
	compound = putU32BEAppend(compound, 0x11223344)
	compound = putU32BEAppend(compound, 17)
	compound = putU32BEAppend(compound, 3400)
	compound = append(compound, makeReceptionReport(0xbc5e9a40, 0, 0, 0x46e1, 273, 0x9f36432, 150137)...)

	sdesHdr := putRTCPHeader(false, 1, RTCPTypeSDES, 3)
	sdesStart := len(compound)
	compound = append(compound, sdesHdr...)
	compound = putU32BEAppend(compound, 0x902f9e2e)
	compound = append(compound, 0x01, 0x09)
	compound = append(compound, []byte("user@host")...)
	compound = append(compound, 0x00)
	for len(compound)%4 != 0 {
		compound = append(compound, 0x00)
	}
	binary.BigEndian.PutUint16(compound[sdesStart+2:sdesStart+4], uint16((len(compound)-sdesStart)/4-1))

	byeHdr := putRTCPHeader(false, 1, RTCPTypeBYE, 1)
	compound = append(compound, byeHdr...)
	compound = putU32BEAppend(compound, 0x902f9e2e)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		packets, err := UnmarshalRTCP(compound)
		if err != nil {
			b.Fatal(err)
		}
		if len(packets) == 0 {
			b.Fatal("no packets")
		}
	}
}

func BenchmarkParseRTCP_SenderReport(b *testing.B) {
	data := make([]byte, 0, 52)
	data = append(data, putRTCPHeader(false, 1, RTCPTypeSR, 12)...)
	data = putU32BEAppend(data, 0x902f9e2e)
	data = putU64BEAppend(data, 0xdf3cf7581c604540)
	data = putU32BEAppend(data, 0x11223344)
	data = putU32BEAppend(data, 17)
	data = putU32BEAppend(data, 3400)
	data = append(data, makeReceptionReport(0xbc5e9a40, 0, 0, 0x46e1, 273, 0x9f36432, 150137)...)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		packets, err := UnmarshalRTCP(data)
		if err != nil {
			b.Fatal(err)
		}
		if len(packets) == 0 {
			b.Fatal("no packets")
		}
	}
}

func BenchmarkParseRTCP_ReceiverReport(b *testing.B) {
	data := make([]byte, 0, 8+48)
	data = append(data, putRTCPHeader(false, 2, RTCPTypeRR, 13)...)
	data = putU32BEAppend(data, 0x11111111)
	data = append(data, makeReceptionReport(0xaaaaaaaa, 5, 10, 54321, 100, 0xffffffff, 200)...)
	data = append(data, makeReceptionReport(0xbbbbbbbb, 0, 0, 9876, 50, 0, 0)...)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		packets, err := UnmarshalRTCP(data)
		if err != nil {
			b.Fatal(err)
		}
		if len(packets) == 0 {
			b.Fatal("no packets")
		}
	}
}

func BenchmarkParseRTCP_SDES(b *testing.B) {
	data := make([]byte, 0, 4+4+2+9+1)
	data = append(data, putRTCPHeader(false, 1, RTCPTypeSDES, 4)...)
	data = putU32BEAppend(data, 0x902f9e2e)
	data = append(data, 0x01, 0x09)
	data = append(data, []byte("test@host")...)
	data = append(data, 0x00)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		packets, err := UnmarshalRTCP(data)
		if err != nil {
			b.Fatal(err)
		}
		if len(packets) == 0 {
			b.Fatal("no packets")
		}
	}
}

func BenchmarkParseRTP_Marshal(b *testing.B) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			Marker:         true,
			PayloadType:    0,
			SequenceNumber: 42,
			Timestamp:      12345,
			SSRC:           0xDEADBEEF,
			CSRC:           []uint32{0x11111111},
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := orig.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRTP_RoundTrip(b *testing.B) {
	orig := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			Marker:         true,
			PayloadType:    0,
			SequenceNumber: 42,
			Timestamp:      12345,
			SSRC:           0xDEADBEEF,
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}
	data, err := orig.Marshal()
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshal_Minimal(b *testing.B) {
	p := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 1,
			Timestamp:      100,
			SSRC:           0xDEADBEEF,
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshal_WithPayload(b *testing.B) {
	p := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 42,
			Timestamp:      5000,
			SSRC:           0xAAAAAAAA,
		},
		Payload: make([]byte, 160),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshal_WithCSRC(b *testing.B) {
	p := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 42,
			Timestamp:      5000,
			SSRC:           0xAAAAAAAA,
			CSRC:           []uint32{0x11111111, 0x22222222, 0x33333333},
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshal_OneByteExtension(b *testing.B) {
	p := &RTPPacket{
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
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshal_TwoByteExtension(b *testing.B) {
	p := &RTPPacket{
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
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshal_WithPadding(b *testing.B) {
	p := &RTPPacket{
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
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshalTo_Minimal(b *testing.B) {
	p := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 1,
			Timestamp:      100,
			SSRC:           0xDEADBEEF,
		},
	}
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshalTo_WithPayload(b *testing.B) {
	p := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 42,
			Timestamp:      5000,
			SSRC:           0xAAAAAAAA,
		},
		Payload: make([]byte, 160),
	}
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshalTo_WithCSRC(b *testing.B) {
	p := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 42,
			Timestamp:      5000,
			SSRC:           0xAAAAAAAA,
			CSRC:           []uint32{0x11111111, 0x22222222, 0x33333333},
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshalTo_OneByteExtension(b *testing.B) {
	p := &RTPPacket{
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
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshalTo_TwoByteExtension(b *testing.B) {
	p := &RTPPacket{
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
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRTCP_Marshal(b *testing.B) {
	data := make([]byte, 0, 52)
	data = append(data, putRTCPHeader(false, 1, RTCPTypeSR, 12)...)
	data = putU32BEAppend(data, 0x902f9e2e)
	data = putU64BEAppend(data, 0xdf3cf7581c604540)
	data = putU32BEAppend(data, 0x11223344)
	data = putU32BEAppend(data, 17)
	data = putU32BEAppend(data, 3400)
	data = append(data, makeReceptionReport(0xbc5e9a40, 0, 0, 0x46e1, 273, 0x9f36432, 150137)...)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTCP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_SR_Minimal(b *testing.B) {
	p := &SenderReport{SSRC: 0x902f9e2e, NTPTime: 0xdf3cf7581c604540, RTPTime: 0x11223344, PacketCount: 17, OctetCount: 3400}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_SR_WithReports(b *testing.B) {
	p := &SenderReport{
		SSRC: 0x902f9e2e, NTPTime: 0xdf3cf7581c604540, RTPTime: 0x11223344, PacketCount: 17, OctetCount: 3400,
		Reports: []ReceptionReport{
			{SSRC: 0xbc5e9a40, FractionLost: 0, TotalLost: 0, LastSequenceNumber: 0x46e1, Jitter: 273, LastSenderReport: 0x9f36432, Delay: 150137},
			{SSRC: 0x12345678, FractionLost: 1, TotalLost: 0x123456, LastSequenceNumber: 0x789A, Jitter: 100, LastSenderReport: 0, Delay: 0},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshalTo_SR_Minimal(b *testing.B) {
	p := &SenderReport{SSRC: 0x902f9e2e, NTPTime: 0xdf3cf7581c604540, RTPTime: 0x11223344, PacketCount: 17, OctetCount: 3400}
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_RR_Minimal(b *testing.B) {
	p := &ReceiverReport{SSRC: 0x11111111}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_RR_WithReports(b *testing.B) {
	p := &ReceiverReport{
		SSRC: 0x11111111,
		Reports: []ReceptionReport{
			{SSRC: 0xaaaaaaaa, FractionLost: 5, TotalLost: 10, LastSequenceNumber: 54321, Jitter: 100, LastSenderReport: 0xffffffff, Delay: 200},
			{SSRC: 0xbbbbbbbb, FractionLost: 0, TotalLost: 0, LastSequenceNumber: 9876, Jitter: 50, LastSenderReport: 0, Delay: 0},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshalTo_RR_Minimal(b *testing.B) {
	p := &ReceiverReport{SSRC: 0x11111111}
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_SDES_Single(b *testing.B) {
	p := &SourceDescription{
		Chunks: []SourceDescriptionChunk{
			{Source: 0x902f9e2e, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "test@host"}}},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_SDES_MultiChunk(b *testing.B) {
	p := &SourceDescription{
		Chunks: []SourceDescriptionChunk{
			{Source: 0x11111111, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "user1"}, {Type: SDESName, Text: "One"}}},
			{Source: 0x22222222, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "user2"}}},
			{Source: 0x33333333, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "user3"}}},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshalTo_SDES_Single(b *testing.B) {
	p := &SourceDescription{
		Chunks: []SourceDescriptionChunk{
			{Source: 0x902f9e2e, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "test@host"}}},
		},
	}
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_BYE_Minimal(b *testing.B) {
	p := &Goodbye{Sources: []uint32{0xDEADBEEF}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_BYE_WithReason(b *testing.B) {
	p := &Goodbye{Sources: []uint32{0xDEADBEEF, 0xCAFEBABE}, Reason: "camera off"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshalTo_BYE_Minimal(b *testing.B) {
	p := &Goodbye{Sources: []uint32{0xDEADBEEF}}
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_APP(b *testing.B) {
	p := &ApplicationDefined{SubType: 3, SSRC: 0x55555555, Name: "TEST", Data: []byte{0x01, 0x02, 0x03, 0x04}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshalTo_APP(b *testing.B) {
	p := &ApplicationDefined{SubType: 3, SSRC: 0x55555555, Name: "TEST", Data: []byte{0x01, 0x02, 0x03, 0x04}}
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_Compound(b *testing.B) {
	sr := &SenderReport{SSRC: 0xAAAAAAAA, NTPTime: 0x1234567890ABCDEF, RTPTime: 5000, PacketCount: 42, OctetCount: 64000}
	sdes := &SourceDescription{Chunks: []SourceDescriptionChunk{{Source: 0xAAAAAAAA, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "test"}}}}}
	bye := &Goodbye{Sources: []uint32{0xAAAAAAAA}}
	packets := []RTCPPacket{sr, sdes, bye}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := MarshalRTCP(packets)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTCPMarshal_CompoundTo(b *testing.B) {
	sr := &SenderReport{SSRC: 0xAAAAAAAA, NTPTime: 0x1234567890ABCDEF, RTPTime: 5000, PacketCount: 42, OctetCount: 64000}
	sdes := &SourceDescription{Chunks: []SourceDescriptionChunk{{Source: 0xAAAAAAAA, Items: []SourceDescriptionItem{{Type: SDESCNAME, Text: "test"}}}}}
	bye := &Goodbye{Sources: []uint32{0xAAAAAAAA}}
	packets := []RTCPPacket{sr, sdes, bye}

	var total int
	for _, p := range packets {
		total += p.MarshalSize()
	}
	buf := make([]byte, total)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		for _, p := range packets {
			nn, err := p.MarshalTo(buf[n:])
			if err != nil {
				b.Fatal(err)
			}
			n += nn
		}
	}
}

func BenchmarkParseSIP_CompactHeaders(b *testing.B) {
	input := "INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
		"v: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds\r\n" +
		"f: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"t: Bob <sip:bob@biloxi.com>\r\n" +
		"i: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"m: <sip:alice@pc33.atlanta.com>\r\n" +
		"c: application/sdp\r\n" +
		"l: 0\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"\r\n"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := newTestReader(input)
		msg, err := UnmarshalSIP(r)
		if err != nil {
			b.Fatal(err)
		}
		if msg == nil {
			b.Fatal("nil message")
		}
	}
}

// ---------------------------------------------------------------------------
// SIP MarshalSize / MarshalCompactSize
// ---------------------------------------------------------------------------

func BenchmarkSIPMarshalSize_Request(b *testing.B) {
	msg := sipBenchRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = msg.MarshalSize()
	}
}

func BenchmarkSIPMarshalSize_Response(b *testing.B) {
	msg := sipBenchResponse()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = msg.MarshalSize()
	}
}

func BenchmarkSIPMarshalCompactSize_Request(b *testing.B) {
	msg := sipBenchRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = msg.MarshalCompactSize()
	}
}

func BenchmarkSIPMarshalCompactSize_Response(b *testing.B) {
	msg := sipBenchResponse()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = msg.MarshalCompactSize()
	}
}

func BenchmarkSIPString(b *testing.B) {
	msg := sipBenchRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = msg.String()
	}
}

func BenchmarkSIPNewRequest(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewRequest(SIPMethodINVITE, "sip:bob@example.com")
	}
}

func BenchmarkSIPNewResponse(b *testing.B) {
	req := sipBenchRequest()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewResponse(req, 200, "OK")
	}
}

// ---------------------------------------------------------------------------
// RTP Unmarshal — missing cases
// ---------------------------------------------------------------------------

func BenchmarkParseRTP_TwoByteExtension(b *testing.B) {
	extData := []byte{0x05, 0x02, 0xDE, 0xAD}
	data := make([]byte, 12+4+len(extData))
	data[0] = 0x90
	data[1] = 96
	binary.BigEndian.PutUint16(data[2:4], 1)
	binary.BigEndian.PutUint32(data[4:8], 100)
	binary.BigEndian.PutUint32(data[8:12], 0xCAFEBABE)
	binary.BigEndian.PutUint16(data[12:14], 0x1000)
	binary.BigEndian.PutUint16(data[14:16], 1)
	copy(data[16:], extData)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRTP_WithPadding(b *testing.B) {
	payload := make([]byte, 160)
	data := make([]byte, 12+len(payload)+4)
	data[0] = 0xA0
	data[1] = 96
	binary.BigEndian.PutUint16(data[2:4], 1)
	binary.BigEndian.PutUint32(data[4:8], 100)
	binary.BigEndian.PutUint32(data[8:12], 0xDEADBEEF)
	copy(data[12:], payload)
	data[len(data)-1] = 4

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRTPMarshalSize_Minimal(b *testing.B) {
	p := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    0,
			SequenceNumber: 1,
			Timestamp:      100,
			SSRC:           0xDEADBEEF,
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.MarshalSize()
	}
}

func BenchmarkRTPMarshalSize_WithPayload(b *testing.B) {
	p := &RTPPacket{
		Header: RTPHeader{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 42,
			Timestamp:      5000,
			SSRC:           0xAAAAAAAA,
		},
		Payload: make([]byte, 160),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.MarshalSize()
	}
}

func BenchmarkRTPMarshalTo_WithPadding(b *testing.B) {
	p := &RTPPacket{
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
	buf := make([]byte, p.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseRTP_RoundTrip_Extensions(b *testing.B) {
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
				{ID: 1, Payload: []byte{0xAA}},
				{ID: 2, Payload: []byte{0xBB, 0xCC}},
			},
		},
		Payload: []byte{0xFF},
	}
	data, err := orig.Marshal()
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalRTP(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// RTCP Unmarshal — missing cases
// ---------------------------------------------------------------------------

func BenchmarkParseRTCP_BYE(b *testing.B) {
	data := make([]byte, 0, 8)
	data = append(data, putRTCPHeader(false, 1, RTCPTypeBYE, 1)...)
	data = putU32BEAppend(data, 0xDEADBEEF)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		packets, err := UnmarshalRTCP(data)
		if err != nil {
			b.Fatal(err)
		}
		if len(packets) == 0 {
			b.Fatal("no packets")
		}
	}
}

func BenchmarkParseRTCP_APP(b *testing.B) {
	data := make([]byte, 0, 16)
	data = append(data, putRTCPHeader(false, 3, RTCPTypeAPP, 3)...)
	data = putU32BEAppend(data, 0x55555555)
	data = append(data, []byte("TEST")...)
	data = append(data, 0x01, 0x02, 0x03, 0x04)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		packets, err := UnmarshalRTCP(data)
		if err != nil {
			b.Fatal(err)
		}
		if len(packets) == 0 {
			b.Fatal("no packets")
		}
	}
}

func BenchmarkParseRTCP_CompoundFull(b *testing.B) {
	var compound []byte

	srHdr := putRTCPHeader(false, 1, RTCPTypeSR, 12)
	compound = append(compound, srHdr...)
	compound = putU32BEAppend(compound, 0x902f9e2e)
	compound = putU64BEAppend(compound, 0xdf3cf7581c604540)
	compound = putU32BEAppend(compound, 0x11223344)
	compound = putU32BEAppend(compound, 17)
	compound = putU32BEAppend(compound, 3400)
	compound = append(compound, makeReceptionReport(0xbc5e9a40, 0, 0, 0x46e1, 273, 0x9f36432, 150137)...)

	sdesHdr := putRTCPHeader(false, 1, RTCPTypeSDES, 3)
	sdesStart := len(compound)
	compound = append(compound, sdesHdr...)
	compound = putU32BEAppend(compound, 0x902f9e2e)
	compound = append(compound, 0x01, 0x09)
	compound = append(compound, []byte("user@host")...)
	compound = append(compound, 0x00)
	for len(compound)%4 != 0 {
		compound = append(compound, 0x00)
	}
	binary.BigEndian.PutUint16(compound[sdesStart+2:sdesStart+4], uint16((len(compound)-sdesStart)/4-1))

	byeHdr := putRTCPHeader(false, 1, RTCPTypeBYE, 1)
	compound = append(compound, byeHdr...)
	compound = putU32BEAppend(compound, 0x902f9e2e)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		packets, err := UnmarshalRTCP(compound)
		if err != nil {
			b.Fatal(err)
		}
		if len(packets) == 0 {
			b.Fatal("no packets")
		}
	}
}

func BenchmarkRTCPMarshalSize_SR(b *testing.B) {
	p := &SenderReport{SSRC: 0x902f9e2e, NTPTime: 0xdf3cf7581c604540, RTPTime: 0x11223344, PacketCount: 17, OctetCount: 3400}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.MarshalSize()
	}
}

func BenchmarkRTCPMarshalSize_RR(b *testing.B) {
	p := &ReceiverReport{
		SSRC: 0x11111111,
		Reports: []ReceptionReport{
			{SSRC: 0xaaaaaaaa, FractionLost: 5, TotalLost: 10, LastSequenceNumber: 54321, Jitter: 100, LastSenderReport: 0xffffffff, Delay: 200},
			{SSRC: 0xbbbbbbbb, FractionLost: 0, TotalLost: 0, LastSequenceNumber: 9876, Jitter: 50, LastSenderReport: 0, Delay: 0},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.MarshalSize()
	}
}
