package b2bua

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
	"github.com/thorsager/trecs/proto"
)

type bobUAS struct {
	t         *testing.T
	ts        *integrationtest.TestServer
	ctx       context.Context
	cancel    context.CancelFunc
	ua        *sipgo.UserAgent
	client    *sipgo.Client
	sipConn   *net.UDPConn
	tcpLn     net.Listener
	rtp       *net.UDPConn
	transport string
	port      int
	sipAddr   *net.UDPAddr
	ready     chan struct{}

	mu               sync.Mutex
	callID           string
	fromTag          string
	toTag            string
	cseq             int
	serverContact    string
	answered         bool
	rejected         bool
	ringBeforeAnswer bool          // if true, send 180 Ringing then wait for answerNow
	answerNow        chan struct{} // signals ringBeforeAnswer mode to send 200 OK
	byeReceived      chan struct{}
	byeOnce          sync.Once
	rtpCount         chan int
	sipDone          chan struct{}
	serverRTPPortB   int
	serverRTPPortBCh chan int

	expectedClientSSRC uint32 // SSRC that Alice will send (set before INVITE)
	expectedBobSSRC    uint32 // SSRC that Bob will send (set before INVITE)
}

// outboundBobUAS is a Bob user-agent that registers with ;ob (RFC 5626 outbound)
// so the server reuses the registration TCP connection for incoming INVITEs.
// The sipgo UA's OnRequest handler processes INVITE/BYE on that connection.
type outboundBobUAS struct {
	t      *testing.T
	ts     *integrationtest.TestServer
	ctx    context.Context
	cancel context.CancelFunc
	ua     *sipgo.UserAgent
	client *sipgo.Client
	rtp    *net.UDPConn

	mu               sync.Mutex
	byeReceived      chan struct{}
	byeOnce          sync.Once
	rtpCount         chan int
	serverRTPPortB   int
	serverRTPPortBCh chan int

	expectedClientSSRC uint32
	expectedBobSSRC    uint32
}

func newOutboundBobUAS(t *testing.T, ts *integrationtest.TestServer) *outboundBobUAS {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	b := &outboundBobUAS{
		t:                t,
		ts:               ts,
		ctx:              ctx,
		cancel:           cancel,
		byeReceived:      make(chan struct{}),
		rtpCount:         make(chan int, 1),
		serverRTPPortBCh: make(chan int, 1),
	}

	var err error
	b.ua, err = sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	// Register OnRequest handler for INVITE and BYE on the UA's transaction layer
	b.ua.TransactionLayer().OnRequest(func(req *sipgo_sip.Request, tx *sipgo_sip.ServerTx) {
		switch req.Method {
		case "INVITE":
			b.t.Logf("Outbound Bob received INVITE: %s", req.Short())

			// Extract server's RTP port B from SDP
			body := req.Body()
			if len(body) > 0 {
				sdp, err := proto.UnmarshalSDPBytes(body)
				if err == nil {
					for _, m := range sdp.MediaDescs {
						if m.Type == "audio" {
							b.mu.Lock()
							b.serverRTPPortB = m.Port
							b.mu.Unlock()
							select {
							case b.serverRTPPortBCh <- m.Port:
							default:
							}
							break
						}
					}
				}
			}

			// Build SDP answer
			rtpPort := b.rtp.LocalAddr().(*net.UDPAddr).Port
			sdp := fmt.Sprintf("v=0\r\no=- %d 1 IN IP4 127.0.0.1\r\ns=bob\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
				time.Now().UnixNano(), rtpPort)

			resp := sipgo_sip.NewResponseFromRequest(req, 200, "OK", []byte(sdp))
			resp.AppendHeader(sipgo_sip.NewHeader("Content-Type", "application/sdp"))
			if err := tx.Respond(resp); err != nil {
				b.t.Logf("Outbound Bob failed to respond 200: %v", err)
			} else {
				b.t.Logf("Outbound Bob sent 200 OK (RTP port %d)", rtpPort)
				go receiveRTP(b.rtp, b.rtpCount, b.ctx, b.expectedClientSSRC)
			}

		case "BYE":
			b.t.Logf("Outbound Bob received BYE: %s", req.Short())
			resp := sipgo_sip.NewResponseFromRequest(req, 200, "OK", nil)
			_ = tx.Respond(resp)
			b.byeOnce.Do(func() { close(b.byeReceived) })
		}
	})

	b.client, err = sipgo.NewClient(b.ua, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)

	b.rtp, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	return b
}

