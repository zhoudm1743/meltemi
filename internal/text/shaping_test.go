package text

import (
	"math"
	"os"
	"reflect"
	"runtime"
	"testing"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// System font candidates used by the shaping tests. Tests skip (with a log)
// when a candidate is absent so shaping assertions never depend on one
// host's exact font set; pixel assumptions stay out of the assertions.
const (
	sourceHanSansSC = "/usr/share/fonts/opentype/source-han-cjk/SourceHanSansSC-Regular.otf"
	dejaVuSans      = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	notoSansCJK     = "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
)

// logShapingEnv records the environment the shaping tests run against, as
// required by the task brief: engine, platform and the concrete font files.
func logShapingEnv(t *testing.T, fonts ...string) {
	t.Helper()
	t.Logf("engine=%s platform=%s/%s", ShapingEngine(), runtime.GOOS, runtime.GOARCH)
	for _, path := range fonts {
		if _, err := os.Stat(path); err != nil {
			t.Logf("font missing: %s (%v)", path, err)
		} else {
			t.Logf("font present: %s", path)
		}
	}
}

// systemFont loads the first existing candidate font or skips the test.
func systemFont(t *testing.T, candidates ...string) []byte {
	t.Helper()
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil {
			t.Logf("using system font %s (%d bytes)", path, len(data))
			return data
		}
	}
	t.Skipf("none of the candidate fonts exists: %v", candidates)
	return nil
}

// requireComplexText skips the test when the HarfBuzz/FreeType engine is not
// active (non-cgo or non-Linux builds keep the legacy engine by design).
func requireComplexText(t *testing.T) {
	t.Helper()
	if ShapingEngine() == "legacy" {
		t.Skipf("complex text shaping not active (engine=%s)", ShapingEngine())
	}
}

func newSystemFace(t *testing.T, data []byte, size float64) *Face {
	t.Helper()
	face, err := NewFace(data, size)
	if err != nil {
		t.Fatalf("NewFace: %v", err)
	}
	t.Cleanup(func() { face.Close() })
	return face
}

func runesOf(value string) []rune { return []rune(value) }

// advanceSum returns the total horizontal advance of the shaped glyphs.
func advanceSum(glyphs []Glyph) fixed.Int26_6 {
	var sum fixed.Int26_6
	for _, g := range glyphs {
		sum += g.Advance
	}
	return sum
}

