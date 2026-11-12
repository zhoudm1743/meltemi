//go:build windows && cgo && meltemi_text_cgo

package text

import (
	"os"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

// TestWindowsHarfbuzzEngineActive pins that the opt-in Windows build really
// replaced the legacy engine instead of silently falling back.
func TestWindowsHarfbuzzEngineActive(t *testing.T) {
	if got := ShapingEngine(); got != "harfbuzz-cgo" {
		t.Fatalf("ShapingEngine() = %q, want harfbuzz-cgo", got)
	}
}

// TestWindowsHarfbuzzCJKFallback proves a CJK rune the primary font lacks is
// shaped and rasterized through the Windows system-font fallback, not drawn
// as the primary notdef.
func TestWindowsHarfbuzzCJKFallback(t *testing.T) {
	requireComplexText(t)

	face, err := NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("NewFace: %v", err)
	}
	defer face.Close()

	layout, err := face.Layout("中")
	if err != nil {
		t.Fatalf("Layout(中): %v", err)
	}
	if len(layout.Missing) != 0 {
		t.Fatalf("Missing = %v, want none; Windows font fallback did not resolve 中", layout.Missing)
	}
	if len(layout.Glyphs) != 1 {
		t.Fatalf("len(Glyphs) = %d, want 1", len(layout.Glyphs))
	}
	if inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
		t.Fatalf("fallback bitmap has no ink: %+v", layout.Bitmap)
	}
}

// TestWindowsHarfbuzzArabicDirection proves HarfBuzz applies real RTL shaping
// on Windows. Arial/Tahoma/Segoe UI all carry Arabic coverage.
func TestWindowsHarfbuzzArabicDirection(t *testing.T) {
	requireComplexText(t)

	candidates := []string{
		`C:\Windows\Fonts\arial.ttf`,
		`C:\Windows\Fonts\tahoma.ttf`,
		`C:\Windows\Fonts\segoeui.ttf`,
	}
	var data []byte
	var used string
	for _, path := range candidates {
		b, err := os.ReadFile(path)
		if err == nil {
			data, used = b, path
			break
		}
	}
	if data == nil {
		t.Skip("no Arabic-capable candidate font found")
	}
	t.Logf("arabic font: %s", used)

	face, err := NewFace(data, 20)
	if err != nil {
		t.Fatalf("NewFace: %v", err)
	}
	defer face.Close()

	layout, err := face.Layout("مرحبا")
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if layout.Direction != DirectionRTL {
		t.Fatalf("Direction = %v, want RTL", layout.Direction)
	}
	if len(layout.Glyphs) == 0 {
		t.Fatal("no shaped glyphs")
	}
	if inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
		t.Fatal("arabic bitmap has no ink")
	}
}
