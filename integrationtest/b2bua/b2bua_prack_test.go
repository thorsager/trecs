package b2bua

import (
	"context"
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

// prackBob is a Bob UAS that sends a reliable provisional (183 with RSeq)
// before the 200 OK, and handles the incoming PRACK using raw UDP.
type prackBob struct {
	t       *testing.T
	ts      *integrationtest.TestServer
	ctx     context.Context
	cancel  context.CancelFunc
	ua      *sipgo.UserAgent
	client  *sipgo.Client
	sipConn *net.UDPConn
	port    int
	rtp     *net.UDPConn

	mu               sync.Mutex
	callID           string
	fromTag          string
	toTag            string
	cseq             int
	serverContact    string
	serverRTPPortB   int
	serverRTPPortBCh chan int
	byeReceived      chan struct{}
	byeOnce          sync.Once
	rtpCount         chan int

	// readLoop dispatches PRACK messages here
	prackCh chan string

	expectedClientSSRC uint32
	expectedBobSSRC    uint32

	inviteOKSignal chan struct{} // when non-nil, wait on this before sending 200 OK for INVITE
}

func newPrackBob(t *testing.T, ts *integrationtest.TestServer) *prackBob {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	rtp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	b := &prackBob{
		t:                t,
		ts:               ts,
		ctx:              ctx,
		cancel:           cancel,
		ua:               ua,
		client:           client,
		sipConn:          conn,
		port:             conn.LocalAddr().(*net.UDPAddr).Port,
		rtp:              rtp,
		serverRTPPortBCh: make(chan int, 1),
		byeReceived:      make(chan struct{}),
		rtpCount:         make(chan int, 1),
		prackCh:          make(chan string, 1),
	}

	go b.readLoop()
	return b
}

func (b *prackBob) register(t *testing.T) {
	t.Helper()

	req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
		User: "bob",
		Host: b.ts.Domain,
		Port: b.ts.UDPPort,
	})
	req.AppendHeader(sipgo_sip.NewHeader("Contact", fmt.Sprintf("<sip:bob@127.0.0.1:%d;transport=udp>", b.port)))
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:bob@%s>;tag=bob-prack", b.ts.Domain)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>", b.ts.Domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "prack-bob-"+b.ts.Domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 REGISTER"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))

	res, err := b.client.Do(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Bob registration should succeed")
}

func (b *prackBob) registerWithAuth(t *testing.T, username, password string) {
	t.Helper()

	req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
		User: "bob",
		Host: b.ts.Domain,
		Port: b.ts.UDPPort,
	})
	req.AppendHeader(sipgo_sip.NewHeader("Contact", fmt.Sprintf("<sip:bob@127.0.0.1:%d;transport=udp>", b.port)))
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:bob@%s>;tag=bob-prack-auth", b.ts.Domain)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>", b.ts.Domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "prack-bob-auth-"+b.ts.Domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 REGISTER"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))

	res, err := b.client.Do(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusUnauthorized, res.StatusCode, "Expected 401 challenge")

	res, err = b.client.DoDigestAuth(t.Context(), req, res, sipgo.DigestAuth{
		Username: username,
		Password: password,
	})
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Bob auth registration should succeed")
}

// readLoop dispatches messages by type:
//
//	INVITE -> goroutine handleInvite
//	BYE    -> goroutine handleBye
//	PRACK  -> b.prackCh
func (b *prackBob) readLoop() {
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

		switch {
		case strings.HasPrefix(msg, "INVITE"):
			b.t.Logf("prackBob dispatching INVITE")
			go b.handleInvite(msg, writeFunc)
		case strings.HasPrefix(msg, "BYE"):
			b.t.Logf("prackBob dispatching BYE")
			go b.handleBye(msg, writeFunc)
		case strings.HasPrefix(msg, "PRACK"):
			b.t.Logf("prackBob routing PRACK to channel")
			select {
			case b.prackCh <- msg:
			default:
				b.t.Logf("prackBob dropped PRACK (channel full)")
			}
		default:
			b.t.Logf("prackBob ignoring unknown message: %s", msg[:min(len(msg), 80)])
		}
	}
}

