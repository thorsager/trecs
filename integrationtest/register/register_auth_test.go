package register

import (
	"testing"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
	"github.com/thorsager/trecs/proto"
)

func TestIntegration_RegisterRejectedWithoutAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		_, client := newTestClient(t, ts)
		req := buildRegisterRequest(ts.Domain, ts.UDPPort)

		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusUnauthorized, res.StatusCode, "Should get 401 when no auth provided")
		assertWWWChallenge(t, res, "127.0.0.1", "SHA-256")
	})

	t.Run("TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		_, client := newTestClient(t, ts)
		req := buildRegisterRequest(ts.Domain, ts.TCPPort)
		req.SetTransport("TCP")

		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusUnauthorized, res.StatusCode, "Should get 401 when no auth provided")
		assertWWWChallenge(t, res, "127.0.0.1", "SHA-256")
	})
}

func TestIntegration_RegisterAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		_, client := newTestClient(t, ts)
		req := buildRegisterRequest(ts.Domain, ts.UDPPort)

		res := doAuthRequest(t, client, req, "alice", "secret")
		assertRegisterOK(t, res, ts)
	})

	t.Run("TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		_, client := newTestClient(t, ts)
		req := buildRegisterRequest(ts.Domain, ts.TCPPort)
		req.SetTransport("TCP")

		res := doAuthRequest(t, client, req, "alice", "secret")
		assertRegisterOK(t, res, ts)
		require.NotNil(t, res.GetHeader("Via"))
		require.Contains(t, res.GetHeader("Via").Value(), "TCP", "Via transport must be TCP")
	})
}

func TestIntegration_UnregisterWithAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		_, client := newTestClient(t, ts)

		req := buildRegisterRequest(ts.Domain, ts.UDPPort)
		res := doAuthRequest(t, client, req, "alice", "secret")
		assertRegisterOK(t, res, ts)

		unreq := buildUnregisterRequest(ts.Domain, ts.UDPPort)
		unres := doAuthRequest(t, client, unreq, "alice", "secret")
		assertUnregisterOK(t, unres, ts)
	})

	t.Run("TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		_, client := newTestClient(t, ts)

		req := buildRegisterRequest(ts.Domain, ts.TCPPort)
		req.SetTransport("TCP")
		res := doAuthRequest(t, client, req, "alice", "secret")
		assertRegisterOK(t, res, ts)

		unreq := buildUnregisterRequest(ts.Domain, ts.TCPPort)
		unreq.SetTransport("TCP")
		unres := doAuthRequest(t, client, unreq, "alice", "secret")
		assertUnregisterOK(t, unres, ts)
	})
}

func TestIntegration_UnregisterAllWithAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		_, client := newTestClient(t, ts)

		req := buildRegisterRequest(ts.Domain, ts.UDPPort)
		res := doAuthRequest(t, client, req, "alice", "secret")
		assertRegisterOK(t, res, ts)

		unreq := buildUnregisterAllRequest(ts.Domain, ts.UDPPort)
		unres := doAuthRequest(t, client, unreq, "alice", "secret")
		assertUnregisterOK(t, unres, ts)
	})

	t.Run("TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		_, client := newTestClient(t, ts)

		req := buildRegisterRequest(ts.Domain, ts.TCPPort)
		req.SetTransport("TCP")
		res := doAuthRequest(t, client, req, "alice", "secret")
		assertRegisterOK(t, res, ts)

		unreq := buildUnregisterAllRequest(ts.Domain, ts.TCPPort)
		unreq.SetTransport("TCP")
		unres := doAuthRequest(t, client, unreq, "alice", "secret")
		assertUnregisterOK(t, unres, ts)
	})
}

// newTestClient creates a sipgo UA and client that auto-cleanup via t.Cleanup.
func newTestClient(t *testing.T, ts *integrationtest.TestServer) (*sipgo.UserAgent, *sipgo.Client) {
	t.Helper()
	ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)
	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close(); ua.Close() })
	return ua, client
}

// doAuthRequest sends a SIP request, expects 401, then completes with Digest auth.
func doAuthRequest(t *testing.T, client *sipgo.Client, req *sipgo_sip.Request, username, password string) *sipgo_sip.Response {
	t.Helper()
	res, err := client.Do(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusUnauthorized, res.StatusCode, "Should get 401 before auth")
	res, err = client.DoDigestAuth(t.Context(), req, res, sipgo.DigestAuth{
		Username: username,
		Password: password,
	})
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Auth request should succeed")
	resCSeq := res.CSeq()
	require.NotNil(t, resCSeq, "200 OK must have CSeq")
	require.Equal(t, req.Method, resCSeq.MethodName, "CSeq method must match")
	return res
}

// assertWWWChallenge asserts the WWW-Authenticate header has the expected format.
func assertWWWChallenge(t *testing.T, res *sipgo_sip.Response, realm, algorithm string) {
	t.Helper()
	wwwAuth := res.GetHeader("WWW-Authenticate")
	require.NotNil(t, wwwAuth, "Should have WWW-Authenticate header")
	val := wwwAuth.Value()
	require.Contains(t, val, "Digest")
	require.Contains(t, val, `realm="`+realm+`"`)
	require.Contains(t, val, "algorithm="+algorithm)
	require.Contains(t, val, "qop=\"auth\"")
	require.Contains(t, val, "nonce=", "WWW-Authenticate must include nonce per RFC 3261 §22.1")
	require.NotContains(t, val, "stale=TRUE", "Initial challenge should not have stale=TRUE")
}
