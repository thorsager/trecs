package integrationtest

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"testing"

	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"

	trecs_sip "github.com/thorsager/trecs/internal/sip"
)

// ExtractChallengeParam extracts a parameter value from a Digest challenge string.
func ExtractChallengeParam(challenge, key string) string {
	prefix := key + "="
	idx := strings.Index(challenge, prefix)
	if idx < 0 {
		return ""
	}
	val := strings.TrimSpace(challenge[idx+len(prefix):])
	if strings.HasPrefix(val, `"`) {
		val = val[1:]
		if end := strings.IndexByte(val, '"'); end >= 0 {
			return val[:end]
		}
		return ""
	}
	end := strings.IndexAny(val, ", \t\r\n")
	if end < 0 {
		return val
	}
	return val[:end]
}

// buildDigestAuthRequest creates a cloned request with a Digest auth header
// computed from the previous challenge response.
func buildDigestAuthRequest(t *testing.T, req *sipgo_sip.Request, challengeRes *sipgo_sip.Response, headerName, username, password string) *sipgo_sip.Request {
	t.Helper()

	var challenge string
	if headerName == "Proxy-Authorization" {
		h := challengeRes.GetHeader("Proxy-Authenticate")
		require.NotNil(t, h)
		challenge = h.Value()
	} else {
		h := challengeRes.GetHeader("WWW-Authenticate")
		require.NotNil(t, h)
		challenge = h.Value()
	}

	realm := ExtractChallengeParam(challenge, "realm")
	nonce := ExtractChallengeParam(challenge, "nonce")
	algorithm := ExtractChallengeParam(challenge, "algorithm")
	if algorithm == "" {
		algorithm = "MD5"
	}
	qop := ExtractChallengeParam(challenge, "qop")

	ha1 := trecs_sip.ComputeHA1(username, realm, password, algorithm)
	cnonce, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	nc := "00000001"
	method := req.Method
	uri := req.Recipient.String()
	digestResponse := trecs_sip.ComputeDigestResponse(ha1, nonce, nc, fmt.Sprintf("%016x", cnonce), qop, string(method), uri, algorithm)

	var b strings.Builder
	b.WriteString(`Digest username="`)
	b.WriteString(username)
	b.WriteString(`", realm="`)
	b.WriteString(realm)
	b.WriteString(`", nonce="`)
	b.WriteString(nonce)
	b.WriteString(`", uri="`)
	b.WriteString(uri)
	b.WriteString(`", response="`)
	b.WriteString(digestResponse)
	b.WriteString(`", algorithm=`)
	b.WriteString(algorithm)
	b.WriteString(`, cnonce="`)
	b.WriteString(fmt.Sprintf("%016x", cnonce))
	b.WriteString(`", nc=`)
	b.WriteString(nc)
	b.WriteString(`, qop=`)
	b.WriteString(qop)

	authReq := req.Clone()
	authReq.RemoveHeader("Via")
	authReq.AppendHeader(sipgo_sip.NewHeader(headerName, b.String()))
	return authReq
}

// BuildProxyAuthRequest creates a cloned request with a Proxy-Authorization header.
func BuildProxyAuthRequest(t *testing.T, req *sipgo_sip.Request, challengeRes *sipgo_sip.Response, username, password string) *sipgo_sip.Request {
	return buildDigestAuthRequest(t, req, challengeRes, "Proxy-Authorization", username, password)
}

// BuildAuthRequest creates a cloned request with an Authorization header.
func BuildAuthRequest(t *testing.T, req *sipgo_sip.Request, challengeRes *sipgo_sip.Response, username, password string) *sipgo_sip.Request {
	return buildDigestAuthRequest(t, req, challengeRes, "Authorization", username, password)
}