func (b *outboundBobUAS) register(t *testing.T) {
	t.Helper()

	contact := "<sip:bob@127.0.0.1:0;transport=tcp;ob>"
	req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
		User: "bob",
		Host: b.ts.Domain,
		Port: b.ts.TCPPort,
	})
	req.AppendHeader(sipgo_sip.NewHeader("Contact", contact))
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:bob@%s>;tag=bob-outbound", b.ts.Domain)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>", b.ts.Domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "b2bua-bob-outbound-"+b.ts.Domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 REGISTER"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	req.SetTransport("TCP")

	res, err := b.client.Do(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Bob outbound registration should succeed")
	t.Logf("Bob outbound registered")
}

func (b *outboundBobUAS) close() {
	b.cancel()
	if b.rtp != nil {
		b.rtp.Close()
	}
	if b.client != nil {
		b.client.Close()
	}
	if b.ua != nil {
		b.ua.Close()
	}
}

func newBobUAS(t *testing.T, ts *integrationtest.TestServer, transport string) *bobUAS {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	b := &bobUAS{
		t:                t,
		ts:               ts,
		ctx:              ctx,
		cancel:           cancel,
		transport:        transport,
		ringBeforeAnswer: false,
		answerNow:        make(chan struct{}, 1),
		byeReceived:      make(chan struct{}),
		rtpCount:         make(chan int, 1),
		sipDone:          make(chan struct{}),
		ready:            make(chan struct{}),
		serverRTPPortBCh: make(chan int, 1),
	}

	var err error
	b.ua, err = sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	switch transport {
	case "udp":
		addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
		conn, err := net.ListenUDP("udp", addr)
		require.NoError(t, err)
		b.sipConn = conn
		b.sipAddr = conn.LocalAddr().(*net.UDPAddr)
		b.port = b.sipAddr.Port
		go b.handleSIPUDP()
		<-b.ready
	case "tcp":
		var lc net.ListenConfig
		b.tcpLn, err = lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		b.port = b.tcpLn.Addr().(*net.TCPAddr).Port
		go b.handleSIPTCP()
		<-b.ready
	}
	b.client, err = sipgo.NewClient(b.ua, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)

	b.rtp, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	return b
}

func (b *bobUAS) handleSIPUDP() {
	defer close(b.sipDone)
	close(b.ready)

	buf := make([]byte, 4096)
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		_ = b.sipConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, remoteAddr, err := b.sipConn.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		msg := string(buf[:n])
		writeFunc := func(data []byte) error {
			_, err := b.sipConn.WriteToUDP(data, remoteAddr)
			return err
		}
		b.t.Logf("Bob received %d bytes from %s: %q", n, remoteAddr, msg[:min(n, 100)])

		switch {
		case strings.HasPrefix(msg, "INVITE"):
			b.handleIncomingInvite(msg, writeFunc)
		case strings.HasPrefix(msg, "BYE"):
			b.handleIncomingBye(msg, writeFunc)
		case strings.HasPrefix(msg, "CANCEL"):
			b.handleIncomingCancel(msg, writeFunc)
		}
	}
}

func (b *bobUAS) handleSIPTCP() {
	defer close(b.sipDone)
	close(b.ready)

	for {
		conn, err := b.tcpLn.Accept()
		if err != nil {
			select {
			case <-b.ctx.Done():
				return
			default:
				continue
			}
		}
		go b.handleTCPConn(conn)
	}
}

