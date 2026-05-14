package proto

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSDP_Minimal(t *testing.T) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\ns=SDP Seminar\r\nt=3034423619 3042462419\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, 0, sdp.Version)
		assert.Equal(t, "jdoe", sdp.Origin.Username)
		assert.Equal(t, "2890844526", sdp.Origin.SessionID)
		assert.Equal(t, "2890844527", sdp.Origin.SessionVersion)
		assert.Equal(t, "IN", sdp.Origin.NetworkType)
		assert.Equal(t, "IP4", sdp.Origin.AddressType)
		assert.Equal(t, "atlanta.example.com", sdp.Origin.Address)
		assert.Equal(t, "SDP Seminar", sdp.SessionName)
		assert.Len(t, sdp.Times, 1)
		assert.Equal(t, int64(3034423619), sdp.Times[0].Start)
		assert.Equal(t, int64(3042462419), sdp.Times[0].Stop)
		assert.Empty(t, sdp.MediaDescs)
	}
}

func TestSDP_AllSessionFields(t *testing.T) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"i=A Seminar on SDP\r\n" +
		"u=http://www.example.com/seminar/\r\n" +
		"e=j.doe@example.com\r\n" +
		"p=+1 617 555-6011\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"b=CT:1000\r\n" +
		"t=2873397496 2873404696\r\n" +
		"r=604800 3600 0 90000\r\n" +
		"z=2882844526 -1h 2898848070 0\r\n" +
		"k=prompt\r\n" +
		"a=recvonly\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "A Seminar on SDP", sdp.SessionInfo)
		assert.Equal(t, "http://www.example.com/seminar/", sdp.URI)
		assert.Equal(t, []string{"j.doe@example.com"}, sdp.Emails)
		assert.Equal(t, []string{"+1 617 555-6011"}, sdp.Phones)
		assert.NotNil(t, sdp.Connection)
		assert.Equal(t, "IN", sdp.Connection.NetworkType)
		assert.Equal(t, "IP4", sdp.Connection.AddressType)
		assert.Equal(t, "224.2.17.12/127", sdp.Connection.Address)
		assert.Len(t, sdp.Bandwidths, 1)
		assert.Equal(t, "CT", sdp.Bandwidths[0].Type)
		assert.Equal(t, 1000, sdp.Bandwidths[0].Value)
		assert.Equal(t, "2882844526 -1h 2898848070 0", sdp.TimeZone)
		assert.NotNil(t, sdp.Encryption)
		assert.Equal(t, "prompt", sdp.Encryption.Method)
		assert.Equal(t, "", sdp.Encryption.Key)
		assert.Len(t, sdp.Attributes, 1)
		assert.Equal(t, "recvonly", sdp.Attributes[0].Key)
		assert.Equal(t, "", sdp.Attributes[0].Value)
	}
}

func TestSDP_RepeatTime(t *testing.T) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"t=3034423619 3042462419\r\n" +
		"r=604800 3600 0 90000\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.Times, 1)
		assert.Len(t, sdp.Times[0].Repeats, 1)
		assert.Equal(t, int64(604800), sdp.Times[0].Repeats[0].Interval)
		assert.Equal(t, int64(3600), sdp.Times[0].Repeats[0].Duration)
		assert.Equal(t, []int64{0, 90000}, sdp.Times[0].Repeats[0].Offsets)
	}
}

func TestSDP_AttributeFlag(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\na=recvonly\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.Attributes, 1)
		assert.Equal(t, "recvonly", sdp.Attributes[0].Key)
		assert.Equal(t, "", sdp.Attributes[0].Value)
	}
}

func TestSDP_AttributeKeyValue(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\na=rtpmap:0 PCMU/8000\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.Attributes, 1)
		assert.Equal(t, "rtpmap", sdp.Attributes[0].Key)
		assert.Equal(t, "0 PCMU/8000", sdp.Attributes[0].Value)
	}
}

