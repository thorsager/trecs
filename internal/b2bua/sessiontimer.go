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

// HasTimerSupport checks if a SIP message includes "timer" in the Supported header.
func HasTimerSupport(msg *proto.SIPMessage) bool {
	supported := msg.Headers.Get("Supported")
	for _, val := range supported {
		for _, tag := range strings.Split(val, ",") {
			if strings.TrimSpace(strings.ToLower(tag)) == "timer" {
				return true
			}
		}
	}
	return false
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
			// Timer fired — send re-INVITE to refresh the session.
			log.Info("session timer: sending refresh re-INVITE", "leg", leg)

			if err := h.sendSessionRefresh(ctx, call, leg, timer); err != nil {
				log.Error("session timer: failed to send refresh", "error", err, "leg", leg)
				// On failure, tear down the call.
				h.sendByeBothLegs(call, "Session refresh failed")
				return
			}

			// Update expiry time and restart timer.
			timer.ExpiresAt = time.Now().Add(timer.Interval)
			refreshTimer.Reset(refreshTime)
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

// sendSessionRefresh sends a re-INVITE to refresh the session for the given leg.
func (h *Handler) sendSessionRefresh(ctx context.Context, call *Call, leg string, timer *SessionTimer) error {
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
				return fmt.Errorf("failed to resolve Alice contact: %w", err)
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
		return fmt.Errorf("failed to send re-INVITE: %w", err)
	}

	// Wait for the response in a goroutine (fire-and-forget for now;
	// the response handler will reset the timer on 200 OK).
	go h.waitForRefreshResponse(ctx, uac, call, leg, timer)

	return nil
}

// waitForRefreshResponse waits for the response to a session refresh re-INVITE.
// A refresh must be accepted within the session interval; if the peer neither
// answers nor rejects it in that time, the session cannot be sustained and the
// call is torn down (this releases the trunk channel for ghost sessions where
// the peer stopped responding but the dialog stayed open).
func (h *Handler) waitForRefreshResponse(ctx context.Context, uac *sip.UACTransaction, call *Call, leg string, timer *SessionTimer) {
	log := slog.Default().With("callID", call.AliceCallID, "leg", leg)

	deadline := time.After(timer.Interval)

	for {
		select {
		case <-ctx.Done():
			// The leg (or call) is going away: stop the refresh transaction so
			// it does not keep retransmitting the re-INVITE until Timer B.
			uac.Cancel()
			return
		case <-deadline:
			log.Error("session timer: refresh not accepted within interval, tearing down call",
				"interval", timer.Interval, "leg", leg)
			h.sendByeBothLegs(call, "Session refresh not accepted within interval")
			return
		case resp := <-uac.Responses:
			sc := resp.StatusCode()
			if sc == 200 {
				log.Info("session timer: refresh accepted", "leg", leg)
				// Reset the timer on successful refresh.
				h.ResetSessionTimer(call, leg)
				return
			}
			if sc >= 300 {
				log.Warn("session timer: refresh rejected", "statusCode", sc, "leg", leg)
				// On 408/481, tear down the call.
				if sc == 408 || sc == 481 {
					h.sendByeBothLegs(call, fmt.Sprintf("Session refresh failed: %d", sc))
				}
				return
			}
			// Provisional responses — continue waiting.
		case err := <-uac.Errors:
			log.Error("session timer: refresh timed out", "error", err, "leg", leg)
			h.sendByeBothLegs(call, "Session refresh timed out")
			return
		}
	}
}
