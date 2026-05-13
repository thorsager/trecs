package proto

import (
	"bufio"
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
