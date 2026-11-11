package text

import (
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// This file is the shaping support shared by every build: the legacy
// (x/image) layout used by non-cgo/non-Linux builds and by the test fake
// seam, plus the engine-independent helpers the HarfBuzz build composes
// shaped runs with. It contains no cgo.

// complexShaper is the complex-text engine behind Face.hb. The HarfBuzz
// build (linux+cgo) installs newComplexShaper and provides the
// implementation; every other build leaves it nil and Face stays on the
// legacy path.
type complexShaper interface {
	// layout shapes value at physical scaleX/scaleY (the returned bitmap
	// carries both physical and logical geometry). scaleX == scaleY == 1
	// produces the logical layout Face.Layout returns.
	layout(value string, scaleX, scaleY float64) (LineLayout, error)
	// close releases every C resource the shaper holds, idempotently.
	close() error
}

// newComplexShaper is non-nil only in builds with a complex-text engine
// (Linux+cgo with HarfBuzz/FreeType). It is set from the engine package's
// init so this file stays cgo-free.
var newComplexShaper func(data []byte, size float64) (complexShaper, error)

// liveEngines counts the C font engines (FreeType faces + HarfBuzz fonts)
// currently allocated across all faces. Tests read it through
// hbEnginesAlive to prove Close releases every engine.
var liveEngines atomic.Int32

// hbEnginesAlive reports how many native font engines are currently
// allocated. It is 0 on builds without a native engine.
func hbEnginesAlive() int { return int(liveEngines.Load()) }

// ShapingEngine names the active shaping engine: "harfbuzz" on Linux+cgo
// builds with HarfBuzz/FreeType/fontconfig, "legacy" elsewhere.
func ShapingEngine() string {
	if newComplexShaper != nil {
		return "harfbuzz"
	}
	return "legacy"
}

// legacyCaretOffsets replays the kern-aware pen progression the legacy
// rasterizer paints, returning one boundary position per rune plus the
// final pen. Zero and negative advances flow through unchanged.
func legacyCaretOffsets(primary font.Face, pick func(rune) font.Face, value string) []fixed.Int26_6 {
	runes := []rune(value)
	offsets := make([]fixed.Int26_6, len(runes)+1)
	var pen fixed.Int26_6
	previous := rune(-1)
	var previousFace font.Face
	for index, r := range runes {
		face := faceAt(primary, pick, r)
		if previous >= 0 && previousFace == face {
			pen += face.Kern(previous, r)
		}
		offsets[index] = pen
		advance, _ := face.GlyphAdvance(r)
		pen += advance
		previous = r
		previousFace = face
	}
	offsets[len(runes)] = pen
	return offsets
}

// legacyLayout builds a LineLayout from the legacy font.Face: one glyph and
// one LTR cluster per rune, pen positions exactly matching what rasterize
// paints (they share the pass-1 progression). The legacy engine does not
// reorder RTL text; that is the documented capability boundary of non-cgo
// and non-Linux builds.
func (f *Face) legacyLayout(value string) LineLayout {
	runes := []rune(value)
	pick := f.legacyPick(f.face, f.size)
	layout := LineLayout{
		Direction:    DirectionLTR,
		Bitmap:       rasterize(f.face, pick, value, 1, 1, 1),
		CaretOffsets: legacyCaretOffsets(f.face, pick, value),
	}
	if len(runes) == 0 {
		return layout
	}
	// The bitmap pass may conclude the line has no ink box (only blank
	// glyphs with zero advances); the cluster/caret contract still holds,
	// so rebuild the per-rune glyph entries from the font primitives.
	if len(layout.Bitmap.Glyphs) == 0 && len(runes) > 0 {
		rects := make([]Glyph, 0, len(runes))
		pen := fixed.Int26_6(0)
		previous := rune(-1)
		var previousFace font.Face
		for _, r := range runes {
			face := faceAt(f.face, pick, r)
			if previous >= 0 && previousFace == face {
				pen += face.Kern(previous, r)
			}
			dr, _, _, adv, _ := face.Glyph(fixed.Point26_6{X: pen, Y: 0}, r)
			rects = append(rects, Glyph{Rune: r, X: dr.Min.X, Y: dr.Min.Y, Width: dr.Dx(), Height: dr.Dy(), Advance: adv})
			pen += adv
			previous = r
			previousFace = face
		}
		layout.Bitmap.Glyphs = rects
	}
	layout.Glyphs = make([]Glyph, 0, len(runes))
	layout.Clusters = make([]Cluster, 0, len(runes))
	for i, g := range layout.Bitmap.Glyphs {
		layout.Glyphs = append(layout.Glyphs, g)
		layout.Clusters = append(layout.Clusters, Cluster{
			StartRune:  i,
			EndRune:    i + 1,
			StartGlyph: i,
			EndGlyph:   i + 1,
			X0:         layout.CaretOffsets[i],
			X1:         layout.CaretOffsets[i+1],
		})
	}
	return layout
}

// strongDirection reports the intrinsic direction of a rune when it has one.
// The RTL ranges are the Unicode bidi R-strong blocks (Hebrew, Arabic,
// Syriac, Thaana, N'Ko, and the Arabic presentation forms); every other
// letter is LTR-strong. Digits, punctuation and marks are weak or neutral
// and report false so itemization can attach them to a neighbor run.
func strongDirection(r rune) (Direction, bool) {
	switch {
	case r >= 0x0590 && r <= 0x05FF, // Hebrew
		r >= 0x0600 && r <= 0x06FF, // Arabic
		r >= 0x0700 && r <= 0x074F, // Syriac
		r >= 0x0750 && r <= 0x077F, // Arabic Supplement
		r >= 0x0780 && r <= 0x07BF, // Thaana
		r >= 0x07C0 && r <= 0x07FF, // NKo
		r >= 0x0800 && r <= 0x083F, // Samaritan
		r >= 0x08A0 && r <= 0x08FF, // Arabic Extended-A
		r >= 0xFB50 && r <= 0xFDFF, // Arabic Presentation Forms-A
		r >= 0xFE70 && r <= 0xFEFF: // Arabic Presentation Forms-B
		return DirectionRTL, true
	}
	if unicode.IsLetter(r) {
		return DirectionLTR, true
	}
	return DirectionLTR, false
}

// directionRun is a maximal sub-range of runes sharing one resolved
// direction. Neutral runes (spaces, digits, punctuation) attach to the
// nearest preceding strong run, or to the first strong run when they lead.
type directionRun struct {
	start, end int
	direction  Direction
}

// itemizeDirection splits [start,end) into direction runs. def is the
// direction for ranges with no strong rune at all.
func itemizeDirection(runes []rune, start, end int, def Direction) []directionRun {
	dirs := make([]Direction, end-start)
	// Resolve each position to the direction of the nearest strong rune at
	// or before it; leading neutrals take the direction of the first strong
	// rune in the whole range, falling back to def when there is none.
	lead := def
	for i := end - 1; i >= start; i-- {
		if d, ok := strongDirection(runes[i]); ok {
			lead = d
		}
	}
	last := lead
	for i := start; i < end; i++ {
		if d, ok := strongDirection(runes[i]); ok {
			last = d
		}
		dirs[i-start] = last
	}
	var runs []directionRun
	runStart := start
	for i := 1; i <= end-start; i++ {
		if i == end-start || dirs[i] != dirs[runStart-start] {
			runs = append(runs, directionRun{start: runStart, end: start + i, direction: dirs[runStart-start]})
			runStart = start + i
		}
	}
	return runs
}

// runeIndexForByte maps a byte offset within a UTF-8 string to the index of
// the rune that starts at or before it (clusters always start on rune
// boundaries, so an exact start is expected).
func runeIndexForByte(text string, byteOffset int) int {
	index := 0
	for i := 0; i < len(text) && i < byteOffset; {
		_, w := utf8.DecodeRuneInString(text[i:])
		i += w
		index++
	}
	return index
}
