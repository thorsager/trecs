package trunk

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	sipgo_sip "github.com/emiago/sipgo/sip"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
	"github.com/thorsager/trecs/internal/trunk"
	"github.com/thorsager/trecs/proto"
)

func TestIntegration_Trunk(t *testing.T) {
	t.Run("T1_StaticOutbound_BasicCall", func(t *testing.T) {
		peer := newTrunkPeer(t)
		defer peer.Close()

		trunks := []trunk.Trunk{
			{
				Name:      "itsp",
				Type:      "static",
				Host:      "127.0.0.1",
				Port:      peer.Port(),
				Transport: "udp",
			},
		}
		routes := []trunk.OutboundRoute{
			{
				Name:        "to-itsp",
				Pattern:     "^9(.*)$",
				StripDigits: 1,
				TrunkName:   "itsp",
			},
		}

		ts := startTrunkTestServer(t, trunks, routes)
		defer ts.Stop()

		peer.expectedServerSSRC = integrationtest.RandomSSRC()

		aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)
		defer aliceClient.Close()
		defer aliceUA.Close()

		aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP.Close()

		callID := fmt.Sprintf("trunk-outbound-%s", t.Name())
		fromTag := "alice-trunk-123"

		invite := buildTrunkInvite(ts.Domain, ts.UDPPort, callID, fromTag, "915551234567", aliceRTP.LocalAddr().(*net.UDPAddr).Port)
		res, err := aliceClient.Do(t.Context(), invite)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

		serverTag := integrationtest.ExtractToTag(res)
		require.NotEmpty(t, serverTag, "To header should have server tag")

		require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
		sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
		require.NoError(t, err)
		serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
		require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

		ack := buildTrunkACK(ts.Domain, ts.UDPPort, callID, fromTag, serverTag)
		err = aliceClient.WriteRequest(ack)
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

		aliceSSRC := integrationtest.RandomSSRC()
		integrationtest.SendRTPPackets(t, aliceRTP, serverRTPAddr, aliceSSRC)

		select {
		case count := <-peer.rtpCount:
			require.Positive(t, count, "Trunk peer should receive RTP packets")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for trunk peer to receive RTP")
		}

		bye := buildTrunkBYE(ts.Domain, ts.UDPPort, callID, fromTag, serverTag)
		byeRes, err := aliceClient.Do(t.Context(), bye)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")

		peer.assertByeReceived(t)
	})

	t.Run("T3_StaticInbound_BasicCall", func(t *testing.T) {
		peer := newTrunkPeer(t)
		defer peer.Close()

		trunks := []trunk.Trunk{
			{
				Name:       "itsp",
				Type:       "static",
				Host:       "127.0.0.1",
				Port:       peer.Port(),
				Transport:  "udp",
				TrustedIPs: []string{"127.0.0.1/32"},
			},
		}
		routes := []trunk.OutboundRoute{}

		ts := startTrunkTestServer(t, trunks, routes)
		defer ts.Stop()

		bob := newBobUAS(t, ts, "udp")
		defer bob.close()
		bob.register(t)
		time.Sleep(100 * time.Millisecond)

		peer.expectedServerSSRC = integrationtest.RandomSSRC()

		aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)
		defer aliceClient.Close()
		defer aliceUA.Close()

		aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP.Close()

		callID := fmt.Sprintf("trunk-inbound-%s", t.Name())
		fromTag := "itsp-from-123"

		invite := buildTrunkInvite(ts.Domain, ts.UDPPort, callID, fromTag, "bob", aliceRTP.LocalAddr().(*net.UDPAddr).Port)
		res, err := aliceClient.Do(t.Context(), invite)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Caller should receive 200 OK")

		serverTag := integrationtest.ExtractToTag(res)
		require.NotEmpty(t, serverTag, "To header should have server tag")

		require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
		sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
		require.NoError(t, err)
		serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
		require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

		ack := buildTrunkACK(ts.Domain, ts.UDPPort, callID, fromTag, serverTag)
		err = aliceClient.WriteRequest(ack)
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

		aliceSSRC := integrationtest.RandomSSRC()
		integrationtest.SendRTPPackets(t, aliceRTP, serverRTPAddr, aliceSSRC)

		select {
		case count := <-bob.rtpCount:
			require.Positive(t, count, "Bob should receive RTP packets")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for Bob to receive RTP")
		}

		bye := buildTrunkBYE(ts.Domain, ts.UDPPort, callID, fromTag, serverTag)
		byeRes, err := aliceClient.Do(t.Context(), bye)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")
	})

	t.Run("T4_CapacityLimit", func(t *testing.T) {
		peer := newTrunkPeer(t)
		defer peer.Close()

		trunks := []trunk.Trunk{
			{
				Name:        "itsp",
				Type:        "static",
				Host:        "127.0.0.1",
				Port:        peer.Port(),
				Transport:   "udp",
				MaxChannels: 2,
			},
		}
		routes := []trunk.OutboundRoute{
			{
				Name:        "to-itsp",
				Pattern:     "^9(.*)$",
				StripDigits: 1,
				TrunkName:   "itsp",
			},
		}

		ts := startTrunkTestServer(t, trunks, routes)
		defer ts.Stop()

		peer.expectedServerSSRC = integrationtest.RandomSSRC()

		aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)
		defer aliceClient.Close()
		defer aliceUA.Close()

		aliceRTP1, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP1.Close()

		aliceRTP2, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP2.Close()

		aliceRTP3, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP3.Close()

		callID1 := fmt.Sprintf("trunk-capacity-1-%s", t.Name())
		callID2 := fmt.Sprintf("trunk-capacity-2-%s", t.Name())
		callID3 := fmt.Sprintf("trunk-capacity-3-%s", t.Name())

		invite1 := buildTrunkInvite(ts.Domain, ts.UDPPort, callID1, "alice-cap-1", "915551234567", aliceRTP1.LocalAddr().(*net.UDPAddr).Port)
		res1, err := aliceClient.Do(t.Context(), invite1)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, res1.StatusCode, "First call should succeed")
		serverTag1 := integrationtest.ExtractToTag(res1)
		require.NotEmpty(t, serverTag1)

		ack1 := buildTrunkACK(ts.Domain, ts.UDPPort, callID1, "alice-cap-1", serverTag1)
		err = aliceClient.WriteRequest(ack1)
		require.NoError(t, err)

		time.Sleep(50 * time.Millisecond)

		invite2 := buildTrunkInvite(ts.Domain, ts.UDPPort, callID2, "alice-cap-2", "915551234568", aliceRTP2.LocalAddr().(*net.UDPAddr).Port)
		res2, err := aliceClient.Do(t.Context(), invite2)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, res2.StatusCode, "Second call should succeed")
		serverTag2 := integrationtest.ExtractToTag(res2)
		require.NotEmpty(t, serverTag2)

		ack2 := buildTrunkACK(ts.Domain, ts.UDPPort, callID2, "alice-cap-2", serverTag2)
		err = aliceClient.WriteRequest(ack2)
		require.NoError(t, err)

		time.Sleep(50 * time.Millisecond)

		invite3 := buildTrunkInvite(ts.Domain, ts.UDPPort, callID3, "alice-cap-3", "915551234569", aliceRTP3.LocalAddr().(*net.UDPAddr).Port)
		res3, err := aliceClient.Do(t.Context(), invite3)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusServiceUnavailable, res3.StatusCode, "Third call should get 503")

		bye1 := buildTrunkBYE(ts.Domain, ts.UDPPort, callID1, "alice-cap-1", serverTag1)
		byeRes1, err := aliceClient.Do(t.Context(), bye1)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, byeRes1.StatusCode, "BYE should get 200 OK")

		bye2 := buildTrunkBYE(ts.Domain, ts.UDPPort, callID2, "alice-cap-2", serverTag2)
		byeRes2, err := aliceClient.Do(t.Context(), bye2)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, byeRes2.StatusCode, "BYE should get 200 OK")
	})

	t.Run("T5_PAIIdentity", func(t *testing.T) {
		peer := newTrunkPeer(t)
		defer peer.Close()

		trunks := []trunk.Trunk{
			{
				Name:      "itsp",
				Type:      "static",
				Host:      "127.0.0.1",
				Port:      peer.Port(),
				Transport: "udp",
				CallerID:  "trunk-main",
			},
		}
		routes := []trunk.OutboundRoute{
			{
				Name:        "to-itsp",
				Pattern:     "^9(.*)$",
				StripDigits: 1,
				TrunkName:   "itsp",
			},
		}

		ts := startTrunkTestServer(t, trunks, routes)
		defer ts.Stop()

		peer.expectedServerSSRC = integrationtest.RandomSSRC()

		aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)
		defer aliceClient.Close()
		defer aliceUA.Close()

		aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP.Close()

		callID := fmt.Sprintf("trunk-pai-%s", t.Name())
		fromTag := "alice-pai-123"

		invite := buildTrunkInvite(ts.Domain, ts.UDPPort, callID, fromTag, "915551234567", aliceRTP.LocalAddr().(*net.UDPAddr).Port)
		res, err := aliceClient.Do(t.Context(), invite)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

		pai := peer.InvitePAI()
		require.NotEmpty(t, pai, "P-Asserted-Identity should be present on trunk INVITE")
		require.Contains(t, pai, "<sip:trunk-main@127.0.0.1>",
			"PAI should contain the trunk CallerID")

		from := peer.InviteFromHeader()
		require.NotEmpty(t, from, "From header should be present on trunk INVITE")
		require.Contains(t, from, "<sip:alice@127.0.0.1>",
			"From header should contain the calling user")

		serverTag := integrationtest.ExtractToTag(res)
		require.NotEmpty(t, serverTag, "To header should have server tag")

		require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
		sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
		require.NoError(t, err)
		serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
		require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

		ack := buildTrunkACK(ts.Domain, ts.UDPPort, callID, fromTag, serverTag)
		err = aliceClient.WriteRequest(ack)
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

		aliceSSRC := integrationtest.RandomSSRC()
		integrationtest.SendRTPPackets(t, aliceRTP, serverRTPAddr, aliceSSRC)

		select {
		case count := <-peer.rtpCount:
			require.Positive(t, count, "Trunk peer should receive RTP packets")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for trunk peer to receive RTP")
		}

		bye := buildTrunkBYE(ts.Domain, ts.UDPPort, callID, fromTag, serverTag)
		byeRes, err := aliceClient.Do(t.Context(), bye)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")

		peer.assertByeReceived(t)
	})

	t.Run("T6_HeaderStripping", func(t *testing.T) {
		peer := newTrunkPeer(t)
		defer peer.Close()

		trunks := []trunk.Trunk{
			{
				Name:         "itsp",
				Type:         "static",
				Host:         "127.0.0.1",
				Port:         peer.Port(),
				Transport:    "udp",
				CallerID:     "trunk-main",
				StripHeaders: []string{"P-Asserted-Identity", "Privacy"},
			},
		}
		routes := []trunk.OutboundRoute{
			{
				Name:        "to-itsp",
				Pattern:     "^9(.*)$",
				StripDigits: 1,
				TrunkName:   "itsp",
			},
		}

		ts := startTrunkTestServer(t, trunks, routes)
		defer ts.Stop()

		peer.expectedServerSSRC = integrationtest.RandomSSRC()

		aliceUA, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)

		aliceClient, err := sipgo.NewClient(aliceUA, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)
		defer aliceClient.Close()
		defer aliceUA.Close()

		aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP.Close()

		callID := fmt.Sprintf("trunk-strip-%s", t.Name())
		fromTag := "alice-strip-123"

		invite := buildTrunkInvite(ts.Domain, ts.UDPPort, callID, fromTag, "915551234567", aliceRTP.LocalAddr().(*net.UDPAddr).Port)
		res, err := aliceClient.Do(t.Context(), invite)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Alice should receive 200 OK")

		// PAI should NOT be present since we configured strip_headers to remove it
		pai := peer.InvitePAI()
		require.Empty(t, pai, "P-Asserted-Identity should be stripped from trunk INVITE")

		// From header should still be present (not stripped)
		from := peer.InviteFromHeader()
		require.NotEmpty(t, from, "From header should still be present on trunk INVITE")

		serverTag := integrationtest.ExtractToTag(res)
		require.NotEmpty(t, serverTag, "To header should have server tag")

		require.NotEmpty(t, res.Body(), "200 OK should have SDP body")
		sdpAnswer, err := proto.UnmarshalSDPBytes(res.Body())
		require.NoError(t, err)
		serverIP, serverRTPPort := integrationtest.ExtractRTPAddr(sdpAnswer)
		require.NotZero(t, serverRTPPort, "SDP answer should have RTP port")

		ack := buildTrunkACK(ts.Domain, ts.UDPPort, callID, fromTag, serverTag)
		err = aliceClient.WriteRequest(ack)
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		serverRTPAddr := &net.UDPAddr{IP: net.ParseIP(serverIP), Port: serverRTPPort}

		aliceSSRC := integrationtest.RandomSSRC()
		integrationtest.SendRTPPackets(t, aliceRTP, serverRTPAddr, aliceSSRC)

		select {
		case count := <-peer.rtpCount:
			require.Positive(t, count, "Trunk peer should receive RTP packets")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for trunk peer to receive RTP")
		}

		bye := buildTrunkBYE(ts.Domain, ts.UDPPort, callID, fromTag, serverTag)
		byeRes, err := aliceClient.Do(t.Context(), bye)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, byeRes.StatusCode, "BYE should get 200 OK")

		peer.assertByeReceived(t)
	})

	t.Run("T7_GhostSessionTeardown", func(t *testing.T) {
		peer := newTrunkPeer(t)
		defer peer.Close()

		// MaxChannels=1 so we can prove channel release
		trunks := []trunk.Trunk{
			{
				Name:              "itsp",
				Type:              "static",
				Host:              "127.0.0.1",
				Port:              peer.Port(),
				Transport:         "udp",
				MaxChannels:       1,
				SessionExpiresSec: 2,
			},
		}
		routes := []trunk.OutboundRoute{
			{
				Name:        "to-itsp",
				Pattern:     "^9(.*)$",
				StripDigits: 1,
				TrunkName:   "itsp",
			},
		}

		ts := startTrunkTestServer(t, trunks, routes)
		defer ts.Stop()

		peer.expectedServerSSRC = integrationtest.RandomSSRC()

		aliceUA1, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)
		aliceClient1, err := sipgo.NewClient(aliceUA1, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)
		defer aliceClient1.Close()
		defer aliceUA1.Close()

		aliceRTP1, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP1.Close()

		callID1 := fmt.Sprintf("trunk-ghost-1-%s", t.Name())
		fromTag1 := "alice-ghost-1"

		invite1 := buildTrunkInvite(ts.Domain, ts.UDPPort, callID1, fromTag1, "915551234567", aliceRTP1.LocalAddr().(*net.UDPAddr).Port)
		res1, err := aliceClient1.Do(t.Context(), invite1)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, res1.StatusCode, "First call should succeed")
		serverTag1 := integrationtest.ExtractToTag(res1)
		require.NotEmpty(t, serverTag1)

		// Complete call 1 (ACK + RTP)
		ack1 := buildTrunkACK(ts.Domain, ts.UDPPort, callID1, fromTag1, serverTag1)
		err = aliceClient1.WriteRequest(ack1)
		require.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		require.NotEmpty(t, res1.Body(), "200 OK should have SDP body")
		sdpAnswer1, err := proto.UnmarshalSDPBytes(res1.Body())
		require.NoError(t, err)
		serverIP1, serverRTPPort1 := integrationtest.ExtractRTPAddr(sdpAnswer1)
		serverRTPAddr1 := &net.UDPAddr{IP: net.ParseIP(serverIP1), Port: serverRTPPort1}

		aliceSSRC1 := integrationtest.RandomSSRC()
		integrationtest.SendRTPPackets(t, aliceRTP1, serverRTPAddr1, aliceSSRC1)

		select {
		case count := <-peer.rtpCount:
			require.Positive(t, count, "Trunk peer should receive RTP packets")
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for trunk peer to receive RTP")
		}

		// Don't send BYE from Alice — let session timer expire
		// Wait for session timer (2s) plus margin
		time.Sleep(3500 * time.Millisecond)

		// Now make a second call — should succeed because session timer
		// should have released the channel
		aliceUA2, err := sipgo.NewUA(sipgo.WithUserAgentHostname(ts.Domain))
		require.NoError(t, err)
		aliceClient2, err := sipgo.NewClient(aliceUA2, sipgo.WithClientAddr("127.0.0.1:0"))
		require.NoError(t, err)
		defer aliceClient2.Close()
		defer aliceUA2.Close()

		aliceRTP2, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		require.NoError(t, err)
		defer aliceRTP2.Close()

		callID2 := fmt.Sprintf("trunk-ghost-2-%s", t.Name())
		fromTag2 := "alice-ghost-2"

		invite2 := buildTrunkInvite(ts.Domain, ts.UDPPort, callID2, fromTag2, "915551234568", aliceRTP2.LocalAddr().(*net.UDPAddr).Port)
		res2, err := aliceClient2.Do(t.Context(), invite2)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, res2.StatusCode, "Second call should succeed after session timer releases channel")

		serverTag2 := integrationtest.ExtractToTag(res2)
		require.NotEmpty(t, serverTag2)

		ack2 := buildTrunkACK(ts.Domain, ts.UDPPort, callID2, fromTag2, serverTag2)
		err = aliceClient2.WriteRequest(ack2)
		require.NoError(t, err)

		// Clean up call 2
		bye2 := buildTrunkBYE(ts.Domain, ts.UDPPort, callID2, fromTag2, serverTag2)
		byeRes2, err := aliceClient2.Do(t.Context(), bye2)
		require.NoError(t, err)
		require.Equal(t, proto.SIPStatusOK, byeRes2.StatusCode, "BYE should get 200 OK")
	})
}