func (b *prackBob) handleInvite(msg string, writeFunc func([]byte) error) {
	b.t.Logf("prackBob handling INVITE")

	// Extract headers
	b.mu.Lock()
	b.toTag = fmt.Sprintf("bob-prack-%d", time.Now().UnixNano())

	b.callID = extractHeader(msg, "Call-ID")
	b.fromTag = extractTagParam(msg, "From", "tag")
	b.serverContact = extractHeader(msg, "Contact")

	cseqStr := extractHeader(msg, "CSeq")
	if parts := strings.Fields(cseqStr); len(parts) > 0 {
		b.cseq, _ = strconv.Atoi(parts[0])
	}

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

	viaHeader := extractHeader(msg, "Via")
	fromLine := extractHeaderLine(msg, "From")

	// Send reliable 183 with RSeq=1
	rseq := "1"
	reliable183 := fmt.Sprintf("SIP/2.0 183 Session Progress\r\n"+
		"Via: %s\r\n"+
		"Call-ID: %s\r\n"+
		"From: %s\r\n"+
		"To: <sip:bob@%s>;tag=%s\r\n"+
		"CSeq: %d INVITE\r\n"+
		"Require: 100rel\r\n"+
		"RSeq: %s\r\n"+
		"Content-Length: 0\r\n\r\n",
		viaHeader, b.callID, fromLine, b.ts.Domain, b.toTag, b.cseq, rseq)

	b.t.Logf("prackBob sending reliable 183 (RSeq=%s)", rseq)
	if err := writeFunc([]byte(reliable183)); err != nil {
		b.t.Logf("prackBob failed to send 183: %v", err)
		return
	}

	// Wait for PRACK
	select {
	case prackMsg := <-b.prackCh:
		b.t.Logf("prackBob received PRACK: %s", prackMsg[:min(len(prackMsg), 120)])
		b.handlePRACK(prackMsg, writeFunc)
	case <-time.After(5 * time.Second):
		b.t.Logf("prackBob did not receive PRACK, timing out")
		timeoutResp := fmt.Sprintf("SIP/2.0 504 PRACK Timeout\r\n"+
			"Via: %s\r\n"+
			"Call-ID: %s\r\n"+
			"From: %s\r\n"+
			"To: <sip:bob@%s>;tag=%s\r\n"+
			"CSeq: %d INVITE\r\n"+
			"Content-Length: 0\r\n\r\n",
			viaHeader, b.callID, fromLine, b.ts.Domain, b.toTag, b.cseq)
		_ = writeFunc([]byte(timeoutResp))
		return
	case <-b.ctx.Done():
		return
	}

	// Wait for signal before sending 200 OK for INVITE (if inviteOKSignal is set)
	if b.inviteOKSignal != nil {
		select {
		case <-b.inviteOKSignal:
			b.t.Logf("prackBob got signal to send 200 OK for INVITE")
		case <-time.After(10 * time.Second):
			b.t.Logf("prackBob timeout waiting for inviteOKSignal")
			return
		case <-b.ctx.Done():
			return
		}
	}

	go receiveRTP(b.rtp, b.rtpCount, b.ctx, b.expectedClientSSRC)

	sdp := fmt.Sprintf("v=0\r\no=- %d 1 IN IP4 127.0.0.1\r\ns=bob\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		time.Now().UnixNano(), b.rtp.LocalAddr().(*net.UDPAddr).Port)

	resp := fmt.Sprintf("SIP/2.0 200 OK\r\n"+
		"Via: %s\r\n"+
		"Call-ID: %s\r\n"+
		"From: %s\r\n"+
		"To: <sip:bob@%s>;tag=%s\r\n"+
		"CSeq: %d INVITE\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %d\r\n\r\n%s",
		viaHeader, b.callID, fromLine, b.ts.Domain, b.toTag, b.cseq, len(sdp), sdp)

	b.t.Logf("prackBob sending 200 OK for INVITE")
	if err := writeFunc([]byte(resp)); err != nil {
		b.t.Logf("prackBob failed to send 200 OK: %v", err)
	}
}

func (b *prackBob) handlePRACK(prackMsg string, writeFunc func([]byte) error) {
	prackVia := extractHeader(prackMsg, "Via")
	rackVal := extractHeader(prackMsg, "RAck")
	b.t.Logf("prackBob processing PRACK with RAck=%s", rackVal)

	fromLine := extractHeaderLine(prackMsg, "From")

	prack200 := fmt.Sprintf("SIP/2.0 200 OK\r\n"+
		"Via: %s\r\n"+
		"Call-ID: %s\r\n"+
		"From: %s\r\n"+
		"To: <sip:bob@%s>;tag=%s\r\n"+
		"CSeq: 1 PRACK\r\n"+
		"Content-Length: 0\r\n\r\n",
		prackVia, b.callID, fromLine, b.ts.Domain, b.toTag)

	b.t.Logf("prackBob sending 200 OK for PRACK")
	if err := writeFunc([]byte(prack200)); err != nil {
		b.t.Logf("prackBob failed to send 200 OK for PRACK: %v", err)
	}
}

func (b *prackBob) handleBye(msg string, writeFunc func([]byte) error) {
	fromHdr := extractHeaderLine(msg, "From")
	resp := fmt.Sprintf("SIP/2.0 200 OK\r\n"+
		"Call-ID: %s\r\n"+
		"From: %s\r\n"+
		"To: <sip:bob@%s>;tag=%s\r\n"+
		"CSeq: 2 BYE\r\n"+
		"Content-Length: 0\r\n\r\n",
		b.callID, fromHdr, b.ts.Domain, b.toTag)
	_ = writeFunc([]byte(resp))
	b.byeOnce.Do(func() { close(b.byeReceived) })
}

func (b *prackBob) close() {
	b.cancel()
	if b.rtp != nil {
		b.rtp.Close()
	}
	if b.sipConn != nil {
		b.sipConn.Close()
	}
	if b.client != nil {
		b.client.Close()
	}
	if b.ua != nil {
		b.ua.Close()
	}
}

// aliceRawUAC is a raw UDP UAC for testing PRACK on the UAS side.
type aliceRawUAC struct {
	t         *testing.T
	ts        *integrationtest.TestServer
	ctx       context.Context
	cancel    context.CancelFunc
	conn      *net.UDPConn
	addr      *net.UDPAddr
	rtp       *net.UDPConn
	callID    string
	fromTag   string
	serverTag string
	cseq      int
}

func newAliceRawUAC(t *testing.T, ts *integrationtest.TestServer) *aliceRawUAC {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	rtp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	return &aliceRawUAC{
		t:       t,
		ts:      ts,
		ctx:     ctx,
		cancel:  cancel,
		conn:    conn,
		addr:    &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: ts.UDPPort},
		rtp:     rtp,
		callID:  fmt.Sprintf("prack-test-%d", time.Now().UnixNano()),
		fromTag: "alice-prack-123",
		cseq:    1,
	}
}

