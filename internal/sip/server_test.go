package sip

import (
	"net"
	"testing"
	"time"

	"gitub.com/thorsager/trec/proto"
)

func TestServerOPTIONSOverUDP(t *testing.T) {
	server, err := NewServer("127.0.0.1:15060")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	handlerCalled := make(chan struct{})
	server.On(proto.SIPMethodOPTIONS, func(req *proto.SIPMessage, tx Transaction) {
		trying := proto.NewResponse(req, 100, "Trying")
		tx.Respond(trying)

		res := proto.NewResponse(req, 200, "OK")
		res.Headers["Allow"] = []string{"INVITE, ACK, BYE, CANCEL, OPTIONS, REGISTER"}
		res.Headers["Accept"] = []string{"application/sdp"}
		tx.Respond(res)
		close(handlerCalled)
	})

	server.Start()
	defer server.Close()

	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer clientConn.Close()

	req := "OPTIONS sip:server SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKtest-branch\r\n" +
		"From: <sip:test@localhost>;tag=test-tag\r\n" +
		"To: <sip:server@localhost>\r\n" +
		"Call-ID: test-options-call-id\r\n" +
		"CSeq: 1 OPTIONS\r\n" +
		"Content-Length: 0\r\n\r\n"

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15060")
	_, err = clientConn.WriteToUDP([]byte(req), serverAddr)
	if err != nil {
		t.Fatalf("WriteToUDP: %v", err)
	}

	// Read both responses: 100 Trying and 200 OK.
	var gotOK bool
	clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 2; i++ {
		buf := make([]byte, 4096)
		n, _, err := clientConn.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("ReadFromUDP: %v", err)
		}
		msg, err := proto.UnmarshalSIPDatagram(buf[:n])
		if err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if msg.StatusCode() == 200 {
			gotOK = true
			allow := msg.Headers.GetFirst("Allow")
			if allow == "" {
				t.Fatal("missing Allow header in 200 OK")
			}
		}
	}
	if !gotOK {
		t.Fatal("never received 200 OK")
	}

	select {
	case <-handlerCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("handler was not called")
	}
}

func TestUnsupportedMethod(t *testing.T) {
	server, err := NewServer("127.0.0.1:15061")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server.Start()
	defer server.Close()

	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer clientConn.Close()

	req := "BYE sip:server SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:9998;branch=z9hG4bKbye-branch\r\n" +
		"From: <sip:alice@localhost>;tag=alice\r\n" +
		"To: <sip:bob@localhost>\r\n" +
		"Call-ID: bye-test-call-id\r\n" +
		"CSeq: 1 BYE\r\n" +
		"Content-Length: 0\r\n\r\n"

	serverAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:15061")
	clientConn.WriteToUDP([]byte(req), serverAddr)

	clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := clientConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}

	msg, err := proto.UnmarshalSIPDatagram(buf[:n])
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if msg.StatusCode() != 501 {
		t.Fatalf("expected 501 Not Implemented, got %d", msg.StatusCode())
	}
}