func startTrunkTestServer(t *testing.T, trunks []trunk.Trunk, routes []trunk.OutboundRoute) *integrationtest.TestServer {
	t.Helper()

	host := "127.0.0.1"

	trunkCfg := &trunk.TrunkConfig{
		Trunks: trunks,
		Routes: routes,
	}

	addr := host + ":0"
	trunkMgr, err := trunk.NewTrunkManager(trunkCfg, host, addr)
	require.NoError(t, err)

	ts := integrationtest.StartTestServerWithDialplan(t, host, nil, integrationtest.WithTrunkManager(trunkMgr))
	trunkMgr.Start(t.Context())
	return ts
}

func buildTrunkInvite(domain string, port int, callID, fromTag, user string, rtpPort int) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.INVITE, sipgo_sip.Uri{
		User: user,
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:alice@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:%s@%s>", user, domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 INVITE"))
	req.AppendHeader(sipgo_sip.NewHeader("Contact", fmt.Sprintf("<sip:alice@127.0.0.1:%d;transport=udp>", rtpPort)))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Type", "application/sdp"))

	sdp := integrationtest.BuildSDPOffer(rtpPort, "127.0.0.1")
	sdpBytes, _ := sdp.Marshal()
	req.SetBody(sdpBytes)
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", strconv.Itoa(len(sdpBytes))))

	return req
}

