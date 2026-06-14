package sip

import (
	"crypto/md5" //nolint:gosec // MD5 required for SIP Digest auth compatibility
	"encoding/hex"
	"testing"
)

func TestParseAuthorization_Valid(t *testing.T) {
	raw := `Digest username="alice", realm="example.com", nonce="abc123", uri="sip:example.com", response="def456", algorithm=MD5, cnonce="ghi789", qop=auth, nc=00000001`
	creds, err := ParseAuthorization(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Username != "alice" {
		t.Fatalf("Username: got %q, want alice", creds.Username)
	}
	if creds.Realm != "example.com" {
		t.Fatalf("Realm: got %q, want example.com", creds.Realm)
	}
	if creds.Nonce != "abc123" {
		t.Fatalf("Nonce: got %q, want abc123", creds.Nonce)
	}
	if creds.URI != "sip:example.com" {
		t.Fatalf("URI: got %q, want sip:example.com", creds.URI)
	}
	if creds.Response != "def456" {
		t.Fatalf("Response: got %q, want def456", creds.Response)
	}
	if creds.Algorithm != "MD5" {
		t.Fatalf("Algorithm: got %q, want MD5", creds.Algorithm)
	}
	if creds.CNonce != "ghi789" {
		t.Fatalf("CNonce: got %q, want ghi789", creds.CNonce)
	}
	if creds.QOP != "auth" {
		t.Fatalf("QOP: got %q, want auth", creds.QOP)
	}
	if creds.NC != 1 {
		t.Fatalf("NC: got %d, want 1", creds.NC)
	}
}

func TestParseAuthorization_NoDigestPrefix(t *testing.T) {
	_, err := ParseAuthorization("Basic xyz")
	if err == nil {
		t.Fatal("expected error for non-Digest auth")
	}
}

func TestParseAuthorization_MissingFields(t *testing.T) {
	_, err := ParseAuthorization(`Digest username="alice", realm="x"`)
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}
}

func TestParseAuthorization_UnterminatedQuote(t *testing.T) {
	_, err := ParseAuthorization(`Digest username="alice, realm="x"`)
	if err == nil {
		t.Fatal("expected error for unterminated quote")
	}
}

