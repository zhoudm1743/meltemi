//go:build linux && cgo

package text

// HarfBuzz shaping engine: itemizes text into font runs (primary vs
// fontconfig fallback) and direction runs, shapes each run with HarfBuzz
// and assembles the visual line. FreeType owns the font data and rasterizes
// glyphs by ID (raster_linux_cgo.go); fontconfig only resolves runes to
// font paths (fontconfig_linux_cgo.go), whose bytes fallback.go reads and
// caches.
//
// Every engine owns its FreeType face and HarfBuzz font and destroys them
// in that order; the shaper tracks creation order and releases everything
// in reverse on Close. On any creation failure the partially built engine
// destroys itself and nothing is cached.

/*
#cgo pkg-config: harfbuzz freetype2
#include <stdlib.h>
#include <ft2build.h>
#include FT_FREETYPE_H
#include <hb.h>
#include <hb-ft.h>
*/
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"unsafe"

	"golang.org/x/image/math/fixed"
)

var (
	ftOnce    sync.Once
	ftLibrary C.FT_Library
	ftInitErr error
)

// ftLib initializes the global FreeType library once. Engine creation,
// shaping and rasterization all happen on the UI thread; the Once only
// guards process startup.
func ftLib() (C.FT_Library, error) {
	ftOnce.Do(func() {
		if C.FT_Init_FreeType(&ftLibrary) != 0 {
			ftInitErr = errors.New("text: freetype initialization failed")
		}
	})
	return ftLibrary, ftInitErr
}

// hbEngine is one font at one physical scale: a FreeType memory face plus
// the HarfBuzz font layered on it. The shaper owns the font data buffer and
// keeps it alive for the FreeType memory face.
type hbEngine struct {
	data  []byte
	face  C.FT_Face
	font  *C.hb_font_t
	scale float64
}

// newHBEngine opens data at size*scale. From the first successful C
// allocation every failure destroys what was already created, so the engine
// never leaks and the caller never receives a half-built face.
func newHBEngine(data []byte, size, scale float64) (*hbEngine, error) {
	if len(data) == 0 {
		return nil, errors.New("text: empty font data")
	}
	lib, err := ftLib()
	if err != nil {
		return nil, err
	}
	if size <= 0 || scale <= 0 || math.IsNaN(size) || math.IsNaN(scale) || math.IsInf(size, 0) || math.IsInf(scale, 0) {
		return nil, fmt.Errorf("text: invalid engine size %v scale %v", size, scale)
	}
	e := &hbEngine{data: data, scale: scale}
	if C.FT_New_Memory_Face(lib, (*C.FT_Byte)(unsafe.Pointer(&data[0])), C.FT_Long(len(data)), 0, &e.face) != 0 {
		return nil, errors.New("text: freetype cannot load font data")
	}
	// The engine owns C resources from here on; destroy balances it.
	liveEngines.Add(1)
	ok := false
	defer func() {
		if !ok {
			e.destroy()
		}
	}()
	if e.face.num_glyphs == 0 {
		return nil, errors.New("text: font has no glyphs")
	}
	px := C.FT_F26Dot6(math.Round(size * scale * 64))
	if C.FT_Set_Char_Size(e.face, 0, px, 72, 72) != 0 {
		return nil, fmt.Errorf("text: freetype cannot set size %vpx", size*scale)
	}
	e.font = C.hb_ft_font_create(e.face, nil)
	if e.font == nil {
		return nil, errors.New("text: harfbuzz font creation failed")
	}
	C.hb_ft_font_set_load_flags(e.font, C.int(C.FT_LOAD_DEFAULT))
	ok = true
	return e, nil
}

// destroy releases the HarfBuzz font first (it borrows the FreeType face)
// and then the face itself. It is idempotent and balances the liveEngines
// counter exactly once per engine.
func (e *hbEngine) destroy() error {
	var err error
	if e.font != nil {
		C.hb_font_destroy(e.font)
		e.font = nil
	}
	if e.face != nil {
		if C.FT_Done_Face(e.face) != 0 {
			err = errors.New("text: freetype face release failed")
		}
		e.face = nil
		liveEngines.Add(-1)
	}
	return err
}

// covers reports whether the font maps r to a real glyph (not .notdef).
func (e *hbEngine) covers(r rune) bool {
	return C.FT_Get_Char_Index(e.face, C.FT_ULong(r)) != 0
}

