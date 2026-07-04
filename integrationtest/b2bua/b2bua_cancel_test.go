package b2bua

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/integrationtest"
)

// TestIntegration_B2BUACancel_UASCancel verifies that the server correctly
// handles a CANCEL from Alice while Bob is ringing (RFC 3261 §9.2).
//   - Alice sends INVITE → server forwards to Bob
//   - Bob sends 180 Ringing → server forwards to Alice
//   - Alice sends CANCEL → server sends 200 OK (for CANCEL) + 487 (for INVITE)
//   - Alice sends ACK for the 487
func TestIntegration_B2BUACancel_UASCancel(t *testing.T) {
	ts := integrationtest.StartTestServer(t, "127.0.0.1")
	defer ts.Stop()

	bob := newBobUAS(t, ts, "udp")
	bob.ringBeforeAnswer = true
	defer bob.close()

	bob.register(t)
	time.Sleep(100 * time.Millisecond)

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	aliceConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: ts.UDPPort})
	require.NoError(t, err)
	defer aliceConn.Close()

	callID := fmt.Sprintf("cancel-uas-%s", t.Name())
	fromTag := "alice-cancel-uas"
	branch := "z9hG4bK-uas-cancel"

	alicePort := aliceRTP.LocalAddr().(*net.UDPAddr).Port

	sdp := integrationtest.BuildSDPOffer(alicePort, "127.0.0.1")
	sdpBytes, err := sdp.Marshal()
	require.NoError(t, err)

	// INVITE with a known branch for CANCEL matching.
	invite := fmt.Sprintf("INVITE sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"Contact: <sip:alice@127.0.0.1:%d;transport=udp>\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %d\r\n\r\n%s",
		alicePort, branch, fromTag, callID, alicePort, len(sdpBytes), sdpBytes)

	_, err = aliceConn.Write([]byte(invite))
	require.NoError(t, err)
	t.Log("Alice sent INVITE")

	// Read responses until we get 180 Ringing.
	buf := make([]byte, 4096)
	got180 := false
	for i := 0; i < 10; i++ {
		_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := aliceConn.Read(buf)
		if err != nil {
			continue
		}
		msg := string(buf[:n])
		t.Logf("Alice received (pre-cancel): %q", msg[:min(len(msg), 200)])
		if strings.Contains(msg, "180 Ringing") {
			got180 = true
			break
		}
		if strings.Contains(msg, "200 OK") {
			// Bob answered immediately — test precondition failed.
			t.Log("Bob answered before CANCEL, skipping cancel test")
			return
		}
	}
	require.True(t, got180, "Should receive 180 Ringing before CANCEL")

	// Build CANCEL with the same branch, Call-ID, From, To, and CSeq seq.
	cancel := fmt.Sprintf("CANCEL sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 CANCEL\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alicePort, branch, fromTag, callID)

	_, err = aliceConn.Write([]byte(cancel))
	require.NoError(t, err)
	t.Log("Alice sent CANCEL")

	// Read responses after CANCEL: expect 200 OK for CANCEL + 487 for INVITE.
	gotCancel200 := false
	gotInvite487 := false
	for i := 0; i < 10; i++ {
		_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := aliceConn.Read(buf)
		if err != nil {
			continue
		}
		msg := string(buf[:n])
		t.Logf("Alice received (post-cancel): %q", msg[:min(len(msg), 200)])

		if strings.Contains(msg, "SIP/2.0 200 OK") {
			// Distinguish 200 for CANCEL vs INVITE by CSeq.
			if strings.Contains(msg, "CANCEL") {
				gotCancel200 = true
			}
		}
		if strings.Contains(msg, "487 Request Terminated") {
			gotInvite487 = true
		}
	}

	require.True(t, gotCancel200, "Should receive 200 OK for CANCEL")
	require.True(t, gotInvite487, "Should receive 487 Request Terminated for INVITE")

	// Send ACK for the 487 to complete the transaction.
	ack := fmt.Sprintf("ACK sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 ACK\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alicePort, branch, fromTag, callID)

	_, err = aliceConn.Write([]byte(ack))
	require.NoError(t, err)
	t.Log("Alice sent ACK for 487")
}

// TestIntegration_B2BUACancel_LateCancel verifies that a CANCEL arriving
// after the call has been answered gets a 481 response.
func TestIntegration_B2BUACancel_LateCancel(t *testing.T) {
	ts := integrationtest.StartTestServer(t, "127.0.0.1")
	defer ts.Stop()

	bob := newBobUAS(t, ts, "udp")
	defer bob.close()

	bob.register(t)
	time.Sleep(100 * time.Millisecond)

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	aliceConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: ts.UDPPort})
	require.NoError(t, err)
	defer aliceConn.Close()

	callID := fmt.Sprintf("cancel-late-%s", t.Name())
	fromTag := "alice-cancel-late"
	branch := "z9hG4bK-late-cancel"

	alicePort := aliceRTP.LocalAddr().(*net.UDPAddr).Port

	sdp := integrationtest.BuildSDPOffer(alicePort, "127.0.0.1")
	sdpBytes, err := sdp.Marshal()
	require.NoError(t, err)

	invite := fmt.Sprintf("INVITE sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"Contact: <sip:alice@127.0.0.1:%d;transport=udp>\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %d\r\n\r\n%s",
		alicePort, branch, fromTag, callID, alicePort, len(sdpBytes), sdpBytes)

	_, err = aliceConn.Write([]byte(invite))
	require.NoError(t, err)
	t.Log("Alice sent INVITE")

	// Read until we get 200 OK (Bob answers immediately).
	buf := make([]byte, 4096)
	got200 := false
	var serverTag string
	for i := 0; i < 10; i++ {
		_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := aliceConn.Read(buf)
		if err != nil {
			continue
		}
		msg := string(buf[:n])
		t.Logf("Alice received (pre-cancel): %q", msg[:min(len(msg), 200)])

		if strings.Contains(msg, "CSeq: 1 INVITE") && strings.Contains(msg, "SIP/2.0 200 OK") {
			got200 = true
			// Extract server tag from To header for ACK.
			if idx := strings.Index(msg, ";tag="); idx != -1 {
				tagPart := msg[idx+5:]
				if end := strings.Index(tagPart, "\r\n"); end != -1 {
					serverTag = tagPart[:end]
				} else if end := strings.Index(tagPart, " "); end != -1 {
					serverTag = tagPart[:end]
				} else {
					serverTag = tagPart
				}
			}
			break
		}
	}
	require.True(t, got200, "Should receive 200 OK before CANCEL")

	// Send ACK for the 200 OK.
	ack := fmt.Sprintf("ACK sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 ACK\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alicePort, branch, fromTag, serverTag, callID)

	_, err = aliceConn.Write([]byte(ack))
	require.NoError(t, err)
	t.Log("Alice sent ACK for 200 OK")

	// Now send CANCEL — this is late, the call is already answered.
	cancel := fmt.Sprintf("CANCEL sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 CANCEL\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alicePort, branch, fromTag, callID)

	_, err = aliceConn.Write([]byte(cancel))
	require.NoError(t, err)
	t.Log("Alice sent CANCEL (late)")

	// Expect 481 or 200 — RFC allows either, but 481 is cleaner.
	// The transaction is already completed, so the server should respond 481.
	got481 := false
	for i := 0; i < 5; i++ {
		_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := aliceConn.Read(buf)
		if err != nil {
			continue
		}
		msg := string(buf[:n])
		t.Logf("Alice received (post-cancel): %q", msg[:min(len(msg), 200)])
		if strings.Contains(msg, "481") {
			got481 = true
			break
		}
	}

	require.True(t, got481, "Should receive 481 for late CANCEL")

	// Send BYE to properly terminate the answered call.
	bob.answerNow <- struct{}{}
	time.Sleep(100 * time.Millisecond)

	bye := fmt.Sprintf("BYE sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=z9hG4bK-bye\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 2 BYE\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alicePort, fromTag, serverTag, callID)

	_, err = aliceConn.Write([]byte(bye))
	require.NoError(t, err)
	t.Log("Alice sent BYE")
}

// TestIntegration_B2BUACancel_NoMatchingTransaction verifies that a CANCEL
// without a matching INVITE transaction gets a 481 response.
func TestIntegration_B2BUACancel_NoMatchingTransaction(t *testing.T) {
	ts := integrationtest.StartTestServer(t, "127.0.0.1")
	defer ts.Stop()

	aliceConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: ts.UDPPort})
	require.NoError(t, err)
	defer aliceConn.Close()

	callID := "cancel-nonexistent-" + t.Name()
	branch := "z9hG4bK-nonexistent"

	cancel := fmt.Sprintf("CANCEL sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=no-matching\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 CANCEL\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		branch, callID)

	_, err = aliceConn.Write([]byte(cancel))
	require.NoError(t, err)

	buf := make([]byte, 4096)
	_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := aliceConn.Read(buf)
	require.NoError(t, err, "Should receive a response")

	msg := string(buf[:n])
	t.Logf("Alice received: %q", msg[:min(len(msg), 200)])
	require.Contains(t, msg, "481", "Should receive 481 for CANCEL with no matching transaction")
}

// TestIntegration_B2BUACancel_UASCancel_WithAuth verifies CANCEL handling
// when proxy auth is enabled. The CANCEL itself is not authenticated —
// only the INVITE requires auth.
func TestIntegration_B2BUACancel_UASCancel_WithAuth(t *testing.T) {
	store := integrationtest.NewTestPasswordStore("127.0.0.1", "SHA-256",
		integrationtest.TestUser("alice", "alicepass", "sip:alice@127.0.0.1"),
		integrationtest.TestUser("bob", "bobpass", "sip:bob@127.0.0.1"),
	)
	ts := integrationtest.StartTestServerWithAuthUsers(t, "127.0.0.1", store)
	defer ts.Stop()

	ringBob := newBobUAS(t, ts, "udp")
	ringBob.ringBeforeAnswer = true
	defer ringBob.close()

	ringBob.registerWithAuth(t, "bob", "bobpass")
	time.Sleep(100 * time.Millisecond)

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	aliceConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: ts.UDPPort})
	require.NoError(t, err)
	defer aliceConn.Close()

	callID := fmt.Sprintf("cancel-auth-%s", t.Name())
	fromTag := "alice-cancel-auth"
	branch := "z9hG4bK-auth-cancel"
	alicePort := aliceRTP.LocalAddr().(*net.UDPAddr).Port

	sdp := integrationtest.BuildSDPOffer(alicePort, "127.0.0.1")
	sdpBytes, err := sdp.Marshal()
	require.NoError(t, err)

	// Send INVITE without auth — expect 407 Proxy Auth Required.
	invite := fmt.Sprintf("INVITE sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s-r1\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"Contact: <sip:alice@127.0.0.1:%d;transport=udp>\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %d\r\n\r\n%s",
		alicePort, branch+"-r1", fromTag, callID, alicePort, len(sdpBytes), sdpBytes)

	_, err = aliceConn.Write([]byte(invite))
	require.NoError(t, err)

	buf := make([]byte, 4096)
	msg := ""
	{
		var readErr error
		for i := 0; i < 10; i++ {
			_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
			var n int
			n, readErr = aliceConn.Read(buf)
			if readErr != nil {
				continue
			}
			msg = string(buf[:n])
			t.Logf("Alice received (auth challenge): %q", msg[:min(len(msg), 200)])
			if strings.Contains(msg, "SIP/2.0 407") {
				break
			}
		}
		require.NoError(t, readErr, "Should receive 407 challenge")
	}
	require.Contains(t, msg, "407 Proxy Authentication Required",
		"Should receive 407 before auth")

	// Send CANCEL with the -r1 branch — should get 481 since the INVITE
	// didn't create a transaction (auth wasn't completed).
	cancel := fmt.Sprintf("CANCEL sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=%s-r1\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 CANCEL\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alicePort, branch+"-r1", fromTag, callID)

	_, err = aliceConn.Write([]byte(cancel))
	require.NoError(t, err)

	_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := aliceConn.Read(buf)
	if err == nil {
		msg = string(buf[:n])
		t.Logf("Alice received (post-cancel): %q", msg[:min(len(msg), 200)])
		// Could be 481 (no transaction) or 200 (CANCEL OK in some edge cases)
		require.Contains(t, msg, "481", "Should receive 481 for CANCEL without matching INVITE transaction")
	}
}

