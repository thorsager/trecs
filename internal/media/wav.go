package media

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type WavData struct {
	SampleRate    uint32
	BitsPerSample uint16
	NumChannels   uint16
	Data          []byte
}

func LoadWav(path string) (*WavData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("wav: %w", err)
	}
	defer f.Close()

	var header [12]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, fmt.Errorf("wav: reading riff header: %w", err)
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return nil, fmt.Errorf("wav: not a valid WAV file")
	}

	var w *WavData
	var data []byte

	for {
		var chunk [8]byte
		if _, err := io.ReadFull(f, chunk[:]); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("wav: reading chunk header: %w", err)
		}
		chunkID := string(chunk[:4])
		chunkSize := binary.LittleEndian.Uint32(chunk[4:8])

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("wav: fmt chunk too small")
			}
			fmtBuf := make([]byte, chunkSize)
			if _, err := io.ReadFull(f, fmtBuf); err != nil {
				return nil, fmt.Errorf("wav: reading fmt chunk: %w", err)
			}
			audioFormat := binary.LittleEndian.Uint16(fmtBuf[0:2])
			if audioFormat != 1 {
				return nil, fmt.Errorf("wav: unsupported audio format %d (only PCM supported)", audioFormat)
			}
			w = &WavData{
				NumChannels:   binary.LittleEndian.Uint16(fmtBuf[2:4]),
				SampleRate:    binary.LittleEndian.Uint32(fmtBuf[4:8]),
				BitsPerSample: binary.LittleEndian.Uint16(fmtBuf[14:16]),
			}

		case "data":
			if w == nil {
				return nil, fmt.Errorf("wav: data chunk before fmt chunk")
			}
			data = make([]byte, chunkSize)
			if _, err := io.ReadFull(f, data); err != nil {
				return nil, fmt.Errorf("wav: reading data chunk: %w", err)
			}

		default:
			if chunkSize > 1<<30 {
				return nil, fmt.Errorf("wav: chunk %q too large (%d)", chunkID, chunkSize)
			}
			if _, err := io.CopyN(io.Discard, f, int64(chunkSize)); err != nil {
				return nil, fmt.Errorf("wav: skipping chunk %q: %w", chunkID, err)
			}
		}
	}

	if w == nil {
		return nil, fmt.Errorf("wav: no fmt chunk found")
	}
	if data == nil {
		return nil, fmt.Errorf("wav: no data chunk found")
	}

	w.Data = data
	return w, nil
}
