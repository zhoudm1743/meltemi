//go:build linux && cgo

package text

// fontconfig lookup for system font fallback. This file only maps a rune to
// a font file path: the bytes are read and cached in Go by fallback.go, and
// the face owns every FreeType/HarfBuzz resource created from them.

/*
#cgo pkg-config: fontconfig
#include <stdlib.h>
#include <fontconfig/fontconfig.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// fcCharsetC and fcFileC are the constant FC_CHARSET/FC_FILE keys as C
// strings, allocated once for the process.
var (
	fcCharsetC = C.CString(C.FC_CHARSET)
	fcFileC    = C.CString(C.FC_FILE)
)

// fcFontForRune asks fontconfig for a font file whose charset covers r. It
// returns "" when no configured font covers the rune.
func fcFontForRune(r rune) (string, error) {
	if C.FcInit() == 0 {
		return "", errors.New("text: fontconfig initialization failed")
	}
	pattern := C.FcPatternCreate()
	if pattern == nil {
		return "", errors.New("text: fontconfig pattern allocation failed")
	}
	defer C.FcPatternDestroy(pattern)

	charset := C.FcCharSetCreate()
	if charset == nil {
		return "", errors.New("text: fontconfig charset allocation failed")
	}
	C.FcCharSetAddChar(charset, C.FcChar32(r))
	if C.FcPatternAddCharSet(pattern, fcCharsetC, charset) == 0 {
		C.FcCharSetDestroy(charset)
		return "", errors.New("text: fontconfig charset registration failed")
	}
	C.FcCharSetDestroy(charset)

	set := C.FcFontList(nil, pattern, nil)
	if set == nil {
		return "", errors.New("text: fontconfig font listing failed")
	}
	defer C.FcFontSetDestroy(set)
	if set.nfont == 0 {
		return "", nil
	}
	fonts := unsafe.Slice(set.fonts, int(set.nfont))
	var file *C.FcChar8
	if C.FcPatternGetString(fonts[0], fcFileC, 0, &file) != C.FcResultMatch {
		return "", fmt.Errorf("text: fontconfig match for rune U+%04X has no file", r)
	}
	return C.GoString((*C.char)(unsafe.Pointer(file))), nil
}

func init() {
	systemFontPath = fcFontForRune
}