func TestHarfBuzzShapesRealScripts(t *testing.T) {
	requireComplexText(t)
	logShapingEnv(t, dejaVuSans, sourceHanSansSC, notoSansCJK)

	t.Run("latinLigature", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("fi")
		if err != nil {
			t.Fatalf("Layout(fi) error = %v", err)
		}
		if layout.Direction != DirectionLTR {
			t.Fatalf("Direction = %v, want LTR", layout.Direction)
		}
		if len(layout.Glyphs) != 1 {
			t.Fatalf("len(Glyphs) = %d, want 1 (fi ligature)", len(layout.Glyphs))
		}
		if len(layout.Clusters) != 1 || layout.Clusters[0].StartRune != 0 || layout.Clusters[0].EndRune != 2 {
			t.Fatalf("Clusters = %+v, want one cluster covering runes [0,2)", layout.Clusters)
		}
		if layout.Bitmap.Width <= 0 || inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
			t.Errorf("bitmap has no ink: %dx%d", layout.Bitmap.Width, layout.Bitmap.Height)
		}
	})

	t.Run("chinese", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, sourceHanSansSC, notoSansCJK), 20)
		layout, err := face.Layout("中文")
		if err != nil {
			t.Fatalf("Layout(中文) error = %v", err)
		}
		if layout.Direction != DirectionLTR {
			t.Errorf("Direction = %v, want LTR", layout.Direction)
		}
		if len(layout.Glyphs) != 2 {
			t.Fatalf("len(Glyphs) = %d, want 2", len(layout.Glyphs))
		}
		if len(layout.Clusters) != 2 {
			t.Fatalf("len(Clusters) = %d, want 2", len(layout.Clusters))
		}
		for i, c := range layout.Clusters {
			if c.StartRune != i || c.EndRune != i+1 || c.EndGlyph-c.StartGlyph != 1 {
				t.Errorf("Clusters[%d] = %+v, want 1:1 rune/glyph cluster at rune %d", i, c, i)
			}
		}
		for i, g := range layout.Glyphs {
			if g.Advance <= 0 {
				t.Errorf("Glyphs[%d].Advance = %v, want positive", i, g.Advance)
			}
		}
		if inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
			t.Error("bitmap has no ink")
		}
	})

	t.Run("arabic", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("مرحبا")
		if err != nil {
			t.Fatalf("Layout(مرحبا) error = %v", err)
		}
		if layout.Direction != DirectionRTL {
			t.Fatalf("Direction = %v, want RTL", layout.Direction)
		}
		runes := runesOf("مرحبا")
		if len(layout.Glyphs) != len(runes) {
			t.Fatalf("len(Glyphs) = %d, want %d (no ligature in this word)", len(layout.Glyphs), len(runes))
		}
		// Visual order must be the reverse of logical order: the first
		// painted glyph is the final rune.
		if layout.Glyphs[0].Rune != runes[len(runes)-1] || layout.Glyphs[len(runes)-1].Rune != runes[0] {
			t.Errorf("glyph runes = %c...%c, want visual order %c...%c",
				layout.Glyphs[0].Rune, layout.Glyphs[len(runes)-1].Rune,
				runes[len(runes)-1], runes[0])
		}
		// The caret starts at the right edge and decreases to zero.
		offsets := layout.CaretOffsets
		if len(offsets) != len(runes)+1 {
			t.Fatalf("len(CaretOffsets) = %d, want %d", len(offsets), len(runes)+1)
		}
		if offsets[0] != advanceSum(layout.Glyphs) {
			t.Errorf("CaretOffsets[0] = %v, want total advance %v (right edge)", offsets[0], advanceSum(layout.Glyphs))
		}
		if offsets[len(runes)] != 0 {
			t.Errorf("CaretOffsets[%d] = %v, want 0 (origin)", len(runes), offsets[len(runes)])
		}
		for i := 1; i < len(offsets); i++ {
			if offsets[i] > offsets[i-1] {
				t.Errorf("CaretOffsets[%d] = %v > CaretOffsets[%d] = %v, want decreasing", i, offsets[i], i-1, offsets[i-1])
			}
		}
		if inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
			t.Error("bitmap has no ink")
		}
	})

	t.Run("lamAlefLigature", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("لا")
		if err != nil {
			t.Fatalf("Layout(لا) error = %v", err)
		}
		if layout.Direction != DirectionRTL {
			t.Fatalf("Direction = %v, want RTL", layout.Direction)
		}
		if len(layout.Clusters) != 1 || layout.Clusters[0].StartRune != 0 || layout.Clusters[0].EndRune != 2 {
			t.Fatalf("Clusters = %+v, want one cluster over both runes (lam-alef ligature)", layout.Clusters)
		}
	})

	t.Run("combiningMarks", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("x́y")
		if err != nil {
			t.Fatalf("Layout(xy) error = %v", err)
		}
		// Whether the font composes the mark away or keeps it as a
		// zero-advance glyph of its own, the layout invariants hold:
		// clusters partition the runes in order, caret offsets are
		// non-decreasing (never cutting into a mark), and the ink covers
		// the combining sequence.
		if n := len(layout.Glyphs); n < 1 || n > len(runesOf("x́y")) {
			t.Fatalf("len(Glyphs) = %d, want 1..%d", n, len(runesOf("x́y")))
		}
		prev := 0
		for _, c := range layout.Clusters {
			if c.StartRune != prev {
				t.Fatalf("Clusters = %+v, do not partition the runes in order", layout.Clusters)
			}
			prev = c.EndRune
		}
		if prev != len(runesOf("x́y")) {
			t.Errorf("Clusters = %+v, do not cover all runes", layout.Clusters)
		}
		for i := 1; i < len(layout.CaretOffsets); i++ {
			if layout.CaretOffsets[i] < layout.CaretOffsets[i-1] {
				t.Errorf("CaretOffsets = %v, not non-decreasing", layout.CaretOffsets)
			}
		}
		if inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
			t.Error("bitmap has no ink")
		}
	})

	t.Run("mixedDirection", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		const value = "abc مرحبا"
		runes := runesOf(value)
		layout, err := face.Layout(value)
		if err != nil {
			t.Fatalf("Layout(%q) error = %v", value, err)
		}
		if layout.Direction != DirectionLTR {
			t.Errorf("line Direction = %v, want LTR (first strong rune is Latin)", layout.Direction)
		}
		if len(layout.CaretOffsets) != len(runes)+1 {
			t.Fatalf("len(CaretOffsets) = %d, want %d", len(layout.CaretOffsets), len(runes)+1)
		}
		if len(layout.Glyphs) != len(runes) {
			t.Fatalf("len(Glyphs) = %d, want %d", len(layout.Glyphs), len(runes))
		}
		if layout.Glyphs[0].Rune != 'a' {
			t.Errorf("Glyphs[0].Rune = %c, want 'a' (LTR run paints first)", layout.Glyphs[0].Rune)
		}
		// The Arabic run paints to the right of the Latin run and reversed
		// within itself: the first Arabic logical rune must paint after the
		// last Arabic logical rune.
		firstArabic, lastArabic := -1, -1
		for i, g := range layout.Glyphs {
			if g.Rune == runes[4] {
				firstArabic = i
			}
			if g.Rune == runes[len(runes)-1] {
				lastArabic = i
			}
		}
		// Paint order is left-to-right visual, so the first logical Arabic
		// rune (rightmost glyph) must paint after the last logical one.
		if firstArabic < 0 || lastArabic < 0 || firstArabic <= lastArabic {
			t.Errorf("Arabic glyph visual order wrong: first logical at %d, last logical at %d", firstArabic, lastArabic)
		}
		// Caret before the first Arabic rune sits at the right edge of the
		// Arabic run; the caret after the last rune ends at the Arabic run's
		// left edge, which is right of where the Latin run ended only if the
		// Arabic run is wider — assert the structural invariants instead:
		// offsets increase through the Latin run and decrease through the
		// Arabic run.
		offsets := layout.CaretOffsets
		for i := 1; i <= 3; i++ {
			if offsets[i] <= offsets[i-1] {
				t.Errorf("CaretOffsets not increasing in Latin run: [%d]=%v [%d]=%v", i-1, offsets[i-1], i, offsets[i])
			}
		}
		for i := 5; i < len(offsets); i++ {
			if offsets[i] >= offsets[i-1] {
				t.Errorf("CaretOffsets not decreasing in Arabic run: [%d]=%v [%d]=%v", i-1, offsets[i-1], i, offsets[i])
			}
		}
		if offsets[4] <= offsets[3] {
			t.Errorf("CaretOffsets[4] = %v, want > %v (right edge of the Arabic run)", offsets[4], offsets[3])
		}
	})

	t.Run("invalidUTF8", func(t *testing.T) {
		face := newTestFace(t)
		value := string([]byte{'W', 0xff, 'd'})
		layout, err := face.Layout(value)
		if err != nil {
			t.Fatalf("Layout with invalid UTF-8 error = %v, want nil", err)
		}
		if len(layout.Glyphs) != 3 {
			t.Fatalf("len(Glyphs) = %d, want 3", len(layout.Glyphs))
		}
		if layout.Bitmap.Glyphs[1].Rune != utf8.RuneError {
			t.Errorf("Bitmap.Glyphs[1].Rune = %q, want RuneError", layout.Bitmap.Glyphs[1].Rune)
		}
		// Deterministic: identical results across calls.
		again, err := face.Layout(value)
		if err != nil {
			t.Fatalf("second Layout error = %v", err)
		}
		if !reflect.DeepEqual(layout, again) {
			t.Error("Layout of invalid UTF-8 is not deterministic")
		}
	})

	t.Run("empty", func(t *testing.T) {
		face := newTestFace(t)
		layout, err := face.Layout("")
		if err != nil {
			t.Fatalf("Layout(\"\") error = %v, want nil", err)
		}
		if layout.Bitmap.Width != 0 || layout.Bitmap.Height != 0 || len(layout.Bitmap.Alpha) != 0 {
			t.Errorf("Bitmap = %+v, want zero", layout.Bitmap)
		}
		if len(layout.Glyphs) != 0 || len(layout.Clusters) != 0 {
			t.Errorf("Glyphs/Clusters = %d/%d, want 0/0", len(layout.Glyphs), len(layout.Clusters))
		}
		if len(layout.CaretOffsets) != 1 || layout.CaretOffsets[0] != 0 {
			t.Errorf("CaretOffsets = %v, want [0]", layout.CaretOffsets)
		}
		if layout.Direction != DirectionLTR {
			t.Errorf("Direction = %v, want LTR", layout.Direction)
		}
	})
}

