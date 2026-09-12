package sip

import (
	"sync"
	"time"
)

type DialogState int

const (
	DialogStateEarly DialogState = iota
	DialogStateConfirmed
	DialogStateTerminated
)

func (s DialogState) String() string {
	switch s {
	case DialogStateEarly:
		return "Early"
	case DialogStateConfirmed:
		return "Confirmed"
	case DialogStateTerminated:
		return "Terminated"
	default:
		return "Unknown"
	}
}

type DialogID struct {
	CallID    string
	LocalTag  string
	RemoteTag string
}

type Dialog struct {
	CreatedAt    time.Time
	ID           DialogID
	LocalURI     string
	RemoteURI    string
	RemoteTarget string
	RouteSet     []string
	LocalSeq     int
	RemoteSeq    int
	state        DialogState
	mu           sync.RWMutex
	Secure       bool
}

func NewDialog(id DialogID, localURI, remoteURI, remoteTarget string) *Dialog {
	return &Dialog{
		ID:           id,
		LocalURI:     localURI,
		RemoteURI:    remoteURI,
		RemoteTarget: remoteTarget,
		CreatedAt:    time.Now(),
		// The initial INVITE that established the dialog used CSeq 1 and is
		// not tracked by this dialog object (the dialog is only created at
		// 2xx time). Seed LocalSeq to 1 so the first IncrementLocalSeq returns
		// 2, keeping in-dialog requests strictly contiguous per RFC 3261 §12.2.1.1.
		LocalSeq: 1,
		state:    DialogStateEarly,
	}
}

func (d *Dialog) State() DialogState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state
}

func (d *Dialog) SetState(s DialogState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = s
}

func (d *Dialog) IncrementLocalSeq() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.LocalSeq++
	return d.LocalSeq
}

func (d *Dialog) SetRemoteSeq(seq int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.RemoteSeq = seq
}

func (d *Dialog) SetRemoteTarget(target string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.RemoteTarget = target
}

func (d *Dialog) IsTerminated() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state == DialogStateTerminated
}
