package dialplan

import "github.com/thorsager/trecs/proto"

type Action int

const (
	ActionEcho Action = iota
	ActionPlay
)

type Entry struct {
	Action Action
	File   string
}

type Dialplan interface {
	Lookup(req *proto.SIPMessage) (*Entry, bool)
}
