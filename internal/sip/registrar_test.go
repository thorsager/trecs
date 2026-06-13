package sip

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thorsager/trecs/proto"
)

type mockTx struct {
	conn      net.Conn
	responses []*proto.SIPMessage
}

func (tx *mockTx) Respond(res *proto.SIPMessage) {
	tx.responses = append(tx.responses, res)
}

func (tx *mockTx) Target() Target {
	if tx.conn != nil {
		return Target{Conn: tx.conn, Addr: tx.conn.RemoteAddr()}
	}
	return Target{}
}

func (tx *mockTx) Transport() Transport {
	return nil
}

func (tx *mockTx) last() *proto.SIPMessage {
	if len(tx.responses) == 0 {
		return nil
	}
	return tx.responses[len(tx.responses)-1]
}

func sipMessage(raw string) *proto.SIPMessage {
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		panic(err)
	}
	return msg
}

func TestRegistrar_RegisterSingleContact(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK, got %v", statusOrNil(res))
	}

	contacts := res.Headers.Get("Contact")
	if len(contacts) != 1 {
		t.Fatalf("expected 1 Contact, got %d", len(contacts))
	}
	if !strings.Contains(contacts[0], "sip:alice@192.168.1.5") {
		t.Fatalf("expected Contact with sip:alice@192.168.1.5, got %q", contacts[0])
	}
	if !strings.Contains(contacts[0], "expires=3600") {
		t.Fatalf("expected expires=3600 in Contact, got %q", contacts[0])
	}

	if res.Headers.GetFirst("Date") == "" {
		t.Fatal("missing Date header")
	}
}

func TestRegistrar_QueryBindings(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	query := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKquery\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 2 REGISTER\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), query, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK, got %v", statusOrNil(res))
	}

	contacts := res.Headers.Get("Contact")
	if len(contacts) != 1 {
		t.Fatalf("expected 1 Contact in query response, got %d: %v", len(contacts), contacts)
	}
	if !strings.Contains(contacts[0], "sip:alice@192.168.1.5") {
		t.Fatalf("expected Contact with sip:alice@192.168.1.5, got %q", contacts[0])
	}
}

func TestRegistrar_Unregister(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKreg\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	tx := &mockTx{}
	unreg := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKunreg\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 2 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=0\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), unreg, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK, got %v", statusOrNil(res))
	}

	contacts := res.Headers.Get("Contact")
	if len(contacts) != 0 {
		t.Fatalf("expected 0 Contacts after unregister, got %d: %v", len(contacts), contacts)
	}
}

func TestRegistrar_UnregisterAll(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKreg2\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 2 REGISTER\r\n"+
		"Contact: <sip:alice@10.0.0.1>\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	tx := &mockTx{}
	unreg := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKstar\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 3 REGISTER\r\n" +
		"Contact: *\r\n" +
		"Expires: 0\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), unreg, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK, got %v", statusOrNil(res))
	}
	if len(res.Headers.Get("Contact")) != 0 {
		t.Fatalf("expected no Contacts after star unregister, got %v", res.Headers.Get("Contact"))
	}
}

func TestRegistrar_StarWithoutExpiresZero(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: *\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	if tx.last().StatusCode() != proto.SIPStatusBadRequest {
		t.Fatalf("expected 400 for star without Expires: 0, got %d", tx.last().StatusCode())
	}
}

func TestRegistrar_CSeqMonotonic(t *testing.T) {
	reg := NewRegistrar()

	tx1 := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKone\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), tx1)

	if tx1.last().StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 for first REGISTER, got %d", tx1.last().StatusCode())
	}

	tx2 := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtwo\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), tx2)

	if tx2.last().StatusCode() != proto.SIPStatusBadRequest {
		t.Fatalf("expected 400 for repeated CSeq, got %d", tx2.last().StatusCode())
	}
}

func TestRegistrar_DefaultExpiry(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	if !strings.Contains(res.Headers.GetFirst("Contact"), "expires=3600") {
		t.Fatalf("expected default expires=3600, got %q", res.Headers.GetFirst("Contact"))
	}
}

func TestRegistrar_MultipleContacts(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Contact: <sip:alice@10.0.0.1>;expires=1800\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contacts := res.Headers.Get("Contact")
	if len(contacts) != 2 {
		t.Fatalf("expected 2 Contacts, got %d: %v", len(contacts), contacts)
	}

	exp := res.Headers.GetFirst("Expires")
	if exp != "1800" {
		t.Fatalf("expected Expires=1800 (minimum), got %s", exp)
	}
}

