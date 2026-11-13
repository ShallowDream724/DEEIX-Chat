package imageutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/draw"
)

// ProcessingVersion is part of all prepared-image cache identities.
const ProcessingVersion = "static-v2-catmullrom"
const DefaultMaxPixels int64 = 64 * 1024 * 1024

var ErrInvalidImage = errors.New("invalid image")
var ErrAnimatedImage = errors.New("animated or unverified animated images are unsupported")
var ErrImagePixelLimit = errors.New("image pixel limit exceeded")

type ProcessingPolicy struct {
	MaxDimension int // Zero disables resizing, but still validates safety and animation.
	MaxPixels    int64
	ForcePNG     bool // Editing inputs and masks preserve their alpha channel.
}

// Prepare returns unchanged bytes for already conforming static images. Only
// legacy images requiring normalization acquire scarce pixel-decoding capacity.
func Prepare(ctx context.Context, data []byte, declaredMIME string, policy ProcessingPolicy, userID uint) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	metadata, err := Inspect(data)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid image headers: %w", ErrInvalidImage, err)
	}
	if !metadata.AnimationKnown || metadata.Animated {
		return nil, "", ErrAnimatedImage
	}
	if metadata.Orientation < 1 || metadata.Orientation > 8 {
		return nil, "", fmt.Errorf("%w: image orientation could not be verified", ErrInvalidImage)
	}
	limit := policy.MaxPixels
	if limit <= 0 {
		limit = DefaultMaxPixels
	}
	pixels := int64(metadata.Width) * int64(metadata.Height)
	if metadata.Width <= 0 || metadata.Height <= 0 || int64(metadata.Width) > limit/int64(metadata.Height) {
		return nil, "", fmt.Errorf("%w: image dimensions exceed %d pixels", ErrImagePixelLimit, limit)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("%w: decode image configuration: %w", ErrInvalidImage, err)
	}
	mime := "image/" + format
	w, h := metadata.Width, metadata.Height
	if metadata.Orientation >= 5 && metadata.Orientation <= 8 {
		w, h = h, w
	}
	resize := policy.MaxDimension > 0 && (w > policy.MaxDimension || h > policy.MaxDimension)
	if !policy.ForcePNG && !resize && metadata.Orientation == 1 && metadata.BitDepth <= 8 && format != "gif" {
		return data, mime, nil
	}
	// Budget for up to 8-byte source pixels, 4-byte target pixels and encoded
	// output/scratch. Source dimensions deliberately bound the whole operation.
	release, err := defaultDecodeGovernor.Acquire(ctx, userID, pixels*16)
	if err != nil {
		return nil, "", err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("%w: decode image: %w", ErrInvalidImage, err)
	}
	if src.Bounds().Dx() != metadata.Width || src.Bounds().Dy() != metadata.Height {
		return nil, "", fmt.Errorf("%w: image dimensions changed during decode", ErrInvalidImage)
	}
	var oriented image.Image = orientationView{Image: src, orientation: metadata.Orientation, width: w, height: h}
	newW, newH := w, h
	if resize {
		scale := float64(policy.MaxDimension) / float64(max(w, h))
		newW, newH = max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
	}
	dst := image.NewNRGBA(image.Rect(0, 0, newW, newH))
	if resize {
		draw.CatmullRom.Scale(dst, dst.Bounds(), oriented, oriented.Bounds(), draw.Src, nil)
	} else {
		draw.Draw(dst, dst.Bounds(), oriented, image.Point{}, draw.Src)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	var out bytes.Buffer
	if format == "jpeg" && !policy.ForcePNG {
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85})
		mime = "image/jpeg"
	} else {
		err = png.Encode(&out, dst)
		mime = "image/png"
	}
	if err != nil {
		return nil, "", fmt.Errorf("%w: encode image: %w", ErrInvalidImage, err)
	}
	return out.Bytes(), mime, nil
}

// orientationView remaps coordinates without allocating a second full source.
type orientationView struct {
	image.Image
	orientation, width, height int
}

func (v orientationView) Bounds() image.Rectangle { return image.Rect(0, 0, v.width, v.height) }
func (v orientationView) At(x, y int) color.Color {
	b := v.Image.Bounds()
	w, h := b.Dx(), b.Dy()
	switch v.orientation {
	case 2:
		x = w - 1 - x
	case 3:
		x, y = w-1-x, h-1-y
	case 4:
		y = h - 1 - y
	case 5:
		x, y = y, x
	case 6:
		x, y = y, h-1-x
	case 7:
		x, y = w-1-y, h-1-x
	case 8:
		x, y = w-1-y, x
	}
	return v.Image.At(x+b.Min.X, y+b.Min.Y)
}
