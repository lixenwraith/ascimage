// Package ascimage converts raster images into terminal cells, a Unicode quadrant
// glyph fitted to each cell's four samples or one background color per sample,
// and reads and writes them as one file holding true color and xterm-256 cells
// at once.
package ascimage

import (
	"image"
	"image/color"
	"slices"

	lcolor "github.com/lixenwraith/color"
	"github.com/lixenwraith/terminal"
)

// DefaultAspect is a terminal cell's width over its height
const DefaultAspect = 0.5

// QuadrantChars maps 4-bit patterns to Unicode quadrant characters
// Bit order: 0=UL, 1=UR, 2=LL, 3=LR (1 = foreground)
var QuadrantChars = [16]rune{
	' ', // 0000 - empty
	'▘', // 0001 - upper-left
	'▝', // 0010 - upper-right
	'▀', // 0011 - upper half
	'▖', // 0100 - lower-left
	'▌', // 0101 - left half
	'▞', // 0110 - anti-diagonal
	'▛', // 0111 - UL + UR + LL
	'▗', // 1000 - lower-right
	'▚', // 1001 - diagonal
	'▐', // 1010 - right half
	'▜', // 1011 - UL + UR + LR
	'▄', // 1100 - lower half
	'▙', // 1101 - UL + LL + LR
	'▟', // 1110 - UR + LL + LR
	'█', // 1111 - full block
}

// RenderMode determines the rendering approach
type RenderMode uint8

const (
	ModeBackgroundOnly RenderMode = iota
	ModeQuadrant
)

func (m RenderMode) String() string {
	switch m {
	case ModeBackgroundOnly:
		return "Background"
	case ModeQuadrant:
		return "Quadrant"
	default:
		return "Unknown"
	}
}

// ConvertedImage holds an image's cells in one color mode; Rune 0 marks a
// transparent cell, which shows what lies under the image
type ConvertedImage struct {
	Cells  []terminal.Cell
	Width  int
	Height int
}

// ConvertImage converts an image to the cells CalculateOutputSize gives at a
// width and aspect, in one color mode
func ConvertImage(img image.Image, targetWidth int, mode RenderMode, colorMode terminal.ColorMode, aspect float64) *ConvertedImage {
	b := img.Bounds()
	w, h := CalculateOutputSize(b.Dx(), b.Dy(), targetWidth, aspect)
	return &ConvertedImage{
		Cells:  ConvertRect(nil, img, w, h, image.Rect(0, 0, w, h), mode, colorMode),
		Width:  w,
		Height: h,
	}
}

// ConvertRect converts the cells r covers of img's grid of gridW by gridH
// cells into dst, row by row, growing it as it must: the cells ConvertImage
// gives at that size, so a viewer converts only what it shows. A cell of r
// outside the grid is transparent.
func ConvertRect(dst []terminal.Cell, img image.Image, gridW, gridH int, r image.Rectangle, mode RenderMode, colorMode terminal.ColorMode) []terminal.Cell {
	n := max(r.Dx(), 0) * max(r.Dy(), 0)
	dst = slices.Grow(dst[:0], n)[:n]
	s := newSampler(img, gridW, gridH, mode)
	i := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := s.cell(x, y)
			if colorMode == terminal.ColorMode256 {
				c.index(mode)
			}
			dst[i] = c.cell(colorMode, mode)
			i++
		}
	}
	return dst
}

// CalculateOutputSize is the cell grid an image fills at a width, at least one row
func CalculateOutputSize(srcW, srcH, targetWidth int, aspect float64) (outW, outH int) {
	if srcW == 0 || srcH == 0 || targetWidth <= 0 {
		return 0, 0
	}
	imageRatio := float64(srcH) / float64(srcW)
	return targetWidth, max(int(float64(targetWidth)*imageRatio*aspect), 1)
}

