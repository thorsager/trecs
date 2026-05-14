package proto

import (
	"bufio"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestReader(input string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(input))
}

func TestParseSIP_ValidRequest(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\nContent-Length: 4\r\n\r\nbody"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "INVITE sip:bob@example.com SIP/2.0", msg.StartLine())
		assert.Equal(t, SIPMethodINVITE, msg.Method())
		assert.Equal(t, "SIP/2.0", msg.Version())
		assert.True(t, msg.IsRequest())
		assert.Equal(t, 0, msg.StatusCode())
		assert.NotEmpty(t, msg.Headers.GetFirst("Content-Length"))
		assert.Equal(t, []byte("body"), msg.Body)
	}
}

func TestParseSIP_ValidResponse(t *testing.T) {
	input := "SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "SIP/2.0 200 OK", msg.StartLine())
		assert.Empty(t, msg.Method())
		assert.Equal(t, "SIP/2.0", msg.Version())
		assert.False(t, msg.IsRequest())
		assert.Equal(t, 200, msg.StatusCode())
		assert.Equal(t, "200 OK", msg.Status())
		assert.Empty(t, msg.Body)
	}
}

func TestParseSIP_NoContentLength(t *testing.T) {
	input := "BYE sip:alice@example.com SIP/2.0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "BYE sip:alice@example.com SIP/2.0", msg.StartLine())
		assert.Empty(t, msg.Body)
	}
}

func TestParseSIP_ContentLengthZero(t *testing.T) {
	input := "ACK sip:host SIP/2.0\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "ACK sip:host SIP/2.0", msg.StartLine())
		assert.Empty(t, msg.Body)
	}
}

func TestParseSIP_MultipleHeaders(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nVia: SIP/2.0/TCP host\r\nVia: SIP/2.0/UDP host2\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "INVITE sip:x SIP/2.0", msg.StartLine())
		via := msg.Headers.Get("Via")
		if assert.NotNil(t, via) {
			assert.Len(t, via, 2)
			assert.Contains(t, via, "SIP/2.0/TCP host")
			assert.Contains(t, via, "SIP/2.0/UDP host2")
		}
	}
}

func TestParseSIP_WithBody(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nContent-Length: 13\r\n\r\nv=0\r\no=user 1"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "INVITE sip:x SIP/2.0", msg.StartLine())
		assert.Equal(t, []byte("v=0\r\no=user 1"), msg.Body)
	}
}

func TestParseSIP_EOFOnStartLine(t *testing.T) {
	msg, err := ParseSIP(newTestReader(""))
	assert.Error(t, err)
	assert.Nil(t, msg)
}

func TestParseSIP_EOFOnHeaders(t *testing.T) {
	msg, err := ParseSIP(newTestReader("INVITE sip:x SIP/2.0\r\n"))
	assert.Error(t, err)
	assert.Nil(t, msg)
}

func TestParseSIP_StreamFalseDiscardsExtraData(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\nContent-Length: 4\r\n\r\nbodyEXTRA_DATA_SHOULD_BE_DISCARDED"
	msg, err := ParseSIPUDP([]byte(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "INVITE sip:bob@example.com SIP/2.0", msg.StartLine())
		assert.Equal(t, []byte("body"), msg.Body)
	}
}

func TestParseSIP_NoContentLengthReadsAllRemainingData(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n\r\nall remaining data goes into body"
	msg, err := ParseSIPUDP([]byte(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "INVITE sip:bob@example.com SIP/2.0", msg.StartLine())
		assert.Equal(t, []byte("all remaining data goes into body"), msg.Body)
	}
}

func TestParseSIP_CSeqHeader(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\nCSeq: 314159 INVITE\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, 314159, msg.CSeq.Seq)
		assert.Equal(t, SIPMethodINVITE, msg.CSeq.Method)
		assert.True(t, msg.IsRequest())
		assert.Equal(t, SIPMethodINVITE, msg.Method())
	}
}

func TestParseMethod_AllValidMethods(t *testing.T) {
	tests := []struct {
		input    string
		expected SIPMethod
	}{
		{"INVITE", SIPMethodINVITE},
		{"ACK", SIPMethodACK},
		{"BYE", SIPMethodBYE},
		{"CANCEL", SIPMethodCANCEL},
		{"REGISTER", SIPMethodREGISTER},
		{"OPTIONS", SIPMethodOPTIONS},
		{"PRACK", SIPMethodPRACK},
		{"SUBSCRIBE", SIPMethodSUBSCRIBE},
		{"NOTIFY", SIPMethodNOTIFY},
		{"PUBLISH", SIPMethodPUBLISH},
		{"INFO", SIPMethodINFO},
		{"REFER", SIPMethodREFER},
		{"MESSAGE", SIPMethodMESSAGE},
		{"UPDATE", SIPMethodUPDATE},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			method, err := parseMethod(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, method)
		})
	}
}

func TestParseMethod_InvalidMethods(t *testing.T) {
	tests := []string{
		"UNKNOWN",
		"",
		"invite",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			method, err := parseMethod(input)
			assert.Error(t, err)
			assert.Empty(t, method)
		})
	}
}

func TestParseCSeq_ValidCSeq(t *testing.T) {
	tests := []struct {
		input  []string
		seq    int
		method SIPMethod
	}{
		{[]string{"1 INVITE"}, 1, SIPMethodINVITE},
		{[]string{"314159 ACK"}, 314159, SIPMethodACK},
		{[]string{"42 BYE"}, 42, SIPMethodBYE},
		{[]string{"100 REGISTER"}, 100, SIPMethodREGISTER},
		{[]string{"5 OPTIONS"}, 5, SIPMethodOPTIONS},
		{[]string{"999999 CANCEL"}, 999999, SIPMethodCANCEL},
	}

	for _, tt := range tests {
		t.Run(tt.input[0], func(t *testing.T) {
			cseq, err := parseCSeq(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.seq, cseq.Seq)
			assert.Equal(t, tt.method, cseq.Method)
		})
	}
}

func TestParseCSeq_Errors(t *testing.T) {
	tests := []struct {
		input     []string
		expectErr string
	}{
		{[]string{}, "No Cseq value found"},
		{[]string{"1 INVITE", "2 BYE"}, "Multipel Cseq values found"},
		{[]string{"INVITE"}, "Invalid Cseq payload"},
		{[]string{"abc INVITE"}, "Invalid Cseq Sequence"},
		{[]string{"1 UNKNOWN"}, "Invalid Cseq Method"},
	}

	for _, tt := range tests {
		t.Run(tt.expectErr, func(t *testing.T) {
			cseq, err := parseCSeq(tt.input)
			assert.Error(t, err)
			assert.Zero(t, cseq)
			assert.Contains(t, err.Error(), tt.expectErr)
		})
	}
}

// =============================================================================
// RFC 3261 Compliance Tests
// =============================================================================
// These tests validate ParseSIP against the structural and semantic requirements
// defined in RFC 3261 (Session Initiation Protocol).
// =============================================================================

// -----------------------------------------------------------------------------
// Section 7.1: Requests
// A valid request MUST contain a Request-Line with Method SP Request-URI SP SIP-Version
// -----------------------------------------------------------------------------

func TestRFC3261_Request_LineStructure(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		method  SIPMethod
		uri     string
		version string
	}{
		{
			name:    "INVITE request",
			input:   "INVITE sip:bob@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
			method:  SIPMethodINVITE,
			uri:     "sip:bob@example.com",
			version: "SIP/2.0",
		},
		{
			name:    "ACK request",
			input:   "ACK sip:bob@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
			method:  SIPMethodACK,
			uri:     "sip:bob@example.com",
			version: "SIP/2.0",
		},
		{
			name:    "BYE request",
			input:   "BYE sip:bob@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
			method:  SIPMethodBYE,
			uri:     "sip:bob@example.com",
			version: "SIP/2.0",
		},
		{
			name:    "CANCEL request",
			input:   "CANCEL sip:bob@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
			method:  SIPMethodCANCEL,
			uri:     "sip:bob@example.com",
			version: "SIP/2.0",
		},
		{
			name:    "REGISTER request",
			input:   "REGISTER sip:registrar.example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
			method:  SIPMethodREGISTER,
			uri:     "sip:registrar.example.com",
			version: "SIP/2.0",
		},
		{
			name:    "OPTIONS request",
			input:   "OPTIONS sip:alice@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
			method:  SIPMethodOPTIONS,
			uri:     "sip:alice@example.com",
			version: "SIP/2.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ParseSIP(newTestReader(tt.input))
			if assert.NoError(t, err) {
				assert.True(t, msg.IsRequest())
				assert.Equal(t, tt.method, msg.Method())
				assert.Equal(t, tt.version, msg.Version())
				assert.Equal(t, 0, msg.StatusCode())
			}
		})
	}
}

func TestRFC3261_Request_InvalidMethods(t *testing.T) {
	tests := []string{
		"GET sip:user@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
		"POST sip:user@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
		"UNKNOWN sip:user@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n",
	}

	for _, input := range tests {
		t.Run(input[:10], func(t *testing.T) {
			msg, err := ParseSIP(newTestReader(input))
			assert.Error(t, err)
			assert.Nil(t, msg)
		})
	}
}

func TestParseSIP_ExtensionMethods(t *testing.T) {
	methods := []struct {
		input  string
		method SIPMethod
	}{
		{"PRACK", SIPMethodPRACK},
		{"SUBSCRIBE", SIPMethodSUBSCRIBE},
		{"NOTIFY", SIPMethodNOTIFY},
		{"PUBLISH", SIPMethodPUBLISH},
		{"INFO", SIPMethodINFO},
		{"REFER", SIPMethodREFER},
		{"MESSAGE", SIPMethodMESSAGE},
		{"UPDATE", SIPMethodUPDATE},
	}

	for _, tt := range methods {
		t.Run(tt.input, func(t *testing.T) {
			input := tt.input + " sip:user@example.com SIP/2.0\r\nCSeq: 1 " + tt.input + "\r\nContent-Length: 0\r\n\r\n"
			msg, err := ParseSIP(newTestReader(input))
			if assert.NoError(t, err) {
				assert.True(t, msg.IsRequest())
				assert.Equal(t, tt.method, msg.Method())
				assert.Equal(t, tt.method, msg.CSeq.Method)
				assert.Equal(t, 1, msg.CSeq.Seq)
			}
		})
	}
}

func TestRFC3261_Request_InsufficientTokens(t *testing.T) {
	tests := []string{
		"INVITE\r\nContent-Length: 0\r\n\r\n",
		"INVITE sip:user@example.com\r\nContent-Length: 0\r\n\r\n",
	}

	for _, input := range tests {
		t.Run(input[:20], func(t *testing.T) {
			msg, err := ParseSIP(newTestReader(input))
			assert.Error(t, err)
			assert.Nil(t, msg)
		})
	}
}

// -----------------------------------------------------------------------------
// Section 7.2: Responses
// A valid response MUST contain a Status-Line with SIP-Version SP Status-Code SP Reason-Phrase
// -----------------------------------------------------------------------------