func TestRegistrar_RefreshBinding(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKreg\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	tx := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKrefresh\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 2 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=7200\r\n"+
		"Content-Length: 0\r\n\r\n"), tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	if !strings.Contains(res.Headers.GetFirst("Contact"), "expires=7200") {
		t.Fatalf("expected expires=7200 in refresh, got %q", res.Headers.GetFirst("Contact"))
	}
}

func TestRegistrar_BadRequestURIMismatch(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	// Per RFC 3261 §10.2 the Request-URI is the registrar domain (no user)
	// and MUST match the host portion of the To AOR. Different domains = 400.
	req := sipMessage("REGISTER sip:different.example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	if tx.last().StatusCode() != proto.SIPStatusBadRequest {
		t.Fatalf("expected 400 for Request-URI domain mismatch, got %d", tx.last().StatusCode())
	}
}

// RFC 3261 §10.2: The Request-URI may include a port; the host portion must
// still match the To header AOR. This covers the case where a UA registers
// against "sip:host:port" (common when using a non-default registrar port).
func TestRegistrar_RequestURIWithPort(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:127.0.0.1:5063 SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:caller@127.0.0.1>;tag=abc\r\n" +
		"To: <sip:caller@127.0.0.1>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:caller@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	if tx.last().StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 for Request-URI with port matching To host, got %d", tx.last().StatusCode())
	}
}

// RFC 3261 §10.2: The Request-URI is the domain of the registrar (no user).
// A request with a domain-only Request-URI and a full AOR in To is valid.
func TestRegistrar_RequestURIDomainOnly(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	if tx.last().StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 for domain-only Request-URI, got %d", tx.last().StatusCode())
	}
}

func TestRegistrar_MissingCallID(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	if tx.last().StatusCode() != proto.SIPStatusBadRequest {
		t.Fatalf("expected 400 for missing Call-ID, got %d", tx.last().StatusCode())
	}
}

func TestRegistrar_Sweep(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKreg\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=1\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	time.Sleep(1100 * time.Millisecond)

	reg.sweep()

	tx := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKquery\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-2\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Content-Length: 0\r\n\r\n"), tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	if len(res.Headers.Get("Contact")) != 0 {
		t.Fatalf("expected 0 Contacts after sweep, got %d: %v", len(res.Headers.Get("Contact")), res.Headers.Get("Contact"))
	}
}

// ---------------------------------------------------------------------------
// RFC 3261 §10 compliance tests
// ---------------------------------------------------------------------------

// RFC 3261 §10.3: Global Expires header is used as default when a Contact
// has no per-contact expires parameter.
func TestRegistrar_GlobalExpiresHeader(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>\r\n" +
		"Expires: 1800\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	if !strings.Contains(res.Headers.GetFirst("Contact"), "expires=1800") {
		t.Fatalf("expected expires=1800 from global Expires header, got %q",
			res.Headers.GetFirst("Contact"))
	}
	if got := res.Headers.GetFirst("Expires"); got != "1800" {
		t.Fatalf("expected Expires header = 1800, got %q", got)
	}
}

// RFC 3261 §10.3: Per-contact expires parameter overrides the global Expires
// header for that specific Contact.
func TestRegistrar_PerContactOverridesGlobal(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Expires: 7200\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	if !strings.Contains(res.Headers.GetFirst("Contact"), "expires=3600") {
		t.Fatalf("expected per-contact expires=3600, got %q",
			res.Headers.GetFirst("Contact"))
	}
}

// RFC 3261 §10.2: Comma-separated contacts in a single Contact header line
// are each processed independently.
func TestRegistrar_CommaSeparatedContacts(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600, <sip:alice@10.0.0.1>;expires=1800\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contacts := res.Headers.Get("Contact")
	if len(contacts) != 2 {
		t.Fatalf("expected 2 Contacts from comma-separated line, got %d: %v",
			len(contacts), contacts)
	}
}