func TestComplexTextCaretClusters(t *testing.T) {
	requireComplexText(t)
	logShapingEnv(t, dejaVuSans)

	t.Run("combiningClusterCaret", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("éx")
		if err != nil {
			t.Fatalf("Layout: %v", err)
		}
		offsets := layout.CaretOffsets
		if len(offsets) != 4 {
			t.Fatalf("len(CaretOffsets) = %d, want 4", len(offsets))
		}
		// The boundary inside the cluster must not cut it: it sits at the
		// cluster start, and the boundary after the cluster at its end.
		if offsets[1] != offsets[0] {
			t.Errorf("CaretOffsets[1] = %v, want %v (intra-cluster boundary at cluster start)", offsets[1], offsets[0])
		}
		if offsets[2] <= offsets[1] {
			t.Errorf("CaretOffsets[2] = %v, want > %v (cluster end)", offsets[2], offsets[1])
		}
		if offsets[3] <= offsets[2] {
			t.Errorf("CaretOffsets[3] = %v, want > %v (next rune)", offsets[3], offsets[2])
		}
		// Cluster hit-test bounds match the caret positions of its edges.
		c := layout.Clusters[0]
		if c.StartRune != 0 || c.EndRune != 2 {
			t.Fatalf("Clusters[0] = %+v, want runes [0,2)", c)
		}
		if c.X0 != offsets[c.StartRune] || c.X1 != offsets[c.EndRune] {
			t.Errorf("Cluster bounds = [%v,%v), want caret edges [%v,%v)", c.X0, c.X1, offsets[c.StartRune], offsets[c.EndRune])
		}
	})

	t.Run("ligatureClusterCaret", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("fi")
		if err != nil {
			t.Fatalf("Layout: %v", err)
		}
		offsets := layout.CaretOffsets
		if offsets[1] != offsets[0] {
			t.Errorf("CaretOffsets[1] = %v, want %v (no cut inside ligature)", offsets[1], offsets[0])
		}
		if offsets[2]-offsets[1] != advanceSum(layout.Glyphs) {
			t.Errorf("CaretOffsets[2]-[1] = %v, want cluster advance %v", offsets[2]-offsets[1], advanceSum(layout.Glyphs))
		}
	})

	t.Run("rtlCaretClusterEdges", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("مرحبا")
		if err != nil {
			t.Fatalf("Layout: %v", err)
		}
		// Each cluster's visual bounds match its caret edges in visual space.
		for _, c := range layout.Clusters {
			lo, hi := c.X0, c.X1
			if lo > hi {
				lo, hi = hi, lo
			}
			if hi <= lo {
				t.Errorf("Cluster %+v has empty visual bounds", c)
			}
			start := layout.CaretOffsets[c.StartRune]
			end := layout.CaretOffsets[c.EndRune]
			if start != c.X1 || end != c.X0 {
				t.Errorf("Cluster %+v bounds do not match caret edges (%v,%v)", c, start, end)
			}
		}
	})
}