func (b *bobUAS) handleTCPConn(conn net.Conn) {
	defer conn.Close()

	readBuf := make([]byte, 4096)
	var buf bytes.Buffer
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := conn.Read(readBuf)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return
		}
		buf.Write(readBuf[:n])

		for {
			raw := buf.Bytes()
			// Look for end of headers
			headerEnd := bytes.Index(raw, []byte("\r\n\r\n"))
			if headerEnd == -1 {
				break
			}
			// Find Content-Length
			cl := 0
			headers := string(raw[:headerEnd])
			for _, line := range strings.Split(headers, "\r\n") {
				if strings.HasPrefix(strings.ToLower(line), "content-length:") {
					cl, _ = strconv.Atoi(strings.TrimSpace(line[len("content-length:"):]))
					break
				}
			}
			totalLen := headerEnd + 4 + cl
			if buf.Len() < totalLen {
				break
			}

			msg := string(raw[:totalLen])
			buf.Next(totalLen)
			writeFunc := func(data []byte) error {
				_, err := conn.Write(data)
				return err
			}
			b.t.Logf("Bob received %d bytes on TCP: %q", totalLen, msg[:min(len(msg), 100)])

			switch {
			case strings.HasPrefix(msg, "INVITE"):
				b.handleIncomingInvite(msg, writeFunc)
			case strings.HasPrefix(msg, "BYE"):
				b.handleIncomingBye(msg, writeFunc)
			case strings.HasPrefix(msg, "CANCEL"):
				b.handleIncomingCancel(msg, writeFunc)
			}
		}
	}
}

