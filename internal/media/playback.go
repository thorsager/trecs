package media

import (
	"context"
	"encoding/binary"
	"math"
	"math/rand"
	"net"
	"time"

	"github.com/thorsager/trecs/proto"
)

const (
	targetSampleRate = 8000 // Hz
	frameInterval    = 20 * time.Millisecond
)

// μ-law clip and bias constants (ITU-T G.711).
const (
	uLawClip = 0x7F7B // 32635 — max PCM magnitude before bias
	uLawBias = 0x84   // 132 — bias added before segment encoding
)

// ulawSegment maps the top 8 bits of a biased sample to its μ-law segment.
var ulawSegment = [256]uint8{
	0, 0, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3, 3,
	4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
	5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
	5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
}

// muLawTable is a pre-computed lookup table mapping 16-bit PCM to μ-law (ITU-T G.711).
// Index with uint16(sample) for O(1) encoding.
var muLawTable [1 << 16]byte

func init() {
	for i := range muLawTable {
		frame := int16(i)
		sign := byte((frame >> 8) & 0x80)
		if sign != 0 {
			if frame == math.MinInt16 {
				frame = math.MaxInt16
			} else {
				frame = -frame
			}
		}
		if frame > uLawClip {
			frame = uLawClip
		}
		frame += uLawBias
		segment := ulawSegment[(frame>>7)&0xFF]
		bottom := (frame >> (segment + 3)) & 0x0F
		muLawTable[i] = ^(sign | segment<<4 | byte(bottom))
	}
}

func muLawEncode(sample int16) byte {
	return muLawTable[uint16(sample)]
}

func convertToMuLaw16(samples []int16) []byte {
	out := make([]byte, len(samples))
	for i, s := range samples {
		out[i] = muLawTable[uint16(s)]
	}
	return out
}

// downsample downsamples audio from inRate to targetRate by linear interpolation.
// targets must be pre-allocated to ceil(len(in) * targetRate / inRate).
func downsample(in []int16, inRate, targetRate uint32) []int16 {
	if inRate == targetRate {
		out := make([]int16, len(in))
		copy(out, in)
		return out
	}
	ratio := float64(inRate) / float64(targetRate)
	outLen := int(math.Ceil(float64(len(in)) / ratio))
	out := make([]int16, outLen)
	for i := range out {
		srcIdx := float64(i) * ratio
		lo := int(srcIdx)
		hi := lo + 1
		frac := srcIdx - float64(lo)
		if hi >= len(in) {
			out[i] = in[lo]
		} else {
			out[i] = int16(float64(in[lo])*(1-frac) + float64(in[hi])*frac)
		}
	}
	return out
}

// RunFilePlayback reads WAV data, converts to 8000 Hz μ-law, and streams it
// as RTP packets to the remote address. Returns a done channel that closes when
// playback completes or the context is canceled.
func RunFilePlayback(ctx context.Context, conn *RTPConn, remoteAddr *net.UDPAddr, payloadType uint8, wav *WavData) <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done)

		pcmSamples := readPCMSamples(wav)

		if len(pcmSamples) == 0 {
			return
		}

		monoSamples := mixToMono(pcmSamples, int(wav.NumChannels))

		resampled := downsample(monoSamples, wav.SampleRate, targetSampleRate)

		muLaw := convertToMuLaw16(resampled)

		serverSSRC := rand.Uint32() //nolint:gosec // SSRC doesn't need cryptographic randomness
		var seq uint16
		var timestamp uint32

		out := &proto.RTPPacket{
			Header: proto.RTPHeader{
				Version: 2, PayloadType: payloadType, SSRC: serverSSRC,
			},
		}

		for offset := 0; offset < len(muLaw); offset += samplesPerFrame {
			select {
			case <-ctx.Done():
				return
			default:
			}

			end := offset + samplesPerFrame
			if end > len(muLaw) {
				end = len(muLaw)
			}
			frame := muLaw[offset:end]

			if len(frame) < samplesPerFrame {
				padded := make([]byte, samplesPerFrame)
				copy(padded, frame)
				frame = padded
			}

			out.Header.SequenceNumber = seq
			out.Header.Timestamp = timestamp
			out.Payload = frame

			if err := conn.WriteRTP(out, remoteAddr); err != nil {
				return
			}

			seq++
			timestamp += samplesPerFrame

			time.Sleep(frameInterval)
		}
	}()

	return done
}

// readPCMSamples converts raw WAV data bytes to int16 samples regardless of
// original bit depth (8-bit unsigned or 16-bit signed).
func readPCMSamples(w *WavData) []int16 {
	bytesPerSample := int(w.BitsPerSample) / 8
	if bytesPerSample == 0 {
		return nil
	}
	totalSamples := len(w.Data) / bytesPerSample
	out := make([]int16, totalSamples)

	switch w.BitsPerSample {
	case 8:
		for i, b := range w.Data {
			out[i] = int16(b) - 128
		}
	case 16:
		for i := range out {
			off := i * 2
			out[i] = int16(binary.LittleEndian.Uint16(w.Data[off:]))
		}
	}
	return out
}

// mixToMono averages stereo samples to mono.
func mixToMono(samples []int16, channels int) []int16 {
	if channels <= 1 {
		return samples
	}
	outLen := len(samples) / channels
	out := make([]int16, outLen)
	for i := range outLen {
		var sum int32
		for ch := range channels {
			sum += int32(samples[i*channels+ch])
		}
		out[i] = int16(sum / int32(channels))
	}
	return out
}
