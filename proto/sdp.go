package proto

import (
	"bufio"
	"bytes"
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

// UnmarshalSDP parses a complete SDP session description from r, returning a
// structured SDP value or an error. The reader must provide a complete
// SDP body; parsing stops at the first empty line or EOF.
func UnmarshalSDP(r io.Reader) (*SDP, error) {
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
				return nil, UnmarshalErrorf("sdp: empty body")
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
			return nil, UnmarshalErrorf("sdp: malformed line: %q", line)
		}
		typ := line[0]
		val := line[2:]

		if typ == 'm' {
			md, err := unmarshalMedia(val)
			if err != nil {
				return nil, err
			}
			sdp.MediaDescs = append(sdp.MediaDescs, md)
			media = &sdp.MediaDescs[len(sdp.MediaDescs)-1]
			continue
		}

		if typ == 'v' {
			if seenVersion {
				return nil, UnmarshalErrorf("sdp: duplicate version line")
			}
			seenVersion = true
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return nil, UnmarshalErrorWrap(err, "sdp: invalid version: %q", val)
			}
			sdp.Version = n
			continue
		}

		if media != nil {
			if err := unmarshalMediaLine(media, typ, val); err != nil {
				return nil, err
			}
		} else {
			if err := unmarshalSessionLine(sdp, typ, val); err != nil {
				return nil, err
			}
		}

		if err != nil {
			break
		}
	}

	if !seenVersion {
		return nil, UnmarshalErrorf("sdp: missing version (v=) line")
	}
	if sdp.Version != 0 {
		return nil, UnmarshalErrorf("sdp: protocol version must be 0, got %d", sdp.Version)
	}
	if sdp.Origin.Username == "" {
		return nil, UnmarshalErrorf("sdp: missing origin (o=) line")
	}
	if sdp.SessionName == "" {
		return nil, UnmarshalErrorf("sdp: missing session name (s=) line")
	}
	if len(sdp.Times) == 0 {
		return nil, UnmarshalErrorf("sdp: missing time (t=) line")
	}

	return sdp, nil
}

func unmarshalSessionLine(sdp *SDP, typ byte, val string) error {
	switch typ {
	case 'o':
		o, err := unmarshalOrigin(val)
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
		c, err := unmarshalConnection(val)
		if err != nil {
			return err
		}
		sdp.Connection = c
	case 'b':
		b, err := unmarshalBandwidth(val)
		if err != nil {
			return err
		}
		sdp.Bandwidths = append(sdp.Bandwidths, b)
	case 't':
		t, err := unmarshalTime(val)
		if err != nil {
			return err
		}
		sdp.Times = append(sdp.Times, t)
	case 'r':
		if len(sdp.Times) == 0 {
			return UnmarshalErrorf("sdp: repeat line before any time line")
		}
		r, err := unmarshalRepeat(val)
		if err != nil {
			return err
		}
		last := len(sdp.Times) - 1
		sdp.Times[last].Repeats = append(sdp.Times[last].Repeats, r)
	case 'z':
		sdp.TimeZone = val
	case 'k':
		k, err := unmarshalEncryption(val)
		if err != nil {
			return err
		}
		sdp.Encryption = k
	case 'a':
		sdp.Attributes = append(sdp.Attributes, unmarshalAttribute(val))
	default:
		return UnmarshalErrorf("sdp: unknown session-level field type: %c", typ)
	}
	return nil
}

func unmarshalMediaLine(md *MediaDescription, typ byte, val string) error {
	switch typ {
	case 'i':
		md.Title = val
	case 'c':
		c, err := unmarshalConnection(val)
		if err != nil {
			return err
		}
		md.Connection = c
	case 'b':
		b, err := unmarshalBandwidth(val)
		if err != nil {
			return err
		}
		md.Bandwidths = append(md.Bandwidths, b)
	case 'k':
		k, err := unmarshalEncryption(val)
		if err != nil {
			return err
		}
		md.Encryption = k
	case 'a':
		md.Attributes = append(md.Attributes, unmarshalAttribute(val))
	default:
		return UnmarshalErrorf("sdp: unknown media-level field type: %c", typ)
	}
	return nil
}

