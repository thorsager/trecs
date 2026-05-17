package sip

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitub.com/thorsager/trec/proto"
)

// Binding represents a single AOR → Contact URI registration binding
// as defined in RFC 3261 §10.
type Binding struct {
	ContactURI string
	CallID     string
	CSeq       int
	Expires    time.Time
	LastUpdate time.Time
}

// Registrar manages SIP registration bindings per RFC 3261 §10.
// It is safe for concurrent use.
type Registrar struct {
	mu       sync.RWMutex
	bindings map[string][]*Binding // keyed by AOR (To header URI)
	lastCSeq map[string]int        // keyed by Call-ID
}

// NewRegistrar creates a new Registrar with no bindings.
func NewRegistrar() *Registrar {
	return &Registrar{
		bindings: make(map[string][]*Binding),
		lastCSeq: make(map[string]int),
	}
}

// Start runs the binding expiry reaper until ctx is cancelled.
// It should be called as a goroutine.
func (r *Registrar) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

// HandleRegister implements the REGISTER request handler per RFC 3261 §10.
// It can be registered directly with Server.On.
func (r *Registrar) HandleRegister(req *proto.SIPMessage, tx Transaction) {
	to, err := req.To()
	if err != nil {
		log.Printf("REGISTER: bad To header: %v", err)
		tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
		return
	}
	aor := to.URI

	// Per RFC 3261 §10.2 the Request-URI is the registrar domain (no user),
	// while the To header is the full AOR (sip:user@domain). We check that
	// the host[:port] parts match.
	if reqURI := req.RequestURI(); reqURI != "" && uriHost(reqURI) != uriHost(aor) {
		log.Printf("REGISTER: Request-URI %q domain != To %q domain", reqURI, aor)
		tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
		return
	}

	callID := req.Headers.GetFirst("Call-ID")
	if callID == "" {
		tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
		return
	}

	rawContacts := req.Headers.Get("Contact")
	defaultExpires := 3600
	if expStr := req.Headers.GetFirst("Expires"); expStr != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(expStr)); err == nil && n >= 0 {
			defaultExpires = n
		}
	}

	contacts, hasStar := extractContacts(rawContacts)

	r.mu.Lock()

	if last, ok := r.lastCSeq[callID]; ok && req.CSeq.Seq <= last {
		r.mu.Unlock()
		log.Printf("REGISTER: non-monotonic CSeq %d (last %d) for Call-ID %s", req.CSeq.Seq, last, callID)
		tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
		return
	}

	if hasStar {
		if defaultExpires != 0 {
			r.mu.Unlock()
			log.Printf("REGISTER: star Contact without Expires: 0")
			tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
			return
		}
		delete(r.bindings, aor)
		r.lastCSeq[callID] = req.CSeq.Seq
		r.mu.Unlock()
		sendRegisterResponse(req, tx, nil)
		return
	}

	if len(rawContacts) == 0 || (len(rawContacts) == 1 && strings.TrimSpace(rawContacts[0]) == "") {
		bindings := r.bindings[aor]
		r.mu.Unlock()
		sendRegisterResponse(req, tx, bindings)
		return
	}

	bindings := r.bindings[aor]
	for _, c := range contacts {
		expires := defaultExpires
		if c.expires >= 0 {
			expires = c.expires
		}
		if expires == 0 {
			bindings = removeBinding(bindings, c.uri)
		} else {
			bindings = upsertBinding(bindings, c.uri, callID, req.CSeq.Seq, expires)
		}
	}

	if len(bindings) == 0 {
		delete(r.bindings, aor)
	} else {
		r.bindings[aor] = bindings
	}
	r.lastCSeq[callID] = req.CSeq.Seq
	r.mu.Unlock()

	sendRegisterResponse(req, tx, bindings)
}

func sendRegisterResponse(req *proto.SIPMessage, tx Transaction, bindings []*Binding) {
	res := proto.NewResponse(req, 200, "OK")
	res.Headers.Add("Date", time.Now().UTC().Format(time.RFC1123))
	res.Headers.Add("Allow", "INVITE, ACK, BYE, CANCEL, OPTIONS, REGISTER")

	minExpires := -1
	for _, b := range bindings {
		remaining := time.Until(b.Expires)
		if remaining <= 0 {
			continue
		}
		secs := int(math.Ceil(remaining.Seconds()))
		if minExpires < 0 || secs < minExpires {
			minExpires = secs
		}
		res.Headers.Add("Contact", fmt.Sprintf("<%s>;expires=%d", b.ContactURI, secs))
	}

	if minExpires >= 0 {
		res.Headers.Add("Expires", strconv.Itoa(minExpires))
	}

	tx.Respond(res)
}

