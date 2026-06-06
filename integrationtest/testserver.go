package integrationtest

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/thorsager/trecs/internal/logutil"
	trecs_sip "github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

// TestServer wraps a trecs SIP server and registrar for integration tests.
type TestServer struct {
	ctx     context.Context
	Server  *trecs_sip.Server
	Reg     *trecs_sip.Registrar
	cancel  context.CancelFunc
	oldLog  *slog.Logger
	Addr    string
	Domain  string
	UDPPort int
	TCPPort int
}

// StartTestServer creates and starts a trecs server with a registrar for
// integration testing. Logging is routed to t.Log(). The server binds to
// host:0 (random OS-assigned port). The caller must call Stop() when done.
func StartTestServer(t *testing.T, host string) *TestServer {
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

	srv.On(proto.SIPMethodREGISTER, reg.HandleRegister)
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
		Server:  srv,
		Reg:     reg,
		Addr:    tcpAddr,
		Domain:  actualHost,
		UDPPort: udpPort,
		TCPPort: tcpPort,
		ctx:     ctx,
		cancel:  cancel,
		oldLog:  oldLog,
	}
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