// RFC 3261 §10.2: Contact headers in addr-spec form (without angle brackets)
// are accepted per RFC 3261 §20.10 grammar.
func TestRegistrar_AddrSpecContact(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: sip:alice@192.168.1.5;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contacts := res.Headers.Get("Contact")
	if len(contacts) != 1 {
		t.Fatalf("expected 1 Contact, got %d: %v", len(contacts), contacts)
	}
	if !strings.Contains(contacts[0], "sip:alice@192.168.1.5") {
		t.Fatalf("expected Contact with sip:alice@192.168.1.5, got %q", contacts[0])
	}
	if !strings.Contains(contacts[0], "expires=3600") {
		t.Fatalf("expected expires=3600 in Contact, got %q", contacts[0])
	}
}

// RFC 3261 §10: URI parameters in Contact URIs (e.g. ;lr, ;transport=tcp)
// are part of the SIP URI and MUST be preserved in the binding and response.
func TestRegistrar_ContactURIParamsPreserved(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5;lr;transport=tcp>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contact := res.Headers.GetFirst("Contact")
	if !strings.Contains(contact, ";lr") {
		t.Fatalf("expected URI param ';lr' preserved in Contact, got %q", contact)
	}
	if !strings.Contains(contact, ";transport=tcp") {
		t.Fatalf("expected URI param ';transport=tcp' preserved in Contact, got %q", contact)
	}
}

// RFC 3261 §10.3: Unregistering a non-existent contact is a no-op — returns
// 200 OK with the remaining (unchanged) bindings.
func TestRegistrar_UnregisterNonExistent(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKreg\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	tx := &mockTx{}
	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKunreg\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 2 REGISTER\r\n" +
		"Contact: <sip:alice@10.0.0.1>;expires=0\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contacts := res.Headers.Get("Contact")
	if len(contacts) != 1 {
		t.Fatalf("expected 1 remaining Contact (non-existent unregister is no-op), got %d: %v",
			len(contacts), contacts)
	}
}

// RFC 3261 §10.3: CSeq tracking is per Call-ID. Two different Call-IDs for
// the same AOR maintain independent CSeq counters.
func TestRegistrar_IndependentCSeqPerCallID(t *testing.T) {
	reg := NewRegistrar()

	// Register via Call-ID "call-a" with CSeq 1
	tx1 := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKa\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-a\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), tx1)
	if tx1.last().StatusCode() != proto.SIPStatusOK {
		t.Fatalf("call-a CSeq 1: expected 200, got %d", tx1.last().StatusCode())
	}

	// Register via Call-ID "call-b" with CSeq 1 — should succeed (different
	// Call-ID, independent counter).
	tx2 := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKb\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-b\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@10.0.0.1>;expires=7200\r\n"+
		"Content-Length: 0\r\n\r\n"), tx2)
	if tx2.last().StatusCode() != proto.SIPStatusOK {
		t.Fatalf("call-b CSeq 1: expected 200, got %d", tx2.last().StatusCode())
	}

	// Both bindings should exist
	tx3 := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKq\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-c\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Content-Length: 0\r\n\r\n"), tx3)
	if tx3.last().StatusCode() != proto.SIPStatusOK {
		t.Fatalf("query: expected 200, got %d", tx3.last().StatusCode())
	}
	if len(tx3.last().Headers.Get("Contact")) != 2 {
		t.Fatalf("expected 2 Contacts from two Call-IDs, got %d: %v",
			len(tx3.last().Headers.Get("Contact")), tx3.last().Headers.Get("Contact"))
	}

	// call-a CSeq 1 again (not greater) ← the transaction layer normally
	// suppresses retransmissions, but the registrar's own CSeq check MUST
	// also reject it.
	tx4 := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKa2\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-a\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), tx4)
	if tx4.last().StatusCode() != proto.SIPStatusBadRequest {
		t.Fatalf("call-a CSeq 1 replay: expected 400, got %d", tx4.last().StatusCode())
	}

	// call-a CSeq 5 (already skipped 2,3,4; should work as long as > 1)
	tx5 := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKa3\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-a\r\n"+
		"CSeq: 5 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), tx5)
	if tx5.last().StatusCode() != proto.SIPStatusOK {
		t.Fatalf("call-a CSeq 5: expected 200, got %d", tx5.last().StatusCode())
	}
}

// RFC 3261 §10.3: The response SHOULD include a Date header in RFC 1123
// format (e.g. "Mon, 02 Jan 2006 15:04:05 GMT").
func TestRegistrar_ResponseDateIsRFC1123(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	date := res.Headers.GetFirst("Date")
	if date == "" {
		t.Fatal("missing Date header")
	}
	_, err := time.Parse(time.RFC1123, date)
	if err != nil {
		t.Fatalf("Date header %q is not valid RFC 1123: %v", date, err)
	}
}

