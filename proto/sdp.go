package proto

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// SDP represents a parsed Session Description Protocol (RFC 8866) session
// description, containing session-level fields and zero or more media descriptions.
type SDP struct {
	Version     int
	Origin      Origin
	SessionName string
	SessionInfo string
	URI         string
	Emails      []string
	Phones      []string
	Connection  *ConnectionInfo
	Bandwidths  []BandwidthInfo
	Times       []TimeDescription
	TimeZone    string
	Encryption  *EncryptionKey
	Attributes  []Attribute
	MediaDescs  []MediaDescription
}

// Origin holds the parsed fields from the SDP origin (o=) line: username,
// session identifier, session version, network type, address type, and
// unicast address.
type Origin struct {
	Username       string
	SessionID      string
	SessionVersion string
	NetworkType    string
	AddressType    string
	Address        string
}

// ConnectionInfo holds the parsed fields from an SDP connection (c=) line:
// network type, address type, and connection address.
type ConnectionInfo struct {
	NetworkType string
	AddressType string
	Address     string
}

// BandwidthInfo holds a parsed SDP bandwidth (b=) line: the bandwidth type
// (e.g. "CT", "AS") and value in kilobits per second.
type BandwidthInfo struct {
	Type  string
	Value int
}

// TimeDescription holds the parsed fields from an SDP time (t=) line along
// with any associated repeat (r=) times. Times are NTP timestamps in seconds
// since 1900.
type TimeDescription struct {
	Start   int64
	Stop    int64
	Repeats []RepeatInfo
}

// RepeatInfo holds the parsed fields from an SDP repeat (r=) line: repeat
// interval, active duration, and offset list. All values are in seconds.
type RepeatInfo struct {
	Interval int64
	Duration int64
	Offsets  []int64
}

// EncryptionKey holds a parsed SDP encryption key (k=) line. Note that the
// k= line is obsolete per RFC 8866 and should not be used in new
// implementations.
type EncryptionKey struct {
	Method string
	Key    string
}

// Attribute holds a parsed SDP attribute (a=) line. The Key is the attribute
// name; Value is the optional portion after the colon.
type Attribute struct {
	Key   string
	Value string
}

// MediaDescription holds the parsed fields from an SDP media (m=) line and
// any following media-level lines (i=, c=, b=, k=, a=).
type MediaDescription struct {
	Type       string
	Port       int
	PortCount  int
	Proto      string
	Fmt        []string
	Title      string
	Connection *ConnectionInfo
	Bandwidths []BandwidthInfo
	Encryption *EncryptionKey
	Attributes []Attribute
}

// ParseSDP parses a complete SDP session description from r, returning a
// structured SDP value or an error. The reader must provide a complete
// SDP body; parsing stops at the first empty line or EOF.
func ParseSDP(r io.Reader) (*SDP, error) {
	var pooled *bufio.Reader
	br, ok := r.(*bufio.Reader)
	if !ok {
		pooled = bufioReaderPool.Get().(*bufio.Reader)
		pooled.Reset(r)
		br = pooled
		defer bufioReaderPool.Put(pooled)
	}

	sdp := &SDP{}
	var media *MediaDescription
	var seenVersion bool
	var seenFirstLine bool

	for {
		line, err := readLine(br)
		if err != nil && line == "" {
			if !seenFirstLine {
				return nil, ParseError("sdp: empty body")
			}
			break
		}
		seenFirstLine = true

		line = strings.TrimSpace(line)
		if line == "" {
			if err != nil {
				break
			}
			continue
		}
		if len(line) < 3 || line[1] != '=' {
			return nil, ParseError("sdp: malformed line: %q", line)
		}
		typ := line[0]
		val := line[2:]

		if typ == 'm' {
			md, err := parseMedia(val)
			if err != nil {
				return nil, err
			}
			sdp.MediaDescs = append(sdp.MediaDescs, md)
			media = &sdp.MediaDescs[len(sdp.MediaDescs)-1]
			continue
		}

		if typ == 'v' {
			if seenVersion {
				return nil, ParseError("sdp: duplicate version line")
			}
			seenVersion = true
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return nil, ParseErrorWrap(err, "sdp: invalid version: %q", val)
			}
			sdp.Version = n
			continue
		}

		if media != nil {
			if err := parseMediaLine(media, typ, val); err != nil {
				return nil, err
			}
		} else {
			if err := parseSessionLine(sdp, typ, val); err != nil {
				return nil, err
			}
		}

		if err != nil {
			break
		}
	}

	if !seenVersion {
		return nil, ParseError("sdp: missing version (v=) line")
	}
	if sdp.Version != 0 {
		return nil, ParseError("sdp: protocol version must be 0, got %d", sdp.Version)
	}
	if sdp.Origin.Username == "" {
		return nil, ParseError("sdp: missing origin (o=) line")
	}
	if sdp.SessionName == "" {
		return nil, ParseError("sdp: missing session name (s=) line")
	}
	if len(sdp.Times) == 0 {
		return nil, ParseError("sdp: missing time (t=) line")
	}

	return sdp, nil
}

