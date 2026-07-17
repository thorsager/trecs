package media

import (
	"context"
	"math"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMuLawEncode_ReferenceValues(t *testing.T) {
	tests := []struct {
		name     string
		input    int16
		expected byte
	}{
		{"zero", 0, 0xFF},
		{"positive_one", 1, 0xFF},
		{"negative_one", -1, 0x7F},
		{"positive_100", 100, 0xF2},
		{"negative_100", -100, 0x72},
		{"positive_256", 256, 0xE7},
		{"negative_256", -256, 0x67},
		{"positive_8031", 8031, 0xA0},
		{"negative_8031", -8031, 0x20},
		{"max_positive", math.MaxInt16, 0x80},
		{"min_negative", math.MinInt16, 0x00},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := muLawEncode(tt.input)
			if got != tt.expected {
				t.Errorf("muLawEncode(%d) = 0x%02X, want 0x%02X", tt.input, got, tt.expected)
			}
		})
	}
}

func TestMuLawEncode_Symmetry(t *testing.T) {
	inputs := []int16{100, 1000, 5000, 20000, 30000, math.MaxInt16}
	for _, v := range inputs {
		pos := muLawEncode(v)
		neg := muLawEncode(-v)
		if pos^neg != 0x80 {
			t.Errorf("symmetry broken for %d: pos=0x%02X neg=0x%02X (xor=0x%02X, want 0x80)", v, pos, neg, pos^neg)
		}
	}
}

func TestMuLawEncode_ClampMax(t *testing.T) {
	got := muLawEncode(math.MaxInt16)
	if got != 0x80 {
		t.Errorf("muLawEncode(MaxInt16) = 0x%02X, want 0x80", got)
	}
}

func TestMuLawEncode_ClampMin(t *testing.T) {
	got := muLawEncode(math.MinInt16)
	if got != 0x00 {
		t.Errorf("muLawEncode(MinInt16) = 0x%02X, want 0x00", got)
	}
}

// pidatoULaw2LPCM is the μ-law decode table from pidato/audio (G.711 standard).
var pidatoULaw2LPCM = [256]int16{
	-32124, -31100, -30076, -29052, -28028, -27004, -25980, -24956,
	-23932, -22908, -21884, -20860, -19836, -18812, -17788, -16764,
	-15996, -15484, -14972, -14460, -13948, -13436, -12924, -12412,
	-11900, -11388, -10876, -10364, -9852, -9340, -8828, -8316,
	-7932, -7676, -7420, -7164, -6908, -6652, -6396, -6140,
	-5884, -5628, -5372, -5116, -4860, -4604, -4348, -4092,
	-3900, -3772, -3644, -3516, -3388, -3260, -3132, -3004,
	-2876, -2748, -2620, -2492, -2364, -2236, -2108, -1980,
	-1884, -1820, -1756, -1692, -1628, -1564, -1500, -1436,
	-1372, -1308, -1244, -1180, -1116, -1052, -988, -924,
	-876, -844, -812, -780, -748, -716, -684, -652,
	-620, -588, -556, -524, -492, -460, -428, -396,
	-372, -356, -340, -324, -308, -292, -276, -260,
	-244, -228, -212, -196, -180, -164, -148, -132,
	-120, -112, -104, -96, -88, -80, -72, -64,
	-56, -48, -40, -32, -24, -16, -8, 0,
	32124, 31100, 30076, 29052, 28028, 27004, 25980, 24956,
	23932, 22908, 21884, 20860, 19836, 18812, 17788, 16764,
	15996, 15484, 14972, 14460, 13948, 13436, 12924, 12412,
	11900, 11388, 10876, 10364, 9852, 9340, 8828, 8316,
	7932, 7676, 7420, 7164, 6908, 6652, 6396, 6140,
	5884, 5628, 5372, 5116, 4860, 4604, 4348, 4092,
	3900, 3772, 3644, 3516, 3388, 3260, 3132, 3004,
	2876, 2748, 2620, 2492, 2364, 2236, 2108, 1980,
	1884, 1820, 1756, 1692, 1628, 1564, 1500, 1436,
	1372, 1308, 1244, 1180, 1116, 1052, 988, 924,
	876, 844, 812, 780, 748, 716, 684, 652,
	620, 588, 556, 524, 492, 460, 428, 396,
	372, 356, 340, 324, 308, 292, 276, 260,
	244, 228, 212, 196, 180, 164, 148, 132,
	120, 112, 104, 96, 88, 80, 72, 64,
	56, 48, 40, 32, 24, 16, 8, 0,
}

func TestMuLawEncode_DecodeRoundtrip(t *testing.T) {
	// Verify that every encoded value decodes back to within quantization error.
	for i := 0; i < 65536; i++ {
		code := muLawTable[i]
		decoded := pidatoULaw2LPCM[code]
		input := int(int16(uint16(i)))
		dist := input - int(decoded)
		if dist < 0 {
			dist = -dist
		}
		if dist > 8000 {
			t.Fatalf("PCM %d → 0x%02X → decoded %d (dist=%d)", input, code, decoded, dist)
		}
	}
}

