package imageutil

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"io"
)

// Metadata describes encoded image headers, without decoding pixels. AnimationKnown
// is false when the bounded scan cannot prove whether further frames exist.
type Metadata struct {
	Width, Height, Orientation, BitDepth int
	Animated, AnimationKnown             bool
}

const MetadataReadLimit = 4 << 20

// ReadMetadata reads at most MetadataReadLimit bytes; it never decodes pixels.
func ReadMetadata(r io.Reader) (Metadata, error) {
	b, err := io.ReadAll(io.LimitReader(r, MetadataReadLimit))
	if err != nil {
		return Metadata{}, err
	}
	return Inspect(b)
}

// Inspect parses encoded image headers without decoding pixels. PNG/WebP chunk
// payloads in resident data are skipped by length; both scans cap chunk counts.
func Inspect(data []byte) (Metadata, error) {
	fullData := data
	if len(data) > MetadataReadLimit {
		data = data[:MetadataReadLimit]
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		if err == nil {
			err = errors.New("invalid image dimensions")
		}
		return Metadata{}, err
	}
	m := Metadata{Width: cfg.Width, Height: cfg.Height, Orientation: 1}
	switch format {
	case "jpeg":
		m.BitDepth, m.AnimationKnown = 8, true
		for p := 2; p+4 <= len(data); {
			if data[p] != 0xff {
				break
			}
			marker := data[p+1]
			p += 2
			if marker == 0xda || marker == 0xd9 {
				break
			}
			if marker == 0xff {
				p--
				continue
			}
			n := int(binary.BigEndian.Uint16(data[p : p+2]))
			if n < 2 || p+n > len(data) {
				break
			}
			if marker == 0xe1 && n >= 8 && string(data[p+2:p+8]) == "Exif\x00\x00" {
				m.Orientation = exifOrientation(data[p+8 : p+n])
			}
			if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc && n >= 3 {
				m.BitDepth = int(data[p+2])
			}
			p += n
		}
	case "png":
		data = fullData
		// eXIf may occur after IDAT. An incomplete bounded scan therefore leaves
		// orientation unknown rather than claiming the pixels are upright.
		m.Orientation = 0
		exifSeen := false
		if len(data) >= 25 {
			m.BitDepth = int(data[24])
		}
		for p, chunks := 8, 0; p+12 <= len(data) && chunks < 65536; chunks++ {
			n := uint64(binary.BigEndian.Uint32(data[p : p+4]))
			kind := string(data[p+4 : p+8])
			if kind == "acTL" {
				m.Animated, m.AnimationKnown = true, true
			}
			// PNG requires acTL before the first IDAT; no image data is inspected.
			if kind == "IDAT" || kind == "IEND" {
				m.AnimationKnown = true
			}
			if n > uint64(len(data)-p-12) {
				break
			}
			if kind == "eXIf" {
				m.Orientation = exifOrientation(data[p+8 : p+8+int(n)])
				exifSeen = true
			}
			if kind == "IEND" {
				if !exifSeen {
					m.Orientation = 1
				}
				break
			}
			p += int(n) + 12
		}
	case "webp":
		data = fullData
		m.BitDepth = 8
		if len(data) >= 21 && string(data[12:16]) == "VP8X" {
			m.Animated = data[20]&2 != 0
			m.AnimationKnown = true
			if data[20]&8 != 0 {
				m.Orientation = 0
				for p, chunks := 12, 0; p+8 <= len(data) && chunks < 65536; chunks++ {
					n := uint64(binary.LittleEndian.Uint32(data[p+4 : p+8]))
					if n > uint64(len(data)-p-8) {
						break
					}
					if string(data[p:p+4]) == "EXIF" {
						exif := data[p+8 : p+8+int(n)]
						if len(exif) >= 6 && string(exif[:6]) == "Exif\x00\x00" {
							exif = exif[6:]
						}
						m.Orientation = exifOrientation(exif)
						break
					}
					p += 8 + int(n) + int(n&1)
				}
			}
		} else if len(data) >= 16 && (string(data[12:16]) == "VP8 " || string(data[12:16]) == "VP8L") {
			m.AnimationKnown = true
		}
	case "gif":
		m.BitDepth = 8
		if len(data) < 13 {
			return m, nil
		}
		p := 13
		if data[10]&128 != 0 {
			p += 3 * (1 << ((data[10] & 7) + 1))
		}
		frames := 0
		for p < len(data) {
			kind := data[p]
			p++
			switch kind {
			case 0x3b:
				m.AnimationKnown = true
				return m, nil
			case 0x21:
				if p >= len(data) {
					return m, nil
				}
				p++
			case 0x2c:
				frames++
				if frames > 1 {
					m.Animated, m.AnimationKnown = true, true
					return m, nil
				}
				if p+9 > len(data) {
					return m, nil
				}
				packed := data[p+8]
				p += 9
				if packed&128 != 0 {
					p += 3 * (1 << ((packed & 7) + 1))
				}
				p++ // LZW minimum code size
			default:
				return m, nil
			}
			for {
				if p >= len(data) {
					return m, nil
				}
				n := int(data[p])
				p++
				if n == 0 {
					break
				}
				if n > len(data)-p {
					return m, nil
				}
				p += n
			}
		}
	default:
		return Metadata{}, errors.New("unsupported image headers")
	}
	return m, nil
}

func exifOrientation(b []byte) int {
	if len(b) < 8 {
		return 1
	}
	var order binary.ByteOrder
	if string(b[:2]) == "II" {
		order = binary.LittleEndian
	} else if string(b[:2]) == "MM" {
		order = binary.BigEndian
	} else {
		return 1
	}
	if order.Uint16(b[2:4]) != 42 {
		return 1
	}
	p := uint64(order.Uint32(b[4:8]))
	if p+2 > uint64(len(b)) {
		return 1
	}
	count := int(order.Uint16(b[p : p+2]))
	p += 2
	for i := 0; i < count && p+12 <= uint64(len(b)); i++ {
		e := b[p : p+12]
		if order.Uint16(e[:2]) == 0x112 && order.Uint16(e[2:4]) == 3 && order.Uint32(e[4:8]) == 1 {
			v := int(order.Uint16(e[8:10]))
			if v >= 1 && v <= 8 {
				return v
			}
		}
		p += 12
	}
	return 1
}
