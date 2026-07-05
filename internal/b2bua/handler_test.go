package b2bua

import (
	"strings"
	"testing"

	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

type mockB2BUATx struct {
	responses []*proto.SIPMessage
}

func (m *mockB2BUATx) Respond(res *proto.SIPMessage) {
	m.responses = append(m.responses, res)
}

func (m *mockB2BUATx) Target() sip.Target       { return sip.Target{} }
func (m *mockB2BUATx) Transport() sip.Transport { return nil }

func cancelRequest(t *testing.T, toWithTag bool) *proto.SIPMessage {
	t.Helper()
	to := "<sip:bob@localhost>"
	if toWithTag {
		to = "<sip:bob@localhost>;tag=bob-tag"
	}
	raw := "CANCEL sip:bob@localhost SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:9999;branch=z9hG4bKcancel-test\r\n" +
		"From: <sip:alice@localhost>;tag=alice\r\n" +
		"To: " + to + "\r\n" +
		"Call-ID: cancel-test-call-id\r\n" +
		"CSeq: 1 CANCEL\r\n" +
		"Content-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		t.Fatalf("UnmarshalSIPDatagram: %v", err)
	}
	return msg
}

func TestHandleCancel_Sends487WhenNoEarlyCall(t *testing.T) {
	h := NewHandler(Config{})

	tx := &mockB2BUATx{}
	h.HandleCancel(t.Context(), cancelRequest(t, false), tx)

	if len(tx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(tx.responses))
	}

	res := tx.responses[0]
	if res.StatusCode() != 487 {
		t.Fatalf("expected 487, got %d", res.StatusCode())
	}
	if res.CSeq.Method != proto.SIPMethodINVITE {
		t.Fatalf("expected CSeq method INVITE, got %s", res.CSeq.Method)
	}

	to := res.Headers.GetFirst("To")
	if !strings.Contains(to, "tag=") {
		t.Fatalf("expected To header to contain a tag, got %s", to)
	}
}

func TestHandleCancel_PreservesExistingToTag(t *testing.T) {
	h := NewHandler(Config{})

	tx := &mockB2BUATx{}
	h.HandleCancel(t.Context(), cancelRequest(t, true), tx)

	if len(tx.responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(tx.responses))
	}

	to := tx.responses[0].Headers.GetFirst("To")
	if !strings.Contains(to, "tag=bob-tag") {
		t.Fatalf("expected existing To tag to be preserved, got %s", to)
	}
}