func TestFontFallbackDiscoveryAndLifecycle(t *testing.T) {
	requireComplexText(t)
	logShapingEnv(t, sourceHanSansSC)

	// goregular has no CJK coverage, so CJK runes force the fallback path.
	newCJKFreeFace := func(t *testing.T) *Face {
		t.Helper()
		return newTestFace(t)
	}

	t.Run("discoveryAndCache", func(t *testing.T) {
		face := newCJKFreeFace(t)
		var lookups, reads int
		origLookup, origRead := lookupSystemFont, readFontFile
		t.Cleanup(func() { lookupSystemFont, readFontFile = origLookup, origRead })
		lookupSystemFont = func(r rune) (FontRef, error) { lookups++; return origLookup(r) }
		readFontFile = func(path string) ([]byte, error) { reads++; return origRead(path) }
		if _, err := face.Layout("中"); err != nil {
			t.Fatalf("Layout(中) error = %v", err)
		}
		if lookups != 1 {
			t.Errorf("fontconfig lookups after first CJK layout = %d, want 1", lookups)
		}
		if reads == 0 {
			t.Error("fallback font file was never read")
		}
		if _, err := face.Layout("中文"); err != nil {
			t.Fatalf("Layout(中文) error = %v", err)
		}
		if lookups != 2 {
			t.Errorf("fontconfig lookups after 中+中文 = %d, want 2 (中 cached, 文 new)", lookups)
		}
		// The fallback engine actually rasterized the CJK ink.
		layout, err := face.Layout("中文")
		if err != nil {
			t.Fatalf("Layout(中文) again: %v", err)
		}
		if inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
			t.Error("fallback bitmap has no ink")
		}
		// Primary-covered runes never consult fontconfig.
		lookups = 0
		if _, err := face.Layout("Wind"); err != nil {
			t.Fatalf("Layout(Wind) error = %v", err)
		}
		if lookups != 0 {
			t.Errorf("fontconfig lookups for pure-Latin text = %d, want 0", lookups)
		}

		// High-DPI: a second scale derives its own fallback engine while
		// the cached path (rune map and font data) is not re-queried.
		readsBefore := reads
		bmp, err := face.RasterizeScaled("中文", 2, 2)
		if err != nil {
			t.Fatalf("RasterizeScaled(中文, 2, 2) error = %v", err)
		}
		if bmp.Width == 0 || len(bmp.Alpha) == 0 {
			t.Errorf("scaled fallback bitmap empty: %+v", bmp)
		}
		if lookups != 0 {
			t.Errorf("fontconfig lookups for cached CJK at scale 2 = %d, want 0", lookups)
		}
		if reads != readsBefore {
			t.Errorf("font file reads for cached CJK at scale 2 = %d, want %d (data cached)", reads, readsBefore)
		}
	})

	t.Run("registerFontPrimaryStillWins", func(t *testing.T) {
		// A face built from data that covers the rune must not consult
		// fontconfig even when fallback fonts exist.
		face := newSystemFace(t, systemFont(t, sourceHanSansSC, notoSansCJK), 20)
		var lookups int
		origLookup := lookupSystemFont
		t.Cleanup(func() { lookupSystemFont = origLookup })
		lookupSystemFont = func(r rune) (FontRef, error) { lookups++; return origLookup(r) }
		if _, err := face.Layout("中"); err != nil {
			t.Fatalf("Layout(中) error = %v", err)
		}
		if lookups != 0 {
			t.Errorf("fontconfig lookups with covering primary font = %d, want 0", lookups)
		}
	})

	t.Run("unresolvedRuneFallsBackToNotdef", func(t *testing.T) {
		face := newCJKFreeFace(t)
		var lookups int
		origLookup := lookupSystemFont
		t.Cleanup(func() { lookupSystemFont = origLookup })
		lookupSystemFont = func(r rune) (FontRef, error) { lookups++; return FontRef{}, nil }
		// Isolate the seam: the pure-Go engine also has a fontscan index
		// that would otherwise resolve 中 from the real system fonts.
		origScan := fontScanLookup
		fontScanLookup = nil
		t.Cleanup(func() { fontScanLookup = origScan })
		layout, err := face.Layout("W中d")
		if err != nil {
			t.Fatalf("Layout with unresolved rune error = %v, want nil", err)
		}
		if len(layout.Glyphs) != 3 {
			t.Fatalf("len(Glyphs) = %d, want 3", len(layout.Glyphs))
		}
		if !reflect.DeepEqual(layout.Missing, []rune{'中'}) {
			t.Errorf("Missing = %v, want [中]", layout.Missing)
		}
		// Negative result is cached too: no repeated lookups.
		if _, err := face.Layout("中中"); err != nil {
			t.Fatalf("second Layout error = %v", err)
		}
		if lookups != 1 {
			t.Errorf("lookups after negative caching = %d, want 1", lookups)
		}
	})

	t.Run("lookupAndReadFailureRollback", func(t *testing.T) {
		face := newCJKFreeFace(t)
		var lookups, reads int
		origLookup, origRead := lookupSystemFont, readFontFile
		t.Cleanup(func() { lookupSystemFont, readFontFile = origLookup, origRead })
		// Isolate the seam from the pure-Go fontscan index.
		origScan := fontScanLookup
		fontScanLookup = nil
		t.Cleanup(func() { fontScanLookup = origScan })
		lookupSystemFont = func(r rune) (FontRef, error) {
			lookups++
			if lookups == 1 {
				return FontRef{}, errTestSeam
			}
			return FontRef{Path: "/nonexistent/fallback-font.ttf"}, nil
		}
		readFontFile = func(path string) ([]byte, error) {
			reads++
			return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
		}

		// First layout: lookup error; second: unreadable file. Both degrade
		// to the primary notdef glyph without losing the whole line.
		for i := 0; i < 2; i++ {
			layout, err := face.Layout("中")
			if err != nil {
				t.Fatalf("Layout #%d error = %v, want graceful notdef", i+1, err)
			}
			if len(layout.Glyphs) != 1 {
				t.Fatalf("Layout #%d len(Glyphs) = %d, want 1", i+1, len(layout.Glyphs))
			}
			if !reflect.DeepEqual(layout.Missing, []rune{'中'}) {
				t.Errorf("Layout #%d Missing = %v, want [中]", i+1, layout.Missing)
			}
		}
		if lookups != 2 {
			t.Errorf("lookups = %d, want 2 (IO failures are not cached as resolved)", lookups)
		}
		if reads != 1 {
			t.Errorf("reads = %d, want 1", reads)
		}
		// No engine leak from the failed fallback loads. Only the cgo engine
		// owns native engines; the pure-Go engine has no counter to inspect.
		if ShapingEngine() == "harfbuzz-cgo" {
			if n := hbEnginesAlive(); n <= 0 {
				t.Errorf("hbEnginesAlive = %d, want > 0 (primary alive)", n)
			}
		}
	})

	t.Run("closeReleasesEngines", func(t *testing.T) {
		cgo := ShapingEngine() == "harfbuzz-cgo"
		if cgo {
			if n := hbEnginesAlive(); n != 0 {
				t.Fatalf("engines alive before test = %d, want 0 (leak from earlier tests)", n)
			}
		}
		face := newCJKFreeFace(t)
		before := 0
		if cgo {
			before = hbEnginesAlive()
			if before != 1 {
				t.Fatalf("engines after NewFace = %d, want 1 (primary only)", before)
			}
		}
		if _, err := face.Layout("中文"); err != nil {
			t.Fatalf("Layout: %v", err)
		}
		if cgo {
			if n := hbEnginesAlive(); n < before+1 {
				t.Errorf("engines after CJK layout = %d, want > %d (fallback derived)", n, before)
			}
		}
		if err := face.Close(); err != nil {
			t.Fatalf("Close error = %v", err)
		}
		if err := face.Close(); err != nil {
			t.Fatalf("second Close error = %v, want nil (idempotent)", err)
		}
		if cgo {
			if n := hbEnginesAlive(); n != 0 {
				t.Errorf("engines after Close = %d, want 0", n)
			}
		}
		if _, err := face.Layout("x"); err == nil {
			t.Error("Layout on closed face error = nil, want non-nil")
		}
		if _, err := face.Rasterize("x"); err == nil {
			t.Error("Rasterize on closed face error = nil, want non-nil")
		}
		if _, err := face.CaretOffsets("x"); err == nil {
			t.Error("CaretOffsets on closed face error = nil, want non-nil")
		}
	})
}

