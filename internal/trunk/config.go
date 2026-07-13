package trunk

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strings"
)

type TrunkType string

const (
	TrunkTypeStatic       TrunkType = "static"
	TrunkTypeRegistration TrunkType = "registration"
)

type Trunk struct {
	Name              string    `json:"name"`
	Type              TrunkType `json:"type"`
	Host              string    `json:"host"`
	Port              int       `json:"port"`
	Transport         string    `json:"transport"`
	AuthUser          string    `json:"auth_user,omitempty"`
	AuthPass          string    `json:"auth_password,omitempty"`
	Realm             string    `json:"realm,omitempty"`
	Codecs            []string  `json:"codecs,omitempty"`
	MaxChannels       int       `json:"max_channels,omitempty"`
	CallerID          string    `json:"caller_id,omitempty"`
	TrustedIPs        []string  `json:"trusted_ips,omitempty"`
	RegisterURI       string    `json:"register_uri,omitempty"`
	StripHeaders      []string  `json:"strip_headers,omitempty"`
	SessionExpiresSec int       `json:"session_expires_sec,omitempty"`
	LocalIP           string    `json:"local_ip,omitempty"`
	validCIDRs        []netip.Prefix
}

type OutboundRoute struct {
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	TrunkName   string `json:"trunk"`
	StripDigits int    `json:"strip_digits,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	compiled    *regexp.Regexp
}

type TrunkConfig struct {
	Trunks []Trunk         `json:"trunks"`
	Routes []OutboundRoute `json:"outbound_routes"`
}

func LoadConfig(path string) (*TrunkConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("trunk: reading config: %w", err)
	}
	var cfg TrunkConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("trunk: parsing config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("trunk: invalid config: %w", err)
	}
	return &cfg, nil
}

func (cfg *TrunkConfig) validate() error {
	if len(cfg.Trunks) == 0 {
		return errors.New("at least one trunk is required")
	}

	trunkNames := make(map[string]bool)
	for i := range cfg.Trunks {
		t := &cfg.Trunks[i]
		if err := t.validate(); err != nil {
			return fmt.Errorf("trunk %q: %w", t.Name, err)
		}
		if trunkNames[t.Name] {
			return fmt.Errorf("duplicate trunk name %q", t.Name)
		}
		trunkNames[t.Name] = true

		for _, cidr := range t.TrustedIPs {
			prefix, err := netip.ParsePrefix(cidr)
			if err != nil {
				return fmt.Errorf("trusted_ip %q: %w", cidr, err)
			}
			t.validCIDRs = append(t.validCIDRs, prefix)
		}
	}

	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if err := r.validate(); err != nil {
			return fmt.Errorf("route %q: %w", r.Name, err)
		}
		if !trunkNames[r.TrunkName] {
			return fmt.Errorf("route %q references unknown trunk %q", r.Name, r.TrunkName)
		}
	}

	return nil
}

func (t *Trunk) validate() error {
	if t.Name == "" {
		return errors.New("name is required")
	}
	if t.Host == "" {
		return errors.New("host is required")
	}
	if t.Port < 1 || t.Port > 65535 {
		return fmt.Errorf("port %d out of range [1-65535]", t.Port)
	}
	switch strings.ToLower(t.Transport) {
	case "udp", "tcp":
	case "":
		t.Transport = "udp"
	default:
		return fmt.Errorf("unsupported transport %q (use udp or tcp)", t.Transport)
	}
	switch t.Type {
	case TrunkTypeStatic, TrunkTypeRegistration:
	default:
		return fmt.Errorf("unsupported type %q (use static or registration)", t.Type)
	}
	if t.Type == TrunkTypeRegistration {
		if t.AuthUser == "" {
			return errors.New("auth_user is required for registration trunks")
		}
		if t.AuthPass == "" {
			return errors.New("auth_password is required for registration trunks")
		}
	}
	if t.MaxChannels < 0 {
		return errors.New("max_channels must be >= 0")
	}
	return nil
}

func (r *OutboundRoute) validate() error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	if r.Pattern == "" {
		return errors.New("pattern is required")
	}
	re, err := regexp.Compile(r.Pattern)
	if err != nil {
		return fmt.Errorf("pattern %q: %w", r.Pattern, err)
	}
	r.compiled = re
	if r.StripDigits < 0 {
		return errors.New("strip_digits must be >= 0")
	}
	if r.TrunkName == "" {
		return errors.New("trunk reference is required")
	}
	return nil
}

func (t *Trunk) TrustedIPMatches(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, prefix := range t.validCIDRs {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (t *Trunk) LocalIPWithDefault(defaultIP string) string {
	if t.LocalIP != "" {
		return t.LocalIP
	}
	return defaultIP
}

func (t *Trunk) RegisterURIString() string {
	if t.RegisterURI != "" {
		return t.RegisterURI
	}
	return fmt.Sprintf("sip:%s@%s", t.AuthUser, t.Host)
}
