package b2bua

import (
	"net"
	"sync"

	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/internal/sip"
)

// Call represents a B2BUA call, tracking both the Alice and Bob legs.
type Call struct {
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

	AliceDialog    *sip.Dialog
	BobDialog      *sip.Dialog
	AliceTransport sip.Transport
}

// Store provides safe concurrent access to active B2BUA calls.
type Store struct {
	mu      sync.Mutex
	calls   map[string]*Call
	bobToAlice map[string]string
}

// NewStore creates an empty B2BUA call store.
func NewStore() *Store {
	return &Store{
		calls:   make(map[string]*Call),
		bobToAlice: make(map[string]string),
	}
}

// Store adds a call, keyed by both Alice and Bob Call-IDs.
func (s *Store) Store(call *Call) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[call.AliceCallID] = call
	s.bobToAlice[call.BobCallID] = call.AliceCallID
}

// Get retrieves a call by either Alice or Bob Call-ID.
func (s *Store) Get(callID string) *Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.calls[callID]; c != nil {
		return c
	}
	if aid := s.bobToAlice[callID]; aid != "" {
		return s.calls[aid]
	}
	return nil
}

// Remove deletes a call by Alice Call-ID and closes Bob's connection.
func (s *Store) Remove(aliceCID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := s.calls[aliceCID]
	if call != nil {
		delete(s.bobToAlice, call.BobCallID)
		delete(s.calls, aliceCID)
		if call.BobConn != nil {
			call.BobConn.Close()
		}
	}
}
