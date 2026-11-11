// Package text turns a font file into rasterized text: it loads a font at a
// fixed size and produces 8-bit alpha coverage bitmaps plus per-glyph
// positioning for render backends to draw.
package text

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"math"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Direction is the resolved inline direction of a run of text.
type Direction int

const (
	// DirectionLTR is left-to-right text.
	DirectionLTR Direction = iota
	// DirectionRTL is right-to-left text.
	DirectionRTL
)

func (d Direction) String() string {
	switch d {
	case DirectionRTL:
		return "RTL"
	default:
		return "LTR"
	}
}

// Cluster maps a logical rune range to the visual glyphs that draw it and to
// the visual extent used for hit-testing. StartRune/EndRune are the logical
// rune boundary indexes (EndRune exclusive); StartGlyph/EndGlyph index into
// LineLayout.Glyphs, which is in visual paint order and therefore differs
// from logical order inside RTL runs. X0 and X1 are the cluster's left and
// right edges in the same coordinate space as CaretOffsets.
type Cluster struct {
	StartRune  int
	EndRune    int
	StartGlyph int
	EndGlyph   int

	X0 fixed.Int26_6
	X1 fixed.Int26_6
}

// LineLayout is the shaped form of one line of text: the logical bitmap plus
// the cluster, direction and caret information complex text needs. Bitmap is
// the logical (scale-1) coverage mask; Face.RasterizeScaled produces the same
// structure at physical resolution through the layout engine. Glyphs is in
// visual paint order (for RTL runs that is the reverse of logical order);
// Bitmap.Glyphs keeps the legacy per-rune view where every rune has one
// entry. CaretOffsets has one entry per rune boundary (runes+1): it is the
// visual pen position of each logical boundary, so it increases through LTR
// runs and decreases through RTL runs; a boundary inside a cluster is pinned
// to the cluster start so editing never cuts a cluster. The final entry is
// where the caret after the last rune sits, which equals the line advance
// only for pure LTR lines.
type LineLayout struct {
	Direction    Direction
	Bitmap       Bitmap
	Glyphs       []Glyph
	Clusters     []Cluster
	CaretOffsets []fixed.Int26_6

	// Missing lists runes no font could cover (system fallback failed);
	// they were drawn with the primary font's notdef glyph. It is nil when
	// every rune had a font.
	Missing []rune
}

// Glyph records where one rune's ink landed in a Bitmap: X and Y are the
// top-left of the drawn glyph rect in bitmap pixels, and Width and Height
// its size. Advance is the exact 26.6 fixed-point pen advance to the next
// glyph before kerning; the next glyph's X already includes their kern
// pair amount. In a LineLayout's visual Glyphs one entry stands for a whole
// cluster and Rune is its first logical rune; in Bitmap.Glyphs every rune
// keeps its own entry and runes after the first of a cluster carry zero
// Advance.
type Glyph struct {
	Rune    rune
	X       int
	Y       int
	Width   int
	Height  int
	Advance fixed.Int26_6
}

// Bitmap is a single line of rasterized text: an 8-bit alpha coverage mask
// with one byte per pixel, plus per-glyph ink positions. Width, Height,
// Stride, OriginX, Alpha and Glyphs describe the physical pixel mask: at
// rasterization scale 1 they are logical pixels too, at scale 2 every field
// is twice the logical resolution. LogicalWidth, LogicalHeight and
// LogicalOriginX carry the draw geometry in logical units: the rectangle of
// size LogicalWidth x LogicalHeight with its top-left at the drawing
// position is where renderers must place the bitmap, however large the
// physical mask is. Alpha holds Stride*Height bytes in row-major order, rows
// top to bottom. The bitmap is sized to cover every glyph's ink, including
// parts that reach left of the origin, right of the advance or outside the
// face's ascent and descent. OriginX is the first glyph's pen origin
// relative to the bitmap's left edge, in physical pixels; LogicalOriginX is
// the same origin in logical units.
type Bitmap struct {
	Width   int
	Height  int
	Stride  int
	OriginX int
	Alpha   []byte
	Glyphs  []Glyph

	// Logical draw geometry: physical size and origin divided by the
	// rasterization scales. A renderer uploads the physical mask but
	// positions and sizes the quad with these values.
	LogicalWidth   float64
	LogicalHeight  float64
	LogicalOriginX float64
}

