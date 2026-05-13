package proto

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

const (
	SIPMethodINVITE   SIPMethod = "INVITE"
	SIPMethodACK      SIPMethod = "ACK"
	SIPMethodBYE      SIPMethod = "BYE"
	SIPMethodCANCEL   SIPMethod = "CANCEL"
	SIPMethodREGISTER SIPMethod = "REGISTER"
	SIPMethodOPTIONS  SIPMethod = "OPTIONS"

	// Extension methods
	SIPMethodPRACK     SIPMethod = "PRACK"     // RFC 3262
	SIPMethodSUBSCRIBE SIPMethod = "SUBSCRIBE" // RFC 6665
	SIPMethodNOTIFY    SIPMethod = "NOTIFY"    // RFC 6665
	SIPMethodPUBLISH   SIPMethod = "PUBLISH"   // RFC 3903
	SIPMethodINFO      SIPMethod = "INFO"      // RFC 6086
	SIPMethodREFER     SIPMethod = "REFER"     // RFC 3515
	SIPMethodMESSAGE   SIPMethod = "MESSAGE"   // RFC 3428
	SIPMethodUPDATE    SIPMethod = "UPDATE"    // RFC 3311
)

var (
	compactHeaders = map[string]string{
		"a": "Accept-Contact",      // RFC 3841
		"b": "Referred-By",         // RFC 3892
		"c": "Content-Type",        // RFC 3261
		"d": "Request-Disposition", // RFC 3841
		"e": "Content-Encoding",    // RFC 3261
		"f": "From",                // RFC 3261
		"i": "Call-ID",             // RFC 3261
		"j": "Reject-Contact",      // RFC 3841
		"k": "Supported",           // RFC 3261
		"l": "Content-Length",      // RFC 3261
		"m": "Contact",             // RFC 3261
		"o": "Event",               // RFC 6665
		"r": "Refer-To",            // RFC 3515
		"s": "Subject",             // RFC 3261
		"t": "To",                  // RFC 3261
		"u": "Allow-Events",        // RFC 6665
		"v": "Via",                 // RFC 3261
	}

	longToCompact = map[string]string{}

	// rfcOverride corrects HTTP canonicalization where SIP RFC 3261 differs.
	rfcOverride = map[string]string{
		"Call-Id":          "Call-ID",
		"Cseq":             "CSeq",
		"Www-Authenticate": "WWW-Authenticate",
	}
)

func init() {
	for c, l := range compactHeaders {
		longToCompact[l] = c
	}
}

// SIPMethod identifies a SIP method as defined in RFC 3261 and extension RFCs.
type SIPMethod string

// SIPHeaders represents the SIP message headers as a multimap keyed by canonical
// header name. Access methods normalize keys to SIP-canonical form.
type SIPHeaders map[string][]string

func canonicalHeaderKey(k string) string {
	can := textproto.CanonicalMIMEHeaderKey(k)
	if rfc, ok := rfcOverride[can]; ok {
		return rfc
	}
	return can
}

// GetFirst returns the first value for header k, or "" if absent.
func (h SIPHeaders) GetFirst(k string) string {
	if values, ok := h[canonicalHeaderKey(k)]; ok {
		return values[0]
	}
	return ""
}

// Get returns all values for header k, or nil if absent.
func (h SIPHeaders) Get(k string) []string {
	if values, ok := h[canonicalHeaderKey(k)]; ok {
		return values
	}
	return nil
}

// Set replaces all values for header k with v.
func (h SIPHeaders) Set(k string, v []string) {
	h[canonicalHeaderKey(k)] = v
}

// Add appends a value to header k.
func (h SIPHeaders) Add(k, v string) {
	can := canonicalHeaderKey(k)
	h[can] = append(h[can], v)
}

// SIPAddress represents a parsed SIP address as used in From, To, and
// Contact headers. It handles both the name-addr form (display-name in angle
// brackets) and addr-spec form (bare URI).
type SIPAddress struct {
	DisplayName string
	URI         string
	Tag         string
	Params      map[string]string
}

// ParseSIPAddress parses a raw From/To/Contact header value and returns a
// structured SIPAddress. The empty string is an error.
func ParseSIPAddress(raw string) (*SIPAddress, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ParseError("empty From/To value")
	}

	ft := &SIPAddress{}

	if openBracket := strings.IndexByte(raw, '<'); openBracket >= 0 {
		closeBracket := strings.IndexByte(raw[openBracket:], '>')
		if closeBracket < 0 {
			return nil, ParseError("invalid name-addr: missing closing bracket")
		}
		closeBracket += openBracket

		display := strings.TrimSpace(raw[:openBracket])
		if display != "" {
			ft.DisplayName = unquoteDisplayName(display)
		}

		ft.URI = strings.TrimSpace(raw[openBracket+1 : closeBracket])

		rest := strings.TrimSpace(raw[closeBracket+1:])
		ft.parseParams(rest)
	} else {
		uri, params := splitAddrSpec(raw)
		ft.URI = uri
		ft.parseParams(params)
	}

	return ft, nil
}

