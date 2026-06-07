package options

import (
	"strings"
	"testing"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
)

func TestIntegration_Options(t *testing.T) {
	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServer(t, "127.0.0.1")
		defer ts.Stop()

		ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)

		req := buildOptionsRequest(ts.Domain, ts.UDPPort)

		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)

		assertOptionsOK(t, res, ts)

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

		req := buildOptionsRequest(ts.Domain, ts.TCPPort)
		req.SetTransport("TCP")

		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)

		assertOptionsOK(t, res, ts)

		via := res.GetHeader("Via")
		require.NotNil(t, via, "Via header must be present")
		assert.Contains(t, via.Value(), "TCP", "Via transport must be TCP")

		client.Close()
		ua.Close()
	})
}

func buildOptionsRequest(domain string, port int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.OPTIONS, sipgo_sip.Uri{
		User: "test",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", "<sip:test@"+domain+">;tag=options123"))
	req.AppendHeader(sipgo_sip.NewHeader("To", "<sip:test@"+domain+">"))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "integration-options-"+domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 OPTIONS"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func assertOptionsOK(t *testing.T, res *sipgo_sip.Response, ts *integrationtest.TestServer) {
	t.Helper()

	assert.Equal(t, 200, res.StatusCode, "expected 200 OK for OPTIONS")

	allow := res.GetHeader("Allow")
	require.NotNil(t, allow, "Allow header must be present")
	allowVal := allow.Value()
	assert.True(t, strings.Contains(allowVal, "REGISTER"), "Allow header must include REGISTER")

	accept := res.GetHeader("Accept")
	require.NotNil(t, accept, "Accept header must be present")
	assert.Contains(t, accept.Value(), "application/sdp")

	supported := res.GetHeader("Supported")
	require.NotNil(t, supported, "Supported header must be present")
	assert.Contains(t, supported.Value(), "timer")
}