// errTestSeam marks injected seam failures.
var errTestSeam = errorString("test seam error")

type errorString string

func (e errorString) Error() string { return string(e) }

// inkedBand returns the min/max inked row and column of the bitmap inside a
// glyph's recorded rect, or ok=false when the rect is empty or lies outside
// the bitmap. It reads the alpha mask itself rather than trusting the
// recorded rect geometry.
func inkedBand(bmp Bitmap, g Glyph) (top, bottom, left, right int, ok bool) {
	if g.Width <= 0 || g.Height <= 0 || g.X < 0 || g.Y < 0 ||
		g.X+g.Width > bmp.Width || g.Y+g.Height > bmp.Height {
		return 0, 0, 0, 0, false
	}
	for y := g.Y; y < g.Y+g.Height; y++ {
		row := y * bmp.Stride
		for x := g.X; x < g.X+g.Width; x++ {
			if bmp.Alpha[row+x] == 0 {
				continue
			}
			if !ok || y < top {
				top = y
			}
			if !ok || y > bottom {
				bottom = y
			}
			if !ok || x < left {
				left = x
			}
			if !ok || x > right {
				right = x
			}
			ok = true
		}
	}
	return top, bottom, left, right, ok
}

// splitMarkBase separates the zero-advance mark from its base glyph in a
// two-glyph base+mark cluster. ok is false when the glyphs are not exactly
// one positive-advance base plus one zero-advance mark.
func splitMarkBase(glyphs []Glyph) (mark, base Glyph, ok bool) {
	for _, g := range glyphs {
		if g.Advance == 0 {
			mark = g
			continue
		}
		if base.Advance != 0 {
			return Glyph{}, Glyph{}, false
		}
		base = g
	}
	return mark, base, base.Advance > 0
}

