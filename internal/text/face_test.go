package text

import (
	"errors"
	"fmt"
	"image"
	"math"
	"testing"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/math/fixed"
)

func newTestFace(t *testing.T) *Face {
	t.Helper()
	face, err := NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("NewFace(goregular, 16) error = %v", err)
	}
	t.Cleanup(func() { face.Close() })
	return face
}

// referenceFace builds an independent face from the same data through the
// public API, so tests can pin Rasterize against a second engine instance
// instead of the implementation's own caches. The exact ink extents depend
// on the active shaping engine (HarfBuzz/FreeType vs the legacy x/image
// rasterizer), so cross-engine pixel pinning is intentionally avoided; the
// engine-independent invariants (positions inside the bitmap, ink present,
// advances summing to the pen) are asserted alongside.
func referenceFace(t *testing.T, data []byte, size float64) *Face {
	t.Helper()
	face, err := NewFace(data, size)
	if err != nil {
		t.Fatalf("NewFace(reference, %v): %v", size, err)
	}
	t.Cleanup(func() { face.Close() })
	return face
}

// pinBitmapAgainstReference pins a bitmap's geometry and per-glyph records
// against an independently constructed face, then checks the invariants
// every engine must satisfy: glyph rects inside the bitmap, ink actually
// painted, and advances summing to the final pen.
func pinBitmapAgainstReference(t *testing.T, bmp Bitmap, ref *Face, value string) {
	t.Helper()
	refBmp, err := ref.Rasterize(value)
	if err != nil {
		t.Fatalf("reference Rasterize: %v", err)
	}
	if bmp.Width != refBmp.Width || bmp.Height != refBmp.Height || bmp.OriginX != refBmp.OriginX {
		t.Errorf("geometry = %dx%d origin %d, want reference %dx%d origin %d",
			bmp.Width, bmp.Height, bmp.OriginX, refBmp.Width, refBmp.Height, refBmp.OriginX)
	}
	if len(bmp.Glyphs) != len(refBmp.Glyphs) {
		t.Fatalf("len(Glyphs) = %d, want reference %d", len(bmp.Glyphs), len(refBmp.Glyphs))
	}
	pen := fixed.Int26_6(0)
	for i, g := range bmp.Glyphs {
		rg := refBmp.Glyphs[i]
		if g != rg {
			t.Errorf("Glyphs[%d] = %+v, want reference %+v", i, g, rg)
		}
		pen += g.Advance
		if g.Width > 0 && g.Height > 0 {
			if g.X < 0 || g.Y < 0 || g.X+g.Width > bmp.Width || g.Y+g.Height > bmp.Height {
				t.Errorf("Glyphs[%d] rect (%d,%d %dx%d) outside bitmap %dx%d", i, g.X, g.Y, g.Width, g.Height, bmp.Width, bmp.Height)
			}
			if n := inkIn(bmp, g.X, g.Y, g.X+g.Width, g.Y+g.Height); n == 0 {
				t.Errorf("Glyphs[%d] rect (%d,%d %dx%d) has no painted ink", i, g.X, g.Y, g.Width, g.Height)
			}
		}
	}
	// The advances must account for the whole line advance the caret API
	// reports (kerning included).
	offsets, err := ref.CaretOffsets(value)
	if err != nil {
		t.Fatalf("reference CaretOffsets: %v", err)
	}
	if len(offsets) > 0 && pen != offsets[len(offsets)-1]-offsets[0] {
		t.Errorf("sum of advances = %v, want caret span %v", pen, offsets[len(offsets)-1]-offsets[0])
	}
}

// inkIn counts the non-zero alpha bytes inside a rect of the bitmap.
func inkIn(bmp Bitmap, x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		row := y * bmp.Stride
		for x := x0; x < x1; x++ {
			if bmp.Alpha[row+x] != 0 {
				n++
			}
		}
	}
	return n
}