// Face is a typeface loaded from font data at a fixed logical size. It is not
// safe for concurrent use; rasterize on the UI thread like the renderer.
//
// RasterizeScaled derives physical faces at the requested scale and caches
// them; Close releases the derived faces and the base face in reverse
// creation order.
type Face struct {
	face   font.Face
	parsed *opentype.Font
	size   float64

	// hb is the complex-text engine (HarfBuzz/FreeType with fontconfig
	// fallback on Linux+cgo). It is nil on the legacy path: non-cgo or
	// non-Linux builds, and faces built from a pre-constructed font.Face by
	// the test seam. Rasterize, RasterizeScaled, CaretOffsets and Layout
	// dispatch on it; it is released by Close in reverse creation order
	// together with the legacy faces below.
	hb complexShaper

	// derive constructs a physical face at the given vertical scale. It is
	// the seam tests inject to observe caching and release order; when nil,
	// faces are derived from parsed at size*scale.
	derive func(scale float64) (font.Face, error)

	derived map[float64]font.Face
	// fallback caches system-font faces used when the primary font has no
	// glyph. They are closed with order.
	fallback map[string]font.Face
	// order records face creation order (base first) so Close can release
	// in reverse.
	order []font.Face
}

// NewFace parses font data (TTF/OTF) and returns a Face rasterizing at the
// given size in logical pixels (72 DPI). It returns an error when the data is
// not a usable font or the size is not positive. On Linux with cgo the face
// shapes with HarfBuzz and rasterizes by glyph ID with FreeType, falling back
// to system fonts through fontconfig for runes the primary font does not
// cover. Windows keeps the legacy x/image engine and, when a system font
// covers the rune, falls back to that font for the same missing-glyph
// case. Other builds keep the legacy engine with no system fallback.
func NewFace(data []byte, size float64) (*Face, error) {
	if size <= 0 {
		return nil, fmt.Errorf("text: font size %v is not positive", size)
	}
	parsed, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("text: parse font: %w", err)
	}
	if newComplexShaper != nil {
		shaper, err := newComplexShaper(data, size)
		if err != nil {
			return nil, err
		}
		return &Face{parsed: parsed, size: size, hb: shaper}, nil
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("text: create face: %w", err)
	}
	return &Face{
		face:   face,
		parsed: parsed,
		size:   size,
		order:  []font.Face{face},
	}, nil
}

// newFace wraps an already constructed font.Face. It is the seam tests use
// to exercise layout with deterministic glyph metrics and kerning. Such a
// face has no font data, so RasterizeScaled can only rasterize at scale 1
// unless the test injects a derive function.
func newFace(face font.Face) *Face {
	return &Face{face: face, order: []font.Face{face}}
}

// CaretOffsets returns rune-boundary pen positions in fixed-point pixels.
// Every position shares the origin of the first glyph's pen: offsets[0] is
// zero for LTR text and the line advance for RTL text, later boundaries
// include the preceding advance and any kerning before the next rune, and
// the final offset is where the caret after the last rune sits (the complete
// string advance for LTR lines). Bitmap ink bounds never affect these
// coordinates. For complex lines the offsets come from Layout, so a boundary
// inside a cluster equals the cluster start and offsets decrease through RTL
// runs.
func (f *Face) CaretOffsets(value string) ([]fixed.Int26_6, error) {
	if f == nil || f.face == nil && f.hb == nil {
		return nil, errors.New("text: caret offsets on a closed or nil face")
	}
	if f.hb != nil {
		layout, err := f.hb.layout(value, 1, 1)
		if err != nil {
			return nil, err
		}
		return layout.CaretOffsets, nil
	}
	return legacyCaretOffsets(f.face, f.legacyPick(f.face, f.size), value), nil
}

// Layout shapes one line of text and returns the bitmap plus the cluster,
// direction and caret mapping complex text editing needs. An empty string
// returns a zero layout with a single zero caret offset. Layout is logical
// (scale 1); RasterizeScaled produces the same structure at physical
// resolution.
//
// Layout must be called on the same thread as the other Face methods; a Face
// is not safe for concurrent use.
func (f *Face) Layout(value string) (LineLayout, error) {
	if f == nil || f.face == nil && f.hb == nil {
		return LineLayout{}, errors.New("text: layout on a closed or nil face")
	}
	if f.hb != nil {
		return f.hb.layout(value, 1, 1)
	}
	return f.legacyLayout(value), nil
}