func buildTrunkACK(domain string, port int, callID, fromTag, serverTag string) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.ACK, sipgo_sip.Uri{
		User: "bob",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:alice@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>;tag=%s", domain, serverTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 ACK"))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func buildTrunkBYE(domain string, port int, callID, fromTag, serverTag string) *sipgo_sip.Request {
	req := sipgo_sip.NewRequest(sipgo_sip.BYE, sipgo_sip.Uri{
		User: "bob",
		Host: domain,
		Port: port,
	})
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:alice@%s>;tag=%s", domain, fromTag)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>;tag=%s", domain, serverTag)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", callID))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "2 BYE"))
	req.AppendHeader(sipgo_sip.NewHeader("Max-Forwards", "70"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))
	return req
}

func newBobUAS(t *testing.T, ts *integrationtest.TestServer, transport string) *bobUAS {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	b := &bobUAS{
		t:           t,
		ctx:         ctx,
		cancel:      cancel,
		ts:          ts,
		transport:   transport,
		byeReceived: make(chan struct{}),
		rtpCount:    make(chan int, 1),
		ready:       make(chan struct{}),
	}

	var err error
	b.sipConn, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	b.sipPort = b.sipConn.LocalAddr().(*net.UDPAddr).Port

	b.rtp, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	go b.sipListen()
	<-b.ready

	return b
}