func TestRasterizeText(t *testing.T) {
	face := newTestFace(t)

	bmp, err := face.Rasterize("Wind")
	if err != nil {
		t.Fatalf("Rasterize(\"Wind\") error = %v", err)
	}
	if bmp.Width <= 0 || bmp.Height <= 0 {
		t.Fatalf("size = %dx%d, want non-zero", bmp.Width, bmp.Height)
	}
	if bmp.Stride < bmp.Width {
		t.Errorf("Stride = %d, want >= Width %d", bmp.Stride, bmp.Width)
	}
	if len(bmp.Alpha) != bmp.Stride*bmp.Height {
		t.Fatalf("len(Alpha) = %d, want Stride*Height = %d", len(bmp.Alpha), bmp.Stride*bmp.Height)
	}
	if len(bmp.Glyphs) != 4 {
		t.Fatalf("len(Glyphs) = %d, want 4", len(bmp.Glyphs))
	}

	// Pin against an independently constructed face from the same data:
	// identical geometry and per-glyph records, plus the engine-independent
	// invariants inside pinBitmapAgainstReference.
	pinBitmapAgainstReference(t, bmp, referenceFace(t, goregular.TTF, 16), "Wind")

	// Engine-independent truths for this text: positive advances.
	for i, g := range bmp.Glyphs {
		if wantRune := rune("Wind"[i]); g.Rune != wantRune {
			t.Errorf("Glyphs[%d].Rune = %q, want %q", i, g.Rune, wantRune)
		}
		if g.Advance <= 0 {
			t.Errorf("Glyphs[%d].Advance = %v, want positive", i, g.Advance)
		}
	}
}

func TestRasterizeEmpty(t *testing.T) {
	face := newTestFace(t)

	bmp, err := face.Rasterize("")
	if err != nil {
		t.Fatalf("Rasterize(\"\") error = %v, want nil", err)
	}
	if bmp.Width != 0 || bmp.Height != 0 {
		t.Errorf("size = %dx%d, want 0x0", bmp.Width, bmp.Height)
	}
	if len(bmp.Alpha) != 0 {
		t.Errorf("len(Alpha) = %d, want 0", len(bmp.Alpha))
	}
	if len(bmp.Glyphs) != 0 {
		t.Errorf("len(Glyphs) = %d, want 0", len(bmp.Glyphs))
	}
}

func TestRasterizeInvalidUTF8(t *testing.T) {
	face := newTestFace(t)

	// A lone 0xff byte is invalid UTF-8: Rasterize must not panic and must
	// treat it as utf8.RuneError like any other rune.
	bmp, err := face.Rasterize(string([]byte{'W', 0xff, 'd'}))
	if err != nil {
		t.Fatalf("Rasterize with invalid UTF-8 error = %v, want nil", err)
	}
	if len(bmp.Glyphs) != 3 {
		t.Fatalf("len(Glyphs) = %d, want 3", len(bmp.Glyphs))
	}
	if bmp.Glyphs[1].Rune != utf8.RuneError {
		t.Errorf("Glyphs[1].Rune = %q, want RuneError", bmp.Glyphs[1].Rune)
	}
	if bmp.Glyphs[1].Width <= 0 || bmp.Glyphs[1].Height <= 0 {
		t.Errorf("Glyphs[1] rect = %dx%d, want non-empty replacement glyph", bmp.Glyphs[1].Width, bmp.Glyphs[1].Height)
	}
	g := bmp.Glyphs[1]
	if n := inkIn(bmp, g.X, g.Y, g.X+g.Width, g.Y+g.Height); n == 0 {
		t.Error("replacement glyph has no painted ink")
	}
}

func TestNewFaceRejectsInvalidFont(t *testing.T) {
	if _, err := NewFace([]byte("not a font"), 16); err == nil {
		t.Error("NewFace with invalid font data error = nil, want non-nil")
	}
}

// fakeFace is a deterministic font.Face: fixed advances and ink bounds per
// rune plus an explicit kern table, so tests can pin kerning, fractional
// pen positions and ink that overhangs the advance or the metrics box.
// Glyph mirrors opentype's quantization: the destination rect is the
// glyph's 26.6 bounds translated by the dot and floored/ceiled.
type fakeFace struct {
	metrics font.Metrics
	advance map[rune]fixed.Int26_6
	bounds  map[rune]fixed.Rectangle26_6 // ink bounds relative to the pen origin
	kern    map[[2]rune]fixed.Int26_6
}

func (f *fakeFace) advanceFor(r rune) fixed.Int26_6 {
	if a, ok := f.advance[r]; ok {
		return a
	}
	return fixed.I(10)
}

func (f *fakeFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	adv := f.advanceFor(r)
	b := f.bounds[r]
	if b.Empty() {
		return image.Rectangle{}, nil, image.Point{}, adv, true
	}
	db := b.Add(dot)
	dr := image.Rect(
		db.Min.X.Floor(), db.Min.Y.Floor(),
		db.Max.X.Ceil(), db.Max.Y.Ceil(),
	)
	mask := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
	for i := range mask.Pix {
		mask.Pix[i] = 0xff
	}
	return dr, mask, image.Point{}, adv, true
}

