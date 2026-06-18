package b2bua

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
	trecs_sip "github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

func doProxyAuthRequest(t *testing.T, client *sipgo.Client, req *sipgo_sip.Request, username, password string) *sipgo_sip.Response {
	t.Helper()

	res, err := client.Do(t.Context(), req)
	require.NoError(t, err)

	if res.StatusCode != proto.SIPStatusProxyAuthenticationRequired {
		return res
	}

	authReq := integrationtest.BuildProxyAuthRequest(t, req, res, username, password)

	res, err = client.Do(t.Context(), authReq)
	require.NoError(t, err)

	resCSeq := res.CSeq()
	require.NotNil(t, resCSeq, "Response must have CSeq")
	require.Equal(t, req.Method, resCSeq.MethodName, "CSeq method must match")
	return res
}

func runB2BUACallWithProxyAuth(t *testing.T, ts *integrationtest.TestServer, transport, username, password string) {
	t.Helper()

	aliceSSRC := integrationtest.RandomSSRC()
	bobSSRC := integrationtest.RandomSSRC()

	bob := newBobUAS(t, ts, transport)
	defer bob.close()
	bob.expectedClientSSRC = aliceSSRC
	bob.expectedBobSSRC = bobSSRC
	bob.registerWithAuth(t, username, password)
	time.Sleep(100 * time.Millisecond)

	port := integrationtest.GetPort(ts, transport)
	aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)
	defer aliceUA.Close()
	aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	defer aliceClient.Close()
	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	callID := "b2bua-proxy-auth-" + t.Name()
	aliceFromTag := "alice-proxy-123"

	invite := buildB2BUAInvite(ts.Domain, port, callID, aliceFromTag, transport, aliceRTP.LocalAddr().(*net.UDPAddr).Port)
	if transport == "tcp" {
		invite.SetTransport("TCP")
	}

	res := doProxyAuthRequest(t, aliceClient, invite, username, password)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

	serverTag := extractToTagB2B(res)
	require.NotEmpty(t, serverTag, "To header should have server tag")

	require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
	require.NoError(t, err)
	serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

	ack := buildB2BUAACK(ts.Domain, port, callID, aliceFromTag, serverTag)
	if transport == "tcp" {
		ack.SetTransport("TCP")
	}
	err = aliceClient.WriteRequest(ack)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

	var serverRTPPortB int
	select {
	case serverRTPPortB = <-bob.serverRTPPortBCh:
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for Bob to extract server RTP port")
	}
	require.NotZero(t, serverRTPPortB, "Bob should have extracted server's RTP port")
	serverRTPAddrB := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: serverRTPPortB}

	sendAliceToBob(t, aliceRTP, serverRTPAddr, bob.rtpCount, aliceSSRC)
	sendBobToAlice(t, bob.rtp, serverRTPAddrB, aliceRTP, bobSSRC)

	bye := buildB2BUABYE(ts.Domain, port, callID, aliceFromTag, serverTag)
	if transport == "tcp" {
		bye.SetTransport("TCP")
	}

	byeRes := doProxyAuthRequest(t, aliceClient, bye, username, password)
	require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")
}

// TestIntegration_ProxyAuth_InviteRejectedWithoutAuth: INVITE with no Proxy-Authorization → 407.
func TestIntegration_ProxyAuth_InviteRejectedWithoutAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	for _, transport := range []string{"udp", "tcp"} {
		t.Run(strings.ToUpper(transport), func(t *testing.T) {
			ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
			defer ts.Stop()

			ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
			require.NoError(t, err)
			client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
			require.NoError(t, err)

			aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
			require.NoError(t, err)
			defer aliceRTP.Close()

			invite := buildB2BUAInvite(ts.Domain, integrationtest.GetPort(ts, transport), "proxy-reject-"+transport, "alice-reject", transport, aliceRTP.LocalAddr().(*net.UDPAddr).Port)
			if transport == "tcp" {
				invite.SetTransport("TCP")
			}

			res, err := client.Do(t.Context(), invite)
			require.NoError(t, err)
			require.Equal(t, proto.SIPStatusProxyAuthenticationRequired, res.StatusCode, "Should get 407 when no proxy auth provided")

			proxyAuth := res.GetHeader("Proxy-Authenticate")
			require.NotNil(t, proxyAuth, "Should have Proxy-Authenticate header")
			val := proxyAuth.Value()
			require.Contains(t, val, "Digest")
			require.Contains(t, val, `realm="127.0.0.1"`)
			require.Contains(t, val, "algorithm=SHA-256")
			require.Contains(t, val, `qop="auth"`)
			require.Contains(t, val, "nonce=", "Proxy-Authenticate must include nonce per RFC 3261 §22.1")
			require.NotContains(t, val, "stale=TRUE", "Initial challenge should not have stale=TRUE")

			client.Close()
			ua.Close()
		})
	}
}