func parseSessionLine(sdp *SDP, typ byte, val string) error {
	switch typ {
	case 'o':
		o, err := parseOrigin(val)
		if err != nil {
			return err
		}
		sdp.Origin = o
	case 's':
		sdp.SessionName = val
	case 'i':
		sdp.SessionInfo = val
	case 'u':
		sdp.URI = val
	case 'e':
		sdp.Emails = append(sdp.Emails, val)
	case 'p':
		sdp.Phones = append(sdp.Phones, val)
	case 'c':
		c, err := parseConnection(val)
		if err != nil {
			return err
		}
		sdp.Connection = c
	case 'b':
		b, err := parseBandwidth(val)
		if err != nil {
			return err
		}
		sdp.Bandwidths = append(sdp.Bandwidths, b)
	case 't':
		t, err := parseTime(val)
		if err != nil {
			return err
		}
		sdp.Times = append(sdp.Times, t)
	case 'r':
		if len(sdp.Times) == 0 {
			return ParseError("sdp: repeat line before any time line")
		}
		r, err := parseRepeat(val)
		if err != nil {
			return err
		}
		last := len(sdp.Times) - 1
		sdp.Times[last].Repeats = append(sdp.Times[last].Repeats, r)
	case 'z':
		sdp.TimeZone = val
	case 'k':
		k, err := parseEncryption(val)
		if err != nil {
			return err
		}
		sdp.Encryption = k
	case 'a':
		sdp.Attributes = append(sdp.Attributes, parseAttribute(val))
	default:
		return ParseError("sdp: unknown session-level field type: %c", typ)
	}
	return nil
}

func parseMediaLine(md *MediaDescription, typ byte, val string) error {
	switch typ {
	case 'i':
		md.Title = val
	case 'c':
		c, err := parseConnection(val)
		if err != nil {
			return err
		}
		md.Connection = c
	case 'b':
		b, err := parseBandwidth(val)
		if err != nil {
			return err
		}
		md.Bandwidths = append(md.Bandwidths, b)
	case 'k':
		k, err := parseEncryption(val)
		if err != nil {
			return err
		}
		md.Encryption = k
	case 'a':
		md.Attributes = append(md.Attributes, parseAttribute(val))
	default:
		return ParseError("sdp: unknown media-level field type: %c", typ)
	}
	return nil
}

func parseOrigin(val string) (Origin, error) {
	f1, rest, ok := strings.Cut(val, " ")
	if !ok {
		return Origin{}, ParseError("sdp: invalid origin: %q", val)
	}
	f2, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return Origin{}, ParseError("sdp: invalid origin: %q", val)
	}
	f3, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return Origin{}, ParseError("sdp: invalid origin: %q", val)
	}
	f4, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return Origin{}, ParseError("sdp: invalid origin: %q", val)
	}
	f5, f6, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return Origin{}, ParseError("sdp: invalid origin: %q", val)
	}
	return Origin{
		Username:       f1,
		SessionID:      f2,
		SessionVersion: f3,
		NetworkType:    f4,
		AddressType:    f5,
		Address:        f6,
	}, nil
}

func parseConnection(val string) (*ConnectionInfo, error) {
	f1, rest, ok := strings.Cut(val, " ")
	if !ok {
		return nil, ParseError("sdp: invalid connection: %q", val)
	}
	f2, f3, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return nil, ParseError("sdp: invalid connection: %q", val)
	}
	return &ConnectionInfo{
		NetworkType: f1,
		AddressType: f2,
		Address:     f3,
	}, nil
}

func parseBandwidth(val string) (BandwidthInfo, error) {
	before, after, ok := strings.Cut(val, ":")
	if !ok {
		return BandwidthInfo{}, ParseError("sdp: invalid bandwidth: %q", val)
	}
	n, err := strconv.Atoi(strings.TrimSpace(after))
	if err != nil {
		return BandwidthInfo{}, ParseErrorWrap(err, "sdp: invalid bandwidth value: %q", val)
	}
	return BandwidthInfo{Type: strings.TrimSpace(before), Value: n}, nil
}