func (b *bobUAS) handleIncomingInvite(msg string, writeFunc func([]byte) error) {
	b.t.Logf("Bob handling INVITE, rejected=%v", b.rejected)

	// Extract Via header for response
	viaHeader := ""
	if idx := strings.Index(msg, "Via:"); idx != -1 {
		line := msg[idx:]
		if end := strings.Index(line, "\r\n"); end != -1 {
			viaHeader = strings.TrimSpace(line[len("Via:"):end])
		}
	}

	b.mu.Lock()
	b.toTag = fmt.Sprintf("bob-%d", time.Now().UnixNano())

	// Extract Call-ID
	if idx := strings.Index(msg, "Call-ID:"); idx != -1 {
		line := msg[idx:]
		if end := strings.Index(line, "\r\n"); end != -1 {
			b.callID = strings.TrimSpace(line[len("Call-ID:"):end])
		}
	}

	// Extract From tag
	if idx := strings.Index(msg, "From:"); idx != -1 {
		line := msg[idx:]
		if end := strings.Index(line, "\r\n"); end != -1 {
			fromLine := line[len("From:"):end]
			if tagIdx := strings.Index(fromLine, ";tag="); tagIdx != -1 {
				b.fromTag = fromLine[tagIdx+5:]
			}
		}
	}

	// Extract CSeq
	if idx := strings.Index(msg, "CSeq:"); idx != -1 {
		line := msg[idx:]
		if end := strings.Index(line, "\r\n"); end != -1 {
			cseqLine := strings.TrimSpace(line[len("CSeq:"):end])
			parts := strings.Fields(cseqLine)
			if len(parts) > 0 {
				if n, err := strconv.Atoi(parts[0]); err == nil {
					b.cseq = n
				}
			}
		}
	}

	// Extract Contact
	if idx := strings.Index(msg, "Contact:"); idx != -1 {
		line := msg[idx:]
		if end := strings.Index(line, "\r\n"); end != -1 {
			b.serverContact = strings.TrimSpace(line[len("Contact:"):end])
		}
	}

	// Extract server's RTP port for Bob (port B) from SDP in INVITE
	if matches := sdpPortRegex.FindStringSubmatch(msg); len(matches) == 2 {
		if port, err := strconv.Atoi(matches[1]); err == nil {
			b.serverRTPPortB = port
			select {
			case b.serverRTPPortBCh <- port:
			default:
			}
		}
	}
	b.mu.Unlock()

	if b.rejected {
		resp := fmt.Sprintf("SIP/2.0 486 Busy Here\r\nVia: %s\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:bob@%s>;tag=%s\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n",
			viaHeader, b.callID, b.extractFromHeader(msg), b.ts.Domain, b.toTag)
		b.t.Logf("Bob sending 486 response")
		_ = writeFunc([]byte(resp))
		return
	}

	go receiveRTP(b.rtp, b.rtpCount, b.ctx, b.expectedClientSSRC)

	// Build SDP answer
	sdp := fmt.Sprintf("v=0\r\no=- %d 1 IN IP4 127.0.0.1\r\ns=bob\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		time.Now().UnixNano(), b.rtp.LocalAddr().(*net.UDPAddr).Port)

	if b.ringBeforeAnswer {
		// Send 180 Ringing instead of immediate 200 OK.
		ringing := fmt.Sprintf("SIP/2.0 180 Ringing\r\nVia: %s\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:bob@%s>;tag=%s\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n",
			viaHeader, b.callID, b.extractFromHeader(msg), b.ts.Domain, b.toTag)
		if err := writeFunc([]byte(ringing)); err != nil {
			b.t.Logf("Bob failed to send 180 Ringing: %v", err)
		} else {
			b.t.Logf("Bob sent 180 Ringing, waiting for answerNow signal")
		}

		// Wait for the signal to answer (or context cancellation).
		select {
		case <-b.answerNow:
		case <-b.ctx.Done():
			b.t.Logf("Bob context canceled while waiting to answer")
			return
		}
	}

	resp := fmt.Sprintf("SIP/2.0 200 OK\r\nVia: %s\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:bob@%s>;tag=%s\r\nCSeq: 1 INVITE\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
		viaHeader, b.callID, b.extractFromHeader(msg), b.ts.Domain, b.toTag, len(sdp), sdp)

	b.t.Logf("Bob sending 200 OK response (len=%d)", len(resp))
	if err := writeFunc([]byte(resp)); err != nil {
		b.t.Logf("Bob failed to send 200 OK: %v", err)
	} else {
		b.t.Logf("Bob sent %d bytes", len(resp))
	}

	b.mu.Lock()
	b.answered = true
	b.mu.Unlock()
}

func (b *bobUAS) handleIncomingBye(msg string, writeFunc func([]byte) error) {
	resp := fmt.Sprintf("SIP/2.0 200 OK\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:bob@%s>;tag=%s\r\nCSeq: 2 BYE\r\nContent-Length: 0\r\n\r\n",
		b.callID, b.extractFromHeader(msg), b.ts.Domain, b.toTag)
	_ = writeFunc([]byte(resp))
	b.byeOnce.Do(func() { close(b.byeReceived) })
}

func (b *bobUAS) handleIncomingCancel(msg string, writeFunc func([]byte) error) {
	b.t.Logf("Bob handling CANCEL")

	// Extract CSeq from the CANCEL request for the response.
	cancelCSeq := "1 CANCEL"
	if idx := strings.Index(msg, "CSeq:"); idx != -1 {
		line := msg[idx:]
		if end := strings.Index(line, "\r\n"); end != -1 {
			cancelCSeq = strings.TrimSpace(line[len("CSeq:"):end])
		}
	}

	resp := fmt.Sprintf("SIP/2.0 200 OK\r\nVia: %s\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:bob@%s>;tag=%s\r\nCSeq: %s\r\nContent-Length: 0\r\n\r\n",
		b.extractViaFromMessage(msg), b.callID, b.extractFromHeader(msg), b.ts.Domain, b.toTag, cancelCSeq)
	_ = writeFunc([]byte(resp))
	b.t.Logf("Bob sent 200 OK for CANCEL")
}

func (b *bobUAS) extractViaFromMessage(msg string) string {
	if idx := strings.Index(msg, "Via:"); idx != -1 {
		line := msg[idx:]
		if end := strings.Index(line, "\r\n"); end != -1 {
			return strings.TrimSpace(line[len("Via:"):end])
		}
	}
	return ""
}

func (b *bobUAS) extractFromHeader(msg string) string {
	if idx := strings.Index(msg, "From:"); idx != -1 {
		line := msg[idx:]
		if end := strings.Index(line, "\r\n"); end != -1 {
			return strings.TrimSpace(line[len("From:"):end])
		}
	}
	return ""
}

func (b *bobUAS) register(t *testing.T) {
	t.Helper()

	t.Logf("Bob registering with port %d (transport=%s)", b.port, b.transport)

	req := buildBobRegisterRequest(b.ts.Domain, getPort(b.ts, b.transport), b.transport, b.port)

	res, err := b.client.Do(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Bob registration should succeed")

	// For TCP, extract port from Via header
	if b.transport == "tcp" {
		via := res.GetHeader("Via")
		require.NotNil(t, via, "Via header must be present")
		b.port = extractPortFromVia(via.Value())
		require.NotZero(t, b.port, "Should extract port from Via header")
	}
}

func (b *bobUAS) registerWithAuth(t *testing.T, username, password string) {
	t.Helper()

	t.Logf("Bob registering with auth (transport=%s)", b.transport)

	req := buildBobRegisterRequest(b.ts.Domain, getPort(b.ts, b.transport), b.transport, b.port)

	res, err := b.client.Do(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusUnauthorized, res.StatusCode, "Expected 401 challenge before auth")

	res, err = b.client.DoDigestAuth(t.Context(), req, res, sipgo.DigestAuth{
		Username: username,
		Password: password,
	})
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Bob auth registration should succeed")

	if b.transport == "tcp" {
		via := res.GetHeader("Via")
		require.NotNil(t, via, "Via header must be present")
		b.port = extractPortFromVia(via.Value())
		require.NotZero(t, b.port, "Should extract port from Via header")
	}
}

func (b *bobUAS) sendBye() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.answered {
		return errors.New("call not answered")
	}

	req := sipgo_sip.NewRequest(sipgo_sip.BYE, sipgo_sip.Uri{
		User: "bob",
		Host: b.ts.Domain,
		Port: getPort(b.ts, b.transport),
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:bob@%s>;tag=%s", b.ts.Domain, b.toTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:alice@%s>;tag=%s", b.ts.Domain, b.fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", b.callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", fmt.Sprintf("%d BYE", b.cseq+1)))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	req.SetTransport(strings.ToUpper(b.transport))

	res, err := b.client.Do(b.ctx, req)
	if err != nil {
		return err
	}
	if res.StatusCode != proto.SIPStatusOK {
		return fmt.Errorf("BYE rejected: %d", res.StatusCode)
	}
	return nil
}

func (b *bobUAS) close() {
	b.cancel()
	if b.rtp != nil {
		b.rtp.Close()
	}
	if b.sipConn != nil {
		b.sipConn.Close()
	}
	if b.tcpLn != nil {
		b.tcpLn.Close()
	}
	if b.client != nil {
		b.client.Close()
	}
	if b.ua != nil {
		b.ua.Close()
	}
}

func receiveRTP(rtp *net.UDPConn, rtpCount chan<- int, ctx context.Context, expectedClientSSRC uint32) {
	var prevSeq uint16
	var prevTs uint32
	var serverSSRC uint32
	packetCount := 0

	buf := make([]byte, 1500)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = rtp.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := rtp.ReadFromUDP(buf)
		if err != nil {
			if packetCount > 0 {
				break
			}
			continue
		}

		var pkt proto.RTPPacket
		if err := proto.UnmarshalRTPTo(buf[:n], &pkt); err != nil {
			continue
		}

		packetCount++

		if packetCount == 1 {
			serverSSRC = pkt.Header.SSRC
			if expectedClientSSRC != 0 && serverSSRC == expectedClientSSRC {
				rtpCount <- -1
				return
			}
		} else {
			if pkt.Header.SequenceNumber != prevSeq+1 {
				rtpCount <- -2
				return
			}

			if pkt.Header.Timestamp != prevTs+160 {
				rtpCount <- -3
				return
			}

			if pkt.Header.SSRC != serverSSRC {
				rtpCount <- -4
				return
			}
		}

		prevSeq = pkt.Header.SequenceNumber
		prevTs = pkt.Header.Timestamp

		if packetCount >= 10 {
			break
		}
	}

	rtpCount <- packetCount
}

