package integrationtest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/thorsager/trecs/internal/b2bua"
	"github.com/thorsager/trecs/internal/dialplan"
	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/internal/media"
	trecs_sip "github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

// TestServer wraps a trecs SIP server and registrar for integration tests.
type TestServer struct {
	ctx      context.Context
	Server   *trecs_sip.Server
	Reg      *trecs_sip.Registrar
	Handler  *b2bua.Handler
	Dialplan dialplan.Dialplan
	cancel   context.CancelFunc
	oldLog   *slog.Logger
	Addr     string
	Domain   string
	UDPPort  int
	TCPPort  int
}

// ServerOption configures the test server.
type ServerOption func(*b2bua.Config)

// WithPRACK enables PRACK (RFC 3262) support.
func WithPRACK() ServerOption {
	return func(cfg *b2bua.Config) {
		cfg.PRACKEnabled = true
	}
}

// StartTestServer creates and starts a trecs server with a registrar, dialplan,
// and B2BUA handler for integration testing. Logging is routed to t.Log(). The server
// binds to host:0 (random OS-assigned port). The caller must call Stop() when done.
func StartTestServer(t *testing.T, host string) *TestServer {
	t.Helper()
	return StartTestServerWithDialplan(t, host, nil)
}

// SetProxyPasswordStore enables proxy authentication on the B2BUA handler.
func (ts *TestServer) SetProxyPasswordStore(store trecs_sip.PasswordStore) {
	ts.Handler.SetProxyPasswordStore(store, ts.ctx)
}

// SetMaxFailedAuthAttempts configures the retry/lockout threshold for both
// registrar and proxy authentication.
func (ts *TestServer) SetMaxFailedAuthAttempts(n int) {
	ts.Reg.SetMaxFailedAuthAttempts(n)
	ts.Handler.SetMaxFailedAuthAttempts(n)
}

// Stop shuts down the test server and restores the original slog default.
func (ts *TestServer) Stop() {
	ts.cancel()
	ts.Server.Close()
	time.Sleep(100 * time.Millisecond)
	slog.SetDefault(ts.oldLog)
}

// Port returns the port number the test server is listening on.
// Returns the TCP port by default; use UDPPort or TCPPort fields directly
// when the transport matters.
func (ts *TestServer) Port() int {
	return ts.TCPPort
}

