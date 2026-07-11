package trunk

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

	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
	"github.com/thorsager/trecs/internal/trunk"
	"github.com/thorsager/trecs/proto"
)

// trunkPeer simulates a SIP trunk peer (e.g., an ITSP or another PBX).
// It listens on UDP and handles incoming INVITE/BYE requests from the server.
type trunkPeer struct {
	t      *testing.T
	ctx    context.Context
	cancel context.CancelFunc
	conn   *net.UDPConn

	mu          sync.Mutex
	callID      string
	fromTag     string
	toTag       string
	cseq        int
	contact     string
	answered    bool
	byeReceived chan struct{}
	byeOnce     sync.Once
	rtpCount    chan int
	rtp         *net.UDPConn

	expectedServerSSRC uint32
	serverRTPPort      int

	// Captured from incoming INVITE for test assertions
	inviteFromHeader string
	invitePAI        string
}

func newTrunkPeer(t *testing.T) *trunkPeer {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	rtp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	p := &trunkPeer{
		t:           t,
		ctx:         ctx,
		cancel:      cancel,
		conn:        conn,
		byeReceived: make(chan struct{}),
		rtpCount:    make(chan int, 1),
		rtp:         rtp,
	}

	go p.listen()
	return p
}

func (p *trunkPeer) Port() int {
	return p.conn.LocalAddr().(*net.UDPAddr).Port
}

func (p *trunkPeer) RTPPort() int {
	return p.rtp.LocalAddr().(*net.UDPAddr).Port
}

func (p *trunkPeer) Close() {
	p.cancel()
	if p.rtp != nil {
		p.rtp.Close()
	}
	if p.conn != nil {
		p.conn.Close()
	}
}

func (p *trunkPeer) listen() {
	buf := make([]byte, 4096)
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		_ = p.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, remoteAddr, err := p.conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		msg := string(buf[:n])
		writeFunc := func(data []byte) error {
			_, err := p.conn.WriteToUDP(data, remoteAddr)
			return err
		}
		p.t.Logf("Trunk peer received %d bytes: %q", n, msg[:min(n, 100)])

		switch {
		case strings.HasPrefix(msg, "INVITE"):
			p.handleIncomingInvite(msg, writeFunc)
		case strings.HasPrefix(msg, "BYE"):
			p.handleIncomingBye(msg, writeFunc)
		}
	}
}

func (p *trunkPeer) handleIncomingInvite(msg string, writeFunc func([]byte) error) {
	viaHeader := extractHeader(msg, "Via")
	fromHeader := extractHeader(msg, "From")
	callID := extractHeader(msg, "Call-ID")

	p.mu.Lock()
	p.callID = callID
	p.fromTag = extractTag(fromHeader)
	p.toTag = fmt.Sprintf("trunk-peer-%d", time.Now().UnixNano())
	p.contact = extractHeader(msg, "Contact")
	p.inviteFromHeader = fromHeader
	p.invitePAI = extractHeader(msg, "P-Asserted-Identity")
	cseqLine := extractHeader(msg, "CSeq")
	if cseqLine != "" {
		parts := strings.Fields(cseqLine)
		if len(parts) > 0 {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				p.cseq = n
			}
		}
	}

	// Extract server's RTP port from SDP
	if matches := sdpPortRegex.FindStringSubmatch(msg); len(matches) == 2 {
		if port, err := strconv.Atoi(matches[1]); err == nil {
			p.serverRTPPort = port
		}
	}
	p.mu.Unlock()

	// Build SDP answer
	rtpPort := p.rtp.LocalAddr().(*net.UDPAddr).Port
	sdp := fmt.Sprintf("v=0\r\no=- %d 1 IN IP4 127.0.0.1\r\ns=trunk-peer\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		time.Now().UnixNano(), rtpPort)

	resp := fmt.Sprintf("SIP/2.0 200 OK\r\nVia: %s\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:trunk@127.0.0.1>;tag=%s\r\nCSeq: 1 INVITE\r\nContent-Type: application/sdp\r\nContact: <sip:trunk@127.0.0.1:%d>\r\nContent-Length: %d\r\n\r\n%s",
		viaHeader, callID, fromHeader, p.toTag, rtpPort, len(sdp), sdp)

	p.t.Logf("Trunk peer sending 200 OK")
	if err := writeFunc([]byte(resp)); err != nil {
		p.t.Logf("Trunk peer failed to send 200 OK: %v", err)
	} else {
		p.t.Logf("Trunk peer sent 200 OK (RTP port %d)", rtpPort)
		go receiveRTP(p.rtp, p.rtpCount, p.ctx, p.expectedServerSSRC)
	}

	p.mu.Lock()
	p.answered = true
	p.mu.Unlock()
}

func (p *trunkPeer) handleIncomingBye(msg string, writeFunc func([]byte) error) {
	resp := fmt.Sprintf("SIP/2.0 200 OK\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:trunk@127.0.0.1>;tag=%s\r\nCSeq: 2 BYE\r\nContent-Length: 0\r\n\r\n",
		p.callID, extractHeader(msg, "From"), p.toTag)
	_ = writeFunc([]byte(resp))
	p.byeOnce.Do(func() { close(p.byeReceived) })
}

func (p *trunkPeer) assertByeReceived(t *testing.T) {
	t.Helper()
	select {
	case <-p.byeReceived:
		t.Log("Trunk peer received BYE")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for BYE on trunk peer")
	}
}

func (p *trunkPeer) InviteFromHeader() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inviteFromHeader
}

func (p *trunkPeer) InvitePAI() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.invitePAI
}

var sdpPortRegex = regexp.MustCompile(`m=audio (\d+) RTP/AVP`)

func extractHeader(msg, name string) string {
	prefix := name + ":"
	idx := strings.Index(msg, prefix)
	if idx == -1 {
		return ""
	}
	line := msg[idx+len(prefix):]
	if end := strings.Index(line, "\r\n"); end != -1 {
		return strings.TrimSpace(line[:end])
	}
	return ""
}

func extractTag(header string) string {
	if idx := strings.Index(header, ";tag="); idx != -1 {
		return header[idx+5:]
	}
	return ""
}

func receiveRTP(rtp *net.UDPConn, rtpCount chan<- int, ctx context.Context, expectedSSRC uint32) {
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
			if expectedSSRC != 0 && serverSSRC == expectedSSRC {
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

// StartTestServerWithTrunks creates a test server with trunk configuration.
func StartTestServerWithTrunks(t *testing.T, host string, trunks []trunk.Trunk, routes []trunk.OutboundRoute) *trunkTestServer {
	t.Helper()

	ts := integrationtest.StartTestServerWithDialplan(t, host, nil)

	trunkCfg := &trunk.TrunkConfig{
		Trunks: trunks,
		Routes: routes,
	}

	trunkMgr, err := trunk.NewTrunkManager(trunkCfg, host, ts.Addr)
	require.NoError(t, err)
	trunkMgr.Start(t.Context())

	// Wire trunk manager into the handler's config via the existing Config
	// We need to reconfigure the handler to include the trunk manager
	// For now, we'll just return the trunk manager and let tests use it

	return &trunkTestServer{
		TestServer: ts,
		TrunkMgr:   trunkMgr,
	}
}

// trunkTestServer wraps TestServer with a trunk manager.
type trunkTestServer struct {
	*integrationtest.TestServer
	TrunkMgr *trunk.TrunkManager
}

// Stop shuts down the test server and trunk manager.
func (ts *trunkTestServer) Stop() {
	ts.TrunkMgr.Stop()
	ts.TestServer.Stop()
}
