package sip

import (
	"crypto/md5" //nolint:gosec // MD5 required for SIP Digest auth compatibility
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

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

// DefaultMaxFailedAuthAttempts is the default number of consecutive auth failures
// allowed before a 403 Forbidden lockout.
const DefaultMaxFailedAuthAttempts = 3

// ClampMaxFailedAuthAttempts clamps n to the valid range [1, 10].
func ClampMaxFailedAuthAttempts(n int) int {
	if n < 1 {
		return 1
	}
	if n > 10 {
		return 10
	}
	return n
}

// AuthAttemptTTL is the lifetime of per-session failed-auth counters.
// Matches the NonceManager TTL (300s) so the attempt budget expires
// together with the nonce, preventing nonce-reuse across counter resets.
const AuthAttemptTTL = 300 * time.Second

// authAttemptEntry tracks the number of consecutive failures for a single
// session key along with its expiry time.
type authAttemptEntry struct {
	count   int
	expires time.Time
}

// AuthAttemptTracker counts failed authentication/authorization attempts per
// session (transport:remote|Call-ID). It is safe for concurrent use.
type AuthAttemptTracker struct {
	mu      sync.Mutex
	entries map[string]*authAttemptEntry
	ttl     time.Duration
}

// NewAuthAttemptTracker creates a tracker that expires entries after ttl.
func NewAuthAttemptTracker(ttl time.Duration) *AuthAttemptTracker {
	return &AuthAttemptTracker{
		entries: make(map[string]*authAttemptEntry),
		ttl:     ttl,
	}
}

// Record increments the failure count for key, refreshes its expiry, and
// returns the new count.
func (at *AuthAttemptTracker) Record(key string) int {
	at.mu.Lock()
	defer at.mu.Unlock()
	e := at.entries[key]
	if e == nil {
		e = &authAttemptEntry{}
		at.entries[key] = e
	}
	e.count++
	e.expires = time.Now().Add(at.ttl)
	return e.count
}

// Count returns the current failure count for key, or 0 if expired/missing.
func (at *AuthAttemptTracker) Count(key string) int {
	at.mu.Lock()
	defer at.mu.Unlock()
	e := at.entries[key]
	if e == nil || time.Now().After(e.expires) {
		return 0
	}
	return e.count
}

// Reset clears the failure count for key.
func (at *AuthAttemptTracker) Reset(key string) {
	at.mu.Lock()
	defer at.mu.Unlock()
	delete(at.entries, key)
}

// Sweep removes expired entries. It should be called periodically.
func (at *AuthAttemptTracker) Sweep() {
	at.mu.Lock()
	defer at.mu.Unlock()
	now := time.Now()
	for k, e := range at.entries {
		if now.After(e.expires) {
			delete(at.entries, k)
		}
	}
}

// AuthAttemptKey builds a session key from the transaction source and Call-ID.
func AuthAttemptKey(tx Transaction, callID string) string {
	target := tx.Target()
	var remote string
	switch {
	case target.Addr != nil:
		remote = target.Addr.String()
	case target.Conn != nil:
		remote = target.Conn.RemoteAddr().String()
	default:
		remote = "unknown"
	}
	transport := "?"
	if tx.Transport() != nil {
		transport = TransportName(tx.Transport())
	}
	return transport + ":" + remote + "|" + callID
}

// sendAuthFailureResponse records a failed attempt and sends either a fresh
// 401/407 challenge or a 403 lockout response.
func sendAuthFailureResponse(
	tracker *AuthAttemptTracker,
	maxAttempts int,
	key string,
	req *proto.SIPMessage,
	tx Transaction,
	passwd PasswordStore,
	nonces *NonceManager,
	respChallengeKey string,
	challengeStatus int,
	statusText string,
	stale bool,
	log *slog.Logger,
) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	count := tracker.Record(key)
	if count < maxAttempts {
		log.Debug("auth failure, challenging", "count", count, "maxAttempts", maxAttempts)
		nonce := nonces.NewNonce()
		challenge := BuildDigestChallenge(passwd.Realm(), nonce, passwd.Algorithm(), stale)
		res := proto.NewResponse(req, challengeStatus, statusText)
		res.Headers.Add(respChallengeKey, challenge)
		tx.Respond(res)
		return
	}
	log.Warn("auth failure threshold reached, locking out", "count", count, "maxAttempts", maxAttempts)
	tracker.Reset(key)
	tx.Respond(proto.NewResponse(req, 403, "Forbidden"))
}