// lineBox returns the ascent (positive, above baseline) and descent
// (positive, below baseline) in 26.6 pixels at this engine's scale.
func (e *hbEngine) lineBox() (ascent, descent fixed.Int26_6) {
	m := e.face.size.metrics
	return fixed.Int26_6(m.ascender), fixed.Int26_6(-m.descender)
}

// hbShapedGlyph is one shaped glyph in HarfBuzz buffer (visual) order.
type hbShapedGlyph struct {
	id          uint32
	clusterByte int
	advance     fixed.Int26_6
	xOffset     fixed.Int26_6
	yOffset     fixed.Int26_6
}

// hbDirection maps the public direction to HarfBuzz's.
func hbDirection(d Direction) C.hb_direction_t {
	if d == DirectionRTL {
		return C.HB_DIRECTION_RTL
	}
	return C.HB_DIRECTION_LTR
}

// shape runs HarfBuzz over one direction run. The buffer's cluster values
// are byte offsets into text.
func (e *hbEngine) shape(text string, dir Direction) ([]hbShapedGlyph, error) {
	buf := C.hb_buffer_create()
	if buf == nil {
		return nil, errors.New("text: harfbuzz buffer allocation failed")
	}
	defer C.hb_buffer_destroy(buf)
	if len(text) > 0 {
		C.hb_buffer_add_utf8(buf, (*C.char)(unsafe.Pointer(unsafe.StringData(text))), C.int(len(text)), 0, C.int(len(text)))
	}
	C.hb_buffer_guess_segment_properties(buf)
	// Direction is itemized by the engine (per direction run); script and
	// language still come from the guesser.
	C.hb_buffer_set_direction(buf, hbDirection(dir))
	C.hb_shape(e.font, buf, nil, 0)
	n := int(C.hb_buffer_get_length(buf))
	infos := unsafe.Slice(C.hb_buffer_get_glyph_infos(buf, nil), n)
	positions := unsafe.Slice(C.hb_buffer_get_glyph_positions(buf, nil), n)
	out := make([]hbShapedGlyph, 0, n)
	for i := 0; i < n; i++ {
		info := infos[i]
		pos := positions[i]
		out = append(out, hbShapedGlyph{
			id:          uint32(info.codepoint),
			clusterByte: int(info.cluster),
			advance:     fixed.Int26_6(pos.x_advance),
			xOffset:     fixed.Int26_6(pos.x_offset),
			yOffset:     fixed.Int26_6(pos.y_offset),
		})
	}
	return out, nil
}

// runGlyph is one shaped glyph with its absolute rune cluster and resolved
// engine, ready for line assembly.
type runGlyph struct {
	clusterStart, clusterEnd int
	advance                  fixed.Int26_6
	xOffset, yOffset         fixed.Int26_6
	penX                     fixed.Int26_6 // assigned during assembly
	id                       uint32
	eng                      *hbEngine
	ink                      rasterBitmap
}

// shapedRun is one itemized run: same font, same direction. glyphs is in
// visual (paint) order; caretEdge has one entry per rune boundary in the
// run ([startRune..endRune]) in visual coordinates.
type shapedRun struct {
	startRune, endRune int
	direction          Direction
	glyphs             []runGlyph
	caretEdge          []fixed.Int26_6
}

// fontRun is one maximal run of runes sharing one engine.
type fontRun struct {
	startRune, endRune int
	eng                *hbEngine
}

// hbShaper is the complex-text engine of one Face: the primary font plus
// the per-scale derived engines and the fontconfig fallback engines.
type hbShaper struct {
	data     []byte // owned copy backing every memory face
	size     float64
	base     *hbEngine
	derived  map[float64]*hbEngine
	fallback map[string]map[float64]*hbEngine // path → scale → engine
	order    []*hbEngine                      // creation order, base first
	resolver *fallbackResolver
	closed   bool
}

func newHBShaper(data []byte, size float64) (complexShaper, error) {
	owned := bytes.Clone(data)
	base, err := newHBEngine(owned, size, 1)
	if err != nil {
		return nil, fmt.Errorf("text: create shaper: %w", err)
	}
	return &hbShaper{
		data:     owned,
		size:     size,
		base:     base,
		order:    []*hbEngine{base},
		resolver: &fallbackResolver{},
	}, nil
}

func init() { newComplexShaper = newHBShaper }