func (f *fakeFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return f.bounds[r], f.advanceFor(r), true
}

func (f *fakeFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) { return f.advanceFor(r), true }

func (f *fakeFace) Kern(r0, r1 rune) fixed.Int26_6 { return f.kern[[2]rune{r0, r1}] }

func (f *fakeFace) Metrics() font.Metrics { return f.metrics }

func (f *fakeFace) Close() error { return nil }

// box8 is an 8x8 ink box sitting on the baseline (y from -8 to 0).
func box8() fixed.Rectangle26_6 {
	return fixed.Rectangle26_6{
		Min: fixed.Point26_6{X: 0, Y: -fixed.I(8)},
		Max: fixed.Point26_6{X: fixed.I(8), Y: 0},
	}
}

func TestRasterizeAppliesKerning(t *testing.T) {
	face := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{'A': fixed.I(10), 'V': fixed.I(15)},
		bounds:  map[rune]fixed.Rectangle26_6{'A': box8(), 'V': box8()},
		kern:    map[[2]rune]fixed.Int26_6{{'A', 'V'}: -fixed.I(2)},
	})
	defer face.Close()

	bmp, err := face.Rasterize("AV")
	if err != nil {
		t.Fatalf("Rasterize error = %v", err)
	}
	if len(bmp.Glyphs) != 2 {
		t.Fatalf("len(Glyphs) = %d, want 2", len(bmp.Glyphs))
	}

	// Pen for V = advance(A) + kern(A,V) = 8; its ink starts there. Without
	// kerning it would start at 10, which this assertion pins down.
	if got, want := bmp.Glyphs[1].X, 8; got != want {
		t.Errorf("Glyphs[1].X = %d, want %d (kerning applied)", got, want)
	}
	// Right edge of ink = 8 + 8 = 16 < advance 10-2+15 = 23, so the bitmap
	// is exactly the kern-aware advance wide.
	if got, want := bmp.Width, 23; got != want {
		t.Errorf("Width = %d, want %d (kern-aware pen advance)", got, want)
	}
	g := bmp.Glyphs[1]
	if n := inkIn(bmp, g.X, g.Y, g.X+g.Width, g.Y+g.Height); n != g.Width*g.Height {
		t.Errorf("kerned V ink = %d pixels, want full rect %d", n, g.Width*g.Height)
	}
}

func TestRasterizeFractionalPen(t *testing.T) {
	face := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{'a': fixed.Int26_6(10<<6 + 32)}, // 10.5px
		bounds:  map[rune]fixed.Rectangle26_6{'a': box8()},
	})
	defer face.Close()

	bmp, err := face.Rasterize("aaa")
	if err != nil {
		t.Fatalf("Rasterize error = %v", err)
	}
	// Pens are 0, 10.5 and 21 in fixed point; measuring each glyph with a
	// ceiled advance would place the glyphs at 0, 11 and 22 instead.
	if got, want := bmp.Glyphs[1].X, 10; got != want {
		t.Errorf("Glyphs[1].X = %d, want %d (fractional pen)", got, want)
	}
	if got, want := bmp.Glyphs[2].X, 21; got != want {
		t.Errorf("Glyphs[2].X = %d, want %d (fractional pen accumulated)", got, want)
	}
	if got, want := bmp.Glyphs[0].Advance, fixed.Int26_6(10<<6+32); got != want {
		t.Errorf("Glyphs[0].Advance = %v, want 10:32", got)
	}
}