func TestRFC3261_Response_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		statusCode int
	}{
		// 1xx Provisional
		{name: "100 Trying", input: "SIP/2.0 100 Trying\r\nContent-Length: 0\r\n\r\n", statusCode: 100},
		{name: "180 Ringing", input: "SIP/2.0 180 Ringing\r\nContent-Length: 0\r\n\r\n", statusCode: 180},
		{name: "181 Call Is Being Forwarded", input: "SIP/2.0 181 Call Is Being Forwarded\r\nContent-Length: 0\r\n\r\n", statusCode: 181},
		// 2xx Success
		{name: "200 OK", input: "SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n", statusCode: 200},
		// 3xx Redirection
		{name: "302 Moved Temporarily", input: "SIP/2.0 302 Moved Temporarily\r\nContent-Length: 0\r\n\r\n", statusCode: 302},
		// 4xx Client Error
		{name: "400 Bad Request", input: "SIP/2.0 400 Bad Request\r\nContent-Length: 0\r\n\r\n", statusCode: 400},
		{name: "401 Unauthorized", input: "SIP/2.0 401 Unauthorized\r\nContent-Length: 0\r\n\r\n", statusCode: 401},
		{name: "403 Forbidden", input: "SIP/2.0 403 Forbidden\r\nContent-Length: 0\r\n\r\n", statusCode: 403},
		{name: "404 Not Found", input: "SIP/2.0 404 Not Found\r\nContent-Length: 0\r\n\r\n", statusCode: 404},
		{name: "407 Proxy Authentication Required", input: "SIP/2.0 407 Proxy Authentication Required\r\nContent-Length: 0\r\n\r\n", statusCode: 407},
		{name: "408 Request Timeout", input: "SIP/2.0 408 Request Timeout\r\nContent-Length: 0\r\n\r\n", statusCode: 408},
		// 5xx Server Error
		{name: "500 Server Internal Error", input: "SIP/2.0 500 Server Internal Error\r\nContent-Length: 0\r\n\r\n", statusCode: 500},
		{name: "503 Service Unavailable", input: "SIP/2.0 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n", statusCode: 503},
		// 6xx Global Failure
		{name: "600 Busy Everywhere", input: "SIP/2.0 600 Busy Everywhere\r\nContent-Length: 0\r\n\r\n", statusCode: 600},
		{name: "603 Decline", input: "SIP/2.0 603 Decline\r\nContent-Length: 0\r\n\r\n", statusCode: 603},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ParseSIP(newTestReader(tt.input))
			if assert.NoError(t, err) {
				assert.False(t, msg.IsRequest())
				assert.Equal(t, tt.statusCode, msg.StatusCode())
				assert.Empty(t, msg.Method())
			}
		})
	}
}

func TestRFC3261_Response_InvalidStatusCodes(t *testing.T) {
	tests := []string{
		"SIP/2.0 abc OK\r\nContent-Length: 0\r\n\r\n",
		"SIP/2.0 20 OK\r\nContent-Length: 0\r\n\r\n",
		"SIP/2.0 2000 OK\r\nContent-Length: 0\r\n\r\n",
	}

	for _, input := range tests {
		t.Run(input[9:14], func(t *testing.T) {
			msg, err := ParseSIP(newTestReader(input))
			assert.Error(t, err)
			assert.Nil(t, msg)
		})
	}
}

// -----------------------------------------------------------------------------
// Section 7.3: Header Fields
// Header fields are separated by CRLF. Multiple values for the same header
// are comma-separated or appear as multiple header lines.
// -----------------------------------------------------------------------------

func TestRFC3261_Headers_MultipleValues(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nVia: SIP/2.0/UDP host1;branch=z9hG4bK1\r\nVia: SIP/2.0/UDP host2;branch=z9hG4bK2\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		via := msg.Headers.Get("Via")
		if assert.NotNil(t, via) {
			assert.Len(t, via, 2)
		}
	}
}

func TestRFC3261_Headers_CommaSeparated(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nAccept: application/sdp, text/plain\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "application/sdp, text/plain", msg.Headers.GetFirst("Accept"))
	}
}

func TestRFC3261_Headers_WhitespaceFolding(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nVia: SIP/2.0/UDP host1;\r\n branch=z9hG4bK1\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "SIP/2.0/UDP host1; branch=z9hG4bK1", msg.Headers.GetFirst("Via"))
	}
}

func TestRFC3261_Headers_CaseInsensitive(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\ncontent-length: 4\r\n\r\nbody"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, []byte("body"), msg.Body)
	}
}

// -----------------------------------------------------------------------------
// Section 7.3.3: Compact Form
// Compact headers MUST be treated identically to their long-form equivalents.
// -----------------------------------------------------------------------------

func TestRFC3261_CompactHeaders(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		key      string
		expected string
	}{
		{name: "f (From)", input: "INVITE sip:x SIP/2.0\r\nf: <sip:alice@atlanta.com>;tag=1928\r\nContent-Length: 0\r\n\r\n", key: "From", expected: "<sip:alice@atlanta.com>;tag=1928"},
		{name: "t (To)", input: "INVITE sip:x SIP/2.0\r\nt: <sip:bob@biloxi.com>\r\nContent-Length: 0\r\n\r\n", key: "To", expected: "<sip:bob@biloxi.com>"},
		{name: "v (Via)", input: "INVITE sip:x SIP/2.0\r\nv: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776\r\nContent-Length: 0\r\n\r\n", key: "Via", expected: "SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776"},
		{name: "i (Call-ID)", input: "INVITE sip:x SIP/2.0\r\ni: a84b4c76e66710@pc33.atlanta.com\r\nContent-Length: 0\r\n\r\n", key: "Call-ID", expected: "a84b4c76e66710@pc33.atlanta.com"},
		{name: "c (Content-Type)", input: "INVITE sip:x SIP/2.0\r\nc: application/sdp\r\nContent-Length: 0\r\n\r\n", key: "Content-Type", expected: "application/sdp"},
		{name: "m (Contact)", input: "INVITE sip:x SIP/2.0\r\nm: <sip:alice@pc33.atlanta.com>\r\nContent-Length: 0\r\n\r\n", key: "Contact", expected: "<sip:alice@pc33.atlanta.com>"},
		{name: "s (Subject)", input: "INVITE sip:x SIP/2.0\r\ns: Meeting\r\nContent-Length: 0\r\n\r\n", key: "Subject", expected: "Meeting"},
		{name: "k (Supported)", input: "INVITE sip:x SIP/2.0\r\nk: 100rel,timer\r\nContent-Length: 0\r\n\r\n", key: "Supported", expected: "100rel,timer"},
		{name: "e (Content-Encoding)", input: "INVITE sip:x SIP/2.0\r\ne: gzip\r\nContent-Length: 0\r\n\r\n", key: "Content-Encoding", expected: "gzip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ParseSIP(newTestReader(tt.input))
			if assert.NoError(t, err) {
				assert.Equal(t, tt.expected, msg.Headers.GetFirst(tt.key))
				_, ok := msg.Headers[strings.ToLower(tt.key)]
				assert.False(t, ok, "compact form key %s should not exist in Headers", strings.ToLower(tt.key))
				_, ok = msg.Headers[string(tt.key[0])]
				assert.False(t, ok, "compact form key %c should not exist in Headers", tt.key[0])
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Section 7.4: Message Bodies
// The body length is determined by Content-Length header.
// -----------------------------------------------------------------------------

func TestRFC3261_Body_ContentLengthHonored(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nContent-Length: 5\r\n\r\n12345EXTRA"
	msg, err := ParseSIPUDP([]byte(input))
	if assert.NoError(t, err) {
		assert.Equal(t, []byte("12345"), msg.Body)
	}
}

func TestRFC3261_Body_EmptyBody(t *testing.T) {
	input := "BYE sip:x SIP/2.0\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Empty(t, msg.Body)
	}
}

func TestRFC3261_Body_SDPBody(t *testing.T) {
	sdp := "v=0\r\no=alice 2890844526 2890844526 IN IP4 host.atlanta.com\r\ns= \r\nc=IN IP4 host.atlanta.com\r\nt=0 0\r\nm=audio 49170 RTP/AVP 0\r\n"
	input := fmt.Sprintf("INVITE sip:bob@example.com SIP/2.0\r\nContent-Length: %d\r\n\r\n%s", len(sdp), sdp)
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, []byte(sdp), msg.Body)
	}
}

// -----------------------------------------------------------------------------
// Section 7.5: Framing
// SIP messages are delimited by CRLF CRLF between headers and body.
// -----------------------------------------------------------------------------

func TestRFC3261_Framing_CRLFSeparator(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nContent-Length: 4\r\n\r\nbody"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, []byte("body"), msg.Body)
	}
}

func TestRFC3261_Framing_MissingCRLFInHeaders(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nContent-Length: 0"
	msg, err := ParseSIP(newTestReader(input))
	assert.Error(t, err)
	assert.Nil(t, msg)
}

// -----------------------------------------------------------------------------
// Section 8.1: UAC Behavior - Mandatory Headers
// A valid SIP request MUST contain: To, From, CSeq, Call-ID, Max-Forwards, Via
// Note: Parser does not validate mandatory headers (application-level concern)
// but must correctly parse them when present.
// -----------------------------------------------------------------------------

func TestRFC3261_MandatoryHeaders_AllPresent(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776\r\n" +
		"Max-Forwards: 70\r\n" +
		"To: Bob <sip:bob@example.com>\r\n" +
		"From: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"

	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.True(t, msg.IsRequest())
		assert.Equal(t, SIPMethodINVITE, msg.Method())
		assert.Equal(t, 314159, msg.CSeq.Seq)
		assert.Equal(t, SIPMethodINVITE, msg.CSeq.Method)
	}
}

// -----------------------------------------------------------------------------
// Section 20.16: CSeq
// The CSeq header consists of a sequence number and a method.
// The method MUST match that of the request.
// -----------------------------------------------------------------------------

func TestRFC3261_CSeq_MethodMatchesRequest(t *testing.T) {
	input := "BYE sip:bob@example.com SIP/2.0\r\nCSeq: 100 BYE\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, SIPMethodBYE, msg.CSeq.Method)
		assert.Equal(t, 100, msg.CSeq.Seq)
	}
}

func TestRFC3261_CSeq_LargeSequenceNumber(t *testing.T) {
	max32 := 2147483647
	input := fmt.Sprintf("INVITE sip:x SIP/2.0\r\nCSeq: %d INVITE\r\nContent-Length: 0\r\n\r\n", max32)
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, max32, msg.CSeq.Seq)
	}
}

func TestRFC3261_CSeq_InResponse(t *testing.T) {
	input := "SIP/2.0 200 OK\r\nCSeq: 5 REGISTER\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, SIPMethodREGISTER, msg.CSeq.Method)
		assert.Equal(t, 5, msg.CSeq.Seq)
	}
}

// -----------------------------------------------------------------------------
// RFC 3261 §7.3.1: Header Field Format — empty values, missing colon
// -----------------------------------------------------------------------------

func TestRFC3261_Header_EmptyValue(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nCall-ID:\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "", msg.Headers.GetFirst("Call-ID"))
	}
}

func TestRFC3261_Header_MissingColon(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nHeaderWithoutColon\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	assert.Error(t, err)
	assert.Nil(t, msg)
}

// -----------------------------------------------------------------------------
// RFC 3261 §7.3.2: Header Folding — tab continuation, multiple folds
// -----------------------------------------------------------------------------

func TestRFC3261_Header_TabContinuation(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nVia: SIP/2.0/UDP host1;\r\n\tbranch=z9hG4bK1\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "SIP/2.0/UDP host1;\tbranch=z9hG4bK1", msg.Headers.GetFirst("Via"))
	}
}

func TestRFC3261_Header_MultipleContinuations(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nSubject: a\r\n b\r\n c\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "a b c", msg.Headers.GetFirst("Subject"))
	}
}

