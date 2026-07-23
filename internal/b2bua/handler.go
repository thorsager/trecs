package b2bua

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/thorsager/trecs/internal/dialplan"
	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/internal/trunk"
	"github.com/thorsager/trecs/proto"
)

// Config holds the dependencies needed to create a B2BUA handler.
type Config struct {
	Registrar      *sip.Registrar
	SessionManager *media.SessionManager
	Server         *sip.Server
	ServerIP       string
	ServerAddr     string
	UACManager     *sip.UACManager
	Dialplan       dialplan.Dialplan
	RTPPortMin     int
	RTPPortMax     int
	PRACKEnabled   bool
	TrunkMgr       *trunk.TrunkManager
	NATAddress     string
}

// Handler implements SIP request handlers for the T-REC B2BUA server,
// including echo service, file playback, B2BUA call bridging, and call teardown.
type Handler struct {
	reg               *sip.Registrar
	sm                *media.SessionManager
	server            *sip.Server
	serverIP          string
	serverAddr        string
	uacMgr            *sip.UACManager
	dp                dialplan.Dialplan
	rtpMin            int
	rtpMax            int
	store             *Store
	proxyPasswd       sip.PasswordStore
	proxyNonces       *sip.NonceManager
	authTracker       *sip.AuthAttemptTracker
	maxFailedAttempts int
	prackMgr          *sip.ReliableProvisionalManager
	trunkMgr          *trunk.TrunkManager
	natAddress        string
	serverPort        string
}

// NewHandler creates a new B2BUA handler with the given configuration.
func NewHandler(cfg Config) *Handler {
	h := &Handler{
		reg:               cfg.Registrar,
		sm:                cfg.SessionManager,
		server:            cfg.Server,
		serverIP:          cfg.ServerIP,
		serverAddr:        cfg.ServerAddr,
		uacMgr:            cfg.UACManager,
		dp:                cfg.Dialplan,
		rtpMin:            cfg.RTPPortMin,
		rtpMax:            cfg.RTPPortMax,
		store:             NewStore(),
		maxFailedAttempts: sip.DefaultMaxFailedAuthAttempts,
	}
	if cfg.PRACKEnabled {
		h.prackMgr = sip.NewReliableProvisionalManager()
	}
	if cfg.TrunkMgr != nil {
		h.trunkMgr = cfg.TrunkMgr
	}
	h.natAddress = cfg.NATAddress
	if _, port, err := net.SplitHostPort(cfg.ServerAddr); err == nil && port != "" {
		h.serverPort = port
	} else {
		h.serverPort = "5060"
	}
	return h
}

// callCtx holds the shared state for a single B2BUA call leg.
// It is passed through the response loops and 200 OK handlers instead of
// threading the same ~20 parameters through every function.
type callCtx struct {
	req           *proto.SIPMessage
	tx            sip.Transaction
	target        *sip.Target
	transportImpl sip.Transport
	uac           *sip.UACTransaction
	rtpConnA      *media.RTPConn
	rtpConnB      *media.RTPConn
	from          *proto.SIPAddress
	aliceFromTag  string
	serverTag     string
	callID        string
	calleeTag     string
	bobCallID     string
	to            *proto.SIPAddress
	selectedPT    uint8
	hasEarlyOffer bool
	aliceSDPOffer *proto.SDP
	aliceSDPBytes []byte
	recordRoute   string
}

// SetProxyPasswordStore enables proxy authentication for INVITE and BYE
// using the same PasswordStore as the registrar. The nonce manager
// runs its own sweep goroutine, stopped when ctx is canceled.
func (h *Handler) SetProxyPasswordStore(store sip.PasswordStore, ctx context.Context) {
	h.proxyPasswd = store
	h.proxyNonces = sip.NewNonceManager(300 * time.Second)
	h.authTracker = sip.NewAuthAttemptTracker(sip.AuthAttemptTTL)
	sip.StartNonceSweeper(ctx, h.proxyNonces, 30*time.Second)
}

// SetMaxFailedAuthAttempts sets the number of consecutive auth failures allowed
// before a 403 Forbidden lockout. The value is clamped to the range [1, 10];
// the default is 3.
func (h *Handler) SetMaxFailedAuthAttempts(n int) {
	h.maxFailedAttempts = sip.ClampMaxFailedAuthAttempts(n)
}

// requireProxyAuth checks the Proxy-Authorization header on req.
// If proxy auth is not configured, it returns nil (passthrough).
// If auth is configured and the header is missing or invalid, it sends
// the appropriate error response and returns nil (caller should abort).
// On success it returns the parsed credentials.
func (h *Handler) requireProxyAuth(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction, method string) *sip.DigestCredentials {
	if h.proxyPasswd == nil {
		return nil
	}
	log := logutil.FromContext(ctx)

	creds := sip.VerifyDigestRequest(req, tx,
		h.proxyPasswd, h.proxyNonces, h.authTracker,
		"Proxy-Authorization", "Proxy-Authenticate",
		407, "Proxy Authentication Required",
		method, log, h.maxFailedAttempts)
	if creds != nil {
		key := sip.AuthAttemptKey(tx, req.Headers.GetFirst("Call-ID"))
		h.authTracker.Reset(key)
		log.Debug("proxy auth: verified", "username", creds.Username, "method", method)
	}
	return creds
}

// HandleOptions responds to OPTIONS requests.
func (h *Handler) HandleOptions(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction) {
	ctx = logutil.WithValues(ctx,
		"from", req.Headers.GetFirst("From"),
		"to", req.Headers.GetFirst("To"),
		"callID", req.Headers.GetFirst("Call-ID"))
	log := logutil.FromContext(ctx)

	log.Debug("OPTIONS received")

	trying := proto.NewResponse(req, 100, "Trying")
	tx.Respond(trying)

	res := proto.NewResponse(req, 200, "OK")
	res.Headers["Allow"] = []string{h.server.AllowMethods()}
	res.Headers["Accept"] = []string{"application/sdp"}
	supported := []string{"timer"}
	if h.prackMgr != nil {
		supported = append(supported, "100rel")
	}
	res.Headers["Supported"] = supported
	tx.Respond(res)

	log.Debug("OPTIONS responded", "statusCode", 200)
}

// HandlePRACK handles incoming PRACK requests for reliable provisional responses.
func (h *Handler) HandlePRACK(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction) {
	if h.prackMgr == nil {
		tx.Respond(proto.NewResponse(req, 501, "Not Implemented"))
		return
	}
	h.prackMgr.HandlePRACK(ctx, req, tx)
}

func (h *Handler) cancelPRACK(callID string) {
	if h.prackMgr != nil {
		h.prackMgr.Cancel(callID)
	}
}

// HandleInvite dispatches to dialplan services or B2BUA call routing.
func (h *Handler) HandleInvite(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction) {
	callID := req.Headers.GetFirst("Call-ID")
	ctx = logutil.WithValues(ctx,
		"callID", callID,
		"from", req.Headers.GetFirst("From"),
		"to", req.Headers.GetFirst("To"),
		"contact", req.Headers.GetFirst("Contact"),
		"contentType", req.Headers.GetFirst("Content-Type"),
		"bodyLen", len(req.Body))
	log := logutil.FromContext(ctx)

	log.Debug("INVITE received")

	trying := proto.NewResponse(req, 100, "Trying")
	tx.Respond(trying)

	// Check if source IP belongs to a static trunk (skip proxy auth)
	isTrustedTrunk := false
	if h.trunkMgr != nil {
		if srcAddr := tx.Target().Addr; srcAddr != nil {
			srcIP := srcAddr.String()
			if host, _, err := net.SplitHostPort(srcIP); err == nil {
				srcIP = host
			}
			if h.trunkMgr.TrustedIPMatches(srcIP) {
				log.Debug("INVITE from trusted trunk, skipping proxy auth", "srcIP", srcIP)
				isTrustedTrunk = true
			}
		}
	}

	if !isTrustedTrunk {
		if h.requireProxyAuth(ctx, req, tx, "INVITE") == nil && h.proxyPasswd != nil {
			return
		}
	}

	if h.dp != nil {
		if entry, ok := h.dp.Lookup(req); ok {
			switch entry.Action {
			case dialplan.ActionEcho:
				h.handleEchoInvite(ctx, req, tx)
			case dialplan.ActionPlay:
				h.handleFileInvite(ctx, req, tx, entry.File)
			}
			return
		}
	}

	h.handleB2BUAInvite(ctx, req, tx)
}

func (h *Handler) resolveClientAddr(sdpOffer *proto.SDP) (clientIP string, clientPort int) {
	clientIP, clientPort = media.ExtractRTPAddr(sdpOffer)
	parsedIP := net.ParseIP(clientIP)
	if parsedIP != nil && parsedIP.IsLoopback() && h.natAddress != "" {
		return h.natAddress, clientPort
	}
	return clientIP, clientPort
}