// engine returns the primary engine at the given vertical scale, deriving
// and caching it on first use; scale 1 is the base engine.
func (s *hbShaper) engine(scale float64) (*hbEngine, error) {
	if scale == 1 {
		return s.base, nil
	}
	if e, ok := s.derived[scale]; ok {
		return e, nil
	}
	e, err := newHBEngine(s.data, s.size, scale)
	if err != nil {
		return nil, fmt.Errorf("text: derive physical face at scale %v: %w", scale, err)
	}
	if s.derived == nil {
		s.derived = make(map[float64]*hbEngine)
	}
	s.derived[scale] = e
	s.order = append(s.order, e)
	return e, nil
}

// fallbackEngine returns the engine for a resolved fallback font path at
// the given scale, reading the font bytes once (cached in the resolver) and
// deriving one engine per scale. Failures are not cached: a later layout
// retries and the rune degrades to the primary notdef meanwhile.
func (s *hbShaper) fallbackEngine(path string, scale float64) (*hbEngine, error) {
	if byScale := s.fallback[path]; byScale != nil {
		if e, ok := byScale[scale]; ok {
			return e, nil
		}
	}
	data, err := s.resolver.dataForPath(path)
	if err != nil {
		return nil, err
	}
	e, err := newHBEngine(data, s.size, scale)
	if err != nil {
		return nil, err
	}
	if s.fallback == nil {
		s.fallback = make(map[string]map[float64]*hbEngine)
	}
	if s.fallback[path] == nil {
		s.fallback[path] = make(map[float64]*hbEngine)
	}
	s.fallback[path][scale] = e
	s.order = append(s.order, e)
	return e, nil
}

// close destroys every engine in reverse creation order, idempotently.
func (s *hbShaper) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	for i := len(s.order) - 1; i >= 0; i-- {
		if destroyErr := s.order[i].destroy(); destroyErr != nil && err == nil {
			err = destroyErr
		}
	}
	s.order = nil
	s.derived = nil
	s.fallback = nil
	return err
}

// layout shapes value at physical scaleX/scaleY and returns the full
// layout. Horizontal advances follow scaleX/scaleY (xRatio) so the ink
// stays rasterized at the vertical scale, matching the legacy
// rasterization contract.
func (s *hbShaper) layout(value string, scaleX, scaleY float64) (LineLayout, error) {
	runes := []rune(value)
	layout := LineLayout{
		Direction:    DirectionLTR,
		CaretOffsets: make([]fixed.Int26_6, len(runes)+1),
	}
	if len(runes) == 0 {
		return layout, nil
	}
	primary, err := s.engine(scaleY)
	if err != nil {
		return LineLayout{}, err
	}
	xRatio := scaleX / scaleY

	// 1. Itemize by font coverage: one engine per maximal rune run.
	fontRuns := itemizeFonts(runes, func(r rune) *hbEngine {
		if primary.covers(r) {
			return primary
		}
		path, lookupErr := s.resolver.pathForRune(r)
		if lookupErr != nil || path == "" {
			layout.noteMissing(r)
			return primary
		}
		fb, openErr := s.fallbackEngine(path, scaleY)
		if openErr != nil {
			layout.noteMissing(r)
			return primary
		}
		return fb
	})

	// 2. Itemize each font run by direction and shape it.
	runs := make([]shapedRun, 0, len(fontRuns))
	for _, fr := range fontRuns {
		for _, dr := range itemizeDirection(runes, fr.startRune, fr.endRune, DirectionLTR) {
			text := string(runes[dr.start:dr.end])
			shaped, err := fr.eng.shape(text, dr.direction)
			if err != nil {
				return LineLayout{}, err
			}
			glyphs := make([]runGlyph, 0, len(shaped))
			for _, g := range shaped {
				glyphs = append(glyphs, runGlyph{
					clusterStart: dr.start + runeIndexForByte(text, g.clusterByte),
					advance:      scaleXFixed(g.advance, xRatio),
					xOffset:      scaleXFixed(g.xOffset, xRatio),
					yOffset:      g.yOffset,
					id:           g.id,
					eng:          fr.eng,
				})
			}
			resolveClusterEnds(glyphs, dr.end)
			runs = append(runs, shapedRun{startRune: dr.start, endRune: dr.end, direction: dr.direction, glyphs: glyphs})
		}
	}
	if len(runs) == 0 {
		return layout, nil
	}
	layout.Direction = runs[0].direction

	// 3. Assemble runs left to right: glyph pens and caret edges.
	assignPens(runs)
	for _, run := range runs {
		for b, edge := range run.caretEdge {
			layout.CaretOffsets[run.startRune+b] = edge
		}
	}

	// 4. Rasterize the glyphs and build the bitmap.
	if err := s.rasterize(&layout, runes, runs, primary, scaleX, scaleY); err != nil {
		return LineLayout{}, err
	}
	return layout, nil
}

