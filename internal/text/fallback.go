package text

import "os"

// This file holds the system-font fallback resolution the complex-text
// engine uses when the primary font does not cover a rune. fontconfig is
// consulted for a font path only; the font bytes are read and cached here,
// in Go, so every C resource stays owned by the face that loaded it. The
// legacy engine never consults this file.
//
// lookupSystemFont and readFontFile are package-level seams so tests can
// count queries and inject discovery/IO failures without a real fontconfig.

// FontRef identifies a system font file and, for a collection (TTC/OTC), the
// face index inside it. FaceIndex is 0 for the common single-font case. The
// complex-text engine must open exactly this face so coverage discovery and
// the glyphs actually drawn agree.
type FontRef struct {
	Path      string
	FaceIndex int
}

// lookupSystemFont resolves a rune to the system font that covers it. It
// returns the zero FontRef when no system font covers the rune; the result
// (including the negative one) is cached by the resolver.
var lookupSystemFont = func(r rune) (FontRef, error) {
	if systemFontPath == nil {
		return FontRef{}, nil
	}
	return systemFontPath(r)
}

// readFontFile loads the bytes of a resolved font path. Kept as a seam so
// tests can inject unreadable files.
var readFontFile = os.ReadFile

// systemFontPath is installed by the fontconfig build (linux+cgo) or by the
// Windows font lookup; it is nil where no system font lookup exists.
var systemFontPath func(r rune) (FontRef, error)

// fallbackResolver caches the rune → font-path mapping and the path → data
// bytes mapping for one face. It owns no C resources; the engine opens the
// cached bytes and owns whatever it creates from them.
//
// Resolution policy: a successful lookup is cached forever (including the
// negative "no font covers this rune" result, so unsupported characters do
// not re-query fontconfig every frame). Lookup and IO errors are NOT
// cached: they surface to the caller, which degrades to the primary font's
// notdef glyph, and the next layout retries.
type fallbackResolver struct {
	byRune     map[rune]FontRef // zero value means no system font covers the rune
	dataByPath map[string][]byte
}

func (r *fallbackResolver) refForRune(ru rune) (FontRef, error) {
	if r.byRune == nil {
		r.byRune = make(map[rune]FontRef)
	}
	if ref, ok := r.byRune[ru]; ok {
		return ref, nil
	}
	ref, err := lookupSystemFont(ru)
	if err != nil {
		return FontRef{}, err
	}
	r.byRune[ru] = ref
	return ref, nil
}

func (r *fallbackResolver) dataForPath(path string) ([]byte, error) {
	if r.dataByPath == nil {
		r.dataByPath = make(map[string][]byte)
	}
	if data, ok := r.dataByPath[path]; ok {
		return data, nil
	}
	data, err := readFontFile(path)
	if err != nil {
		return nil, err
	}
	r.dataByPath[path] = data
	return data, nil
}
