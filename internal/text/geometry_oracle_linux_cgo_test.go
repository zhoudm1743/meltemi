//go:build linux && cgo

package text

import (
	"testing"

	"golang.org/x/image/math/fixed"
)

// TestMarkOffsetConvention pins the HarfBuzz y-offset conversion against raw
// HarfBuzz/FreeType values straight from the C libraries, independent of the
// engine's own coordinate conversion under test. HarfBuzz reports glyph
// offsets in the font's y-up coordinate system and FreeType bitmap_top is
// the distance of the ink top above the baseline, so in the bitmap's y-down
// rows the mark ink must start at (-yOffset - top) relative to the baseline.
// A flipped sign shifts every GPOS-attached mark vertically by twice its
// offset and fails here.
func TestMarkOffsetConvention(t *testing.T) {
	requireComplexText(t)
	logShapingEnv(t, dejaVuSans)

	data := systemFont(t, dejaVuSans)
	eng, err := newHBEngine(data, 20, 1)
	if err != nil {
		t.Fatalf("newHBEngine: %v", err)
	}
	defer func() {
		if err := eng.destroy(); err != nil {
			t.Errorf("destroy: %v", err)
		}
	}()
	shaped, err := eng.shape("بَ", DirectionRTL)
	if err != nil {
		t.Fatalf("shape: %v", err)
	}
	var mark hbShapedGlyph
	for _, g := range shaped {
		if g.advance == 0 {
			mark = g
		}
	}
	if mark.advance != 0 {
		t.Skipf("font places the mark without a zero-advance glyph")
	}
	bm, err := eng.rasterGlyph(mark.id)
	if err != nil {
		t.Fatalf("rasterGlyph(%d): %v", mark.id, err)
	}
	if bm.width == 0 || bm.height == 0 {
		t.Skipf("mark glyph %d has no outline", mark.id)
	}
	want := (-mark.yOffset - fixed.I(bm.top)).Round()

	face := newSystemFace(t, data, 20)
	layout, err := face.Layout("بَ")
	if err != nil {
		t.Fatalf("Layout error = %v", err)
	}
	ascent, descent := eng.lineBox()
	if layout.Bitmap.Height != ascent.Ceil()+descent.Ceil() {
		t.Fatalf("bitmap height %d, want ascent+descent %d+%d (ink outside the line box)",
			layout.Bitmap.Height, ascent.Ceil(), descent.Ceil())
	}
	var painted Glyph
	for _, g := range layout.Glyphs {
		if g.Advance == 0 {
			painted = g
		}
	}
	mTop, _, _, _, ok := inkedBand(layout.Bitmap, painted)
	if !ok {
		t.Fatalf("painted mark rect %+v has no ink in the bitmap", painted)
	}
	if got := mTop - ascent.Ceil(); got != want {
		t.Errorf("mark ink top relative to baseline = %d, want %d (y_offset %v, bitmap_top %d): mark vertically misplaced",
			got, want, mark.yOffset, bm.top)
	}
}
