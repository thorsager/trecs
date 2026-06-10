package sip

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

// URI utility functions for SIP processing, consolidated from various files.

// StripBrackets removes surrounding angle brackets from a URI string.
// If the string has no brackets it is returned unmodified.
func StripBrackets(s string) string {
	if s != "" && s[0] == '<' {
		if closeIdx := strings.IndexByte(s, '>'); closeIdx >= 0 {
			return s[1:closeIdx]
		}
	}
	return s
}

// ExtractUser returns the user part of a SIP URI (sip:user@host ...).
func ExtractUser(uri string) string {
	uri = strings.TrimPrefix(uri, "sip:")
	user, _, found := strings.Cut(uri, "@")
	if found {
		return user
	}
	return ""
}

// NormalizeAOR strips the port from a SIP URI for registrar lookup.
func NormalizeAOR(uri string) string {
	before, after, found := strings.Cut(uri, "@")
	if found {
		host, _, hasPort := strings.Cut(after, ":")
		if hasPort {
			return before + "@" + host
		}
	}
	return uri
}

// GenerateTag creates a random SIP tag value (8 random bytes, hex-encoded).
func GenerateTag() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "trec"
	}
	return hex.EncodeToString(b)
}

// uriHost extracts the host[:port] from a SIP URI like "sip:user@host:port;params".
func uriHost(uri string) string {
	uri = strings.TrimPrefix(uri, "sip:")
	if at := strings.LastIndexByte(uri, '@'); at >= 0 {
		uri = uri[at+1:]
	}
	if semi := strings.IndexByte(uri, ';'); semi >= 0 {
		uri = uri[:semi]
	}
	return uri
}

// uriHostname extracts just the host (without port) from a SIP URI.
func uriHostname(uri string) string {
	h := uriHost(uri)
	if colon := strings.IndexByte(h, ':'); colon >= 0 {
		h = h[:colon]
	}
	return h
}

// StripURIParams removes URI parameters (everything after the first ';') from a SIP URI.
func StripURIParams(uri string) string {
	if semi := strings.IndexByte(uri, ';'); semi >= 0 {
		return uri[:semi]
	}
	return uri
}

// extractSIPURI parses a Contact URI into host, port, and transport.
func extractSIPURI(contactURI string) (host string, port int, transport string) {
	transport = "UDP"
	port = 5060

	uri := contactURI
	if strings.HasPrefix(strings.ToLower(uri), "sip:") {
		uri = uri[4:]
	}

	var params string
	if semi := strings.IndexByte(uri, ';'); semi >= 0 {
		params = uri[semi+1:]
		uri = uri[:semi]
	}

	if params != "" {
		for p := range strings.SplitSeq(params, ";") {
			p = strings.TrimSpace(p)
			k, v, ok := strings.Cut(p, "=")
			if ok && strings.EqualFold(k, "transport") {
				transport = strings.ToUpper(v)
			}
		}
	}

	if at := strings.IndexByte(uri, '@'); at >= 0 {
		uri = uri[at+1:]
	}

	h, p, err := net.SplitHostPort(uri)
	if err != nil {
		host = uri
	} else {
		host = h
		if pn, err := strconv.Atoi(p); err == nil {
			port = pn
		}
	}

	return host, port, transport
}