// -----------------------------------------------------------------------------
// RFC 3261 §7.3.1: Multiple LWS after colon
// -----------------------------------------------------------------------------

func TestRFC3261_Header_MultipleSpacesAfterColon(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nContact:   <sip:alice@atlanta.com>\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "<sip:alice@atlanta.com>", msg.Headers.GetFirst("Contact"))
	}
}

// -----------------------------------------------------------------------------
// RFC 3261 §20.14: Content-Length MUST NOT appear more than once
// -----------------------------------------------------------------------------

func TestRFC3261_DuplicateContentLength(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nContent-Length: 4\r\nContent-Length: 5\r\n\r\nbody"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, []byte("body"), msg.Body)
	}
}

// -----------------------------------------------------------------------------
// RFC 3261 §7.1: SIPS URI and URI parameters in Request-URI
// -----------------------------------------------------------------------------

func TestRFC3261_SIPSRequest(t *testing.T) {
	input := "INVITE sips:bob@example.com SIP/2.0\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.True(t, msg.IsRequest())
		assert.Equal(t, SIPMethodINVITE, msg.Method())
		assert.Contains(t, msg.StartLine(), "sips:bob@example.com")
	}
}

func TestRFC3261_Request_URIParameters(t *testing.T) {
	input := "INVITE sip:bob@example.com;lr;transport=tcp SIP/2.0\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.True(t, msg.IsRequest())
		assert.Equal(t, SIPMethodINVITE, msg.Method())
		assert.Contains(t, msg.StartLine(), "sip:bob@example.com;lr;transport=tcp")
	}
}

// -----------------------------------------------------------------------------
// RFC 3261 §7.2: Response with reason phrase containing spaces
// -----------------------------------------------------------------------------

func TestRFC3261_Response_MultiWordReason(t *testing.T) {
	input := "SIP/2.0 181 Call Is Being Forwarded\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		assert.False(t, msg.IsRequest())
		assert.Equal(t, 181, msg.StatusCode())
		assert.Equal(t, "181 Call Is Being Forwarded", msg.Status())
	}
}

func TestSIPMessage_From_Request(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"From: \"Alice\" <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"To: Bob <sip:bob@biloxi.com>\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := msg.From()
		if assert.NoError(t, err) {
			assert.Equal(t, "Alice", from.DisplayName)
			assert.Equal(t, "sip:alice@atlanta.com", from.URI)
			assert.Equal(t, "1928301774", from.Tag)
		}
	}
}

func TestSIPMessage_From_AddrSpec(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"From: sip:alice@atlanta.com;tag=abc\r\n" +
		"To: <sip:bob@biloxi.com>\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := msg.From()
		if assert.NoError(t, err) {
			assert.Empty(t, from.DisplayName)
			assert.Equal(t, "sip:alice@atlanta.com", from.URI)
			assert.Equal(t, "abc", from.Tag)
		}
	}
}

func TestSIPMessage_To_Response(t *testing.T) {
	input := "SIP/2.0 200 OK\r\n" +
		"From: \"Alice\" <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"To: \"Bob\" <sip:bob@biloxi.com>;tag=a6c85cf\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		to, err := msg.To()
		if assert.NoError(t, err) {
			assert.Equal(t, "Bob", to.DisplayName)
			assert.Equal(t, "sip:bob@biloxi.com", to.URI)
			assert.Equal(t, "a6c85cf", to.Tag)
		}
	}
}

func TestSIPMessage_FromTo_NoDisplayName(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"From: <sip:alice@atlanta.com>;tag=abc\r\n" +
		"To: <sip:bob@biloxi.com>;tag=def\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := msg.From()
		if assert.NoError(t, err) {
			assert.Empty(t, from.DisplayName)
			assert.Equal(t, "sip:alice@atlanta.com", from.URI)
			assert.Equal(t, "abc", from.Tag)
		}
		to, err := msg.To()
		if assert.NoError(t, err) {
			assert.Empty(t, to.DisplayName)
			assert.Equal(t, "sip:bob@biloxi.com", to.URI)
			assert.Equal(t, "def", to.Tag)
		}
	}
}

func TestSIPMessage_From_MissingFrom(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"To: <sip:bob@biloxi.com>\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := msg.From()
		assert.Error(t, err)
		assert.Nil(t, from)
	}
}

func TestSIPMessage_To_MissingTo(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"From: <sip:alice@atlanta.com>\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		to, err := msg.To()
		assert.Error(t, err)
		assert.Nil(t, to)
	}
}

func TestSIPMessage_From_CompactForm(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"f: \"Alice\" <sip:alice@atlanta.com>;tag=abc\r\n" +
		"t: \"Bob\" <sip:bob@biloxi.com>;tag=def\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := msg.From()
		if assert.NoError(t, err) {
			assert.Equal(t, "Alice", from.DisplayName)
			assert.Equal(t, "abc", from.Tag)
		}
		to, err := msg.To()
		if assert.NoError(t, err) {
			assert.Equal(t, "Bob", to.DisplayName)
			assert.Equal(t, "def", to.Tag)
		}
	}
}

func TestSIPMessage_From_Anonymous(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"From: Anonymous <sip:c8oqz84zk7z@privacy.org>;tag=hyh8\r\n" +
		"To: <sip:bob@biloxi.com>\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := msg.From()
		if assert.NoError(t, err) {
			assert.Equal(t, "Anonymous", from.DisplayName)
			assert.Equal(t, "sip:c8oqz84zk7z@privacy.org", from.URI)
			assert.Equal(t, "hyh8", from.Tag)
		}
	}
}

func TestSIPMessage_FromTo_SIPSURI(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"From: <sips:alice@atlanta.com>;tag=abc\r\n" +
		"To: <sips:bob@biloxi.com>;tag=def\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := msg.From()
		if assert.NoError(t, err) {
			assert.Equal(t, "sips:alice@atlanta.com", from.URI)
		}
		to, err := msg.To()
		if assert.NoError(t, err) {
			assert.Equal(t, "sips:bob@biloxi.com", to.URI)
		}
	}
}

func TestSIPMessage_FromTo_URIParams(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"From: <sip:alice@atlanta.com;lr>;tag=abc\r\n" +
		"To: <sip:bob@biloxi.com;transport=tcp>;tag=def\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := msg.From()
		if assert.NoError(t, err) {
			assert.Equal(t, "sip:alice@atlanta.com;lr", from.URI)
			assert.Equal(t, "abc", from.Tag)
		}
		to, err := msg.To()
		if assert.NoError(t, err) {
			assert.Equal(t, "sip:bob@biloxi.com;transport=tcp", to.URI)
			assert.Equal(t, "def", to.Tag)
		}
	}
}

func TestParseSIPAddress_NameAddrWithDisplayName(t *testing.T) {
	ft, err := ParseSIPAddress(`"Bob" <sip:bob@biloxi.com>;tag=a48s`)
	if assert.NoError(t, err) {
		assert.Equal(t, "Bob", ft.DisplayName)
		assert.Equal(t, "sip:bob@biloxi.com", ft.URI)
		assert.Equal(t, "a48s", ft.Tag)
	}
}

func TestParseSIPAddress_NameAddrWithoutDisplayName(t *testing.T) {
	ft, err := ParseSIPAddress(`<sip:bob@biloxi.com>;tag=a48s`)
	if assert.NoError(t, err) {
		assert.Empty(t, ft.DisplayName)
		assert.Equal(t, "sip:bob@biloxi.com", ft.URI)
		assert.Equal(t, "a48s", ft.Tag)
	}
}

func TestParseSIPAddress_NameAddrWithoutTag(t *testing.T) {
	ft, err := ParseSIPAddress(`<sip:alice@atlanta.com>`)
	if assert.NoError(t, err) {
		assert.Empty(t, ft.DisplayName)
		assert.Equal(t, "sip:alice@atlanta.com", ft.URI)
		assert.Empty(t, ft.Tag)
	}
}

func TestParseSIPAddress_AddrSpec(t *testing.T) {
	ft, err := ParseSIPAddress(`sip:alice@atlanta.com;tag=887s`)
	if assert.NoError(t, err) {
		assert.Empty(t, ft.DisplayName)
		assert.Equal(t, "sip:alice@atlanta.com", ft.URI)
		assert.Equal(t, "887s", ft.Tag)
	}
}

func TestParseSIPAddress_AddrSpecWithoutTag(t *testing.T) {
	ft, err := ParseSIPAddress(`sip:alice@atlanta.com`)
	if assert.NoError(t, err) {
		assert.Empty(t, ft.DisplayName)
		assert.Equal(t, "sip:alice@atlanta.com", ft.URI)
		assert.Empty(t, ft.Tag)
	}
}

func TestParseSIPAddress_Anonymous(t *testing.T) {
	ft, err := ParseSIPAddress(`Anonymous <sip:c8oqz84zk7z@privacy.org>;tag=hyh8`)
	if assert.NoError(t, err) {
		assert.Equal(t, "Anonymous", ft.DisplayName)
		assert.Equal(t, "sip:c8oqz84zk7z@privacy.org", ft.URI)
		assert.Equal(t, "hyh8", ft.Tag)
	}
}

func TestParseSIPAddress_NameAddrWithURIParams(t *testing.T) {
	ft, err := ParseSIPAddress(`"Alice" <sip:alice@atlanta.com;lr>;tag=abc`)
	if assert.NoError(t, err) {
		assert.Equal(t, "Alice", ft.DisplayName)
		assert.Equal(t, "sip:alice@atlanta.com;lr", ft.URI)
		assert.Equal(t, "abc", ft.Tag)
	}
}

func TestParseSIPAddress_DisplayNameWithSpaces(t *testing.T) {
	ft, err := ParseSIPAddress(`"Bob Smith" <sip:bob@example.com>`)
	if assert.NoError(t, err) {
		assert.Equal(t, "Bob Smith", ft.DisplayName)
		assert.Equal(t, "sip:bob@example.com", ft.URI)
		assert.Empty(t, ft.Tag)
	}
}

func TestParseSIPAddress_EmptyString(t *testing.T) {
	ft, err := ParseSIPAddress("")
	assert.Error(t, err)
	assert.Nil(t, ft)
}

func TestParseSIPAddress_SIPSURI(t *testing.T) {
	ft, err := ParseSIPAddress(`<sips:bob@biloxi.com>;tag=abc`)
	if assert.NoError(t, err) {
		assert.Empty(t, ft.DisplayName)
		assert.Equal(t, "sips:bob@biloxi.com", ft.URI)
		assert.Equal(t, "abc", ft.Tag)
	}
}

func TestParseSIPAddress_MissingClosingBracket(t *testing.T) {
	ft, err := ParseSIPAddress(`<sip:bob@biloxi.com`)
	assert.Error(t, err)
	assert.Nil(t, ft)
}

func TestParseSIPAddress_CaseInsensitiveLookup(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nf: <sip:alice@atlanta.com>;tag=1928\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		ft, err := ParseSIPAddress(msg.Headers.GetFirst("From"))
		if assert.NoError(t, err) {
			assert.Equal(t, "sip:alice@atlanta.com", ft.URI)
			assert.Equal(t, "1928", ft.Tag)
		}
	}
}