// RFC 3261 §8.2.6 (general response construction) + §10.3: The response's
// Via header MUST match the request's Via header (branch and transport
// protocol are echoed back).
func TestRegistrar_ResponseViaEchoRequest(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	const via = "SIP/2.0/UDP 10.0.0.1:5060;branch=z9hG4bKmybranch"
	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: " + via + "\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if got := res.Headers.GetFirst("Via"); got != via {
		t.Fatalf("Via header: got %q, want %q", got, via)
	}
}

// RFC 3261 §8.2.6 + §10.3: The response's CSeq MUST match the request's
// CSeq (same sequence number and method).
func TestRegistrar_ResponseCSeqMatchesRequest(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 42 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.CSeq.Seq != 42 || res.CSeq.Method != proto.SIPMethodREGISTER {
		t.Fatalf("CSeq: got %d %s, want 42 REGISTER", res.CSeq.Seq, res.CSeq.Method)
	}
}

// RFC 3261 §20.28 + §10.3: When multiple Contacts are in the response, the
// Expires header value MUST be the minimum of all per-Contact expires
// values.
func TestRegistrar_MinimumExpiresInResponse(t *testing.T) {
	reg := NewRegistrar()

	// Register two contacts with different expires values.
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK1\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Contact: <sip:alice@10.0.0.1>;expires=7200\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	tx := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKq\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-2\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Content-Length: 0\r\n\r\n"), tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contacts := res.Headers.Get("Contact")
	if len(contacts) != 2 {
		t.Fatalf("expected 2 Contacts, got %d", len(contacts))
	}
	exp := res.Headers.GetFirst("Expires")
	if exp == "" {
		t.Fatal("missing Expires header in multi-contact query response")
	}
	secs, _ := strconv.Atoi(exp)
	if secs < 3595 || secs > 3605 {
		t.Fatalf("expected Expires ~3600 (minimum of 3600,7200), got %d", secs)
	}
}

// RFC 3261 §10.3: Expires: 0 with no Contact and no star is a query, not
// an unregister-all. The bindings are returned.
func TestRegistrar_ExpiresZeroWithoutStarIsQuery(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKreg\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	tx := &mockTx{}
	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKq\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-2\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Expires: 0\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contacts := res.Headers.Get("Contact")
	if len(contacts) != 1 {
		t.Fatalf("expected 1 Contact (query with Expires: 0 without star), got %d: %v",
			len(contacts), contacts)
	}
}

// RFC 3261 §10.3: After removing one of multiple bindings (expires=0), the
// response Contact count reflects only the remaining active bindings.
func TestRegistrar_PartialUnregisterPreservesOthers(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK1\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Contact: <sip:alice@10.0.0.1>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	tx := &mockTx{}
	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK2\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 2 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=0\r\n"+
		"Content-Length: 0\r\n\r\n"), tx)

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contacts := res.Headers.Get("Contact")
	if len(contacts) != 1 {
		t.Fatalf("expected 1 remaining Contact after partial unregister, got %d: %v",
			len(contacts), contacts)
	}
	if !strings.Contains(contacts[0], "sip:alice@10.0.0.1") {
		t.Fatalf("expected remaining Contact to be sip:alice@10.0.0.1, got %q", contacts[0])
	}
}

// --- RFC 5626 Outbound Tests ---

func TestRegistrar_GetBindings(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].ContactURI != "sip:alice@192.168.1.5" {
		t.Fatalf("expected ContactURI sip:alice@192.168.1.5, got %q", bindings[0].ContactURI)
	}
}

func TestRegistrar_GetBindingsNonexistentAOR(t *testing.T) {
	reg := NewRegistrar()
	bindings := reg.GetBindings("sip:nobody@example.com")
	if len(bindings) != 0 {
		t.Fatalf("expected 0 bindings, got %d", len(bindings))
	}
}

func TestRegistrar_GetBindingsCopySemantics(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}

	bindings[0].ContactURI = "sip:alice@modified"

	original := reg.GetBindings("sip:alice@example.com")
	if original[0].ContactURI == "sip:alice@modified" {
		t.Fatal("GetBindings must return a copy; modifying the result should not affect internal state")
	}
}