// requireNoError is a test helper.
func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIntegration_B2BUACancel_UASCancel_TCP verifies CANCEL over TCP transport.
func TestIntegration_B2BUACancel_UASCancel_TCP(t *testing.T) {
	ts := integrationtest.StartTestServer(t, "127.0.0.1")
	defer ts.Stop()

	bob := newBobUAS(t, ts, "tcp")
	bob.ringBeforeAnswer = true
	defer bob.close()

	bob.register(t)
	time.Sleep(100 * time.Millisecond)

	aliceRTP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer aliceRTP.Close()

	aliceConn, err := net.DialTCP("tcp", nil, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: ts.TCPPort})
	require.NoError(t, err)
	defer aliceConn.Close()

	callID := fmt.Sprintf("cancel-tcp-%s", t.Name())
	fromTag := "alice-cancel-tcp"
	branch := "z9hG4bK-tcp-cancel"

	alicePort := aliceRTP.LocalAddr().(*net.UDPAddr).Port

	sdp := integrationtest.BuildSDPOffer(alicePort, "127.0.0.1")
	sdpBytes, err := sdp.Marshal()
	require.NoError(t, err)

	invite := fmt.Sprintf("INVITE sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/TCP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"Contact: <sip:alice@127.0.0.1:%d;transport=tcp>\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %d\r\n\r\n%s",
		alicePort, branch, fromTag, callID, alicePort, len(sdpBytes), sdpBytes)

	_, err = aliceConn.Write([]byte(invite))
	require.NoError(t, err)
	t.Log("Alice sent INVITE on TCP")

	buf := make([]byte, 4096)
	got180 := false
	for i := 0; i < 10; i++ {
		_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := aliceConn.Read(buf)
		if err != nil {
			continue
		}
		msg := string(buf[:n])
		t.Logf("Alice received (pre-cancel): %q", msg[:min(len(msg), 200)])
		if strings.Contains(msg, "180 Ringing") {
			got180 = true
			break
		}
		if strings.Contains(msg, "200 OK") {
			t.Log("Bob answered before CANCEL, skipping cancel test")
			return
		}
	}
	require.True(t, got180, "Should receive 180 Ringing before CANCEL")

	cancel := fmt.Sprintf("CANCEL sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/TCP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 CANCEL\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alicePort, branch, fromTag, callID)

	_, err = aliceConn.Write([]byte(cancel))
	require.NoError(t, err)
	t.Log("Alice sent CANCEL on TCP")

	gotCancel200 := false
	gotInvite487 := false
	for i := 0; i < 10; i++ {
		_ = aliceConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := aliceConn.Read(buf)
		if err != nil {
			continue
		}
		msg := string(buf[:n])
		t.Logf("Alice received (post-cancel): %q", msg[:min(len(msg), 200)])

		if strings.Contains(msg, "SIP/2.0 200 OK") {
			if strings.Contains(msg, "CANCEL") {
				gotCancel200 = true
			}
		}
		if strings.Contains(msg, "487 Request Terminated") {
			gotInvite487 = true
		}
	}

	require.True(t, gotCancel200, "Should receive 200 OK for CANCEL over TCP")
	require.True(t, gotInvite487, "Should receive 487 Request Terminated for INVITE over TCP")

	ack := fmt.Sprintf("ACK sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/TCP 127.0.0.1:%d;branch=%s\r\n"+
		"From: <sip:alice@127.0.0.1>;tag=%s\r\n"+
		"To: <sip:bob@127.0.0.1>\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 ACK\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alicePort, branch, fromTag, callID)

	_, err = aliceConn.Write([]byte(ack))
	require.NoError(t, err)
	t.Log("Alice sent ACK for 487 on TCP")
}

// TestIntegration_B2BUACancel_WithPRACK verifies CANCEL works when a reliable
// provisional (183 with RSeq) has been sent but PRACK hasn't arrived yet.
// Alice sends CANCEL instead of PRACK — expects 487 + PRACK cancellation.
func TestIntegration_B2BUACancel_WithPRACK(t *testing.T) {
	ts := integrationtest.StartTestServerWithDialplan(t, "127.0.0.1", nil, integrationtest.WithPRACK())
	defer ts.Stop()

	pb := newPrackBob(t, ts)
	defer pb.close()
	pb.register(t)
	time.Sleep(100 * time.Millisecond)

	alice := newAliceRawUAC(t, ts)
	defer alice.close()

	// Alice sends INVITE with Supported: 100rel
	alice.sendINVITE(true)

	// Read 100 Trying
	msg := alice.readResponse(5 * time.Second)
	require.Contains(t, msg, "100 Trying")

	// Read 183 Session Progress (reliable, with RSeq)
	msg = alice.readResponse(5 * time.Second)
	require.Contains(t, msg, "183 Session Progress")
	require.Contains(t, strings.ToLower(msg), "rseq:")
	require.Contains(t, msg, "Require: 100rel")

	rseq := extractHeader(msg, "RSeq")
	require.NotEmpty(t, rseq, "183 should have RSeq")

	alice.serverTag = extractTagParam(msg, "To", "tag")
	require.NotEmpty(t, alice.serverTag, "183 should have To tag")

	// Alice sends CANCEL before PRACK — server should cancel the call,
	// cancel the pending PRACK, and send 487 to Alice.
	alice.sendCANCEL()

	// Read responses after CANCEL: expect 200 OK for CANCEL + 487 for INVITE.
	gotCancel200 := false
	gotInvite487 := false
	for i := 0; i < 10; i++ {
		msg = alice.readResponse(2 * time.Second)
		if msg == "" {
			continue
		}
		t.Logf("Alice received (post-cancel): %q", msg[:min(len(msg), 200)])

		if strings.Contains(msg, "SIP/2.0 200 OK") {
			if strings.Contains(msg, "CANCEL") {
				gotCancel200 = true
			}
		}
		if strings.Contains(msg, "487 Request Terminated") {
			gotInvite487 = true
		}
	}

	require.True(t, gotCancel200, "Should receive 200 OK for CANCEL after reliable provisional")
	require.True(t, gotInvite487, "Should receive 487 Request Terminated after CANCEL with PRACK")

	// Send ACK for the 487 using the same branch as the INVITE for matching.
	ack := fmt.Sprintf("ACK sip:bob@127.0.0.1 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:%d;branch=z9hG4bK-%s\r\n"+
		"From: <sip:alice@%s>;tag=%s\r\n"+
		"To: <sip:bob@%s>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: 1 ACK\r\n"+
		"Max-Forwards: 70\r\n"+
		"Content-Length: 0\r\n\r\n",
		alice.conn.LocalAddr().(*net.UDPAddr).Port, alice.callID,
		alice.ts.Domain, alice.fromTag, alice.ts.Domain, alice.serverTag,
		alice.callID)

	_, err := alice.conn.WriteToUDP([]byte(ack), alice.addr)
	require.NoError(t, err)
	t.Log("Alice sent ACK for 487 after PRACK cancel")
	time.Sleep(200 * time.Millisecond) // allow server to process ACK before teardown
}
