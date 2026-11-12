//go:build windows && !meltemi_text_cgo

package text

import (
	"os"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

// These tests exercise the default pure-Go engine on Windows. The shared
// shaping tests use Linux font paths and skip here, so this file proves the
// engine against fonts that actually ship with Windows.

func TestWindowsGoTextEngineActive(t *testing.T) {
	if got := ShapingEngine(); got != "go-text" {
		t.Fatalf("ShapingEngine() = %q, want go-text", got)
	}
}

func readFirstFont(t *testing.T, paths ...string) ([]byte, string) {
	t.Helper()
	for _, path := range paths {
		if data, err := os.ReadFile(path); err == nil {
			return data, path
		}
	}
	return nil, ""
}

func TestWindowsGoTextLigature(t *testing.T) {
	data, path := readFirstFont(t,
		`C:\Windows\Fonts\calibri.ttf`,
		`C:\Windows\Fonts\cambria.ttc`,
		`C:\Windows\Fonts\candara.ttf`,
		`C:\Windows\Fonts\constan.ttf`,
	)
	if data == nil {
		t.Skip("no ligature-capable candidate font found")
	}
	face, err := NewFace(data, 20)
	if err != nil {
		t.Fatalf("NewFace(%s): %v", path, err)
	}
	defer face.Close()

	layout, err := face.Layout("office")
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(layout.Glyphs) >= len([]rune("office")) {
		t.Skipf("%s does not expose an office/ffi ligature; glyphs=%d", path, len(layout.Glyphs))
	}
	if inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
		t.Fatalf("ligature layout has no ink: %+v", layout.Bitmap)
	}
}

func TestWindowsGoTextArabicDirection(t *testing.T) {
	data, path := readFirstFont(t,
		`C:\Windows\Fonts\arial.ttf`,
		`C:\Windows\Fonts\tahoma.ttf`,
		`C:\Windows\Fonts\segoeui.ttf`,
	)
	if data == nil {
		t.Skip("no Arabic-capable candidate font found")
	}
	face, err := NewFace(data, 20)
	if err != nil {
		t.Fatalf("NewFace(%s): %v", path, err)
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
	if len(layout.CaretOffsets) != len([]rune("مرحبا"))+1 {
		t.Fatalf("caret offsets = %d, want %d", len(layout.CaretOffsets), len([]rune("مرحبا"))+1)
	}
}

// TestWindowsGoTextFontScanFallback disables the curated Windows candidate
// list so the pure-Go fontscan index has to resolve CJK itself.
func TestWindowsGoTextFontScanFallback(t *testing.T) {
	orig := systemFontPath
	systemFontPath = nil
	t.Cleanup(func() { systemFontPath = orig })

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
		t.Fatalf("Missing = %v, want none from fontscan fallback", layout.Missing)
	}
	if inkIn(layout.Bitmap, 0, 0, layout.Bitmap.Width, layout.Bitmap.Height) == 0 {
		t.Fatal("fontscan fallback bitmap has no ink")
	}
}

func TestWindowsGoTextCJKFallback(t *testing.T) {
	if systemFontPath == nil {
		t.Fatal("systemFontPath is nil on Windows")
	}
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
	if len(layout.Glyphs) != 1 || layout.Glyphs[0].Width <= 0 || layout.Glyphs[0].Height <= 0 {
		t.Fatalf("fallback glyph = %+v, want ink", layout.Glyphs)
	}
}