func TestRegistrar_ContactWithOB(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5;transport=tcp>;ob;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if !bindings[0].OB {
		t.Fatal("expected OB=true on binding")
	}
	if bindings[0].RegID != -1 {
		t.Fatalf("expected RegID=-1 (not specified), got %d", bindings[0].RegID)
	}

	res := tx.last()
	if res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode())
	}
	contact := res.Headers.GetFirst("Contact")
	if !strings.Contains(contact, ";ob") {
		t.Fatalf("expected ;ob in response Contact, got %q", contact)
	}
}

func TestRegistrar_ContactWithOBAndRegID(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5;transport=tcp>;ob;reg-id=2;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if !bindings[0].OB {
		t.Fatal("expected OB=true on binding")
	}
	if bindings[0].RegID != 2 {
		t.Fatalf("expected RegID=2, got %d", bindings[0].RegID)
	}

	res := tx.last()
	contact := res.Headers.GetFirst("Contact")
	if !strings.Contains(contact, ";ob") {
		t.Fatalf("expected ;ob in response Contact, got %q", contact)
	}
	if !strings.Contains(contact, ";reg-id=2") {
		t.Fatalf("expected ;reg-id=2 in response Contact, got %q", contact)
	}
}

func TestRegistrar_RemoveBindingsByFlowID(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK1\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:bob@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK2\r\n"+
		"From: <sip:bob@example.com>;tag=def\r\n"+
		"To: <sip:bob@example.com>\r\n"+
		"Call-ID: call-2\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:bob@10.0.0.1>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	if len(reg.GetBindings("sip:alice@example.com")) != 1 {
		t.Fatal("expected 1 binding for alice")
	}
	if len(reg.GetBindings("sip:bob@example.com")) != 1 {
		t.Fatal("expected 1 binding for bob")
	}

	reg.RemoveBindingsByFlowID("some-flow-id")

	if len(reg.GetBindings("sip:alice@example.com")) != 1 {
		t.Fatal("alice binding should remain after unrelated flow removal")
	}
	if len(reg.GetBindings("sip:bob@example.com")) != 1 {
		t.Fatal("bob binding should remain after unrelated flow removal")
	}
}

func TestRegistrar_FlowIDSetOnTCPRegistration(t *testing.T) {
	reg := NewRegistrar()
	mockConn := &mockTCPAddrConn{
		local:  &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5060},
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.5"), Port: 54321},
	}
	tx := &mockTx{conn: mockConn}

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5;transport=tcp>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), tx)

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].FlowID == "" {
		t.Fatal("expected non-empty FlowID for TCP registration")
	}
	t.Logf("FlowID: %s", bindings[0].FlowID)
}

func TestRegistrar_FlowIDEmptyForUDP(t *testing.T) {
	reg := NewRegistrar()

	reg.HandleRegister(t.Context(), sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n"+
		"From: <sip:alice@example.com>;tag=abc\r\n"+
		"To: <sip:alice@example.com>\r\n"+
		"Call-ID: call-1\r\n"+
		"CSeq: 1 REGISTER\r\n"+
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n"+
		"Content-Length: 0\r\n\r\n"), &mockTx{})

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].FlowID != "" {
		t.Fatalf("expected empty FlowID for UDP registration, got %q", bindings[0].FlowID)
	}
}

func TestRegistrar_OBOnlyWithAngleBracket(t *testing.T) {
	// RFC 5626 §4: ob and reg-id MUST use name-addr form.
	// Address-spec form should not parse ob/reg-id.
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: sip:alice@192.168.1.5;ob;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].OB {
		t.Fatal("OB should not be parsed from addr-spec form (RFC 5626 §4)")
	}
}

func TestRegistrar_FlowTimer_TCP(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Supported: outbound\r\n" +
		"Contact: <sip:alice@192.168.1.5;transport=tcp>;ob;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK, got %v", statusOrNil(res))
	}
	if res.Headers.GetFirst("Require") != "outbound" {
		t.Fatalf("expected Require: outbound, got %q", res.Headers.GetFirst("Require"))
	}
	if res.Headers.GetFirst("Flow-Timer") != "120" {
		t.Fatalf("expected Flow-Timer: 120, got %q", res.Headers.GetFirst("Flow-Timer"))
	}
}

func TestRegistrar_FlowTimer_UDP(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Supported: outbound\r\n" +
		"Contact: <sip:alice@192.168.1.5;transport=tcp>;ob;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK, got %v", statusOrNil(res))
	}
	if res.Headers.GetFirst("Require") != "outbound" {
		t.Fatalf("expected Require: outbound, got %q", res.Headers.GetFirst("Require"))
	}
	if res.Headers.GetFirst("Flow-Timer") != "" {
		t.Fatalf("expected no Flow-Timer for UDP, got %q", res.Headers.GetFirst("Flow-Timer"))
	}
}

