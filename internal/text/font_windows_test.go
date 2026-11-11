//go:build windows

package text

import (
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func TestWindowsCJKFallbackChangesAdvance(t *testing.T) {
	if systemFontPath == nil {
		t.Fatal("systemFontPath is nil on Windows")
	}
	withFallback, err := NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("NewFace: %v", err)
	}
	defer withFallback.Close()
	withLayout, err := withFallback.Layout("中")
	if err != nil {
		t.Fatalf("Layout with fallback: %v", err)
	}

	orig := systemFontPath
	systemFontPath = nil
	t.Cleanup(func() { systemFontPath = orig })
	plain, err := NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("NewFace without fallback: %v", err)
	}
	defer plain.Close()
	plainLayout, err := plain.Layout("中")
	if err != nil {
		t.Fatalf("Layout without fallback: %v", err)
	}
	if len(withLayout.CaretOffsets) != 2 || len(plainLayout.CaretOffsets) != 2 {
		t.Fatalf("caret lengths = %d/%d, want 2/2", len(withLayout.CaretOffsets), len(plainLayout.CaretOffsets))
	}
	if withLayout.CaretOffsets[1] == plainLayout.CaretOffsets[1] {
		t.Fatalf("CJK advance with system font = %v, same as the primary notdef; fallback did not load", withLayout.CaretOffsets[1])
	}
	if withLayout.Glyphs[0].Width <= 0 || withLayout.Glyphs[0].Height <= 0 {
		t.Fatalf("fallback glyph = %+v, want ink", withLayout.Glyphs[0])
	}
}