func TestRasterizeInkOverhangExtendsBitmap(t *testing.T) {
	face := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(3)},
		advance: map[rune]fixed.Int26_6{'j': fixed.I(4)},
		// Ink reaches 3px left of the origin, 12px above the baseline
		// (taller than the 10px ascent) and 1px below the descent.
		bounds: map[rune]fixed.Rectangle26_6{
			'j': {
				Min: fixed.Point26_6{X: -fixed.I(3), Y: -fixed.I(12)},
				Max: fixed.Point26_6{X: fixed.I(4), Y: fixed.I(4)},
			},
		},
	})
	defer face.Close()

	bmp, err := face.Rasterize("j")
	if err != nil {
		t.Fatalf("Rasterize error = %v", err)
	}
	// Box: x from -3 to max(advance 4, ink 4) = 7 wide; y from -12 to
	// max(descent 3, ink 4) = 16 tall. An ascent/descent-only box would be
	// 4x13 and clip the glyph.
	if got, want := bmp.Width, 7; got != want {
		t.Errorf("Width = %d, want %d", got, want)
	}
	if got, want := bmp.Height, 16; got != want {
		t.Errorf("Height = %d, want %d", got, want)
	}
	if got, want := bmp.OriginX, 3; got != want {
		t.Errorf("OriginX = %d, want %d (pen origin after left-overhang translation)", got, want)
	}
	g := bmp.Glyphs[0]
	if g.X != 0 || g.Y != 0 {
		t.Errorf("glyph origin = (%d,%d), want (0,0) (overhang shifted into bitmap)", g.X, g.Y)
	}
	if n := inkIn(bmp, 0, 0, bmp.Width, bmp.Height); n != g.Width*g.Height {
		t.Errorf("painted ink = %d, want %d (whole glyph rect)", n, g.Width*g.Height)
	}
}

func TestRasterizeRightOverhangAndSpace(t *testing.T) {
	face := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{'V': fixed.I(15), ' ': fixed.I(5)},
		bounds: map[rune]fixed.Rectangle26_6{
			// Ink extends 5px past the 15px advance.
			'V': {Min: fixed.Point26_6{X: 0, Y: -fixed.I(8)}, Max: fixed.Point26_6{X: fixed.I(20), Y: 0}},
		},
	})
	defer face.Close()

	bmp, err := face.Rasterize("V ")
	if err != nil {
		t.Fatalf("Rasterize error = %v", err)
	}
	// Ink right edge 20 and total advance 15+5 = 20 agree; the trailing
	// space still counts, so the bitmap is 20 wide.
	if got, want := bmp.Width, 20; got != want {
		t.Errorf("Width = %d, want %d (ink overhang and trailing space)", got, want)
	}
	if got, want := bmp.Height, 12; got != want {
		t.Errorf("Height = %d, want %d (ascent+descent box)", got, want)
	}
	if len(bmp.Glyphs) != 2 {
		t.Fatalf("len(Glyphs) = %d, want 2", len(bmp.Glyphs))
	}
	if got, want := bmp.Glyphs[1].Advance, fixed.I(5); got != want {
		t.Errorf("space Advance = %v, want 5:00", got)
	}
	g := bmp.Glyphs[0]
	if n := inkIn(bmp, g.X, g.Y, g.X+g.Width, g.Y+g.Height); n != g.Width*g.Height {
		t.Errorf("overhanging V ink = %d pixels, want full rect %d", n, g.Width*g.Height)
	}
}

// TestRasterizeLeadingSpaceOverhang is the regression case for sizing the
// bitmap from glyph rects queried at the actual pen position: the V's ink
// sits at pen 5 (after the leading space) and reaches x=25, so a bitmap
// sized from zero-pen glyph rects (width 20) would clip 5 pixels on the
// right of what DrawString actually paints.
func TestRasterizeLeadingSpaceOverhang(t *testing.T) {
	face := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{'V': fixed.I(15), ' ': fixed.I(5)},
		bounds: map[rune]fixed.Rectangle26_6{
			'V': {Min: fixed.Point26_6{X: 0, Y: -fixed.I(8)}, Max: fixed.Point26_6{X: fixed.I(20), Y: 0}},
		},
	})
	defer face.Close()

	bmp, err := face.Rasterize(" V")
	if err != nil {
		t.Fatalf("Rasterize error = %v", err)
	}
	if got, want := bmp.Width, 25; got != want {
		t.Errorf("Width = %d, want %d (pen-shifted ink union)", got, want)
	}
	if len(bmp.Glyphs) != 2 {
		t.Fatalf("len(Glyphs) = %d, want 2", len(bmp.Glyphs))
	}
	space, v := bmp.Glyphs[0], bmp.Glyphs[1]
	if space.Width != 0 || space.Height != 0 {
		t.Errorf("space rect = %dx%d, want empty", space.Width, space.Height)
	}
	if v.X != 5 {
		t.Errorf("V X = %d, want 5 (after leading space)", v.X)
	}
	if v.X+v.Width != bmp.Width {
		t.Errorf("V right edge = %d, want bitmap width %d (no clipping)", v.X+v.Width, bmp.Width)
	}
	if n := inkIn(bmp, v.X, v.Y, v.X+v.Width, v.Y+v.Height); n != v.Width*v.Height {
		t.Errorf("V ink = %d pixels, want full rect %d (no clipping)", n, v.Width*v.Height)
	}
	if n := inkIn(bmp, 0, 0, v.X, bmp.Height); n != 0 {
		t.Errorf("leading space region has %d ink pixels, want 0", n)
	}
}

