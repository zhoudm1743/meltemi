//go:build windows

package text

import (
	"os"
	"sync"

	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

func init() {
	systemFontPath = windowsSystemFont
}

// windowsFontCandidates is a short, ordered list of fonts shipped with
// Windows. The first file whose cmap covers a rune wins. This is the legacy
// engine's stand-in for fontconfig: it only resolves a path.
var windowsFontCandidates = []string{
	`C:\Windows\Fonts\msyh.ttc`,
	`C:\Windows\Fonts\msyhbd.ttc`,
	`C:\Windows\Fonts\simsun.ttc`,
	`C:\Windows\Fonts\msjh.ttc`,
	`C:\Windows\Fonts\segoeui.ttf`,
	`C:\Windows\Fonts\arial.ttf`,
	`C:\Windows\Fonts\seguiemj.ttf`,
}

var (
	winFontMu     sync.Mutex
	winFontByRune = map[rune]string{}
	winFontBytes  = map[string][]byte{}
	winFontColl   = map[string]*opentype.Collection{}
	winFontBuf    sfnt.Buffer
)

func windowsSystemFont(r rune) (string, error) {
	winFontMu.Lock()
	defer winFontMu.Unlock()
	if path, ok := winFontByRune[r]; ok {
		return path, nil
	}
	var found string
	for _, path := range windowsFontCandidates {
		if windowsFontCovers(path, r) {
			found = path
			break
		}
	}
	winFontByRune[r] = found
	return found, nil
}

func windowsFontCovers(path string, r rune) bool {
	collection, err := windowsCollection(path)
	if err != nil || collection == nil {
		return false
	}
	for i := 0; i < collection.NumFonts(); i++ {
		face, err := collection.Font(i)
		if err != nil || face == nil {
			continue
		}
		index, err := face.GlyphIndex(&winFontBuf, r)
		if err == nil && index != 0 {
			return true
		}
	}
	return false
}

func windowsCollection(path string) (*opentype.Collection, error) {
	if collection, ok := winFontColl[path]; ok {
		return collection, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		winFontColl[path] = nil
		return nil, err
	}
	collection, err := opentype.ParseCollection(data)
	if err != nil {
		winFontColl[path] = nil
		return nil, err
	}
	winFontBytes[path] = data
	winFontColl[path] = collection
	return collection, nil
}

func fontCoveringRune(path string, data []byte, r rune) (*opentype.Font, error) {
	winFontMu.Lock()
	defer winFontMu.Unlock()
	if _, ok := winFontBytes[path]; !ok {
		winFontBytes[path] = data
	}
	collection, ok := winFontColl[path]
	if !ok || collection == nil {
		parsed, err := opentype.ParseCollection(winFontBytes[path])
		if err != nil {
			return nil, err
		}
		winFontColl[path] = parsed
		collection = parsed
	}
	for i := 0; i < collection.NumFonts(); i++ {
		face, err := collection.Font(i)
		if err != nil || face == nil {
			continue
		}
		index, err := face.GlyphIndex(&winFontBuf, r)
		if err == nil && index != 0 {
			return face, nil
		}
	}
	return nil, nil
}