func TestRegistrar_FlowTimer_NoSupportedOutbound(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5;transport=tcp>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK, got %v", statusOrNil(res))
	}
	if res.Headers.GetFirst("Require") == "outbound" {
		t.Fatal("expected no Require: outbound when Supported: outbound not present")
	}
	if res.Headers.GetFirst("Flow-Timer") != "" {
		t.Fatalf("expected no Flow-Timer when outbound not negotiated, got %q", res.Headers.GetFirst("Flow-Timer"))
	}
}

// ── Auth test helpers ──

type testUserEntry struct {
	ha1  string
	aors []string
}

type testPasswordStore struct {
	realm     string
	algorithm string
	users     map[string]testUserEntry
}

func (s *testPasswordStore) Realm() string               { return s.realm }
func (s *testPasswordStore) Algorithm() string            { return s.algorithm }
func (s *testPasswordStore) HA1(username string) (string, bool) {
	u, ok := s.users[username]
	if !ok {
		return "", false
	}
	return u.ha1, true
}
func (s *testPasswordStore) AORs(username string) ([]string, bool) {
	u, ok := s.users[username]
	if !ok {
		return nil, false
	}
	return u.aors, true
}

func newTestAuthStore(aliceSecret string) *testPasswordStore {
	return &testPasswordStore{
		realm:     "example.com",
		algorithm: "SHA-256",
		users: map[string]testUserEntry{
			"alice": {
				ha1:  ComputeHA1("alice", "example.com", aliceSecret, "SHA-256"),
				aors: []string{"sip:alice@example.com"},
			},
		},
	}
}

func buildAuthHeader(nonce, username, ha1, realm, algorithm, cnonce, uri string, nc uint64) string {
	resp := ComputeDigestResponse(ha1, nonce, "00000001", cnonce, "auth", "REGISTER", uri, algorithm)
	return "Digest username=\"" + username + "\", realm=\"" + realm +
		"\", nonce=\"" + nonce +
		"\", uri=\"" + uri +
		"\", response=\"" + resp +
		"\", algorithm=" + algorithm +
		", cnonce=\"" + cnonce +
		"\", qop=auth, nc=00000001"
}

// ── Auth tests ──

func TestRegistrar_Auth_NoAuthHeader(t *testing.T) {
	reg := NewRegistrar()
	store := newTestAuthStore("secret")
	reg.SetPasswordStore(store)
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %v", statusOrNil(res))
	}
	wwwAuth := res.Headers.GetFirst("WWW-Authenticate")
	if wwwAuth == "" {
		t.Fatal("missing WWW-Authenticate header in 401")
	}
	if !strings.Contains(wwwAuth, "realm=\"example.com\"") {
		t.Fatalf("WWW-Authenticate missing realm: %q", wwwAuth)
	}
	if !strings.Contains(wwwAuth, "nonce=") {
		t.Fatalf("WWW-Authenticate missing nonce: %q", wwwAuth)
	}
	if !strings.Contains(wwwAuth, "algorithm=SHA-256") {
		t.Fatalf("WWW-Authenticate missing algorithm: %q", wwwAuth)
	}
	if !strings.Contains(wwwAuth, "qop=\"auth\"") {
		t.Fatalf("WWW-Authenticate missing qop: %q", wwwAuth)
	}
}

func TestRegistrar_Auth_ValidCredentials(t *testing.T) {
	reg := NewRegistrar()
	store := newTestAuthStore("secret")
	reg.SetPasswordStore(store)
	tx := &mockTx{}

	nonce := reg.nonces.NewNonce()
	cnonce := "test-cnonce"
	authHeader := buildAuthHeader(nonce, "alice", store.users["alice"].ha1, "example.com", "SHA-256", cnonce, "sip:alice@example.com", 1)

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Authorization: " + authHeader + "\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK, got %v", statusOrNil(res))
	}

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].ContactURI != "sip:alice@192.168.1.5" {
		t.Fatalf("expected contact sip:alice@192.168.1.5, got %q", bindings[0].ContactURI)
	}
}

