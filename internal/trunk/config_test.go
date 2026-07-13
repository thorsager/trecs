package trunk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "trunks.json")
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err)
	return path
}

func TestLoadConfig_Valid(t *testing.T) {
	json := `{
		"trunks": [
			{
				"name": "twilio",
				"type": "registration",
				"host": "sip.twilio.com",
				"port": 5060,
				"transport": "udp",
				"auth_user": "TR123",
				"auth_password": "secret",
				"realm": "sip.twilio.com",
				"max_channels": 10,
				"caller_id": "+15551234567"
			},
			{
				"name": "office",
				"type": "static",
				"host": "10.0.1.50",
				"port": 5061,
				"transport": "tcp",
				"trusted_ips": ["10.0.1.0/24"],
				"max_channels": 20
			}
		],
		"outbound_routes": [
			{
				"name": "local",
				"pattern": "^\\d{3,5}$",
				"trunk": "office"
			},
			{
				"name": "external",
				"pattern": "^1\\d{10}$",
				"trunk": "twilio",
				"strip_digits": 1
			}
		]
	}`
	cfg, err := LoadConfig(writeTempConfig(t, json))
	require.NoError(t, err)
	require.Len(t, cfg.Trunks, 2)
	require.Len(t, cfg.Routes, 2)

	twilio := cfg.Trunks[0]
	assert.Equal(t, "twilio", twilio.Name)
	assert.Equal(t, TrunkTypeRegistration, twilio.Type)
	assert.Equal(t, "sip.twilio.com", twilio.Host)
	assert.Equal(t, 5060, twilio.Port)
	assert.Equal(t, "udp", twilio.Transport)
	assert.Equal(t, "TR123", twilio.AuthUser)
	assert.Equal(t, 10, twilio.MaxChannels)
	assert.Equal(t, "+15551234567", twilio.CallerID)

	office := cfg.Trunks[1]
	assert.Equal(t, "office", office.Name)
	assert.Equal(t, TrunkTypeStatic, office.Type)
	assert.Equal(t, "tcp", office.Transport)
	assert.Len(t, office.validCIDRs, 1)
	assert.True(t, office.TrustedIPMatches("10.0.1.42"))
	assert.False(t, office.TrustedIPMatches("10.0.2.1"))

	local := cfg.Routes[0]
	assert.Equal(t, "local", local.Name)
	assert.True(t, local.compiled.MatchString("123"))
	assert.False(t, local.compiled.MatchString("123456"))

	external := cfg.Routes[1]
	assert.Equal(t, 1, external.StripDigits)
}

func TestLoadConfig_StripHeaders(t *testing.T) {
	json := `{
		"trunks": [
			{
				"name": "t1",
				"type": "static",
				"host": "example.com",
				"port": 5060,
				"strip_headers": ["X-Extension", "X-Internal-Info"]
			}
		],
		"outbound_routes": [
			{
				"name": "r1",
				"pattern": "^\\d+$",
				"trunk": "t1"
			}
		]
	}`
	cfg, err := LoadConfig(writeTempConfig(t, json))
	require.NoError(t, err)
	require.Len(t, cfg.Trunks, 1)
	assert.Equal(t, []string{"X-Extension", "X-Internal-Info"}, cfg.Trunks[0].StripHeaders)
}

func TestLoadConfig_StripHeadersDefaultEmpty(t *testing.T) {
	json := `{
		"trunks": [
			{
				"name": "t1",
				"type": "static",
				"host": "example.com",
				"port": 5060
			}
		],
		"outbound_routes": [
			{
				"name": "r1",
				"pattern": "^\\d+$",
				"trunk": "t1"
			}
		]
	}`
	cfg, err := LoadConfig(writeTempConfig(t, json))
	require.NoError(t, err)
	assert.Nil(t, cfg.Trunks[0].StripHeaders)
}

func TestLoadConfig_LocalIP(t *testing.T) {
	json := `{
		"trunks": [
			{
				"name": "t1",
				"type": "static",
				"host": "example.com",
				"port": 5060,
				"local_ip": "10.0.1.10"
			}
		],
		"outbound_routes": []
	}`
	cfg, err := LoadConfig(writeTempConfig(t, json))
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.10", cfg.Trunks[0].LocalIP)
}

func TestLoadConfig_LocalIPDefaultEmpty(t *testing.T) {
	json := `{
		"trunks": [
			{
				"name": "t1",
				"type": "static",
				"host": "example.com",
				"port": 5060
			}
		],
		"outbound_routes": []
	}`
	cfg, err := LoadConfig(writeTempConfig(t, json))
	require.NoError(t, err)
	assert.Empty(t, cfg.Trunks[0].LocalIP)
}