func sendAliceToBob(t *testing.T, aliceRTP *net.UDPConn, serverRTPAddr *net.UDPAddr, bobRTPCount chan int, aliceSSRC uint32) {
	t.Helper()

	for i := range 10 {
		pkt := integrationtest.BuildRTPPacket(
			uint16(100+i),
			uint32(i*160),
			aliceSSRC,
			[]byte{byte(i), byte(i), byte(i), byte(i)},
		)
		buf, _ := pkt.Marshal()
		_, _ = aliceRTP.WriteToUDP(buf, serverRTPAddr)
		if i < 9 {
			time.Sleep(20 * time.Millisecond)
		}
	}

	select {
	case count := <-bobRTPCount:
		if count < 0 {
			switch count {
			case -1:
				t.Fatal("SSRC not isolated (server leaked Alice's SSRC)")
			case -2:
				t.Fatal("RTP sequence continuity broken")
			case -3:
				t.Fatal("RTP timestamp not monotonic")
			case -4:
				t.Fatal("SSRC inconsistent across packets")
			}
			return
		}
		require.Positive(t, count, "Bob should receive RTP packets")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Bob to receive RTP")
	}
}

func sendBobToAlice(t *testing.T, bobRTP *net.UDPConn, serverRTPAddrB *net.UDPAddr, aliceRTP *net.UDPConn, bobSSRC uint32) {
	t.Helper()

	for i := range 10 {
		pkt := integrationtest.BuildRTPPacket(
			uint16(200+i),
			uint32(i*160),
			bobSSRC,
			[]byte{byte(0x80 + i), byte(i), byte(i), byte(i)},
		)
		buf, _ := pkt.Marshal()
		_, _ = bobRTP.WriteToUDP(buf, serverRTPAddrB)
		if i < 9 {
			time.Sleep(20 * time.Millisecond)
		}
	}

	_ = aliceRTP.SetReadDeadline(time.Now().Add(5 * time.Second))
	var alicePrevSeq uint16
	var alicePrevTs uint32
	var aliceServerSSRC uint32
	alicePacketCount := 0
	for {
		buf := make([]byte, 1500)
		n, _, err := aliceRTP.ReadFromUDP(buf)
		if err != nil {
			if alicePacketCount > 0 {
				break
			}
			t.Fatal("timeout waiting for Alice to receive RTP from Bob")
		}

		var pkt proto.RTPPacket
		require.NoError(t, proto.UnmarshalRTPTo(buf[:n], &pkt))
		alicePacketCount++

		if alicePacketCount == 1 {
			aliceServerSSRC = pkt.Header.SSRC
			require.NotEqual(t, bobSSRC, aliceServerSSRC, "SSRC not isolated (server leaked Bob's SSRC)")
		} else {
			require.Equal(t, alicePrevSeq+1, pkt.Header.SequenceNumber, "RTP sequence continuity broken (B→A)")
			require.Equal(t, alicePrevTs+160, pkt.Header.Timestamp, "RTP timestamp not monotonic (B→A)")
			require.Equal(t, aliceServerSSRC, pkt.Header.SSRC, "SSRC inconsistent across packets (B→A)")
		}

		alicePrevSeq = pkt.Header.SequenceNumber
		alicePrevTs = pkt.Header.Timestamp

		if alicePacketCount >= 10 {
			break
		}
	}
}

