package text

import "github.com/go-text/typesetting/font"

// fontScanLookup resolves a rune through the pure-Go system font index. The
// pure-Go engine installs it in init; it is nil when that engine is not built.
// Tests may clear it to isolate the path/face seam fallback.
var fontScanLookup func(rune) *font.Face