func TestTrunk_LocalIPWithDefault(t *testing.T) {
	trk := &Trunk{LocalIP: "10.0.1.10"}
	assert.Equal(t, "10.0.1.10", trk.LocalIPWithDefault("127.0.0.1"))

	trk2 := &Trunk{LocalIP: ""}
	assert.Equal(t, "192.168.1.1", trk2.LocalIPWithDefault("192.168.1.1"))
}

func TestLoadConfig_DefaultTransport(t *testing.T) {
	json := `{
		"trunks": [
			{
				"name": "t1",
				"type": "registration",
				"host": "example.com",
				"port": 5060,
				"auth_user": "u",
				"auth_password": "p"
			}
		],
		"outbound_routes": []
	}`
	cfg, err := LoadConfig(writeTempConfig(t, json))
	require.NoError(t, err)
	assert.Equal(t, "udp", cfg.Trunks[0].Transport)
}

func TestLoadConfig_DefaultsRegisterURI(t *testing.T) {
	json := `{
		"trunks": [
			{
				"name": "t1",
				"type": "registration",
				"host": "sip.example.com",
				"port": 5060,
				"auth_user": "myuser",
				"auth_password": "mypass"
			}
		],
		"outbound_routes": []
	}`
	cfg, err := LoadConfig(writeTempConfig(t, json))
	require.NoError(t, err)
	assert.Equal(t, "sip:myuser@sip.example.com", cfg.Trunks[0].RegisterURIString())
}

func TestLoadConfig_ExplicitRegisterURI(t *testing.T) {
	json := `{
		"trunks": [
			{
				"name": "t1",
				"type": "registration",
				"host": "sip.example.com",
				"port": 5060,
				"auth_user": "myuser",
				"auth_password": "mypass",
				"register_uri": "sip:+15551234567@provider.com"
			}
		],
		"outbound_routes": []
	}`
	cfg, err := LoadConfig(writeTempConfig(t, json))
	require.NoError(t, err)
	assert.Equal(t, "sip:+15551234567@provider.com", cfg.Trunks[0].RegisterURIString())
}

func TestLoadConfig_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			"empty trunks",
			`{"trunks": [], "outbound_routes": []}`,
			"at least one trunk",
		},
		{
			"missing name",
			`{"trunks": [{"type": "static", "host": "h", "port": 5060}], "outbound_routes": []}`,
			"name is required",
		},
		{
			"missing host",
			`{"trunks": [{"name": "t1", "type": "static", "port": 5060}], "outbound_routes": []}`,
			"host is required",
		},
		{
			"bad port",
			`{"trunks": [{"name": "t1", "type": "static", "host": "h", "port": 0}], "outbound_routes": []}`,
			"port 0 out of range",
		},
		{
			"bad transport",
			`{"trunks": [{"name": "t1", "type": "static", "host": "h", "port": 5060, "transport": "sctp"}], "outbound_routes": []}`,
			"unsupported transport",
		},
		{
			"missing auth_user for registration",
			`{"trunks": [{"name": "t1", "type": "registration", "host": "h", "port": 5060, "auth_password": "p"}], "outbound_routes": []}`,
			"auth_user is required",
		},
		{
			"missing auth_pass for registration",
			`{"trunks": [{"name": "t1", "type": "registration", "host": "h", "port": 5060, "auth_user": "u"}], "outbound_routes": []}`,
			"auth_password is required",
		},
		{
			"duplicate trunk name",
			`{"trunks": [{"name": "t1", "type": "static", "host": "h1", "port": 5060}, {"name": "t1", "type": "static", "host": "h2", "port": 5061}], "outbound_routes": []}`,
			"duplicate trunk name",
		},
		{
			"bad CIDR",
			`{"trunks": [{"name": "t1", "type": "static", "host": "h", "port": 5060, "trusted_ips": ["not-a-cidr"]}], "outbound_routes": []}`,
			"trusted_ip",
		},
		{
			"route missing trunk ref",
			`{"trunks": [{"name": "t1", "type": "static", "host": "h", "port": 5060}], "outbound_routes": [{"name": "r1", "pattern": "^\\d+$", "trunk": "nonexistent"}]}`,
			"references unknown trunk",
		},
		{
			"bad route pattern",
			`{"trunks": [{"name": "t1", "type": "static", "host": "h", "port": 5060}], "outbound_routes": [{"name": "r1", "pattern": "[invalid", "trunk": "t1"}]}`,
			"pattern",
		},
		{
			"negative strip_digits",
			`{"trunks": [{"name": "t1", "type": "static", "host": "h", "port": 5060}], "outbound_routes": [{"name": "r1", "pattern": "^\\d+$", "trunk": "t1", "strip_digits": -1}]}`,
			"strip_digits must be >= 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(writeTempConfig(t, tt.json))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