// extremeInkedColumns returns the leftmost and rightmost inked columns of
// the whole bitmap, or ok=false when the bitmap has no ink.
func extremeInkedColumns(bmp Bitmap) (left, right int, ok bool) {
	for x := 0; x < bmp.Width; x++ {
		for y := 0; y < bmp.Height; y++ {
			if bmp.Alpha[y*bmp.Stride+x] != 0 {
				if !ok || x < left {
					left = x
				}
				if !ok || x > right {
					right = x
				}
				ok = true
				break
			}
		}
	}
	return left, right, ok
}

// TestDrawingGeometryInvariants pins the drawing geometry against
// font-independent invariants read from the inked bitmap itself, so a
// systematic engine bug (glyph pens mirrored for RTL runs, mark ink shifted
// vertically) fails here instead of passing on same-engine semantic
// assertions. The vertical mark-offset conversion is pinned separately
// against raw HarfBuzz/FreeType values in TestMarkOffsetConvention (cgo
// only). Tests skip (with a log) when the environment lacks the font or the
// font folds combining marks away; no assertion depends on one host's exact
// pixels.
func TestDrawingGeometryInvariants(t *testing.T) {
	requireComplexText(t)
	logShapingEnv(t, dejaVuSans)

	t.Run("arabicMarkOverBase", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("بَ")
		if err != nil {
			t.Fatalf("Layout error = %v", err)
		}
		if len(layout.Glyphs) != 2 {
			t.Skipf("font folds the mark into the base (%d glyphs), cannot test mark geometry", len(layout.Glyphs))
		}
		mark, base, ok := splitMarkBase(layout.Glyphs)
		if !ok {
			t.Fatalf("Glyphs = %+v, want one base glyph and one zero-advance mark", layout.Glyphs)
		}
		mTop, mBottom, mLeft, mRight, ok := inkedBand(layout.Bitmap, mark)
		if !ok {
			t.Fatalf("mark ink rect %+v has no ink in the bitmap", mark)
		}
		bTop, bBottom, bLeft, bRight, ok := inkedBand(layout.Bitmap, base)
		if !ok {
			t.Fatalf("base ink rect %+v has no ink in the bitmap", base)
		}
		// The mark's ink band sits entirely above the base's and the two
		// inks overlap horizontally: the mark is attached over the base,
		// not parked at the far edge of the run.
		if mBottom >= bTop {
			t.Errorf("mark ink rows [%d,%d] not above base ink rows [%d,%d]", mTop, mBottom, bTop, bBottom)
		}
		if mRight < bLeft || mLeft > bRight {
			t.Errorf("mark ink cols [%d,%d] and base ink cols [%d,%d] do not overlap: mark detached from base",
				mLeft, mRight, bLeft, bRight)
		}
	})

	t.Run("rtlVisualInkOrderMatchesLogicalClusters", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		const value = "مرحبا"
		runes := runesOf(value)
		layout, err := face.Layout(value)
		if err != nil {
			t.Fatalf("Layout(%q) error = %v", value, err)
		}
		if len(layout.Glyphs) != len(runes) {
			t.Skipf("font ligates the word (%d glyphs for %d runes), cannot pin per-rune ink order",
				len(layout.Glyphs), len(runes))
		}
		left, right, ok := extremeInkedColumns(layout.Bitmap)
		if !ok {
			t.Fatal("bitmap has no ink")
		}
		// ownerOf returns the per-rune Bitmap.Glyphs entry whose inked band
		// contains the column.
		ownerOf := func(col int) int {
			for i, g := range layout.Bitmap.Glyphs {
				_, _, l, r, ok := inkedBand(layout.Bitmap, g)
				if ok && col >= l && col <= r {
					return i
				}
			}
			return -1
		}
		// Correct visual order for a pure RTL line: the leftmost inked
		// column belongs to the final logical rune and the rightmost to the
		// first. A double-mirrored line paints the whole word backwards.
		if got := ownerOf(left); got != len(runes)-1 {
			t.Errorf("leftmost inked column %d belongs to rune index %d, want final rune index %d (%q): line is mirrored",
				left, got, len(runes)-1, runes[len(runes)-1])
		}
		if got := ownerOf(right); got != 0 {
			t.Errorf("rightmost inked column %d belongs to rune index %d, want first rune index 0 (%q): line is mirrored",
				right, got, runes[0])
		}
	})

	t.Run("latinMarkAboveBase", func(t *testing.T) {
		face := newSystemFace(t, systemFont(t, dejaVuSans), 20)
		layout, err := face.Layout("x́")
		if err != nil {
			t.Fatalf("Layout error = %v", err)
		}
		if len(layout.Glyphs) != 2 {
			t.Skipf("font folds the mark into the base (%d glyphs), cannot test mark geometry", len(layout.Glyphs))
		}
		mark, base, ok := splitMarkBase(layout.Glyphs)
		if !ok {
			t.Fatalf("Glyphs = %+v, want one base glyph and one zero-advance mark", layout.Glyphs)
		}
		_, mBottom, _, _, ok := inkedBand(layout.Bitmap, mark)
		if !ok {
			t.Fatalf("mark ink rect %+v has no ink in the bitmap", mark)
		}
		bTop, _, _, _, ok := inkedBand(layout.Bitmap, base)
		if !ok {
			t.Fatalf("base ink rect %+v has no ink in the bitmap", base)
		}
		if mBottom >= bTop {
			t.Errorf("combining mark ink is not above its base: mark bottom row %d, base top row %d", mBottom, bTop)
		}
	})
}

