package dialplan

import (
	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

type StaticDialplan struct {
	entries map[string]Entry
}

func NewStatic(entries map[string]Entry) *StaticDialplan {
	return &StaticDialplan{entries: entries}
}

func (s *StaticDialplan) Lookup(req *proto.SIPMessage) (*Entry, bool) {
	user := sip.ExtractUser(req.RequestURI())
	e, ok := s.entries[user]
	if !ok {
		e, ok = s.entries["*"]
	}
	if !ok {
		return nil, false
	}
	return &e, true
}
