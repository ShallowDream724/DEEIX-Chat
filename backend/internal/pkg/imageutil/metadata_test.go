package imageutil

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

func TestInspectPNGAndAPNGHeaders(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 7, 5))); err != nil {
		t.Fatal(err)
	}
	m, err := Inspect(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if m.Width != 7 || m.Height != 5 || m.BitDepth != 8 || m.Animated || !m.AnimationKnown {
		t.Fatalf("unexpected PNG metadata: %+v", m)
	}
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk, 8)
	copy(chunk[4:], "acTL")
	binary.BigEndian.PutUint32(chunk[8:], 2)
	binary.BigEndian.PutUint32(chunk[16:], crc32.ChecksumIEEE(chunk[4:16]))
	data := append(append(append([]byte{}, b.Bytes()[:33]...), chunk...), b.Bytes()[33:]...)
	m, err = Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Animated || !m.AnimationKnown {
		t.Fatalf("APNG must be animated: %+v", m)
	}
	m, err = Inspect(b.Bytes()[:33])
	if err != nil {
		t.Fatal(err)
	}
	if m.AnimationKnown {
		t.Fatal("truncated PNG header cannot prove static")
	}
}

func TestInspectGIFAnimationAndTruncation(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 4, 3), color.Palette{color.Black, color.White})
	for _, count := range []int{1, 2} {
		var b bytes.Buffer
		frames := []*image.Paletted{frame}
		delays := []int{1}
		if count == 2 {
			frames = append(frames, frame)
			delays = append(delays, 1)
		}
		if err := gif.EncodeAll(&b, &gif.GIF{Image: frames, Delay: delays}); err != nil {
			t.Fatal(err)
		}
		m, err := Inspect(b.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !m.AnimationKnown || m.Animated != (count == 2) {
			t.Fatalf("%d GIF frames: %+v", count, m)
		}
		m, err = Inspect(b.Bytes()[:13])
		if err != nil {
			t.Fatal(err)
		}
		if m.AnimationKnown {
			t.Fatal("truncated GIF cannot prove static")
		}
	}
}

func TestExifOrientationUsesBoundedOffsets(t *testing.T) {
	b := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0}
	if got := exifOrientation(b); got != 6 {
		t.Fatalf("orientation %d", got)
	}
	binary.LittleEndian.PutUint32(b[4:8], 0xffffffff)
	if got := exifOrientation(b); got != 1 {
		t.Fatalf("invalid offset orientation %d", got)
	}
}

func TestInspectWebPAnimationFlagAndUnknownExif(t *testing.T) {
	data := make([]byte, 30)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], 22)
	copy(data[8:], "WEBPVP8X")
	binary.LittleEndian.PutUint32(data[16:], 10)
	data[24] = 6
	data[27] = 4
	for _, animated := range []bool{false, true} {
		if animated {
			data[20] = 2
		} else {
			data[20] = 0
		}
		m, err := Inspect(data)
		if err != nil {
			t.Fatal(err)
		}
		if m.Width != 7 || m.Height != 5 || m.Animated != animated || !m.AnimationKnown {
			t.Fatalf("WebP header: %+v", m)
		}
	}
	data[20] = 8
	m, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Orientation != 0 {
		t.Fatal("EXIF flag without readable EXIF must leave orientation unknown")
	}
}