func unmarshalOrigin(val string) (Origin, error) {
	f1, rest, ok := strings.Cut(val, " ")
	if !ok {
		return Origin{}, UnmarshalErrorf("sdp: invalid origin: %q", val)
	}
	f2, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return Origin{}, UnmarshalErrorf("sdp: invalid origin: %q", val)
	}
	f3, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return Origin{}, UnmarshalErrorf("sdp: invalid origin: %q", val)
	}
	f4, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return Origin{}, UnmarshalErrorf("sdp: invalid origin: %q", val)
	}
	f5, f6, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return Origin{}, UnmarshalErrorf("sdp: invalid origin: %q", val)
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

func unmarshalConnection(val string) (*ConnectionInfo, error) {
	f1, rest, ok := strings.Cut(val, " ")
	if !ok {
		return nil, UnmarshalErrorf("sdp: invalid connection: %q", val)
	}
	f2, f3, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return nil, UnmarshalErrorf("sdp: invalid connection: %q", val)
	}
	return &ConnectionInfo{
		NetworkType: f1,
		AddressType: f2,
		Address:     f3,
	}, nil
}

func unmarshalBandwidth(val string) (BandwidthInfo, error) {
	before, after, ok := strings.Cut(val, ":")
	if !ok {
		return BandwidthInfo{}, UnmarshalErrorf("sdp: invalid bandwidth: %q", val)
	}
	n, err := strconv.Atoi(strings.TrimSpace(after))
	if err != nil {
		return BandwidthInfo{}, UnmarshalErrorWrap(err, "sdp: invalid bandwidth value: %q", val)
	}
	return BandwidthInfo{Type: strings.TrimSpace(before), Value: n}, nil
}

func unmarshalTime(val string) (TimeDescription, error) {
	startStr, rest, ok := strings.Cut(val, " ")
	if !ok {
		return TimeDescription{}, UnmarshalErrorf("sdp: invalid time: %q", val)
	}
	stopStr := strings.TrimSpace(rest)
	if stopStr == "" {
		return TimeDescription{}, UnmarshalErrorf("sdp: invalid time: %q", val)
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		return TimeDescription{}, UnmarshalErrorWrap(err, "sdp: invalid time start: %q", val)
	}
	stop, err := strconv.ParseInt(stopStr, 10, 64)
	if err != nil {
		return TimeDescription{}, UnmarshalErrorWrap(err, "sdp: invalid time stop: %q", val)
	}
	return TimeDescription{Start: start, Stop: stop}, nil
}

func unmarshalRepeat(val string) (RepeatInfo, error) {
	intvStr, rest, ok := strings.Cut(val, " ")
	if !ok {
		return RepeatInfo{}, UnmarshalErrorf("sdp: invalid repeat: %q", val)
	}
	durStr, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return RepeatInfo{}, UnmarshalErrorf("sdp: invalid repeat: %q", val)
	}
	interval, err := strconv.ParseInt(intvStr, 10, 64)
	if err != nil {
		return RepeatInfo{}, UnmarshalErrorWrap(err, "sdp: invalid repeat interval: %q", val)
	}
	duration, err := strconv.ParseInt(durStr, 10, 64)
	if err != nil {
		return RepeatInfo{}, UnmarshalErrorWrap(err, "sdp: invalid repeat duration: %q", val)
	}
	var offsets []int64
	rest = strings.TrimLeft(rest, " ")
	for rest != "" {
		var oStr string
		oStr, rest, _ = strings.Cut(rest, " ")
		n, err := strconv.ParseInt(oStr, 10, 64)
		if err != nil {
			return RepeatInfo{}, UnmarshalErrorWrap(err, "sdp: invalid repeat offset: %q", oStr)
		}
		offsets = append(offsets, n)
		rest = strings.TrimLeft(rest, " ")
	}
	return RepeatInfo{Interval: interval, Duration: duration, Offsets: offsets}, nil
}

func unmarshalEncryption(val string) (*EncryptionKey, error) {
	before, after, ok := strings.Cut(val, ":")
	if !ok {
		return &EncryptionKey{Method: val, Key: ""}, nil
	}
	return &EncryptionKey{Method: before, Key: after}, nil
}

func unmarshalAttribute(val string) Attribute {
	k, v, found := strings.Cut(val, ":")
	if found {
		return Attribute{Key: k, Value: v}
	}
	return Attribute{Key: val}
}