// Rasterize draws one line of text into a coverage bitmap. An empty string
// returns a zero-size bitmap without error. Invalid UTF-8 bytes are treated
// as utf8.RuneError, matching utf8.DecodeRuneInString, so malformed input
// neither panics nor fails.
//
// Rasterize is RasterizeScaled at scale 1: every Bitmap field is in logical
// pixels.
//
// Layout runs the same fixed-point pen progression font.Drawer uses when
// drawing (including Face.Kern between neighboring runes), so the recorded
// glyph positions are the positions the ink lands on. The bitmap covers the
// union of every glyph's ink rect, the pen advance and the face's
// ascent/descent box, so nothing clips.
func (f *Face) Rasterize(value string) (Bitmap, error) {
	if f == nil || f.face == nil && f.hb == nil {
		return Bitmap{}, errors.New("text: rasterize on a closed or nil face")
	}
	if value == "" {
		return Bitmap{}, nil
	}
	if f.hb != nil {
		layout, err := f.hb.layout(value, 1, 1)
		if err != nil {
			return Bitmap{}, err
		}
		return layout.Bitmap, nil
	}
	return rasterize(f.face, f.legacyPick(f.face, f.size), value, 1, 1, 1), nil
}

// RasterizeScaled draws one line of text at a physical resolution of scaleX
// by scaleY times the logical size, for crisp text on high-DPI targets. The
// returned bitmap's mask, strides, glyph positions and OriginX are physical
// pixels; LogicalWidth, LogicalHeight and LogicalOriginX are the logical
// geometry a renderer draws the quad with, so layout stays in logical units
// while the upload is physical.
//
// Glyph masks cannot be stretched, so the ink is rasterized at the vertical
// scale and the horizontal pen positions (kern, advances) follow scaleX;
// with the uniform scales real targets report this is simply the content
// scale. One physical face is derived per vertical scale and cached until
// Close; scale 1 always rasterizes on the base face.
//
// Both scales must be positive and finite. An empty string returns a
// zero-size bitmap without error, like Rasterize.
func (f *Face) RasterizeScaled(value string, scaleX, scaleY float64) (Bitmap, error) {
	if f == nil || f.face == nil && f.hb == nil {
		return Bitmap{}, errors.New("text: rasterize on a closed or nil face")
	}
	if scaleX <= 0 || scaleY <= 0 || math.IsNaN(scaleX) || math.IsNaN(scaleY) || math.IsInf(scaleX, 0) || math.IsInf(scaleY, 0) {
		return Bitmap{}, fmt.Errorf("text: invalid scale (%v, %v)", scaleX, scaleY)
	}
	if value == "" {
		return Bitmap{}, nil
	}
	if f.hb != nil {
		layout, err := f.hb.layout(value, scaleX, scaleY)
		if err != nil {
			return Bitmap{}, err
		}
		return layout.Bitmap, nil
	}
	physical, err := f.scaledFace(scaleY)
	if err != nil {
		return Bitmap{}, err
	}
	return rasterize(physical, f.legacyPick(physical, f.size*scaleY), value, scaleX/scaleY, scaleX, scaleY), nil
}

// scaledFace returns the cached physical face for the given vertical scale,
// deriving and caching it on first use. Scale 1 is the base face itself and
// is never cached.
func (f *Face) scaledFace(scale float64) (font.Face, error) {
	if scale == 1 {
		return f.face, nil
	}
	if derived, ok := f.derived[scale]; ok {
		return derived, nil
	}
	derive := f.derive
	if derive == nil {
		if f.parsed == nil {
			return nil, fmt.Errorf("text: cannot derive a physical face at scale %v: face was built without font data", scale)
		}
		derive = func(s float64) (font.Face, error) {
			return opentype.NewFace(f.parsed, &opentype.FaceOptions{
				Size:    f.size * s,
				DPI:     72,
				Hinting: font.HintingFull,
			})
		}
	}
	physical, err := derive(scale)
	if err != nil {
		return nil, fmt.Errorf("text: derive physical face at scale %v: %w", scale, err)
	}
	if f.derived == nil {
		f.derived = map[float64]font.Face{}
	}
	f.derived[scale] = physical
	f.order = append(f.order, physical)
	return physical, nil
}

// scaleXFixed scales a 26.6 fixed-point horizontal metric by the ratio
// between the horizontal and vertical scales. The ratio is 1 for the uniform
// scales real targets report, and then the value passes through unchanged.
func scaleXFixed(v fixed.Int26_6, xRatio float64) fixed.Int26_6 {
	if xRatio == 1 {
		return v
	}
	return fixed.Int26_6(math.Round(float64(v) * xRatio))
}