// TestIntegration_ProxyAuth_InviteAcceptedWithAuth: Full call with Proxy-Authorization.
func TestIntegration_ProxyAuth_InviteAcceptedWithAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("bob", "password", "sip:bob@127.0.0.1"),
	)

	for _, transport := range []string{"udp", "tcp"} {
		t.Run(strings.ToUpper(transport), func(t *testing.T) {
			ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
			defer ts.Stop()
			runB2BUACallWithProxyAuth(t, ts, transport, "bob", "password")
		})
	}
}

// TestIntegration_ProxyAuth_ByeRejectedWithoutAuth: BYE with no Proxy-Authorization → 407.
func TestIntegration_ProxyAuth_ByeRejectedWithoutAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("bob", "password", "sip:bob@127.0.0.1"),
	)

	for _, transport := range []string{"udp", "tcp"} {
		t.Run(strings.ToUpper(transport), func(t *testing.T) {
			ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
			defer ts.Stop()

			bob := newBobUAS(t, ts, transport)
			defer bob.close()
			bob.registerWithAuth(t, "bob", "password")
			time.Sleep(100 * time.Millisecond)

			port := integrationtest.GetPort(ts, transport)
			aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
			require.NoError(t, err)
			defer aliceUA.Close()
			aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
			require.NoError(t, err)
			defer aliceClient.Close()
			aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
			require.NoError(t, err)
			defer aliceRTP.Close()

			callID := "b2bua-proxy-bye-reject-" + t.Name()
			aliceFromTag := "alice-bye-reject"

			invite := buildB2BUAInvite(ts.Domain, port, callID, aliceFromTag, transport, aliceRTP.LocalAddr().(*net.UDPAddr).Port)
			if transport == "tcp" {
				invite.SetTransport("TCP")
			}

			res := doProxyAuthRequest(t, aliceClient, invite, "bob", "password")
			require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

			serverTag := extractToTagB2B(res)
			require.NotEmpty(t, serverTag)

			ack := buildB2BUAACK(ts.Domain, port, callID, aliceFromTag, serverTag)
			if transport == "tcp" {
				ack.SetTransport("TCP")
			}
			_ = aliceClient.WriteRequest(ack)

			bye := buildB2BUABYE(ts.Domain, port, callID, aliceFromTag, serverTag)
			if transport == "tcp" {
				bye.SetTransport("TCP")
			}

			// Send BYE without Proxy-Authorization → expect 407
			byeRes, err := aliceClient.Do(t.Context(), bye)
			require.NoError(t, err)
			require.Equal(t, proto.SIPStatusProxyAuthenticationRequired, byeRes.StatusCode, "BYE without proxy auth should get 407")
		})
	}
}

// TestIntegration_ProxyAuth_ByeAcceptedWithAuth: BYE with Proxy-Authorization → 200.
func TestIntegration_ProxyAuth_ByeAcceptedWithAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("bob", "password", "sip:bob@127.0.0.1"),
	)

	for _, transport := range []string{"udp", "tcp"} {
		t.Run(strings.ToUpper(transport), func(t *testing.T) {
			ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
			defer ts.Stop()
			runB2BUACallWithProxyAuth(t, ts, transport, "bob", "password")
		})
	}
}