func unquoteDisplayName(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, "\\\"", "\"")
		s = strings.ReplaceAll(s, "\\\\", "\\")
	}
	return s
}

func (ft *SIPAddress) parseParams(s string) {
	if s == "" {
		return
	}
	s = strings.TrimPrefix(s, ";")
	for s != "" {
		var p string
		p, s, _ = strings.Cut(s, ";")
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k, v, found := strings.Cut(p, "=")
		if found {
			if k == "tag" {
				ft.Tag = v
			} else {
				if ft.Params == nil {
					ft.Params = make(map[string]string)
				}
				ft.Params[k] = v
			}
		} else {
			if ft.Params == nil {
				ft.Params = make(map[string]string)
			}
			ft.Params[k] = ""
		}
	}
}

func splitAddrSpec(raw string) (uri, params string) {
	if idx := strings.Index(raw, ";tag="); idx >= 0 {
		return raw[:idx], raw[idx+1:]
	}
	return raw, ""
}

// CSeq holds the parsed CSeq header: a sequence number paired with a method.
type CSeq struct {
	Method SIPMethod
	Seq    int
}

func (c *CSeq) String() string {
	return fmt.Sprintf("%d %s", c.Seq, c.Method)
}

// SIPMessage represents a parsed SIP request or response.
type SIPMessage struct {
	startLine *startLine
	CSeq      CSeq
	Headers   SIPHeaders
	Body      []byte
}

// StartLine returns the raw start-line (request-line or status-line).
func (m *SIPMessage) StartLine() string {
	if m.startLine == nil {
		return ""
	}
	return m.startLine.String()
}

// IsRequest reports whether the message is a SIP request.
func (m *SIPMessage) IsRequest() bool {
	if m.startLine == nil {
		return false
	}
	return m.startLine.IsRequest
}

// StatusCode returns the response status code, or 0 for requests or nil
// messages.
func (m *SIPMessage) StatusCode() int {
	if m.startLine == nil {
		return 0
	}
	if m.startLine.IsRequest {
		return 0
	}
	return m.startLine.StatusCode
}

// Status returns the response status-line (code + reason), or "" for requests
// or nil messages.
func (m *SIPMessage) Status() string {
	if m.startLine == nil {
		return ""
	}
	if m.startLine.IsRequest {
		return ""
	}
	return m.startLine.Status
}

// Method returns the request method, or "" for responses or nil messages.
func (m *SIPMessage) Method() SIPMethod {
	if m.startLine == nil {
		return ""
	}
	if !m.startLine.IsRequest {
		return ""
	}
	return m.startLine.Method
}

// Version returns the SIP version from the start-line.
func (m *SIPMessage) Version() string {
	if m.startLine == nil {
		return ""
	}
	return m.startLine.Version
}

// From parses and returns the From header as a SIPAddress. Returns an error
// if the header is absent or malformed.
func (m *SIPMessage) From() (*SIPAddress, error) {
	return ParseSIPAddress(m.Headers.GetFirst("From"))
}

// To parses and returns the To header as a SIPAddress. Returns an error if
// the header is absent or malformed.
func (m *SIPMessage) To() (*SIPAddress, error) {
	return ParseSIPAddress(m.Headers.GetFirst("To"))
}

type startLine struct {
	IsRequest  bool
	Method     SIPMethod
	Version    string
	StatusCode int
	Status     string
	URI        string
}

func (sl *startLine) String() string {
	if sl.IsRequest {
		return string(sl.Method) + " " + sl.URI + " " + sl.Version
	}
	return sl.Version + " " + sl.Status
}

// ParseSIPUDP parses a complete SIP message from a byte slice, consuming any
// trailing datagram data.
func ParseSIPUDP(data []byte) (*SIPMessage, error) {
	return parseSIP(bufio.NewReader(bytes.NewReader(data)), false)
}

// ParseSIP parses a SIP message from r. If r is already a *bufio.Reader it
// is used directly; otherwise a buffered reader is created. For streamed
// transports (TCP), remaining data after the body is preserved in the reader.
func ParseSIP(r io.Reader) (*SIPMessage, error) {
	br,ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return parseSIP(br, true)
}

