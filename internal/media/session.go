package media

import (
	"context"
	"math/rand"
	"net"
	"sync"

	"github.com/thorsager/trecs/internal/sip"
)

// SessionKey uniquely identifies a SIP dialog for media session lookup.
type SessionKey struct {
	CallID    string
	LocalTag  string
	RemoteTag string
}

// SessionState tracks the progress of the offer/answer exchange.
type SessionState int

const (
	SessionCreated   SessionState = iota // initial
	SessionWaitingAck                    // delayed offer: 200 OK sent, waiting for ACK
	SessionActive                        // early offer: echo running, or ACK received for delayed
)

type SessionKind int

const (
	SessionKindEcho SessionKind = iota
	SessionKindPlay
)

// Session holds the media-related state for one call.
type Session struct {
	mu          sync.Mutex
	Key         SessionKey
	Kind        SessionKind
	State       SessionState
	RTPConn     *RTPConn
	PayloadType uint8
	ServerAddr  net.Addr
	RemoteAddr  net.Addr
	ServerSSRC  uint32

	WavData       *WavData
	CallerContact string
	CallerURI     string
	TargetURI     string

	SipTransport sip.Transport
	SipTarget    *sip.Target

	ctx    context.Context
	cancel context.CancelFunc
}

// SetRemoteAddr is safe for concurrent access.
func (s *Session) SetRemoteAddr(addr net.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RemoteAddr = addr
}

// RemoteAddrSafe returns the remote address safely.
func (s *Session) RemoteAddrSafe() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.RemoteAddr
}

// SetState is safe for concurrent access.
func (s *Session) SetState(st SessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = st
}

// StateSafe returns the current state safely.
func (s *Session) StateSafe() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.State
}

// NewSession creates a Session with a cancellable context derived from parent.
func NewSession(parent context.Context, key SessionKey, rtpConn *RTPConn, payloadType uint8, serverAddr net.Addr) *Session {
	ctx, cancel := context.WithCancel(parent)
	return &Session{
		Key:         key,
		State:       SessionCreated,
		RTPConn:     rtpConn,
		PayloadType: payloadType,
		ServerAddr:  serverAddr,
		ServerSSRC:  rand.Uint32(),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Ctx returns the session's context. It is cancelled when Cancel is called.
func (s *Session) Ctx() context.Context {
	return s.ctx
}

// Cancel terminates the echo loop and closes the RTP connection.
func (s *Session) Cancel() {
	s.cancel()
}

// SessionManager tracks active echo sessions keyed by dialog ID.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[SessionKey]*Session
}

// NewSessionManager creates a new empty session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[SessionKey]*Session),
	}
}

// Add stores a session.
func (sm *SessionManager) Add(s *Session) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[s.Key] = s
}

// Get retrieves a session by key.
func (sm *SessionManager) Get(key SessionKey) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.sessions[key]
}

// Remove deletes a session by key.
func (sm *SessionManager) Remove(key SessionKey) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, key)
}