func unmarshalMedia(val string) (MediaDescription, error) {
	typ, rest, ok := strings.Cut(val, " ")
	if !ok {
		return MediaDescription{}, UnmarshalErrorf("sdp: invalid media: %q", val)
	}
	portStr, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return MediaDescription{}, UnmarshalErrorf("sdp: invalid media: %q", val)
	}
	proto, rest, ok := strings.Cut(strings.TrimLeft(rest, " "), " ")
	if !ok {
		return MediaDescription{}, UnmarshalErrorf("sdp: invalid media: %q", val)
	}

	md := MediaDescription{
		Type:  typ,
		Proto: proto,
	}

	portCount := 1
	if idx := strings.IndexByte(portStr, '/'); idx >= 0 {
		n, err := strconv.Atoi(portStr[idx+1:])
		if err != nil {
			return MediaDescription{}, UnmarshalErrorWrap(err, "sdp: invalid media port count: %q", val)
		}
		portCount = n
		portStr = portStr[:idx]
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return MediaDescription{}, UnmarshalErrorWrap(err, "sdp: invalid media port: %q", val)
	}
	md.Port = port
	md.PortCount = portCount

	rest = strings.TrimLeft(rest, " ")
	if rest != "" {
		md.Fmt = strings.Split(rest, " ")
	}

	return md, nil
}

// MarshalSize returns the exact number of bytes needed to represent the
// session description in SDP text format.
func (s *SDP) MarshalSize() int {
	sz := 0

	// v= line
	sz += 2 + intLen(int64(s.Version)) + 2

	// o= line
	sz += 2 + len(s.Origin.Username) + 1 + len(s.Origin.SessionID) + 1 +
		len(s.Origin.SessionVersion) + 1 + len(s.Origin.NetworkType) + 1 +
		len(s.Origin.AddressType) + 1 + len(s.Origin.Address) + 2

	// s= line
	sz += 2 + len(s.SessionName) + 2

	if s.SessionInfo != "" {
		sz += 2 + len(s.SessionInfo) + 2
	}
	if s.URI != "" {
		sz += 2 + len(s.URI) + 2
	}
	for _, e := range s.Emails {
		sz += 2 + len(e) + 2
	}
	for _, p := range s.Phones {
		sz += 2 + len(p) + 2
	}
	if s.Connection != nil {
		sz += 2 + len(s.Connection.NetworkType) + 1 +
			len(s.Connection.AddressType) + 1 +
			len(s.Connection.Address) + 2
	}
	for _, bw := range s.Bandwidths {
		sz += 2 + len(bw.Type) + 1 + intLen(int64(bw.Value)) + 2
	}
	for _, td := range s.Times {
		sz += 2 + intLen(td.Start) + 1 + intLen(td.Stop) + 2
		for _, r := range td.Repeats {
			sz += 2 + intLen(r.Interval) + 1 + intLen(r.Duration)
			for _, o := range r.Offsets {
				sz += 1 + intLen(o)
			}
			sz += 2
		}
	}
	if s.TimeZone != "" {
		sz += 2 + len(s.TimeZone) + 2
	}
	if s.Encryption != nil {
		sz += 2 + len(s.Encryption.Method) + 2
		if s.Encryption.Key != "" {
			sz += 1 + len(s.Encryption.Key)
		}
	}
	for _, a := range s.Attributes {
		sz += 2 + len(a.Key) + 2
		if a.Value != "" {
			sz += 1 + len(a.Value)
		}
	}
	for _, md := range s.MediaDescs {
		// m= line
		sz += 2 + len(md.Type) + 1 + intLen(int64(md.Port))
		if md.PortCount > 1 {
			sz += 1 + intLen(int64(md.PortCount))
		}
		sz += 1 + len(md.Proto) + 1 + joinLen(md.Fmt, " ") + 2

		if md.Title != "" {
			sz += 2 + len(md.Title) + 2
		}
		if md.Connection != nil {
			sz += 2 + len(md.Connection.NetworkType) + 1 +
				len(md.Connection.AddressType) + 1 +
				len(md.Connection.Address) + 2
		}
		for _, bw := range md.Bandwidths {
			sz += 2 + len(bw.Type) + 1 + intLen(int64(bw.Value)) + 2
		}
		if md.Encryption != nil {
			sz += 2 + len(md.Encryption.Method) + 2
			if md.Encryption.Key != "" {
				sz += 1 + len(md.Encryption.Key)
			}
		}
		for _, a := range md.Attributes {
			sz += 2 + len(a.Key) + 2
			if a.Value != "" {
				sz += 1 + len(a.Value)
			}
		}
	}
	return sz
}