// rasterize draws one non-empty line of text with f at physical resolution
// and records physical glyph positions. xRatio is the horizontal-to-vertical
// scale ratio applied to the pen; scaleX and scaleY convert the physical
// result back into the logical geometry carried by the Bitmap. The caller
// validates the face and the empty string.
func rasterize(primary font.Face, pick func(rune) font.Face, value string, xRatio, scaleX, scaleY float64) Bitmap {
	metrics := primary.Metrics()

	// Pass 1 walks the pen and unions the glyph rects at those actual pen
	// positions, so ink that overhangs a glyph's advance is covered no
	// matter where in the string the glyph sits. The vertical box
	// additionally covers the ascent/descent so blank glyphs (spaces) keep
	// the line height. pick, when set, supplies a fallback face for runes
	// the primary face does not cover; kerning applies only inside one face.
	x0, x1 := 0, 0
	y0, y1 := -metrics.Ascent.Ceil(), metrics.Descent.Ceil()
	pos := fixed.Int26_6(0)
	prev := rune(-1)
	var prevFace font.Face
	for i := 0; i < len(value); {
		r, w := utf8.DecodeRuneInString(value[i:])
		face := faceAt(primary, pick, r)
		if prev >= 0 && prevFace == face {
			pos += scaleXFixed(face.Kern(prev, r), xRatio)
		}
		dr, _, _, adv, _ := face.Glyph(fixed.Point26_6{X: pos, Y: 0}, r)
		if !dr.Empty() {
			x0 = min(x0, dr.Min.X)
			x1 = max(x1, dr.Max.X)
			y0 = min(y0, dr.Min.Y)
			y1 = max(y1, dr.Max.Y)
		}
		pos += scaleXFixed(adv, xRatio)
		prev = r
		prevFace = face
		i += w
	}
	x1 = max(x1, pos.Ceil())
	width := x1 - x0
	height := y1 - y0
	if width <= 0 || height <= 0 {
		return Bitmap{}
	}

	// The dot is integral by construction (it is built from Floor/Ceil), so
	// the rects Glyph computes at these pen positions are exactly the rects
	// painted below, glyph by glyph, with the same pen progression.
	dot := fixed.P(-x0, -y0)

	// Pass 2 records each glyph at the real pen positions and paints it with
	// draw.DrawMask, which is the same primitive font.Drawer.DrawString
	// replays; recording and painting therefore cannot drift apart.
	img := image.NewAlpha(image.Rect(0, 0, width, height))
	var glyphs []Glyph
	pen := dot
	prev = rune(-1)
	prevFace = nil
	for i := 0; i < len(value); {
		r, w := utf8.DecodeRuneInString(value[i:])
		face := faceAt(primary, pick, r)
		if prev >= 0 && prevFace == face {
			pen.X += scaleXFixed(face.Kern(prev, r), xRatio)
		}
		dr, mask, maskp, adv, ok := face.Glyph(pen, r)
		glyphs = append(glyphs, Glyph{
			Rune:    r,
			X:       dr.Min.X,
			Y:       dr.Min.Y,
			Width:   dr.Dx(),
			Height:  dr.Dy(),
			Advance: scaleXFixed(adv, xRatio),
		})
		if ok && !dr.Empty() {
			draw.DrawMask(img, dr, image.White, image.Point{}, mask, maskp, draw.Over)
		}
		pen.X += scaleXFixed(adv, xRatio)
		prev = r
		prevFace = face
		i += w
	}

	originX := dot.X.Round()
	return Bitmap{
		Width:   width,
		Height:  height,
		Stride:  img.Stride,
		OriginX: originX,
		Alpha:   img.Pix,
		Glyphs:  glyphs,

		LogicalWidth:   float64(width) / scaleX,
		LogicalHeight:  float64(height) / scaleY,
		LogicalOriginX: float64(originX) / scaleX,
	}
}

// Close releases the glyph caches held by the face: the derived physical
// faces and the base face are closed in reverse creation order, together
// with the complex-text engine and its fallback fonts when present. The face
// must not be used after Close; Close itself is idempotent and returns the
// first close error, if any.
func (f *Face) Close() error {
	if f == nil {
		return nil
	}
	var err error
	if f.hb != nil {
		if closeErr := f.hb.close(); closeErr != nil && err == nil {
			err = closeErr
		}
		f.hb = nil
	}
	for i := len(f.order) - 1; i >= 0; i-- {
		if closeErr := f.order[i].Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	f.face = nil
	f.derived = nil
	f.fallback = nil
	f.order = nil
	return err
}
