package dialplan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/proto"
)

func helperMakeRequest(uri string) *proto.SIPMessage {
	raw := "INVITE " + uri + " SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK.test\r\nFrom: <sip:test@127.0.0.1>;tag=abc\r\nTo: <sip:test@127.0.0.1>\r\nCall-ID: test123\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		panic(err)
	}
	return msg
}

func TestStaticDialplan_Lookup_Match(t *testing.T) {
	dp := NewStatic(map[string]Entry{
		"echo": {Action: ActionEcho},
		"play": {Action: ActionPlay, File: "/tmp/tone.wav"},
	})

	req := helperMakeRequest("sip:echo@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	require.True(t, ok)
	assert.Equal(t, ActionEcho, entry.Action)
	assert.Empty(t, entry.File)
}

func TestStaticDialplan_Lookup_Play(t *testing.T) {
	dp := NewStatic(map[string]Entry{
		"play": {Action: ActionPlay, File: "/tmp/tone.wav"},
	})

	req := helperMakeRequest("sip:play@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	require.True(t, ok)
	assert.Equal(t, ActionPlay, entry.Action)
	assert.Equal(t, "/tmp/tone.wav", entry.File)
}

func TestStaticDialplan_Lookup_NoMatch(t *testing.T) {
	dp := NewStatic(map[string]Entry{
		"echo": {Action: ActionEcho},
	})

	req := helperMakeRequest("sip:unknown@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	assert.False(t, ok)
	assert.Nil(t, entry)
}

func TestStaticDialplan_Lookup_Wildcard(t *testing.T) {
	dp := NewStatic(map[string]Entry{
		"*": {Action: ActionPlay, File: "/tmp/tone.wav"},
	})

	req := helperMakeRequest("sip:anything@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	require.True(t, ok)
	assert.Equal(t, ActionPlay, entry.Action)
	assert.Equal(t, "/tmp/tone.wav", entry.File)
}

func TestStaticDialplan_Lookup_ExactOverridesWildcard(t *testing.T) {
	dp := NewStatic(map[string]Entry{
		"*":    {Action: ActionPlay, File: "/tmp/fallback.wav"},
		"echo": {Action: ActionEcho},
	})

	req := helperMakeRequest("sip:echo@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	require.True(t, ok)
	assert.Equal(t, ActionEcho, entry.Action)

	req2 := helperMakeRequest("sip:unknown@127.0.0.1:5060")
	entry2, ok2 := dp.Lookup(req2)
	require.True(t, ok2)
	assert.Equal(t, ActionPlay, entry2.Action)
	assert.Equal(t, "/tmp/fallback.wav", entry2.File)
}

func TestStaticDialplan_Lookup_Empty(t *testing.T) {
	dp := NewStatic(map[string]Entry{})

	req := helperMakeRequest("sip:echo@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	assert.False(t, ok)
	assert.Nil(t, entry)
}
