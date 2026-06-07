package register

import (
	"testing"
	"time"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
)

func TestIntegration_RegisterNoAuth(t *testing.T) {
	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServer(t, "127.0.0.1")
		defer ts.Stop()

		ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)

		req := buildRegisterRequest(ts.Domain, ts.UDPPort)

		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)

		assertRegisterOK(t, res, ts)

		client.Close()
		ua.Close()
	})

	t.Run("TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServer(t, "127.0.0.1")
		defer ts.Stop()

		ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)

		req := buildRegisterRequest(ts.Domain, ts.TCPPort)
		req.SetTransport("TCP")

		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)

		assertRegisterOK(t, res, ts)

		via := res.GetHeader("Via")
		require.NotNil(t, via, "Via header must be present")
		assert.Contains(t, via.Value(), "TCP", "Via transport must be TCP")

		client.Close()
		ua.Close()
	})
}

func TestIntegration_Unregister(t *testing.T) {
	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServer(t, "127.0.0.1")
		defer ts.Stop()

		ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)

		req := buildRegisterRequest(ts.Domain, ts.UDPPort)
		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		assertRegisterOK(t, res, ts)

		unreq := buildUnregisterRequest(ts.Domain, ts.UDPPort)
		unres, err := client.Do(t.Context(), unreq)
		require.NoError(t, err)
		assertUnregisterOK(t, unres, ts)

		client.Close()
		ua.Close()
	})

	t.Run("TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServer(t, "127.0.0.1")
		defer ts.Stop()

		ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)

		req := buildRegisterRequest(ts.Domain, ts.TCPPort)
		req.SetTransport("TCP")
		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		assertRegisterOK(t, res, ts)

		unreq := buildUnregisterRequest(ts.Domain, ts.TCPPort)
		unreq.SetTransport("TCP")
		unres, err := client.Do(t.Context(), unreq)
		require.NoError(t, err)
		assertUnregisterOK(t, unres, ts)

		client.Close()
		ua.Close()
	})
}

func TestIntegration_UnregisterAll(t *testing.T) {
	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServer(t, "127.0.0.1")
		defer ts.Stop()

		ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)

		req := buildRegisterRequest(ts.Domain, ts.UDPPort)
		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		assertRegisterOK(t, res, ts)

		unreq := buildUnregisterAllRequest(ts.Domain, ts.UDPPort)
		unres, err := client.Do(t.Context(), unreq)
		require.NoError(t, err)
		assertUnregisterOK(t, unres, ts)

		client.Close()
		ua.Close()
	})

	t.Run("TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServer(t, "127.0.0.1")
		defer ts.Stop()

		ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)

		req := buildRegisterRequest(ts.Domain, ts.TCPPort)
		req.SetTransport("TCP")
		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		assertRegisterOK(t, res, ts)

		unreq := buildUnregisterAllRequest(ts.Domain, ts.TCPPort)
		unreq.SetTransport("TCP")
		unres, err := client.Do(t.Context(), unreq)
		require.NoError(t, err)
		assertUnregisterOK(t, unres, ts)

		client.Close()
		ua.Close()
	})
}

func buildRegisterRequest(domain string, port int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
		User: "alice",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("Contact", "<sip:alice@192.168.1.100>;expires=3600"))
	req.AppendHeader(sipgo_sip.NewHeader("From", "<sip:alice@"+domain+">;tag=test123"))
	req.AppendHeader(sipgo_sip.NewHeader("To", "<sip:alice@"+domain+">"))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "integration-call-"+domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 REGISTER"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func buildUnregisterRequest(domain string, port int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
		User: "alice",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("Contact", "<sip:alice@192.168.1.100>;expires=0"))
	req.AppendHeader(sipgo_sip.NewHeader("From", "<sip:alice@"+domain+">;tag=test123"))
	req.AppendHeader(sipgo_sip.NewHeader("To", "<sip:alice@"+domain+">"))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "integration-unreg-"+domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "2 REGISTER"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func buildUnregisterAllRequest(domain string, port int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
		User: "alice",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("Contact", "*"))
	req.AppendHeader(sipgo_sip.NewHeader("From", "<sip:alice@"+domain+">;tag=test123"))
	req.AppendHeader(sipgo_sip.NewHeader("To", "<sip:alice@"+domain+">"))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "integration-unregall-"+domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "2 REGISTER"))
	req.AppendHeader(sipgo_sip.NewHeader("Expires", "0"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func assertRegisterOK(t *testing.T, res *sipgo_sip.Response, ts *integrationtest.TestServer) {
	t.Helper()

	assert.Equal(t, 200, res.StatusCode, "expected 200 OK (no auth challenge)")

	assert.Nil(t, res.GetHeader("WWW-Authenticate"),
		"should not have WWW-Authenticate header when no auth is required")

	contacts := res.GetHeaders("Contact")
	require.Len(t, contacts, 1, "expected exactly 1 Contact in response")
	contactStr := contacts[0].Value()
	assert.Contains(t, contactStr, "sip:alice@192.168.1.100")
	assert.Contains(t, contactStr, "expires=3600")

	expires := res.GetHeader("Expires")
	require.NotNil(t, expires, "Expires header must be present")
	assert.Equal(t, "3600", expires.Value())

	date := res.GetHeader("Date")
	require.NotNil(t, date, "Date header must be present")
	_, err := time.Parse(time.RFC1123, date.Value())
	require.NoError(t, err, "Date header must be valid RFC 1123: %s", date.Value())

	via := res.GetHeader("Via")
	require.NotNil(t, via, "Via header must be present")

	cseq := res.CSeq()
	require.NotNil(t, cseq)
	assert.Equal(t, uint32(1), cseq.SeqNo)
	assert.Equal(t, sipgo_sip.RequestMethod("REGISTER"), cseq.MethodName)

	callID := res.CallID()
	require.NotNil(t, callID)
	assert.Contains(t, string(*callID), "integration-call-")

	aor := "sip:alice@" + ts.Domain
	bindings := ts.Reg.GetBindings(aor)
	require.Len(t, bindings, 1, "expected 1 binding after registration")
	assert.Equal(t, "sip:alice@192.168.1.100", bindings[0].ContactURI)
}

func assertUnregisterOK(t *testing.T, res *sipgo_sip.Response, ts *integrationtest.TestServer) {
	t.Helper()

	assert.Equal(t, 200, res.StatusCode, "expected 200 OK for unregister")

	contacts := res.GetHeaders("Contact")
	assert.Empty(t, contacts, "expected no Contacts after unregister")

	aor := "sip:alice@" + ts.Domain
	bindings := ts.Reg.GetBindings(aor)
	assert.Empty(t, bindings, "expected 0 bindings after unregister")
}