// marshalTo is the internal implementation shared by Marshal, MarshalTo, and
// String. sz must equal MarshalSize() and len(buf) >= sz.
func (s *SDP) marshalTo(buf []byte, sz int) int {
	pos := 0

	// v=0\r\n
	buf[pos] = 'v'; buf[pos+1] = '='
	pos += 2
	pos += len(strconv.AppendInt(buf[pos:pos], int64(s.Version), 10))
	buf[pos] = '\r'; buf[pos+1] = '\n'
	pos += 2

	// o=...
	buf[pos] = 'o'; buf[pos+1] = '='
	pos += 2
	pos += copy(buf[pos:], s.Origin.Username)
	buf[pos] = ' '; pos++
	pos += copy(buf[pos:], s.Origin.SessionID)
	buf[pos] = ' '; pos++
	pos += copy(buf[pos:], s.Origin.SessionVersion)
	buf[pos] = ' '; pos++
	pos += copy(buf[pos:], s.Origin.NetworkType)
	buf[pos] = ' '; pos++
	pos += copy(buf[pos:], s.Origin.AddressType)
	buf[pos] = ' '; pos++
	pos += copy(buf[pos:], s.Origin.Address)
	buf[pos] = '\r'; buf[pos+1] = '\n'
	pos += 2

	// s=...
	buf[pos] = 's'; buf[pos+1] = '='
	pos += 2
	pos += copy(buf[pos:], s.SessionName)
	buf[pos] = '\r'; buf[pos+1] = '\n'
	pos += 2

	if s.SessionInfo != "" {
		buf[pos] = 'i'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], s.SessionInfo)
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}
	if s.URI != "" {
		buf[pos] = 'u'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], s.URI)
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}
	for _, e := range s.Emails {
		buf[pos] = 'e'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], e)
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}
	for _, p := range s.Phones {
		buf[pos] = 'p'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], p)
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}

	if s.Connection != nil {
		buf[pos] = 'c'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], s.Connection.NetworkType)
		buf[pos] = ' '; pos++
		pos += copy(buf[pos:], s.Connection.AddressType)
		buf[pos] = ' '; pos++
		pos += copy(buf[pos:], s.Connection.Address)
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}
	for _, bw := range s.Bandwidths {
		buf[pos] = 'b'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], bw.Type)
		buf[pos] = ':'; pos++
		pos += len(strconv.AppendInt(buf[pos:pos], int64(bw.Value), 10))
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}
	for _, td := range s.Times {
		buf[pos] = 't'; buf[pos+1] = '='
		pos += 2
		pos += len(strconv.AppendInt(buf[pos:pos], td.Start, 10))
		buf[pos] = ' '; pos++
		pos += len(strconv.AppendInt(buf[pos:pos], td.Stop, 10))
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2

		for _, r := range td.Repeats {
			buf[pos] = 'r'; buf[pos+1] = '='
			pos += 2
			pos += len(strconv.AppendInt(buf[pos:pos], r.Interval, 10))
			buf[pos] = ' '; pos++
			pos += len(strconv.AppendInt(buf[pos:pos], r.Duration, 10))
			for _, o := range r.Offsets {
				buf[pos] = ' '; pos++
				pos += len(strconv.AppendInt(buf[pos:pos], o, 10))
			}
			buf[pos] = '\r'; buf[pos+1] = '\n'
			pos += 2
		}
	}
	if s.TimeZone != "" {
		buf[pos] = 'z'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], s.TimeZone)
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}
	if s.Encryption != nil {
		buf[pos] = 'k'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], s.Encryption.Method)
		if s.Encryption.Key != "" {
			buf[pos] = ':'; pos++
			pos += copy(buf[pos:], s.Encryption.Key)
		}
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}
	for _, a := range s.Attributes {
		buf[pos] = 'a'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], a.Key)
		if a.Value != "" {
			buf[pos] = ':'; pos++
			pos += copy(buf[pos:], a.Value)
		}
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2
	}

	for _, md := range s.MediaDescs {
		buf[pos] = 'm'; buf[pos+1] = '='
		pos += 2
		pos += copy(buf[pos:], md.Type)
		buf[pos] = ' '; pos++
		pos += len(strconv.AppendInt(buf[pos:pos], int64(md.Port), 10))
		if md.PortCount > 1 {
			buf[pos] = '/'; pos++
			pos += len(strconv.AppendInt(buf[pos:pos], int64(md.PortCount), 10))
		}
		buf[pos] = ' '; pos++
		pos += copy(buf[pos:], md.Proto)
		buf[pos] = ' '; pos++
		for i, f := range md.Fmt {
			if i > 0 {
				buf[pos] = ' '; pos++
			}
			pos += copy(buf[pos:], f)
		}
		buf[pos] = '\r'; buf[pos+1] = '\n'
		pos += 2

		if md.Title != "" {
			buf[pos] = 'i'; buf[pos+1] = '='
			pos += 2
			pos += copy(buf[pos:], md.Title)
			buf[pos] = '\r'; buf[pos+1] = '\n'
			pos += 2
		}
		if md.Connection != nil {
			buf[pos] = 'c'; buf[pos+1] = '='
			pos += 2
			pos += copy(buf[pos:], md.Connection.NetworkType)
			buf[pos] = ' '; pos++
			pos += copy(buf[pos:], md.Connection.AddressType)
			buf[pos] = ' '; pos++
			pos += copy(buf[pos:], md.Connection.Address)
			buf[pos] = '\r'; buf[pos+1] = '\n'
			pos += 2
		}
		for _, bw := range md.Bandwidths {
			buf[pos] = 'b'; buf[pos+1] = '='
			pos += 2
			pos += copy(buf[pos:], bw.Type)
			buf[pos] = ':'; pos++
			pos += len(strconv.AppendInt(buf[pos:pos], int64(bw.Value), 10))
			buf[pos] = '\r'; buf[pos+1] = '\n'
			pos += 2
		}
		if md.Encryption != nil {
			buf[pos] = 'k'; buf[pos+1] = '='
			pos += 2
			pos += copy(buf[pos:], md.Encryption.Method)
			if md.Encryption.Key != "" {
				buf[pos] = ':'; pos++
				pos += copy(buf[pos:], md.Encryption.Key)
			}
			buf[pos] = '\r'; buf[pos+1] = '\n'
			pos += 2
		}
		for _, a := range md.Attributes {
			buf[pos] = 'a'; buf[pos+1] = '='
			pos += 2
			pos += copy(buf[pos:], a.Key)
			if a.Value != "" {
				buf[pos] = ':'; pos++
				pos += copy(buf[pos:], a.Value)
			}
			buf[pos] = '\r'; buf[pos+1] = '\n'
			pos += 2
		}
	}

	return pos
}

