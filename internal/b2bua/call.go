package b2bua

import (
	"context"
	"net"
	"sync"

	"github.com/thorsager/trecs/internal/media"
	"github.com/thorsager/trecs/internal/sip"
)

// Call represents a B2BUA call, tracking both the Alice and Bob legs.
type Call struct {
	BobConn         net.Conn
	AliceTransport  sip.Transport
	BobTransport    sip.Transport
	BobRTPAddr      net.Addr
	BobTarget       *sip.Target
	BobSess         *media.Session
	AliceSess       *media.Session
	BobDialog       *sip.Dialog
	AliceDialog     *sip.Dialog
	Bridge          *media.Bridge
	AliceTarget     *sip.Target
	BobCalleeTag    string
	BobRemoteTag    string
	AliceFromTag    string
	AliceServerTag  string
	AliceContactURI string
	AliceCallID     string
	BobContactURI   string
	BobCallID       string
	BridgeReady     bool
	TrunkName       string // name of trunk if this is a trunk call, empty for internal
}

// EarlyCall tracks a pending B2BUA call while Bob is ringing (before answer).
// Used to support CANCEL propagation to the Bob leg.
type EarlyCall struct {
	AliceCallID    string
	BobCallID      string
	AliceServerTag string
	BobTx          *sip.UACTransaction
	RTPConnA       *media.RTPConn
	RTPConnB       *media.RTPConn
	Cancel         context.CancelFunc
}

// Store provides safe concurrent access to active B2BUA calls.
type Store struct {
	calls      map[string]*Call
	bobToAlice map[string]string
	early      map[string]*EarlyCall
	mu         sync.Mutex
}

// NewStore creates an empty B2BUA call store.
func NewStore() *Store {
	return &Store{
		calls:      make(map[string]*Call),
		bobToAlice: make(map[string]string),
		early:      make(map[string]*EarlyCall),
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

// StoreEarly adds a pending (ringing) call for CANCEL tracking.
func (s *Store) StoreEarly(ec *EarlyCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.early[ec.AliceCallID] = ec
}

// GetEarly retrieves a pending call by Alice Call-ID.
func (s *Store) GetEarly(callID string) *EarlyCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.early[callID]
}

// RemoveEarly removes a pending call by Alice Call-ID.
func (s *Store) RemoveEarly(aliceCID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.early, aliceCID)
}
