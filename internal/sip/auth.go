package sip

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/thorsager/trecs/proto"
)

type DigestCredentials struct {
	Username  string
	Realm     string
	Nonce     string
	URI       string
	Response  string
	Algorithm string
	CNonce    string
	QOP       string
	NC        uint64
	Opaque    string
}

// ParseDigest parses the value of a Digest-type header (Authorization,
// Proxy-Authorization, etc.) and returns the parsed credentials.
func ParseDigest(raw string) (*DigestCredentials, error) {
	raw = strings.TrimSpace(raw)
	// Scheme is case-insensitive per RFC 7235 §4.1.
	if len(raw) < 6 || !strings.EqualFold(raw[:6], "Digest") {
		return nil, fmt.Errorf("sip auth: not a Digest Authorization header")
	}
	// Skip scheme; must be followed by LWS (SP, HTAB, or CRLF folding) per RFC 3261 §7.3.1.
	raw = strings.TrimSpace(raw[6:])
	if len(raw) == 0 {
		return nil, fmt.Errorf("sip auth: not a Digest Authorization header")
	}

	creds := &DigestCredentials{Algorithm: "MD5"}

	for raw != "" {
		raw = strings.TrimSpace(raw)
		key, rest, found := strings.Cut(raw, "=")
		if !found {
			break
		}
		key = strings.TrimSpace(key)
		rest = strings.TrimSpace(rest)

		var value string
		if strings.HasPrefix(rest, "\"") {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("sip auth: unterminated quoted string")
			}
			value = rest[1 : 1+end]
			rest = rest[1+end+1:]
			if idx := strings.IndexByte(rest, ','); idx >= 0 {
				rest = rest[idx+1:]
			} else {
				rest = ""
			}
		} else {
			if idx := strings.IndexByte(rest, ','); idx >= 0 {
				value = strings.TrimSpace(rest[:idx])
				rest = rest[idx+1:]
			} else {
				value = strings.TrimSpace(rest)
				rest = ""
			}
		}

		switch strings.ToLower(key) {
		case "username":
			creds.Username = value
		case "realm":
			creds.Realm = value
		case "nonce":
			creds.Nonce = value
		case "uri":
			creds.URI = value
		case "response":
			creds.Response = value
		case "algorithm":
			creds.Algorithm = strings.ToUpper(value)
		case "cnonce":
			creds.CNonce = value
		case "qop":
			creds.QOP = value
		case "nc":
			n, err := strconv.ParseUint(value, 16, 64)
			if err != nil {
				return nil, fmt.Errorf("sip auth: invalid nc: %s", value)
			}
			creds.NC = n
		case "opaque":
			creds.Opaque = value
		}
		raw = rest
	}

	if creds.Username == "" || creds.Realm == "" || creds.Nonce == "" ||
		creds.URI == "" || creds.Response == "" {
		return nil, fmt.Errorf("sip auth: missing required field")
	}

	return creds, nil
}

// ParseAuthorization parses an Authorization header value (see ParseDigest).
func ParseAuthorization(raw string) (*DigestCredentials, error) {
	return ParseDigest(raw)
}

// ParseProxyAuthorization parses a Proxy-Authorization header value (see ParseDigest).
func ParseProxyAuthorization(raw string) (*DigestCredentials, error) {
	return ParseDigest(raw)
}

func BuildDigestChallenge(realm, nonce, algorithm string, stale bool) string {
	var b strings.Builder
	b.WriteString("Digest realm=\"")
	b.WriteString(realm)
	b.WriteString("\", nonce=\"")
	b.WriteString(nonce)
	b.WriteString("\", algorithm=")
	b.WriteString(algorithm)
	b.WriteString(", qop=\"auth\"")
	if stale {
		b.WriteString(", stale=TRUE")
	}
	return b.String()
}

func BuildWWWAuthenticate(realm, nonce, algorithm string, stale bool) string {
	return BuildDigestChallenge(realm, nonce, algorithm, stale)
}

func BuildProxyAuthenticate(realm, nonce, algorithm string, stale bool) string {
	return BuildDigestChallenge(realm, nonce, algorithm, stale)
}

