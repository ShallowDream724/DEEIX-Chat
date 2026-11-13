package imageutil

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
	"time"
)

func pngFixture(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	im.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 64})
	if err := png.Encode(&out, im); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPrepareFastPathAndZeroDimensionAvoidPixelDecode(t *testing.T) {
	data := pngFixture(t)
	// Occupying all decode capacity proves the conforming path never queues a decode.
	release, err := defaultDecodeGovernor.Acquire(t.Context(), 999, defaultDecodeGovernor.maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, dimension := range []int{0, 1536} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		got, mime, err := Prepare(ctx, data, "image/jpeg", ProcessingPolicy{MaxDimension: dimension}, 1)
		cancel()
		if err != nil || mime != "image/png" || !bytes.Equal(got, data) || &got[0] != &data[0] {
			t.Fatalf("fast path must return original PNG bytes: %s %v", mime, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, _, err := Prepare(ctx, data, "image/png", ProcessingPolicy{MaxPixels: 1, ForcePNG: true}, 1); !errors.Is(err, ErrImagePixelLimit) {
		t.Fatalf("pixel budget must precede decode admission: %v", err)
	}
}

func TestPreparePreservesAlphaAndRejectsAnimation(t *testing.T) {
	data := pngFixture(t)
	got, mime, err := Prepare(t.Context(), data, "image/png", ProcessingPolicy{MaxDimension: 2}, 1)
	if err != nil || mime != "image/png" {
		t.Fatalf("alpha resize: %s %v", mime, err)
	}
	im, _, err := image.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, a := im.At(0, 0).RGBA()
	if a >= 65535 {
		t.Fatal("resize flattened alpha")
	}
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.NRGBA{}, color.NRGBA{R: 255, A: 255}})
	var one bytes.Buffer
	if err := gif.Encode(&one, frame, nil); err != nil {
		t.Fatal(err)
	}
	got, mime, err = Prepare(t.Context(), one.Bytes(), "image/gif", ProcessingPolicy{}, 1)
	if err != nil || mime != "image/png" {
		t.Fatalf("static GIF should normalize as alpha-safe PNG: %s %v", mime, err)
	}
	var many bytes.Buffer
	if err := gif.EncodeAll(&many, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{1, 1}}); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{many.Bytes(), one.Bytes()[:one.Len()-1]} {
		if _, _, err := Prepare(t.Context(), input, "image/gif", ProcessingPolicy{}, 1); !errors.Is(err, ErrAnimatedImage) {
			t.Fatalf("animation or unverified GIF must not become a first frame: %v", err)
		}
	}
	if _, _, err := Prepare(t.Context(), []byte("invalid"), "image/png", ProcessingPolicy{}, 1); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("invalid data accepted: %v", err)
	}
}

func TestDecodeGovernorUserIsolationCancellationAndBudget(t *testing.T) {
	g := NewDecodeGovernor(2, 1, 100)
	first, err := g.Acquire(t.Context(), 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	ctx, cancel := context.WithCancel(t.Context())
	blocked := make(chan error, 1)
	go func() {
		release, err := g.Acquire(ctx, 1, 40)
		if err == nil {
			release()
		}
		blocked <- err
	}()
	second, err := g.Acquire(t.Context(), 2, 40)
	if err != nil {
		t.Fatalf("one user's waiting work blocked another user: %v", err)
	}
	budgetCtx, budgetCancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer budgetCancel()
	if _, err := g.Acquire(budgetCtx, 3, 30); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("global capacity ignored: %v", err)
	}
	second()
	cancel()
	if err := <-blocked; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := g.Acquire(t.Context(), 3, 101); err == nil {
		t.Fatal("oversized decode admitted")
	}
	first()
	first() // Release is idempotent.
	last, err := g.Acquire(t.Context(), 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	last()
}

func TestOrientationViewCoordinates(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(y*2 + x + 1), A: 255})
		}
	}
	for orientation, want := range map[int][]uint8{1: {1, 2, 3, 4, 5, 6}, 2: {2, 1, 4, 3, 6, 5}, 3: {6, 5, 4, 3, 2, 1}, 4: {5, 6, 3, 4, 1, 2}, 5: {1, 3, 5, 2, 4, 6}, 6: {5, 3, 1, 6, 4, 2}, 7: {6, 4, 2, 5, 3, 1}, 8: {2, 4, 6, 1, 3, 5}} {
		w, h := 2, 3
		if orientation >= 5 {
			w, h = h, w
		}
		v := orientationView{Image: src, orientation: orientation, width: w, height: h}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if got := color.NRGBAModel.Convert(v.At(x, y)).(color.NRGBA).R; got != want[y*w+x] {
					t.Fatalf("orientation %d at %d,%d: %d != %d", orientation, x, y, got, want[y*w+x])
				}
			}
		}
	}
}