type bobUAS struct {
	t         *testing.T
	ctx       context.Context
	cancel    context.CancelFunc
	ts        *integrationtest.TestServer
	transport string
	sipConn   *net.UDPConn
	sipPort   int
	rtp       *net.UDPConn

	mu          sync.Mutex
	callID      string
	fromTag     string
	toTag       string
	cseq        int
	answered    bool
	byeReceived chan struct{}
	byeOnce     sync.Once
	rtpCount    chan int
	ready       chan struct{}
}

func (b *bobUAS) sipListen() {
	close(b.ready)

	buf := make([]byte, 4096)
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		_ = b.sipConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, remoteAddr, err := b.sipConn.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		msg := string(buf[:n])
		writeFunc := func(data []byte) error {
			_, err := b.sipConn.WriteToUDP(data, remoteAddr)
			return err
		}
		b.t.Logf("Bob received %d bytes: %q", n, msg[:min(n, 100)])

		switch {
		case strings.HasPrefix(msg, "INVITE"):
			b.handleInvite(msg, writeFunc)
		case strings.HasPrefix(msg, "BYE"):
			b.handleBye(msg, writeFunc)
		}
	}
}

func (b *bobUAS) handleInvite(msg string, writeFunc func([]byte) error) {
	viaHeader := extractBobHeader(msg, "Via")
	callID := extractBobHeader(msg, "Call-ID")
	fromHeader := extractBobHeader(msg, "From")

	b.mu.Lock()
	b.toTag = fmt.Sprintf("bob-%d", time.Now().UnixNano())
	b.callID = callID
	if idx := strings.Index(fromHeader, ";tag="); idx != -1 {
		b.fromTag = fromHeader[idx+5:]
	}
	cseqLine := extractBobHeader(msg, "CSeq")
	if cseqLine != "" {
		parts := strings.Fields(cseqLine)
		if len(parts) > 0 {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				b.cseq = n
			}
		}
	}
	b.mu.Unlock()

	rtpPort := b.rtp.LocalAddr().(*net.UDPAddr).Port
	sdp := fmt.Sprintf("v=0\r\no=- %d 1 IN IP4 127.0.0.1\r\ns=bob\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n",
		time.Now().UnixNano(), rtpPort)

	resp := fmt.Sprintf("SIP/2.0 200 OK\r\nVia: %s\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:bob@%s>;tag=%s\r\nCSeq: %d INVITE\r\nContent-Type: application/sdp\r\nContact: <sip:bob@127.0.0.1:%d;transport=%s>\r\nContent-Length: %d\r\n\r\n%s",
		viaHeader, callID, fromHeader, b.ts.Domain, b.toTag, b.cseq, rtpPort, b.transport, len(sdp), sdp)

	b.t.Logf("Bob sending 200 OK")
	if err := writeFunc([]byte(resp)); err != nil {
		b.t.Logf("Bob failed to send 200 OK: %v", err)
	} else {
		b.t.Logf("Bob sent 200 OK (RTP port %d)", rtpPort)
		go receiveRTP(b.rtp, b.rtpCount, b.ctx, 0)
	}

	b.mu.Lock()
	b.answered = true
	b.mu.Unlock()
}

