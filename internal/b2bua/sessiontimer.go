package b2bua

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

const (
	DefaultSessionExpires = 1800 * time.Second // RFC 4028 recommended default
	DefaultMinSE          = 90 * time.Second   // RFC 4028 absolute minimum
)

// SessionTimer tracks the state of a session timer for a single SIP dialog leg.
type SessionTimer struct {
	Interval  time.Duration      // negotiated Session-Expires value
	MinSE     time.Duration      // negotiated Min-SE value
	Refresher string             // "uac" or "uas" — who refreshes
	ExpiresAt time.Time          // when the session expires (absolute)
	StartTime time.Time          // when this timer was last started/reset
	Cancel    context.CancelFunc // to cancel the timer goroutine

	// baseCtx is the parent context captured at StartSessionTimer. Resets
	// restart from this context rather than context.Background(), keeping the
	// timer tied to the same lifecycle it was originally started under.
	baseCtx context.Context
}

// ParseSessionExpires extracts the interval and refresher from a Session-Expires header value.
// Format: "delta-seconds;refresher=uac|uas"
// Returns an interval of 0 when the header is absent or unparseable, letting
// callers fall back to their offered/default value. The refresher defaults to
// "uac" per RFC 4028 §8 Table 2 whenever a header carried no refresher param.
func ParseSessionExpires(header string) (interval time.Duration, refresher string) {
	if header == "" {
		return 0, "uac"
	}
	parts := strings.SplitN(header, ";", 2)
	secs, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil {
		return 0, "uac"
	}
	interval = time.Duration(secs) * time.Second
	refresher = "uac" // default per RFC 4028 §8 Table 2
	if len(parts) > 1 {
		for _, param := range strings.Split(parts[1], ";") {
			sep := strings.Index(param, "=")
			if sep < 0 {
				continue
			}
			// The param name is case-insensitive (RFC 3261 §25.1).
			if !strings.EqualFold(strings.TrimSpace(param[:sep]), "refresher") {
				continue
			}
			value := strings.Trim(strings.TrimSpace(param[sep+1:]), `"`)
			if v := strings.ToLower(value); v == "uac" || v == "uas" {
				refresher = v
			}
		}
	}
	return interval, refresher
}