func TestParseSIPAddress_ExtractFromSIPMessage(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"From: \"Alice\" <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"To: \"Bob\" <sip:bob@biloxi.com>;tag=abc\r\n" +
		"CSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if assert.NoError(t, err) {
		from, err := ParseSIPAddress(msg.Headers.GetFirst("From"))
		if assert.NoError(t, err) {
			assert.Equal(t, "Alice", from.DisplayName)
			assert.Equal(t, "sip:alice@atlanta.com", from.URI)
			assert.Equal(t, "1928301774", from.Tag)
		}

		to, err := ParseSIPAddress(msg.Headers.GetFirst("To"))
		if assert.NoError(t, err) {
			assert.Equal(t, "Bob", to.DisplayName)
			assert.Equal(t, "sip:bob@biloxi.com", to.URI)
			assert.Equal(t, "abc", to.Tag)
		}
	}
}

func TestParseSIPAddress_AddrSpecWithURIParams(t *testing.T) {
	ft, err := ParseSIPAddress(`sip:alice@atlanta.com;lr;tag=abc`)
	if assert.NoError(t, err) {
		assert.Equal(t, "sip:alice@atlanta.com;lr", ft.URI)
		assert.Equal(t, "abc", ft.Tag)
	}
}

func TestHeaders_GetFirst_Exists(t *testing.T) {
	h := SIPHeaders{"Content-Length": {"150"}}
	assert.Equal(t, "150", h.GetFirst("Content-Length"))
}

func TestHeaders_GetFirst_CanonicalizesKey(t *testing.T) {
	h := SIPHeaders{"Content-Length": {"150"}}
	assert.Equal(t, "150", h.GetFirst("content-length"))
	assert.Equal(t, "150", h.GetFirst("CONTENT-LENGTH"))
}

func TestHeaders_GetFirst_Missing(t *testing.T) {
	h := SIPHeaders{"Via": {"SIP/2.0/UDP host"}}
	assert.Equal(t, "", h.GetFirst("From"))
}

func TestHeaders_Get_AllValues(t *testing.T) {
	h := SIPHeaders{"Via": {"SIP/2.0/UDP a", "SIP/2.0/UDP b"}}
	values := h.Get("Via")
	if assert.NotNil(t, values) {
		assert.Len(t, values, 2)
		assert.Equal(t, []string{"SIP/2.0/UDP a", "SIP/2.0/UDP b"}, values)
	}
}

func TestHeaders_Get_Missing(t *testing.T) {
	h := SIPHeaders{}
	assert.Nil(t, h.Get("Call-ID"))
}

func TestHeaders_Set_AddsNewKey(t *testing.T) {
	h := make(SIPHeaders)
	h.Set("From", []string{"<sip:alice@atlanta.com>"})
	assert.Equal(t, "<sip:alice@atlanta.com>", h.GetFirst("From"))
}

func TestHeaders_Set_CanonicalizesKey(t *testing.T) {
	h := make(SIPHeaders)
	h.Set("from", []string{"<sip:alice@atlanta.com>"})
	assert.Equal(t, "<sip:alice@atlanta.com>", h.GetFirst("From"))
}

func TestHeaders_Set_OverwritesExisting(t *testing.T) {
	h := SIPHeaders{"Via": {"SIP/2.0/UDP old"}}
	h.Set("Via", []string{"SIP/2.0/UDP new"})
	assert.Equal(t, []string{"SIP/2.0/UDP new"}, h.Get("Via"))
}

func TestHeaders_Add_NewKeyCreatesSlice(t *testing.T) {
	h := make(SIPHeaders)
	h.Add("Via", "SIP/2.0/UDP first")
	assert.Equal(t, []string{"SIP/2.0/UDP first"}, h.Get("Via"))
}

func TestHeaders_Add_AppendsToExisting(t *testing.T) {
	h := SIPHeaders{"Via": {"SIP/2.0/UDP first"}}
	h.Add("Via", "SIP/2.0/UDP second")
	values := h.Get("Via")
	if assert.NotNil(t, values) {
		assert.Len(t, values, 2)
		assert.Equal(t, "SIP/2.0/UDP second", values[1])
	}
}

func TestHeaders_Add_CanonicalizesKey(t *testing.T) {
	h := make(SIPHeaders)
	h.Add("via", "SIP/2.0/UDP host")
	assert.Equal(t, "SIP/2.0/UDP host", h.GetFirst("Via"))
}

func TestHeaders_Add_MultipleSeparateKeys(t *testing.T) {
	h := make(SIPHeaders)
	h.Add("Via", "SIP/2.0/UDP a")
	h.Add("Route", "<sip:proxy>")
	h.Add("From", "<sip:alice>")
	assert.Equal(t, []string{"SIP/2.0/UDP a"}, h.Get("Via"))
	assert.Equal(t, []string{"<sip:proxy>"}, h.Get("Route"))
	assert.Equal(t, []string{"<sip:alice>"}, h.Get("From"))
}

func TestHeaders_GetFirst_EmptyValue(t *testing.T) {
	h := SIPHeaders{"Content-Length": {""}}
	assert.Equal(t, "", h.GetFirst("Content-Length"))
}

func TestHeaders_GetFirst_MultipleValues(t *testing.T) {
	h := SIPHeaders{"Via": {"first", "second"}}
	assert.Equal(t, "first", h.GetFirst("Via"))
}

func TestHeaders_Get_NilOnEmptyMap(t *testing.T) {
	h := make(SIPHeaders)
	assert.Nil(t, h.Get("NonExistent"))
}

func TestParseHeaders_ExpandsCompactForm(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nf: <sip:alice@atlanta.com>;tag=1928\r\nContent-Length: 0\r\n\r\n"
	tp := newTestReader(input)
	msg, err := ParseSIP(tp)
	if assert.NoError(t, err) {
		assert.Equal(t, "<sip:alice@atlanta.com>;tag=1928", msg.Headers.GetFirst("From"))
		_, ok := msg.Headers["From"]
		assert.True(t, ok)
		_, ok = msg.Headers["f"]
		assert.False(t, ok)
		_, ok = msg.Headers["F"]
		assert.False(t, ok)
	}
}

func TestParseHeaders_PassesThroughUnknownKeys(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nX-Custom: custom-value\r\nContent-Length: 0\r\n\r\n"
	tp := newTestReader(input)
	msg, err := ParseSIP(tp)
	if assert.NoError(t, err) {
		assert.Equal(t, "custom-value", msg.Headers.GetFirst("X-Custom"))
	}
}

func TestParseHeaders_CanonicalizesMixedCaseLongForm(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\nvia: SIP/2.0/UDP host\r\nContent-Length: 0\r\n\r\n"
	tp := newTestReader(input)
	msg, err := ParseSIP(tp)
	if assert.NoError(t, err) {
		assert.Equal(t, "SIP/2.0/UDP host", msg.Headers.GetFirst("Via"))
	}
}

func TestParseHeaders_EmptyHeaders(t *testing.T) {
	input := "INVITE sip:x SIP/2.0\r\n\r\n"
	tp := newTestReader(input)
	msg, err := ParseSIP(tp)
	if assert.NoError(t, err) {
		assert.Len(t, msg.Headers, 0)
	}
}

// ---------------------------------------------------------------------------
// Marshal / MarshalSize / MarshalTo / String
// ---------------------------------------------------------------------------

func TestSIPMarshal_RequestRoundTrip(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776\r\n" +
		"Max-Forwards: 70\r\n" +
		"To: Bob <sip:bob@example.com>\r\n" +
		"From: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Content-Length: 4\r\n" +
		"\r\n" +
		"body"

	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}

	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}

	// Re-parse to verify round-trip semantic equivalence
	// (header order is normalized during marshal)
	msg2, err := ParseSIP(newTestReader(string(got)))
	if assert.NoError(t, err) {
		assert.Equal(t, msg.StartLine(), msg2.StartLine())
		assert.Equal(t, msg.Headers.Get("Via"), msg2.Headers.Get("Via"))
		assert.Equal(t, msg.Headers.GetFirst("Max-Forwards"), msg2.Headers.GetFirst("Max-Forwards"))
		assert.Equal(t, msg.Headers.GetFirst("To"), msg2.Headers.GetFirst("To"))
		assert.Equal(t, msg.Headers.GetFirst("From"), msg2.Headers.GetFirst("From"))
		assert.Equal(t, msg.Headers.GetFirst("Call-ID"), msg2.Headers.GetFirst("Call-ID"))
		assert.Equal(t, msg.CSeq, msg2.CSeq)
		assert.Equal(t, msg.Body, msg2.Body)
	}

	buf := make([]byte, msg.MarshalSize())
	n, err := msg.MarshalTo(buf)
	if assert.NoError(t, err) {
		assert.Equal(t, len(got), n)
		assert.Equal(t, string(got), string(buf[:n]))
	}

	assert.Equal(t, string(got), msg.String())
}

func TestSIPMarshal_ResponseRoundTrip(t *testing.T) {
	input := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP server.example.com;branch=z9hG4bK9a8b\r\n" +
		"From: Alice <sip:alice@example.com>;tag=abc\r\n" +
		"To: Bob <sip:bob@example.com>;tag=def\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n"

	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}

	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}

	msg2, err := ParseSIP(newTestReader(string(got)))
	if assert.NoError(t, err) {
		assert.Equal(t, msg.StartLine(), msg2.StartLine())
		assert.Equal(t, msg.Headers.Get("Via"), msg2.Headers.Get("Via"))
		assert.Equal(t, msg.Headers.GetFirst("From"), msg2.Headers.GetFirst("From"))
		assert.Equal(t, msg.Headers.GetFirst("To"), msg2.Headers.GetFirst("To"))
		assert.Equal(t, msg.Headers.GetFirst("Call-ID"), msg2.Headers.GetFirst("Call-ID"))
		assert.Equal(t, msg.CSeq, msg2.CSeq)
		assert.Equal(t, msg.Body, msg2.Body)
	}
}

func TestSIPMarshal_IgnoreStaleCSeqInHeaders(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}
	// mutate CSeq; Headers["CSeq"] still holds stale "1 INVITE"
	msg.CSeq.Seq = 999

	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		assert.Contains(t, string(got), "CSeq: 999 INVITE")
		assert.NotContains(t, string(got), "CSeq: 1 INVITE")
	}
}

func TestSIPMarshal_IgnoreStaleContentLengthInHeaders(t *testing.T) {
	input := "INVITE sip:bob@example.com SIP/2.0\r\nContent-Length: 4\r\n\r\nbody"
	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}
	// mutate body; Headers["Content-Length"] still holds stale "4"
	msg.Body = []byte("newbody")

	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		assert.Contains(t, string(got), "Content-Length: 7")
		assert.NotContains(t, string(got), "Content-Length: 4")
	}
}

func TestSIPMarshal_MarshalSize(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true,
			Method:    "INVITE",
			URI:       "sip:bob@example.com",
			Version:   "SIP/2.0",
		},
		Headers: SIPHeaders{
			"From":    {"<sip:alice@atlanta.com>;tag=abc"},
			"To":      {"<sip:bob@biloxi.com>"},
			"Call-ID": {"a84b4c76e66710@pc33.atlanta.com"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
		Body: []byte("v=0\r\no=user 1"),
	}
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}

	// re-parse and verify
	msg2, err := ParseSIP(newTestReader(string(got)))
	if assert.NoError(t, err) {
		assert.Equal(t, "INVITE sip:bob@example.com SIP/2.0", msg2.StartLine())
		assert.Equal(t, "<sip:alice@atlanta.com>;tag=abc", msg2.Headers.GetFirst("From"))
		assert.Equal(t, 1, msg2.CSeq.Seq)
		assert.Equal(t, "INVITE", string(msg2.CSeq.Method))
		assert.Equal(t, []byte("v=0\r\no=user 1"), msg2.Body)
	}
}