func TestCaretOffsetsAdvanceKernFractionalAndOverhang(t *testing.T) {
	face := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{
			'A': 15<<6 + 32,
			'V': 10<<6 + 16,
			'j': 4<<6 + 32,
		},
		bounds: map[rune]fixed.Rectangle26_6{
			'A': {Min: fixed.Point26_6{X: -fixed.I(2), Y: -fixed.I(8)}, Max: fixed.Point26_6{X: fixed.I(21), Y: 0}},
			'V': {Min: fixed.Point26_6{X: -fixed.I(3), Y: -fixed.I(8)}, Max: fixed.Point26_6{X: fixed.I(9), Y: 0}},
			'j': box8(),
		},
		kern: map[[2]rune]fixed.Int26_6{
			{'A', 'V'}: -(2<<6 + 16),
			{'V', 'j'}: 1<<6 + 32,
		},
	})
	defer face.Close()

	offsets, err := face.CaretOffsets("AVj")
	if err != nil {
		t.Fatalf("CaretOffsets error = %v", err)
	}
	want := []fixed.Int26_6{0, 13<<6 + 16, 25 << 6, 29<<6 + 32}
	if len(offsets) != len(want) {
		t.Fatalf("len(CaretOffsets) = %d, want %d", len(offsets), len(want))
	}
	for index := range want {
		if offsets[index] != want[index] {
			t.Errorf("CaretOffsets[%d] = %v (%0.2f), want %v (%0.2f)", index, offsets[index], float64(offsets[index])/64, want[index], float64(want[index])/64)
		}
	}
}

func TestCaretOffsetsEmptyAndClosed(t *testing.T) {
	face := newFace(&fakeFace{})
	offsets, err := face.CaretOffsets("")
	if err != nil {
		t.Fatalf("CaretOffsets empty error = %v", err)
	}
	if len(offsets) != 1 || offsets[0] != 0 {
		t.Fatalf("CaretOffsets empty = %v, want [0]", offsets)
	}
	if err := face.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if _, err := face.CaretOffsets("closed"); err == nil {
		t.Fatal("CaretOffsets on closed face error = nil, want non-nil")
	}
	var nilFace *Face
	if _, err := nilFace.CaretOffsets("nil"); err == nil {
		t.Fatal("CaretOffsets on nil face error = nil, want non-nil")
	}
}

// TestRasterizeFractionalPenOverhang pins that pass 1 queries glyph rects
// at fractional pen positions too: 'b' sits at pen 10.5 and its ink reaches
// x=25, beyond both the total advance (21) and the zero-pen ink width (14).
func TestRasterizeFractionalPenOverhang(t *testing.T) {
	face := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{'a': fixed.Int26_6(10<<6 + 32), 'b': fixed.Int26_6(10<<6 + 32)},
		bounds: map[rune]fixed.Rectangle26_6{
			'a': box8(),
			'b': {Min: fixed.Point26_6{X: 0, Y: -fixed.I(8)}, Max: fixed.Point26_6{X: fixed.I(14), Y: 0}},
		},
	})
	defer face.Close()

	bmp, err := face.Rasterize("ab")
	if err != nil {
		t.Fatalf("Rasterize error = %v", err)
	}
	// b: pen 10.5, ink to 24.5 -> ceil 25; advance only reaches 21.
	if got, want := bmp.Width, 25; got != want {
		t.Errorf("Width = %d, want %d (fractional pen + overhang)", got, want)
	}
	b := bmp.Glyphs[1]
	if b.X != 10 {
		t.Errorf("b X = %d, want 10 (floor of fractional pen 10.5)", b.X)
	}
	if b.X+b.Width != bmp.Width {
		t.Errorf("b right edge = %d, want bitmap width %d (no clipping)", b.X+b.Width, bmp.Width)
	}
	if n := inkIn(bmp, b.X, b.Y, b.X+b.Width, b.Y+b.Height); n != b.Width*b.Height {
		t.Errorf("b ink = %d pixels, want full rect %d (no clipping)", n, b.Width*b.Height)
	}
}

