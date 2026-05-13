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
		msg, err := ParseSIP(r)
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
		msg, err := ParseSIP(r)
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
		msg, err := ParseSIP(r)
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
		msg, err := ParseSIP(r)
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
		msg, err := ParseSIP(r)
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
		_, err := ParseSIPAddress(raw)
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
		_, err := ParseSIPAddress(raw)
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
		_, err := ParseSIPAddress(raw)
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
			_, err := parseCSeq([]string{v})
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
			_, err := parseMethod(m)
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
	msg, err := ParseSIP(r)
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
	msg, err := ParseSIP(r)
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
		_, err := ParseRTP(data)
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
		_, err := ParseRTP(data)
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
		_, err := ParseRTP(data)
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
		_, err := ParseRTP(data)
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
		packets, err := ParseRTCP(compound)
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
		packets, err := ParseRTCP(data)
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
		packets, err := ParseRTCP(data)
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
		packets, err := ParseRTCP(data)
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
		_, err := ParseRTP(data)
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
		_, err := ParseRTCP(data)
		if err != nil {
			b.Fatal(err)
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
		msg, err := ParseSIP(r)
		if err != nil {
			b.Fatal(err)
		}
		if msg == nil {
			b.Fatal("nil message")
		}
	}
}
