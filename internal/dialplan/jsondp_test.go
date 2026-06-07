package dialplan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thorsager/trecs/proto"
)

func helperMakeRequestJSON(uri string) *proto.SIPMessage {
	raw := "INVITE " + uri + " SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK.test\r\nFrom: <sip:test@127.0.0.1>;tag=abc\r\nTo: <sip:test@127.0.0.1>\r\nCall-ID: test123\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"
	msg, err := proto.UnmarshalSIPDatagram([]byte(raw))
	if err != nil {
		panic(err)
	}
	return msg
}

func TestJSONDialplan_NewFromFile(t *testing.T) {
	content := `{
		"extensions": {
			"echo": { "action": "echo" },
			"play": { "action": "play", "file": "/tmp/tone.wav" }
		}
	}`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dialplan.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	dp, err := NewFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, dp)
}

func TestJSONDialplan_NewFromFile_NotFound(t *testing.T) {
	_, err := NewFromFile("/nonexistent/path.json")
	require.Error(t, err)
}

func TestJSONDialplan_NewFromFile_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{invalid json"), 0o600))

	_, err := NewFromFile(path)
	require.Error(t, err)
}

func TestJSONDialplan_Lookup_Echo(t *testing.T) {
	content := `{
		"extensions": {
			"echo": { "action": "echo" }
		}
	}`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dialplan.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	dp, err := NewFromFile(path)
	require.NoError(t, err)

	req := helperMakeRequestJSON("sip:echo@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	require.True(t, ok)
	assert.Equal(t, ActionEcho, entry.Action)
	assert.Empty(t, entry.File)
}

func TestJSONDialplan_Lookup_Play(t *testing.T) {
	content := `{
		"extensions": {
			"play": { "action": "play", "file": "/tmp/tone.wav" }
		}
	}`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dialplan.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	dp, err := NewFromFile(path)
	require.NoError(t, err)

	req := helperMakeRequestJSON("sip:play@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	require.True(t, ok)
	assert.Equal(t, ActionPlay, entry.Action)
	assert.Equal(t, "/tmp/tone.wav", entry.File)
}

func TestJSONDialplan_Lookup_NoMatch(t *testing.T) {
	content := `{
		"extensions": {
			"echo": { "action": "echo" }
		}
	}`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dialplan.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	dp, err := NewFromFile(path)
	require.NoError(t, err)

	req := helperMakeRequestJSON("sip:unknown@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	assert.False(t, ok)
	assert.Nil(t, entry)
}

func TestJSONDialplan_Lookup_UnknownAction(t *testing.T) {
	content := `{
		"extensions": {
			"unknown": { "action": "foobar" }
		}
	}`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dialplan.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	dp, err := NewFromFile(path)
	require.NoError(t, err)

	req := helperMakeRequestJSON("sip:unknown@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	assert.False(t, ok)
	assert.Nil(t, entry)
}

func TestJSONDialplan_Lookup_EmptyExtensions(t *testing.T) {
	content := `{
		"extensions": {}
	}`

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dialplan.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	dp, err := NewFromFile(path)
	require.NoError(t, err)

	req := helperMakeRequestJSON("sip:echo@127.0.0.1:5060")
	entry, ok := dp.Lookup(req)
	assert.False(t, ok)
	assert.Nil(t, entry)
}
