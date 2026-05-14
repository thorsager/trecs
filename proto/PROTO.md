# proto

Package `proto` provides wire-format parsing (`Unmarshal*`) and serialization (`Marshal*`) for SIP, SDP, RTP, and RTCP.

All serializable types follow a consistent three-method pattern:

```go
MarshalSize() int
MarshalTo(buf []byte) (int, error)
Marshal() ([]byte, error)
```

## SIP

### Reading

```go
// From a TCP stream (reuses reader for subsequent messages).
reader := bufio.NewReader(conn)
msg, err := proto.UnmarshalSIP(reader)

// From a UDP datagram (byte slice, self-contained).
msg, err := proto.UnmarshalSIPDatagram(data)
```

Parsed fields are available directly:

```go
fmt.Println(msg.StartLine())  // "INVITE sip:bob@example.com SIP/2.0"
fmt.Println(msg.Method())     // "INVITE"
fmt.Println(msg.StatusCode()) // 200
fmt.Println(msg.IsRequest())  // true
fmt.Println(string(msg.Body))

from, _ := msg.From()
fmt.Println(from.URI, from.Tag)

to, _ := msg.To()
fmt.Println(to.URI, to.DisplayName)

via := msg.Headers.GetFirst("Via")
cseq := msg.Headers.Get("CSeq")   // []string
```

### Writing

SIPMessage wraps an unexported start-line. The primary write workflow is read-modify-write:

```go
// Parse an existing message, modify it, and serialize it back.
msg, _ := proto.UnmarshalSIPDatagram(data)
msg.Headers.Set("Max-Forwards", []string{"70"})
msg.Body = []byte("v=0\r\no=...")

// Marshal to wire format.
data, err := msg.Marshal()

// Compact form (uses single-char header keys like "v" for "Via").
compact, err := msg.MarshalCompact()

// Pre-allocated buffer.
buf := make([]byte, msg.MarshalSize())
n, err := msg.MarshalTo(buf)
```

Headers with struct fields (`CSeq`, `Content-Length`) are emitted from their
struct field counterparts rather than the `Headers` map. Headers sort
alphabetically on output.

## SDP

### Reading 

```go
// From an io.Reader.
sdp, err := proto.UnmarshalSDP(bytes.NewReader(data))

// From a byte slice.
sdp, err := proto.UnmarshalSDPBytes(data)

fmt.Println(sdp.Origin.Username)          // o= line
fmt.Println(sdp.SessionName)              // s= line
fmt.Println(sdp.Connection.Address)       // c= line
for _, md := range sdp.MediaDescs {
    fmt.Println(md.Type, md.Port, md.Proto) // m= line
    for _, a := range md.Attributes {
        fmt.Println(a.Key, a.Value)          // a= line
    }
}
fmt.Println(sdp.StartTime())  // t= as time.Time
fmt.Println(sdp.StopTime())
```

### Writing

```go
sdp := &proto.SDP{
    Origin: proto.Origin{
        Username: "-", SessionID: "12345", SessionVersion: "2",
        NetworkType: "IN", AddressType: "IP4", Address: "192.168.1.1",
    },
    SessionName: "Phone Call",
    Times: []proto.TimeDescription{
        {Start: 0, Stop: 0}, // unbounded
    },
    MediaDescs: []proto.MediaDescription{
        {Type: "audio", Port: 5004, Proto: "RTP/AVP", Fmt: []string{"0", "8"}},
    },
}

data, err := sdp.Marshal()
buf := make([]byte, sdp.MarshalSize())
n, err := sdp.MarshalTo(buf)
```

### Wire format (string)

```go
s := sdp.String() // "v=0\r\no=- 12345 2 IN IP4 192.168.1.1\r\ns=Phone Call\r\n..."
```

## RTP

### Reading

```go
pkt, err := proto.UnmarshalRTP(data)
fmt.Println(pkt.Header.SequenceNumber)
fmt.Println(pkt.Header.Timestamp)
fmt.Println(pkt.Header.SSRC)
fmt.Println(pkt.Header.PayloadType)
fmt.Println(len(pkt.Payload))
```

### Writing

```go
pkt := &proto.RTPPacket{
    Header: proto.RTPHeader{
        Version:        2,
        PayloadType:    0,   // PCMU
        SequenceNumber: 1,
        Timestamp:      160,
        SSRC:           0xdeadbeef,
        Marker:         true,
    },
    Payload: []byte{...},
}

data, err := pkt.Marshal()
buf := make([]byte, pkt.MarshalSize())
n, err := pkt.MarshalTo(buf)
```


## RTCP

### Reading

RTCP is compound: a single UDP payload may contain multiple bundled packets.

```go
packets, err := proto.UnmarshalRTCP(data)
for _, pkt := range packets {
    switch p := pkt.(type) {
    case *proto.SenderReport:
        fmt.Println(p.NTPTime, p.RTPTime, p.PacketCount)
    case *proto.ReceiverReport:
        fmt.Println(p.SSRC)
    case *proto.SourceDescription:
        for _, chunk := range p.Chunks {
            fmt.Println(chunk.Source)
        }
    case *proto.Goodbye:
        fmt.Println(p.Sources, p.Reason)
    case *proto.ApplicationDefined:
        fmt.Println(p.Name, p.Data)
    }
}
```

### Writing

```go
// Individual packet.
sr := &proto.SenderReport{
    SSRC: 0xdeadbeef,
    NTPTime: 0, RTPTime: 0, PacketCount: 0, OctetCount: 0,
}
data, err := sr.Marshal()

// Compound packet (one or more RTCP packets bundled together).
compound, err := proto.MarshalRTCP([]proto.RTCPPacket{sr, bye})

// Pre-allocated buffer.
buf := make([]byte, sr.MarshalSize())
n, err := sr.MarshalTo(buf)
```


## Error Handling

All unmarshal functions return `*proto.UnmarshalError` which wraps an underlying cause and supports `errors.Unwrap`:

```go
msg, err := proto.UnmarshalSIP(reader)
var umErr *proto.UnmarshalError
if errors.As(err, &umErr) {
    fmt.Println(umErr.Msg)   // "Content-Length required for stream transport"
    fmt.Println(umErr.Cause) // underlying error, if any
}
```