func TestSDP_MediaSection(t *testing.T) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"t=3034423619 3042462419\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.MediaDescs, 1)
		md := sdp.MediaDescs[0]
		assert.Equal(t, "audio", md.Type)
		assert.Equal(t, 49170, md.Port)
		assert.Equal(t, 1, md.PortCount)
		assert.Equal(t, "RTP/AVP", md.Proto)
		assert.Equal(t, []string{"0"}, md.Fmt)
		assert.Len(t, md.Attributes, 1)
		assert.Equal(t, "rtpmap", md.Attributes[0].Key)
		assert.Equal(t, "0 PCMU/8000", md.Attributes[0].Value)
	}
}

func TestSDP_MultipleMediaSections(t *testing.T) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"t=3034423619 3042462419\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"m=video 51372 RTP/AVP 31\r\n" +
		"a=rtpmap:31 H261/90000\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.MediaDescs, 2)
		assert.Equal(t, "audio", sdp.MediaDescs[0].Type)
		assert.Equal(t, 49170, sdp.MediaDescs[0].Port)
		assert.Equal(t, "video", sdp.MediaDescs[1].Type)
		assert.Equal(t, 51372, sdp.MediaDescs[1].Port)
		assert.Equal(t, []string{"31"}, sdp.MediaDescs[1].Fmt)
		assert.Len(t, sdp.MediaDescs[1].Attributes, 1)
	}
}

func TestSDP_MediaWithPortCount(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nm=video 49170/2 RTP/AVP 31\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.MediaDescs, 1)
		md := sdp.MediaDescs[0]
		assert.Equal(t, 49170, md.Port)
		assert.Equal(t, 2, md.PortCount)
	}
}

func TestSDP_MediaWithMultipleFormats(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nm=audio 49170 RTP/AVP 0 8 9\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, []string{"0", "8", "9"}, sdp.MediaDescs[0].Fmt)
	}
}

func TestSDP_MediaLevelFields(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\n" +
		"c=IN IP4 203.0.113.1\r\n" +
		"t=0 0\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"i=Phone call\r\n" +
		"c=IN IP4 203.0.113.2\r\n" +
		"b=AS:64\r\n" +
		"k=clear:abc123\r\n" +
		"a=sendrecv\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		md := sdp.MediaDescs[0]
		assert.Equal(t, "Phone call", md.Title)
		assert.NotNil(t, md.Connection)
		assert.Equal(t, "203.0.113.2", md.Connection.Address)
		assert.Len(t, md.Bandwidths, 1)
		assert.Equal(t, "AS", md.Bandwidths[0].Type)
		assert.Equal(t, 64, md.Bandwidths[0].Value)
		assert.NotNil(t, md.Encryption)
		assert.Equal(t, "clear", md.Encryption.Method)
		assert.Equal(t, "abc123", md.Encryption.Key)
		assert.Len(t, md.Attributes, 1)
		assert.Equal(t, "sendrecv", md.Attributes[0].Key)
	}
}

func TestSDP_IPv6(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP6 2001:db8::1\r\ns=Test\r\n" +
		"c=IN IP6 2001:db8::1\r\n" +
		"t=0 0\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, "IP6", sdp.Origin.AddressType)
		assert.Equal(t, "2001:db8::1", sdp.Origin.Address)
		assert.Equal(t, "IP6", sdp.Connection.AddressType)
		assert.Equal(t, "2001:db8::1", sdp.Connection.Address)
	}
}

func TestSDP_EncryptionKey(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nk=clear:abc123\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.NotNil(t, sdp.Encryption)
		assert.Equal(t, "clear", sdp.Encryption.Method)
		assert.Equal(t, "abc123", sdp.Encryption.Key)
	}
}

func TestSDP_EncryptionMethodOnly(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nk=prompt\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.NotNil(t, sdp.Encryption)
		assert.Equal(t, "prompt", sdp.Encryption.Method)
		assert.Equal(t, "", sdp.Encryption.Key)
	}
}

