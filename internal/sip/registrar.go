package sip

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thorsager/trecs/internal/logutil"

	"github.com/thorsager/trecs/proto"
)

const defaultFlowTimer = 120 // RFC 5626 §5.4: default keepalive interval for reliable transports

// Binding represents a single AOR → Contact URI registration binding
// as defined in RFC 3261 §10.
type Binding struct {
	Expires     time.Time
	LastUpdate  time.Time
	ContactURI  string
	CallID      string
	FlowID      string
	SIPInstance string
	CSeq        int
	RegID       int
	OB          bool
}

// Registrar manages SIP registration bindings per RFC 3261 §10.
// It is safe for concurrent use.
type Registrar struct {
	bindings map[string][]*Binding
	lastCSeq map[string]int
	mu       sync.RWMutex
	passwd   PasswordStore // nil = no authentication (accept all)
	nonces   *NonceManager // always non-nil; created in NewRegistrar
}

// SetPasswordStore enables Digest authentication for REGISTER requests.
// When set, the registrar challenges unauthenticated REGISTERs with 401 and
// verifies Authorization headers before accepting bindings.
func (r *Registrar) SetPasswordStore(store PasswordStore) {
	r.passwd = store
}

// NewRegistrar creates a new Registrar with no bindings.
func NewRegistrar() *Registrar {
	return &Registrar{
		bindings: make(map[string][]*Binding),
		lastCSeq: make(map[string]int),
		nonces:   NewNonceManager(300 * time.Second),
	}
}

// Start runs the binding expiry reaper until ctx is canceled.
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
			r.nonces.Sweep()
		}
	}
}

// GetBindings returns a copy of all active bindings for an AOR.
func (r *Registrar) GetBindings(aor string) []*Binding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bindings := r.bindings[aor]
	result := make([]*Binding, len(bindings))
	for i, b := range bindings {
		cp := *b
		result[i] = &cp
	}
	return result
}