// TestRasterizeScaledBitmapGeometry pins the physical/logical split of a
// scaled bitmap against independently constructed oracle faces: the alpha
// mask, glyph positions and strides live in physical pixels at the given
// scale, while LogicalWidth/LogicalHeight/LogicalOriginX carry the logical
// draw geometry a renderer must place the quad with. Scales 1, 1.5 and 2,
// invalid scales, empty text, anisotropic scales and a non-zero pen origin
// (left-overhanging glyph) are covered.
func TestRasterizeScaledBitmapGeometry(t *testing.T) {
	face := newTestFace(t)
	const value = "Wind"

	bmp1, err := face.Rasterize(value)
	if err != nil {
		t.Fatalf("Rasterize(%q) error = %v", value, err)
	}

	t.Run("scale1MatchesRasterize", func(t *testing.T) {
		bmp, err := face.RasterizeScaled(value, 1, 1)
		if err != nil {
			t.Fatalf("RasterizeScaled(1,1) error = %v", err)
		}
		if bmp.Width != bmp1.Width || bmp.Height != bmp1.Height || bmp.Stride != bmp1.Stride || bmp.OriginX != bmp1.OriginX {
			t.Errorf("geometry = %dx%d stride %d origin %d, want scale-1 Rasterize %dx%d stride %d origin %d",
				bmp.Width, bmp.Height, bmp.Stride, bmp.OriginX, bmp1.Width, bmp1.Height, bmp1.Stride, bmp1.OriginX)
		}
		if fmt.Sprint(bmp.Glyphs) != fmt.Sprint(bmp1.Glyphs) {
			t.Error("Glyphs differ between RasterizeScaled(1,1) and Rasterize")
		}
		if string(bmp.Alpha) != string(bmp1.Alpha) {
			t.Error("Alpha differs between RasterizeScaled(1,1) and Rasterize")
		}
		if bmp.LogicalWidth != float64(bmp1.Width) || bmp.LogicalHeight != float64(bmp1.Height) || bmp.LogicalOriginX != float64(bmp1.OriginX) {
			t.Errorf("logical geometry = %vx%v origin %v, want physical %dx%d origin %d at scale 1",
				bmp.LogicalWidth, bmp.LogicalHeight, bmp.LogicalOriginX, bmp1.Width, bmp1.Height, bmp1.OriginX)
		}
	})

	// checkScaled pins one uniform scale against an independently
	// constructed reference face at size*scale: the physical bitmap must be
	// pixel-identical to what that face rasterizes at the scaled size.
	checkScaled := func(t *testing.T, scale float64) {
		t.Helper()
		reference := referenceFace(t, goregular.TTF, 16*scale)
		want, err := reference.Rasterize(value)
		if err != nil {
			t.Fatalf("reference Rasterize: %v", err)
		}
		bmp, err := face.RasterizeScaled(value, scale, scale)
		if err != nil {
			t.Fatalf("RasterizeScaled(%v) error = %v", scale, err)
		}
		if bmp.Width != want.Width || bmp.Height != want.Height || bmp.OriginX != want.OriginX {
			t.Errorf("physical geometry = %dx%d origin %d, want reference %dx%d origin %d",
				bmp.Width, bmp.Height, bmp.OriginX, want.Width, want.Height, want.OriginX)
		}
		if bmp.Width == bmp1.Width || bmp.Height == bmp1.Height {
			t.Errorf("physical size %dx%d equals the scale-1 bitmap %dx%d: the scale was not applied",
				bmp.Width, bmp.Height, bmp1.Width, bmp1.Height)
		}
		if bmp.Stride < bmp.Width {
			t.Errorf("Stride = %d, want >= Width %d", bmp.Stride, bmp.Width)
		}
		if len(bmp.Alpha) != bmp.Stride*bmp.Height {
			t.Fatalf("len(Alpha) = %d, want Stride*Height = %d", len(bmp.Alpha), bmp.Stride*bmp.Height)
		}
		if len(bmp.Glyphs) != len(want.Glyphs) {
			t.Fatalf("len(Glyphs) = %d, want reference %d", len(bmp.Glyphs), len(want.Glyphs))
		}
		for i, g := range bmp.Glyphs {
			if g != want.Glyphs[i] {
				t.Errorf("Glyphs[%d] = %+v, want reference %+v", i, g, want.Glyphs[i])
			}
		}
		// Logical geometry must match the logical (scale-1) bitmap within
		// per-glyph rounding: the renderer draws this rect in logical units.
		if d := math.Abs(bmp.LogicalWidth - float64(bmp1.Width)); d > 1 {
			t.Errorf("LogicalWidth = %v, want %v (scale-1 width) ±1, off by %v", bmp.LogicalWidth, bmp1.Width, d)
		}
		if d := math.Abs(bmp.LogicalHeight - float64(bmp1.Height)); d > 1 {
			t.Errorf("LogicalHeight = %v, want %v (scale-1 height) ±1, off by %v", bmp.LogicalHeight, bmp1.Height, d)
		}
		if d := math.Abs(bmp.LogicalOriginX - float64(bmp1.OriginX)); d > 1 {
			t.Errorf("LogicalOriginX = %v, want %v (scale-1 origin) ±1, off by %v", bmp.LogicalOriginX, bmp1.OriginX, d)
		}
	}

	t.Run("scale2", func(t *testing.T) { checkScaled(t, 2) })
	t.Run("scale1.5", func(t *testing.T) { checkScaled(t, 1.5) })

	t.Run("anisotropic", func(t *testing.T) {
		// A rasterizer cannot stretch glyph masks, so it rasterizes at the
		// vertical scale and scales horizontal pen positions: the height
		// follows scaleY (=1, identical to the logical bitmap) while the
		// width follows scaleX.
		bmp, err := face.RasterizeScaled(value, 2, 1)
		if err != nil {
			t.Fatalf("RasterizeScaled(2,1) error = %v", err)
		}
		if bmp.Height != bmp1.Height {
			t.Errorf("Height = %d, want %d (vertical scale 1)", bmp.Height, bmp1.Height)
		}
		if bmp.Width <= bmp1.Width {
			t.Errorf("Width = %d, want > %d (horizontal scale 2)", bmp.Width, bmp1.Width)
		}
		if d := math.Abs(bmp.LogicalWidth - float64(bmp.Width)/2); d > 1 {
			t.Errorf("LogicalWidth = %v, want physical Width/scaleX %v ±1", bmp.LogicalWidth, float64(bmp.Width)/2)
		}
		if bmp.LogicalHeight != float64(bmp.Height) {
			t.Errorf("LogicalHeight = %v, want %v (scaleY 1)", bmp.LogicalHeight, float64(bmp.Height))
		}
	})

	t.Run("invalidScales", func(t *testing.T) {
		nan := math.NaN()
		inf := math.Inf(1)
		for _, s := range [][2]float64{{0, 1}, {1, 0}, {-2, 1}, {1, -2}, {nan, 1}, {1, nan}, {inf, 1}, {1, inf}} {
			if _, err := face.RasterizeScaled(value, s[0], s[1]); err == nil {
				t.Errorf("RasterizeScaled(%v, %v) error = nil, want non-nil", s[0], s[1])
			}
		}
		// Scale is validated before the empty-string fast path.
		if _, err := face.RasterizeScaled("", 0, 2); err == nil {
			t.Error("RasterizeScaled(\"\", 0, 2) error = nil, want non-nil")
		}
	})

	t.Run("empty", func(t *testing.T) {
		bmp, err := face.RasterizeScaled("", 2, 2)
		if err != nil {
			t.Fatalf("RasterizeScaled(\"\", 2, 2) error = %v, want nil", err)
		}
		if bmp.Width != 0 || bmp.Height != 0 || bmp.OriginX != 0 || bmp.LogicalWidth != 0 || bmp.LogicalHeight != 0 || bmp.LogicalOriginX != 0 {
			t.Errorf("empty bitmap geometry = %+v, want all zero", bmp)
		}
		if len(bmp.Alpha) != 0 || len(bmp.Glyphs) != 0 {
			t.Errorf("empty bitmap has %d alpha bytes and %d glyphs, want 0", len(bmp.Alpha), len(bmp.Glyphs))
		}
	})

	t.Run("leftOverhangOrigin", func(t *testing.T) {
		// goregular 'j' inks one physical pixel left of its pen origin at
		// 16px and two at 32px, so OriginX is non-zero and scales.
		bmp, err := face.RasterizeScaled("j", 2, 2)
		if err != nil {
			t.Fatalf("RasterizeScaled(j, 2, 2) error = %v", err)
		}
		if bmp.OriginX <= 0 {
			t.Fatalf("OriginX = %d, want > 0 (left-overhanging glyph)", bmp.OriginX)
		}
		if d := math.Abs(float64(bmp.OriginX) - 2); d > 2 {
			t.Errorf("OriginX = %d, want ~2 (double the scale-1 origin)", bmp.OriginX)
		}
		if bmp.LogicalOriginX <= 0 {
			t.Errorf("LogicalOriginX = %v, want > 0", bmp.LogicalOriginX)
		}
		if d := math.Abs(bmp.LogicalOriginX - float64(bmp.OriginX)/2); d > 1 {
			t.Errorf("LogicalOriginX = %v, want physical OriginX/scale %v ±1", bmp.LogicalOriginX, float64(bmp.OriginX)/2)
		}
	})
}