func parseTime(val string) (TimeDescription, error) {
	startStr, rest, ok := strings.Cut(val, " ")
	if !ok {
		return TimeDescription{}, ParseError("sdp: invalid time: %q", val)
	}
	stopStr := strings.TrimSpace(rest)
	if stopStr == "" {
		return TimeDescription{}, ParseError("sdp: invalid time: %q", val)
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		return TimeDescription{}, ParseErrorWrap(err, "sdp: invalid time start: %q", val)
	}
	stop, err := strconv.ParseInt(stopStr, 10, 64)
	if err != nil {
		return TimeDescription{}, ParseErrorWrap(err, "sdp: invalid time stop: %q", val)
	}
	return TimeDescription{Start: start, Stop: stop}, nil
}

func parseRepeat(val string) (RepeatInfo, error) {
	intvStr, rest, ok := strings.Cut(val, " ")
	if !ok {
		return RepeatInfo{}, ParseError("sdp: invalid repeat: %q", val)
	}
	durStr, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return RepeatInfo{}, ParseError("sdp: invalid repeat: %q", val)
	}
	interval, err := strconv.ParseInt(intvStr, 10, 64)
	if err != nil {
		return RepeatInfo{}, ParseErrorWrap(err, "sdp: invalid repeat interval: %q", val)
	}
	duration, err := strconv.ParseInt(durStr, 10, 64)
	if err != nil {
		return RepeatInfo{}, ParseErrorWrap(err, "sdp: invalid repeat duration: %q", val)
	}
	var offsets []int64
	rest = strings.TrimLeft(rest, " ")
	for rest != "" {
		var oStr string
		oStr, rest, _ = strings.Cut(rest, " ")
		n, err := strconv.ParseInt(oStr, 10, 64)
		if err != nil {
			return RepeatInfo{}, ParseErrorWrap(err, "sdp: invalid repeat offset: %q", oStr)
		}
		offsets = append(offsets, n)
		rest = strings.TrimLeft(rest, " ")
	}
	return RepeatInfo{Interval: interval, Duration: duration, Offsets: offsets}, nil
}

func parseEncryption(val string) (*EncryptionKey, error) {
	before, after, ok := strings.Cut(val, ":")
	if !ok {
		return &EncryptionKey{Method: val, Key: ""}, nil
	}
	return &EncryptionKey{Method: before, Key: after}, nil
}

func parseAttribute(val string) Attribute {
	k, v, found := strings.Cut(val, ":")
	if found {
		return Attribute{Key: k, Value: v}
	}
	return Attribute{Key: val}
}

func parseMedia(val string) (MediaDescription, error) {
	typ, rest, ok := strings.Cut(val, " ")
	if !ok {
		return MediaDescription{}, ParseError("sdp: invalid media: %q", val)
	}
	portStr, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return MediaDescription{}, ParseError("sdp: invalid media: %q", val)
	}
	proto, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return MediaDescription{}, ParseError("sdp: invalid media: %q", val)
	}

	md := MediaDescription{
		Type:  typ,
		Proto: proto,
	}

	portCount := 1
	if idx := strings.IndexByte(portStr, '/'); idx >= 0 {
		n, err := strconv.Atoi(portStr[idx+1:])
		if err != nil {
			return MediaDescription{}, ParseErrorWrap(err, "sdp: invalid media port count: %q", val)
		}
		portCount = n
		portStr = portStr[:idx]
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return MediaDescription{}, ParseErrorWrap(err, "sdp: invalid media port: %q", val)
	}
	md.Port = port
	md.PortCount = portCount

	rest = strings.TrimLeft(rest, " ")
	if rest != "" {
		md.Fmt = strings.Split(rest, " ")
	}

	return md, nil
}

