package speech

import (
	"bytes"
	"encoding/binary"
	"errors"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
)

// InspectAudio derives duration from encoded sample counts instead of trusting
// browser wall clocks. Only the provider-supported PCM16 WAV/Ogg Opus pass.
func InspectAudio(b []byte) (domain.Audio, error) {
	a := domain.Audio{Bytes: b}
	if len(b) > 8<<20 {
		return a, domain.ErrInvalid
	}
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE" {
		var rate, align uint32
		var size uint64
		for pos := 12; pos+8 <= len(b); {
			kind := string(b[pos : pos+4])
			n := int(binary.LittleEndian.Uint32(b[pos+4:]))
			pos += 8
			// Streaming WAV uses an unknown data length until HTTP EOF. Once
			// uploaded as a complete file, its remaining PCM bytes are authoritative.
			if kind == "data" && uint64(n) == 0xffffffff {
				n = len(b) - pos
			}
			if n < 0 || pos+n > len(b) {
				return a, domain.ErrInvalid
			}
			chunk := b[pos : pos+n]
			if kind == "fmt " {
				if n < 16 || binary.LittleEndian.Uint16(chunk) != 1 || binary.LittleEndian.Uint16(chunk[14:]) != 16 {
					return a, domain.ErrInvalid
				}
				ch := binary.LittleEndian.Uint16(chunk[2:])
				rate = binary.LittleEndian.Uint32(chunk[4:])
				align = uint32(binary.LittleEndian.Uint16(chunk[12:]))
				if (ch != 1 && ch != 2) || align != uint32(ch)*2 {
					return a, domain.ErrInvalid
				}
				switch rate {
				case 8000, 16000, 22050, 24000, 32000, 44100, 48000:
				default:
					return a, domain.ErrInvalid
				}
			}
			if kind == "data" {
				size += uint64(n)
			}
			pos += n + n%2
		}
		if rate == 0 || align == 0 || size == 0 || size%uint64(align) != 0 {
			return a, domain.ErrInvalid
		}
		a.Format = "wav"
		a.DurationMS = int64((size*1000 + uint64(rate*align) - 1) / uint64(rate*align))
		return a, nil
	}
	if len(b) >= 27 && string(b[:4]) == "OggS" {
		var granule uint64
		var preSkip uint16
		var decoded, lastSamples uint64
		var packet []byte
		packetIndex := 0
		var serial, sequence uint32
		eos := false
		for pos := 0; pos < len(b); {
			if eos || pos+27 > len(b) || string(b[pos:pos+4]) != "OggS" || b[pos+4] != 0 {
				return a, domain.ErrInvalid
			}
			pageSerial := binary.LittleEndian.Uint32(b[pos+14:])
			pageSeq := binary.LittleEndian.Uint32(b[pos+18:])
			if pos == 0 {
				serial = pageSerial
				sequence = pageSeq
				if b[pos+5]&2 == 0 {
					return a, domain.ErrInvalid
				}
			} else {
				if pageSerial != serial || pageSeq != sequence+1 || b[pos+5]&2 != 0 {
					return a, domain.ErrInvalid
				}
				sequence = pageSeq
			}
			if (b[pos+5]&1 != 0) != (len(packet) > 0) {
				return a, domain.ErrInvalid
			}
			segments := int(b[pos+26])
			if pos+27+segments > len(b) {
				return a, domain.ErrInvalid
			}
			size := 0
			for _, n := range b[pos+27 : pos+27+segments] {
				size += int(n)
			}
			start := pos + 27 + segments
			end := start + size
			if end > len(b) {
				return a, domain.ErrInvalid
			}
			cursor := start
			for _, segment := range b[pos+27 : pos+27+segments] {
				n := int(segment)
				packet = append(packet, b[cursor:cursor+n]...)
				cursor += n
				if len(packet) > 64<<10 {
					return a, domain.ErrInvalid
				}
				if n == 255 {
					continue
				}
				switch packetIndex {
				case 0:
					if len(packet) < 19 || !bytes.Equal(packet[:8], []byte("OpusHead")) || (packet[9] != 1 && packet[9] != 2) {
						return a, domain.ErrInvalid
					}
					preSkip = binary.LittleEndian.Uint16(packet[10:])
				case 1:
					if !bytes.HasPrefix(packet, []byte("OpusTags")) {
						return a, domain.ErrInvalid
					}
				default:
					samples, e := domain.OpusSamples48k(packet)
					if e != nil {
						return a, e
					}
					lastSamples = uint64(samples)
					decoded += lastSamples
				}
				packetIndex++
				packet = packet[:0]
			}
			g := binary.LittleEndian.Uint64(b[pos+6:])
			if g != ^uint64(0) {
				granule = g
			}
			eos = b[pos+5]&4 != 0
			pos = end
		}
		if !eos || len(packet) > 0 || packetIndex < 3 || granule <= uint64(preSkip) || granule > decoded || decoded-granule > lastSamples {
			return a, domain.ErrInvalid
		}
		a.Format = "ogg"
		a.DurationMS = int64((granule - uint64(preSkip)) * 1000 / 48000)
		return a, nil
	}
	return a, errors.Join(domain.ErrInvalid, errors.New("PCM16 WAV or Ogg Opus required"))
}