// sampler reads the samples of a grid of cells over an image: one a cell in
// background mode, a cell's four quadrants in quadrant mode, each the source
// pixel under its centre
type sampler struct {
	img            image.Image
	b              image.Rectangle
	cellsW, cellsH int
	gridW, gridH   int // samples across and down
	mode           RenderMode

	// img as the type it is, where at reads that type's buffer
	rgba     *image.RGBA
	nrgba    *image.NRGBA
	ycbcr    *image.YCbCr
	gray     *image.Gray
	paletted *image.Paletted
	palette  [256]struct { // paletted's entries as samples
		rgb lcolor.RGB
		ok  bool
	}
}

func newSampler(img image.Image, cellsW, cellsH int, mode RenderMode) sampler {
	s := sampler{img: img, b: img.Bounds(), cellsW: cellsW, cellsH: cellsH, gridW: cellsW, gridH: cellsH, mode: mode}
	if mode == ModeQuadrant {
		s.gridW, s.gridH = 2*cellsW, 2*cellsH
	}
	if s.b.Empty() {
		s.cellsW, s.cellsH = 0, 0
	}
	switch p := img.(type) {
	case *image.RGBA:
		s.rgba = p
	case *image.NRGBA:
		s.nrgba = p
	case *image.YCbCr:
		s.ycbcr = p
	case *image.Gray:
		s.gray = p
	case *image.Paletted:
		s.paletted = p
		for i, c := range p.Palette[:min(len(p.Palette), len(s.palette))] {
			if c != nil { // At panics on a nil entry only where a pixel names it
				s.palette[i].rgb, s.palette[i].ok = sampleRGB(c)
			}
		}
	}
	return s
}

// at is sample (gx, gy). The decoders' 8-bit types are read from their buffers,
// where At boxes a color at an allocation a sample, and any other through At;
// each takes its color's own RGBA, which an 8-bit shortcut misses by one.
func (s *sampler) at(gx, gy int) (lcolor.RGB, bool) {
	srcW, srcH := int64(s.b.Dx()), int64(s.b.Dy())
	x := min(s.b.Min.X+int((int64(gx)*srcW+srcW/2)/int64(s.gridW)), s.b.Max.X-1)
	y := min(s.b.Min.Y+int((int64(gy)*srcH+srcH/2)/int64(s.gridH)), s.b.Max.Y-1)
	switch {
	case s.rgba != nil:
		return unmultiply(s.rgba.RGBAAt(x, y).RGBA())
	case s.nrgba != nil:
		return unmultiply(s.nrgba.NRGBAAt(x, y).RGBA())
	case s.ycbcr != nil:
		return unmultiply(s.ycbcr.YCbCrAt(x, y).RGBA())
	case s.gray != nil:
		return unmultiply(s.gray.GrayAt(x, y).RGBA())
	case s.paletted != nil:
		e := s.palette[s.paletted.ColorIndexAt(x, y)]
		return e.rgb, e.ok
	}
	return sampleRGB(s.img.At(x, y))
}

// cell is cell (x, y) in true color; one outside the grid, or one whose every
// sample is transparent, is transparent
func (s *sampler) cell(x, y int) DualCell {
	if x < 0 || y < 0 || x >= s.cellsW || y >= s.cellsH {
		return DualCell{Transparent: true}
	}
	if s.mode != ModeQuadrant {
		rgb, ok := s.at(x, y)
		if !ok {
			return DualCell{Transparent: true}
		}
		return DualCell{Rune: ' ', TrueBg: rgb}
	}
	var px [4]lcolor.RGB
	opaque := false
	for i := range px {
		var ok bool
		px[i], ok = s.at(2*x+i%2, 2*y+i/2)
		opaque = opaque || ok
	}
	if !opaque {
		return DualCell{Transparent: true}
	}
	r, fg, bg := FitQuadrant(px)
	return DualCell{Rune: r, TrueFg: fg, TrueBg: bg}
}

