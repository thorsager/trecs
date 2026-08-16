package b2bua

import (
	"testing"
	"time"

	"github.com/thorsager/trecs/proto"
)

func TestParseSessionExpires(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantSE  time.Duration
		wantRef string
	}{
		{
			name:    "empty header",
			header:  "",
			wantSE:  DefaultSessionExpires,
			wantRef: "uac",
		},
		{
			name:    "seconds only",
			header:  "1800",
			wantSE:  1800 * time.Second,
			wantRef: "uac",
		},
		{
			name:    "with refresher uac",
			header:  "3600;refresher=uac",
			wantSE:  3600 * time.Second,
			wantRef: "uac",
		},
		{
			name:    "with refresher uas",
			header:  "900;refresher=uas",
			wantSE:  900 * time.Second,
			wantRef: "uas",
		},
		{
			name:    "with refresher and extra params",
			header:  "1800;refresher=uac;other=value",
			wantSE:  1800 * time.Second,
			wantRef: "uac",
		},
		{
			name:    "invalid number",
			header:  "invalid",
			wantSE:  DefaultSessionExpires,
			wantRef: "uac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSE, gotRef := ParseSessionExpires(tt.header)
			if gotSE != tt.wantSE {
				t.Errorf("ParseSessionExpires(%q) interval = %v, want %v", tt.header, gotSE, tt.wantSE)
			}
			if gotRef != tt.wantRef {
				t.Errorf("ParseSessionExpires(%q) refresher = %q, want %q", tt.header, gotRef, tt.wantRef)
			}
		})
	}
}

func TestParseMinSE(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty header", "", 0},
		{"valid seconds", "90", 90 * time.Second},
		{"large value", "3600", 3600 * time.Second},
		{"invalid", "abc", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMinSE(tt.header)
			if got != tt.want {
				t.Errorf("ParseMinSE(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestHasTimerSupport(t *testing.T) {
	tests := []struct {
		name string
		msg  *proto.SIPMessage
		want bool
	}{
		{
			name: "no Supported header",
			msg:  &proto.SIPMessage{Headers: make(proto.SIPHeaders)},
			want: false,
		},
		{
			name: "Supported without timer",
			msg: &proto.SIPMessage{Headers: proto.SIPHeaders{
				"Supported": []string{"100rel"},
			}},
			want: false,
		},
		{
			name: "Supported with timer",
			msg: &proto.SIPMessage{Headers: proto.SIPHeaders{
				"Supported": []string{"timer"},
			}},
			want: true,
		},
		{
			name: "Supported with timer and other tags",
			msg: &proto.SIPMessage{Headers: proto.SIPHeaders{
				"Supported": []string{"100rel, timer"},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasTimerSupport(tt.msg)
			if got != tt.want {
				t.Errorf("HasTimerSupport() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatSessionExpires(t *testing.T) {
	got := FormatSessionExpires(1800, "uac")
	want := "1800;refresher=uac"
	if got != want {
		t.Errorf("FormatSessionExpires(1800, uac) = %q, want %q", got, want)
	}
}

func TestFormatMinSE(t *testing.T) {
	got := FormatMinSE(90)
	want := "90"
	if got != want {
		t.Errorf("FormatMinSE(90) = %q, want %q", got, want)
	}
}

func TestDurationToSeconds(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want int
	}{
		{0, 0},
		{90 * time.Second, 90},
		{1800 * time.Second, 1800},
		{90500 * time.Millisecond, 91}, // rounds up
	}

	for _, tt := range tests {
		got := DurationToSeconds(tt.d)
		if got != tt.want {
			t.Errorf("DurationToSeconds(%v) = %d, want %d", tt.d, got, tt.want)
		}
	}
}