func TestRegistrar_Auth_WrongPassword(t *testing.T) {
	reg := NewRegistrar()
	store := newTestAuthStore("secret")
	reg.SetPasswordStore(store)
	tx := &mockTx{}

	nonce := reg.nonces.NewNonce()
	wrongHa1 := ComputeHA1("alice", "example.com", "wrong", "SHA-256")
	cnonce := "test-cnonce"
	authHeader := buildAuthHeader(nonce, "alice", wrongHa1, "example.com", "SHA-256", cnonce, "sip:alice@example.com", 1)

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Authorization: " + authHeader + "\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", statusOrNil(res))
	}

	bindings := reg.GetBindings("sip:alice@example.com")
	if len(bindings) != 0 {
		t.Fatal("expected no bindings after failed auth")
	}
}

func TestRegistrar_Auth_WrongAOR(t *testing.T) {
	reg := NewRegistrar()
	store := newTestAuthStore("secret")
	reg.SetPasswordStore(store)
	tx := &mockTx{}

	nonce := reg.nonces.NewNonce()
	cnonce := "test-cnonce"
	// Build auth with the Request-URI (sip:bob@example.com) so it passes URI validation,
	// but alice's credentials — AOR check will reject bob's AOR.
	authHeader := buildAuthHeader(nonce, "alice", store.users["alice"].ha1, "example.com", "SHA-256", cnonce, "sip:bob@example.com", 1)

	// Register with a different AOR (bob instead of alice)
	req := sipMessage("REGISTER sip:bob@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:bob@example.com>;tag=abc\r\n" +
		"To: <sip:bob@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Authorization: " + authHeader + "\r\n" +
		"Contact: <sip:bob@192.168.1.6>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", statusOrNil(res))
	}
}

func TestRegistrar_Auth_UnknownNonce(t *testing.T) {
	reg := NewRegistrar()
	store := newTestAuthStore("secret")
	reg.SetPasswordStore(store)
	tx := &mockTx{}

	cnonce := "test-cnonce"
	// Use a nonce that was never issued
	authHeader := buildAuthHeader("unknown-nonce", "alice", store.users["alice"].ha1, "example.com", "SHA-256", cnonce, "sip:alice@example.com", 1)

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Authorization: " + authHeader + "\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %v", statusOrNil(res))
	}
	wwwAuth := res.Headers.GetFirst("WWW-Authenticate")
	if strings.Contains(wwwAuth, "stale=TRUE") {
		t.Fatalf("expected stale=FALSE for unknown nonce, got %q", wwwAuth)
	}
}

func TestRegistrar_Auth_ExpiredNonce(t *testing.T) {
	reg := NewRegistrar()
	store := newTestAuthStore("secret")
	reg.SetPasswordStore(store)
	tx := &mockTx{}

	nonce := reg.nonces.NewNonce()

	// Set expiry to the past to simulate expiration without sleeping.
	reg.nonces.mu.Lock()
	reg.nonces.entries[nonce].expires = time.Now().Add(-1 * time.Second)
	reg.nonces.mu.Unlock()

	cnonce := "test-cnonce"
	authHeader := buildAuthHeader(nonce, "alice", store.users["alice"].ha1, "example.com", "SHA-256", cnonce, "sip:alice@example.com", 1)

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Authorization: " + authHeader + "\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %v", statusOrNil(res))
	}
	wwwAuth := res.Headers.GetFirst("WWW-Authenticate")
	if !strings.Contains(wwwAuth, "stale=TRUE") {
		t.Fatalf("expected stale=TRUE for expired nonce, got %q", wwwAuth)
	}
}

func TestRegistrar_Auth_NoAuthWhenNotConfigured(t *testing.T) {
	reg := NewRegistrar()
	tx := &mockTx{}

	req := sipMessage("REGISTER sip:alice@example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKtest\r\n" +
		"From: <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:alice@example.com>\r\n" +
		"Call-ID: call-1\r\n" +
		"CSeq: 1 REGISTER\r\n" +
		"Contact: <sip:alice@192.168.1.5>;expires=3600\r\n" +
		"Content-Length: 0\r\n\r\n")

	reg.HandleRegister(t.Context(), req, tx)

	res := tx.last()
	if res == nil || res.StatusCode() != proto.SIPStatusOK {
		t.Fatalf("expected 200 OK when no auth configured, got %v", statusOrNil(res))
	}
}

func statusOrNil(msg *proto.SIPMessage) string {
	if msg == nil {
		return "nil"
	}
	return msg.Status()
}
