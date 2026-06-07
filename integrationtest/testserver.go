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

// StartTestServer creates and starts a trecs server with a registrar, dialplan,
// and B2BUA handler for integration testing. Logging is routed to t.Log(). The server
// binds to host:0 (random OS-assigned port). The caller must call Stop() when done.
func StartTestServer(t *testing.T, host string) *TestServer {
	t.Helper()
	return StartTestServerWithDialplan(t, host, nil)
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
// when done.
func StartTestServerWithDialplan(t *testing.T, host string, dp dialplan.Dialplan) *TestServer {
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

	h := b2bua.NewHandler(b2bua.Config{
		Registrar:      reg,
		SessionManager: sm,
		Server:         srv,
		ServerIP:       "127.0.0.1",
		ServerAddr:     addr,
		UACManager:     uacMgr,
		Dialplan:       dp,
		RTPPortMin:     0,
		RTPPortMax:     0,
	})

	srv.On(proto.SIPMethodREGISTER, reg.HandleRegister)
	srv.On(proto.SIPMethodOPTIONS, h.HandleOptions)
	srv.On(proto.SIPMethodINVITE, h.HandleInvite)
	srv.On(proto.SIPMethodBYE, h.HandleBye)
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

// GetPort returns the appropriate port for the given transport.
func GetPort(ts *TestServer, transport string) int {
	if transport == "tcp" {
		return ts.TCPPort
	}
	return ts.UDPPort
}