func TestSIPMarshal_NilStartLine(t *testing.T) {
	msg := &SIPMessage{}
	assert.Equal(t, 0, msg.MarshalSize())
	got, err := msg.Marshal()
	assert.NoError(t, err)
	assert.Empty(t, got)
}

func TestSIPMarshal_BufferTooSmall(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{IsRequest: true, Method: "INVITE", URI: "sip:x", Version: "SIP/2.0"},
	}
	buf := make([]byte, 1)
	_, err := msg.MarshalTo(buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "buffer too small")
}

func TestSIPMarshal_DeterministicHeaderOrder(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{IsRequest: true, Method: "INVITE", URI: "sip:x", Version: "SIP/2.0"},
		Headers: SIPHeaders{
			"Z-Last":  {"z"},
			"From":    {"<sip:a>"},
			"To":      {"<sip:b>"},
			"Subject": {"hello"},
			"Call-ID": {"id"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	// The output should have CSeq and Content-Length at the end,
	// and the user headers should be in sorted order.
	expected := "INVITE sip:x SIP/2.0\r\n" +
		"Call-ID: id\r\n" +
		"From: <sip:a>\r\n" +
		"Subject: hello\r\n" +
		"To: <sip:b>\r\n" +
		"Z-Last: z\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	assert.Equal(t, expected, string(got))
}

// =============================================================================
// RFC 3261 Strict Compliance — SIP Message Marshalling
// =============================================================================

// -----------------------------------------------------------------------------
// Section 7: Request-Line = Method SP Request-URI SP SIP-Version CRLF
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_RequestLineGrammar(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true,
			Method:    "INVITE",
			URI:       "sip:bob@atlanta.com",
			Version:   "SIP/2.0",
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	// INVITE sip:bob@atlanta.com SIP/2.0\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n
	firstCRLF := strings.Index(string(got), "\r\n")
	if assert.Greater(t, firstCRLF, 0) {
		requestLine := string(got[:firstCRLF]) // before CRLF
		parts := strings.Split(requestLine, " ")
		if assert.Len(t, parts, 3) {
			assert.Equal(t, "INVITE", parts[0], "Method token")
			assert.Equal(t, "sip:bob@atlanta.com", parts[1], "Request-URI")
			assert.Equal(t, "SIP/2.0", parts[2], "SIP-Version")
		}
	}
	assert.Equal(t, byte('\r'), got[firstCRLF], "carriage return before linefeed")
	assert.Equal(t, byte('\n'), got[firstCRLF+1], "linefeed")
}

func TestRFC3261_Marshal_RequestLine_MultipleMethods(t *testing.T) {
	methods := []SIPMethod{
		SIPMethodINVITE, SIPMethodACK, SIPMethodBYE, SIPMethodCANCEL,
		SIPMethodREGISTER, SIPMethodOPTIONS, SIPMethodPRACK,
		SIPMethodSUBSCRIBE, SIPMethodNOTIFY, SIPMethodPUBLISH,
		SIPMethodINFO, SIPMethodREFER, SIPMethodMESSAGE, SIPMethodUPDATE,
	}
	for _, method := range methods {
		t.Run(string(method), func(t *testing.T) {
			msg := &SIPMessage{
				startLine: &startLine{
					IsRequest: true,
					Method:    method,
					URI:       "sip:user@host",
					Version:   "SIP/2.0",
				},
				CSeq: CSeq{Seq: 1, Method: method},
			}
			got, err := msg.Marshal()
			if !assert.NoError(t, err) {
				return
			}
			firstCRLF := strings.Index(string(got), "\r\n")
			requestLine := string(got[:firstCRLF])
			parts := strings.Split(requestLine, " ")
			if assert.Len(t, parts, 3) {
				assert.Equal(t, string(method), parts[0])
				assert.Equal(t, "sip:user@host", parts[1])
				assert.Equal(t, "SIP/2.0", parts[2])
			}
			// CSeq method must match start-line method
			assert.Contains(t, string(got), "CSeq: 1 "+string(method))
		})
	}
}

func TestRFC3261_Marshal_RequestLine_URIParameters(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true,
			Method:    "INVITE",
			URI:       "sip:bob@atlanta.com;lr;transport=tcp",
			Version:   "SIP/2.0",
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		assert.Contains(t, string(got), "INVITE sip:bob@atlanta.com;lr;transport=tcp SIP/2.0\r\n")
	}
}

func TestRFC3261_Marshal_RequestLine_SIPSURI(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true,
			Method:    "INVITE",
			URI:       "sips:bob@atlanta.com",
			Version:   "SIP/2.0",
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		assert.Contains(t, string(got), "INVITE sips:bob@atlanta.com SIP/2.0\r\n")
	}
}

// -----------------------------------------------------------------------------
// Section 7: Status-Line = SIP-Version SP Status-Code SP Reason-Phrase CRLF
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_StatusLineGrammar(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: false,
			Version:   "SIP/2.0",
			StatusCode: 200,
			Status:    "200 OK",
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	firstCRLF := strings.Index(string(got), "\r\n")
	if assert.Greater(t, firstCRLF, 0) {
		statusLine := string(got[:firstCRLF])
		parts := strings.SplitN(statusLine, " ", 3)
		if assert.Len(t, parts, 3) {
			assert.Equal(t, "SIP/2.0", parts[0], "SIP-Version")
			assert.Equal(t, "200", parts[1], "Status-Code (3 DIGIT)")
			assert.Equal(t, "OK", parts[2], "Reason-Phrase")
			assert.Len(t, parts[1], 3, "Status-Code must be exactly 3 digits")
		}
	}
}

func TestRFC3261_Marshal_StatusLine_VariousCodes(t *testing.T) {
	codes := []int{100, 180, 200, 302, 400, 404, 500, 503, 600}
	for _, code := range codes {
		t.Run(fmt.Sprintf("%d", code), func(t *testing.T) {
			reason, _ := rfc3261ReasonPhrase(code)
			msg := &SIPMessage{
				startLine: &startLine{
					IsRequest: false,
					Version:   "SIP/2.0",
					StatusCode: code,
					Status:    fmt.Sprintf("%d %s", code, reason),
				},
				CSeq: CSeq{Seq: 1, Method: "INVITE"},
			}
			got, err := msg.Marshal()
			if !assert.NoError(t, err) {
				return
			}
			firstCRLF := strings.Index(string(got), "\r\n")
			statusLine := string(got[:firstCRLF])
			parts := strings.SplitN(statusLine, " ", 3)
			if assert.Len(t, parts, 3) {
				assert.Equal(t, fmt.Sprintf("%d", code), parts[1])
				assert.Len(t, parts[1], 3)
				assert.Equal(t, reason, parts[2], "Reason-Phrase for %d", code)
			}
		})
	}
}

func TestRFC3261_Marshal_StatusLine_MultiWordReason(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: false,
			Version:   "SIP/2.0",
			StatusCode: 181,
			Status:    "181 Call Is Being Forwarded",
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		firstCRLF := strings.Index(string(got), "\r\n")
		statusLine := string(got[:firstCRLF])
		assert.Equal(t, "SIP/2.0 181 Call Is Being Forwarded", statusLine)
	}
}

// -----------------------------------------------------------------------------
// Section 7.3.1: message-header = field-name ":" SP field-value CRLF
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_HeaderFormat(t *testing.T) {
	msg := sipBenchRequest()
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	// Split headers from body at the first empty line (body separator)
	s := string(got)
	headerEnd := strings.Index(s, "\r\n\r\n")
	if !assert.Greater(t, headerEnd, 0) {
		return
	}
	headerSection := s[:headerEnd]
	lines := strings.Split(headerSection, "\r\n")
	// Line 0 = start-line, skip it
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		colonIdx := strings.IndexByte(line, ':')
		if assert.Greater(t, colonIdx, 0, "header %q must contain colon", line) {
			assert.Equal(t, byte(' '), line[colonIdx+1], "SP after colon in %q", line)
			// Verify header name contains only token chars (alphanumeric + - _ . ! % * ` ' #)
			fieldName := line[:colonIdx]
			for _, c := range fieldName {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
					c >= '0' && c <= '9' || c == '-' || c == '_' ||
					c == '.' || c == '!' || c == '%' || c == '*' ||
					c == '\'' || c == '`' || c == '#') {
					t.Errorf("header name %q contains invalid token char %q", fieldName, c)
				}
			}
		}
	}
}

func TestRFC3261_Marshal_Header_CanonicalForms(t *testing.T) {
	msg := sipBenchRequest()
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	s := string(got)
	// All registered SIP header canonical forms
	assert.Contains(t, s, "Via: ")
	assert.Contains(t, s, "Max-Forwards: ")
	assert.Contains(t, s, "To: ")
	assert.Contains(t, s, "From: ")
	assert.Contains(t, s, "Call-ID: ")
	assert.Contains(t, s, "CSeq: ")
	assert.Contains(t, s, "Contact: ")
	assert.Contains(t, s, "Content-Type: ")
	assert.Contains(t, s, "Content-Length: ")

	// No compact-form header lines (check each line's prefix)
	headerEnd := strings.Index(s, "\r\n\r\n")
	if headerEnd > 0 {
		for _, line := range strings.Split(s[:headerEnd], "\r\n") {
			if line == "" || strings.HasPrefix(line, "SIP/") || !strings.Contains(line, ":") {
				continue
			}
			fieldName, _, _ := strings.Cut(line, ":")
			fieldName = strings.TrimSpace(fieldName)
			if compactKey, isCompact := compactHeaders[fieldName]; isCompact {
				t.Errorf("compact header form %q found (long form is %q)", fieldName, compactKey)
			}
		}
	}

	// No lowercase/raw original forms in header lines
	for _, line := range strings.Split(s[:headerEnd], "\r\n") {
		name, _, _ := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if name != "" && !strings.HasPrefix(line, "SIP/") {
			want := canonicalHeaderKey(name)
			if name != want {
				t.Errorf("header name %q is not canonical (should be %q)", name, want)
			}
		}
	}
}

func TestRFC3261_Marshal_CRLFOnly(t *testing.T) {
	msg := sipBenchRequest()
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	// No bare LF (every \r must be followed by \n; every \n must be preceded by \r)
	lfCount := 0
	crCount := 0
	for i := 0; i < len(got); i++ {
		switch got[i] {
		case '\r':
			crCount++
			assert.Less(t, i+1, len(got), "CR at final byte must not be bare")
			if i+1 < len(got) {
				assert.Equal(t, byte('\n'), got[i+1], "CR at byte %d must be followed by LF", i)
			}
		case '\n':
			lfCount++
			assert.Greater(t, i, 0, "LF at byte 0 must not be bare")
			if i > 0 {
				assert.Equal(t, byte('\r'), got[i-1], "LF at byte %d must be preceded by CR", i)
			}
		}
	}
	assert.Equal(t, crCount, lfCount, "CR and LF counts must match")
}

// -----------------------------------------------------------------------------
// Section 7.5: Message body — CRLF separator, Content-Length accuracy
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_BodySeparator(t *testing.T) {
	msg := sipBenchRequest()
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	// The body separator is a CRLF on its own after the last header
	// Structure: ...Content-Length: 137\r\n\r\nv=0\r\n...
	clLine := "Content-Length: 137\r\n"
	clIdx := strings.Index(string(got), clLine)
	if assert.Greater(t, clIdx, 0) {
		sepStart := clIdx + len(clLine)
		if sepStart+1 < len(got) {
			assert.Equal(t, "\r\n", string(got[sepStart:sepStart+2]),
				"body separator must be CRLF")
			// The body follows the separator CRLF
			bodyStart := sepStart + 2
			assert.Equal(t, "v=0\r\n", string(got[bodyStart:bodyStart+5]),
				"body follows separator")
		}
	}
}

