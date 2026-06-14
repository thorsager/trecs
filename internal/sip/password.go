package sip

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type PasswordStore interface {
	Realm() string
	Algorithm() string
	HA1(username string) (string, bool)
	AORs(username string) ([]string, bool)
}

type jsonUserEntry struct {
	HA1  string   `json:"ha1"`
	AORs []string `json:"aors"`
}

type jsonPasswordConfig struct {
	Realm     string                   `json:"realm"`
	Algorithm string                   `json:"algorithm"`
	Users     map[string]jsonUserEntry `json:"users"`
}

type JSONPasswordStore struct {
	realm     string
	algorithm string
	users     map[string]jsonUserEntry
}

func NewJSONPasswordStore(path string) (*JSONPasswordStore, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: reading password file: %w", err)
	}
	var cfg jsonPasswordConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("auth: parsing password file: %w", err)
	}
	if cfg.Realm == "" {
		return nil, errors.New("auth: realm is required")
	}
	if cfg.Algorithm == "" {
		cfg.Algorithm = "SHA-256"
	}
	switch cfg.Algorithm {
	case "MD5", "SHA-256", "SHA-512-256":
	default:
		return nil, fmt.Errorf("auth: unsupported algorithm: %s (supported: MD5, SHA-256, SHA-512-256)", cfg.Algorithm)
	}
	for name, u := range cfg.Users {
		if u.HA1 == "" {
			return nil, fmt.Errorf("auth: user %q has no ha1", name)
		}
		if len(u.AORs) == 0 {
			return nil, fmt.Errorf("auth: user %q has no aors", name)
		}
		for _, aor := range u.AORs {
			if aor == "" || !strings.HasPrefix(aor, "sip:") {
				return nil, fmt.Errorf("auth: user %q has invalid aor: %q", name, aor)
			}
		}
	}
	return &JSONPasswordStore{
		realm:     cfg.Realm,
		algorithm: cfg.Algorithm,
		users:     cfg.Users,
	}, nil
}

func (s *JSONPasswordStore) Realm() string {
	return s.realm
}

func (s *JSONPasswordStore) Algorithm() string {
	return s.algorithm
}

func (s *JSONPasswordStore) HA1(username string) (string, bool) {
	u, ok := s.users[username]
	if !ok {
		return "", false
	}
	return u.HA1, true
}

func (s *JSONPasswordStore) AORs(username string) ([]string, bool) {
	u, ok := s.users[username]
	if !ok {
		return nil, false
	}
	return u.AORs, true
}

func AORAllowed(allowed []string, aor string) bool {
	aorNorm := NormalizeAOR(StripURIParams(aor))
	for _, a := range allowed {
		aNorm := NormalizeAOR(a)
		if aNorm == aorNorm {
			return true
		}
	}
	return false
}