func TestNoCGOLegacyFallback(t *testing.T) {
	logShapingEnv(t)

	// The legacy engine (used by non-cgo/non-Linux builds and by the test
	// fake seam) must provide the same LineLayout contract with trivial
	// 1:1 clusters and LTR direction.
	face := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{'A': fixed.I(10), '中': fixed.I(10), 'x': fixed.I(10)},
		bounds:  map[rune]fixed.Rectangle26_6{'A': box8(), '中': box8(), 'x': box8()},
	})
	defer face.Close()

	layout, err := face.Layout("A中x")
	if err != nil {
		t.Fatalf("Layout error = %v", err)
	}
	if layout.Direction != DirectionLTR {
		t.Errorf("Direction = %v, want LTR", layout.Direction)
	}
	if len(layout.Glyphs) != 3 {
		t.Fatalf("len(Glyphs) = %d, want 3", len(layout.Glyphs))
	}
	for i, c := range layout.Clusters {
		if c.EndRune-c.StartRune != 1 || c.EndGlyph-c.StartGlyph != 1 {
			t.Errorf("Clusters[%d] = %+v, want 1:1 rune/glyph cluster", i, c)
		}
	}
	if len(layout.CaretOffsets) != 4 {
		t.Fatalf("len(CaretOffsets) = %d, want 4", len(layout.CaretOffsets))
	}
	for i := 1; i < len(layout.CaretOffsets); i++ {
		if layout.CaretOffsets[i] <= layout.CaretOffsets[i-1] {
			t.Errorf("CaretOffsets = %v, want increasing", layout.CaretOffsets)
		}
	}
	// CaretOffsets stays byte/rune deterministic on the legacy path.
	offsets, err := face.CaretOffsets("A中x")
	if err != nil {
		t.Fatalf("CaretOffsets error = %v", err)
	}
	if !reflect.DeepEqual(offsets, layout.CaretOffsets) {
		t.Errorf("CaretOffsets = %v, want Layout's %v", offsets, layout.CaretOffsets)
	}

	// Empty and invalid input stay safe on the legacy path too.
	if _, err := face.Layout(""); err != nil {
		t.Errorf("Layout(\"\") error = %v, want nil", err)
	}
	if layout, err := face.Layout(string([]byte{'A', 0xff})); err != nil || len(layout.Glyphs) != 2 || layout.Bitmap.Glyphs[1].Rune != utf8.RuneError {
		t.Errorf("Layout(invalid UTF-8) = %+v, %v; want 2 glyphs with RuneError", layout.Bitmap.Glyphs, err)
	}

	// The rune-boundary caret math must tolerate zero and negative advances
	// (combining marks and reversed-direction math share this code).
	neg := newFace(&fakeFace{
		metrics: font.Metrics{Ascent: fixed.I(10), Descent: fixed.I(2)},
		advance: map[rune]fixed.Int26_6{'a': fixed.I(8), 'b': -fixed.I(2), 'c': fixed.I(0), 'd': fixed.I(6)},
		bounds:  map[rune]fixed.Rectangle26_6{'a': box8(), 'd': box8()},
	})
	defer neg.Close()
	layout, err = neg.Layout("abcd")
	if err != nil {
		t.Fatalf("Layout with negative/zero advances error = %v", err)
	}
	if len(layout.CaretOffsets) != 5 {
		t.Fatalf("len(CaretOffsets) = %d, want 5", len(layout.CaretOffsets))
	}
	if layout.CaretOffsets[1]-layout.CaretOffsets[0] != fixed.I(8) {
		t.Errorf("first advance = %v, want 8", layout.CaretOffsets[1]-layout.CaretOffsets[0])
	}
	if layout.CaretOffsets[2]-layout.CaretOffsets[1] != -fixed.I(2) {
		t.Errorf("negative advance = %v, want -2", layout.CaretOffsets[2]-layout.CaretOffsets[1])
	}
	if layout.CaretOffsets[3] != layout.CaretOffsets[2] {
		t.Errorf("zero-advance boundary = %v, want equal to previous %v", layout.CaretOffsets[3], layout.CaretOffsets[2])
	}
	if layout.CaretOffsets[4]-layout.CaretOffsets[3] != fixed.I(6) {
		t.Errorf("last advance = %v, want 6", layout.CaretOffsets[4]-layout.CaretOffsets[3])
	}
	if math.IsNaN(float64(layout.CaretOffsets[4])) {
		t.Error("caret offsets became NaN")
	}
}