func runB2BUACall(t *testing.T, ts *integrationtest.TestServer, aliceTransport, bobTransport, byeFrom string) {
	t.Helper()

	bob := newBobUAS(t, ts, bobTransport)
	defer bob.close()

	aliceSSRC := integrationtest.RandomSSRC()
	bobSSRC := integrationtest.RandomSSRC()
	bob.expectedClientSSRC = aliceSSRC
	bob.expectedBobSSRC = bobSSRC

	bob.register(t)
	time.Sleep(100 * time.Millisecond)

	aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	defer aliceClient.Close()
	defer aliceUA.Close()

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	callID := fmt.Sprintf("b2bua-%s-%s-%s", aliceTransport, bobTransport, t.Name())
	aliceFromTag := "alice-from-123"

	invite := buildB2BUAInvite(ts.Domain, integrationtest.GetPort(ts, aliceTransport), callID, aliceFromTag, aliceTransport, aliceRTP.LocalAddr().(*net.UDPAddr).Port)
	if aliceTransport == "tcp" {
		invite.SetTransport("TCP")
	}

	res, err := aliceClient.Do(t.Context(), invite)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

	serverTag := extractToTagB2B(res)
	require.NotEmpty(t, serverTag, "To header should have server tag")

	require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
	require.NoError(t, err)
	serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

	ack := buildB2BUAACK(ts.Domain, integrationtest.GetPort(ts, aliceTransport), callID, aliceFromTag, serverTag)
	if aliceTransport == "tcp" {
		ack.SetTransport("TCP")
	}
	err = aliceClient.WriteRequest(ack)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

	var serverRTPPortB int
	select {
	case serverRTPPortB = <-bob.serverRTPPortBCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for Bob to extract server RTP port")
	}
	require.NotZero(t, serverRTPPortB, "Bob should have extracted server's RTP port")
	serverRTPAddrB := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: serverRTPPortB}

	sendAliceToBob(t, aliceRTP, serverRTPAddr, bob.rtpCount, aliceSSRC)

	sendBobToAlice(t, bob.rtp, serverRTPAddrB, aliceRTP, bobSSRC)

	switch byeFrom {
	case "alice_bye":
		bye := buildB2BUABYE(ts.Domain, integrationtest.GetPort(ts, aliceTransport), callID, aliceFromTag, serverTag)
		if aliceTransport == "tcp" {
			bye.SetTransport("TCP")
		}
		byeRes, err := aliceClient.Do(t.Context(), bye)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")
	case "bob_bye":
		require.NoError(t, bob.sendBye(), "Bob should be able to send BYE and get 200 OK")
	}
}

