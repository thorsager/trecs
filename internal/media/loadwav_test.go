package media

import (
	"testing"
)

func TestLoadWav(t *testing.T) {
	w, err := LoadWav("../../EDIS-SCD-02.wav")
	if err != nil {
		t.Fatalf("LoadWav: %v", err)
	}
	if w.SampleRate != 44100 {
		t.Errorf("SampleRate = %d, want 44100", w.SampleRate)
	}
	if w.BitsPerSample != 16 {
		t.Errorf("BitsPerSample = %d, want 16", w.BitsPerSample)
	}
	if w.NumChannels != 1 {
		t.Errorf("NumChannels = %d, want 1", w.NumChannels)
	}
	if len(w.Data) == 0 {
		t.Error("Data is empty")
	}
	t.Logf("OK: %d Hz, %d-bit, %d ch, %d bytes", w.SampleRate, w.BitsPerSample, w.NumChannels, len(w.Data))
}