func TestRFC3261_Marshal_ContentLengthAccuracy(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"empty", nil},
		{"empty_slice", []byte{}},
		{"four_bytes", []byte("body")},
		{"sdp", []byte("v=0\r\no=user 1\r\n")},
		{"binary", []byte{0x00, 0x01, 0x02, 0xFF}},
		{"large", []byte("x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &SIPMessage{
				startLine: &startLine{
					IsRequest: true, Method: "INVITE",
					URI: "sip:x", Version: "SIP/2.0",
				},
				CSeq: CSeq{Seq: 1, Method: "INVITE"},
				Body: tt.body,
			}
			if tt.name == "large" {
				msg.Body = make([]byte, 65535)
			}
			got, err := msg.Marshal()
			if !assert.NoError(t, err) {
				return
			}
			s := string(got)
			expectedCL := len(msg.Body)
			clHeader := fmt.Sprintf("Content-Length: %d", expectedCL)
			assert.Contains(t, s, clHeader,
				"Content-Length header must match body length %d", expectedCL)
			// Verify the Content-Length value is on its own line just before body separator
			clIdx := strings.Index(s, clHeader)
			if assert.Greater(t, clIdx, 0) {
				lineEnd := clIdx + len(clHeader)
				assert.Equal(t, "\r\n", s[lineEnd:lineEnd+2],
					"Content-Length line must end with CRLF")
				// Body separator (CRLF) follows Content-Length line
				sepStart := lineEnd + 2
				assert.Equal(t, "\r\n", s[sepStart:sepStart+2],
					"body separator must be CRLF after Content-Length")
			}
		})
	}
}

func TestRFC3261_Marshal_ContentLength_AfterCSeq(t *testing.T) {
	msg := sipBenchRequest()
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	s := string(got)
	cseqIdx := strings.Index(s, "CSeq: ")
	clIdx := strings.Index(s, "Content-Length: ")
	if assert.Greater(t, cseqIdx, 0) && assert.Greater(t, clIdx, 0) {
		assert.Less(t, cseqIdx, clIdx,
			"CSeq must appear before Content-Length in marshalled output")
	}
	// Content-Length is the last header before the body separator
	afterCL := s[clIdx:]
	contentAfterCL := afterCL[len("Content-Length: 137"):]
	assert.True(t, strings.HasPrefix(contentAfterCL, "\r\n\r\n"),
		"Content-Length must be the last header, followed by CRLF body separator")
}

func TestRFC3261_Marshal_NoExtraContentLength(t *testing.T) {
	// Even if Headers map has stale Content-Length entries, marshalled output
	// must have exactly one Content-Length, computed from the body.
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:x", Version: "SIP/2.0",
		},
		Headers: SIPHeaders{
			"Content-Length": {"999"},
			"From":           {"<sip:a>"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
		Body: []byte("hello"),
	}
	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		s := string(got)
		// Count Content-Length occurrences
		count := strings.Count(s, "Content-Length:")
		assert.Equal(t, 1, count, "exactly one Content-Length header")
		assert.Contains(t, s, "Content-Length: 5")
		assert.NotContains(t, s, "Content-Length: 999")
	}
}

// -----------------------------------------------------------------------------
// Section 20.16: CSeq = "CSeq" HCOLON seqno SP Method
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_CSeqFormat(t *testing.T) {
	tests := []struct {
		seq    int
		method SIPMethod
	}{
		{1, "INVITE"},
		{2147483647, "BYE"},
		{0, "ACK"},
		{999999999, "REGISTER"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%d", tt.method, tt.seq), func(t *testing.T) {
			msg := &SIPMessage{
				startLine: &startLine{
					IsRequest: true, Method: tt.method,
					URI: "sip:x", Version: "SIP/2.0",
				},
				CSeq: CSeq{Seq: tt.seq, Method: tt.method},
			}
			got, err := msg.Marshal()
			if assert.NoError(t, err) {
				expected := fmt.Sprintf("CSeq: %d %s", tt.seq, tt.method)
				assert.Contains(t, string(got), expected)
			}
		})
	}
}

func TestRFC3261_Marshal_CSeqMethodMatches(t *testing.T) {
	// RFC 3261 §20.16: CSeq method MUST match the request method
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "BYE",
			URI: "sip:alice@example.com", Version: "SIP/2.0",
		},
		CSeq: CSeq{Seq: 1, Method: "BYE"},
	}
	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		assert.Contains(t, string(got), "BYE sip:alice@example.com SIP/2.0\r\n")
		assert.Contains(t, string(got), "CSeq: 1 BYE")
	}
}

// -----------------------------------------------------------------------------
// Section 20.14: Content-Length must appear at most once; body preserved
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_BodyPreserved(t *testing.T) {
	body := []byte("v=0\r\no=alice 2890844526 2890844526 IN IP4 host.atlanta.com\r\ns=-\r\n")
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:bob@example.com", Version: "SIP/2.0",
		},
		Headers: SIPHeaders{
			"Content-Type": {"application/sdp"},
			"From":         {"<sip:alice@atlanta.com>;tag=abc"},
			"To":           {"<sip:bob@biloxi.com>"},
			"Call-ID":      {"id123"},
			"Via":          {"SIP/2.0/UDP host;branch=z9hG4bK1"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
		Body: body,
	}
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	// Body must appear after the CRLF separator and be byte-identical
	// Find the body separator (last \r\n\r\n in the message)
	lastCRLF := strings.LastIndex(string(got), "\r\n\r\n")
	if assert.Greater(t, lastCRLF, 0) {
		extractedBody := got[lastCRLF+4:] // skip \r\n\r\n
		assert.Equal(t, body, extractedBody, "body must be byte-identical")
	}
}

func TestRFC3261_Marshal_NoExtraBody(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "OPTIONS",
			URI: "sip:proxy.example.com", Version: "SIP/2.0",
		},
		CSeq: CSeq{Seq: 1, Method: "OPTIONS"},
		Body: nil,
	}
	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		// Empty body: Content-Length: 0, message ends with \r\n\r\n
		assert.True(t, strings.HasSuffix(string(got), "Content-Length: 0\r\n\r\n"),
			"marshal with nil body must end with Content-Length: 0\\r\\n\\r\\n")
	}
}

// -----------------------------------------------------------------------------
// Section 7.3: Multiple header values (e.g. multiple Via)
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_MultipleHeaderValues(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:x", Version: "SIP/2.0",
		},
		Headers: SIPHeaders{
			"Via": {
				"SIP/2.0/UDP host1;branch=z9hG4bK1",
				"SIP/2.0/UDP host2;branch=z9hG4bK2",
			},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	s := string(got)
	// Count Via lines — must have 2 occurrences
	count := strings.Count(s, "Via: ")
	assert.Equal(t, 2, count, "each Via value must be on its own header line")
	// Each Via line must have proper format
	lines := strings.Split(s, "\r\n")
	viaLines := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "Via: ") {
			viaLines++
		}
	}
	assert.Equal(t, 2, viaLines)
}

// -----------------------------------------------------------------------------
// Section 18.3: UDP — complete message in a single datagram
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_RoundTrip_InviteSDP(t *testing.T) {
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
	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	// Re-parse and verify complete semantic round-trip
	msg2, err := ParseSIP(newTestReader(string(got)))
	if assert.NoError(t, err) {
		assert.Equal(t, msg.StartLine(), msg2.StartLine())
		assert.Equal(t, msg.Method(), msg2.Method())
		for k, vals := range msg.Headers {
			assert.Equal(t, vals, msg2.Headers.Get(k), "header %s mismatch", k)
		}
		assert.Equal(t, msg.CSeq, msg2.CSeq)
		assert.Equal(t, msg.Body, msg2.Body, "SDP body must be byte-identical")
	}
}

func TestRFC3261_Marshal_RoundTrip_ResponseWithBody(t *testing.T) {
	input := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP server.example.com;branch=z9hG4bK9a8b\r\n" +
		"From: Alice <sip:alice@example.com>;tag=abc\r\n" +
		"To: Bob <sip:bob@example.com>;tag=def\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Contact: <sip:bob@biloxi.com>\r\n" +
		"Content-Type: application/sdp\r\n" +
		"Content-Length: 137\r\n" +
		"\r\n" +
		"v=0\r\n" +
		"o=bob 2890844526 2890844526 IN IP4 biloxi.example.com\r\n" +
		"s=-\r\n" +
		"c=IN IP4 biloxi.example.com\r\n" +
		"t=0 0\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}
	got, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	msg2, err := ParseSIP(newTestReader(string(got)))
	if assert.NoError(t, err) {
		assert.False(t, msg2.IsRequest())
		assert.Equal(t, 200, msg2.StatusCode())
		assert.Equal(t, msg.Body, msg2.Body)
	}
}

func TestRFC3261_Marshal_Response_NoBody(t *testing.T) {
	input := "SIP/2.0 180 Ringing\r\n" +
		"Via: SIP/2.0/UDP proxy.example.com;branch=z9hG4bK1\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:bob@example.com>;tag=def\r\n" +
		"Call-ID: call123\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}
	got, err := msg.Marshal()
	if assert.NoError(t, err) {
		assert.Contains(t, string(got), "SIP/2.0 180 Ringing\r\n")
		assert.Contains(t, string(got), "Content-Length: 0\r\n\r\n")
		assert.True(t, strings.HasSuffix(string(got), "Content-Length: 0\r\n\r\n"),
			"empty-body response must end with Content-Length: 0\\r\\n\\r\\n")
	}
}

// -----------------------------------------------------------------------------
// Section 8.1.1.1: INVITE-specific Via, Max-Forwards, etc. in correct form
// -----------------------------------------------------------------------------

func TestRFC3261_Marshal_AllMethodsProduceValidWireFormat(t *testing.T) {
	methods := []SIPMethod{
		SIPMethodINVITE, SIPMethodACK, SIPMethodBYE, SIPMethodCANCEL,
		SIPMethodREGISTER, SIPMethodOPTIONS,
		SIPMethodPRACK, SIPMethodSUBSCRIBE, SIPMethodNOTIFY,
		SIPMethodPUBLISH, SIPMethodINFO, SIPMethodREFER,
		SIPMethodMESSAGE, SIPMethodUPDATE,
	}
	for _, method := range methods {
		t.Run(string(method), func(t *testing.T) {
			msg := &SIPMessage{
				startLine: &startLine{
					IsRequest: true, Method: method,
					URI: "sip:user@host", Version: "SIP/2.0",
				},
				Headers: SIPHeaders{
					"From":  {"<sip:a@a>"},
					"To":    {"<sip:b@b>"},
					"Via":   {"SIP/2.0/UDP host;branch=z9hG4bK1"},
					"Call-ID": {"call@host"},
				},
				CSeq: CSeq{Seq: 1, Method: method},
			}
			got, err := msg.Marshal()
			if !assert.NoError(t, err) {
				return
			}
			// Verify result is parseable by our own parser
			msg2, err := ParseSIP(newTestReader(string(got)))
			if assert.NoError(t, err) {
				assert.Equal(t, method, msg2.Method())
				assert.Equal(t, method, msg2.CSeq.Method)
			}
		})
	}
}

