package trunk

import (
	"context"
	"log/slog"
	"net/netip"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManagerWithTrunks(trunks []Trunk, routes []OutboundRoute) *TrunkManager {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := &TrunkManager{
		trunks:      make(map[string]*Trunk, len(trunks)),
		routes:      make([]*OutboundRoute, len(routes)),
		activeCalls: make(map[string]int),
		regs:        make(map[string]*registrationState),
		serverIP:    "127.0.0.1",
		serverAddr:  "127.0.0.1:5060",
		logger:      logger,
	}
	for i := range trunks {
		t := &trunks[i]
		m.trunks[t.Name] = t
	}
	for i := range routes {
		r := &routes[i]
		r.compiled = regexp.MustCompile(r.Pattern)
		m.routes[i] = r
	}
	return m
}

func TestAcquireChannel_Unlimited(t *testing.T) {
	m := newTestManagerWithTrunks(
		[]Trunk{{Name: "t1", Type: TrunkTypeStatic, Host: "h", Port: 5060}},
		nil,
	)

	assert.True(t, m.AcquireChannel("t1"))
	assert.True(t, m.AcquireChannel("t1"))
	assert.True(t, m.AcquireChannel("t1"))
	assert.Equal(t, 3, m.ActiveCalls("t1"))
}

func TestAcquireChannel_Limited(t *testing.T) {
	m := newTestManagerWithTrunks(
		[]Trunk{{Name: "t1", Type: TrunkTypeStatic, Host: "h", Port: 5060, MaxChannels: 2}},
		nil,
	)

	assert.True(t, m.AcquireChannel("t1"))
	assert.True(t, m.AcquireChannel("t1"))
	assert.False(t, m.AcquireChannel("t1"))
}

func TestAcquireChannel_UnknownTrunk(t *testing.T) {
	m := newTestManagerWithTrunks(nil, nil)
	assert.False(t, m.AcquireChannel("nonexistent"))
}

func TestReleaseChannel(t *testing.T) {
	m := newTestManagerWithTrunks(
		[]Trunk{{Name: "t1", Type: TrunkTypeStatic, Host: "h", Port: 5060, MaxChannels: 1}},
		nil,
	)

	assert.True(t, m.AcquireChannel("t1"))
	m.ReleaseChannel("t1")
	assert.True(t, m.AcquireChannel("t1"))
}

func TestReleaseChannel_DoesNotGoNegative(t *testing.T) {
	m := newTestManagerWithTrunks(
		[]Trunk{{Name: "t1", Type: TrunkTypeStatic, Host: "h", Port: 5060}},
		nil,
	)

	m.ReleaseChannel("t1")
	m.ReleaseChannel("t1")
	m.ReleaseChannel("t1")
	assert.Equal(t, 0, m.ActiveCalls("t1"))
}

func TestTrunkByName(t *testing.T) {
	m := newTestManagerWithTrunks(
		[]Trunk{{Name: "t1", Type: TrunkTypeStatic, Host: "h", Port: 5060}},
		nil,
	)

	assert.NotNil(t, m.TrunkByName("t1"))
	assert.Nil(t, m.TrunkByName("nonexistent"))
}

func TestTrustedIPMatches(t *testing.T) {
	trunk := &Trunk{
		Name:       "office",
		Type:       TrunkTypeStatic,
		Host:       "10.0.1.50",
		Port:       5060,
		TrustedIPs: []string{"10.0.1.0/24", "192.168.0.0/16"},
	}
	// Populate validCIDRs as TrunkConfig.validate() would
	for _, cidr := range trunk.TrustedIPs {
		prefix, err := netip.ParsePrefix(cidr)
		require.NoError(t, err)
		trunk.validCIDRs = append(trunk.validCIDRs, prefix)
	}

	assert.True(t, trunk.TrustedIPMatches("10.0.1.1"))
	assert.True(t, trunk.TrustedIPMatches("10.0.1.254"))
	assert.True(t, trunk.TrustedIPMatches("192.168.1.100"))
	assert.True(t, trunk.TrustedIPMatches("192.168.0.1"))
	assert.False(t, trunk.TrustedIPMatches("10.0.2.1"))
	assert.False(t, trunk.TrustedIPMatches("172.16.0.1"))
}

func TestNewTrunkManager_DuplicateName(t *testing.T) {
	cfg := &TrunkConfig{
		Trunks: []Trunk{
			{Name: "t1", Type: TrunkTypeStatic, Host: "h1", Port: 5060},
			{Name: "t1", Type: TrunkTypeStatic, Host: "h2", Port: 5061},
		},
	}
	for i := range cfg.Routes {
		cfg.Routes[i].compiled = regexp.MustCompile(cfg.Routes[i].Pattern)
	}
	_, err := NewTrunkManager(cfg, "127.0.0.1", ":5060")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestTrunkManager_StartStop(t *testing.T) {
	cfg := &TrunkConfig{
		Trunks: []Trunk{
			{Name: "t1", Type: TrunkTypeStatic, Host: "h", Port: 5060},
		},
	}
	// Compile routes (none in this test)
	cfg.Routes = []OutboundRoute{}

	m, err := NewTrunkManager(cfg, "127.0.0.1", ":5060")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)

	// Start again should be idempotent
	m.Start(ctx)

	cancel()
	m.Stop()
}