// ParseMinSE extracts the Min-SE interval from a Min-SE header value.
// RFC 4028 allows generic parameters after the numeric value
// (e.g., "90;foo=bar"), so parsing stops at the first ';'.
// Returns 0 if the header is absent or unparseable.
func ParseMinSE(header string) time.Duration {
	if header == "" {
		return 0
	}
	parts := strings.SplitN(header, ";", 2)
	secs, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// HasOptionTag reports whether the named header of msg contains the given
// option tag. Option-tag lists are comma-separated and names/values are
// case-insensitive (RFC 3261 §25.1, §20).
func HasOptionTag(msg *proto.SIPMessage, header, tag string) bool {
	for _, val := range msg.Headers.Get(header) {
		for _, t := range strings.Split(val, ",") {
			if strings.EqualFold(strings.TrimSpace(t), tag) {
				return true
			}
		}
	}
	return false
}

// HasTimerSupport checks if a SIP message includes "timer" in the Supported header.
func HasTimerSupport(msg *proto.SIPMessage) bool {
	return HasOptionTag(msg, "Supported", "timer")
}

// timersInPlay reports whether RFC 4028 session timers are relevant for a
// forwarded in-dialog request: the request itself engages them (Supported:
// timer or Session-Expires/Min-SE present), or a session timer has already
// been negotiated on one of the call's legs.
func timersInPlay(req *proto.SIPMessage, call *Call) bool {
	if HasTimerSupport(req) || req.Headers.GetFirst("Session-Expires") != "" ||
		req.Headers.GetFirst("Min-SE") != "" {
		return true
	}
	return call.AliceSessionTimer != nil || call.BobSessionTimer != nil
}

// copyTimerNegotiationHeaders relays the RFC 4028 negotiation headers from a
// peer response onto the response being sent back to the originating leg.
// Session-Expires and Min-SE are copied verbatim; Require and Supported are
// copied only for the "timer" option tag, so no unrelated option tags are
// invented. Without this, the re-INVITE initiator never sees the negotiated
// timer values, and a relayed 422 would be missing its mandatory Min-SE.
func copyTimerNegotiationHeaders(src, dst *proto.SIPMessage) {
	if se := src.Headers.GetFirst("Session-Expires"); se != "" {
		dst.Headers.Add("Session-Expires", se)
	}
	if ms := src.Headers.GetFirst("Min-SE"); ms != "" {
		dst.Headers.Add("Min-SE", ms)
	}
	if HasOptionTag(src, "Require", "timer") {
		dst.Headers.Add("Require", "timer")
	}
	if HasOptionTag(src, "Supported", "timer") {
		dst.Headers.Add("Supported", "timer")
	}
}

// FormatSessionExpires formats a Session-Expires header value.
func FormatSessionExpires(secs int, refresher string) string {
	return fmt.Sprintf("%d;refresher=%s", secs, refresher)
}

// FormatMinSE formats a Min-SE header value.
func FormatMinSE(secs int) string {
	return strconv.Itoa(secs)
}

// DurationToSeconds rounds a duration to whole seconds for SIP headers.
func DurationToSeconds(d time.Duration) int {
	return int(d.Round(time.Second).Seconds())
}

// StartSessionTimer starts a session timer goroutine for a call leg.
// For the refresher side, it sends a re-INVITE at half the interval.
// For the non-refresher side, it monitors for incoming re-INVITEs.
func (h *Handler) StartSessionTimer(ctx context.Context, call *Call, leg string) {
	timer := call.AliceSessionTimer
	if leg == "bob" {
		timer = call.BobSessionTimer
	}
	if timer == nil || timer.Interval <= 0 {
		return
	}

	// Cancel any existing timer for this leg.
	h.StopSessionTimer(call, leg)

	timerCtx, cancel := context.WithCancel(ctx)
	timer.Cancel = cancel
	timer.baseCtx = ctx
	timer.StartTime = time.Now()
	timer.ExpiresAt = timer.StartTime.Add(timer.Interval)

	if (leg == "alice" && timer.Refresher == "uas") ||
		(leg == "bob" && timer.Refresher == "uac") {
		// We are the refresher: send re-INVITE at half the interval.
		go h.refresherTimerLoop(timerCtx, call, leg, timer)
	} else {
		// We are the non-refresher: monitor for incoming refreshes.
		go h.nonRefresherTimerLoop(timerCtx, call, leg, timer)
	}

	log := slog.Default()
	log.Info("session timer started",
		"callID", call.AliceCallID,
		"leg", leg,
		"interval", timer.Interval,
		"refresher", timer.Refresher,
		"expiresAt", timer.ExpiresAt)
}

// StopSessionTimer stops the session timer goroutine for a call leg.
func (h *Handler) StopSessionTimer(call *Call, leg string) {
	timer := call.AliceSessionTimer
	if leg == "bob" {
		timer = call.BobSessionTimer
	}
	if timer != nil && timer.Cancel != nil {
		timer.Cancel()
		timer.Cancel = nil
	}
}

// ResetSessionTimer resets the session timer for a call leg (called on re-INVITE receipt).
func (h *Handler) ResetSessionTimer(call *Call, leg string) {
	timer := call.AliceSessionTimer
	if leg == "bob" {
		timer = call.BobSessionTimer
	}
	if timer == nil || timer.Interval <= 0 {
		return
	}

	// Cancel existing timer and restart on the same parent context the timer
	// was originally started with. context.Background() would disconnect the
	// restarted timer from the call lifecycle; reusing the loop's own (soon to
	// be canceled) context would kill it immediately.
	h.StopSessionTimer(call, leg)
	parent := timer.baseCtx
	if parent == nil {
		parent = context.Background()
	}
	h.StartSessionTimer(parent, call, leg)

	log := slog.Default()
	log.Debug("session timer reset",
		"callID", call.AliceCallID,
		"leg", leg,
		"interval", timer.Interval)
}

// refresherTimerLoop runs when we are the refresher. It sends a re-INVITE
// at half the session interval and waits for the response.
func (h *Handler) refresherTimerLoop(ctx context.Context, call *Call, leg string, timer *SessionTimer) {
	log := slog.Default().With("callID", call.AliceCallID, "leg", leg)

	// Calculate when to send the refresh: half the interval before expiry.
	halfInterval := timer.Interval / 2
	refreshTime := timer.Interval - halfInterval

	refreshTimer := time.NewTimer(refreshTime)
	defer refreshTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Debug("session timer: refresher loop stopped", "leg", leg)
			return
		case <-refreshTimer.C:
			// Timer fired — send re-INVITE to refresh the session and await
			// its outcome inline. Starting the next refresh while a previous
			// one is still in flight would place two concurrent INVITEs in
			// the same dialog (RFC 3261 §12.2.1) and invite 491 glare.
			log.Info("session timer: sending refresh re-INVITE", "leg", leg)

			uac, err := h.sendSessionRefresh(ctx, call, leg, timer)
			if err != nil {
				log.Error("session timer: failed to send refresh", "error", err, "leg", leg)
				// On failure, tear down the call.
				h.sendByeBothLegs(call, "Session refresh failed")
				return
			}

			switch h.waitForRefreshResponse(ctx, uac, call, leg, timer) {
			case refreshAccepted:
				// waitForRefreshResponse already restarted the timer for the
				// next interval (a new loop generation owns the schedule).
				return
			case refreshTerminated:
				// The call was torn down or this leg's context was canceled.
				return
			case refreshFailed:
				// The peer rejected the refresh with a status that does not
				// end the session (e.g. 491 Request Pending). Keep the
				// session and retry at the next half-interval point.
				timer.ExpiresAt = time.Now().Add(timer.Interval)
				refreshTimer.Reset(refreshTime)
			}
		}
	}
}