func (h *Handler) handleEchoInvite(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction) {
	log := logutil.FromContext(ctx)

	log.Debug("echo: handling INVITE", "hasSDP", len(req.Body) > 0)

	serverTag := sip.GenerateTag()

	var sdpOffer *proto.SDP
	if len(req.Body) > 0 && req.Headers.GetFirst("Content-Type") == "application/sdp" {
		sdp, err := proto.UnmarshalSDPBytes(req.Body)
		if err != nil {
			res := proto.NewResponse(req, 488, "Not Acceptable Here")
			tx.Respond(res)
			return
		}
		sdpOffer = sdp
	}

	rtpConn, err := media.NewRTPConnRange(h.rtpMin, h.rtpMax)
	if err != nil {
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	rtpAddr := rtpConn.LocalAddr().(*net.UDPAddr)
	payloadType := uint8(proto.PCMU)

	var sdpBody *proto.SDP
	if sdpOffer != nil {
		payloadType = media.PickPayloadType(sdpOffer)
		sdpBody = media.BuildAnswer(sdpOffer, rtpAddr.Port, payloadType, h.serverIP)
	} else {
		sdpBody = media.BuildOffer(rtpAddr.Port, payloadType, h.serverIP)
	}

	from, err := req.From()
	if err != nil {
		rtpConn.Close()
		res := proto.NewResponse(req, 400, "Bad Request")
		tx.Respond(res)
		return
	}

	callID := req.Headers.GetFirst("Call-ID")
	key := media.SessionKey{
		CallID:    callID,
		RemoteTag: from.Tag,
		LocalTag:  serverTag,
	}

	session := media.NewSession(ctx, key, rtpConn, payloadType, rtpAddr)

	if sdpOffer != nil {
		clientIP, clientPort := h.resolveClientAddr(sdpOffer)
		remoteAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", clientIP, clientPort))
		if err != nil {
			rtpConn.Close()
			log.Error("failed to resolve client RTP address", "clientIP", clientIP, "error", err)
			res := proto.NewResponse(req, 500, "Internal Server Error")
			tx.Respond(res)
			return
		}
		session.SetRemoteAddr(remoteAddr)
		session.SetState(media.SessionActive)
	} else {
		session.SetState(media.SessionWaitingAck)
	}

	h.sm.Add(session)

	res := proto.NewResponse(req, 200, "OK")
	toHeader := req.Headers.GetFirst("To")
	res.Headers.Set("To", []string{toHeader + ";tag=" + serverTag})

	sdpBytes, _ := sdpBody.Marshal()
	res.Body = sdpBytes
	res.Headers.Set("Content-Type", []string{"application/sdp"})
	res.Headers["Allow"] = []string{h.server.AllowMethods()}

	tx.Respond(res)
	log.Debug("echo: responded 200 OK", "rtpPort", rtpAddr.Port, "payloadType", payloadType)

	if sdpOffer != nil {
		go media.RunEcho(session.Ctx(), rtpConn, payloadType)
	}
}

func (h *Handler) handleFileInvite(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction, filePath string) {
	callID := req.Headers.GetFirst("Call-ID")
	ctx = logutil.WithValues(ctx, "filePath", filePath, "callID", callID)
	log := logutil.FromContext(ctx)

	log.Debug("file: handling INVITE", "hasSDP", len(req.Body) > 0)

	serverTag := sip.GenerateTag()

	var sdpOffer *proto.SDP
	if len(req.Body) > 0 && req.Headers.GetFirst("Content-Type") == "application/sdp" {
		sdp, err := proto.UnmarshalSDPBytes(req.Body)
		if err != nil {
			res := proto.NewResponse(req, 488, "Not Acceptable Here")
			tx.Respond(res)
			return
		}
		sdpOffer = sdp
	}

	wav, err := media.LoadWav(filePath)
	if err != nil {
		log.Error("file: failed to load", "path", filePath, "error", err)
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	rtpConn, err := media.NewRTPConnRange(h.rtpMin, h.rtpMax)
	if err != nil {
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	rtpAddr := rtpConn.LocalAddr().(*net.UDPAddr)
	payloadType := uint8(proto.PCMU)

	var sdpBody *proto.SDP
	if sdpOffer != nil {
		payloadType = media.PickPayloadType(sdpOffer)
		sdpBody = media.BuildAnswer(sdpOffer, rtpAddr.Port, payloadType, h.serverIP)
	} else {
		sdpBody = media.BuildOffer(rtpAddr.Port, payloadType, h.serverIP)
	}

	from, err := req.From()
	if err != nil {
		rtpConn.Close()
		res := proto.NewResponse(req, 400, "Bad Request")
		tx.Respond(res)
		return
	}

	key := media.SessionKey{
		CallID:    callID,
		RemoteTag: from.Tag,
		LocalTag:  serverTag,
	}

	session := media.NewSession(ctx, key, rtpConn, payloadType, rtpAddr)
	session.Kind = media.SessionKindPlay
	session.WavData = wav
	session.CallerContact = req.Headers.GetFirst("Contact")
	session.CallerURI = from.URI
	if to, err := req.To(); err == nil {
		session.TargetURI = to.URI
	}
	txTarget := tx.Target()
	session.SipTransport = tx.Transport()
	session.SipTarget = &txTarget

	if sdpOffer != nil {
		clientIP, clientPort := h.resolveClientAddr(sdpOffer)
		remoteAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", clientIP, clientPort))
		if err != nil {
			rtpConn.Close()
			log.Error("failed to resolve client RTP address", "clientIP", clientIP, "error", err)
			res := proto.NewResponse(req, 500, "Internal Server Error")
			tx.Respond(res)
			return
		}
		session.SetRemoteAddr(remoteAddr)
		session.SetState(media.SessionActive)
	} else {
		session.SetState(media.SessionWaitingAck)
	}

	h.sm.Add(session)

	res := proto.NewResponse(req, 200, "OK")
	toHeader := req.Headers.GetFirst("To")
	res.Headers.Set("To", []string{toHeader + ";tag=" + serverTag})

	sdpBytes, _ := sdpBody.Marshal()
	res.Body = sdpBytes
	res.Headers.Set("Content-Type", []string{"application/sdp"})
	res.Headers["Allow"] = []string{h.server.AllowMethods()}

	tx.Respond(res)
	log.Debug("file: responded 200 OK", "rtpPort", rtpAddr.Port, "payloadType", payloadType)

	if sdpOffer != nil {
		remoteAddr := session.RemoteAddrSafe().(*net.UDPAddr)
		done := media.RunFilePlayback(session.Ctx(), rtpConn, remoteAddr, payloadType, wav)
		callLog := logutil.FromContext(ctx)
		go func() {
			<-done
			if session.Ctx().Err() == nil {
				h.sendByeToSession(logutil.NewContext(session.Ctx(), callLog), session, callID)
			}
		}()
	}
}

func (h *Handler) sendByeToSession(ctx context.Context, session *media.Session, callID string) {
	ctx = logutil.WithValues(ctx, "target", session.TargetURI)
	log := logutil.FromContext(ctx)

	log.Debug("file: sending BYE for playback complete")

	serverPort := h.serverPort

	session.Cancel()

	targetURI := session.TargetURI
	if targetURI == "" {
		targetURI = "sip:file@" + h.serverIP
	}

	bye := proto.NewRequest(proto.SIPMethodBYE, sip.StripBrackets(session.CallerContact))
	viaTransport := sip.TransportName(session.SipTransport)
	bye.Headers.Add("Via",
		fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s", viaTransport, h.serverIP, serverPort, sip.GenerateBranch()))
	bye.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", targetURI, session.Key.LocalTag))
	bye.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s", session.CallerURI, session.Key.RemoteTag))
	bye.Headers.Add("Call-ID", callID)
	bye.CSeq = proto.CSeq{Method: proto.SIPMethodBYE, Seq: 2}
	bye.Headers.Add("Max-Forwards", "70")
	bye.Headers.Add("Content-Length", "0")

	if session.SipTransport == nil || session.SipTarget == nil {
		log.Error("file: no transport/target for BYE")
		h.sm.Remove(session.Key)
		return
	}

	if err := session.SipTransport.Send(bye, session.SipTarget); err != nil {
		log.Error("file: failed to send BYE", "error", err)
	} else {
		log.Info("file: sent BYE for playback complete")
	}

	h.sm.Remove(session.Key)
}

func (h *Handler) handleB2BUAInvite(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction) {
	log := logutil.FromContext(ctx)

	log.Debug("B2BUA: handling INVITE", "hasSDP", len(req.Body) > 0)

	to, err := req.To()
	if err != nil {
		res := proto.NewResponse(req, 400, "Bad Request")
		tx.Respond(res)
		return
	}

	// Check trunk routes first. If an outbound route matches,
	// dispatch the call to the trunk peer instead of a registered user.
	if h.trunkMgr != nil {
		user := sip.ExtractUser(to.URI)
		if t, transformed, ok := h.trunkMgr.MatchRoute(user); ok {
			log.Info("B2BUA: trunk route matched",
				"trunk", t.Name,
				"user", user,
				"transformed", transformed)
			h.handleTrunkInvite(ctx, req, tx, t, transformed, to)
			return
		}
	}

	aor := sip.NormalizeAOR(to.URI)
	bindings := h.reg.GetBindings(aor)
	if len(bindings) == 0 {
		log.Info("B2BUA: no binding", "aor", aor, "target", to.URI)
		res := proto.NewResponse(req, 404, "Not Found")
		tx.Respond(res)
		return
	}

	binding, target, transport := h.selectBinding(bindings, log)
	if target == nil {
		log.Warn("B2BUA: no reachable binding", "aor", aor)
		res := proto.NewResponse(req, 502, "Bad Gateway")
		tx.Respond(res)
		return
	}

	aliceSupports100rel := detect100relSupport(req, h.prackMgr, log)

	from, err := req.From()
	if err != nil {
		res := proto.NewResponse(req, 400, "Bad Request")
		tx.Respond(res)
		return
	}

	aliceFromTag := from.Tag
	if aliceFromTag == "" {
		log.Warn("B2BUA: missing From tag, generating fallback", "from", from.URI)
		aliceFromTag = sip.GenerateTag()
	}

	callID := req.Headers.GetFirst("Call-ID")
	serverTag := sip.GenerateTag()

	rtpConnA, err := media.NewRTPConnRange(h.rtpMin, h.rtpMax)
	if err != nil {
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	rtpConnB, err := media.NewRTPConnRange(h.rtpMin, h.rtpMax)
	if err != nil {
		rtpConnA.Close()
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	hasEarlyOffer := len(req.Body) > 0 && req.Headers.GetFirst("Content-Type") == "application/sdp"
	var aliceSDPOffer *proto.SDP
	if hasEarlyOffer {
		aliceSDPOffer, err = proto.UnmarshalSDPBytes(req.Body)
		if err != nil {
			rtpConnA.Close()
			rtpConnB.Close()
			res := proto.NewResponse(req, 488, "Not Acceptable Here")
			tx.Respond(res)
			return
		}
	}

	selectedPT := media.PickPayloadType(aliceSDPOffer)

	bobSDP := media.BuildOffer(rtpConnB.LocalAddr().(*net.UDPAddr).Port, selectedPT, h.serverIP)
	bobSDPBytes, _ := bobSDP.Marshal()

	var aliceSDPBytes []byte
	if hasEarlyOffer {
		aliceAnswer := media.BuildAnswer(aliceSDPOffer, rtpConnA.LocalAddr().(*net.UDPAddr).Port, selectedPT, h.serverIP)
		aliceSDPBytes, _ = aliceAnswer.Marshal()
	}

	calleeTag := sip.GenerateTag()
	bobCallID := sip.GenerateCallID()

	calleeFrom := fmt.Sprintf("<%s>;tag=%s", from.URI, calleeTag)

	serverPort := h.serverPort
	recordRoute := fmt.Sprintf("<sip:trec@%s:%s;lr>", h.serverIP, serverPort)
	contactHeader := fmt.Sprintf("<sip:trec@%s:%s;transport=%s>", h.serverIP, serverPort, transport)

	var transportImpl sip.Transport
	switch transport {
	case "TCP":
		transportImpl = h.server.TCPTransport()
	default:
		transportImpl = h.server.UDPTransport()
	}

	uac := h.uacMgr.NewTransaction(ctx, proto.SIPMethodINVITE, transportImpl, target)

	bobInvite := proto.NewRequest(proto.SIPMethodINVITE, binding.ContactURI)
	bobInvite.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s", transport, h.serverIP, serverPort, uac.Branch))
	bobInvite.Headers.Add("From", calleeFrom)
	bobInvite.Headers.Add("To", fmt.Sprintf("<%s>", to.URI))
	bobInvite.Headers.Add("Contact", contactHeader)
	bobInvite.Headers.Add("Call-ID", bobCallID)
	bobInvite.Headers.Add("Max-Forwards", "70")
	bobInvite.Headers.Add("Record-Route", recordRoute)
	bobInvite.Headers.Add("Content-Type", "application/sdp")
	if h.prackMgr != nil {
		bobInvite.Headers.Add("Supported", "100rel")
	}
	bobInvite.Body = bobSDPBytes

	// Create a cancelable context for the response loop lifecycle
	// so that CANCEL can abort the pending INVITE to Bob.
	responseCtx, responseCancel := context.WithCancel(ctx)
	early := &EarlyCall{
		AliceCallID:    callID,
		BobCallID:      bobCallID,
		AliceServerTag: serverTag,
		BobTx:          uac,
		RTPConnA:       rtpConnA,
		RTPConnB:       rtpConnB,
		Cancel:         responseCancel,
	}
	h.store.StoreEarly(early)

	cc := &callCtx{
		req: req, tx: tx, target: target, transportImpl: transportImpl, uac: uac,
		rtpConnA: rtpConnA, rtpConnB: rtpConnB,
		from: from, aliceFromTag: aliceFromTag, serverTag: serverTag, callID: callID,
		calleeTag: calleeTag, bobCallID: bobCallID, to: to,
		selectedPT: selectedPT, hasEarlyOffer: hasEarlyOffer,
		aliceSDPOffer: aliceSDPOffer, aliceSDPBytes: aliceSDPBytes,
		recordRoute: recordRoute,
	}

	go h.b2buaResponseLoop(responseCtx, cc, bobInvite, binding, aliceSupports100rel)

	log.Debug("B2BUA: INVITE sent to Bob",
		"bobContact", binding.ContactURI,
		"transport", transport,
		"rtpPortA", rtpConnA.LocalAddr().(*net.UDPAddr).Port,
		"rtpPortB", rtpConnB.LocalAddr().(*net.UDPAddr).Port)
}

func (h *Handler) selectBinding(bindings []*sip.Binding, log *slog.Logger) (*sip.Binding, *sip.Target, string) {
	for i := len(bindings) - 1; i >= 0; i-- {
		b := bindings[i]

		if b.OB && b.FlowID != "" {
			if fc := h.server.Pool().GetByFlowID(b.FlowID); fc != nil {
				return b, &sip.Target{Conn: fc.Conn, FlowID: fc.Key.String()}, "TCP"
			}
		}

		tgt, tr, err := sip.TargetFromContact(b.ContactURI)
		if err != nil {
			log.Warn("B2BUA: unresolvable contact", "contact", b.ContactURI, "error", err)
			continue
		}

		if tr == "TCP" && tgt.Conn != nil {
			wrapped, werr := h.server.TCPTransport().HandleOutbound(tgt.Conn)
			if werr != nil {
				log.Error("B2BUA: HandleOutbound failed", "contact", b.ContactURI, "error", werr)
				tgt.Conn.Close()
				continue
			}
			tgt.Conn = wrapped
			log.Info("B2BUA: registered outbound TCP flow", "contact", b.ContactURI)
		}

		return b, tgt, tr
	}

	return nil, nil, ""
}

func (h *Handler) handleTrunkInvite(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction, trk *trunk.Trunk, dest string, to *proto.SIPAddress) {
	log := logutil.FromContext(ctx)

	trunkIP := trk.LocalIP
	if trunkIP == "" {
		trunkIP = h.serverIP
	}

	if !h.trunkMgr.AcquireChannel(trk.Name) {
		log.Warn("trunk at capacity", "trunk", trk.Name, "max", trk.MaxChannels)
		res := proto.NewResponse(req, 503, "Service Unavailable")
		tx.Respond(res)
		return
	}

	rtpConnA, err := media.NewRTPConnRange(h.rtpMin, h.rtpMax)
	if err != nil {
		h.trunkMgr.ReleaseChannel(trk.Name)
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	rtpConnB, err := media.NewRTPConnRange(h.rtpMin, h.rtpMax)
	if err != nil {
		rtpConnA.Close()
		h.trunkMgr.ReleaseChannel(trk.Name)
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	hasEarlyOffer := len(req.Body) > 0 && req.Headers.GetFirst("Content-Type") == "application/sdp"
	var aliceSDPOffer *proto.SDP
	if hasEarlyOffer {
		aliceSDPOffer, err = proto.UnmarshalSDPBytes(req.Body)
		if err != nil {
			rtpConnA.Close()
			rtpConnB.Close()
			h.trunkMgr.ReleaseChannel(trk.Name)
			res := proto.NewResponse(req, 488, "Not Acceptable Here")
			tx.Respond(res)
			return
		}
	}

	selectedPT := media.PickPayloadType(aliceSDPOffer)

	bobSDP := media.BuildOffer(rtpConnB.LocalAddr().(*net.UDPAddr).Port, selectedPT, trunkIP)
	bobSDPBytes, _ := bobSDP.Marshal()

	var aliceSDPBytes []byte
	if hasEarlyOffer {
		aliceAnswer := media.BuildAnswer(aliceSDPOffer, rtpConnA.LocalAddr().(*net.UDPAddr).Port, selectedPT, h.serverIP)
		aliceSDPBytes, _ = aliceAnswer.Marshal()
	}

	from, err := req.From()
	if err != nil {
		rtpConnA.Close()
		rtpConnB.Close()
		h.trunkMgr.ReleaseChannel(trk.Name)
		res := proto.NewResponse(req, 400, "Bad Request")
		tx.Respond(res)
		return
	}

	serverTag := sip.GenerateTag()
	callID := req.Headers.GetFirst("Call-ID")
	calleeTag := sip.GenerateTag()
	bobCallID := sip.GenerateCallID()

	serverPort := h.serverPort

	bobHostPort := net.JoinHostPort(trk.Host, strconv.Itoa(trk.Port))
	bobTarget, bobTransport, err := sip.TargetFromContact(fmt.Sprintf("sip:%s@%s", dest, bobHostPort))
	if err != nil {
		rtpConnA.Close()
		rtpConnB.Close()
		h.trunkMgr.ReleaseChannel(trk.Name)
		log.Error("failed to resolve trunk target", "trunk", trk.Name, "error", err)
		res := proto.NewResponse(req, 502, "Bad Gateway")
		tx.Respond(res)
		return
	}

	var transportImpl sip.Transport
	switch {
	case bobTransport == "TCP" && trk.Transport == "tcp":
		transportImpl = h.server.TCPTransport()
		if bobTarget.Conn != nil {
			wrapped, werr := h.server.TCPTransport().HandleOutbound(bobTarget.Conn)
			if werr != nil {
				bobTarget.Conn.Close()
				rtpConnA.Close()
				rtpConnB.Close()
				h.trunkMgr.ReleaseChannel(trk.Name)
				log.Error("failed to handle outbound TCP for trunk", "trunk", trk.Name, "error", werr)
				res := proto.NewResponse(req, 502, "Bad Gateway")
				tx.Respond(res)
				return
			}
			bobTarget.Conn = wrapped
		}
	default:
		transportImpl = h.server.UDPTransport()
	}

	calleeFrom := fmt.Sprintf("<%s>;tag=%s", from.URI, calleeTag)
	contactHeader := fmt.Sprintf("<sip:trec@%s:%s;transport=%s>", trunkIP, serverPort, bobTransport)
	recordRoute := fmt.Sprintf("<sip:trec@%s:%s;lr>", trunkIP, serverPort)

	bobReqURI := fmt.Sprintf("sip:%s@%s", dest, bobHostPort)
	bobInvite := proto.NewRequest(proto.SIPMethodINVITE, bobReqURI)

	uac := h.uacMgr.NewTransaction(ctx, proto.SIPMethodINVITE, transportImpl, bobTarget)

	bobInvite.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s", bobTransport, trunkIP, serverPort, uac.Branch))
	bobInvite.Headers.Add("From", calleeFrom)
	bobInvite.Headers.Add("To", fmt.Sprintf("<%s>", to.URI))
	bobInvite.Headers.Add("Contact", contactHeader)
	bobInvite.Headers.Add("Call-ID", bobCallID)
	bobInvite.Headers.Add("Max-Forwards", "70")
	bobInvite.Headers.Add("Record-Route", recordRoute)
	bobInvite.Headers.Add("Content-Type", "application/sdp")

	if trk.CallerID != "" {
		pai := fmt.Sprintf("<sip:%s@%s>", trk.CallerID, trunkIP)
		bobInvite.Headers.Add("P-Asserted-Identity", pai)
		bobInvite.Headers.Add("Privacy", "none")
	}

	for _, hdr := range trk.StripHeaders {
		bobInvite.Headers.Delete(hdr)
	}

	sessionExpires := time.Duration(0)
	if trk.SessionExpiresSec > 0 {
		sessionExpires = time.Duration(trk.SessionExpiresSec) * time.Second
		bobInvite.Headers.Add("Session-Expires", fmt.Sprintf("%d;refresher=uac", trk.SessionExpiresSec))
	}

	bobInvite.Body = bobSDPBytes

	responseCtx, responseCancel := context.WithCancel(ctx)
	early := &EarlyCall{
		AliceCallID:    callID,
		BobCallID:      bobCallID,
		AliceServerTag: serverTag,
		BobTx:          uac,
		RTPConnA:       rtpConnA,
		RTPConnB:       rtpConnB,
		Cancel:         responseCancel,
	}
	h.store.StoreEarly(early)

	cc := &callCtx{
		req: req, tx: tx, target: bobTarget, transportImpl: transportImpl, uac: uac,
		rtpConnA: rtpConnA, rtpConnB: rtpConnB,
		from: from, aliceFromTag: from.Tag, serverTag: serverTag, callID: callID,
		calleeTag: calleeTag, bobCallID: bobCallID, to: to,
		selectedPT: selectedPT, hasEarlyOffer: hasEarlyOffer,
		aliceSDPOffer: aliceSDPOffer, aliceSDPBytes: aliceSDPBytes,
		recordRoute: recordRoute,
	}

	go h.trunkResponseLoop(responseCtx, cc, bobInvite, trk.Name, bobReqURI, sessionExpires)

	log.Info("B2BUA: trunk INVITE sent",
		"trunk", trk.Name,
		"dest", bobReqURI,
		"transport", bobTransport,
		"rtpPortA", rtpConnA.LocalAddr().(*net.UDPAddr).Port,
		"rtpPortB", rtpConnB.LocalAddr().(*net.UDPAddr).Port)
}

func detect100relSupport(req *proto.SIPMessage, prackMgr *sip.ReliableProvisionalManager, log *slog.Logger) bool {
	if prackMgr == nil {
		return false
	}
	for _, v := range req.Headers["Supported"] {
		if strings.Contains(strings.ToLower(v), "100rel") {
			return true
		}
	}
	for _, v := range req.Headers["Require"] {
		if strings.Contains(strings.ToLower(v), "100rel") {
			log.Info("B2BUA: Alice requires 100rel, enabling reliable provisionals")
			return true
		}
	}
	return false
}

func (h *Handler) trunkResponseLoop(ctx context.Context, cc *callCtx,
	bobInvite *proto.SIPMessage, trunkName, bobReqURI string,
	sessionExpires time.Duration,
) {
	ctx = logutil.WithValues(ctx,
		"bobCallID", cc.bobCallID,
		"bobBranch", cc.uac.Branch,
		"trunk", trunkName)
	log := logutil.FromContext(ctx)

	log.Debug("B2BUA: trunk response loop started")

	defer h.store.RemoveEarly(cc.callID)

	if err := cc.uac.Send(bobInvite); err != nil {
		cc.rtpConnA.Close()
		cc.rtpConnB.Close()
		h.trunkMgr.ReleaseChannel(trunkName)
		log.Error("B2BUA: trunk INVITE send failed", "error", err)
		cc.tx.Respond(proto.NewResponse(cc.req, 502, "Bad Gateway"))
		return
	}
	log.Info("B2BUA: trunk INVITE sent", "dest", bobReqURI)

	for {
		select {
		case <-ctx.Done():
			log.Info("B2BUA: trunk response loop canceled")
			cc.rtpConnA.Close()
			cc.rtpConnB.Close()
			h.trunkMgr.ReleaseChannel(trunkName)
			return

		case resp := <-cc.uac.Responses:
			sc := resp.StatusCode()

			if sc >= 100 && sc < 200 {
				reason := resp.Status()
				if idx := strings.Index(reason, " "); idx != -1 {
					reason = reason[idx+1:]
				}
				prov := proto.NewResponse(cc.req, sc, reason)
				prov.Headers.Set("To", []string{fmt.Sprintf("<%s>;tag=%s", cc.to.URI, cc.serverTag)})
				if len(resp.Body) > 0 {
					prov.Body = resp.Body
					if ct := resp.Headers["Content-Type"]; len(ct) > 0 {
						prov.Headers["Content-Type"] = ct
					}
					prov.Headers.Set("Content-Length", []string{strconv.Itoa(len(resp.Body))})
				}
				cc.tx.Respond(prov)
				continue
			}

			if sc == 200 {
				h.handleTrunk200OK(ctx, cc, resp, trunkName, bobReqURI, sessionExpires)
				return
			}

			if sc >= 300 {
				cc.rtpConnA.Close()
				cc.rtpConnB.Close()
				h.trunkMgr.ReleaseChannel(trunkName)
				log.Info("B2BUA: trunk error response", "statusCode", sc, "reason", resp.Status())
				errReason := resp.Status()
				if idx := strings.Index(errReason, " "); idx != -1 {
					errReason = errReason[idx+1:]
				}
				errResp := proto.NewResponse(cc.req, sc, errReason)
				cc.tx.Respond(errResp)
				return
			}

		case err := <-cc.uac.Errors:
			cc.rtpConnA.Close()
			cc.rtpConnB.Close()
			h.trunkMgr.ReleaseChannel(trunkName)
			log.Error("B2BUA: trunk INVITE timed out", "error", err)
			cc.tx.Respond(proto.NewResponse(cc.req, 408, "Request Timeout"))
			return
		}
	}
}

func (h *Handler) handleTrunk200OK(ctx context.Context, cc *callCtx,
	resp *proto.SIPMessage, trunkName, bobReqURI string,
	sessionExpires time.Duration,
) {
	ctx = logutil.WithValues(ctx,
		"bobCallID", cc.bobCallID,
		"bobTo", resp.Headers.GetFirst("To"))
	log := logutil.FromContext(ctx)

	log.Debug("B2BUA: handling trunk 200 OK")

	if ctx.Err() != nil {
		log.Info("B2BUA: call was canceled, discarding trunk 200 OK")
		cc.rtpConnA.Close()
		cc.rtpConnB.Close()
		h.trunkMgr.ReleaseChannel(trunkName)
		return
	}

	bobTo, err := resp.To()
	if err != nil {
		cc.rtpConnA.Close()
		cc.rtpConnB.Close()
		h.trunkMgr.ReleaseChannel(trunkName)
		log.Error("B2BUA: missing To in trunk 200 OK")
		return
	}

	bobSDP, err := proto.UnmarshalSDPBytes(resp.Body)
	if err != nil {
		cc.rtpConnA.Close()
		cc.rtpConnB.Close()
		h.trunkMgr.ReleaseChannel(trunkName)
		log.Error("B2BUA: failed to parse trunk SDP", "error", err)
		return
	}
	bobIP, bobPort := media.ExtractRTPAddr(bobSDP)
	bobRTPAddr := &net.UDPAddr{IP: net.ParseIP(bobIP), Port: bobPort}
	log.Info("B2BUA: trunk RTP address", "ip", bobIP, "port", bobPort)

	ackToTrunk := proto.NewRequest(proto.SIPMethodACK, bobReqURI)
	ackToTrunk.Headers.Add("Via",
		fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s;rport",
			sip.TransportName(cc.transportImpl), h.serverIP, h.serverPort, sip.GenerateBranch()))
	ackToTrunk.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", cc.from.URI, cc.calleeTag))
	ackToTrunk.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s", cc.to.URI, bobTo.Tag))
	ackToTrunk.Headers.Add("Call-ID", cc.bobCallID)
	ackToTrunk.CSeq = proto.CSeq{Method: proto.SIPMethodACK, Seq: 1}
	ackToTrunk.Headers.Add("Max-Forwards", "70")
	ackToTrunk.Headers.Add("Content-Length", "0")
	if err := cc.transportImpl.Send(ackToTrunk, cc.target); err != nil {
		log.Error("B2BUA: failed to send ACK to trunk", "error", err)
	} else {
		log.Info("B2BUA: sent ACK to trunk", "dest", bobReqURI)
	}

	var alice200SDP []byte
	if cc.hasEarlyOffer {
		alice200SDP = cc.aliceSDPBytes
	} else {
		aliceOffer := media.BuildOffer(cc.rtpConnA.LocalAddr().(*net.UDPAddr).Port, cc.selectedPT, h.serverIP)
		alice200SDP, _ = aliceOffer.Marshal()
	}

	alice200 := proto.NewResponse(cc.req, 200, "OK")
	toHeader := cc.req.Headers.GetFirst("To")
	alice200.Headers.Set("To", []string{toHeader + ";tag=" + cc.serverTag})
	alice200.Body = alice200SDP
	alice200.Headers.Set("Content-Type", []string{"application/sdp"})
	alice200.Headers["Allow"] = []string{"INVITE, ACK, BYE, CANCEL, OPTIONS, REGISTER"}
	alice200.Headers.Add("Record-Route", cc.recordRoute)
	aliceContactHeader := fmt.Sprintf("<sip:trec@%s:%s;transport=%s>", h.serverIP, h.serverPort, sip.TransportName(cc.tx.Transport()))
	alice200.Headers.Add("Contact", aliceContactHeader)
	cc.tx.Respond(alice200)
	log.Info("B2BUA: sent 200 OK to Alice for trunk call")

	aliceKey := media.SessionKey{
		CallID:    cc.callID,
		RemoteTag: cc.aliceFromTag,
		LocalTag:  cc.serverTag,
	}
	aliceSess := media.NewSession(ctx, aliceKey, cc.rtpConnA, cc.selectedPT, cc.rtpConnA.LocalAddr())
	if cc.hasEarlyOffer {
		aIP, aPort := h.resolveClientAddr(cc.aliceSDPOffer)
		aRTPAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", aIP, aPort))
		if err != nil {
			log.Error("failed to resolve Alice RTP address", "clientIP", aIP, "error", err)
			return
		}
		aliceSess.SetRemoteAddr(aRTPAddr)
	}
	h.sm.Add(aliceSess)

	bobKey := media.SessionKey{
		CallID:    cc.bobCallID,
		RemoteTag: cc.calleeTag,
		LocalTag:  bobTo.Tag,
	}
	bobSess := media.NewSession(ctx, bobKey, cc.rtpConnB, cc.selectedPT, cc.rtpConnB.LocalAddr())
	bobSess.SetRemoteAddr(bobRTPAddr)
	h.sm.Add(bobSess)

	bridge := media.NewBridge(ctx, cc.rtpConnA, cc.rtpConnB)

	aliceContact := cc.req.Headers.GetFirst("Contact")
	serverContact := fmt.Sprintf("<sip:trec@%s:%s>", h.serverIP, h.serverPort)

	aliceDialogID := sip.DialogID{
		CallID:    cc.callID,
		LocalTag:  cc.serverTag,
		RemoteTag: cc.aliceFromTag,
	}
	aliceDialog := sip.NewDialog(aliceDialogID, serverContact, cc.from.URI, aliceContact)
	aliceDialog.SetState(sip.DialogStateConfirmed)

	bobDialogID := sip.DialogID{
		CallID:    cc.bobCallID,
		LocalTag:  cc.calleeTag,
		RemoteTag: bobTo.Tag,
	}
	bobDialog := sip.NewDialog(bobDialogID, serverContact, cc.to.URI, bobReqURI)
	bobDialog.SetState(sip.DialogStateConfirmed)

	aliceTarget := cc.tx.Target()
	call := &Call{
		AliceCallID:     cc.callID,
		BobCallID:       cc.bobCallID,
		Bridge:          bridge,
		AliceSess:       aliceSess,
		BobSess:         bobSess,
		BobRTPAddr:      bobRTPAddr,
		BobContactURI:   bobReqURI,
		BobTransport:    cc.transportImpl,
		BobTarget:       cc.target,
		BobCalleeTag:    cc.calleeTag,
		BobRemoteTag:    bobTo.Tag,
		AliceFromTag:    cc.aliceFromTag,
		AliceServerTag:  cc.serverTag,
		AliceContactURI: aliceContact,
		AliceTarget:     &aliceTarget,
		AliceDialog:     aliceDialog,
		BobDialog:       bobDialog,
		AliceTransport:  cc.tx.Transport(),
		TrunkName:       trunkName,
	}

	if cc.hasEarlyOffer {
		aIP, aPort := h.resolveClientAddr(cc.aliceSDPOffer)
		aRTPAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", aIP, aPort))
		if err != nil {
			log.Error("failed to resolve Alice RTP address", "clientIP", aIP, "error", err)
			return
		}
		bridge.SetARemote(aRTPAddr)
		bridge.SetBRemote(bobRTPAddr)
		bridge.Start()
		call.BridgeReady = true
		aliceSess.SetState(media.SessionActive)
		log.Debug("B2BUA: trunk bridge started (early offer)")
	} else {
		aliceSess.SetState(media.SessionWaitingAck)
		log.Debug("B2BUA: trunk waiting for Alice ACK (delayed offer)")
	}

	h.store.Store(call)

	if sessionExpires > 0 {
		go h.trunkSessionTimer(ctx, cc.callID, trunkName, sessionExpires)
	}
}

func (h *Handler) trunkSessionTimer(ctx context.Context, callID, trunkName string, sessionExpires time.Duration) {
	log := logutil.FromContext(ctx).With("component", "session_timer", "callID", callID, "trunk", trunkName)

	select {
	case <-ctx.Done():
		return
	case <-time.After(sessionExpires):
	}

	log.Info("session timer expired, tearing down call")

	call := h.store.Get(callID)
	if call == nil {
		log.Debug("session timer: call already cleaned up")
		return
	}

	serverPort := h.serverPort

	// Send BYE to Alice
	fwdBye := proto.NewRequest(proto.SIPMethodBYE, sip.StripBrackets(call.AliceContactURI))
	fwdBye.Headers.Add("Via",
		fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s",
			sip.TransportName(call.AliceTransport), h.serverIP, serverPort, sip.GenerateBranch()))
	fwdBye.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s",
		sip.StripBrackets(call.AliceDialog.LocalURI), call.AliceDialog.ID.LocalTag))
	fwdBye.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s",
		sip.StripBrackets(call.AliceDialog.RemoteURI), call.AliceDialog.ID.RemoteTag))
	fwdBye.Headers.Add("Call-ID", call.AliceDialog.ID.CallID)
	fwdBye.CSeq = proto.CSeq{Method: proto.SIPMethodBYE, Seq: 2}
	fwdBye.Headers.Add("Max-Forwards", "70")
	fwdBye.Headers.Add("Content-Length", "0")

	if err := call.AliceTransport.Send(fwdBye, call.AliceTarget); err != nil {
		log.Warn("session timer: failed to send BYE to Alice", "error", err)
	} else {
		log.Info("session timer: sent BYE to Alice")
	}

	// Clean up
	call.Bridge.Stop()

	if call.AliceSess != nil {
		call.AliceSess.Cancel()
		call.AliceSess.RTPConn.Close()
		h.sm.Remove(call.AliceSess.Key)
	}
	if call.BobSess != nil {
		call.BobSess.Cancel()
		call.BobSess.RTPConn.Close()
		h.sm.Remove(call.BobSess.Key)
	}

	h.store.Remove(call.AliceCallID)

	if h.trunkMgr != nil {
		h.trunkMgr.ReleaseChannel(trunkName)
		log.Debug("session timer: released trunk channel")
	}
}

func (h *Handler) b2buaResponseLoop(ctx context.Context, cc *callCtx,
	bobInvite *proto.SIPMessage, binding *sip.Binding, aliceSupports100rel bool,
) {
	ctx = logutil.WithValues(ctx,
		"bobCallID", cc.bobCallID,
		"bobBranch", cc.uac.Branch,
		"bobContact", binding.ContactURI)
	log := logutil.FromContext(ctx)

	log.Debug("B2BUA: response loop started")

	// Clean up the early call tracking when the response loop exits
	// (whether by answer, error, or cancellation).
	defer h.store.RemoveEarly(cc.callID)

	if err := cc.uac.Send(bobInvite); err != nil {
		cc.rtpConnA.Close()
		cc.rtpConnB.Close()
		log.Error("B2BUA: failed to send INVITE to Bob", "error", err)
		cc.tx.Respond(proto.NewResponse(cc.req, 502, "Bad Gateway"))
		return
	}
	log.Info("B2BUA: sent INVITE to Bob", "contact", binding.ContactURI, "branch", cc.uac.Branch)

	defer func() {
		h.cancelPRACK(cc.callID)
	}()

	prackDone := make(chan struct{})

	for {
		select {
		case <-prackDone:
			log.Warn("B2BUA: response loop exiting due to PRACK timeout")
			return
		case <-ctx.Done():
			log.Info("B2BUA: response loop canceled")
			cc.rtpConnA.Close()
			cc.rtpConnB.Close()
			h.cancelPRACK(cc.callID)
			return
		case resp := <-cc.uac.Responses:
			sc := resp.StatusCode()

			if sc >= 100 && sc < 200 {
				if sc == 180 || sc == 183 {
					log.Info("B2BUA: Bob progress, forwarding to Alice", "statusCode", sc, "reason", resp.Status())
					reason := resp.Status()
					if idx := strings.Index(reason, " "); idx != -1 {
						reason = reason[idx+1:]
					}
					prov := proto.NewResponse(cc.req, sc, reason)
					prov.Headers.Set("To", []string{fmt.Sprintf("<%s>;tag=%s", cc.to.URI, cc.serverTag)})
					if len(resp.Body) > 0 {
						prov.Body = resp.Body
						if ct := resp.Headers["Content-Type"]; len(ct) > 0 {
							prov.Headers["Content-Type"] = ct
						}
						prov.Headers.Set("Content-Length", []string{strconv.Itoa(len(resp.Body))})
					}

					// UAC side: send PRACK if Bob sent a reliable provisional
					bobRequire := resp.Headers.GetFirst("Require")
					bobRSeq := resp.Headers.GetFirst("RSeq")
					needsPRACK := bobRequire != "" && bobRSeq != "" &&
						strings.Contains(strings.ToLower(bobRequire), "100rel")
					if needsPRACK && h.prackMgr != nil {
						rseqVal := bobRSeq
						cseqVal := strconv.Itoa(resp.CSeq.Seq)
						bobRespTo, _ := resp.To()
						bobRespTag := ""
						if bobRespTo != nil {
							bobRespTag = bobRespTo.Tag
						}
						prack := proto.NewRequest(proto.SIPMethodPRACK, binding.ContactURI)
						prack.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s",
							sip.TransportName(cc.transportImpl), h.serverIP, h.serverPort, sip.GenerateBranch()))
						prack.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", cc.from.URI, cc.calleeTag))
						prack.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s", cc.to.URI, bobRespTag))
						prack.Headers.Add("Call-ID", cc.bobCallID)
						prack.CSeq = proto.CSeq{Method: proto.SIPMethodPRACK, Seq: resp.CSeq.Seq + 1}
						prack.Headers.Add("RAck", rseqVal+" "+cseqVal+" INVITE")
						prack.Headers.Add("Max-Forwards", "70")
						prack.Headers.Add("Content-Length", "0")

						prackTx := h.uacMgr.NewTransaction(ctx, proto.SIPMethodPRACK, cc.transportImpl, cc.target)
						if err := prackTx.Send(prack); err != nil {
							log.Error("B2BUA: failed to send PRACK", "error", err)
						} else {
							log.Info("B2BUA: sent PRACK for Bob's reliable provisional", "rseq", rseqVal)
						}
					}

					// UAS side: send reliable provisional if Alice supports 100rel
					if aliceSupports100rel && h.prackMgr != nil {
						prackTimeout := func() {
							log.Warn("B2BUA: PRACK timeout, tearing down call", "rseq",
								prov.Headers.GetFirst("RSeq"))
							cc.rtpConnA.Close()
							cc.rtpConnB.Close()
							cc.tx.Respond(proto.NewResponse(cc.req, 504, "PRACK Timeout"))
							close(prackDone)
						}
						h.prackMgr.SendReliable(ctx, cc.tx, prov, cc.callID, prackTimeout)
					} else {
						cc.tx.Respond(prov)
					}
				}
				continue
			}

			if sc == 200 {
				h.cancelPRACK(cc.callID)
				h.handleBob200OK(ctx, cc, resp, binding)
				return
			}

			if sc >= 300 {
				h.cancelPRACK(cc.callID)
				cc.rtpConnA.Close()
				cc.rtpConnB.Close()
				log.Info("B2BUA: Bob error response, forwarding to Alice", "statusCode", sc, "reason", resp.Status())
				errReason := resp.Status()
				if idx := strings.Index(errReason, " "); idx != -1 {
					errReason = errReason[idx+1:]
				}
				errResp := proto.NewResponse(cc.req, sc, errReason)
				cc.tx.Respond(errResp)
				return
			}

		case err := <-cc.uac.Errors:
			h.cancelPRACK(cc.callID)
			cc.rtpConnA.Close()
			cc.rtpConnB.Close()
			log.Error("B2BUA: Bob INVITE timed out", "error", err)
			cc.tx.Respond(proto.NewResponse(cc.req, 408, "Request Timeout"))
			return
		}
	}
}

