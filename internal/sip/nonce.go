package sip

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type nonceEntry struct {
	expires time.Time
	maxNC   uint64
}

type NonceManager struct {
	mu      sync.Mutex
	entries map[string]*nonceEntry
	ttl     time.Duration
}

func NewNonceManager(ttl time.Duration) *NonceManager {
	return &NonceManager{
		entries: make(map[string]*nonceEntry),
		ttl:     ttl,
	}
}

func (nm *NonceManager) NewNonce() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	nonce := hex.EncodeToString(b)

	nm.mu.Lock()
	nm.entries[nonce] = &nonceEntry{expires: time.Now().Add(nm.ttl)}
	nm.mu.Unlock()

	return nonce
}

func (nm *NonceManager) Verify(nonce string, nc uint64) (known, valid bool) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	entry, ok := nm.entries[nonce]
	if !ok {
		return false, false
	}

	if time.Now().After(entry.expires) {
		delete(nm.entries, nonce)
		return true, false
	}

	if nc <= entry.maxNC {
		return true, false
	}

	entry.maxNC = nc
	return true, true
}

// Sweep removes expired entries. It should be called periodically.
func (nm *NonceManager) Sweep() {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	now := time.Now()
	for nonce, entry := range nm.entries {
		if now.After(entry.expires) {
			delete(nm.entries, nonce)
		}
	}
}

// StartNonceSweeper starts a background goroutine that calls Sweep() on nm
// at the given interval. The goroutine stops when ctx is canceled.
func StartNonceSweeper(ctx context.Context, nm *NonceManager, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				nm.Sweep()
			case <-ctx.Done():
				return
			}
		}
	}()
}