// RemoveBindingsByFlowID removes all bindings associated with the given flow.
// Called when a TCP connection dies.
func (r *Registrar) RemoveBindingsByFlowID(flowID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for aor, bindings := range r.bindings {
		var filtered []*Binding
		for _, b := range bindings {
			if b.FlowID != flowID {
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

// HandleRegister implements the REGISTER request handler per RFC 3261 §10.
// It can be registered directly with Server.On.
func (r *Registrar) HandleRegister(ctx context.Context, req *proto.SIPMessage, tx Transaction) {
	ctx = logutil.WithValues(ctx,
		"contact", req.Headers.GetFirst("Contact"),
		"expires", req.Headers.GetFirst("Expires"),
		"require", req.Headers.GetFirst("Require"),
		"supported", req.Headers.GetFirst("Supported"))
	log := logutil.FromContext(ctx)

	log.Debug("REGISTER received")

	to, err := req.To()
	if err != nil {
		log.Error("REGISTER: bad To header", "error", err)
		tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
		return
	}

	aor := to.URI

	// Per RFC 3261 §10.2 the Request-URI is the registrar domain (no user),
	// while the To header is the full AOR (sip:user@domain). We check that
	// the host[:port] parts match.
	if reqURI := req.RequestURI(); reqURI != "" && uriHostname(reqURI) != uriHostname(aor) {
		log.Warn("REGISTER: Request-URI domain mismatch", "requestURI", reqURI, "aor", aor)
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

	if r.passwd != nil {
		authHeader := req.Headers.GetFirst("Authorization")
		if authHeader == "" {
			log.Debug("REGISTER: no Authorization, challenging")
			nonce := r.nonces.NewNonce()
			challenge := BuildWWWAuthenticate(r.passwd.Realm(), nonce, r.passwd.Algorithm(), false)
			res := proto.NewResponse(req, 401, "Unauthorized")
			res.Headers.Add("WWW-Authenticate", challenge)
			tx.Respond(res)
			return
		}

		creds, err := ParseAuthorization(authHeader)
		if err != nil || creds.Username == "" {
			log.Warn("REGISTER: bad Authorization header", "error", err)
			tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
			return
		}

		ha1, userExists := r.passwd.HA1(creds.Username)
		if !userExists || !VerifyDigest(creds, ha1) {
			log.Warn("REGISTER: digest verification failed", "username", creds.Username)
			tx.Respond(proto.NewResponse(req, 403, "Forbidden"))
			return
		}

		known, valid := r.nonces.Verify(creds.Nonce, creds.NC)
		if !valid {
			log.Warn("REGISTER: nonce rejected", "known", known)
			stale := known
			nonce := r.nonces.NewNonce()
			challenge := BuildWWWAuthenticate(r.passwd.Realm(), nonce, r.passwd.Algorithm(), stale)
			res := proto.NewResponse(req, 401, "Unauthorized")
			res.Headers.Add("WWW-Authenticate", challenge)
			tx.Respond(res)
			return
		}

		allowedAORs, _ := r.passwd.AORs(creds.Username)
		if !AORAllowed(allowedAORs, aor) {
			log.Warn("REGISTER: AOR not authorized", "username", creds.Username, "aor", aor)
			tx.Respond(proto.NewResponse(req, 403, "Forbidden"))
			return
		}
	}

	r.mu.Lock()

	if last, ok := r.lastCSeq[callID]; ok && req.CSeq.Seq <= last {
		r.mu.Unlock()
		log.Warn("REGISTER: non-monotonic CSeq", "cseq", req.CSeq.Seq, "last", last, "callID", callID)
		tx.Respond(proto.NewResponse(req, 400, "Bad Request"))
		return
	}

	if hasStar {
		if defaultExpires != 0 {
			r.mu.Unlock()
			log.Warn("REGISTER: star Contact without Expires: 0")
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

	var flowID string
	if target := tx.Target(); target.Conn != nil {
		flowID = FlowKeyFromConn(target.Conn).String()
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
			bindings = upsertBinding(bindings, c.uri, callID, req.CSeq.Seq, expires, flowID, c.ob, c.regID, c.sipInstance)
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
		contact := fmt.Sprintf("<%s>;expires=%d", b.ContactURI, secs)
		if b.OB {
			contact += ";ob"
		}
		if b.RegID > 0 {
			contact += fmt.Sprintf(";reg-id=%d", b.RegID)
		}
		if b.SIPInstance != "" {
			contact += ";+sip.instance=" + b.SIPInstance
		}
		res.Headers.Add("Contact", contact)
	}

	if minExpires >= 0 {
		res.Headers.Add("Expires", strconv.Itoa(minExpires))
	}

	// Per RFC 5626 §6: include Require: outbound when the UAC requested it.
	// Per RFC 5626 §5.4: include Flow-Timer with default 120s for reliable transports.
	if containsIgnoreCase(req.Headers.GetFirst("Supported"), "outbound") {
		res.Headers.Add("Require", "outbound")
		if req.IsReliableTransport() {
			res.Headers.Add("Flow-Timer", strconv.Itoa(defaultFlowTimer))
		}
	}

	slog.Debug("REGISTER responded", "statusCode", res.StatusCode())
	tx.Respond(res)
}

func containsIgnoreCase(s, substr string) bool {
	s = strings.ToLower(s)
	substr = strings.ToLower(substr)
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == substr {
			return true
		}
	}
	return false
}

type parsedContact struct {
	uri         string
	sipInstance string
	expires     int
	regID       int
	ob          bool
}

func (pc *parsedContact) addHeaderParam(param string) {
	param = strings.TrimSpace(param)
	if strings.EqualFold(param, "ob") {
		pc.ob = true
		return
	}
	k, v, ok := strings.Cut(param, "=")
	if !ok {
		return
	}
	if strings.EqualFold(k, "reg-id") {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			pc.regID = n
		}
		return
	}
	if strings.EqualFold(k, "expires") {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			pc.expires = n
		}
		return
	}
	if strings.EqualFold(k, "+sip.instance") {
		pc.sipInstance = v
		return
	}
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
				slog.Warn("REGISTER: skipping invalid contact", "error", err)
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
	for i := range len(raw) {
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

	c := parsedContact{expires: -1, regID: -1}

	open := -1
	if idx := strings.IndexByte(raw, '<'); idx >= 0 {
		open = idx
		closeIdx := strings.IndexByte(raw[open+1:], '>')
		if closeIdx < 0 {
			return parsedContact{}, fmt.Errorf("unmatched '<' in contact: %s", raw)
		}
		closeIdx += open + 1
		c.uri = strings.TrimSpace(raw[open+1 : closeIdx])
		rest := strings.TrimSpace(raw[closeIdx+1:])
		if rest != "" {
			for _, p := range strings.Split(rest, ";") {
				c.addHeaderParam(p)
			}
		}
	} else {
		uri, expires := splitAddrSpecContact(raw)
		c.uri = uri
		if expires >= 0 {
			c.expires = expires
		}
	}

	// Check for ;ob in URI params when name-addr form is used. Some
	// UAs (e.g. pjsua) place ;ob inside the angle brackets rather than
	// as a header-level parameter as RFC 5626 §4 requires.
	if !c.ob && open >= 0 {
		cp := c.uri
		if semi := strings.IndexByte(cp, ';'); semi >= 0 {
			for _, p := range strings.Split(cp[semi+1:], ";") {
				p = strings.TrimSpace(p)
				if strings.EqualFold(p, "ob") {
					c.ob = true
					break
				}
			}
		}
	}

	if c.uri == "" {
		return parsedContact{}, fmt.Errorf("empty contact URI: %s", raw)
	}
	return c, nil
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

func removeBinding(bindings []*Binding, contactURI string) []*Binding {
	for i, b := range bindings {
		if b.ContactURI == contactURI {
			return append(bindings[:i], bindings[i+1:]...)
		}
	}
	return bindings
}

func upsertBinding(bindings []*Binding, contactURI, callID string, cseq, expiresSec int, flowID string, ob bool, regID int, sipInstance string) []*Binding {
	now := time.Now()
	for _, b := range bindings {
		if b.ContactURI == contactURI {
			b.CallID = callID
			b.CSeq = cseq
			b.Expires = now.Add(time.Duration(expiresSec) * time.Second)
			b.LastUpdate = now
			b.FlowID = flowID
			b.OB = ob
			if regID >= 0 {
				b.RegID = regID
			}
			if sipInstance != "" {
				b.SIPInstance = sipInstance
			}
			return bindings
		}
	}
	return append(bindings, &Binding{
		ContactURI:  contactURI,
		CallID:      callID,
		CSeq:        cseq,
		Expires:     now.Add(time.Duration(expiresSec) * time.Second),
		LastUpdate:  now,
		FlowID:      flowID,
		OB:          ob,
		RegID:       regID,
		SIPInstance: sipInstance,
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
