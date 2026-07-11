package trunk

import (
	"log/slog"
	"os"
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type testManager struct {
	trunks map[string]*Trunk
	routes []*OutboundRoute
	logger *slog.Logger
	mu     sync.RWMutex
}

func (m *testManager) MatchRoute(user string) (*Trunk, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, r := range m.routes {
		locs := r.compiled.FindStringIndex(user)
		if locs == nil {
			continue
		}
		trunk, ok := m.trunks[r.TrunkName]
		if !ok {
			continue
		}
		transformed := applyDigitManipulation(user, r.StripDigits, r.Prefix)
		return trunk, transformed, true
	}
	return nil, "", false
}

func (m *testManager) TrunkByName(name string) *Trunk {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.trunks[name]
}

func TestApplyDigitManipulation(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		stripDigits int
		prefix      string
		want        string
	}{
		{"no manipulation", "14155551234", 0, "", "14155551234"},
		{"strip one", "14155551234", 1, "", "4155551234"},
		{"strip three", "01144123456789", 3, "", "44123456789"},
		{"prefix only", "12345", 0, "+1", "+112345"},
		{"strip and prefix", "14155551234", 1, "+", "+4155551234"},
		{"strip more than length", "12", 5, "", "12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyDigitManipulation(tt.input, tt.stripDigits, tt.prefix)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMatchRoute(t *testing.T) {
	m := testRouteManager(t)

	tests := []struct {
		name       string
		user       string
		wantMatch  bool
		wantTrunk  string
		wantResult string
	}{
		{"match local ext", "123", true, "office", "123"},
		{"match local ext 5-digit", "12345", true, "office", "12345"},
		{"match us number", "14155551234", true, "twilio", "4155551234"},
		{"no match long", "123456", false, "", ""},
		{"no match non-digit", "abc", false, "", ""},
		{"match after strip", "14155551234", true, "twilio", "4155551234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trunk, result, ok := m.MatchRoute(tt.user)
			if !tt.wantMatch {
				assert.False(t, ok)
				return
			}
			assert.True(t, ok)
			assert.Equal(t, tt.wantTrunk, trunk.Name)
			assert.Equal(t, tt.wantResult, result)
		})
	}
}

func testRouteManager(t *testing.T) *testManager {
	t.Helper()

	cfg := &TrunkConfig{
		Trunks: []Trunk{
			{
				Name:        "twilio",
				Type:        TrunkTypeRegistration,
				Host:        "sip.twilio.com",
				Port:        5060,
				Transport:   "udp",
				AuthUser:    "TR123",
				AuthPass:    "secret",
				MaxChannels: 10,
			},
			{
				Name:        "office",
				Type:        TrunkTypeStatic,
				Host:        "10.0.1.50",
				Port:        5060,
				Transport:   "tcp",
				MaxChannels: 20,
			},
		},
		Routes: []OutboundRoute{
			{Name: "local", Pattern: `^\d{3,5}$`, TrunkName: "office"},
			{Name: "external", Pattern: `^1\d{10}$`, TrunkName: "twilio", StripDigits: 1},
		},
	}
	// Compile routes
	for i := range cfg.Routes {
		cfg.Routes[i].compiled = mustCompilePattern(cfg.Routes[i].Pattern)
	}

	m := &testManager{
		trunks: make(map[string]*Trunk),
		routes: make([]*OutboundRoute, len(cfg.Routes)),
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	for i := range cfg.Trunks {
		tr := &cfg.Trunks[i]
		m.trunks[tr.Name] = tr
	}
	for i := range cfg.Routes {
		m.routes[i] = &cfg.Routes[i]
	}
	return m
}

func mustCompilePattern(pattern string) *regexp.Regexp {
	re, err := regexp.Compile(pattern)
	if err != nil {
		panic(err)
	}
	return re
}