func TestSDP_Bandwidth(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\n" +
		"b=CT:1000\r\nb=AS:64\r\n" +
		"t=0 0\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.Bandwidths, 2)
		assert.Equal(t, "CT", sdp.Bandwidths[0].Type)
		assert.Equal(t, 1000, sdp.Bandwidths[0].Value)
		assert.Equal(t, "AS", sdp.Bandwidths[1].Type)
		assert.Equal(t, 64, sdp.Bandwidths[1].Value)
	}
}

func TestSDP_EmptyBody(t *testing.T) {
	_, err := UnmarshalSDP(strings.NewReader(""))
	assert.Error(t, err)
}

func TestSDP_MissingVersion(t *testing.T) {
	input := "o=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_MissingOrigin(t *testing.T) {
	input := "v=0\r\ns=Test\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_MissingSessionName(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_MissingTime(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_InvalidVersion(t *testing.T) {
	input := "v=1\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_MalformedLine(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ninvalid-line\r\ns=Test\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_UnknownFieldType(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nx=unknown\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_RoundTrip(t *testing.T) {
	input := "v=0\r\n" +
		"o=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"i=A Seminar on SDP\r\n" +
		"u=http://www.example.com/seminar/\r\n" +
		"e=j.doe@example.com\r\n" +
		"p=+1 617 555-6011\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"b=CT:1000\r\n" +
		"t=3034423619 3042462419\r\n" +
		"r=604800 3600 0 90000\r\n" +
		"k=prompt\r\n" +
		"a=recvonly\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"i=Phone call\r\n" +
		"c=IN IP4 203.0.113.2\r\n" +
		"b=AS:64\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		output := sdp.String()
		sdp2, err := UnmarshalSDP(strings.NewReader(output))
		if assert.NoError(t, err) {
			assert.Equal(t, sdp.Origin, sdp2.Origin)
			assert.Equal(t, sdp.SessionName, sdp2.SessionName)
			assert.Equal(t, sdp.SessionInfo, sdp2.SessionInfo)
			assert.Equal(t, sdp.URI, sdp2.URI)
			assert.Equal(t, sdp.Emails, sdp2.Emails)
			assert.Equal(t, sdp.Phones, sdp2.Phones)
			assert.Equal(t, sdp.Connection, sdp2.Connection)
			assert.Equal(t, sdp.Bandwidths, sdp2.Bandwidths)
			assert.Equal(t, sdp.Encryption, sdp2.Encryption)
			assert.Equal(t, sdp.Attributes, sdp2.Attributes)
			assert.Len(t, sdp2.MediaDescs, 1)
			assert.Equal(t, sdp.MediaDescs[0].Type, sdp2.MediaDescs[0].Type)
			assert.Equal(t, sdp.MediaDescs[0].Port, sdp2.MediaDescs[0].Port)
			assert.Equal(t, sdp.MediaDescs[0].PortCount, sdp2.MediaDescs[0].PortCount)
			assert.Equal(t, sdp.MediaDescs[0].Proto, sdp2.MediaDescs[0].Proto)
			assert.Equal(t, sdp.MediaDescs[0].Fmt, sdp2.MediaDescs[0].Fmt)
			assert.Equal(t, sdp.MediaDescs[0].Title, sdp2.MediaDescs[0].Title)
			assert.Equal(t, sdp.MediaDescs[0].Connection, sdp2.MediaDescs[0].Connection)
			assert.Equal(t, sdp.MediaDescs[0].Bandwidths, sdp2.MediaDescs[0].Bandwidths)
			assert.Equal(t, sdp.MediaDescs[0].Attributes, sdp2.MediaDescs[0].Attributes)
		}
	}
}

func TestSDP_UnboundedTime(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.Times, 1)
		assert.Equal(t, int64(0), sdp.Times[0].Start)
		assert.Equal(t, int64(0), sdp.Times[0].Stop)
	}
}