func runOutboundCall(t *testing.T, ts *integrationtest.TestServer, outboundSide string) {
	t.Helper()

	aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	defer aliceClient.Close()
	defer aliceUA.Close()

	var (
		bobRTP        *net.UDPConn
		bobRTPCount   chan int
		bobRTPPortBCh chan int
		port          int
		transport     string
		aliceSSRC     uint32
		bobSSRC       uint32
	)

	aliceSSRC = integrationtest.RandomSSRC()
	bobSSRC = integrationtest.RandomSSRC()

	callID := fmt.Sprintf("b2bua-outbound-%s-%s", outboundSide, t.Name())
	aliceFromTag := "alice-from-outbound"

	switch outboundSide {
	case "bob":
		port = ts.UDPPort
		transport = "udp"

		bob := newOutboundBobUAS(t, ts)
		defer bob.close()
		bob.expectedClientSSRC = aliceSSRC
		bob.expectedBobSSRC = bobSSRC
		bob.register(t)
		bobRTP = bob.rtp
		bobRTPCount = bob.rtpCount
		bobRTPPortBCh = bob.serverRTPPortBCh

	case "alice":
		port = ts.TCPPort
		transport = "tcp"

		contact := "<sip:alice@127.0.0.1:0;transport=tcp;ob>"
		regReq := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
			User: "alice",
			Host: ts.Domain,
			Port: ts.TCPPort,
		})
		regReq.AppendHeader(sipgo_sip.NewHeader("Contact", contact))
		regReq.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:alice@%s>;tag=alice-outbound", ts.Domain)))
		regReq.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:alice@%s>", ts.Domain)))
		regReq.AppendHeader(sipgo_sip.NewHeader("Call-ID", "b2bua-alice-outbound-"+ts.Domain))
		regReq.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 REGISTER"))
		regReq.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
		regReq.SetTransport("TCP")

		regRes, err := aliceClient.Do(t.Context(), regReq)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, regRes.StatusCode, "Alice outbound registration should succeed")

		bob := newBobUAS(t, ts, "udp")
		defer bob.close()
		bob.expectedClientSSRC = aliceSSRC
		bob.expectedBobSSRC = bobSSRC
		bob.register(t)
		bobRTP = bob.rtp
		bobRTPCount = bob.rtpCount
		bobRTPPortBCh = bob.serverRTPPortBCh

	default:
		t.Fatalf("unknown outboundSide: %s", outboundSide)
	}

	time.Sleep(100 * time.Millisecond)

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	invite := buildB2BUAInvite(ts.Domain, port, callID, aliceFromTag, transport, aliceRTP.LocalAddr().(*net.UDPAddr).Port)
	if transport == "tcp" {
		invite.SetTransport("TCP")
	}

	res, err := aliceClient.Do(t.Context(), invite)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

	serverTag := extractToTagB2B(res)
	require.NotEmpty(t, serverTag, "To header should have server tag")

	require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
	require.NoError(t, err)
	serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

	ack := buildB2BUAACK(ts.Domain, port, callID, aliceFromTag, serverTag)
	if transport == "tcp" {
		ack.SetTransport("TCP")
	}
	err = aliceClient.WriteRequest(ack)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

	var serverRTPPortB int
	select {
	case serverRTPPortB = <-bobRTPPortBCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for Bob to extract server RTP port")
	}
	require.NotZero(t, serverRTPPortB, "Bob should have extracted server's RTP port")
	serverRTPAddrB := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: serverRTPPortB}

	sendAliceToBob(t, aliceRTP, serverRTPAddr, bobRTPCount, aliceSSRC)

	sendBobToAlice(t, bobRTP, serverRTPAddrB, aliceRTP, bobSSRC)

	bye := buildB2BUABYE(ts.Domain, port, callID, aliceFromTag, serverTag)
	if transport == "tcp" {
		bye.SetTransport("TCP")
	}
	byeRes, err := aliceClient.Do(t.Context(), bye)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")
}