func TestParseAuthorization_AlgorithmDefault(t *testing.T) {
	raw := `Digest username="alice", realm="example.com", nonce="abc", uri="sip:x", response="def"`
	creds, err := ParseAuthorization(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Algorithm != "MD5" {
		t.Fatalf("default algorithm: got %q, want MD5", creds.Algorithm)
	}
}

func TestParseAuthorization_NoQuotes(t *testing.T) {
	raw := `Digest username=alice, realm=example.com, nonce=abc, uri=sip:x, response=def, algorithm=SHA-256`
	creds, err := ParseAuthorization(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Username != "alice" {
		t.Fatalf("Username: got %q, want alice", creds.Username)
	}
	if creds.Algorithm != "SHA-256" {
		t.Fatalf("Algorithm: got %q, want SHA-256", creds.Algorithm)
	}
}

func TestBuildWWWAuthenticate(t *testing.T) {
	challenge := BuildWWWAuthenticate("example.com", "abc123", "SHA-256", false)
	expected := `Digest realm="example.com", nonce="abc123", algorithm=SHA-256, qop="auth"`
	if challenge != expected {
		t.Fatalf("got %q, want %q", challenge, expected)
	}
}

func TestBuildWWWAuthenticate_Stale(t *testing.T) {
	challenge := BuildWWWAuthenticate("example.com", "abc123", "MD5", true)
	expected := `Digest realm="example.com", nonce="abc123", algorithm=MD5, qop="auth", stale=TRUE`
	if challenge != expected {
		t.Fatalf("got %q, want %q", challenge, expected)
	}
}

func TestComputeHA1_MD5(t *testing.T) {
	// RFC 2617 §3.5 test vector (note: RFC prints the HA1 incorrectly,
	// but the actual MD5 of "Mufasa:testrealm@host.com:Circle Of Life"
	// is 939e7578ed9e3c518a452acee763bce9)
	ha1 := ComputeHA1("Mufasa", "testrealm@host.com", "Circle Of Life", "MD5")
	expected := "939e7578ed9e3c518a452acee763bce9"
	if ha1 != expected {
		t.Fatalf("MD5 HA1: got %q, want %q", ha1, expected)
	}
}

func TestComputeDigestResponse_MD5_RFC2617(t *testing.T) {
	// RFC 2617 §3.5 test vector. The RFC's printed HA1 has a typo,
	// so we compute it from scratch. The final digest response
	// (6629fae49393a05397450978507c4ef1) is the correct reference.
	ha1 := ComputeHA1("Mufasa", "testrealm@host.com", "Circle Of Life", "MD5")
	nonce := "dcd98b7102dd2f0e8b11d0f600bfb0c093"
	nc := "00000001"
	cnonce := "0a4f113b"
	qop := "auth"
	method := "GET"
	uri := "/dir/index.html"
	algorithm := "MD5"

	resp := ComputeDigestResponse(ha1, nonce, nc, cnonce, qop, method, uri, algorithm)
	expected := "6629fae49393a05397450978507c4ef1"
	if resp != expected {
		t.Fatalf("Digest response: got %q, want %q", resp, expected)
	}
}

func TestComputeDigestResponse_MD5_SIP(t *testing.T) {
	ha1 := ComputeHA1("alice", "example.com", "secret", "MD5")
	nonce := "abc123"
	cnonce := "xyz789"
	qop := "auth"
	method := "REGISTER"
	uri := "sip:example.com"
	algorithm := "MD5"

	resp := ComputeDigestResponse(ha1, nonce, "00000001", cnonce, qop, method, uri, algorithm)
	if resp == "" {
		t.Fatal("empty digest response")
	}

	// Verify it's 32 hex chars (MD5)
	if len(resp) != 32 {
		t.Fatalf("MD5 response length: got %d, want 32", len(resp))
	}
}

func TestComputeDigestResponse_SHA256(t *testing.T) {
	ha1 := ComputeHA1("alice", "example.com", "secret", "SHA-256")
	nonce := "abc123"
	cnonce := "xyz789"
	qop := "auth"
	method := "REGISTER"
	uri := "sip:example.com"
	algorithm := "SHA-256"

	resp := ComputeDigestResponse(ha1, nonce, "00000001", cnonce, qop, method, uri, algorithm)
	if resp == "" {
		t.Fatal("empty digest response")
	}

	// SHA-256 produces 64 hex chars
	if len(resp) != 64 {
		t.Fatalf("SHA-256 response length: got %d, want 64", len(resp))
	}
}

func TestComputeDigestResponse_SHA512_256(t *testing.T) {
	ha1 := ComputeHA1("alice", "example.com", "secret", "SHA-512-256")
	nonce := "abc123"
	cnonce := "xyz789"
	qop := "auth"
	method := "REGISTER"
	uri := "sip:example.com"
	algorithm := "SHA-512-256"

	resp := ComputeDigestResponse(ha1, nonce, "00000001", cnonce, qop, method, uri, algorithm)
	if resp == "" {
		t.Fatal("empty digest response")
	}

	// SHA-512-256 produces 64 hex chars
	if len(resp) != 64 {
		t.Fatalf("SHA-512-256 response length: got %d, want 64", len(resp))
	}
}

func TestVerifyDigest_Valid(t *testing.T) {
	// Simulate the full REGISTER auth flow
	ha1 := ComputeHA1("alice", "example.com", "secret", "SHA-256")
	nonce := "test-nonce"
	cnonce := "client-nonce"

	rawResponse := ComputeDigestResponse(ha1, nonce, "00000001", cnonce, "auth", "REGISTER", "sip:example.com", "SHA-256")

	creds := &DigestCredentials{
		Username:  "alice",
		Realm:     "example.com",
		Nonce:     nonce,
		URI:       "sip:example.com",
		Response:  rawResponse,
		Algorithm: "SHA-256",
		CNonce:    cnonce,
		QOP:       "auth",
		NC:        1,
	}

	if !VerifyDigest(creds, ha1, "REGISTER") {
		t.Fatal("VerifyDigest returned false for valid credentials")
	}
}

func TestVerifyDigest_WrongPassword(t *testing.T) {
	ha1 := ComputeHA1("alice", "example.com", "wrong", "SHA-256")
	creds := &DigestCredentials{
		Username:  "alice",
		Realm:     "example.com",
		Nonce:     "n",
		URI:       "sip:example.com",
		Response:  "bad",
		Algorithm: "SHA-256",
		CNonce:    "c",
		QOP:       "auth",
		NC:        1,
	}

	if VerifyDigest(creds, ha1, "REGISTER") {
		t.Fatal("VerifyDigest returned true for wrong password")
	}
}

func TestHA1_AlgorithmsProduceDifferentHashes(t *testing.T) {
	md5Hash := ComputeHA1("alice", "example.com", "secret", "MD5")
	sha256Hash := ComputeHA1("alice", "example.com", "secret", "SHA-256")
	sha512256Hash := ComputeHA1("alice", "example.com", "secret", "SHA-512-256")

	if md5Hash == sha256Hash {
		t.Fatal("MD5 and SHA-256 HA1 should differ")
	}
	if sha256Hash == sha512256Hash {
		t.Fatal("SHA-256 and SHA-512-256 HA1 should differ")
	}
	if len(md5Hash) != 32 {
		t.Fatalf("MD5 HA1 length: got %d, want 32", len(md5Hash))
	}
	if len(sha256Hash) != 64 {
		t.Fatalf("SHA-256 HA1 length: got %d, want 64", len(sha256Hash))
	}
	if len(sha512256Hash) != 64 {
		t.Fatalf("SHA-512-256 HA1 length: got %d, want 64", len(sha512256Hash))
	}
}

func TestComputeHA1_UnknownAlgorithmDefaultsToMD5(t *testing.T) {
	ha1 := ComputeHA1("user", "realm", "pass", "UNKNOWN")
	expected := ComputeHA1("user", "realm", "pass", "MD5")
	if ha1 != expected {
		t.Fatal("unknown algorithm should fall back to MD5")
	}
}

func TestParseAuthorization_NC(t *testing.T) {
	raw := `Digest username="a", realm="r", nonce="n", uri="u", response="x", nc=00000005, qop=auth`
	creds, err := ParseAuthorization(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.NC != 5 {
		t.Fatalf("NC: got %d, want 5", creds.NC)
	}
}

func TestParseAuthorization_InvalidNC(t *testing.T) {
	raw := `Digest username="a", realm="r", nonce="n", uri="u", response="x", nc=xyz, qop=auth`
	_, err := ParseAuthorization(raw)
	if err == nil {
		t.Fatal("expected error for invalid NC")
	}
}

func TestParseAuthorization_SchemeCaseInsensitive(t *testing.T) {
	raw := `DIGEST username="alice", realm="example.com", nonce="abc", uri="sip:x", response="def"`
	creds, err := ParseAuthorization(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Username != "alice" {
		t.Fatalf("Username: got %q, want alice", creds.Username)
	}
}

func TestParseAuthorization_HTABSeparator(t *testing.T) {
	raw := "Digest\tusername=\"alice\", realm=\"example.com\", nonce=\"abc\", uri=\"sip:x\", response=\"def\""
	creds, err := ParseAuthorization(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Username != "alice" {
		t.Fatalf("Username: got %q, want alice", creds.Username)
	}
}

func TestParseAuthorization_SchemeOnly(t *testing.T) {
	_, err := ParseAuthorization("Digest")
	if err == nil {
		t.Fatal("expected error for bare scheme with no params")
	}
}

func TestParseAuthorization_CRLFFolding(t *testing.T) {
	raw := "Digest \r\n realm=\"example.com\", nonce=\"abc\", uri=\"sip:x\", response=\"def\", username=\"alice\""
	creds, err := ParseAuthorization(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Username != "alice" {
		t.Fatalf("Username: got %q, want alice", creds.Username)
	}
}

func TestParseProxyAuthorization(t *testing.T) {
	raw := `Digest username="bob", realm="example.com", nonce="xyz", uri="sip:example.com", response="abc", algorithm=SHA-256, cnonce="c1", qop=auth, nc=00000001`
	creds, err := ParseProxyAuthorization(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Username != "bob" {
		t.Fatalf("Username: got %q, want bob", creds.Username)
	}
	if creds.Realm != "example.com" {
		t.Fatalf("Realm: got %q, want example.com", creds.Realm)
	}
	if creds.Algorithm != "SHA-256" {
		t.Fatalf("Algorithm: got %q, want SHA-256", creds.Algorithm)
	}
}

func TestBuildProxyAuthenticate(t *testing.T) {
	challenge := BuildProxyAuthenticate("proxy.realm", "nonce123", "SHA-256", false)
	expected := `Digest realm="proxy.realm", nonce="nonce123", algorithm=SHA-256, qop="auth"`
	if challenge != expected {
		t.Fatalf("got %q, want %q", challenge, expected)
	}
}

func TestBuildProxyAuthenticate_Stale(t *testing.T) {
	challenge := BuildProxyAuthenticate("proxy.realm", "nonce123", "MD5", true)
	expected := `Digest realm="proxy.realm", nonce="nonce123", algorithm=MD5, qop="auth", stale=TRUE`
	if challenge != expected {
		t.Fatalf("got %q, want %q", challenge, expected)
	}
}

func TestParseProxyAuthorization_NoDigestPrefix(t *testing.T) {
	_, err := ParseProxyAuthorization("Basic xyz")
	if err == nil {
		t.Fatal("expected error for non-Digest auth")
	}
}

func TestVerifyDigest_WithInviteMethod(t *testing.T) {
	ha1 := ComputeHA1("alice", "example.com", "secret", "SHA-256")
	nonce := "test-nonce"
	cnonce := "client-nonce"
	rawResponse := ComputeDigestResponse(ha1, nonce, "00000001", cnonce, "auth", "INVITE", "sip:example.com", "SHA-256")

	creds := &DigestCredentials{
		Username:  "alice",
		Realm:     "example.com",
		Nonce:     nonce,
		URI:       "sip:example.com",
		Response:  rawResponse,
		Algorithm: "SHA-256",
		CNonce:    cnonce,
		QOP:       "auth",
		NC:        1,
	}

	if !VerifyDigest(creds, ha1, "INVITE") {
		t.Fatal("VerifyDigest returned false for valid INVITE credentials")
	}
	// Must fail with wrong method
	if VerifyDigest(creds, ha1, "BYE") {
		t.Fatal("VerifyDigest should fail when method does not match")
	}
}

func TestVerifyDigest_WithByeMethod(t *testing.T) {
	ha1 := ComputeHA1("alice", "example.com", "secret", "SHA-256")
	nonce := "test-nonce"
	cnonce := "client-nonce"
	rawResponse := ComputeDigestResponse(ha1, nonce, "00000001", cnonce, "auth", "BYE", "sip:example.com", "SHA-256")

	creds := &DigestCredentials{
		Username:  "alice",
		Realm:     "example.com",
		Nonce:     nonce,
		URI:       "sip:example.com",
		Response:  rawResponse,
		Algorithm: "SHA-256",
		CNonce:    cnonce,
		QOP:       "auth",
		NC:        1,
	}

	if !VerifyDigest(creds, ha1, "BYE") {
		t.Fatal("VerifyDigest returned false for valid BYE credentials")
	}
}

func TestVerifyDigest_NoQop(t *testing.T) {
	// Without qop, the formula is H(HA1:nonce:HA2) — no nc/cnonce/qop.
	ha1 := ComputeHA1("alice", "example.com", "secret", "MD5")
	nonce := "simple-nonce"
	uri := "sip:example.com"
	method := "INVITE"

	// Compute the expected response using the no-qop formula directly.
	ha2 := hexHash("MD5", []byte(method+":"+uri))
	respData := ha1 + ":" + nonce + ":" + ha2
	h := md5.Sum([]byte(respData)) //nolint:gosec // MD5 required for SIP Digest auth
	expected := hex.EncodeToString(h[:])

	creds := &DigestCredentials{
		Username:  "alice",
		Realm:     "example.com",
		Nonce:     nonce,
		URI:       uri,
		Response:  expected,
		Algorithm: "MD5",
	}

	if !VerifyDigest(creds, ha1, method) {
		t.Fatal("VerifyDigest returned false for valid no-qop credentials")
	}

	// Wrong response should fail.
	credsBad := *creds
	credsBad.Response = "00000000000000000000000000000000"
	if VerifyDigest(&credsBad, ha1, method) {
		t.Fatal("VerifyDigest should fail for wrong response")
	}
}

func TestVerifyDigest_QopAuthInt(t *testing.T) {
	ha1 := ComputeHA1("alice", "example.com", "secret", "SHA-256")
	nonce := "test-nonce"
	cnonce := "client-nonce"

	// qop=auth-int uses the same formula path as auth.
	rawResponse := ComputeDigestResponse(ha1, nonce, "00000001", cnonce, "auth-int", "INVITE", "sip:example.com", "SHA-256")

	creds := &DigestCredentials{
		Username:  "alice",
		Realm:     "example.com",
		Nonce:     nonce,
		URI:       "sip:example.com",
		Response:  rawResponse,
		Algorithm: "SHA-256",
		CNonce:    cnonce,
		QOP:       "auth-int",
		NC:        1,
	}

	if !VerifyDigest(creds, ha1, "INVITE") {
		t.Fatal("VerifyDigest returned false for valid auth-int credentials")
	}

	// Must fail with wrong method.
	if VerifyDigest(creds, ha1, "BYE") {
		t.Fatal("VerifyDigest should fail when method does not match (auth-int)")
	}
}