type parsedContact struct {
	uri     string
	expires int // -1 = not specified
}

func extractContacts(raw []string) (contacts []parsedContact, hasStar bool) {
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "*" {
			hasStar = true
			continue
		}
		for _, part := range splitContactValues(v) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if part == "*" {
				hasStar = true
				continue
			}
			c, err := parseContactURI(part)
			if err != nil {
				log.Printf("REGISTER: skipping invalid contact: %v", err)
				continue
			}
			contacts = append(contacts, c)
		}
	}
	return
}

func splitContactValues(raw string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, raw[start:i])
				start = i + 1
			}
		}
	}
	if start < len(raw) {
		parts = append(parts, raw[start:])
	}
	return parts
}

func parseContactURI(raw string) (parsedContact, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return parsedContact{}, fmt.Errorf("invalid contact: %s", raw)
	}

	c := parsedContact{expires: -1}

	if open := strings.IndexByte(raw, '<'); open >= 0 {
		close := strings.IndexByte(raw[open+1:], '>')
		if close < 0 {
			return parsedContact{}, fmt.Errorf("unmatched '<' in contact: %s", raw)
		}
		close += open + 1
		c.uri = strings.TrimSpace(raw[open+1 : close])
		rest := strings.TrimSpace(raw[close+1:])
		if rest != "" {
			c.expires = parseExpiresParam(rest)
		}
	} else {
		uri, expires := splitAddrSpecContact(raw)
		c.uri = uri
		c.expires = expires
	}

	if c.uri == "" {
		return parsedContact{}, fmt.Errorf("empty contact URI: %s", raw)
	}
	return c, nil
}

func parseExpiresParam(params string) int {
	for _, p := range strings.Split(params, ";") {
		p = strings.TrimSpace(p)
		if k, v, ok := strings.Cut(p, "="); ok && strings.EqualFold(k, "expires") {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				return n
			}
		}
	}
	return -1
}

func splitAddrSpecContact(raw string) (uri string, expires int) {
	expires = -1
	parts := strings.Split(raw, ";")
	i := len(parts) - 1
	for ; i > 0; i-- {
		p := strings.TrimSpace(parts[i])
		k, v, ok := strings.Cut(p, "=")
		if ok && strings.EqualFold(k, "expires") {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				expires = n
			}
		} else {
			break
		}
	}
	uri = strings.TrimRight(strings.Join(parts[:i+1], ";"), ";")
	return
}

// uriHost extracts the host[:port] from a SIP URI like "sip:user@host:port;params".
func uriHost(uri string) string {
	uri = strings.TrimPrefix(uri, "sip:")
	if at := strings.LastIndexByte(uri, '@'); at >= 0 {
		uri = uri[at+1:]
	}
	if semi := strings.IndexByte(uri, ';'); semi >= 0 {
		uri = uri[:semi]
	}
	return uri
}

func removeBinding(bindings []*Binding, contactURI string) []*Binding {
	for i, b := range bindings {
		if b.ContactURI == contactURI {
			return append(bindings[:i], bindings[i+1:]...)
		}
	}
	return bindings
}

func upsertBinding(bindings []*Binding, contactURI, callID string, cseq int, expiresSec int) []*Binding {
	now := time.Now()
	for _, b := range bindings {
		if b.ContactURI == contactURI {
			b.CallID = callID
			b.CSeq = cseq
			b.Expires = now.Add(time.Duration(expiresSec) * time.Second)
			b.LastUpdate = now
			return bindings
		}
	}
	return append(bindings, &Binding{
		ContactURI: contactURI,
		CallID:     callID,
		CSeq:       cseq,
		Expires:    now.Add(time.Duration(expiresSec) * time.Second),
		LastUpdate: now,
	})
}

func (r *Registrar) sweep() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for aor, bindings := range r.bindings {
		var filtered []*Binding
		for _, b := range bindings {
			if b.Expires.After(now) {
				filtered = append(filtered, b)
			}
		}
		if len(filtered) == 0 {
			delete(r.bindings, aor)
		} else {
			r.bindings[aor] = filtered
		}
	}
}
