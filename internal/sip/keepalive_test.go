package sip

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/thorsager/trecs/internal/logutil"
)

func TestKeepaliveTrackerUpdateActivity(t *testing.T) {
	kt := NewKeepaliveTracker(50*time.Millisecond, logutil.NewTestLogger(t))
	kt.UpdateActivity()

	time.Sleep(10 * time.Millisecond)
	kt.UpdateActivity()

	// Just ensure no panics
}

func TestKeepaliveTrackerRunSendsCRLF(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	kt := NewKeepaliveTracker(20*time.Millisecond, logutil.NewTestLogger(t))
	go kt.Run(ctx, client, "test-flow")

	buf := make([]byte, 4)
	server.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err := server.Read(buf)
	if err != nil {
		t.Fatalf("expected CRLF keepalive, got error: %v", err)
	}
	if n < 2 || buf[0] != '\r' || buf[1] != '\n' {
		t.Fatalf("expected CRLF, got %q", buf[:n])
	}
}

func TestKeepaliveTrackerStopsOnCancel(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx, cancel := context.WithCancel(t.Context())

	kt := NewKeepaliveTracker(10*time.Millisecond, logutil.NewTestLogger(t))

	done := make(chan struct{})
	go func() {
		kt.Run(ctx, client, "test-flow")
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not exit after context cancel")
	}
}

func TestKeepaliveTrackerUpdatePreventsSend(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	kt := NewKeepaliveTracker(100*time.Millisecond, logutil.NewTestLogger(t))
	go kt.Run(ctx, client, "test-flow")

	kt.UpdateActivity()

	// Should not receive CRLF within 30ms since we just updated activity
	server.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	buf := make([]byte, 4)
	n, err := server.Read(buf)
	if err == nil && n > 0 {
		t.Fatalf("unexpected data received (activity should suppress keepalive): %q", buf[:n])
	}
}