// StartTestServerWithDialplan creates and starts a trecs server with a registrar,
// dialplan, and B2BUA handler for integration testing. Logging is routed to t.Log().
// The server binds to host:0 (random OS-assigned port). The caller must call Stop()
// when done. Options can configure PRACK and other features.
func StartTestServerWithDialplan(t *testing.T, host string, dp dialplan.Dialplan, opts ...ServerOption) *TestServer {
	t.Helper()

	oldLog := slog.Default()
	slog.SetDefault(logutil.NewTestLogger(t))

	ctx, cancel := context.WithCancel(t.Context())

	addr := host + ":0"
	srv, err := trecs_sip.NewServer(addr)
	if err != nil {
		cancel()
		slog.SetDefault(oldLog)
		t.Fatalf("failed to create server: %v", err)
	}

	reg := trecs_sip.NewRegistrar()
	go reg.Start(ctx)

	sm := media.NewSessionManager()
	uacMgr := trecs_sip.NewUACManager()

	cfg := b2bua.Config{
		Registrar:      reg,
		SessionManager: sm,
		Server:         srv,
		ServerIP:       host,
		ServerAddr:     addr,
		UACManager:     uacMgr,
		Dialplan:       dp,
		RTPPortMin:     0,
		RTPPortMax:     0,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	h := b2bua.NewHandler(cfg)

	srv.On(proto.SIPMethodREGISTER, reg.HandleRegister)
	srv.On(proto.SIPMethodOPTIONS, h.HandleOptions)
	srv.On(proto.SIPMethodINVITE, h.HandleInvite)
	srv.On(proto.SIPMethodBYE, h.HandleBye)
	srv.On(proto.SIPMethodPRACK, h.HandlePRACK)
	srv.OnAck(h.HandleAck)
	srv.OnResponse(h.HandleResponse)

	srv.Start()

	time.Sleep(50 * time.Millisecond)

	udpAddr := srv.UDPTransport().LocalAddr().String()
	tcpAddr := srv.TCPTransport().LocalAddr().String()
	_, udpPortStr, _ := net.SplitHostPort(udpAddr)
	_, tcpPortStr, _ := net.SplitHostPort(tcpAddr)
	udpPort, _ := strconv.Atoi(udpPortStr)
	tcpPort, _ := strconv.Atoi(tcpPortStr)

	actualHost, _, _ := net.SplitHostPort(tcpAddr)

	return &TestServer{
		Server:   srv,
		Reg:      reg,
		Handler:  h,
		Dialplan: dp,
		Addr:     tcpAddr,
		Domain:   actualHost,
		UDPPort:  udpPort,
		TCPPort:  tcpPort,
		ctx:      ctx,
		cancel:   cancel,
		oldLog:   oldLog,
	}
}

// WriteTempDialplan creates a temporary dialplan JSON file and returns the path.
// The file is created in t.TempDir() and is automatically cleaned up.
func WriteTempDialplan(t *testing.T, extensions map[string]map[string]string) string {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "trec_dialplan_*.json")
	if err != nil {
		t.Fatalf("failed to create temp dialplan: %v", err)
	}
	defer f.Close()

	cfg := map[string]interface{}{
		"extensions": extensions,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal dialplan: %v", err)
	}

	if _, err := f.Write(data); err != nil {
		t.Fatalf("failed to write dialplan: %v", err)
	}

	return f.Name()
}

// TestUserEntry defines a test user for NewTestPasswordStore.
type TestUserEntry struct {
	Username string
	Password string
	AORs     []string
}

// TestUser is a convenience constructor for TestUserEntry.
func TestUser(username, password string, aors ...string) TestUserEntry {
	return TestUserEntry{
		Username: username,
		Password: password,
		AORs:     aors,
	}
}

// testPasswordStore is an in-memory trecs_sip.PasswordStore for tests.
type testPasswordStore struct {
	realm     string
	algorithm string
	ha1s      map[string]string
	aors      map[string][]string
}

func (s *testPasswordStore) Realm() string     { return s.realm }
func (s *testPasswordStore) Algorithm() string { return s.algorithm }
func (s *testPasswordStore) HA1(username string) (string, bool) {
	h, ok := s.ha1s[username]
	return h, ok
}

func (s *testPasswordStore) AORs(username string) ([]string, bool) {
	a, ok := s.aors[username]
	return a, ok
}

// NewTestPasswordStore creates an in-memory PasswordStore for testing.
// HA1 hashes are computed automatically from the given plaintext passwords.
func NewTestPasswordStore(realm, algorithm string, users ...TestUserEntry) trecs_sip.PasswordStore {
	s := &testPasswordStore{
		realm:     realm,
		algorithm: algorithm,
		ha1s:      make(map[string]string, len(users)),
		aors:      make(map[string][]string, len(users)),
	}
	for _, u := range users {
		s.ha1s[u.Username] = trecs_sip.ComputeHA1(u.Username, realm, u.Password, algorithm)
		s.aors[u.Username] = u.AORs
	}
	return s
}

// StartTestServerWithAuthUsers creates a test server with Digest authentication for
// REGISTER requests, using the given PasswordStore.
func StartTestServerWithAuthUsers(t *testing.T, host string, store trecs_sip.PasswordStore) *TestServer {
	t.Helper()

	ts := StartTestServerWithDialplan(t, host, nil)
	ts.Reg.SetPasswordStore(store)
	ts.SetProxyPasswordStore(store)

	return ts
}

// GetPort returns the appropriate port for the given transport.
func GetPort(ts *TestServer, transport string) int {
	if transport == "tcp" {
		return ts.TCPPort
	}
	return ts.UDPPort
}
