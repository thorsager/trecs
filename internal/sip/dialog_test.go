package sip

import (
	"testing"
)

func TestNewDialog_LocalSeqStartsAtOne(t *testing.T) {
	d := NewDialog(DialogID{CallID: "call1", LocalTag: "a", RemoteTag: "b"}, "sip:local", "sip:remote", "sip:target")
	if d.LocalSeq != 1 {
		t.Fatalf("NewDialog LocalSeq = %d, want 1 (initial INVITE CSeq)", d.LocalSeq)
	}
}

func TestIncrementLocalSeq_MonotonicAndContiguous(t *testing.T) {
	d := NewDialog(DialogID{CallID: "call1", LocalTag: "a", RemoteTag: "b"}, "sip:local", "sip:remote", "sip:target")

	// After the initial INVITE (CSeq 1), the first in-dialog request must be 2.
	want := 2
	for i := 0; i < 5; i++ {
		got := d.IncrementLocalSeq()
		if got != want {
			t.Fatalf("IncrementLocalSeq #%d = %d, want %d (contiguous)", i, got, want)
		}
		want++
	}
}

func TestIncrementLocalSeq_MutexSafe(t *testing.T) {
	d := NewDialog(DialogID{CallID: "call1", LocalTag: "a", RemoteTag: "b"}, "sip:local", "sip:remote", "sip:target")

	const goroutines = 16
	const iters = 100
	seen := make(chan int, goroutines*iters)
	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < iters; j++ {
				seen <- d.IncrementLocalSeq()
			}
		}()
	}
	values := make(map[int]bool)
	for i := 0; i < goroutines*iters; i++ {
		v := <-seen
		if values[v] {
			t.Fatalf("duplicate local sequence number %d produced", v)
		}
		values[v] = true
	}
}
