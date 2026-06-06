package sip

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripBrackets(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"<sip:user@host>", "sip:user@host"},
		{"sip:user@host", "sip:user@host"},
		{"<sip:user@host", "<sip:user@host"},
		{"", ""},
	}
	for _, tc := range tests {
		got := StripBrackets(tc.input)
		assert.Equal(t, tc.want, got, "StripBrackets(%q)", tc.input)
	}
}

func TestExtractUser(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"sip:alice@example.com", "alice"},
		{"sip:bob@192.168.1.1:5060", "bob"},
		{"sip:user@host", "user"},
		{"sip:user", ""},
		{"sip:@host", ""},
		{"not-a-sip-uri", ""},
	}
	for _, tc := range tests {
		got := ExtractUser(tc.uri)
		assert.Equal(t, tc.want, got, "ExtractUser(%q)", tc.uri)
	}
}

func TestNormalizeAOR(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sip:alice@example.com:5060", "sip:alice@example.com"},
		{"sip:alice@example.com", "sip:alice@example.com"},
		{"sip:bob@192.168.1.1:5060;transport=tcp", "sip:bob@192.168.1.1"},
		{"no-at-sign", "no-at-sign"},
		{"sip:alice@", "sip:alice@"},
	}
	for _, tc := range tests {
		got := NormalizeAOR(tc.input)
		assert.Equal(t, tc.want, got, "NormalizeAOR(%q)", tc.input)
	}
}

func TestGenerateTag(t *testing.T) {
	tag := GenerateTag()
	assert.Len(t, tag, 16, "GenerateTag() length")
	_, err := hex.DecodeString(tag)
	assert.NoError(t, err, "GenerateTag() should be valid hex")
}

func TestGenerateTagUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		tag := GenerateTag()
		assert.False(t, seen[tag], "duplicate tag generated: %q", tag)
		seen[tag] = true
	}
}

func TestURIHost(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"sip:alice@example.com:5060", "example.com:5060"},
		{"sip:alice@example.com", "example.com"},
		{"sip:alice@192.168.1.1:5060;transport=tcp", "192.168.1.1:5060"},
		{"sip:alice@host", "host"},
		{"sip:host", "host"},
	}
	for _, tc := range tests {
		got := uriHost(tc.uri)
		assert.Equal(t, tc.want, got, "uriHost(%q)", tc.uri)
	}
}

func TestURIHostname(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"sip:alice@example.com:5060", "example.com"},
		{"sip:alice@example.com", "example.com"},
		{"sip:bob@192.168.1.1:5060;transport=tcp", "192.168.1.1"},
		{"sip:host", "host"},
	}
	for _, tc := range tests {
		got := uriHostname(tc.uri)
		assert.Equal(t, tc.want, got, "uriHostname(%q)", tc.uri)
	}
}

func BenchmarkStripBrackets(b *testing.B) {
	input := "<sip:alice@example.com:5060;transport=tcp>"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		StripBrackets(input)
	}
}

func BenchmarkStripBracketsNoBrackets(b *testing.B) {
	input := "sip:alice@example.com:5060;transport=tcp"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		StripBrackets(input)
	}
}

func BenchmarkExtractUser(b *testing.B) {
	uri := "sip:alice@example.com:5060"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ExtractUser(uri)
	}
}

func BenchmarkExtractUserNoUser(b *testing.B) {
	uri := "sip:example.com"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ExtractUser(uri)
	}
}

func BenchmarkNormalizeAOR(b *testing.B) {
	uri := "sip:alice@example.com:5060;transport=tcp"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		NormalizeAOR(uri)
	}
}

func BenchmarkNormalizeAORNoPort(b *testing.B) {
	uri := "sip:alice@example.com"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		NormalizeAOR(uri)
	}
}

func BenchmarkGenerateTag(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		GenerateTag()
	}
}

func BenchmarkURIHost(b *testing.B) {
	uri := "sip:alice@example.com:5060;transport=tcp"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		uriHost(uri)
	}
}

func BenchmarkURIHostname(b *testing.B) {
	uri := "sip:alice@example.com:5060;transport=tcp"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		uriHostname(uri)
	}
}

func BenchmarkExtractSIPURI(b *testing.B) {
	contactURI := "sip:alice@example.com:5070;transport=tcp"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		extractSIPURI(contactURI)
	}
}

func BenchmarkExtractSIPURIDefaults(b *testing.B) {
	contactURI := "sip:alice@example.com"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		extractSIPURI(contactURI)
	}
}

func TestExtractSIPURI(t *testing.T) {
	tests := []struct {
		contactURI    string
		wantHost      string
		wantPort      int
		wantTransport string
	}{
		{"sip:alice@example.com:5070;transport=tcp", "example.com", 5070, "TCP"},
		{"sip:bob@192.168.1.1", "192.168.1.1", 5060, "UDP"},
		{"sip:carol@host:5061", "host", 5061, "UDP"},
		{"sip:user@host:5060;transport=udp", "host", 5060, "UDP"},
		{"sip:host:5080;transport=tls", "host", 5080, "TLS"},
		{"sip:user@[::1]:5060", "::1", 5060, "UDP"},
		{"sip:user@host", "host", 5060, "UDP"},
	}
	for _, tc := range tests {
		host, port, transport := extractSIPURI(tc.contactURI)
		assert.Equal(t, tc.wantHost, host, "extractSIPURI(%q) host", tc.contactURI)
		assert.Equal(t, tc.wantPort, port, "extractSIPURI(%q) port", tc.contactURI)
		assert.True(t, strings.EqualFold(transport, tc.wantTransport),
			"extractSIPURI(%q) transport = %q, want %q", tc.contactURI, transport, tc.wantTransport)
	}
}
