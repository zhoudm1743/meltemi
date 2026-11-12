package text

import (
	"strconv"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// faceAt returns the face that should draw r. pick is nil on the complex-text
// engine and on builds with no system-font lookup; it may also return nil to
// keep the primary face.
func faceAt(primary font.Face, pick func(rune) font.Face, r rune) font.Face {
	if pick != nil {
		if face := pick(r); face != nil {
			return face
		}
	}
	return primary
}

// legacyPick returns a per-rune face selector for the legacy rasterizer.
// It is nil when this face has no font data (the test seam), when the
// complex-text engine owns shaping, or when the build has no system font
// lookup. A rune the primary face covers stays on that face, so Latin
// metrics do not change just because a fallback font is installed.
func (f *Face) legacyPick(primary font.Face, size float64) func(rune) font.Face {
	if f == nil || f.hb != nil || f.parsed == nil || primary == nil || systemFontPath == nil || size <= 0 {
		return nil
	}
	return func(r rune) font.Face {
		if _, ok := primary.GlyphAdvance(r); ok {
			return primary
		}
		fallback := f.fallbackFace(r, size)
		if fallback == nil {
			return primary
		}
		if _, ok := fallback.GlyphAdvance(r); !ok {
			return primary
		}
		return fallback
	}
}

func (f *Face) fallbackFace(r rune, size float64) font.Face {
	ref, err := lookupSystemFont(r)
	if err != nil || ref.Path == "" {
		return nil
	}
	key := ref.Path + "\x00" + strconv.FormatFloat(size, 'g', -1, 64)
	if f.fallback != nil {
		if face, ok := f.fallback[key]; ok {
			return face
		}
	}
	data, err := readFontFile(ref.Path)
	if err != nil {
		return nil
	}
	parsed, err := fontCoveringRune(ref.Path, data, r)
	if err != nil || parsed == nil {
		return nil
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil
	}
	if f.fallback == nil {
		f.fallback = make(map[string]font.Face)
	}
	f.fallback[key] = face
	f.order = append(f.order, face)
	return face
}