// trackedFace wraps fakeFace and records Close calls, so the cache lifecycle
// test can pin creation reuse and release order without a real font.
type trackedFace struct {
	*fakeFace
	name   string
	closes *[]string
}

func (t *trackedFace) Close() error {
	*t.closes = append(*t.closes, t.name)
	return nil
}

// TestScaledFaceCacheLifecycle pins that RasterizeScaled derives one physical
// face per scale and reuses it, that scale-1 rasterization never derives, and
// that Close releases the derived faces and the base face in reverse creation
// order, idempotently.
func TestScaledFaceCacheLifecycle(t *testing.T) {
	base := &fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{'A': fixed.I(10), 'V': fixed.I(15)},
		bounds:  map[rune]fixed.Rectangle26_6{'A': box8(), 'V': box8()},
		kern:    map[[2]rune]fixed.Int26_6{{'A', 'V'}: -fixed.I(2)},
	}
	var closes []string
	tracked := func(name string) font.Face {
		return &trackedFace{fakeFace: base, name: name, closes: &closes}
	}

	face := newFace(tracked("base"))
	derives := 0
	face.derive = func(scale float64) (font.Face, error) {
		derives++
		return tracked(fmt.Sprintf("scale-%v", scale)), nil
	}

	if _, err := face.RasterizeScaled("AV", 2, 2); err != nil {
		t.Fatalf("RasterizeScaled(2,2) error = %v", err)
	}
	if _, err := face.RasterizeScaled("AV", 2, 2); err != nil {
		t.Fatalf("RasterizeScaled(2,2) again error = %v", err)
	}
	if derives != 1 {
		t.Errorf("derive called %d times for two RasterizeScaled(2,2) calls, want 1 (cached)", derives)
	}
	if len(face.derived) != 1 {
		t.Errorf("derived cache = %d faces, want 1", len(face.derived))
	}
	if _, err := face.RasterizeScaled("AV", 1.5, 1.5); err != nil {
		t.Fatalf("RasterizeScaled(1.5,1.5) error = %v", err)
	}
	if derives != 2 {
		t.Errorf("derive called %d times for scales 2 and 1.5, want 2", derives)
	}
	if _, err := face.Rasterize("AV"); err != nil {
		t.Fatalf("Rasterize error = %v", err)
	}
	if derives != 2 {
		t.Errorf("derive called %d times after scale-1 Rasterize, want still 2", derives)
	}

	if err := face.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	want := []string{"scale-1.5", "scale-2", "base"}
	if fmt.Sprint(closes) != fmt.Sprint(want) {
		t.Errorf("close order = %v, want %v (reverse creation order)", closes, want)
	}

	if err := face.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if len(closes) != len(want) {
		t.Errorf("second Close closed %d more faces: %v", len(closes)-len(want), closes)
	}
	if _, err := face.RasterizeScaled("AV", 2, 2); err == nil {
		t.Error("RasterizeScaled on closed face error = nil, want non-nil")
	}
	if _, err := face.Rasterize("AV"); err == nil {
		t.Error("Rasterize on closed face error = nil, want non-nil")
	}
}

// TestScaledFaceDeriveError pins that a failed derivation surfaces the error
// and caches nothing.
func TestScaledFaceDeriveError(t *testing.T) {
	face := newFace(&fakeFace{})
	face.derive = func(scale float64) (font.Face, error) {
		return nil, errors.New("boom")
	}
	if _, err := face.RasterizeScaled("AV", 2, 2); err == nil {
		t.Fatal("RasterizeScaled with failing derive error = nil, want non-nil")
	}
	if len(face.derived) != 0 {
		t.Errorf("derived cache = %d faces after failure, want 0", len(face.derived))
	}
}