func (h *Handler) handleBob200OK(ctx context.Context, cc *callCtx,
	resp *proto.SIPMessage, binding *sip.Binding,
) {
	ctx = logutil.WithValues(ctx,
		"bobCallID", cc.bobCallID,
		"bobTo", resp.Headers.GetFirst("To"),
		"bobHasSDP", len(resp.Body) > 0)
	log := logutil.FromContext(ctx)

	log.Debug("B2BUA: handling Bob 200 OK")
	log.Info("B2BUA: Bob answered 200 OK")

	// Guard: if the response loop was canceled (e.g. by concurrent CANCEL
	// from Alice), discard Bob's answer per RFC 3261 §12.1.1.
	if ctx.Err() != nil {
		log.Info("B2BUA: call was canceled, discarding Bob's 200 OK")
		cc.rtpConnA.Close()
		cc.rtpConnB.Close()
		return
	}

	bobTo, err := resp.To()
	if err != nil {
		cc.rtpConnA.Close()
		cc.rtpConnB.Close()
		log.Error("B2BUA: missing To in Bob's 200 OK")
		return
	}

	bobSDP, err := proto.UnmarshalSDPBytes(resp.Body)
	if err != nil {
		cc.rtpConnA.Close()
		cc.rtpConnB.Close()
		log.Error("B2BUA: failed to parse Bob's SDP", "error", err)
		return
	}
	bobIP, bobPort := media.ExtractRTPAddr(bobSDP)
	bobRTPAddr := &net.UDPAddr{IP: net.ParseIP(bobIP), Port: bobPort}
	log.Info("B2BUA: Bob RTP address", "ip", bobIP, "port", bobPort)

	ackToBob := proto.NewRequest(proto.SIPMethodACK, binding.ContactURI)
	ackToBob.Headers.Add("Via",
		fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s;rport",
			sip.TransportName(cc.transportImpl), h.serverIP, h.serverPort, sip.GenerateBranch()))
	ackToBob.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", cc.from.URI, cc.calleeTag))
	ackToBob.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s", cc.to.URI, bobTo.Tag))
	ackToBob.Headers.Add("Call-ID", cc.bobCallID)
	ackToBob.CSeq = proto.CSeq{Method: proto.SIPMethodACK, Seq: 1}
	ackToBob.Headers.Add("Max-Forwards", "70")
	ackToBob.Headers.Add("Content-Length", "0")
	if err := cc.transportImpl.Send(ackToBob, cc.target); err != nil {
		log.Error("B2BUA: failed to send ACK to Bob", "error", err)
	} else {
		log.Info("B2BUA: sent ACK to Bob", "contact", binding.ContactURI)
	}

	var alice200SDP []byte
	if cc.hasEarlyOffer {
		alice200SDP = cc.aliceSDPBytes
	} else {
		aliceOffer := media.BuildOffer(cc.rtpConnA.LocalAddr().(*net.UDPAddr).Port, cc.selectedPT, h.serverIP)
		alice200SDP, _ = aliceOffer.Marshal()
	}

	alice200 := proto.NewResponse(cc.req, 200, "OK")
	toHeader := cc.req.Headers.GetFirst("To")
	alice200.Headers.Set("To", []string{toHeader + ";tag=" + cc.serverTag})
	alice200.Body = alice200SDP
	alice200.Headers.Set("Content-Type", []string{"application/sdp"})
	alice200.Headers["Allow"] = []string{"INVITE, ACK, BYE, CANCEL, OPTIONS, REGISTER"}
	alice200.Headers.Add("Record-Route", cc.recordRoute)
	aliceContactHeader := fmt.Sprintf("<sip:trec@%s:%s;transport=%s>", h.serverIP, h.serverPort, sip.TransportName(cc.tx.Transport()))
	alice200.Headers.Add("Contact", aliceContactHeader)
	cc.tx.Respond(alice200)
	log.Info("B2BUA: sent 200 OK to Alice")

	aliceKey := media.SessionKey{
		CallID:    cc.callID,
		RemoteTag: cc.aliceFromTag,
		LocalTag:  cc.serverTag,
	}
	aliceSess := media.NewSession(ctx, aliceKey, cc.rtpConnA, cc.selectedPT, cc.rtpConnA.LocalAddr())
	if cc.hasEarlyOffer {
		aIP, aPort := h.resolveClientAddr(cc.aliceSDPOffer)
		aRTPAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", aIP, aPort))
		if err != nil {
			log.Error("failed to resolve Alice RTP address", "clientIP", aIP, "error", err)
			return
		}
		aliceSess.SetRemoteAddr(aRTPAddr)
	}
	h.sm.Add(aliceSess)

	bobKey := media.SessionKey{
		CallID:    cc.bobCallID,
		RemoteTag: cc.calleeTag,
		LocalTag:  bobTo.Tag,
	}
	bobSess := media.NewSession(ctx, bobKey, cc.rtpConnB, cc.selectedPT, cc.rtpConnB.LocalAddr())
	bobSess.SetRemoteAddr(bobRTPAddr)
	h.sm.Add(bobSess)

	bridge := media.NewBridge(ctx, cc.rtpConnA, cc.rtpConnB)

	aliceContact := cc.req.Headers.GetFirst("Contact")

	serverContact := fmt.Sprintf("<sip:trec@%s:%s>", h.serverIP, h.serverPort)

	aliceDialogID := sip.DialogID{
		CallID:    cc.callID,
		LocalTag:  cc.serverTag,
		RemoteTag: cc.aliceFromTag,
	}
	aliceDialog := sip.NewDialog(aliceDialogID, serverContact, cc.from.URI, aliceContact)
	aliceDialog.SetState(sip.DialogStateConfirmed)

	bobDialogID := sip.DialogID{
		CallID:    cc.bobCallID,
		LocalTag:  cc.calleeTag,
		RemoteTag: bobTo.Tag,
	}
	bobDialog := sip.NewDialog(bobDialogID, serverContact, cc.to.URI, binding.ContactURI)
	bobDialog.SetState(sip.DialogStateConfirmed)

	aliceTarget := cc.tx.Target()
	call := &Call{
		AliceCallID:     cc.callID,
		BobCallID:       cc.bobCallID,
		Bridge:          bridge,
		AliceSess:       aliceSess,
		BobSess:         bobSess,
		BobRTPAddr:      bobRTPAddr,
		BobContactURI:   binding.ContactURI,
		BobTransport:    cc.transportImpl,
		BobTarget:       cc.target,
		BobCalleeTag:    cc.calleeTag,
		BobRemoteTag:    bobTo.Tag,
		AliceFromTag:    cc.aliceFromTag,
		AliceServerTag:  cc.serverTag,
		AliceContactURI: aliceContact,
		AliceTarget:     &aliceTarget,
		AliceDialog:     aliceDialog,
		BobDialog:       bobDialog,
		AliceTransport:  cc.tx.Transport(),
	}

	if cc.hasEarlyOffer {
		aIP, aPort := h.resolveClientAddr(cc.aliceSDPOffer)
		aRTPAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", aIP, aPort))
		if err != nil {
			log.Error("failed to resolve Alice RTP address", "clientIP", aIP, "error", err)
			return
		}
		bridge.SetARemote(aRTPAddr)
		bridge.SetBRemote(bobRTPAddr)
		bridge.Start()
		call.BridgeReady = true
		aliceSess.SetState(media.SessionActive)
		log.Debug("B2BUA: bridge started (early offer)")
	} else {
		aliceSess.SetState(media.SessionWaitingAck)
		log.Debug("B2BUA: waiting for Alice ACK with SDP (delayed offer)")
	}

	h.store.Store(call)
}

