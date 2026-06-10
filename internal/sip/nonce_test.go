package sip

import (
	"testing"
	"time"
)

func TestNonceManager_NewNonce(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	nonce := nm.NewNonce()
	if nonce == "" {
		t.Fatal("empty nonce")
	}
	if len(nonce) != 16 {
		t.Fatalf("nonce length: got %d, want 16", len(nonce))
	}
}

func TestNonceManager_UniqueNonces(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		n := nm.NewNonce()
		if seen[n] {
			t.Fatal("duplicate nonce")
		}
		seen[n] = true
	}
}

func TestNonceManager_VerifyValid(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	nonce := nm.NewNonce()
	known, valid := nm.Verify(nonce, 1)
	if !known {
		t.Fatal("expected known=true")
	}
	if !valid {
		t.Fatal("expected valid=true")
	}
}

func TestNonceManager_VerifyUnknown(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	known, valid := nm.Verify("nonexistent", 1)
	if known {
		t.Fatal("expected known=false for unknown nonce")
	}
	if valid {
		t.Fatal("expected valid=false for unknown nonce")
	}
}

func TestNonceManager_VerifyReplay(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	nonce := nm.NewNonce()
	nm.Verify(nonce, 1)

	known, valid := nm.Verify(nonce, 1)
	if !known {
		t.Fatal("expected known=true")
	}
	if valid {
		t.Fatal("expected valid=false for replayed nc")
	}
}

func TestNonceManager_VerifyLowerNC(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	nonce := nm.NewNonce()
	nm.Verify(nonce, 5)

	known, valid := nm.Verify(nonce, 3)
	if !known {
		t.Fatal("expected known=true")
	}
	if valid {
		t.Fatal("expected valid=false for lower nc")
	}
}

func TestNonceManager_VerifyMonotonicNC(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	nonce := nm.NewNonce()

	for i := 1; i <= 10; i++ {
		_, valid := nm.Verify(nonce, uint64(i))
		if !valid {
			t.Fatalf("nc=%d should be valid", i)
		}
	}
}

func TestNonceManager_VerifyExpired(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	nonce := nm.NewNonce()

	// Set expiry to the past to simulate expiration without sleeping.
	nm.mu.Lock()
	nm.entries[nonce].expires = time.Now().Add(-1 * time.Second)
	nm.mu.Unlock()

	known, valid := nm.Verify(nonce, 1)
	if !known {
		t.Fatal("expected known=true for expired nonce")
	}
	if valid {
		t.Fatal("expected valid=false for expired nonce")
	}
}

func TestNonceManager_VerifyExpiredThenDeleted(t *testing.T) {
	nm := NewNonceManager(time.Minute)

	nonce := nm.NewNonce()

	// Set expiry to the past.
	nm.mu.Lock()
	nm.entries[nonce].expires = time.Now().Add(-1 * time.Second)
	nm.mu.Unlock()

	// First Verify should see the expired entry and delete it
	known, valid := nm.Verify(nonce, 1)
	if !known {
		t.Fatal("expected known=true for expired nonce (before deletion)")
	}
	if valid {
		t.Fatal("expected valid=false for expired nonce")
	}

	// Second Verify: entry was deleted, so it's unknown
	known, valid = nm.Verify(nonce, 1)
	if known {
		t.Fatal("expected known=false after first Verify deleted the entry")
	}
	if valid {
		t.Fatal("expected valid=false")
	}
}