// MarshalTo serializes the session description into buf, which must be at
// least MarshalSize() bytes. Returns the number of bytes written.
func (s *SDP) MarshalTo(buf []byte) (int, error) {
	sz := s.MarshalSize()
	if len(buf) < sz {
		return 0, fmt.Errorf("sdp: buffer too small for marshal")
	}
	return s.marshalTo(buf, sz), nil
}

// Marshal serializes the session description to SDP text format.
func (s *SDP) Marshal() ([]byte, error) {
	sz := s.MarshalSize()
	buf := make([]byte, sz)
	s.marshalTo(buf, sz)
	return buf, nil
}

// String renders the session description back to SDP text format with CRLF
// line endings, suitable for use as a SIP body.
func (s *SDP) String() string {
	sz := s.MarshalSize()
	buf := make([]byte, sz)
	s.marshalTo(buf, sz)
	return string(buf)
}

func joinLen(strs []string, sep string) int {
	if len(strs) == 0 {
		return 0
	}
	n := len(sep) * (len(strs) - 1)
	for _, s := range strs {
		n += len(s)
	}
	return n
}

func intLen(n int64) int {
	if n == 0 {
		return 1
	}
	count := 0
	for m := n; m > 0; m /= 10 {
		count++
	}
	return count
}

// UnmarshalSDPBytes parses a complete SDP session description from a byte slice.
func UnmarshalSDPBytes(data []byte) (*SDP, error) {
	return UnmarshalSDP(bytes.NewReader(data))
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