func rfc3261ReasonPhrase(code int) (string, bool) {
	m := map[int]string{
		100: "Trying", 180: "Ringing", 181: "Call Is Being Forwarded",
		200: "OK", 302: "Moved Temporarily",
		400: "Bad Request", 401: "Unauthorized", 403: "Forbidden",
		404: "Not Found", 407: "Proxy Authentication Required",
		408: "Request Timeout", 500: "Server Internal Error",
		503: "Service Unavailable", 600: "Busy Everywhere", 603: "Decline",
	}
	s, ok := m[code]
	return s, ok
}

func sipBenchRequest() *SIPMessage {
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
	msg, err := ParseSIP(newTestReader(input))
	if err != nil {
		panic(err)
	}
	return msg
}

func sipBenchResponse() *SIPMessage {
	input := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP server.example.com;branch=z9hG4bK9a8b\r\n" +
		"From: Alice <sip:alice@example.com>;tag=abc\r\n" +
		"To: Bob <sip:bob@example.com>;tag=def\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Contact: <sip:bob@biloxi.com>\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if err != nil {
		panic(err)
	}
	return msg
}

func sipBenchMinimal() *SIPMessage {
	input := "BYE sip:alice@example.com SIP/2.0\r\n" +
		"To: Alice <sip:alice@example.com>;tag=abc\r\n" +
		"From: Bob <sip:bob@example.com>;tag=def\r\n" +
		"CSeq: 1 BYE\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if err != nil {
		panic(err)
	}
	return msg
}

// =============================================================================
// RFC 3261 Strict Compliance — SIP Compact Marshalling (§7.3.3)
// =============================================================================

// -----------------------------------------------------------------------------
// Section 7.3.3: Compact Form — single-character header field names
// -----------------------------------------------------------------------------

func TestRFC3261_MarshalCompact_Section7_3_3(t *testing.T) {
	// Build a message covering all 17 registered compact-form headers
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:user@host", Version: "SIP/2.0",
		},
		Headers: SIPHeaders{
			"Accept-Contact":      {"+sip.instances"},
			"Referred-By":         {"<sip:a@b>"},
			"Content-Type":        {"application/sdp"},
			"Request-Disposition": {"queue"},
			"Content-Encoding":    {"gzip"},
			"From":                {"<sip:a@a>"},
			"Call-ID":             {"call123"},
			"Reject-Contact":      {"*"},
			"Supported":           {"100rel"},
			"Contact":             {"<sip:a@a>"},
			"Event":               {"presence"},
			"Refer-To":            {"<sip:b@b>"},
			"Subject":             {"meeting"},
			"To":                  {"<sip:b@b>"},
			"Allow-Events":        {"presence"},
			"Via":                 {"SIP/2.0/UDP host;branch=z9hG4bK1"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
		Body: []byte("hello"),
	}
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	s := string(got)

	// Every registered compact form appears as a header line
	assert.Contains(t, s, "a: ", "Accept-Contact -> a:")
	assert.Contains(t, s, "b: ", "Referred-By -> b:")
	assert.Contains(t, s, "c: ", "Content-Type -> c:")
	assert.Contains(t, s, "d: ", "Request-Disposition -> d:")
	assert.Contains(t, s, "e: ", "Content-Encoding -> e:")
	assert.Contains(t, s, "f: ", "From -> f:")
	assert.Contains(t, s, "i: ", "Call-ID -> i:")
	assert.Contains(t, s, "j: ", "Reject-Contact -> j:")
	assert.Contains(t, s, "k: ", "Supported -> k:")
	assert.Contains(t, s, "l: ", "Content-Length -> l:")
	assert.Contains(t, s, "m: ", "Contact -> m:")
	assert.Contains(t, s, "o: ", "Event -> o:")
	assert.Contains(t, s, "r: ", "Refer-To -> r:")
	assert.Contains(t, s, "s: ", "Subject -> s:")
	assert.Contains(t, s, "t: ", "To -> t:")
	assert.Contains(t, s, "u: ", "Allow-Events -> u:")
	assert.Contains(t, s, "v: ", "Via -> v:")

	// No long forms of compressed headers appear
	assert.NotContains(t, s, "Accept-Contact: ")
	assert.NotContains(t, s, "Referred-By: ")
	assert.NotContains(t, s, "Content-Type: ")
	assert.NotContains(t, s, "Request-Disposition: ")
	assert.NotContains(t, s, "Content-Encoding: ")
	assert.NotContains(t, s, "From: ")
	assert.NotContains(t, s, "Call-ID: ")
	assert.NotContains(t, s, "Reject-Contact: ")
	assert.NotContains(t, s, "Supported: ")
	assert.NotContains(t, s, "Contact: ")
	assert.NotContains(t, s, "Event: ")
	assert.NotContains(t, s, "Refer-To: ")
	assert.NotContains(t, s, "Subject: ")
	assert.NotContains(t, s, "To: ")
	assert.NotContains(t, s, "Allow-Events: ")
	assert.NotContains(t, s, "Via: ")
	assert.NotContains(t, s, "Content-Length: ")

	// CSeq has no compact form and is unaffected
	assert.Contains(t, s, "CSeq: ")

	// Output parses back correctly
	msg2, err := ParseSIP(newTestReader(s))
	if assert.NoError(t, err) {
		for k, vals := range msg.Headers {
			assert.Equal(t, vals, msg2.Headers.Get(k), "header %s round-trip", k)
		}
		assert.Equal(t, msg.CSeq, msg2.CSeq)
		assert.Equal(t, msg.Body, msg2.Body)
	}
}