func ComputeHA1(username, realm, password, algorithm string) string {
	data := username + ":" + realm + ":" + password
	switch algorithm {
	case "MD5":
		h := md5.Sum([]byte(data))
		return hex.EncodeToString(h[:])
	case "SHA-256":
		h := sha256.Sum256([]byte(data))
		return hex.EncodeToString(h[:])
	case "SHA-512-256":
		h := sha512.Sum512_256([]byte(data))
		return hex.EncodeToString(h[:])
	default:
		h := md5.Sum([]byte(data))
		return hex.EncodeToString(h[:])
	}
}

func h(algorithm string, data []byte) []byte {
	switch algorithm {
	case "MD5":
		h := md5.Sum(data)
		return h[:]
	case "SHA-256":
		h := sha256.Sum256(data)
		return h[:]
	case "SHA-512-256":
		h := sha512.Sum512_256(data)
		return h[:]
	default:
		h := md5.Sum(data)
		return h[:]
	}
}

func hexHash(algorithm string, data []byte) string {
	sum := h(algorithm, data)
	return hex.EncodeToString(sum)
}

func ComputeDigestResponse(ha1, nonce, nc, cnonce, qop, method, uri, algorithm string) string {
	a2 := method + ":" + uri
	ha2 := hexHash(algorithm, []byte(a2))

	var respData string
	if qop == "auth" || qop == "auth-int" {
		respData = ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2
	} else {
		respData = ha1 + ":" + nonce + ":" + ha2
	}
	return hexHash(algorithm, []byte(respData))
}

func VerifyDigest(creds *DigestCredentials, ha1 string, method string) bool {
	nc := ""
	if creds.QOP != "" {
		nc = fmt.Sprintf("%08x", creds.NC)
	}
	expected := ComputeDigestResponse(ha1, creds.Nonce, nc, creds.CNonce, creds.QOP, method, creds.URI, creds.Algorithm)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(creds.Response)) == 1
}

// VerifyDigestRequest performs the full digest authentication flow for a SIP request:
//  1. Check that the auth header exists, otherwise send a challenge.
//  2. Parse the header value.
//  3. Look up the user's HA1 and verify the digest response.
//  4. Verify the nonce (replay protection and expiry).
//
// On success it returns the parsed credentials. On failure it sends the
// appropriate error response (400, 403, or challengeStatus) and returns nil.
//
// log is used for structured logging; pass a *slog.Logger from the caller's context.
func VerifyDigestRequest(req *proto.SIPMessage, tx Transaction,
	passwd PasswordStore, nonces *NonceManager,
	authHeaderKey, respChallengeKey string,
	challengeStatus int, statusText string,
	method string, log *slog.Logger,
) *DigestCredentials {
	authHeader := req.Headers.GetFirst(authHeaderKey)
	if authHeader == "" {
		log.Debug("no credentials, challenging")
		nonce := nonces.NewNonce()
		challenge := BuildDigestChallenge(passwd.Realm(), nonce, passwd.Algorithm(), false)
		res := proto.NewResponse(req, challengeStatus, statusText)
		res.Headers.Add(respChallengeKey, challenge)
		tx.Respond(res)
		return nil
	}

	creds, err := ParseDigest(authHeader)
	if err != nil || creds.Username == "" {
		log.Warn("bad auth header", "error", err)
		tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
		return nil
	}

	if creds.URI != req.RequestURI() {
		log.Warn("uri mismatch", "expected", req.RequestURI(), "got", creds.URI)
		tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
		return nil
	}

	ha1, userExists := passwd.HA1(creds.Username)
	if !userExists || !VerifyDigest(creds, ha1, method) {
		log.Warn("digest verification failed", "username", creds.Username, "method", method)
		tx.Respond(proto.NewResponse(req, 403, "Forbidden"))
		return nil
	}

	known, valid := nonces.Verify(creds.Nonce, creds.NC)
	if !valid {
		log.Warn("nonce rejected", "known", known)
		stale := known
		nonce := nonces.NewNonce()
		challenge := BuildDigestChallenge(passwd.Realm(), nonce, passwd.Algorithm(), stale)
		res := proto.NewResponse(req, challengeStatus, statusText)
		res.Headers.Add(respChallengeKey, challenge)
		tx.Respond(res)
		return nil
	}

	return creds
}