func TestSDP_MultipleTimes(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\n" +
		"t=3034423619 3042462419\r\n" +
		"t=3042462419 3050501219\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Len(t, sdp.Times, 2)
		assert.Equal(t, int64(3034423619), sdp.Times[0].Start)
		assert.Equal(t, int64(3042462419), sdp.Times[0].Stop)
		assert.Equal(t, int64(3042462419), sdp.Times[1].Start)
		assert.Equal(t, int64(3050501219), sdp.Times[1].Stop)
	}
}

func TestSDP_MultipleEmails(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\n" +
		"e=alice@example.com\r\ne=bob@example.com\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, []string{"alice@example.com", "bob@example.com"}, sdp.Emails)
	}
}

func TestSDP_OriginInvalid(t *testing.T) {
	input := "v=0\r\no=too few fields\r\ns=Test\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_ConnectionInvalid(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\n" +
		"c=too few\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_BandwidthInvalid(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nb=nocolon\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_BandwidthInvalidValue(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nb=CT:notanumber\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_MediaInvalid(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nm=too few\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_MediaInvalidPort(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nm=audio notaport RTP/AVP 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_RepeatBeforeTime(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nr=604800 3600\r\nt=0 0\r\n"
	_, err := UnmarshalSDP(strings.NewReader(input))
	assert.Error(t, err)
}

func TestSDP_CRLFVariants(t *testing.T) {
	input := "v=0\no=- 0 0 IN IP4 host.example.com\ns=Test\nt=0 0\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	assert.NoError(t, err)
	assert.Equal(t, "Test", sdp.SessionName)
}

func TestSDP_RFC4566Example(t *testing.T) {
	input := "v=0\r\n" +
		"o=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"i=A Seminar on the Session Description Protocol\r\n" +
		"u=http://www.example.com/seminars/sdp.pdf\r\n" +
		"e=j.doe@example.com (Jane Doe)\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"b=CT:1000\r\n" +
		"t=2873397496 2873404696\r\n" +
		"a=recvonly\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.Equal(t, 0, sdp.Version)
		assert.Equal(t, "jdoe", sdp.Origin.Username)
		assert.Equal(t, "SDP Seminar", sdp.SessionName)
		assert.Equal(t, "A Seminar on the Session Description Protocol", sdp.SessionInfo)
		assert.Equal(t, "http://www.example.com/seminars/sdp.pdf", sdp.URI)
		assert.Equal(t, "j.doe@example.com (Jane Doe)", sdp.Emails[0])
		assert.Equal(t, "224.2.17.12/127", sdp.Connection.Address)
		assert.Len(t, sdp.Bandwidths, 1)
		assert.Len(t, sdp.Times, 1)
		assert.Len(t, sdp.MediaDescs, 1)
		assert.Equal(t, "audio", sdp.MediaDescs[0].Type)
		assert.Equal(t, 49170, sdp.MediaDescs[0].Port)
		assert.Equal(t, "RTP/AVP", sdp.MediaDescs[0].Proto)
		assert.Equal(t, []string{"0"}, sdp.MediaDescs[0].Fmt)
	}
}

func TestSDP_StartStopTime(t *testing.T) {
	input := "v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\n"
	sdp, err := UnmarshalSDP(strings.NewReader(input))
	if assert.NoError(t, err) {
		assert.True(t, sdp.StartTime().IsZero())
		assert.True(t, sdp.StopTime().IsZero())
	}

	input2 := fmt.Sprintf("v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=%d %d\r\n",
		int64(3780064800), int64(3780064800+3600))
	sdp2, err := UnmarshalSDP(strings.NewReader(input2))
	if assert.NoError(t, err) {
		assert.False(t, sdp2.StartTime().IsZero())
		assert.False(t, sdp2.StopTime().IsZero())
	}
	assert.NoError(t, err)
}