// HandleAck handles incoming ACK requests, routing to echo or B2BUA.
func (h *Handler) HandleAck(ctx context.Context, msg *proto.SIPMessage, target sip.Target, transport sip.Transport) {
	ctx = logutil.WithValues(ctx,
		"callID", msg.Headers.GetFirst("Call-ID"),
		"from", msg.Headers.GetFirst("From"),
		"to", msg.Headers.GetFirst("To"),
		"hasSDP", len(msg.Body) > 0)
	log := logutil.FromContext(ctx)

	log.Debug("ACK received")

	if call := h.checkB2BUAAck(log, msg); call != nil {
		return
	}

	from, err := msg.From()
	if err != nil {
		return
	}
	to, err := msg.To()
	if err != nil {
		return
	}

	key := media.SessionKey{
		CallID:    msg.Headers.GetFirst("Call-ID"),
		RemoteTag: from.Tag,
		LocalTag:  to.Tag,
	}

	session := h.sm.Get(key)
	if session == nil {
		return
	}

	if session.StateSafe() != media.SessionWaitingAck {
		return
	}

	if len(msg.Body) > 0 {
		sdp, err := proto.UnmarshalSDPBytes(msg.Body)
		if err != nil {
			return
		}
		clientIP, clientPort := media.ExtractRTPAddr(sdp)
		remoteAddr := &net.UDPAddr{IP: net.ParseIP(clientIP), Port: clientPort}
		session.SetRemoteAddr(remoteAddr)
		session.SetState(media.SessionActive)

		switch session.Kind {
		case media.SessionKindPlay:
			done := media.RunFilePlayback(session.Ctx(), session.RTPConn, remoteAddr, session.PayloadType, session.WavData)
			go func() {
				<-done
				if session.Ctx().Err() == nil {
					h.sendByeToSession(session.Ctx(), session, session.Key.CallID)
				}
			}()
		default:
			go media.RunEcho(session.Ctx(), session.RTPConn, session.PayloadType)
		}
	}
}