// noteMissing records a rune that had to fall back to the notdef glyph.
func (l *LineLayout) noteMissing(r rune) {
	for _, m := range l.Missing {
		if m == r {
			return
		}
	}
	l.Missing = append(l.Missing, r)
}

// itemizeFonts groups consecutive runes that map to the same engine into
// maximal runs.
func itemizeFonts(runes []rune, engineFor func(rune) *hbEngine) []fontRun {
	if len(runes) == 0 {
		return nil
	}
	runs := make([]fontRun, 0, 4)
	start := 0
	current := engineFor(runes[0])
	for i := 1; i <= len(runes); i++ {
		if i == len(runes) {
			runs = append(runs, fontRun{startRune: start, endRune: i, eng: current})
			break
		}
		if eng := engineFor(runes[i]); eng != current {
			runs = append(runs, fontRun{startRune: start, endRune: i, eng: current})
			start = i
			current = eng
		}
	}
	return runs
}

// resolveClusterEnds fills each glyph's cluster end with the smallest
// cluster start greater than its own, or endRune when it is the last
// cluster. Cluster starts decrease along visual order in RTL runs, so the
// next cluster is found by value, not by position.
func resolveClusterEnds(glyphs []runGlyph, endRune int) {
	if len(glyphs) == 0 {
		return
	}
	seen := make(map[int]bool, len(glyphs))
	starts := make([]int, 0, len(glyphs))
	for _, g := range glyphs {
		if !seen[g.clusterStart] {
			seen[g.clusterStart] = true
			starts = append(starts, g.clusterStart)
		}
	}
	sort.Ints(starts)
	for i := range glyphs {
		end := endRune
		for _, s := range starts {
			if s > glyphs[i].clusterStart {
				end = s
				break
			}
		}
		glyphs[i].clusterEnd = end
	}
}

// edgeEvent accumulates, at one rune boundary, the advance of every cluster
// that ends at or before it.
type edgeEvent struct {
	at     int // rune index, run-relative cluster end
	amount fixed.Int26_6
}

// assignPens lays the shaped runs out left to right, computing each glyph's
// pen and each boundary's caret edge. HarfBuzz emits glyphs in visual order
// with positive advances, so both directions place glyphs in buffer order
// with forward pen accumulation; zero-advance marks stay at the pen and are
// positioned by their x_offset. A boundary takes the position after every
// cluster ending at or before it, so a boundary inside a cluster is pinned
// to the cluster start. RTL runs only mirror the caret edges (not the
// glyph pens), so edges decrease through them and the line grows rightward
// like LTR runs.
func assignPens(runs []shapedRun) {
	pen := fixed.Int26_6(0)
	for i := range runs {
		run := &runs[i]
		width := fixed.Int26_6(0)
		events := make([]edgeEvent, 0, len(run.glyphs))
		for _, g := range run.glyphs {
			width += g.advance
			events = append(events, edgeEvent{at: g.clusterEnd - run.startRune, amount: g.advance})
		}
		sort.Slice(events, func(a, b int) bool { return events[a].at < events[b].at })
		edges := make([]fixed.Int26_6, run.endRune-run.startRune+1)
		ei, acc := 0, fixed.Int26_6(0)
		for b := 0; b <= run.endRune-run.startRune; b++ {
			for ei < len(events) && events[ei].at <= b {
				acc += events[ei].amount
				ei++
			}
			edges[b] = acc
		}
		run.caretEdge = edges
		prefix := fixed.Int26_6(0)
		for k := range run.glyphs {
			run.glyphs[k].penX = pen + prefix
			prefix += run.glyphs[k].advance
		}
		if run.direction == DirectionRTL {
			for b := range edges {
				edges[b] = pen + width - edges[b]
			}
		} else {
			for b := range edges {
				edges[b] = pen + edges[b]
			}
		}
		pen += width
	}
}