func BenchmarkSDP_Minimal(b *testing.B) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\ns=SDP Seminar\r\nt=3034423619 3042462419\r\n"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalSDP(strings.NewReader(input))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSDP_FullFeatured(b *testing.B) {
	input := "v=0\r\n" +
		"o=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"i=A Seminar on SDP\r\n" +
		"u=http://www.example.com/seminar/\r\n" +
		"e=j.doe@example.com\r\n" +
		"p=+1 617 555-6011\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"b=CT:1000\r\n" +
		"t=3034423619 3042462419\r\n" +
		"r=604800 3600 0 90000\r\n" +
		"z=2882844526 -1h 2898848070 0\r\n" +
		"k=prompt\r\n" +
		"a=recvonly\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"i=Phone call\r\n" +
		"c=IN IP4 203.0.113.2\r\n" +
		"b=AS:64\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalSDP(strings.NewReader(input))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSDP_ManyMedia(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nc=IN IP4 203.0.113.1\r\nt=0 0\r\n")
	for i := 0; i < 20; i++ {
		sb.WriteString(fmt.Sprintf("m=audio %d RTP/AVP 0\r\n", 10000+i))
		sb.WriteString("a=rtpmap:0 PCMU/8000\r\n")
		sb.WriteString("a=ptime:20\r\n")
	}
	input := sb.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalSDP(strings.NewReader(input))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestSDP_MarshalRoundTrip(t *testing.T) {
	input := "v=0\r\n" +
		"o=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"i=A Seminar on SDP\r\n" +
		"u=http://www.example.com/seminar/\r\n" +
		"e=j.doe@example.com\r\n" +
		"p=+1 617 555-6011\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"b=CT:1000\r\n" +
		"t=3034423619 3042462419\r\n" +
		"r=604800 3600 0 90000\r\n" +
		"k=prompt\r\n" +
		"a=recvonly\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"i=Phone call\r\n" +
		"c=IN IP4 203.0.113.2\r\n" +
		"b=AS:64\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"
	sdp, err := UnmarshalSDPBytes([]byte(input))
	if !assert.NoError(t, err) {
		return
	}
	data, err := sdp.Marshal()
	if !assert.NoError(t, err) {
		return
	}
	sdp2, err := UnmarshalSDPBytes(data)
	if assert.NoError(t, err) {
		assert.Equal(t, sdp.Origin, sdp2.Origin)
		assert.Equal(t, sdp.SessionName, sdp2.SessionName)
		assert.Equal(t, sdp.Connection, sdp2.Connection)
		assert.Equal(t, sdp.Bandwidths, sdp2.Bandwidths)
		assert.Equal(t, sdp.Encryption, sdp2.Encryption)
		assert.Equal(t, sdp.Attributes, sdp2.Attributes)
		assert.Equal(t, sdp.TimeZone, sdp2.TimeZone)
		assert.Len(t, sdp2.MediaDescs, 1)
		assert.Equal(t, sdp.MediaDescs[0].Type, sdp2.MediaDescs[0].Type)
		assert.Equal(t, sdp.MediaDescs[0].Port, sdp2.MediaDescs[0].Port)
		assert.Equal(t, sdp.MediaDescs[0].Connection, sdp2.MediaDescs[0].Connection)
	}
}

func TestSDP_MarshalTo_BufferTooSmall(t *testing.T) {
	sdp := &SDP{
		Version:     0,
		Origin:      Origin{Username: "-", SessionID: "0", SessionVersion: "0", NetworkType: "IN", AddressType: "IP4", Address: "0.0.0.0"},
		SessionName: "Test",
		Times:       []TimeDescription{{Start: 0, Stop: 0}},
	}
	_, err := sdp.MarshalTo([]byte{})
	assert.Error(t, err)
}

func TestSDP_MarshalSize_MatchesActual(t *testing.T) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\ns=SDP Seminar\r\nt=3034423619 3042462419\r\n"
	sdp, err := UnmarshalSDPBytes([]byte(input))
	if !assert.NoError(t, err) {
		return
	}
	data, err := sdp.Marshal()
	if assert.NoError(t, err) {
		assert.Equal(t, sdp.MarshalSize(), len(data))
	}
}