func TestRFC3261_MarshalCompact_CompactKeyIsToken(t *testing.T) {
	// §7.3.3: compact form is a single character that is a valid token
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:user@host", Version: "SIP/2.0",
		},
		Headers: SIPHeaders{
			"Via":       {"SIP/2.0/UDP host;branch=z9hG4bK1"},
			"From":      {"<sip:a@a>"},
			"To":        {"<sip:b@b>"},
			"Call-ID":   {"id"},
			"Supported": {"100rel"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	s := string(got)
	headerEnd := strings.Index(s, "\r\n\r\n")
	if !assert.Greater(t, headerEnd, 0) {
		return
	}
	for i, line := range strings.Split(s[:headerEnd], "\r\n") {
		if i == 0 {
			continue // skip start-line
		}
		colonIdx := strings.IndexByte(line, ':')
		if colonIdx <= 0 {
			continue
		}
		fieldName := line[:colonIdx]
		if fieldName == "CSeq" || fieldName == "l" || fieldName == "Max-Forwards" {
			continue // already valid
		}
		// Compact key must be a single token character (alphanumeric)
		if assert.Len(t, fieldName, 1, "compact form must be single char, got %q", fieldName) {
			c := fieldName[0]
			assert.True(t, c >= 'a' && c <= 'z', "compact key %q must be lowercase letter", c)
		}
	}
}

func TestRFC3261_MarshalCompact_UnchangedHeaders(t *testing.T) {
	// Headers without registered compact forms must remain in long form
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:user@host", Version: "SIP/2.0",
		},
		Headers: SIPHeaders{
			"CSeq":          {"1 INVITE"},
			"Max-Forwards":  {"70"},
			"User-Agent":    {"test"},
			"Content-Length": {"0"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.MarshalCompact()
	if assert.NoError(t, err) {
		s := string(got)
		// CSeq and Max-Forwards have no compact form
		assert.Contains(t, s, "CSeq: 1 INVITE", "CSeq unchanged in compact mode")
		assert.NotContains(t, s, "cseq: ")
		assert.Contains(t, s, "Max-Forwards: 70", "Max-Forwards unchanged")
		// User-Agent has no compact form
		assert.Contains(t, s, "User-Agent: test", "custom header unchanged")
		// Content-Length DOES have compact form, so it should be "l:"
		assert.Contains(t, s, "l: 0", "Content-Length uses compact form l:")
	}
}

// -----------------------------------------------------------------------------
// Section 7: Start-line unchanged by compact mode
// -----------------------------------------------------------------------------

func TestRFC3261_MarshalCompact_RequestLineUnchanged(t *testing.T) {
	msg := sipBenchRequest()
	normal, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	compact, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	// Extract start-lines (everything before first CRLF)
	nl := strings.Index(string(normal), "\r\n")
	cl := strings.Index(string(compact), "\r\n")
	if assert.Equal(t, nl, cl, "start-line length must match") {
		assert.Equal(t, string(normal[:nl]), string(compact[:cl]),
			"start-line must be identical in normal and compact mode")
	}
}

func TestRFC3261_MarshalCompact_StatusLineUnchanged(t *testing.T) {
	msg := sipBenchResponse()
	normal, err := msg.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	compact, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	nl := strings.Index(string(normal), "\r\n")
	cl := strings.Index(string(compact), "\r\n")
	if assert.Equal(t, nl, cl) {
		assert.Equal(t, string(normal[:nl]), string(compact[:cl]),
			"status-line must be identical")
	}
}

// -----------------------------------------------------------------------------
// Section 7.3.1: Header format rules apply equally to compact headers
// -----------------------------------------------------------------------------

func TestRFC3261_MarshalCompact_HeaderFormat(t *testing.T) {
	msg := sipBenchRequest()
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	s := string(got)
	headerEnd := strings.Index(s, "\r\n\r\n")
	if !assert.Greater(t, headerEnd, 0) {
		return
	}
	for i, line := range strings.Split(s[:headerEnd], "\r\n") {
		if i == 0 {
			continue // skip start-line (contains sip: URI)
		}
		colonIdx := strings.IndexByte(line, ':')
		if colonIdx <= 0 {
			continue
		}
		// §7.3.1: field-name ":" SP field-value
		assert.Equal(t, byte(' '), line[colonIdx+1], "SP after colon in compact header %q", line)
	}
}

func TestRFC3261_MarshalCompact_CRLFOnly(t *testing.T) {
	msg := sipBenchRequest()
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	crCount, lfCount := 0, 0
	for i := 0; i < len(got); i++ {
		switch got[i] {
		case '\r':
			crCount++
			assert.Less(t, i+1, len(got), "CR at final byte")
			if i+1 < len(got) {
				assert.Equal(t, byte('\n'), got[i+1], "CR must be followed by LF at byte %d", i)
			}
		case '\n':
			lfCount++
			assert.Greater(t, i, 0, "LF at byte 0")
			if i > 0 {
				assert.Equal(t, byte('\r'), got[i-1], "LF must be preceded by CR at byte %d", i)
			}
		}
	}
	assert.Equal(t, crCount, lfCount, "CR/LF must be paired")
}

// -----------------------------------------------------------------------------
// Section 20.14: Content-Length in compact form
// -----------------------------------------------------------------------------

func TestRFC3261_MarshalCompact_ContentLengthAccuracy(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"empty", nil},
		{"zero_length", []byte{}},
		{"small", []byte("hello")},
		{"sdp", []byte("v=0\r\no=user 1\r\n")},
		{"binary", []byte{0x00, 0x01, 0xFF}},
		{"large", make([]byte, 65535)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &SIPMessage{
				startLine: &startLine{
					IsRequest: true, Method: "INVITE",
					URI: "sip:x", Version: "SIP/2.0",
				},
				CSeq: CSeq{Seq: 1, Method: "INVITE"},
				Body: tt.body,
			}
			got, err := msg.MarshalCompact()
			if !assert.NoError(t, err) {
				return
			}
			s := string(got)
			expectedCL := len(msg.Body)
			clLine := fmt.Sprintf("l: %d", expectedCL)
			assert.Contains(t, s, clLine,
				"compact Content-Length (l:) must match body length %d", expectedCL)
			// Must be the last header before body separator
			clIdx := strings.Index(s, clLine)
			if assert.Greater(t, clIdx, 0) {
				lineEnd := clIdx + len(clLine)
				assert.Equal(t, "\r\n", s[lineEnd:lineEnd+2], "l: line must end with CRLF")
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Section 20.16: CSeq unaffected by compact mode
// -----------------------------------------------------------------------------

func TestRFC3261_MarshalCompact_CSeqUnchanged(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:x", Version: "SIP/2.0",
		},
		CSeq: CSeq{Seq: 42, Method: "INVITE"},
	}
	got, err := msg.MarshalCompact()
	if assert.NoError(t, err) {
		assert.Contains(t, string(got), "CSeq: 42 INVITE",
			"CSeq must remain in long form")
	}
}

// -----------------------------------------------------------------------------
// Section 7.5: Body separator and preservation
// -----------------------------------------------------------------------------

func TestRFC3261_MarshalCompact_BodyPreserved(t *testing.T) {
	body := []byte("v=0\r\no=user 2890844526 IN IP4 host\r\ns=-\r\n")
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:bob@example.com", Version: "SIP/2.0",
		},
		Headers: SIPHeaders{
			"Content-Type": {"application/sdp"},
			"From":         {"<sip:alice@atlanta.com>;tag=abc"},
			"To":           {"<sip:bob@biloxi.com>"},
			"Call-ID":      {"id123"},
			"Via":          {"SIP/2.0/UDP host;branch=z9hG4bK1"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
		Body: body,
	}
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	// Body follows the \r\n\r\n separator
	s := string(got)
	lastSep := strings.LastIndex(s, "\r\n\r\n")
	if assert.Greater(t, lastSep, 0) {
		extracted := got[lastSep+4:]
		assert.Equal(t, body, extracted, "body must be byte-identical")
	}
	// The separator must be the empty line after the last header (l:)
	assert.Contains(t, s, "l: "+fmt.Sprintf("%d", len(body))+"\r\n\r\n")
}

// -----------------------------------------------------------------------------
// Deterministic header ordering applies to compact mode too
// -----------------------------------------------------------------------------

func TestRFC3261_MarshalCompact_DeterministicOrder(t *testing.T) {
	msg := &SIPMessage{
		startLine: &startLine{
			IsRequest: true, Method: "INVITE",
			URI: "sip:x", Version: "SIP/2.0",
		},
		Headers: SIPHeaders{
			"Z-Last":  {"z"},
			"From":    {"<sip:a>"},
			"To":      {"<sip:b>"},
			"Subject": {"hello"},
			"Call-ID": {"id"},
		},
		CSeq: CSeq{Seq: 1, Method: "INVITE"},
	}
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	// Order is determined by LONG header names, but keys are output as compact forms
	// Sorted long names: Call-ID, From, Subject, To, Z-Last
	// Output keys: i, f, s, t, Z-Last (Z-Last has no compact form)
	expected := "INVITE sip:x SIP/2.0\r\n" +
		"i: id\r\n" +
		"f: <sip:a>\r\n" +
		"s: hello\r\n" +
		"t: <sip:b>\r\n" +
		"Z-Last: z\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"l: 0\r\n\r\n"
	assert.Equal(t, expected, string(got))
}

// -----------------------------------------------------------------------------
// Round-trip: compact output is valid SIP parsable by our parser
// -----------------------------------------------------------------------------

func TestRFC3261_MarshalCompact_RoundTrip_Response(t *testing.T) {
	input := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP server.example.com;branch=z9hG4bK9a8b\r\n" +
		"From: Alice <sip:alice@example.com>;tag=abc\r\n" +
		"To: Bob <sip:bob@example.com>;tag=def\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Contact: <sip:bob@biloxi.com>\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	msg2, err := ParseSIP(newTestReader(string(got)))
	if assert.NoError(t, err) {
		assert.Equal(t, msg.StartLine(), msg2.StartLine())
		for k, vals := range msg.Headers {
			assert.Equal(t, vals, msg2.Headers.Get(k), "header %s round-trip", k)
		}
		assert.Equal(t, msg.CSeq, msg2.CSeq)
		assert.Equal(t, msg.Body, msg2.Body)
	}
}

func TestRFC3261_MarshalCompact_RoundTrip_SDP(t *testing.T) {
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
	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	msg2, err := ParseSIP(newTestReader(string(got)))
	if assert.NoError(t, err) {
		assert.Equal(t, msg.Method(), msg2.Method())
		for k, vals := range msg.Headers {
			assert.Equal(t, vals, msg2.Headers.Get(k), "header %s round-trip", k)
		}
		assert.Equal(t, msg.CSeq, msg2.CSeq)
		assert.Equal(t, msg.Body, msg2.Body, "SDP body round-trip")
	}
}

func TestRFC3261_MarshalCompact_SizeMatchesOutput(t *testing.T) {
	tests := []struct {
		name string
		msg  *SIPMessage
	}{
		{"request", sipBenchRequest()},
		{"response", sipBenchResponse()},
		{"minimal", sipBenchMinimal()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sz := tt.msg.MarshalCompactSize()
			got, err := tt.msg.MarshalCompact()
			if assert.NoError(t, err) {
				assert.Equal(t, sz, len(got),
					"MarshalCompactSize %d must match actual output length %d", sz, len(got))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MarshalCompact
// ---------------------------------------------------------------------------

func TestSIPMarshalCompact_HeaderForms(t *testing.T) {
	msg := sipBenchRequest()
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	s := string(got)
	// Verify compact forms are used
	assert.Contains(t, s, "v: ", "Via should become v:")
	assert.Contains(t, s, "f: ", "From should become f:")
	assert.Contains(t, s, "t: ", "To should become t:")
	assert.Contains(t, s, "i: ", "Call-ID should become i:")
	assert.Contains(t, s, "m: ", "Contact should become m:")
	assert.Contains(t, s, "c: ", "Content-Type should become c:")
	assert.Contains(t, s, "l: ", "Content-Length should become l:")
	assert.NotContains(t, s, "Via: ", "long form Via must not appear")
	assert.NotContains(t, s, "Content-Length: ", "long form Content-Length must not appear")

	// CSeq has no compact form; must remain long
	assert.Contains(t, s, "CSeq: ", "CSeq has no compact form")
	// Max-Forwards has no compact form
	assert.Contains(t, s, "Max-Forwards: ", "Max-Forwards has no compact form")
}

func TestSIPMarshalCompact_Response(t *testing.T) {
	msg := sipBenchResponse()
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}
	s := string(got)
	assert.Contains(t, s, "SIP/2.0 200 OK\r\n")
	assert.Contains(t, s, "v: ", "Via compact")
	assert.Contains(t, s, "f: ", "From compact")
	assert.Contains(t, s, "t: ", "To compact")
	assert.Contains(t, s, "i: ", "Call-ID compact")
	assert.Contains(t, s, "m: ", "Contact compact")
	assert.Contains(t, s, "l: ", "Content-Length compact")
}

func TestSIPMarshalCompact_RoundTrip(t *testing.T) {
	input := "INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP pc33.atlanta.com;branch=z9hG4bK776asdhds\r\n" +
		"Max-Forwards: 70\r\n" +
		"To: Bob <sip:bob@biloxi.com>\r\n" +
		"From: Alice <sip:alice@atlanta.com>;tag=1928301774\r\n" +
		"Call-ID: a84b4c76e66710@pc33.atlanta.com\r\n" +
		"CSeq: 314159 INVITE\r\n" +
		"Contact: <sip:alice@pc33.atlanta.com>\r\n" +
		"Content-Type: application/sdp\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := ParseSIP(newTestReader(input))
	if !assert.NoError(t, err) {
		return
	}
	got, err := msg.MarshalCompact()
	if !assert.NoError(t, err) {
		return
	}

	// Verify compact output parses back correctly (parser accepts compact forms)
	msg2, err := ParseSIP(newTestReader(string(got)))
	if assert.NoError(t, err) {
		assert.Equal(t, msg.StartLine(), msg2.StartLine())
		assert.Equal(t, msg.Method(), msg2.Method())
		for k, vals := range msg.Headers {
			assert.Equal(t, vals, msg2.Headers.Get(k), "header %s round-trip", k)
		}
		assert.Equal(t, msg.CSeq, msg2.CSeq)
		assert.Equal(t, msg.Body, msg2.Body)
	}
}

func TestSIPMarshalCompact_SizeMatches(t *testing.T) {
	msg := sipBenchRequest()
	sz := msg.MarshalCompactSize()
	got, err := msg.MarshalCompact()
	if assert.NoError(t, err) {
		assert.Equal(t, sz, len(got), "MarshalCompactSize must match actual output length")
	}
}

func BenchmarkSIPMarshalCompact_Request(b *testing.B) {
	msg := sipBenchRequest()
	sz := msg.MarshalCompactSize()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, sz)
		msg.marshalToCompact(buf)
	}
}

func BenchmarkSIPMarshalCompact_Response(b *testing.B) {
	msg := sipBenchResponse()
	sz := msg.MarshalCompactSize()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, sz)
		msg.marshalToCompact(buf)
	}
}

func BenchmarkSIPMarshalCompact_Minimal(b *testing.B) {
	msg := sipBenchMinimal()
	sz := msg.MarshalCompactSize()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, sz)
		msg.marshalToCompact(buf)
	}
}

func BenchmarkSIPMarshalCompactTo_Request(b *testing.B) {
	msg := sipBenchRequest()
	sz := msg.MarshalCompactSize()
	buf := make([]byte, sz)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.marshalToCompact(buf)
	}
}

func BenchmarkSIPMarshalCompactTo_Response(b *testing.B) {
	msg := sipBenchResponse()
	sz := msg.MarshalCompactSize()
	buf := make([]byte, sz)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.marshalToCompact(buf)
	}
}

func BenchmarkSIPMarshalCompactTo_Minimal(b *testing.B) {
	msg := sipBenchMinimal()
	sz := msg.MarshalCompactSize()
	buf := make([]byte, sz)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.marshalToCompact(buf)
	}
}

func BenchmarkSIPMarshal_Request(b *testing.B) {
	msg := sipBenchRequest()
	sz := msg.MarshalSize()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, sz)
		msg.marshalTo(buf)
	}
}

func BenchmarkSIPMarshal_Response(b *testing.B) {
	msg := sipBenchResponse()
	sz := msg.MarshalSize()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, sz)
		msg.marshalTo(buf)
	}
}

func BenchmarkSIPMarshal_Minimal(b *testing.B) {
	msg := sipBenchMinimal()
	sz := msg.MarshalSize()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, sz)
		msg.marshalTo(buf)
	}
}

func BenchmarkSIPMarshalTo_Request(b *testing.B) {
	msg := sipBenchRequest()
	sz := msg.MarshalSize()
	buf := make([]byte, sz)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.marshalTo(buf)
	}
}

func BenchmarkSIPMarshalTo_Response(b *testing.B) {
	msg := sipBenchResponse()
	sz := msg.MarshalSize()
	buf := make([]byte, sz)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.marshalTo(buf)
	}
}

func BenchmarkSIPMarshalTo_Minimal(b *testing.B) {
	msg := sipBenchMinimal()
	sz := msg.MarshalSize()
	buf := make([]byte, sz)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.marshalTo(buf)
	}
}