func parseSIP(r *bufio.Reader, streamed bool) (*SIPMessage, error) {
	msg := &SIPMessage{
		Headers: make(SIPHeaders, 16),
	}

	line, err := readLine(r)
	if err != nil {
		return nil, ParseErrorWrap(err, "error reading start-line")
	}

	if sl, err := parseStartLine(line); err != nil {
		return nil, err
	} else {
		msg.startLine = sl
	}

	if msg.Headers, err = parseHeaders(r); err != nil {
		return nil, err
	}

	if values, ok := msg.Headers["CSeq"]; ok {
		cseq, err := parseCSeq(values)
		if err != nil {
			return nil, err
		}
		msg.CSeq = cseq
	}

	if clStr, ok := msg.Headers["Content-Length"]; ok {
		contentLength, err := strconv.Atoi(strings.TrimSpace(clStr[0]))
		if err == nil && contentLength > 0 {
			msg.Body = make([]byte, contentLength)
			_, err = io.ReadFull(r, msg.Body)
			if err != nil {
				return nil, ParseErrorWrap(err, "error reading body")
			}
		}
		if !streamed {
			_, _ = io.CopyN(io.Discard, r, 1<<20)
		}
	} else {
		msg.Body, err = io.ReadAll(r)
		if err != nil {
			return nil, ParseErrorWrap(err, "error reading body")
		}
	}

	return msg, nil
}

func parseHeaders(r *bufio.Reader) (SIPHeaders, error) {
	h := make(SIPHeaders, 16)
	for {
		line, err := readContinuedLine(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, ParseErrorWrap(io.EOF, "header terminated prematurely")
			}
			return nil, ParseErrorWrap(err, "Failed to read headers")
		}

		if line == "" {
			break
		}
		k, v, found := strings.Cut(line, ":")
		if !found {
			return nil, ParseError("Invalid header: %s", line)
		}
		if l, found := compactHeaders[k]; found {
			k = l
		}
		k = canonicalHeaderKey(k)
		for len(v) > 0 && v[0] == ' ' {
			v = v[1:]
		}
		h[k] = append(h[k], v)
	}
	return h, nil
}

func parseStartLine(s string) (*startLine, error) {
	t0, rest, ok := strings.Cut(s, " ")
	if !ok {
		return nil, ParseError("Invalid start-line: %s", s)
	}
	t1, t2, ok := strings.Cut(rest, " ")
	if !ok || t1 == "" || t2 == "" {
		return nil, ParseError("Invalid start-line: %s", s)
	}

	sl := &startLine{}
	if strings.HasPrefix(t0, "SIP/") {
		sl.IsRequest = false
		sl.Version = t0
		if len(t1) != 3 {
			return nil, ParseError("Invalid start-line status-code: %s", t1)
		}
		if sc, err := strconv.Atoi(t1); err != nil {
			return nil, ParseErrorWrap(err, "Invalid start-line status-code: %s", t1)
		} else {
			sl.StatusCode = sc
		}
		sl.Status = t1 + " " + t2
	} else {
		sl.IsRequest = true
		if m, err := parseMethod(t0); err != nil {
			return nil, err
		} else {
			sl.Method = m
		}

		sl.URI = t1

		if !strings.HasPrefix(t2, "SIP/") {
			return nil, ParseError("Invalid start-line version: %s", t2)
		}
		sl.Version = t2
	}
	return sl, nil
}

func parseCSeq(values []string) (CSeq, error) {
	if len(values) == 0 {
		return CSeq{}, ParseError("No Cseq value found")
	}
	if len(values) > 1 {
		return CSeq{}, ParseError("Multipel Cseq values found")
	}
	seqStr, methodStr, found := strings.Cut(values[0], " ")
	if !found {
		return CSeq{}, ParseError("Invalid Cseq payload: %s", values[0])
	}
	seq, err := strconv.Atoi(seqStr)
	if err != nil {
		return CSeq{}, ParseErrorWrap(err, "Invalid Cseq Sequence %s", seqStr)
	}
	method, err := parseMethod(methodStr)
	if err != nil {
		return CSeq{}, ParseErrorWrap(err, "Invalid Cseq Method")
	}

	return CSeq{Seq: seq, Method: method}, nil
}

func parseMethod(v string) (SIPMethod, error) {
	switch SIPMethod(v) {
	case SIPMethodINVITE, SIPMethodACK, SIPMethodCANCEL, SIPMethodOPTIONS,
		SIPMethodBYE, SIPMethodREGISTER,
		SIPMethodPRACK, SIPMethodSUBSCRIBE, SIPMethodNOTIFY,
		SIPMethodPUBLISH, SIPMethodINFO, SIPMethodREFER,
		SIPMethodMESSAGE, SIPMethodUPDATE:
		return SIPMethod(v), nil
	}
	return "", ParseError("Invalid Method: %s", v)
}
