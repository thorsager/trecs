package trunk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

const defaultReRegisterFraction = 0.75

type registrationState struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type TrunkManager struct {
	trunks      map[string]*Trunk
	routes      []*OutboundRoute
	activeCalls map[string]int
	regs        map[string]*registrationState
	serverIP    string
	serverAddr  string
	logger      *slog.Logger
	mu          sync.RWMutex
	started     bool
}

func NewTrunkManager(cfg *TrunkConfig, serverIP, serverAddr string) (*TrunkManager, error) {
	m := &TrunkManager{
		trunks:      make(map[string]*Trunk, len(cfg.Trunks)),
		routes:      make([]*OutboundRoute, len(cfg.Routes)),
		activeCalls: make(map[string]int),
		regs:        make(map[string]*registrationState),
		serverIP:    serverIP,
		serverAddr:  serverAddr,
		logger:      slog.Default().With("component", "trunk_manager"),
	}
	for i := range cfg.Trunks {
		t := &cfg.Trunks[i]
		if _, exists := m.trunks[t.Name]; exists {
			return nil, fmt.Errorf("duplicate trunk name %q", t.Name)
		}
		if t.validCIDRs == nil && len(t.TrustedIPs) > 0 {
			for _, cidr := range t.TrustedIPs {
				prefix, err := netip.ParsePrefix(cidr)
				if err != nil {
					return nil, fmt.Errorf("trunk %q: trusted_ip %q: %w", t.Name, cidr, err)
				}
				t.validCIDRs = append(t.validCIDRs, prefix)
			}
		}
		m.trunks[t.Name] = t
	}
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.compiled == nil {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("route %q: invalid pattern %q: %w", r.Name, r.Pattern, err)
			}
			r.compiled = re
		}
		m.routes[i] = r
	}
	return m, nil
}

func (m *TrunkManager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()

	for _, t := range m.trunks {
		if t.Type == TrunkTypeRegistration {
			m.startRegistration(ctx, t)
		}
	}
}

func (m *TrunkManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, rs := range m.regs {
		rs.cancel()
		<-rs.done
		delete(m.regs, name)
	}
	m.started = false
}

