package b2bua

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/thorsager/trecs/internal/dialplan"
	"github.com/thorsager/trecs/internal/logutil"
	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/internal/sip"
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
}

// Handler implements SIP request handlers for the T-REC B2BUA server,
// including echo service, file playback, B2BUA call bridging, and call teardown.
type Handler struct {
	reg        *sip.Registrar
	sm         *media.SessionManager
	server     *sip.Server
	serverIP   string
	serverAddr string
	uacMgr     *sip.UACManager
	dp         dialplan.Dialplan
	rtpMin     int
	rtpMax     int
	store      *Store
}

// NewHandler creates a new B2BUA handler with the given configuration.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		reg:        cfg.Registrar,
		sm:         cfg.SessionManager,
		server:     cfg.Server,
		serverIP:   cfg.ServerIP,
		serverAddr: cfg.ServerAddr,
		uacMgr:     cfg.UACManager,
		dp:         cfg.Dialplan,
		rtpMin:     cfg.RTPPortMin,
		rtpMax:     cfg.RTPPortMax,
		store:      NewStore(),
	}
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
	res.Headers["Supported"] = []string{"timer"}
	tx.Respond(res)

	log.Debug("OPTIONS responded", "statusCode", 200)
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
		clientIP, clientPort := media.ExtractRTPAddr(sdpOffer)
		remoteAddr := &net.UDPAddr{IP: net.ParseIP(clientIP), Port: clientPort}
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
		clientIP, clientPort := media.ExtractRTPAddr(sdpOffer)
		remoteAddr := &net.UDPAddr{IP: net.ParseIP(clientIP), Port: clientPort}
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

	_, serverPort, _ := net.SplitHostPort(h.serverAddr)
	if serverPort == "" {
		serverPort = "5060"
	}

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

	aor := sip.NormalizeAOR(to.URI)
	bindings := h.reg.GetBindings(aor)
	if len(bindings) == 0 {
		log.Info("B2BUA: no binding", "aor", aor, "target", to.URI)
		res := proto.NewResponse(req, 404, "Not Found")
		tx.Respond(res)
		return
	}

	var binding *sip.Binding
	var target *sip.Target
	var transport string

	// Prefer bindings with live TCP flows, then newest bindings.
	for i := len(bindings) - 1; i >= 0; i-- {
		b := bindings[i]

		if b.OB && b.FlowID != "" {
			if fc := h.server.Pool().GetByFlowID(b.FlowID); fc != nil {
				binding = b
				target = &sip.Target{Conn: fc.Conn, FlowID: fc.Key.String()}
				transport = "TCP"
				log.Info("B2BUA: reusing TCP flow", "flowID", b.FlowID, "contact", b.ContactURI)
				break
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

		binding = b
		target = tgt
		transport = tr
		break
	}

	if target == nil {
		log.Warn("B2BUA: no reachable binding", "aor", aor)
		res := proto.NewResponse(req, 502, "Bad Gateway")
		tx.Respond(res)
		return
	}

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

	_, serverPort, _ := net.SplitHostPort(h.serverAddr)
	if serverPort == "" {
		serverPort = "5060"
	}
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
	bobInvite.Body = bobSDPBytes

	go h.b2buaResponseLoop(ctx, req, tx, target, transportImpl, uac,
		rtpConnA, rtpConnB, from, aliceFromTag, serverTag, callID,
		calleeTag, bobCallID, to, selectedPT, hasEarlyOffer,
		aliceSDPOffer, aliceSDPBytes, recordRoute,
		bobInvite, serverPort, binding)

	log.Debug("B2BUA: INVITE sent to Bob",
		"bobContact", binding.ContactURI,
		"transport", transport,
		"rtpPortA", rtpConnA.LocalAddr().(*net.UDPAddr).Port,
		"rtpPortB", rtpConnB.LocalAddr().(*net.UDPAddr).Port)
}

func (h *Handler) b2buaResponseLoop(ctx context.Context,
	req *proto.SIPMessage, tx sip.Transaction,
	target *sip.Target, transportImpl sip.Transport, uac *sip.UACTransaction,
	rtpConnA, rtpConnB *media.RTPConn,
	from *proto.SIPAddress, aliceFromTag, serverTag, callID string,
	calleeTag, bobCallID string, to *proto.SIPAddress,
	selectedPT uint8, hasEarlyOffer bool,
	aliceSDPOffer *proto.SDP, aliceSDPBytes []byte,
	recordRoute string,
	bobInvite *proto.SIPMessage, serverPort string,
	binding *sip.Binding,
) {
	ctx = logutil.WithValues(ctx,
		"bobCallID", bobCallID,
		"bobBranch", uac.Branch,
		"bobContact", binding.ContactURI)
	log := logutil.FromContext(ctx)

	log.Debug("B2BUA: response loop started")

	defer rtpConnA.Close()
	defer rtpConnB.Close()

	if err := uac.Send(bobInvite); err != nil {
		log.Error("B2BUA: failed to send INVITE to Bob", "error", err)
		tx.Respond(proto.NewResponse(req, 502, "Bad Gateway"))
		return
	}
	log.Info("B2BUA: sent INVITE to Bob", "contact", binding.ContactURI, "branch", uac.Branch)

	for {
		select {
		case resp := <-uac.Responses:
			sc := resp.StatusCode()

			if sc >= 100 && sc < 200 {
				if sc == 180 || sc == 183 {
					log.Info("B2BUA: Bob progress, forwarding to Alice", "statusCode", sc, "reason", resp.Status())
					prov := proto.NewResponse(req, sc, resp.Status())
					tx.Respond(prov)
				}
				continue
			}

			if sc == 200 {
				h.handleBob200OK(ctx, resp, req, tx, target, transportImpl,
					rtpConnA, rtpConnB, from, aliceFromTag, serverTag, callID,
					calleeTag, bobCallID, to, selectedPT, hasEarlyOffer,
					aliceSDPOffer, aliceSDPBytes, recordRoute, serverPort,
					binding)
				return
			}

			if sc >= 300 {
				log.Info("B2BUA: Bob error response, forwarding to Alice", "statusCode", sc, "reason", resp.Status())
				errResp := proto.NewResponse(req, sc, resp.Status())
				tx.Respond(errResp)
				return
			}

		case err := <-uac.Errors:
			log.Error("B2BUA: Bob INVITE timed out", "error", err)
			tx.Respond(proto.NewResponse(req, 408, "Request Timeout"))
			return
		}
	}
}

func (h *Handler) handleBob200OK(
	ctx context.Context,
	resp *proto.SIPMessage, req *proto.SIPMessage, tx sip.Transaction,
	target *sip.Target, transportImpl sip.Transport,
	rtpConnA, rtpConnB *media.RTPConn,
	from *proto.SIPAddress, aliceFromTag, serverTag, callID string,
	calleeTag, bobCallID string, to *proto.SIPAddress,
	selectedPT uint8, hasEarlyOffer bool,
	aliceSDPOffer *proto.SDP, aliceSDPBytes []byte,
	recordRoute string, serverPort string,
	binding *sip.Binding,
) {
	ctx = logutil.WithValues(ctx,
		"bobCallID", bobCallID,
		"bobTo", resp.Headers.GetFirst("To"),
		"bobHasSDP", len(resp.Body) > 0)
	log := logutil.FromContext(ctx)

	log.Debug("B2BUA: handling Bob 200 OK")
	log.Info("B2BUA: Bob answered 200 OK")

	bobTo, err := resp.To()
	if err != nil {
		log.Error("B2BUA: missing To in Bob's 200 OK")
		return
	}

	bobSDP, err := proto.UnmarshalSDPBytes(resp.Body)
	if err != nil {
		log.Error("B2BUA: failed to parse Bob's SDP", "error", err)
		return
	}
	bobIP, bobPort := media.ExtractRTPAddr(bobSDP)
	bobRTPAddr := &net.UDPAddr{IP: net.ParseIP(bobIP), Port: bobPort}
	log.Info("B2BUA: Bob RTP address", "ip", bobIP, "port", bobPort)

	ackToBob := proto.NewRequest(proto.SIPMethodACK, binding.ContactURI)
	ackToBob.Headers.Add("Via",
		fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s;rport",
			sip.TransportName(transportImpl), h.serverIP, serverPort, sip.GenerateBranch()))
	ackToBob.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", from.URI, calleeTag))
	ackToBob.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s", to.URI, bobTo.Tag))
	ackToBob.Headers.Add("Call-ID", bobCallID)
	ackToBob.CSeq = proto.CSeq{Method: proto.SIPMethodACK, Seq: 1}
	ackToBob.Headers.Add("Max-Forwards", "70")
	ackToBob.Headers.Add("Content-Length", "0")
	if err := transportImpl.Send(ackToBob, target); err != nil {
		log.Error("B2BUA: failed to send ACK to Bob", "error", err)
	} else {
		log.Info("B2BUA: sent ACK to Bob", "contact", binding.ContactURI)
	}

	var alice200SDP []byte
	if hasEarlyOffer {
		alice200SDP = aliceSDPBytes
	} else {
		aliceOffer := media.BuildOffer(rtpConnA.LocalAddr().(*net.UDPAddr).Port, selectedPT, h.serverIP)
		alice200SDP, _ = aliceOffer.Marshal()
	}

	alice200 := proto.NewResponse(req, 200, "OK")
	toHeader := req.Headers.GetFirst("To")
	alice200.Headers.Set("To", []string{toHeader + ";tag=" + serverTag})
	alice200.Body = alice200SDP
	alice200.Headers.Set("Content-Type", []string{"application/sdp"})
	alice200.Headers["Allow"] = []string{"INVITE, ACK, BYE, CANCEL, OPTIONS, REGISTER"}
	alice200.Headers.Add("Record-Route", recordRoute)
	aliceContactHeader := fmt.Sprintf("<sip:trec@%s:%s;transport=%s>", h.serverIP, serverPort, sip.TransportName(tx.Transport()))
	alice200.Headers.Add("Contact", aliceContactHeader)
	tx.Respond(alice200)
	log.Info("B2BUA: sent 200 OK to Alice")

	aliceKey := media.SessionKey{
		CallID:    callID,
		RemoteTag: aliceFromTag,
		LocalTag:  serverTag,
	}
	aliceSess := media.NewSession(ctx, aliceKey, rtpConnA, selectedPT, rtpConnA.LocalAddr())
	if hasEarlyOffer {
		aIP, aPort := media.ExtractRTPAddr(aliceSDPOffer)
		aliceSess.SetRemoteAddr(&net.UDPAddr{IP: net.ParseIP(aIP), Port: aPort})
	}
	h.sm.Add(aliceSess)

	bobKey := media.SessionKey{
		CallID:    bobCallID,
		RemoteTag: calleeTag,
		LocalTag:  bobTo.Tag,
	}
	bobSess := media.NewSession(ctx, bobKey, rtpConnB, selectedPT, rtpConnB.LocalAddr())
	bobSess.SetRemoteAddr(bobRTPAddr)
	h.sm.Add(bobSess)

	bridge := media.NewBridge(ctx, rtpConnA, rtpConnB)

	aliceContact := req.Headers.GetFirst("Contact")

	serverContact := fmt.Sprintf("<sip:trec@%s:%s>", h.serverIP, serverPort)

	aliceDialogID := sip.DialogID{
		CallID:    callID,
		LocalTag:  serverTag,
		RemoteTag: aliceFromTag,
	}
	aliceDialog := sip.NewDialog(aliceDialogID, serverContact, from.URI, aliceContact)
	aliceDialog.SetState(sip.DialogStateConfirmed)

	bobDialogID := sip.DialogID{
		CallID:    bobCallID,
		LocalTag:  calleeTag,
		RemoteTag: bobTo.Tag,
	}
	bobDialog := sip.NewDialog(bobDialogID, serverContact, to.URI, binding.ContactURI)
	bobDialog.SetState(sip.DialogStateConfirmed)

	aliceTarget := tx.Target()
	call := &Call{
		AliceCallID:     callID,
		BobCallID:       bobCallID,
		Bridge:          bridge,
		AliceSess:       aliceSess,
		BobSess:         bobSess,
		BobRTPAddr:      bobRTPAddr,
		BobContactURI:   binding.ContactURI,
		BobTransport:    transportImpl,
		BobTarget:       target,
		BobCalleeTag:    calleeTag,
		BobRemoteTag:    bobTo.Tag,
		AliceFromTag:    aliceFromTag,
		AliceServerTag:  serverTag,
		AliceContactURI: aliceContact,
		AliceTarget:     &aliceTarget,
		AliceDialog:     aliceDialog,
		BobDialog:       bobDialog,
		AliceTransport:  tx.Transport(),
	}

	if hasEarlyOffer {
		aIP, aPort := media.ExtractRTPAddr(aliceSDPOffer)
		aRTPAddr := &net.UDPAddr{IP: net.ParseIP(aIP), Port: aPort}
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

	_, serverPort, _ := net.SplitHostPort(h.serverAddr)
	if serverPort == "" {
		serverPort = "5060"
	}

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