func TestMuLawEncode_ZeroCode(t *testing.T) {
	// Both 0x7F and 0xFF decode to 0. Our encoder uses 0xFF for PCM 0.
	code := muLawTable[0]
	decoded := pidatoULaw2LPCM[code]
	if decoded != 0 {
		t.Errorf("PCM 0 → 0x%02X → decoded %d, want 0", code, decoded)
	}
	code = muLawTable[math.MaxUint16]
	decoded = pidatoULaw2LPCM[code]
	if decoded != 0 {
		t.Errorf("PCM -1 → 0x%02X → decoded %d, want 0", code, decoded)
	}
}

func TestConvertToMuLaw16_Length(t *testing.T) {
	samples := []int16{0, 100, 200, 300}
	out := convertToMuLaw16(samples)
	if len(out) != len(samples) {
		t.Errorf("convertToMuLaw16 length = %d, want %d", len(out), len(samples))
	}
}

func BenchmarkMuLawEncode(b *testing.B) {
	inputs := []int16{0, 1, 100, 1000, 8031, math.MaxInt16, math.MinInt16}
	b.ResetTimer()
	for range b.N {
		for _, s := range inputs {
			muLawEncode(s)
		}
	}
}

func BenchmarkConvertToMuLaw16(b *testing.B) {
	samples := make([]int16, 8000)
	for i := range samples {
		samples[i] = int16(i * 4)
	}
	b.ResetTimer()
	for range b.N {
		convertToMuLaw16(samples)
	}
}

func BenchmarkDownsample(b *testing.B) {
	in := make([]int16, 48000)
	for i := range in {
		in[i] = int16(i)
	}
	b.ResetTimer()
	for range b.N {
		downsample(in, 48000, 8000)
	}
}

func BenchmarkReadPCMSamples16bit(b *testing.B) {
	wav := &WavData{
		SampleRate:    44100,
		BitsPerSample: 16,
		NumChannels:   2,
		Data:          make([]byte, 44100*2*2), // 1 second of stereo 16-bit
	}
	b.ResetTimer()
	for range b.N {
		readPCMSamples(wav)
	}
}

func BenchmarkMixToMonoStereo(b *testing.B) {
	samples := make([]int16, 44100*2)
	for i := range samples {
		samples[i] = int16(i)
	}
	b.ResetTimer()
	for range b.N {
		mixToMono(samples, 2)
	}
}

func TestRunFilePlayback_Timing(t *testing.T) {
	frameCount := 20
	samplesPerTestFrame := 160
	totalSamples := frameCount * samplesPerTestFrame

	wav := &WavData{
		SampleRate:    8000,
		BitsPerSample: 16,
		NumChannels:   1,
		Data:          make([]byte, totalSamples*2),
	}
	for i := range totalSamples {
		val := int16(8000)
		wav.Data[i*2] = byte(val)
		wav.Data[i*2+1] = byte(val >> 8)
	}

	serverConn, err := NewRTPConn()
	require.NoError(t, err)
	defer serverConn.Close()

	clientConn, err := NewRTPConn()
	require.NoError(t, err)
	defer clientConn.Close()

	cliUDP := clientConn.LocalAddr().(*net.UDPAddr)
	clientAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: cliUDP.Port}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := RunFilePlayback(ctx, serverConn, clientAddr, 0, wav)

	type received struct {
		seq     uint16
		ts      uint32
		arrived time.Time
	}
	var packets []received

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		default:
		}

		if len(packets) >= frameCount {
			break
		}

		clientConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		pkt, _, err := clientConn.ReadRTP()
		if err != nil {
			continue
		}
		packets = append(packets, received{
			seq:     pkt.Header.SequenceNumber,
			ts:      pkt.Header.Timestamp,
			arrived: time.Now(),
		})
		clientConn.Release(pkt)
	}

	<-done

	require.Equal(t, frameCount, len(packets), "should receive all packets")

	for i, p := range packets {
		require.Equal(t, uint16(i), p.seq, "seq mismatch at packet %d", i)
		require.Equal(t, uint32(i*samplesPerTestFrame), p.ts, "timestamp mismatch at packet %d", i)
	}

	maxJitter := time.Duration(0)
	for i := 1; i < len(packets); i++ {
		interval := packets[i].arrived.Sub(packets[i-1].arrived)
		jitter := interval - frameInterval
		if jitter < 0 {
			jitter = -jitter
		}
		if jitter > maxJitter {
			maxJitter = jitter
		}
	}
	t.Logf("max per-packet jitter: %v", maxJitter)

	lastDelta := packets[frameCount-1].arrived.Sub(packets[0].arrived)
	expectedLast := time.Duration(frameCount-1) * frameInterval
	drift := lastDelta - expectedLast
	if drift < 0 {
		drift = -drift
	}
	t.Logf("total drift over %d packets: %v", frameCount, drift)

	require.Less(t, maxJitter, 2*time.Millisecond, "per-packet jitter too large")
	require.Less(t, drift, 10*time.Millisecond, "total drift too large")
}
