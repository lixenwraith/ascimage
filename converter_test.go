package ascimage

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/lixenwraith/terminal"
)

func gradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			a := uint8(255)
			if x < w/5 {
				a = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: uint8((x ^ y) * 7), A: a})
		}
	}
	return img
}

// TestConvertRectGivesTheWholeConversionsCells: a viewer converting only its
// window sees the cells the whole image converts to, and none past the grid
func TestConvertRectGivesTheWholeConversionsCells(t *testing.T) {
	img := gradient(97, 61)
	for _, mode := range []RenderMode{ModeBackgroundOnly, ModeQuadrant} {
		for _, cm := range []terminal.ColorMode{terminal.ColorModeTrueColor, terminal.ColorMode256, terminal.ColorMode16} {
			whole := ConvertImage(img, 40, mode, cm, DefaultAspect)
			r := image.Rect(31, whole.Height-5, 47, whole.Height+2) // over the grid's right and bottom edges
			got := ConvertRect(nil, img, whole.Width, whole.Height, r, mode, cm)
			if len(got) != r.Dx()*r.Dy() {
				t.Fatalf("mode %v: %d cells for a %v window", mode, len(got), r)
			}
			for i, c := range got {
				x, y := r.Min.X+i%r.Dx(), r.Min.Y+i/r.Dx()
				want := terminal.Cell{}
				if x < whole.Width && y < whole.Height {
					want = whole.Cells[y*whole.Width+x]
				}
				if c != want {
					t.Fatalf("mode %v color %v: cell %d,%d = %+v, want %+v", mode, cm, x, y, c, want)
				}
			}
		}
	}
}

// hidden keeps an image's type from the sampler, which then reads it through At
type hidden struct{ image.Image }

// pictures is one picture in each type the sampler reads from its buffer, a
// YCbCr in each ratio given. px is a pixel's four bytes: a color and its alpha,
// a YCbCr's planes, a gray, a palette index, and the palette's entries.
func pictures(b image.Rectangle, px func(x, y int) [4]uint8, ratios ...image.YCbCrSubsampleRatio) []image.Image {
	rgba, nrgba, gray := image.NewRGBA(b), image.NewNRGBA(b), image.NewGray(b)
	pal := image.NewPaletted(b, make(color.Palette, 256))
	for i := range pal.Palette {
		c := px(i, 3*i)
		pal.Palette[i] = color.NRGBA{R: c[0], G: c[1], B: c[2], A: c[3]}
	}
	imgs := []image.Image{rgba, nrgba, gray, pal}
	for _, ratio := range ratios {
		imgs = append(imgs, image.NewYCbCr(b, ratio))
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := px(x, y)
			copy(rgba.Pix[rgba.PixOffset(x, y):], c[:])
			copy(nrgba.Pix[nrgba.PixOffset(x, y):], c[:])
			gray.Pix[gray.PixOffset(x, y)] = c[1]
			pal.Pix[pal.PixOffset(x, y)] = c[2]
			for _, img := range imgs[4:] {
				p := img.(*image.YCbCr)
				p.Y[p.YOffset(x, y)], p.Cb[p.COffset(x, y)], p.Cr[p.COffset(x, y)] = c[0], c[1], c[2]
			}
		}
	}
	return imgs
}

// TestKnownImageTypeConvertsToTheCellsAtGives: a type read from its buffer
// converts as it does behind a wrapper that leaves the sampler only At, whole
// and as a sub-image. Background mode at 1:1 shows each pixel's sample as a
// cell, and every color byte meets every alpha.
func TestKnownImageTypeConvertsToTheCellsAtGives(t *testing.T) {
	imgs := pictures(image.Rect(-7, 3, 249, 259), func(x, y int) [4]uint8 {
		return [4]uint8{uint8(x), uint8(3*x + y), uint8(x ^ y), uint8(y)}
	}, image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420,
		image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410)
	for _, whole := range imgs {
		sub := whole.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(image.Rect(-2, 6, 246, 258))
		for _, img := range []image.Image{whole, sub} {
			w, h := img.Bounds().Dx(), img.Bounds().Dy()
			r := image.Rect(0, 0, w, h)
			got := ConvertRect(nil, img, w, h, r, ModeBackgroundOnly, terminal.ColorModeTrueColor)
			want := ConvertRect(nil, hidden{img}, w, h, r, ModeBackgroundOnly, terminal.ColorModeTrueColor)
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%T %v: pixel %d,%d is %+v, At gives %+v", img, img.Bounds(), i%w, i/w, got[i], want[i])
				}
			}
		}
	}
}

// BenchmarkConvertRect160x48: a 160x48 window in quadrant mode at 1:1 of a
// 1600x1000 picture, by its type; hidden is any other type, read through At
func BenchmarkConvertRect160x48(b *testing.B) {
	const w, h = 1600, 1000
	imgs := pictures(image.Rect(0, 0, w, h), func(x, y int) [4]uint8 {
		return [4]uint8{uint8(x), uint8(y), uint8(x ^ y), 0xff}
	}, image.YCbCrSubsampleRatio420)
	gw, gh := CalculateOutputSize(w, h, w/2, DefaultAspect)
	win := image.Rect(0, 0, 160, 48).Add(image.Pt((gw-160)/2, (gh-48)/2))
	for _, img := range append(imgs, hidden{imgs[0]}) {
		b.Run(strings.TrimPrefix(fmt.Sprintf("%T", img), "*image."), func(b *testing.B) {
			b.ReportAllocs()
			var dst []terminal.Cell
			for b.Loop() {
				dst = ConvertRect(dst, img, gw, gh, win, ModeQuadrant, terminal.ColorModeTrueColor)
			}
		})
	}
}