func (m *TrunkManager) startRegistration(ctx context.Context, t *Trunk) {
	regCtx, cancel := context.WithCancel(ctx)
	rs := &registrationState{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	m.mu.Lock()
	m.regs[t.Name] = rs
	m.mu.Unlock()

	go m.registrationLoop(regCtx, t, rs)
}

func (m *TrunkManager) registrationLoop(ctx context.Context, t *Trunk, rs *registrationState) {
	defer close(rs.done)

	var backoff time.Duration

	for {
		expires, err := m.registerOnce(ctx, t)
		if err != nil {
			m.logger.Error("trunk registration failed", "trunk", t.Name, "error", err)
			if ctx.Err() != nil {
				return
			}
			if backoff == 0 {
				backoff = 30 * time.Second
			} else {
				backoff *= 2
				if backoff > 5*time.Minute {
					backoff = 5 * time.Minute
				}
			}
			sleepWithCtx(ctx, backoff)
			continue
		}

		backoff = 0
		reRegisterIn := time.Duration(float64(expires) * defaultReRegisterFraction)
		m.logger.Info("trunk registered successfully", "trunk", t.Name, "reRegisterIn", reRegisterIn)

		select {
		case <-ctx.Done():
			return
		case <-time.After(reRegisterIn):
		}
	}
}

func (m *TrunkManager) registerOnce(ctx context.Context, t *Trunk) (time.Duration, error) {
	registerURI := t.RegisterURIString()
	hostPort := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	localIP := t.LocalIPWithDefault(m.serverIP)

	contact := fmt.Sprintf("<sip:trec@%s>", localIP)
	branch := sip.GenerateBranch()
	callID := fmt.Sprintf("trunk-reg-%s-%d", t.Name, time.Now().UnixNano())

	var reqBody string
	reqBody += fmt.Sprintf("REGISTER %s SIP/2.0\r\n", registerURI)
	reqBody += fmt.Sprintf("Via: SIP/2.0/%s %s;branch=%s\r\n", strings.ToUpper(t.Transport), localIP, branch)
	reqBody += fmt.Sprintf("From: %s\r\n", registerURI)
	reqBody += fmt.Sprintf("To: %s\r\n", registerURI)
	reqBody += fmt.Sprintf("Call-ID: %s\r\n", callID)
	reqBody += "CSeq: 1 REGISTER\r\n"
	reqBody += fmt.Sprintf("Contact: %s\r\n", contact)
	reqBody += "Max-Forwards: 70\r\n"
	reqBody += "Content-Length: 0\r\n\r\n"

	resp, err := sendSIP(ctx, t.Transport, hostPort, []byte(reqBody))
	if err != nil {
		return 0, fmt.Errorf("register send: %w", err)
	}

	sc := resp.StatusCode()
	switch sc {
	case 200:
		expires := parseExpires(resp, 3600)
		return time.Duration(expires) * time.Second, nil

	case 401:
		wwwAuth := resp.Headers.GetFirst("WWW-Authenticate")
		if wwwAuth == "" {
			return 0, errors.New("401 without WWW-Authenticate")
		}
		return m.authenticatedRegister(ctx, t, registerURI, hostPort, contact, callID, wwwAuth)

	default:
		return 0, fmt.Errorf("register returned %d %s", sc, resp.Status())
	}
}

func (m *TrunkManager) localIP(t *Trunk) string {
	return t.LocalIPWithDefault(m.serverIP)
}

func (m *TrunkManager) authenticatedRegister(ctx context.Context, t *Trunk, registerURI, hostPort, contact, callID, wwwAuth string) (time.Duration, error) {
	challenge, err := sip.ParseDigest(wwwAuth)
	if err != nil {
		return 0, fmt.Errorf("parse www-auth: %w", err)
	}

	ha1 := sip.ComputeHA1(t.AuthUser, challenge.Realm, t.AuthPass, challenge.Algorithm)
	nc := "00000001"
	digestResp := sip.ComputeDigestResponse(ha1, challenge.Nonce, nc,
		"deadbeef", "auth", "REGISTER", registerURI, challenge.Algorithm)

	authValue := fmt.Sprintf(`Digest username=%q, realm=%q, nonce=%q, uri=%q, response=%q, algorithm=%s, cnonce="deadbeef", nc=%s, qop=auth`,
		t.AuthUser, challenge.Realm, challenge.Nonce, registerURI, digestResp, challenge.Algorithm, nc)

	branch := sip.GenerateBranch()
	localIP := m.localIP(t)

	var body string
	body += fmt.Sprintf("REGISTER %s SIP/2.0\r\n", registerURI)
	body += fmt.Sprintf("Via: SIP/2.0/%s %s;branch=%s\r\n", strings.ToUpper(t.Transport), localIP, branch)
	body += fmt.Sprintf("From: %s\r\n", registerURI)
	body += fmt.Sprintf("To: %s\r\n", registerURI)
	body += fmt.Sprintf("Call-ID: %s\r\n", callID)
	body += "CSeq: 2 REGISTER\r\n"
	body += fmt.Sprintf("Contact: %s\r\n", contact)
	body += "Max-Forwards: 70\r\n"
	body += fmt.Sprintf("Authorization: %s\r\n", authValue)
	body += "Content-Length: 0\r\n\r\n"

	resp, err := sendSIP(ctx, t.Transport, hostPort, []byte(body))
	if err != nil {
		return 0, fmt.Errorf("auth register send: %w", err)
	}

	if resp.StatusCode() != 200 {
		return 0, fmt.Errorf("auth register returned %d %s", resp.StatusCode(), resp.Status())
	}

	expires := parseExpires(resp, 3600)
	return time.Duration(expires) * time.Second, nil
}

func sendSIP(ctx context.Context, transport, hostPort string, data []byte) (*proto.SIPMessage, error) {
	switch strings.ToLower(transport) {
	case "udp":
		return sendSIPUDP(ctx, hostPort, data)
	case "tcp":
		return sendSIPTCP(ctx, hostPort, data)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", transport)
	}
}

func sendSIPUDP(ctx context.Context, hostPort string, data []byte) (*proto.SIPMessage, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return nil, fmt.Errorf("resolve udp: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("dial udp: %w", err)
	}
	defer conn.Close()

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write udp: %w", err)
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	conn.SetReadDeadline(deadline) //nolint:errcheck

	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read udp response: %w", err)
	}

	msg, err := proto.UnmarshalSIPDatagram(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return msg, nil
}

func sendSIPTCP(ctx context.Context, hostPort string, data []byte) (*proto.SIPMessage, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, fmt.Errorf("dial tcp: %w", err)
	}
	defer conn.Close()

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("write tcp: %w", err)
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	conn.SetReadDeadline(deadline) //nolint:errcheck

	return proto.UnmarshalSIP(conn)
}

func parseExpires(resp *proto.SIPMessage, defaultSecs int) int {
	expStr := resp.Headers.GetFirst("Expires")
	if expStr == "" {
		for _, c := range resp.Headers.Get("Contact") {
			if strings.Contains(c, ";expires=") {
				for _, part := range strings.Split(c, ";") {
					part = strings.TrimSpace(part)
					k, v, ok := strings.Cut(part, "=")
					if ok && strings.EqualFold(k, "expires") {
						if n, err := strconv.Atoi(v); err == nil && n > 0 {
							return n
						}
					}
				}
			}
		}
		return defaultSecs
	}
	if n, err := strconv.Atoi(strings.TrimSpace(expStr)); err == nil && n > 0 {
		return n
	}
	return defaultSecs
}

func (m *TrunkManager) HandleResponse(msg *proto.SIPMessage) {
}

// TrustedIPMatches returns true if the given IP belongs to any static trunk's
// trusted CIDR range. IP can be with or without port.
func (m *TrunkManager) TrustedIPMatches(ip string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.trunks {
		if t.TrustedIPMatches(ip) {
			return true
		}
	}
	return false
}

func (m *TrunkManager) AcquireChannel(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.trunks[name]
	if !ok {
		return false
	}
	if t.MaxChannels == 0 {
		m.activeCalls[name]++
		return true
	}
	if m.activeCalls[name] >= t.MaxChannels {
		return false
	}
	m.activeCalls[name]++
	return true
}

func (m *TrunkManager) ReleaseChannel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeCalls[name] > 0 {
		m.activeCalls[name]--
	}
}

func (m *TrunkManager) ActiveCalls(name string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeCalls[name]
}

func sleepWithCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
