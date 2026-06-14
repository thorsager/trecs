package b2bua

import (
	"net"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
	"github.com/thorsager/trecs/proto"
)

func TestIntegration_B2BUACallWithAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("bob", "password", "sip:bob@127.0.0.1"),
	)

	t.Run("UDP_UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		bob := newBobUAS(t, ts, "udp")
		defer bob.close()

		bob.registerWithAuth(t, "bob", "password")
		time.Sleep(100 * time.Millisecond)

		runAliceInviteAndVerify(t, ts, bob, "udp", "alice_bye", "bob", "password")
	})

	t.Run("TCP_TCP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		bob := newBobUAS(t, ts, "tcp")
		defer bob.close()

		bob.registerWithAuth(t, "bob", "password")
		time.Sleep(100 * time.Millisecond)

		runAliceInviteAndVerify(t, ts, bob, "tcp", "alice_bye", "bob", "password")
	})
}

func runAliceInviteAndVerify(t *testing.T, ts *integrationtest.TestServer, bob *bobUAS, transport, byeFrom, authUsername, authPassword string) {
	t.Helper()

	aliceSSRC := randomSSRC()
	bobSSRC := randomSSRC()
	bob.expectedClientSSRC = aliceSSRC
	bob.expectedBobSSRC = bobSSRC

	aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
	require.NoError(t, err)

	aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	defer aliceClient.Close()
	defer aliceUA.Close()

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	callID := "b2bua-auth-" + t.Name()
	aliceFromTag := "alice-auth-123"

	invite := buildB2BUAInvite(ts.Domain, integrationtest.GetPort(ts, transport), callID, aliceFromTag, transport, aliceRTP.LocalAddr().(*net.UDPAddr).Port)
	if transport == "tcp" {
		invite.SetTransport("TCP")
	}

	res := doProxyAuthRequest(t, aliceClient, invite, authUsername, authPassword)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

	serverTag := extractToTagB2B(res)
	require.NotEmpty(t, serverTag, "To header should have server tag")

	require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
	sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
	require.NoError(t, err)
	serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
	require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

	ack := buildB2BUAACK(ts.Domain, integrationtest.GetPort(ts, transport), callID, aliceFromTag, serverTag)
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

	switch byeFrom {
	case "alice_bye":
		bye := buildB2BUABYE(ts.Domain, integrationtest.GetPort(ts, transport), callID, aliceFromTag, serverTag)
		if transport == "tcp" {
			bye.SetTransport("TCP")
		}
		byeRes := doProxyAuthRequest(t, aliceClient, bye, authUsername, authPassword)
		require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")
	case "bob_bye":
		require.NoError(t, bob.sendBye(), "Bob should be able to send BYE and get 200 OK")
	}
}

// Verify that unauthenticated registration is rejected when auth is enabled.
func TestIntegration_B2BUARegisterRejectedWithoutAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "secret", "sip:alice@127.0.0.1"),
	)

	t.Run("UDP", func(t *testing.T) {
		ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
		defer ts.Stop()

		ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)

		req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
			User: "alice",
			Host: ts.Domain,
			Port: ts.UDPPort,
		})
		req.AppendHeader(sipgo_sip.NewHeader("Contact", "<sip:alice@127.0.0.1>"))
		req.AppendHeader(sipgo_sip.NewHeader("From", "<sip:alice@"+ts.Domain+">;tag=test-noauth"))
		req.AppendHeader(sipgo_sip.NewHeader("To", "<sip:alice@"+ts.Domain+">"))
		req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "reject-noauth-"+ts.Domain))
		req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 REGISTER"))
		req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))

		res, err := client.Do(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusUnauthorized, res.StatusCode, "Should get 401 when no auth provided")

		wwwAuth := res.GetHeader("WWW-Authenticate")
		require.NotNil(t, wwwAuth, "Should have WWW-Authenticate header")
		val := wwwAuth.Value()
		require.Contains(t, val, "Digest")
		require.Contains(t, val, `realm="127.0.0.1"`)
		require.Contains(t, val, "algorithm=SHA-256")
		require.Contains(t, val, "qop=\"auth\"")
		require.Contains(t, val, "nonce=", "WWW-Authenticate must include nonce per RFC 3261 §22.1")
		require.NotContains(t, val, "stale=TRUE", "Initial challenge should not have stale=TRUE")

		client.Close()
		ua.Close()
	})
}