// FitQuadrant returns the quadrant glyph and its two colors that show a cell's four
// quadrants, UL UR LL LR, with the least squared error, each color the mean of its
// side. A split and its complement fit alike and the first of equals wins, so only
// the eight leaving the lower right to the background are tried.
func FitQuadrant(pixels [4]lcolor.RGB) (rune, lcolor.RGB, lcolor.RGB) {
	if pixels[0] == pixels[1] && pixels[1] == pixels[2] && pixels[2] == pixels[3] {
		return QuadrantChars[0], lcolor.RGB{}, pixels[0]
	}
	ch := [3][4]uint32{
		{uint32(pixels[0].R), uint32(pixels[1].R), uint32(pixels[2].R), uint32(pixels[3].R)},
		{uint32(pixels[0].G), uint32(pixels[1].G), uint32(pixels[2].G), uint32(pixels[3].G)},
		{uint32(pixels[0].B), uint32(pixels[1].B), uint32(pixels[2].B), uint32(pixels[3].B)},
	}
	// A side of n samples summing s, shown at its truncated mean m, is off by its
	// samples' squares less m(2s - nm): the split whose sides gain most is the fit
	var g0, g1, g2, g3, g4, g5, g6, g7 uint32
	for c := range ch {
		p0, p1, p2, p3 := ch[c][0], ch[c][1], ch[c][2], ch[c][3]
		t := p0 + p1 + p2 + p3
		g0 += sideGain(t, 4)
		g1 += p0*p0 + sideGain(t-p0, 3)
		g2 += p1*p1 + sideGain(t-p1, 3)
		g3 += sideGain(p0+p1, 2) + sideGain(p2+p3, 2)
		g4 += p2*p2 + sideGain(t-p2, 3)
		g5 += sideGain(p0+p2, 2) + sideGain(p1+p3, 2)
		g6 += sideGain(p1+p2, 2) + sideGain(p0+p3, 2)
		g7 += p3*p3 + sideGain(t-p3, 3)
	}
	// A gain keyed above its split's rank, so the greatest key is the first best
	key := max(g0<<3|7, g1<<3|6, g2<<3|5, g3<<3|4, g4<<3|3, g5<<3|2, g6<<3|1, g7<<3)
	best := 7 - key&7
	m0, m1, m2 := -(best & 1), -(best >> 1 & 1), -(best >> 2)
	n := best&1 + best>>1&1 + best>>2
	var fore, back [3]uint8
	for c := range ch {
		f := ch[c][0]&m0 + ch[c][1]&m1 + ch[c][2]&m2
		t := ch[c][0] + ch[c][1] + ch[c][2] + ch[c][3]
		fore[c], back[c] = uint8(f*meanScale[n]>>16), uint8((t-f)*meanScale[4-n]>>16)
	}
	return QuadrantChars[best], lcolor.RGB{R: fore[0], G: fore[1], B: fore[2]}, lcolor.RGB{R: back[0], G: back[1], B: back[2]}
}

// sideGain is what a side of n samples summing s takes off the error at its
// truncated mean; n is a constant at every call, so the division is a shift or a
// multiply
func sideGain(s, n uint32) uint32 {
	m := s / n
	return m * (2*s - n*m)
}

// meanScale[n] times a sum of n samples, shifted down 16, is their truncated mean,
// exact for the sums four 8-bit samples reach; a side of none reads zero
var meanScale = [5]uint32{0, 1 << 16, 1 << 15, 1<<16/3 + 1, 1 << 14}

// sampleRGB is a pixel's color, its alpha divided out, and whether it shows at all
func sampleRGB(c color.Color) (lcolor.RGB, bool) { return unmultiply(c.RGBA()) }

func unmultiply(r, g, b, a uint32) (lcolor.RGB, bool) {
	if a == 0 {
		return lcolor.RGB{}, false
	}
	return lcolor.RGB{R: uint8((r * 0xff) / a), G: uint8((g * 0xff) / a), B: uint8((b * 0xff) / a)}, true
}