func (h *Handler) checkB2BUAAck(log *slog.Logger, msg *proto.SIPMessage) *Call {
	callID := msg.Headers.GetFirst("Call-ID")
	call := h.store.Get(callID)
	if call == nil {
		return nil
	}

	if call.BridgeReady {
		return call
	}

	if len(msg.Body) == 0 {
		log.Warn("B2BUA: ACK has no SDP body (delayed offer)")
		return call
	}

	sdp, err := proto.UnmarshalSDPBytes(msg.Body)
	if err != nil {
		log.Error("B2BUA: failed to parse ACK SDP", "error", err)
		return call
	}

	clientIP, clientPort := media.ExtractRTPAddr(sdp)
	aRTPAddr := &net.UDPAddr{IP: net.ParseIP(clientIP), Port: clientPort}

	call.Bridge.SetARemote(aRTPAddr)
	call.Bridge.SetBRemote(call.BobRTPAddr)
	call.BridgeReady = true
	call.Bridge.Start()

	call.AliceSess.SetRemoteAddr(aRTPAddr)
	call.AliceSess.SetState(media.SessionActive)
	log.Info("B2BUA: bridge started (delayed offer)")

	return call
}

// HandleBye handles incoming BYE requests, forwarding to the other leg.
func (h *Handler) HandleBye(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction) {
	callID := req.Headers.GetFirst("Call-ID")
	ctx = logutil.WithValues(ctx,
		"callID", callID,
		"from", req.Headers.GetFirst("From"),
		"to", req.Headers.GetFirst("To"),
		"cseq", req.Headers.GetFirst("CSeq"))
	log := logutil.FromContext(ctx)

	log.Debug("BYE received")

	trying := proto.NewResponse(req, 100, "Trying")
	tx.Respond(trying)

	if h.requireProxyAuth(ctx, req, tx, "BYE") == nil && h.proxyPasswd != nil {
		return
	}

	serverPort := h.serverPort

	call := h.store.Get(req.Headers.GetFirst("Call-ID"))
	if call != nil {
		log.Debug("B2BUA: BYE forwarding to other leg")
		call.Bridge.Stop()

		isFromAlice := callID == call.AliceCallID

		var dlg *sip.Dialog
		var fwdTransport sip.Transport
		var fwdTargetObj *sip.Target
		var fwdRequestURI string
		var viaTransport string

		if isFromAlice {
			dlg = call.BobDialog
			fwdRequestURI = sip.StripBrackets(call.BobContactURI)
			fwdTransport = call.BobTransport
			fwdTargetObj = call.BobTarget
			viaTransport = sip.TransportName(fwdTransport)
		} else {
			dlg = call.AliceDialog
			fwdRequestURI = sip.StripBrackets(call.AliceContactURI)
			fwdTargetObj = call.AliceTarget
			if fwdTargetObj == nil {
				var err error
				fwdTargetObj, _, err = sip.TargetFromContact(fwdRequestURI)
				if err != nil {
					log.Error("B2BUA: failed to resolve Alice Contact", "contact", fwdRequestURI, "error", err)
					fwdTargetObj = &sip.Target{}
				}
			}
			fwdTransport = call.AliceTransport
			viaTransport = sip.TransportName(fwdTransport)
		}

		fwdBye := proto.NewRequest(proto.SIPMethodBYE, fwdRequestURI)
		fwdBye.Headers.Add("Via",
			fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s",
				viaTransport, h.serverIP, serverPort, sip.GenerateBranch()))
		fwdBye.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s",
			sip.StripBrackets(dlg.LocalURI), dlg.ID.LocalTag))
		fwdBye.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s",
			sip.StripBrackets(dlg.RemoteURI), dlg.ID.RemoteTag))
		fwdBye.Headers.Add("Call-ID", dlg.ID.CallID)
		fwdBye.CSeq = proto.CSeq{Method: proto.SIPMethodBYE, Seq: 2}
		fwdBye.Headers.Add("Max-Forwards", "70")
		fwdBye.Headers.Add("Content-Length", "0")

		if err := fwdTransport.Send(fwdBye, fwdTargetObj); err != nil {
			log.Error("B2BUA: failed to forward BYE", "error", err)
		} else {
			log.Info("B2BUA: forwarded BYE")
		}

		if call.AliceDialog != nil {
			call.AliceDialog.SetState(sip.DialogStateTerminated)
		}
		if call.BobDialog != nil {
			call.BobDialog.SetState(sip.DialogStateTerminated)
		}

		if call.BobSess != nil {
			call.BobSess.Cancel()
			call.BobSess.RTPConn.Close()
			h.sm.Remove(call.BobSess.Key)
		}
		if call.AliceSess != nil {
			call.AliceSess.Cancel()
			call.AliceSess.RTPConn.Close()
			h.sm.Remove(call.AliceSess.Key)
		}

		h.store.Remove(call.AliceCallID)

		if call.TrunkName != "" && h.trunkMgr != nil {
			h.trunkMgr.ReleaseChannel(call.TrunkName)
			log.Debug("B2BUA: released trunk channel", "trunk", call.TrunkName)
		}
	} else {
		from, err := req.From()
		if err == nil {
			to, err := req.To()
			if err == nil {
				key := media.SessionKey{
					CallID:    callID,
					RemoteTag: from.Tag,
					LocalTag:  to.Tag,
				}
				session := h.sm.Get(key)
				if session != nil {
					session.Cancel()
					session.RTPConn.Close()
					h.sm.Remove(key)
				}
			}
		}
	}

	res := proto.NewResponse(req, 200, "OK")
	res.Headers["Allow"] = []string{h.server.AllowMethods()}
	tx.Respond(res)
	log.Debug("BYE responded", "statusCode", 200)
}

