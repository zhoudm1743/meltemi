//go:build !windows

package text

import "golang.org/x/image/font/opentype"

// fontCoveringRune parses data and returns the collection member that covers
// r. Non-Windows builds reach it only when a test or a future lookup installs
// systemFontPath; the Linux HarfBuzz engine does not use this path.
func fontCoveringRune(_ string, data []byte, r rune) (*opentype.Font, error) {
	collection, err := opentype.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	for i := 0; i < collection.NumFonts(); i++ {
		face, err := collection.Font(i)
		if err != nil || face == nil {
			continue
		}
		index, err := face.GlyphIndex(nil, r)
		if err == nil && index != 0 {
			return face, nil
		}
	}
	return nil, nil
}