func (a *aliceRawUAC) close() {
	a.cancel()
	if a.conn != nil {
		a.conn.Close()
	}
	if a.rtp != nil {
		a.rtp.Close()
	}
}

func (a *aliceRawUAC) sendINVITE(supported100rel bool) {
	rtpPort := a.rtp.LocalAddr().(*net.UDPAddr).Port
	sdp := fmt.Sprintf("v=0\r\no=- %d 1 IN IP4 127.0.0.1\r\ns=alice\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		time.Now().UnixNano(), rtpPort)

	invite := fmt.Sprintf("INVITE sip:bob@%s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=z9hG4bK-%s\r\n"+
		"From: <sip:alice@%s>;tag=%s\r\n"+
		"To: <sip:bob@%s>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"Contact: <sip:alice@127.0.0.1:%d>\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %d\r\n",
		a.ts.Domain, a.conn.LocalAddr().(*net.UDPAddr).Port, a.callID,
		a.ts.Domain, a.fromTag, a.ts.Domain, a.callID,
		a.conn.LocalAddr().(*net.UDPAddr).Port, len(sdp))

	if supported100rel {
		invite += "Supported: 100rel\r\n"
	}

	invite += "\r\n" + sdp

	a.t.Logf("Alice sending INVITE (supported100rel=%v)", supported100rel)
	_, err := a.conn.WriteToUDP([]byte(invite), a.addr)
	require.NoError(a.t, err)
}

func (a *aliceRawUAC) readResponse(timeout time.Duration) string {
	buf := make([]byte, 4096)
	_ = a.conn.SetReadDeadline(time.Now().Add(timeout))
	n, _, err := a.conn.ReadFromUDP(buf)
	if err != nil {
		return ""
	}
	msg := string(buf[:n])
	a.t.Logf("Alice received %d bytes", n)
	return msg
}

func (a *aliceRawUAC) sendPRACK(rackRSeq, rackCSeq string) {
	a.cseq++
	prack := fmt.Sprintf("PRACK sip:bob@%s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=z9hG4bK-prack-%s\r\n"+
		"From: <sip:alice@%s>;tag=%s\r\n"+
		"To: <sip:bob@%s>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: %d PRACK\r\n"+
		"RAck: %s %s INVITE\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		a.ts.Domain, a.conn.LocalAddr().(*net.UDPAddr).Port, a.callID,
		a.ts.Domain, a.fromTag, a.ts.Domain, a.serverTag,
		a.callID, a.cseq, rackRSeq, rackCSeq)

	a.t.Logf("Alice sending PRACK (RAck: %s %s INVITE)", rackRSeq, rackCSeq)
	_, err := a.conn.WriteToUDP([]byte(prack), a.addr)
	require.NoError(a.t, err)
}

func (a *aliceRawUAC) sendACK() {
	ack := fmt.Sprintf("ACK sip:bob@%s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=z9hG4bK-ack-%s\r\n"+
		"From: <sip:alice@%s>;tag=%s\r\n"+
		"To: <sip:bob@%s>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 ACK\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		a.ts.Domain, a.conn.LocalAddr().(*net.UDPAddr).Port, a.callID,
		a.ts.Domain, a.fromTag, a.ts.Domain, a.serverTag,
		a.callID)

	a.t.Logf("Alice sending ACK")
	_, err := a.conn.WriteToUDP([]byte(ack), a.addr)
	require.NoError(a.t, err)
}

func (a *aliceRawUAC) sendBYE() {
	a.cseq++
	bye := fmt.Sprintf("BYE sip:bob@%s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=z9hG4bK-bye-%s\r\n"+
		"From: <sip:alice@%s>;tag=%s\r\n"+
		"To: <sip:bob@%s>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: %d BYE\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		a.ts.Domain, a.conn.LocalAddr().(*net.UDPAddr).Port, a.callID,
		a.ts.Domain, a.fromTag, a.ts.Domain, a.serverTag,
		a.callID, a.cseq)

	a.t.Logf("Alice sending BYE")
	_, err := a.conn.WriteToUDP([]byte(bye), a.addr)
	require.NoError(a.t, err)
}

// --- header helpers (package-level, in addition to b2bua_helpers.go) ---

func extractHeader(msg, header string) string {
	re := regexp.MustCompile(`(?im)^` + regexp.QuoteMeta(header) + `:\s*(.*?)\r?\n`)
	matches := re.FindStringSubmatch(msg)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func extractHeaderLine(msg, header string) string {
	re := regexp.MustCompile(`(?im)^` + regexp.QuoteMeta(header) + `:\s*(.*?)\r?\n`)
	matches := re.FindStringSubmatch(msg)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func extractTagParam(msg, header, param string) string {
	line := extractHeader(msg, header)
	re := regexp.MustCompile(`;` + param + `=([^\s;]+)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func extractBody(msg string) string {
	idx := strings.Index(msg, "\r\n\r\n")
	if idx == -1 {
		return ""
	}
	return msg[idx+4:]
}

// --- Tests ---

func TestIntegration_B2BUAPRACK_UAS(t *testing.T) {
	ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil, integrationtest.WithPRACK())
	defer ts.Stop()

	pb := newPrackBob(t, ts)
	pb.inviteOKSignal = make(chan struct{}, 1)
	defer pb.close()
	pb.register(t)
	time.Sleep(100 * time.Millisecond)

	alice := newAliceRawUAC(t, ts)
	defer alice.close()

	// Alice sends INVITE with Supported: 100rel
	alice.sendINVITE(true)

	// Read 100 Trying
	msg := alice.readResponse(5 * time.Second)
	require.Contains(t, msg, "100 Trying")

	// Read 183 Session Progress (reliable, with RSeq)
	msg = alice.readResponse(5 * time.Second)
	require.Contains(t, msg, "183 Session Progress")
	require.Contains(t, strings.ToLower(msg), "rseq:")
	require.Contains(t, msg, "Require: 100rel")

	rseq := extractHeader(msg, "RSeq")
	require.NotEmpty(t, rseq, "183 should have RSeq")

	alice.serverTag = extractTagParam(msg, "To", "tag")
	require.NotEmpty(t, alice.serverTag, "183 should have To tag")

	// Alice sends PRACK, then signals Bob to send 200 OK for INVITE
	alice.sendPRACK(rseq, "1")
	pb.inviteOKSignal <- struct{}{}

	// Read 200 OK for PRACK
	msg = alice.readResponse(5 * time.Second)
	require.NotEmpty(t, msg, "Should receive 200 OK for PRACK")
	require.Contains(t, msg, "200 OK")

	// Read 200 OK for INVITE
	msg = alice.readResponse(5 * time.Second)
	require.NotEmpty(t, msg, "Should receive 200 OK for INVITE")
	require.Contains(t, msg, "200 OK")
	require.Contains(t, msg, "application/sdp")

	if alice.serverTag == "" {
		alice.serverTag = extractTagParam(msg, "To", "tag")
	}

	sdpAnswer, err := proto.UnmarshalSDPBytes([]byte(extractBody(msg)))
	require.NoError(t, err)
	_, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort)

	// ACK
	alice.sendACK()
	time.Sleep(100 * time.Millisecond)

	// RTP verification
	aliceSSRC := integrationtest.RandomSSRC()
	bobSSRC := integrationtest.RandomSSRC()
	pb.expectedClientSSRC = aliceSSRC
	pb.expectedBobSSRC = bobSSRC

	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: serverRTPPort}
	sendAliceToBob(t, alice.rtp, serverRTPAddr, pb.rtpCount, aliceSSRC)

	var serverRTPPortB int
	select {
	case serverRTPPortB = <-pb.serverRTPPortBCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for Bob to extract server RTP port")
	}
	serverRTPAddrB := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: serverRTPPortB}

	sendBobToAlice(t, pb.rtp, serverRTPAddrB, alice.rtp, bobSSRC)

	// BYE
	alice.sendBYE()
	msg = alice.readResponse(5 * time.Second)
	for !strings.Contains(msg, "200 OK") {
		msg = alice.readResponse(5 * time.Second)
	}
}

func TestIntegration_B2BUAPRACK_UAC(t *testing.T) {
	ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil, integrationtest.WithPRACK())
	defer ts.Stop()

	pb := newPrackBob(t, ts)
	pb.inviteOKSignal = make(chan struct{}, 1)
	pb.expectedClientSSRC = integrationtest.RandomSSRC()
	pb.expectedBobSSRC = integrationtest.RandomSSRC()
	defer pb.close()
	pb.register(t)
	time.Sleep(100 * time.Millisecond)

	aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)
	defer aliceUA.Close()

	aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	defer aliceClient.Close()

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	callID := fmt.Sprintf("prack-uac-%d", time.Now().UnixNano())
	aliceFromTag := "alice-uac-123"

	invite := buildB2BUAInvite(ts.Domain, ts.UDPPort, callID, aliceFromTag, "udp",
		aliceRTP.LocalAddr().(*net.UDPAddr).Port)

	// Pre-signal Bob to send 200 OK for INVITE so the call completes
	pb.inviteOKSignal <- struct{}{}

	res, err := aliceClient.Do(t.Context(), invite)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

	serverTag := extractToTagB2B(res)
	require.NotEmpty(t, serverTag)

	require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
	require.NoError(t, err)
	serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort)

	// Verify PRACK was handled by Bob (prackBob got it via prackCh and sent 200 OK)
	select {
	case <-pb.prackCh:
		t.Log("PRACK was received and handled by Bob")
	case <-time.After(5 * time.Second):
		t.Fatal("Bob did not receive PRACK from server")
	}

	ack := buildB2BUAACK(ts.Domain, ts.UDPPort, callID, aliceFromTag, serverTag)
	err = aliceClient.WriteRequest(ack)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

	var serverRTPPortB int
	select {
	case serverRTPPortB = <-pb.serverRTPPortBCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for Bob to extract server RTP port")
	}
	require.NotZero(t, serverRTPPortB)
	serverRTPAddrB := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: serverRTPPortB}

	sendAliceToBob(t, aliceRTP, serverRTPAddr, pb.rtpCount, pb.expectedClientSSRC)
	sendBobToAlice(t, pb.rtp, serverRTPAddrB, aliceRTP, pb.expectedBobSSRC)

	bye := buildB2BUABYE(ts.Domain, ts.UDPPort, callID, aliceFromTag, serverTag)
	byeRes, err := aliceClient.Do(t.Context(), bye)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")
}

func TestIntegration_B2BUAPRACK_WithAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("bob", "password", "sip:bob@127.0.0.1"),
	)

	ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil, integrationtest.WithPRACK())
	defer ts.Stop()
	ts.Reg.SetPasswordStore(store)
	ts.SetProxyPasswordStore(store)

	pb := newPrackBob(t, ts)
	pb.inviteOKSignal = make(chan struct{}, 1)
	pb.expectedClientSSRC = integrationtest.RandomSSRC()
	pb.expectedBobSSRC = integrationtest.RandomSSRC()
	defer pb.close()
	pb.registerWithAuth(t, "bob", "password")
	time.Sleep(100 * time.Millisecond)

	aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)
	defer aliceUA.Close()

	aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	defer aliceClient.Close()

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	callID := fmt.Sprintf("prack-auth-%d", time.Now().UnixNano())

	invite := buildB2BUAInvite(ts.Domain, ts.UDPPort, callID, "alice-auth-123", "udp",
		aliceRTP.LocalAddr().(*net.UDPAddr).Port)

	// Pre-signal Bob to send 200 OK for INVITE so the call completes
	pb.inviteOKSignal <- struct{}{}

	// Use doProxyAuthRequest to handle 407 challenge
	res := doProxyAuthRequest(t, aliceClient, invite, "bob", "password")
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

	serverTag := extractToTagB2B(res)
	require.NotEmpty(t, serverTag)

	require.NotEmpty(t, res.Body())
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
	require.NoError(t, err)
	serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort)

	select {
	case <-pb.prackCh:
		t.Log("PRACK was received and handled by Bob")
	case <-time.After(5 * time.Second):
		t.Fatal("Bob did not receive PRACK from server")
	}

	ack := buildB2BUAACK(ts.Domain, ts.UDPPort, callID, "alice-auth-123", serverTag)
	err = aliceClient.WriteRequest(ack)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

	var serverRTPPortB int
	select {
	case serverRTPPortB = <-pb.serverRTPPortBCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for Bob to extract server RTP port")
	}
	serverRTPAddrB := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: serverRTPPortB}

	sendAliceToBob(t, aliceRTP, serverRTPAddr, pb.rtpCount, pb.expectedClientSSRC)
	sendBobToAlice(t, pb.rtp, serverRTPAddrB, aliceRTP, pb.expectedBobSSRC)

	bye := buildB2BUABYE(ts.Domain, ts.UDPPort, callID, "alice-auth-123", serverTag)
	byeRes := doProxyAuthRequest(t, aliceClient, bye, "bob", "password")
	require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")
}