// HandleCancel handles incoming CANCEL requests for B2BUA calls.
// The transaction layer has already sent 200 OK for the CANCEL.
// This handler sends 487 Request Terminated for the INVITE using
// the correct transport (ist.transport) and To tag, then propagates
// the CANCEL to Bob and cleans up early call resources.
func (h *Handler) HandleCancel(ctx context.Context, req *proto.SIPMessage, tx sip.Transaction) {
	callID := req.Headers.GetFirst("Call-ID")
	ctx = logutil.WithValues(ctx,
		"callID", callID,
		"from", req.Headers.GetFirst("From"),
		"to", req.Headers.GetFirst("To"))
	log := logutil.FromContext(ctx)

	log.Debug("CANCEL received in B2BUA handler")

	// If the call is already established, CANCEL is too late.
	if call := h.store.Get(callID); call != nil {
		log.Debug("CANCEL: call already answered, ignoring")
		return
	}

	// Look up the early (ringing) call.
	early := h.store.GetEarly(callID)

	// Build and send 487 Request Terminated with the correct To tag
	// per RFC 3261 §12.1.1 and using ist.transport (fixes #30 review).
	// This must always be done: the transaction layer already sent 200 OK
	// for the CANCEL and delegated INVITE termination to this handler. In
	// the race window where CANCEL arrives before handleB2BUAInvite stores
	// the EarlyCall, we still terminate the INVITE transaction even though
	// there is no Bob leg to propagate the CANCEL to.
	inviteResp := proto.NewResponse(req, 487, "Request Terminated")
	inviteResp.CSeq = proto.CSeq{Method: proto.SIPMethodINVITE, Seq: req.CSeq.Seq}
	toHeader := req.Headers.GetFirst("To")
	if !strings.Contains(toHeader, "tag=") {
		serverTag := "trecs-cancel"
		if early != nil {
			serverTag = early.AliceServerTag
		}
		toHeader = fmt.Sprintf("%s;tag=%s", toHeader, serverTag)
		inviteResp.Headers.Set("To", []string{toHeader})
	}
	tx.Respond(inviteResp)

	if early == nil {
		log.Debug("CANCEL: no pending call found, 487 sent")
		return
	}

	// Cancel the response loop context to abort the pending INVITE to Bob.
	early.Cancel()

	// Send CANCEL to Bob if the UAC transaction is still pending.
	if err := early.BobTx.SendCancel(); err != nil {
		log.Error("CANCEL: failed to send CANCEL to Bob", "error", err)
	} else {
		log.Info("CANCEL: sent CANCEL to Bob")
	}

	// Close RTP connections (Close is idempotent; response loop may also close them).
	early.RTPConnA.Close()
	early.RTPConnB.Close()

	// Remove from early store and cancel pending PRACK.
	h.store.RemoveEarly(callID)
	h.cancelPRACK(callID)

	log.Info("CANCEL: handled, propagation to Bob complete")
}

// HandleResponse routes incoming SIP responses to the UAC manager.
func (h *Handler) HandleResponse(ctx context.Context, msg *proto.SIPMessage, target sip.Target, transport sip.Transport) {
	ctx = logutil.WithValues(ctx,
		"statusCode", msg.StatusCode(),
		"reason", msg.Status(),
		"callID", msg.Headers.GetFirst("Call-ID"),
		"cseq", msg.Headers.GetFirst("CSeq"))
	log := logutil.FromContext(ctx)

	log.Debug("Response received")

	h.uacMgr.HandleResponse(msg)
}