func runB2BUAReject(t *testing.T, ts *integrationtest.TestServer, aliceTransport, bobTransport string) {
	t.Helper()

	bob := newBobUAS(t, ts, bobTransport)
	bob.rejected = true
	defer bob.close()

	bob.register(t)
	time.Sleep(100 * time.Millisecond)

	aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	defer aliceClient.Close()
	defer aliceUA.Close()

	callID := fmt.Sprintf("b2bua-reject-%s-%s-%s", aliceTransport, bobTransport, t.Name())

	invite := buildB2BUAInvite(ts.Domain, integrationtest.GetPort(ts, aliceTransport), callID, "alice-from-123", aliceTransport, 12345)
	if aliceTransport == "tcp" {
		invite.SetTransport("TCP")
	}

	res, err := aliceClient.Do(t.Context(), invite)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusBusyHere, res.StatusCode, "Alice should receive 486 Busy Here")
}

func buildBobRegisterRequest(domain string, serverPort int, transport string, bobPort int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
		User: "bob",
		Host: domain,
		Port: serverPort,
	})

	contact := fmt.Sprintf("<sip:bob@127.0.0.1:%d;transport=%s>", bobPort, transport)
	req.AppendHeader(sipgo_sip.NewHeader("Contact", contact))
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:bob@%s>;tag=bob-123", domain)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>", domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "b2bua-bob-"+domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 REGISTER"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))

	if transport == "tcp" {
		req.SetTransport("TCP")
	}

	return req
}

func extractPortFromVia(viaValue string) int {
	parts := strings.SplitN(viaValue, " ", 2)
	if len(parts) < 2 {
		return 0
	}
	sentBy := parts[1]
	if idx := strings.Index(sentBy, ";"); idx != -1 {
		sentBy = sentBy[:idx]
	}
	if idx := strings.LastIndex(sentBy, ":"); idx != -1 {
		portStr := sentBy[idx+1:]
		if port, err := strconv.Atoi(portStr); err == nil {
			return port
		}
	}
	return 0
}

func buildB2BUAInvite(domain string, port int, callID, fromTag, transport string, rtpPort int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.INVITE, sipgo_sip.Uri{
		User: "bob",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:alice@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>", domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 INVITE"))
	req.AppendHeader(sipgo_sip.NewHeader("Contact", fmt.Sprintf("<sip:alice@127.0.0.1:%d;transport=%s>", rtpPort, transport)))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Type", "application/sdp"))

	sdp := integrationtest.BuildSDPOffer(rtpPort, "127.0.0.1")
	sdpBytes, _ := sdp.Marshal()
	req.SetBody(sdpBytes)
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", strconv.Itoa(len(sdpBytes))))

	return req
}

func buildB2BUAACK(domain string, port int, callID, fromTag, serverTag string) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.ACK, sipgo_sip.Uri{
		User: "bob",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:alice@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>;tag=%s", domain, serverTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 ACK"))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func buildB2BUABYE(domain string, port int, callID, fromTag, serverTag string) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.BYE, sipgo_sip.Uri{
		User: "bob",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:alice@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>;tag=%s", domain, serverTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "2 BYE"))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func extractToTagB2B(res *sipgo_sip.Response) string {
	return integrationtest.ExtractToTag(res)
}

func getPort(ts *integrationtest.TestServer, transport string) int {
	return integrationtest.GetPort(ts, transport)
}

var sdpPortRegex = regexp.MustCompile(`m=audio (\d+) RTP/AVP`)