// TestIntegration_ProxyAuth_WrongPassword: INVITE with bad credentials is
// challenged up to maxAttempts-1 times, then locked out with 403.
func TestIntegration_ProxyAuth_WrongPassword(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	for _, transport := range []string{"udp", "tcp"} {
		t.Run(strings.ToUpper(transport), func(t *testing.T) {
			ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
			ts.SetMaxFailedAuthAttempts(2)
			defer ts.Stop()

			ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
			require.NoError(t, err)
			client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
			require.NoError(t, err)

			aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
			require.NoError(t, err)
			defer aliceRTP.Close()

			invite := buildB2BUAInvite(ts.Domain, integrationtest.GetPort(ts, transport), "proxy-wrongpw-"+transport, "alice-wrongpw", transport, aliceRTP.LocalAddr().(*net.UDPAddr).Port)
			if transport == "tcp" {
				invite.SetTransport("TCP")
			}

			// Send INVITE without auth → get 407 (not counted as failure).
			res, err := client.Do(t.Context(), invite)
			require.NoError(t, err)
			require.Equal(t, proto.SIPStatusProxyAuthenticationRequired, res.StatusCode)

			// First wrong password → challenged again (count=1, below threshold).
			authReq := integrationtest.BuildProxyAuthRequest(t, invite, res, "alice", "wrongpass")
			res2, err := client.Do(t.Context(), authReq)
			require.NoError(t, err)
			require.Equal(t, proto.SIPStatusProxyAuthenticationRequired, res2.StatusCode, "First wrong password should be challenged")

			// Second wrong password → 403 lockout (count=2, threshold reached).
			authReq2 := integrationtest.BuildProxyAuthRequest(t, invite, res2, "alice", "wrongpass")
			badRes, err := client.Do(t.Context(), authReq2)
			require.NoError(t, err)
			require.Equal(t, proto.SIPStatusForbidden, badRes.StatusCode, "Wrong password should get 403 after threshold")

			client.Close()
			ua.Close()
		})
	}
}

// TestIntegration_ProxyAuth_BadNonce: INVITE with gibberish nonce → 407 stale=FALSE
// TestIntegration_ProxyAuth_OptionsBypassesAuth: OPTIONS should succeed without auth.
func TestIntegration_ProxyAuth_OptionsBypassesAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	for _, transport := range []string{"udp", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
			defer ts.Stop()

			ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
			require.NoError(t, err)
			defer ua.Close()
			client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
			require.NoError(t, err)
			defer client.Close()

			port := integrationtest.GetPort(ts, transport)
			req := sipgo_sip.NewRequest(sipgo_sip.OPTIONS, sipgo_sip.Uri{
				User: "",
				Host: ts.Domain,
				Port: port,
			})
			req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:alice@%s>;tag=options-test", ts.Domain)))
			req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:alice@%s>", ts.Domain)))
			req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "options-bypass-"+transport))
			req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 OPTIONS"))
			req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
			req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
			if transport == "tcp" {
				req.SetTransport("TCP")
			}

			res, err := client.Do(t.Context(), req)
			require.NoError(t, err)
			require.Equal(t, proto.SIPStatusOK, res.StatusCode, "OPTIONS should succeed without proxy auth")
		})
	}
}

func TestIntegration_ProxyAuth_BadNonce(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
	defer ts.Stop()

	ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)
	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	invite := buildB2BUAInvite(ts.Domain, ts.UDPPort, "proxy-badnonce", "alice-nonce", "udp", aliceRTP.LocalAddr().(*net.UDPAddr).Port)

	// Send with a made-up nonce + correct digest for that nonce
	ha1 := trecs_sip.ComputeHA1("alice", "127.0.0.1", "secret", "SHA-256")
	response := trecs_sip.ComputeDigestResponse(ha1, "bogus-nonce", "00000001", "cnonce", "auth", "INVITE", invite.Recipient.String(), "SHA-256")
	authValue := fmt.Sprintf(`Digest username="alice", realm="127.0.0.1", nonce="bogus-nonce", uri=%q, response=%q, algorithm=SHA-256, cnonce="cnonce", nc=00000001, qop=auth`,
		invite.Recipient.String(), response)

	authReq := invite.Clone()
	authReq.AppendHeader(sipgo_sip.NewHeader("Proxy-Authorization", authValue))
	res, err := client.Do(t.Context(), authReq)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusProxyAuthenticationRequired, res.StatusCode, "Bad nonce should get 407")

	// The stale flag should be FALSE because the nonce was unknown, not expired
	proxyAuth := res.GetHeader("Proxy-Authenticate")
	require.NotNil(t, proxyAuth)
	require.NotContains(t, proxyAuth.Value(), "stale=TRUE", "Unknown nonce should not set stale=TRUE")

	client.Close()
	ua.Close()
}