func TestSDP_Marshal_AllSessionFields(t *testing.T) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"i=A Seminar on SDP\r\n" +
		"u=http://www.example.com/seminar/\r\n" +
		"e=j.doe@example.com\r\n" +
		"p=+1 617 555-6011\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"b=CT:1000\r\n" +
		"t=2873397496 2873404696\r\n" +
		"r=604800 3600 0 90000\r\n" +
		"z=2882844526 -1h 2898848070 0\r\n" +
		"k=prompt\r\n" +
		"a=recvonly\r\n"
	sdp, err := UnmarshalSDPBytes([]byte(input))
	if !assert.NoError(t, err) {
		return
	}
	data, err := sdp.Marshal()
	if assert.NoError(t, err) {
		sdp2, err := UnmarshalSDPBytes(data)
		if assert.NoError(t, err) {
			assert.Equal(t, sdp.SessionInfo, sdp2.SessionInfo)
			assert.Equal(t, sdp.URI, sdp2.URI)
			assert.Equal(t, sdp.Emails, sdp2.Emails)
			assert.Equal(t, sdp.Phones, sdp2.Phones)
			assert.Equal(t, sdp.TimeZone, sdp2.TimeZone)
			assert.Equal(t, sdp.Encryption, sdp2.Encryption)
			assert.Equal(t, sdp.Attributes, sdp2.Attributes)
		}
	}
}

func BenchmarkSDP_LargeAttributes(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("v=0\r\no=- 0 0 IN IP4 host.example.com\r\ns=Test\r\nt=0 0\r\nm=audio 49170 RTP/AVP 8 0 18 101\r\n")
	for i := 0; i < 50; i++ {
		sb.WriteString(fmt.Sprintf("a=fmtp:%d bitrate=%d\r\n", i%128, i*1000))
	}
	sb.WriteString("a=rtpmap:0 PCMU/8000\r\n")
	sb.WriteString("a=rtpmap:18 G729/8000\r\n")
	sb.WriteString("a=rtpmap:101 telephone-event/8000\r\n")
	input := sb.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := UnmarshalSDP(strings.NewReader(input))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSDP_Marshal_Minimal(b *testing.B) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\ns=SDP Seminar\r\nt=3034423619 3042462419\r\n"
	sdp, err := UnmarshalSDPBytes([]byte(input))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := sdp.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSDP_Marshal_FullFeatured(b *testing.B) {
	input := "v=0\r\n" +
		"o=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\n" +
		"s=SDP Seminar\r\n" +
		"i=A Seminar on SDP\r\n" +
		"u=http://www.example.com/seminar/\r\n" +
		"e=j.doe@example.com\r\n" +
		"p=+1 617 555-6011\r\n" +
		"c=IN IP4 224.2.17.12/127\r\n" +
		"b=CT:1000\r\n" +
		"t=3034423619 3042462419\r\n" +
		"r=604800 3600 0 90000\r\n" +
		"z=2882844526 -1h 2898848070 0\r\n" +
		"k=prompt\r\n" +
		"a=recvonly\r\n" +
		"m=audio 49170 RTP/AVP 0\r\n" +
		"i=Phone call\r\n" +
		"c=IN IP4 203.0.113.2\r\n" +
		"b=AS:64\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n"
	sdp, err := UnmarshalSDPBytes([]byte(input))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := sdp.Marshal()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSDP_MarshalTo_Minimal(b *testing.B) {
	input := "v=0\r\no=jdoe 2890844526 2890844527 IN IP4 atlanta.example.com\r\ns=SDP Seminar\r\nt=3034423619 3042462419\r\n"
	sdp, err := UnmarshalSDPBytes([]byte(input))
	if err != nil {
		b.Fatal(err)
	}
	buf := make([]byte, sdp.MarshalSize())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := sdp.MarshalTo(buf)
		if err != nil {
			b.Fatal(err)
		}
	}
}