// nonRefresherTimerLoop runs when we are the non-refresher. It waits for
// the session to expire and sends BYE if no refresh arrives.
func (h *Handler) nonRefresherTimerLoop(ctx context.Context, call *Call, leg string, timer *SessionTimer) {
	log := slog.Default().With("callID", call.AliceCallID, "leg", leg)

	// Safety timeout: send BYE slightly before expiry.
	// RFC 4028 recommends min(32s, interval/3) before expiry.
	safetyMargin := timer.Interval / 3
	if safetyMargin > 32*time.Second {
		safetyMargin = 32 * time.Second
	}
	safetyTimeout := timer.Interval - safetyMargin

	safetyTimer := time.NewTimer(safetyTimeout)
	defer safetyTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Debug("session timer: non-refresher loop stopped", "leg", leg)
			return
		case <-safetyTimer.C:
			// Safety timeout expired — no refresh received, tear down.
			log.Info("session timer: no refresh received, tearing down call", "leg", leg)
			h.sendByeBothLegs(call, "Session timer expired (no refresh)")
			return
		}
	}
}

// sendSessionRefresh sends a re-INVITE to refresh the session for the given
// leg and returns its UAC transaction. The caller must pass it to
// waitForRefreshResponse before sending the next refresh: only one refresh
// transaction may be in flight per dialog (RFC 3261 §12.2.1).
func (h *Handler) sendSessionRefresh(ctx context.Context, call *Call, leg string, timer *SessionTimer) (*sip.UACTransaction, error) {
	serverPort := h.serverPort

	var dlg *sip.Dialog
	var fwdTransport sip.Transport
	var fwdTargetObj *sip.Target
	var fwdRequestURI string
	var fwdCallID string

	if leg == "alice" {
		// Refresh Alice's leg: send re-INVITE to Alice.
		dlg = call.AliceDialog
		fwdRequestURI = sip.StripBrackets(call.AliceContactURI)
		fwdTransport = call.AliceTransport
		fwdTargetObj = call.AliceTarget
		if fwdTargetObj == nil {
			var err error
			fwdTargetObj, _, err = sip.TargetFromContact(fwdRequestURI)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve Alice contact: %w", err)
			}
		}
		fwdCallID = call.AliceCallID
	} else {
		// Refresh Bob's leg: send re-INVITE to Bob.
		dlg = call.BobDialog
		fwdRequestURI = sip.StripBrackets(call.BobContactURI)
		fwdTransport = call.BobTransport
		fwdTargetObj = call.BobTarget
		fwdCallID = call.BobCallID
	}

	viaTransport := sip.TransportName(fwdTransport)

	// Create the UAC transaction first so we can use its registered branch in
	// the Via header. Responses are matched to client transactions by the Via
	// branch (RFC 3261 §17.1.3); using a fresh branch here would prevent the
	// refresh response from ever reaching the UAC's Responses channel.
	uac := h.uacMgr.NewTransaction(ctx, proto.SIPMethodINVITE, fwdTransport, fwdTargetObj)

	fwdInvite := proto.NewRequest(proto.SIPMethodINVITE, fwdRequestURI)
	fwdInvite.Headers.Add("Via",
		fmt.Sprintf("SIP/2.0/%s %s:%s;branch=%s",
			viaTransport, h.serverIP, serverPort, uac.Branch))
	fwdInvite.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s",
		sip.StripBrackets(dlg.LocalURI), dlg.ID.LocalTag))
	fwdInvite.Headers.Add("To", fmt.Sprintf("<%s>;tag=%s",
		sip.StripBrackets(dlg.RemoteURI), dlg.ID.RemoteTag))
	fwdInvite.Headers.Add("Call-ID", fwdCallID)
	fwdInvite.Headers.Add("Contact", fmt.Sprintf("<sip:trec@%s:%s;transport=%s>",
		h.serverIP, serverPort, viaTransport))
	fwdCSeq := dlg.IncrementLocalSeq()
	fwdInvite.CSeq = proto.CSeq{Method: proto.SIPMethodINVITE, Seq: fwdCSeq}
	fwdInvite.Headers.Add("Max-Forwards", "70")
	fwdInvite.Headers.Add("Supported", "timer")
	// A session refresh request MUST carry Session-Expires equal to the maximum
	// of Min-SE and the current session interval, plus the negotiated refresher
	// (RFC 4028 §7.4). Min-SE is the negotiated minimum for this leg.
	if timer.Interval > 0 {
		se := timer.Interval
		if timer.MinSE > se {
			se = timer.MinSE
		}
		fwdInvite.Headers.Add("Session-Expires", FormatSessionExpires(DurationToSeconds(se), timer.Refresher))
	}
	if timer.MinSE > 0 {
		fwdInvite.Headers.Add("Min-SE", FormatMinSE(DurationToSeconds(timer.MinSE)))
	}
	fwdInvite.Headers.Add("Content-Length", "0")

	if err := uac.Send(fwdInvite); err != nil {
		uac.Cancel()
		return nil, fmt.Errorf("failed to send re-INVITE: %w", err)
	}

	return uac, nil
}

