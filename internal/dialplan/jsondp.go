package dialplan

import (
	"encoding/json"
	"os"

	"github.com/thorsager/trecs/internal/sip"
	"github.com/thorsager/trecs/proto"
)

type jsonEntry struct {
	Action string `json:"action"`
	File   string `json:"file,omitempty"`
}

type jsonDialplan struct {
	entries map[string]Entry
}

type jsonConfig struct {
	Extensions map[string]jsonEntry `json:"extensions"`
}

func NewFromFile(path string) (Dialplan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg jsonConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	entries := make(map[string]Entry, len(cfg.Extensions))
	for ext, je := range cfg.Extensions {
		switch je.Action {
		case "echo":
			entries[ext] = Entry{Action: ActionEcho}
		case "play":
			entries[ext] = Entry{Action: ActionPlay, File: je.File}
		}
	}
	return &jsonDialplan{entries: entries}, nil
}

func (j *jsonDialplan) Lookup(req *proto.SIPMessage) (*Entry, bool) {
	user := sip.ExtractUser(req.RequestURI())
	e, ok := j.entries[user]
	if !ok {
		e, ok = j.entries["*"]
	}
	if !ok {
		return nil, false
	}
	return &e, true
}
