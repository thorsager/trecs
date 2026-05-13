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
