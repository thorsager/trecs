package integrationtest

import (
	"testing"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_Unregister(t *testing.T) {
	t.Run("UDP", func(t *testing.T) {
		ts := StartTestServer(t, "127.0.0.1")
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
		ts := StartTestServer(t, "127.0.0.1")
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
		ts := StartTestServer(t, "127.0.0.1")
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
		ts := StartTestServer(t, "127.0.0.1")
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

func assertUnregisterOK(t *testing.T, res *sipgo_sip.Response, ts *TestServer) {
	t.Helper()

	assert.Equal(t, 200, res.StatusCode, "expected 200 OK for unregister")

	contacts := res.GetHeaders("Contact")
	assert.Empty(t, contacts, "expected no Contacts after unregister")

	aor := "sip:alice@" + ts.Domain
	bindings := ts.Reg.GetBindings(aor)
	assert.Empty(t, bindings, "expected 0 bindings after unregister")
}