func (b *bobUAS) handleBye(msg string, writeFunc func([]byte) error) {
	viaHeader := extractBobHeader(msg, "Via")
	callID := extractBobHeader(msg, "Call-ID")
	fromHeader := extractBobHeader(msg, "From")
	cseqLine := extractBobHeader(msg, "CSeq")
	cseqNum := 0
	if cseqLine != "" {
		parts := strings.Fields(cseqLine)
		if len(parts) > 0 {
			if n, err := strconv.Atoi(parts[0]); err == nil {
				cseqNum = n
			}
		}
	}

	resp := fmt.Sprintf("SIP/2.0 200 OK\r\nVia: %s\r\nCall-ID: %s\r\nFrom: %s\r\nTo: <sip:bob@%s>;tag=%s\r\nCSeq: %d BYE\r\nContent-Length: 0\r\n\r\n",
		viaHeader, callID, fromHeader, b.ts.Domain, b.toTag, cseqNum)
	_ = writeFunc([]byte(resp))
	b.byeOnce.Do(func() { close(b.byeReceived) })
}

func (b *bobUAS) register(t *testing.T) {
	t.Helper()

	t.Logf("Bob registering SIP port %d (transport=%s)", b.sipPort, b.transport)

	contact := fmt.Sprintf("<sip:bob@127.0.0.1:%d;transport=%s>", b.sipPort, b.transport)
	req := sipgo_sip.NewRequest(sipgo_sip.REGISTER, sipgo_sip.Uri{
		User: "bob",
		Host: b.ts.Domain,
		Port: integrationtest.GetPort(b.ts, b.transport),
	})
	req.AppendHeader(sipgo_sip.NewHeader("Contact", contact))
	req.AppendHeader(sipgo_sip.NewHeader("From", fmt.Sprintf("<sip:bob@%s>;tag=bob-123", b.ts.Domain)))
	req.AppendHeader(sipgo_sip.NewHeader("To", fmt.Sprintf("<sip:bob@%s>", b.ts.Domain)))
	req.AppendHeader(sipgo_sip.NewHeader("Call-ID", "trunk-bob-"+b.ts.Domain))
	req.AppendHeader(sipgo_sip.NewHeader("CSeq", "1 REGISTER"))
	req.AppendHeader(sipgo_sip.NewHeader("Content-Length", "0"))

	if b.transport == "tcp" {
		req.SetTransport("TCP")
	}

	ua, err := sipgo.NewUA(sipgo.WithUserAgentHostname(b.ts.Domain))
	require.NoError(t, err)
	defer ua.Close()

	client, err := sipgo.NewClient(ua, sipgo.WithClientAddr("127.0.0.1:0"))
	require.NoError(t, err)
	defer client.Close()

	res, err := client.Do(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, proto.SIPStatusOK, res.StatusCode, "Bob registration should succeed")
	t.Logf("Bob registered")
}

func (b *bobUAS) close() {
	b.cancel()
	if b.rtp != nil {
		b.rtp.Close()
	}
	if b.sipConn != nil {
		b.sipConn.Close()
	}
}

func extractBobHeader(msg, name string) string {
	prefix := name + ":"
	idx := strings.Index(msg, prefix)
	if idx == -1 {
		return ""
	}
	line := msg[idx+len(prefix):]
	if end := strings.Index(line, "\r\n"); end != -1 {
		return strings.TrimSpace(line[:end])
	}
	return ""
}
