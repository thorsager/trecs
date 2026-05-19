package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"gitub.com/thorsager/trec/internal/media"
	"gitub.com/thorsager/trec/internal/session"
	"gitub.com/thorsager/trec/internal/sip"
	"gitub.com/thorsager/trec/proto"
)

type B2BUACall struct {
	AliceCallID    string
	BobCallID      string
	Bridge         *media.Bridge
	AliceSess      *media.Session
	BobSess        *media.Session
	BobConn        net.Conn
	BobRTPAddr     net.Addr
	BridgeReady    bool

	BobContactURI  string
	BobTransport   sip.Transport
	BobTarget      *sip.Target
	BobCalleeTag   string
	BobRemoteTag   string
	AliceFromTag   string
	AliceServerTag string
	AliceContactURI string
	AliceTarget    *sip.Target

	AliceDialog    *session.Dialog
	BobDialog      *session.Dialog
	AliceTransport sip.Transport
}

type ServerHandlers struct {
	reg        *sip.Registrar
	sm         *media.SessionManager
	server     *sip.Server
	serverIP   string
	serverAddr string
	uacMgr     *sip.UACManager

	b2buaCalls map[string]*B2BUACall
	b2buaByBob map[string]string
	b2buaMu    sync.Mutex
}

func (h *ServerHandlers) storeB2BUACall(call *B2BUACall) {
	h.b2buaMu.Lock()
	defer h.b2buaMu.Unlock()
	h.b2buaCalls[call.AliceCallID] = call
	h.b2buaByBob[call.BobCallID] = call.AliceCallID
}

func (h *ServerHandlers) getB2BUACall(callID string) *B2BUACall {
	h.b2buaMu.Lock()
	defer h.b2buaMu.Unlock()
	if c := h.b2buaCalls[callID]; c != nil {
		return c
	}
	if aid := h.b2buaByBob[callID]; aid != "" {
		return h.b2buaCalls[aid]
	}
	return nil
}

func (h *ServerHandlers) removeB2BUACall(aliceCID string) {
	h.b2buaMu.Lock()
	defer h.b2buaMu.Unlock()
	call := h.b2buaCalls[aliceCID]
	if call != nil {
		delete(h.b2buaByBob, call.BobCallID)
		delete(h.b2buaCalls, aliceCID)
		if call.BobConn != nil {
			call.BobConn.Close()
		}
	}
}

func generateTag() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "trec"
	}
	return hex.EncodeToString(b)
}

func extractUser(uri string) string {
	uri = strings.TrimPrefix(uri, "sip:")
	if at := strings.IndexByte(uri, '@'); at >= 0 {
		return uri[:at]
	}
	return ""
}

// normalizeAOR strips the port from a SIP URI for registrar lookup.
// The registrar keys by address-of-record (sip:user@host, no port).
// stripBrackets removes surrounding angle brackets from a URI string.
// If the string has no brackets it is returned unmodified.
func stripBrackets(s string) string {
	if len(s) > 0 && s[0] == '<' {
		if close := strings.IndexByte(s, '>'); close >= 0 {
			return s[1:close]
		}
	}
	return s
}

func normalizeAOR(uri string) string {
	if at := strings.IndexByte(uri, '@'); at >= 0 {
		host := uri[at+1:]
		if colon := strings.IndexByte(host, ':'); colon >= 0 {
			return uri[:at+1] + host[:colon]
		}
	}
	return uri
}

func (h *ServerHandlers) handleOptions(req *proto.SIPMessage, tx sip.Transaction) {
	trying := proto.NewResponse(req, 100, "Trying")
	tx.Respond(trying)

	res := proto.NewResponse(req, 200, "OK")
	res.Headers["Allow"] = []string{h.server.AllowMethods()}
	res.Headers["Accept"] = []string{"application/sdp"}
	res.Headers["Supported"] = []string{"timer"}
	tx.Respond(res)
}

func (h *ServerHandlers) handleInvite(req *proto.SIPMessage, tx sip.Transaction) {
	trying := proto.NewResponse(req, 100, "Trying")
	tx.Respond(trying)

	user := extractUser(req.RequestURI())
	if user == "echo" {
		h.handleEchoInvite(req, tx)
		return
	}

	h.handleB2BUAInvite(req, tx)
}