// ParseDigest parses the value of a Digest-type header (Authorization,
// Proxy-Authorization, etc.) and returns the parsed credentials.
func ParseDigest(raw string) (*DigestCredentials, error) {
	raw = strings.TrimSpace(raw)
	// Scheme is case-insensitive per RFC 7235 §4.1.
	if len(raw) < 6 || !strings.EqualFold(raw[:6], "Digest") {
		return nil, errors.New("sip auth: not a Digest Authorization header")
	}
	// Skip scheme; must be followed by LWS (SP, HTAB, or CRLF folding) per RFC 3261 §7.3.1.
	raw = strings.TrimSpace(raw[6:])
	if len(raw) == 0 {
		return nil, errors.New("sip auth: not a Digest Authorization header")
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
				return nil, errors.New("sip auth: unterminated quoted string")
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
		return nil, errors.New("sip auth: missing required field")
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
		h := md5.Sum([]byte(data)) //nolint:gosec // MD5 required for SIP Digest auth
		return hex.EncodeToString(h[:])
	case "SHA-256":
		h := sha256.Sum256([]byte(data))
		return hex.EncodeToString(h[:])
	case "SHA-512-256":
		h := sha512.Sum512_256([]byte(data))
		return hex.EncodeToString(h[:])
	default:
		h := md5.Sum([]byte(data)) //nolint:gosec // MD5 required for SIP Digest auth
		return hex.EncodeToString(h[:])
	}
}

func h(algorithm string, data []byte) []byte {
	switch algorithm {
	case "MD5":
		h := md5.Sum(data) //nolint:gosec // MD5 required for SIP Digest auth
		return h[:]
	case "SHA-256":
		h := sha256.Sum256(data)
		return h[:]
	case "SHA-512-256":
		h := sha512.Sum512_256(data)
		return h[:]
	default:
		h := md5.Sum(data) //nolint:gosec // MD5 required for SIP Digest auth
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

// VerifyDigestRequest performs the full digest authentication flow for a SIP request.
//
// Failures (malformed header, realm/URI mismatch, unknown username, wrong
// password, invalid nonce) are treated as authentication failures and are
// subject to the configured retry limit: the first maxAttempts-1 failures
// receive a fresh 401/407 challenge; the maxAttempts-th failure receives 403.
// Only a missing auth header bypasses counting (it receives the initial
// challenge). On success the per-session failure counter is reset and the
// parsed credentials are returned.
func VerifyDigestRequest(req *proto.SIPMessage, tx Transaction,
	passwd PasswordStore, nonces *NonceManager, attempts *AuthAttemptTracker,
	authHeaderKey, respChallengeKey string,
	challengeStatus int, statusText string,
	method string, log *slog.Logger,
	maxAttempts int,
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

	callID := req.Headers.GetFirst("Call-ID")
	key := AuthAttemptKey(tx, callID)

	creds, err := ParseDigest(authHeader)
	if err != nil || creds.Username == "" {
		log.Warn("bad auth header", "error", err)
		sendAuthFailureResponse(attempts, maxAttempts, key, req, tx,
			passwd, nonces, respChallengeKey, challengeStatus, statusText,
			false, log)
		return nil
	}

	if creds.URI != req.RequestURI() {
		log.Warn("uri mismatch", "expected", req.RequestURI(), "got", creds.URI)
		sendAuthFailureResponse(attempts, maxAttempts, key, req, tx,
			passwd, nonces, respChallengeKey, challengeStatus, statusText,
			false, log)
		return nil
	}

	if creds.Realm != passwd.Realm() {
		log.Warn("realm mismatch", "expected", passwd.Realm(), "got", creds.Realm)
		sendAuthFailureResponse(attempts, maxAttempts, key, req, tx,
			passwd, nonces, respChallengeKey, challengeStatus, statusText,
			false, log)
		return nil
	}

	known, valid := nonces.Verify(creds.Nonce, creds.NC)
	if !valid {
		log.Warn("nonce rejected", "known", known)
		sendAuthFailureResponse(attempts, maxAttempts, key, req, tx,
			passwd, nonces, respChallengeKey, challengeStatus, statusText,
			known, log)
		return nil
	}

	ha1, userExists := passwd.HA1(creds.Username)
	if !userExists {
		ha1 = "0000000000000000000000000000000000000000000000000000000000000000"
	}
	if !VerifyDigest(creds, ha1, method) || !userExists {
		log.Warn("digest verification failed", "username", creds.Username, "method", method)
		sendAuthFailureResponse(attempts, maxAttempts, key, req, tx,
			passwd, nonces, respChallengeKey, challengeStatus, statusText,
			false, log)
		return nil
	}

	return creds
}