// refreshOutcome describes how waitForRefreshResponse concluded, so the
// refresher loop can decide whether to keep the session alive, wait, or stop.
type refreshOutcome int

const (
	// refreshAccepted: the peer answered 200 OK. The session timer was reset
	// and a fresh loop generation owns the next refresh, so the caller stops.
	refreshAccepted refreshOutcome = iota
	// refreshTerminated: a fatal response or error was handled (call torn
	// down), or the context was canceled. The caller stops.
	refreshTerminated
	// refreshFailed: the peer rejected the refresh with a non-fatal final
	// response (e.g. 491 Request Pending). The session is still valid; the
	// caller should retry at the next half-interval point.
	refreshFailed
)

// waitForRefreshResponse waits (on the caller's goroutine) for the response to
// a session refresh re-INVITE and reports the outcome. A refresh must be
// accepted within the session interval; if the peer neither answers nor rejects
// it in that time, the session cannot be sustained and the call is torn down
// (this releases the trunk channel for ghost sessions where the peer stopped
// responding but the dialog stayed open).
//
// It is called synchronously from refresherTimerLoop so that only one refresh
// transaction is ever outstanding on a dialog (RFC 3261 §12.2.1).
func (h *Handler) waitForRefreshResponse(ctx context.Context, uac *sip.UACTransaction, call *Call, leg string, timer *SessionTimer) refreshOutcome {
	log := slog.Default().With("callID", call.AliceCallID, "leg", leg)

	// Stoppable timer: time.After would leave a full-interval timer armed
	// (up to 30 min for the default) after every early return.
	deadline := time.NewTimer(timer.Interval)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			// The leg (or call) is going away: stop the refresh transaction so
			// it does not keep retransmitting the re-INVITE until Timer B.
			uac.Cancel()
			return refreshTerminated
		case <-deadline.C:
			log.Error("session timer: refresh not accepted within interval, tearing down call",
				"interval", timer.Interval, "leg", leg)
			h.sendByeBothLegs(call, "Session refresh not accepted within interval")
			return refreshTerminated
		case resp := <-uac.Responses:
			sc := resp.StatusCode()
			if sc == 200 {
				log.Info("session timer: refresh accepted", "leg", leg)
				// Reset the timer on successful refresh.
				h.ResetSessionTimer(call, leg)
				return refreshAccepted
			}
			if sc >= 300 {
				// On 408/481, tear down the call (transaction timed out or the
				// dialog is gone). Any other final response (e.g. 491 Request
				// Pending during glare) leaves the session intact, so let the
				// loop retry at the next refresh point.
				if sc == 408 || sc == 481 {
					log.Warn("session timer: refresh failed fatally, tearing down call", "statusCode", sc, "leg", leg)
					h.sendByeBothLegs(call, fmt.Sprintf("Session refresh failed: %d", sc))
					return refreshTerminated
				}
				log.Info("session timer: refresh rejected, will retry", "statusCode", sc, "leg", leg)
				return refreshFailed
			}
			// Provisional responses — continue waiting.
		case err := <-uac.Errors:
			log.Error("session timer: refresh timed out", "error", err, "leg", leg)
			h.sendByeBothLegs(call, "Session refresh timed out")
			return refreshTerminated
		}
	}
}