// String renders the session description back to SDP text format with CRLF
// line endings, suitable for use as a SIP body.
func (s *SDP) String() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("v=%d\r\n", s.Version))
	b.WriteString(fmt.Sprintf("o=%s %s %s %s %s %s\r\n",
		s.Origin.Username, s.Origin.SessionID, s.Origin.SessionVersion,
		s.Origin.NetworkType, s.Origin.AddressType, s.Origin.Address))
	b.WriteString(fmt.Sprintf("s=%s\r\n", s.SessionName))
	if s.SessionInfo != "" {
		b.WriteString(fmt.Sprintf("i=%s\r\n", s.SessionInfo))
	}
	if s.URI != "" {
		b.WriteString(fmt.Sprintf("u=%s\r\n", s.URI))
	}
	for _, e := range s.Emails {
		b.WriteString(fmt.Sprintf("e=%s\r\n", e))
	}
	for _, p := range s.Phones {
		b.WriteString(fmt.Sprintf("p=%s\r\n", p))
	}
	if s.Connection != nil {
		b.WriteString(fmt.Sprintf("c=%s %s %s\r\n",
			s.Connection.NetworkType, s.Connection.AddressType, s.Connection.Address))
	}
	for _, bw := range s.Bandwidths {
		b.WriteString(fmt.Sprintf("b=%s:%d\r\n", bw.Type, bw.Value))
	}
	for _, td := range s.Times {
		b.WriteString(fmt.Sprintf("t=%d %d\r\n", td.Start, td.Stop))
		for _, r := range td.Repeats {
			b.WriteString(fmt.Sprintf("r=%d %d", r.Interval, r.Duration))
			for _, o := range r.Offsets {
				b.WriteString(fmt.Sprintf(" %d", o))
			}
			b.WriteString("\r\n")
		}
	}
	if s.TimeZone != "" {
		b.WriteString(fmt.Sprintf("z=%s\r\n", s.TimeZone))
	}
	if s.Encryption != nil {
		if s.Encryption.Key != "" {
			b.WriteString(fmt.Sprintf("k=%s:%s\r\n", s.Encryption.Method, s.Encryption.Key))
		} else {
			b.WriteString(fmt.Sprintf("k=%s\r\n", s.Encryption.Method))
		}
	}
	for _, a := range s.Attributes {
		if a.Value != "" {
			b.WriteString(fmt.Sprintf("a=%s:%s\r\n", a.Key, a.Value))
		} else {
			b.WriteString(fmt.Sprintf("a=%s\r\n", a.Key))
		}
	}
	for _, md := range s.MediaDescs {
		portStr := strconv.Itoa(md.Port)
		if md.PortCount > 1 {
			portStr += "/" + strconv.Itoa(md.PortCount)
		}
		b.WriteString(fmt.Sprintf("m=%s %s %s %s\r\n",
			md.Type, portStr, md.Proto, strings.Join(md.Fmt, " ")))
		if md.Title != "" {
			b.WriteString(fmt.Sprintf("i=%s\r\n", md.Title))
		}
		if md.Connection != nil {
			b.WriteString(fmt.Sprintf("c=%s %s %s\r\n",
				md.Connection.NetworkType, md.Connection.AddressType, md.Connection.Address))
		}
		for _, bw := range md.Bandwidths {
			b.WriteString(fmt.Sprintf("b=%s:%d\r\n", bw.Type, bw.Value))
		}
		if md.Encryption != nil {
			if md.Encryption.Key != "" {
				b.WriteString(fmt.Sprintf("k=%s:%s\r\n", md.Encryption.Method, md.Encryption.Key))
			} else {
				b.WriteString(fmt.Sprintf("k=%s\r\n", md.Encryption.Method))
			}
		}
		for _, a := range md.Attributes {
			if a.Value != "" {
				b.WriteString(fmt.Sprintf("a=%s:%s\r\n", a.Key, a.Value))
			} else {
				b.WriteString(fmt.Sprintf("a=%s\r\n", a.Key))
			}
		}
	}
	return b.String()
}

// StartTime returns the session start time as a Go time.Time, converting
// from the NTP timestamp in the first time description. Returns the zero
// time if no times are present or the start time is zero (unbounded).
func (s *SDP) StartTime() time.Time {
	if len(s.Times) == 0 {
		return time.Time{}
	}
	return ntpToTime(s.Times[0].Start)
}

// StopTime returns the session stop time as a Go time.Time, converting
// from the NTP timestamp in the first time description. Returns the zero
// time if no times are present or the stop time is zero (unbounded).
func (s *SDP) StopTime() time.Time {
	if len(s.Times) == 0 {
		return time.Time{}
	}
	return ntpToTime(s.Times[0].Stop)
}

func ntpToTime(ntp int64) time.Time {
	if ntp == 0 {
		return time.Time{}
	}
	base := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	sec := ntp & 0xFFFFFFFF
	return base.Add(time.Duration(sec) * time.Second)
}