func (h *ServerHandlers) handleEchoInvite(req *proto.SIPMessage, tx sip.Transaction) {
	serverTag := generateTag()

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

	rtpConn, err := media.NewRTPConn()
	if err != nil {
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	rtpAddr := rtpConn.LocalAddr().(*net.UDPAddr)
	payloadType := uint8(media.PCMU)

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

	session := media.NewSession(key, rtpConn, payloadType, rtpAddr)

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

	if sdpOffer != nil {
		go media.RunEcho(session.Ctx(), rtpConn, payloadType)
	}
}

func (h *ServerHandlers) handleB2BUAInvite(req *proto.SIPMessage, tx sip.Transaction) {
	to, err := req.To()
	if err != nil {
		res := proto.NewResponse(req, 400, "Bad Request")
		tx.Respond(res)
		return
	}

	aor := normalizeAOR(to.URI)
	bindings := h.reg.GetBindings(aor)
	if len(bindings) == 0 {
		log.Printf("B2BUA: no binding for %q (normalized from %q)", aor, to.URI)
		res := proto.NewResponse(req, 404, "Not Found")
		tx.Respond(res)
		return
	}

	var binding *sip.Binding
	var target *sip.Target
	var transport string

	// Prefer bindings with live TCP flows, then newest bindings.
	// Bindings without a live connection (stale UDP, dead TCP) are skipped.
	for i := len(bindings) - 1; i >= 0; i-- {
		b := bindings[i]

		if b.OB && b.FlowID != "" {
			if fc := h.server.Pool().GetByFlowID(b.FlowID); fc != nil {
				binding = b
				target = &sip.Target{Conn: fc.Conn, FlowID: fc.Key.String()}
				transport = "TCP"
				log.Printf("B2BUA: reusing TCP flow %s for %s", b.FlowID, b.ContactURI)
				break
			}
		}

		tgt, tr, err := sip.TargetFromContact(b.ContactURI)
		if err != nil {
			log.Printf("B2BUA: %q unresolvable (%v)", b.ContactURI, err)
			continue
		}

		if tr == "TCP" && tgt.Conn != nil {
			wrapped, werr := h.server.TCPTransport().HandleOutbound(tgt.Conn)
			if werr != nil {
				log.Printf("HandleOutbound(%q): %v", b.ContactURI, werr)
				tgt.Conn.Close()
				continue
			}
			tgt.Conn = wrapped
			log.Printf("B2BUA: registered outbound TCP flow to %s", b.ContactURI)
		}

		binding = b
		target = tgt
		transport = tr
		break
	}

	if target == nil {
		log.Printf("B2BUA: no reachable binding for %q", aor)
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
		log.Printf("B2BUA: missing From tag in INVITE from %s — generating fallback", from.URI)
		aliceFromTag = generateTag()
	}

	callID := req.Headers.GetFirst("Call-ID")
	serverTag := generateTag()

	rtpConnA, err := media.NewRTPConn()
	if err != nil {
		res := proto.NewResponse(req, 500, "Server Internal Error")
		tx.Respond(res)
		return
	}

	rtpConnB, err := media.NewRTPConn()
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

	calleeTag := generateTag()
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

	uac := h.uacMgr.NewTransaction(context.Background(), proto.SIPMethodINVITE, transportImpl, target)

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

	go h.b2buaResponseLoop(req, tx, target, transportImpl, uac,
		rtpConnA, rtpConnB, from, aliceFromTag, serverTag, callID,
		calleeTag, bobCallID, to, selectedPT, hasEarlyOffer,
		aliceSDPOffer, aliceSDPBytes, recordRoute,
		bobInvite, serverPort, binding)
}

func (h *ServerHandlers) b2buaResponseLoop(
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
	defer rtpConnA.Close()
	defer rtpConnB.Close()

	if err := uac.Send(bobInvite); err != nil {
		log.Printf("B2BUA: failed to send INVITE to Bob: %v", err)
		tx.Respond(proto.NewResponse(req, 502, "Bad Gateway"))
		return
	}
	log.Printf("B2BUA: sent INVITE to %s [%s]", binding.ContactURI, uac.Branch)

	for {
		select {
		case resp := <-uac.Responses:
			sc := resp.StatusCode()

			if sc >= 100 && sc < 200 {
				if sc == 180 || sc == 183 {
					log.Printf("B2BUA: Bob %d %s — forwarding to Alice", sc, resp.Status())
					prov := proto.NewResponse(req, sc, resp.Status())
					tx.Respond(prov)
				}
				continue
			}

			if sc == 200 {
				h.handleBob200OK(resp, req, tx, target, transportImpl,
					rtpConnA, rtpConnB, from, aliceFromTag, serverTag, callID,
					calleeTag, bobCallID, to, selectedPT, hasEarlyOffer,
					aliceSDPOffer, aliceSDPBytes, recordRoute, serverPort,
					binding)
				return
			}

			if sc >= 300 {
				log.Printf("B2BUA: Bob responded %d %s — forwarding to Alice", sc, resp.Status())
				status := sc
				errResp := proto.NewResponse(req, status, resp.Status())
				tx.Respond(errResp)
				return
			}

		case err := <-uac.Errors:
			log.Printf("B2BUA: Bob INVITE timed out: %v", err)
			tx.Respond(proto.NewResponse(req, 408, "Request Timeout"))
			return
		}
	}
}

func (h *ServerHandlers) handleBob200OK(
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
	log.Printf("B2BUA: Bob answered 200 OK for %s", callID)

	bobTo, err := resp.To()
	if err != nil {
		log.Printf("B2BUA: missing To in Bob's 200 OK")
		return
	}

	bobSDP, err := proto.UnmarshalSDPBytes(resp.Body)
	if err != nil {
		log.Printf("B2BUA: failed to parse Bob's SDP: %v", err)
		return
	}
	bobIP, bobPort := media.ExtractRTPAddr(bobSDP)
	bobRTPAddr := &net.UDPAddr{IP: net.ParseIP(bobIP), Port: bobPort}
	log.Printf("B2BUA: Bob RTP at %s:%d", bobIP, bobPort)

	ackToBob := proto.NewRequest(proto.SIPMethodACK, binding.ContactURI)
	ackToBob.Headers.Add("Via",
		fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s;rport",
			transportName(transportImpl), h.serverIP, serverPort, sip.GenerateBranch()))
	ackToBob.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", from.URI, calleeTag))
	ackToBob.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s", to.URI, bobTo.Tag))
	ackToBob.Headers.Add("Call-ID", bobCallID)
	ackToBob.CSeq = proto.CSeq{Method: proto.SIPMethodACK, Seq: 1}
	ackToBob.Headers.Add("Max-Forwards", "70")
	ackToBob.Headers.Add("Content-Length", "0")
	if err := transportImpl.Send(ackToBob, target); err != nil {
		log.Printf("B2BUA: failed to send ACK to Bob: %v", err)
	} else {
		log.Printf("B2BUA: sent ACK to Bob [%s]", binding.ContactURI)
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
	aliceContactHeader := fmt.Sprintf("<sip:trec@%s:%s;transport=%s>", h.serverIP, serverPort, transportName(tx.Transport()))
	alice200.Headers.Add("Contact", aliceContactHeader)
	tx.Respond(alice200)
	log.Printf("B2BUA: sent 200 OK to Alice [%s]", callID)

	aliceKey := media.SessionKey{
		CallID:    callID,
		RemoteTag: aliceFromTag,
		LocalTag:  serverTag,
	}
	aliceSess := media.NewSession(aliceKey, rtpConnA, selectedPT, rtpConnA.LocalAddr())
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
	bobSess := media.NewSession(bobKey, rtpConnB, selectedPT, rtpConnB.LocalAddr())
	bobSess.SetRemoteAddr(bobRTPAddr)
	h.sm.Add(bobSess)

	bridge := media.NewBridge(rtpConnA, rtpConnB)

	aliceContact := req.Headers.GetFirst("Contact")

	serverContact := fmt.Sprintf("<sip:trec@%s:%s>", h.serverIP, serverPort)

	aliceDialogID := session.DialogID{
		CallID:    callID,
		LocalTag:  serverTag,
		RemoteTag: aliceFromTag,
	}
	aliceDialog := session.NewDialog(aliceDialogID, serverContact, from.URI, aliceContact)
	aliceDialog.SetState(session.DialogStateConfirmed)

	bobDialogID := session.DialogID{
		CallID:    bobCallID,
		LocalTag:  calleeTag,
		RemoteTag: bobTo.Tag,
	}
	bobDialog := session.NewDialog(bobDialogID, serverContact, to.URI, binding.ContactURI)
	bobDialog.SetState(session.DialogStateConfirmed)

	aliceTarget := tx.Target()
	call := &B2BUACall{
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
		log.Printf("B2BUA: bridge started (early offer)")
	} else {
		aliceSess.SetState(media.SessionWaitingAck)
		log.Printf("B2BUA: waiting for Alice ACK with SDP (delayed offer)")
	}

	h.storeB2BUACall(call)
}

func transportName(t sip.Transport) string {
	switch t.(type) {
	case *sip.TCPTransport:
		return "TCP"
	default:
		return "UDP"
	}
}

func (h *ServerHandlers) handleAck(msg *proto.SIPMessage, target sip.Target, transport sip.Transport) {
	callID := msg.Headers.GetFirst("Call-ID")
	if call, err := checkB2BUAAck(h, msg, callID); err == nil && call != nil {
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
		go media.RunEcho(session.Ctx(), session.RTPConn, session.PayloadType)
	}
}

func checkB2BUAAck(h *ServerHandlers, msg *proto.SIPMessage, callID string) (*B2BUACall, error) {
	h.b2buaMu.Lock()
	call := h.b2buaCalls[callID]
	h.b2buaMu.Unlock()

	if call == nil {
		return nil, nil
	}

	if call.BridgeReady {
		return call, nil
	}

	if len(msg.Body) == 0 {
		log.Printf("B2BUA: ACK for %s has no SDP body (delayed offer)", callID)
		return call, nil
	}

	sdp, err := proto.UnmarshalSDPBytes(msg.Body)
	if err != nil {
		log.Printf("B2BUA: failed to parse ACK SDP: %v", err)
		return call, nil
	}

	clientIP, clientPort := media.ExtractRTPAddr(sdp)
	aRTPAddr := &net.UDPAddr{IP: net.ParseIP(clientIP), Port: clientPort}

	call.Bridge.SetARemote(aRTPAddr)
	call.Bridge.SetBRemote(call.BobRTPAddr)
	call.BridgeReady = true
	call.Bridge.Start()

	call.AliceSess.SetRemoteAddr(aRTPAddr)
	call.AliceSess.SetState(media.SessionActive)
	log.Printf("B2BUA: bridge started (delayed offer) for %s", callID)

	return call, nil
}

func (h *ServerHandlers) handleBye(req *proto.SIPMessage, tx sip.Transaction) {
	trying := proto.NewResponse(req, 100, "Trying")
	tx.Respond(trying)

	callID := req.Headers.GetFirst("Call-ID")

	_, serverPort, _ := net.SplitHostPort(h.serverAddr)
	if serverPort == "" {
		serverPort = "5060"
	}

	call := h.getB2BUACall(callID)
	if call != nil {
		log.Printf("B2BUA: BYE for %s — forwarding to other leg", callID)
		call.Bridge.Stop()

		isFromAlice := callID == call.AliceCallID

		var dlg *session.Dialog
		var fwdTransport sip.Transport
		var fwdTargetObj *sip.Target
		var fwdRequestURI string
		var viaTransport string

		if isFromAlice {
			dlg = call.BobDialog
			fwdRequestURI = stripBrackets(call.BobContactURI)
			fwdTransport = call.BobTransport
			fwdTargetObj = call.BobTarget
			viaTransport = transportName(fwdTransport)
		} else {
			dlg = call.AliceDialog
			fwdRequestURI = stripBrackets(call.AliceContactURI)
			fwdTargetObj = call.AliceTarget
			if fwdTargetObj == nil {
				var err error
				fwdTargetObj, _, err = sip.TargetFromContact(fwdRequestURI)
				if err != nil {
					log.Printf("B2BUA: failed to resolve Alice Contact %q: %v", fwdRequestURI, err)
					fwdTargetObj = &sip.Target{}
				}
			}
			fwdTransport = call.AliceTransport
			viaTransport = transportName(fwdTransport)
		}

		fwdBye := proto.NewRequest(proto.SIPMethodBYE, fwdRequestURI)
		fwdBye.Headers.Add("Via",
			fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s",
				viaTransport, h.serverIP, serverPort, sip.GenerateBranch()))
		fwdBye.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s",
			stripBrackets(dlg.LocalURI), dlg.ID.LocalTag))
		fwdBye.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s",
			stripBrackets(dlg.RemoteURI), dlg.ID.RemoteTag))
		fwdBye.Headers.Add("Call-ID", dlg.ID.CallID)
		fwdBye.CSeq = proto.CSeq{Method: proto.SIPMethodBYE, Seq: 2}
		fwdBye.Headers.Add("Max-Forwards", "70")
		fwdBye.Headers.Add("Content-Length", "0")

		if err := fwdTransport.Send(fwdBye, fwdTargetObj); err != nil {
			log.Printf("B2BUA: failed to forward BYE to %s: %v", dlg.ID.CallID, err)
		} else {
			log.Printf("B2BUA: forwarded BYE to %s", dlg.ID.CallID)
		}

		if call.AliceDialog != nil {
			call.AliceDialog.SetState(session.DialogStateTerminated)
		}
		if call.BobDialog != nil {
			call.BobDialog.SetState(session.DialogStateTerminated)
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

		h.removeB2BUACall(call.AliceCallID)
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
}

func (h *ServerHandlers) handleResponse(msg *proto.SIPMessage, target sip.Target, transport sip.Transport) {
	h.uacMgr.HandleResponse(msg)
}
