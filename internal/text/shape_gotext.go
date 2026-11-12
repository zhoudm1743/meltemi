//go:build !meltemi_text_cgo

package text

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/fontscan"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// Pure-Go complex-text engine. It shapes with go-text/typesetting (a pure Go
// HarfBuzz port plus bidi/script segmentation) and rasterizes glyph outlines
// with golang.org/x/image/vector. It implements the same complexShaper
// interface as the opt-in cgo HarfBuzz/FreeType engine, so Face, LineLayout
// and the whole ui package do not change.
//
// The engine owns no C resources: parsed fonts are ordinary Go values and are
// released by dropping references. Fallback fonts come from the same
// rune -> FontRef seam the legacy path uses.

func init() {
	newComplexShaper = newGoTextShaper
	shapingEngineName = "go-text"
	fontScanLookup = fontScanResolve
}

// goTextShaper is the complex-text engine of one Face: the parsed primary
// font, reusable segmenter/shaper state, and the parsed fallback fonts.
type goTextShaper struct {
	size      float64
	primary   *font.Face
	segmenter shaping.Segmenter
	shaper    shaping.HarfbuzzShaper
	resolver  *fallbackResolver
	fallbacks map[FontRef]*font.Face
	closed    bool
}

func newGoTextShaper(data []byte, size float64) (complexShaper, error) {
	if len(data) == 0 {
		return nil, errors.New("text: empty font data")
	}
	if size <= 0 || math.IsNaN(size) || math.IsInf(size, 0) {
		return nil, fmt.Errorf("text: invalid font size %v", size)
	}
	primary, err := parseGoTextFace(data, 0)
	if err != nil {
		return nil, err
	}
	return &goTextShaper{
		size:     size,
		primary:  primary,
		resolver: &fallbackResolver{},
	}, nil
}

// parseGoTextFace parses single-font data, or a collection when faceIndex is
// non-zero or the data is a TTC/OTC.
func parseGoTextFace(data []byte, faceIndex int) (*font.Face, error) {
	if faceIndex == 0 {
		if face, err := font.ParseTTF(bytes.NewReader(data)); err == nil {
			return face, nil
		}
	}
	faces, err := font.ParseTTC(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("text: parse font: %w", err)
	}
	if faceIndex < 0 || faceIndex >= len(faces) {
		return nil, fmt.Errorf("text: face index %d out of range", faceIndex)
	}
	return faces[faceIndex], nil
}

// fallbackFace resolves and caches the system font covering r. Failures are
// not cached so a later layout can retry.
func (s *goTextShaper) fallbackFace(r rune) *font.Face {
	ref, err := s.resolver.refForRune(r)
	if err != nil || ref.Path == "" {
		return nil
	}
	if s.fallbacks == nil {
		s.fallbacks = make(map[FontRef]*font.Face)
	}
	if face, ok := s.fallbacks[ref]; ok {
		return face
	}
	data, err := s.resolver.dataForPath(ref.Path)
	if err != nil {
		return nil
	}
	face, err := parseGoTextFace(data, ref.FaceIndex)
	if err != nil {
		return nil
	}
	s.fallbacks[ref] = face
	return face
}

// goTextFontMap resolves each rune to the primary font or a system fallback.
type goTextFontMap struct{ s *goTextShaper }

func (m goTextFontMap) ResolveFace(r rune) *font.Face {
	if _, ok := m.s.primary.NominalGlyph(r); ok {
		return m.s.primary
	}
	// On Windows the curated candidate list is fast and deterministic, so
	// try it first; elsewhere (or when it misses) fall back to a pure-Go
	// scan of the system font directories.
	if face := m.s.fallbackFace(r); face != nil {
		if _, ok := face.NominalGlyph(r); ok {
			return face
		}
	}
	if fontScanLookup != nil {
		if face := fontScanLookup(r); face != nil {
			return face
		}
	}
	return m.s.primary
}

// fontScanMap is the process-wide pure-Go system font index. Building it
// scans the OS font directories once; fontscan caches the result on disk.
var (
	fontScanOnce sync.Once
	fontScanMu   sync.Mutex
	fontScanMap  *fontscan.FontMap
)

func sharedFontScan() *fontscan.FontMap {
	fontScanOnce.Do(func() {
		fm := fontscan.NewFontMap(log.New(io.Discard, "", 0))
		cacheDir := ""
		if base, err := os.UserCacheDir(); err == nil {
			candidate := filepath.Join(base, "meltemi", "fontscan")
			if mkErr := os.MkdirAll(candidate, 0o755); mkErr == nil {
				cacheDir = candidate
			}
		}
		if err := fm.UseSystemFonts(cacheDir); err != nil {
			return
		}
		fontScanMap = fm
	})
	return fontScanMap
}

// fontScanResolve returns a system face covering r, or nil. The FontMap is
// not safe for concurrent use, so its caches are guarded while resolving.
func fontScanResolve(r rune) *font.Face {
	fm := sharedFontScan()
	if fm == nil {
		return nil
	}
	fontScanMu.Lock()
	defer fontScanMu.Unlock()
	face := fm.ResolveFace(r)
	if face == nil {
		return nil
	}
	if _, ok := face.NominalGlyph(r); !ok {
		return nil
	}
	return face
}

func (s *goTextShaper) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.fallbacks = nil
	s.primary = nil
	return nil
}

// goTextGlyph is one shaped glyph with its absolute rune cluster and resolved
// face, ready for pen assignment and rasterization.
type goTextGlyph struct {
	clusterStart, clusterEnd  int
	advance, xOffset, yOffset fixed.Int26_6
	penX                      fixed.Int26_6
	id                        font.GID
	face                      *font.Face
	outline                   font.GlyphOutline
	hasOutline                bool
	rect                      image.Rectangle // absolute line coordinates
}

// goTextRun is one segmented run: same face and direction.
type goTextRun struct {
	startRune, endRune int
	direction          Direction
	visualIndex        int32
	face               *font.Face
	lineBounds         shaping.Bounds
	glyphs             []goTextGlyph
	caretEdge          []fixed.Int26_6
}

type goTextEdgeEvent struct {
	at     int
	amount fixed.Int26_6
}

type goTextRef struct{ run, glyph int }

func goTextDirection(d di.Direction) Direction {
	if d == di.DirectionRTL {
		return DirectionRTL
	}
	return DirectionLTR
}

func validScale(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// layout shapes value at physical scaleX/scaleY and returns the full layout,
// preserving the LineLayout contract the HarfBuzz engine also fulfills.
func (s *goTextShaper) layout(value string, scaleX, scaleY float64) (LineLayout, error) {
	runes := []rune(value)
	layout := LineLayout{
		Direction:    DirectionLTR,
		CaretOffsets: make([]fixed.Int26_6, len(runes)+1),
	}
	if len(runes) == 0 {
		return layout, nil
	}
	if s.closed || s.primary == nil {
		return LineLayout{}, errors.New("text: layout on a closed shaper")
	}
	if !validScale(scaleX) || !validScale(scaleY) {
		return LineLayout{}, fmt.Errorf("text: invalid scale (%v, %v)", scaleX, scaleY)
	}
	physical := s.size * scaleY
	if physical <= 0 || math.IsNaN(physical) || math.IsInf(physical, 0) {
		return LineLayout{}, fmt.Errorf("text: invalid physical size %v", physical)
	}
	xRatio := scaleX / scaleY

	input := shaping.Input{
		Text:      runes,
		RunStart:  0,
		RunEnd:    len(runes),
		Direction: di.DirectionLTR,
		Face:      s.primary,
		Size:      fixed.Int26_6(math.Round(physical * 64)),
	}
	inputs := s.segmenter.Split(input, goTextFontMap{s})
	outputs := make([]shaping.Output, 0, len(inputs))
	for _, in := range inputs {
		outputs = append(outputs, s.shaper.Shape(in))
	}
	if len(outputs) == 0 {
		return layout, nil
	}

	runs := make([]goTextRun, 0, len(outputs))
	for _, out := range outputs {
		run := goTextRun{
			startRune:   out.Runes.Offset,
			endRune:     out.Runes.Offset + out.Runes.Count,
			direction:   goTextDirection(out.Direction),
			visualIndex: out.VisualIndex,
			face:        out.Face,
			lineBounds:  out.LineBounds,
		}
		if run.startRune < 0 {
			run.startRune = 0
		}
		if run.endRune > len(runes) {
			run.endRune = len(runes)
		}
		for _, g := range out.Glyphs {
			run.glyphs = append(run.glyphs, goTextGlyph{
				clusterStart: g.ClusterIndex,
				advance:      scaleXFixed(g.Advance, xRatio),
				xOffset:      scaleXFixed(g.XOffset, xRatio),
				yOffset:      g.YOffset,
				id:           g.GlyphID,
				face:         out.Face,
			})
		}
		goTextResolveClusterEnds(run.glyphs, run.startRune, run.endRune)
		runs = append(runs, run)
	}

	// Logical order (by rune start) drives direction, caret edges and
	// clusters. Visual order (VisualIndex) drives glyph pens and painting.
	logical := make([]int, len(runs))
	visual := make([]int, len(runs))
	for i := range runs {
		logical[i], visual[i] = i, i
	}
	sort.SliceStable(logical, func(a, b int) bool { return runs[logical[a]].startRune < runs[logical[b]].startRune })
	sort.SliceStable(visual, func(a, b int) bool { return runs[visual[a]].visualIndex < runs[visual[b]].visualIndex })

	layout.Direction = runs[logical[0]].direction

	pen := fixed.Int26_6(0)
	for _, ri := range visual {
		var edges []fixed.Int26_6
		edges, pen = goTextAssignRunPens(&runs[ri], pen)
		runs[ri].caretEdge = edges
	}
	for i := range runs {
		for b, edge := range runs[i].caretEdge {
			if idx := runs[i].startRune + b; idx >= 0 && idx < len(layout.CaretOffsets) {
				layout.CaretOffsets[idx] = edge
			}
		}
	}

	if err := s.rasterize(&layout, runes, runs, visual, physical, xRatio, pen); err != nil {
		return LineLayout{}, err
	}
	return layout, nil
}

// goTextResolveClusterEnds fills each glyph's cluster end with the smallest
// cluster start greater than its own, or endRune for the last cluster.
func goTextResolveClusterEnds(glyphs []goTextGlyph, startRune, endRune int) {
	if len(glyphs) == 0 {
		return
	}
	seen := make(map[int]bool, len(glyphs))
	starts := make([]int, 0, len(glyphs))
	for i := range glyphs {
		if glyphs[i].clusterStart < startRune {
			glyphs[i].clusterStart = startRune
		}
		if glyphs[i].clusterStart > endRune {
			glyphs[i].clusterStart = endRune
		}
		if !seen[glyphs[i].clusterStart] {
			seen[glyphs[i].clusterStart] = true
			starts = append(starts, glyphs[i].clusterStart)
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

// goTextAssignRunPens lays out one run starting at pen, returning its caret
// edges (absolute) and the pen after the run. RTL runs mirror only the caret
// edges, exactly like the HarfBuzz engine: glyphs stay in visual order with
// forward advances and the line grows rightward.
func goTextAssignRunPens(run *goTextRun, pen fixed.Int26_6) ([]fixed.Int26_6, fixed.Int26_6) {
	width := fixed.Int26_6(0)
	events := make([]goTextEdgeEvent, 0, len(run.glyphs))
	for _, g := range run.glyphs {
		width += g.advance
		events = append(events, goTextEdgeEvent{at: g.clusterEnd - run.startRune, amount: g.advance})
	}
	sort.Slice(events, func(a, b int) bool { return events[a].at < events[b].at })

	n := run.endRune - run.startRune
	if n < 0 {
		n = 0
	}
	edges := make([]fixed.Int26_6, n+1)
	ei, acc := 0, fixed.Int26_6(0)
	for b := 0; b <= n; b++ {
		for ei < len(events) && events[ei].at <= b {
			acc += events[ei].amount
			ei++
		}
		edges[b] = acc
	}
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
	return edges, pen + width
}

func (s *goTextShaper) rasterize(layout *LineLayout, runes []rune, runs []goTextRun, visual []int, physical, xRatio float64, total fixed.Int26_6) error {
	refs := make([]goTextRef, 0, len(runes)+len(runs))
	for _, ri := range visual {
		for gi := range runs[ri].glyphs {
			refs = append(refs, goTextRef{run: ri, glyph: gi})
		}
	}

	// Fetch each glyph's outline and compute its ink rect in absolute line
	// coordinates (y grows downward, baseline at y=0).
	x0, y0 := math.Inf(1), math.Inf(1)
	x1, y1 := math.Inf(-1), math.Inf(-1)
	note := func(minX, minY, maxX, maxY float64) {
		x0 = min(x0, minX)
		x1 = max(x1, maxX)
		y0 = min(y0, minY)
		y1 = max(y1, maxY)
	}
	note(0, 0, float64(total)/64, 0)
	for i := range runs {
		ascent := float64(runs[i].lineBounds.Ascent) / 64
		descent := float64(runs[i].lineBounds.Descent) / 64
		note(0, -ascent, 0, -descent)
	}

	for _, ref := range refs {
		run := &runs[ref.run]
		g := &run.glyphs[ref.glyph]
		outline, ok := run.face.GlyphDataOutline(g.id)
		if !ok {
			if data := run.face.GlyphData(g.id); data != nil {
				switch d := data.(type) {
				case font.GlyphOutline:
					outline, ok = d, true
				case font.GlyphBitmap:
					if d.Outline != nil {
						outline, ok = *d.Outline, true
					}
				case font.GlyphSVG:
					outline, ok = d.Outline, true
				}
			}
		}
		if !ok {
			continue
		}
		g.outline, g.hasOutline = outline, true
		if rect, hasInk := goTextOutlineRect(g, physical, xRatio); hasInk {
			g.rect = rect
			note(float64(rect.Min.X), float64(rect.Min.Y), float64(rect.Max.X), float64(rect.Max.Y))
		}
	}

	if math.IsInf(x0, 1) {
		x0, x1, y0, y1 = 0, float64(total)/64, 0, 0
	}
	ix0, iy0 := int(math.Floor(x0)), int(math.Floor(y0))
	ix1, iy1 := int(math.Ceil(x1)), int(math.Ceil(y1))
	width, height := ix1-ix0, iy1-iy0

	var img *image.Alpha
	var raster *vector.Rasterizer
	if width > 0 && height > 0 {
		img = image.NewAlpha(image.Rect(0, 0, width, height))
		raster = vector.NewRasterizer(width, height)
		raster.DrawOp = draw.Over
	}

	visualIndex := make(map[goTextRef]int, len(refs))
	for _, ref := range refs {
		run := &runs[ref.run]
		g := &run.glyphs[ref.glyph]
		entry := Glyph{Rune: runes[g.clusterStart], Advance: g.advance}
		if raster != nil && g.hasOutline && !g.rect.Empty() {
			goTextAddOutline(raster, g, physical, xRatio, ix0, iy0)
			entry.X = g.rect.Min.X - ix0
			entry.Y = g.rect.Min.Y - iy0
			entry.Width = g.rect.Dx()
			entry.Height = g.rect.Dy()
		}
		visualIndex[ref] = len(layout.Glyphs)
		layout.Glyphs = append(layout.Glyphs, entry)
	}
	if raster != nil {
		raster.Draw(img, img.Bounds(), image.White, image.Point{})
	}

	s.fillBitmapGlyphs(layout, runes, runs, refs, ix0, iy0)
	s.fillClusters(layout, runs, visualIndex)
	s.noteMissingRunes(layout, runes, runs)

	scaleY := physical / s.size
	scaleX := scaleY * xRatio
	if img != nil {
		layout.Bitmap.Width = width
		layout.Bitmap.Height = height
		layout.Bitmap.Stride = img.Stride
		layout.Bitmap.OriginX = -ix0
		layout.Bitmap.Alpha = img.Pix
	}
	layout.Bitmap.LogicalWidth = float64(width) / scaleX
	layout.Bitmap.LogicalHeight = float64(height) / scaleY
	layout.Bitmap.LogicalOriginX = float64(-ix0) / scaleX
	return nil
}

// goTextOutlineRect returns the ink rect of g in absolute line coordinates.
func goTextOutlineRect(g *goTextGlyph, physical, xRatio float64) (image.Rectangle, bool) {
	upem := float64(g.face.Upem())
	if upem == 0 || len(g.outline.Segments) == 0 {
		return image.Rectangle{}, false
	}
	scaleY := physical / upem
	scaleXf := scaleY * xRatio
	penX := float64(g.penX)/64 + float64(g.xOffset)/64
	offY := float64(g.yOffset) / 64
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, seg := range g.outline.Segments {
		for _, p := range seg.Args {
			x := penX + float64(p.X)*scaleXf
			y := -(offY + float64(p.Y)*scaleY)
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
		}
	}
	if math.IsInf(minX, 1) {
		return image.Rectangle{}, false
	}
	return image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY))), true
}

// goTextAddOutline feeds one glyph's transformed outline into the rasterizer.
func goTextAddOutline(raster *vector.Rasterizer, g *goTextGlyph, physical, xRatio float64, x0, y0 int) {
	upem := float64(g.face.Upem())
	scaleY := physical / upem
	scaleXf := scaleY * xRatio
	penX := float64(g.penX)/64 + float64(g.xOffset)/64
	offY := float64(g.yOffset) / 64
	dx, dy := float64(x0), float64(y0)
	open := false
	for _, seg := range g.outline.Segments {
		x := func(p ot.SegmentPoint) float32 { return float32(penX + float64(p.X)*scaleXf - dx) }
		y := func(p ot.SegmentPoint) float32 { return float32(-(offY + float64(p.Y)*scaleY) - dy) }
		switch seg.Op {
		case ot.SegmentOpMoveTo:
			if open {
				raster.ClosePath()
			}
			raster.MoveTo(x(seg.Args[0]), y(seg.Args[0]))
			open = true
		case ot.SegmentOpLineTo:
			raster.LineTo(x(seg.Args[0]), y(seg.Args[0]))
		case ot.SegmentOpQuadTo:
			raster.QuadTo(x(seg.Args[0]), y(seg.Args[0]), x(seg.Args[1]), y(seg.Args[1]))
		case ot.SegmentOpCubeTo:
			raster.CubeTo(x(seg.Args[0]), y(seg.Args[0]), x(seg.Args[1]), y(seg.Args[1]), x(seg.Args[2]), y(seg.Args[2]))
		}
	}
	if open {
		raster.ClosePath()
	}
}

// fillBitmapGlyphs builds the legacy per-rune view: the first rune of each
// cluster carries the whole cluster advance and ink, later runes carry zero.
func (s *goTextShaper) fillBitmapGlyphs(layout *LineLayout, runes []rune, runs []goTextRun, refs []goTextRef, x0, y0 int) {
	clusterAdvance := make(map[int]fixed.Int26_6)
	clusterRect := make(map[int]image.Rectangle)
	clusterHasInk := make(map[int]bool)
	for _, ref := range refs {
		g := &runs[ref.run].glyphs[ref.glyph]
		clusterAdvance[g.clusterStart] += g.advance
		if !g.rect.Empty() {
			if r, ok := clusterRect[g.clusterStart]; ok {
				clusterRect[g.clusterStart] = r.Union(g.rect)
			} else {
				clusterRect[g.clusterStart] = g.rect
			}
			clusterHasInk[g.clusterStart] = true
		}
	}
	layout.Bitmap.Glyphs = make([]Glyph, len(runes))
	for i := range runes {
		layout.Bitmap.Glyphs[i].Rune = runes[i]
	}
	for _, ref := range refs {
		g := &runs[ref.run].glyphs[ref.glyph]
		for r := g.clusterStart; r < g.clusterEnd && r < len(runes); r++ {
			if r < 0 {
				continue
			}
			entry := layout.Bitmap.Glyphs[r]
			entry.Rune = runes[r]
			if r == g.clusterStart {
				entry.Advance = clusterAdvance[g.clusterStart]
			}
			if clusterHasInk[g.clusterStart] {
				rect := clusterRect[g.clusterStart]
				entry.X, entry.Y = rect.Min.X-x0, rect.Min.Y-y0
				entry.Width, entry.Height = rect.Dx(), rect.Dy()
			}
			layout.Bitmap.Glyphs[r] = entry
		}
	}
}

// fillClusters builds logical-order clusters referencing visual glyph indices.
func (s *goTextShaper) fillClusters(layout *LineLayout, runs []goTextRun, visualIndex map[goTextRef]int) {
	logical := make([]int, len(runs))
	for i := range runs {
		logical[i] = i
	}
	sort.SliceStable(logical, func(a, b int) bool { return runs[logical[a]].startRune < runs[logical[b]].startRune })
	for _, ri := range logical {
		run := &runs[ri]
		if len(run.glyphs) == 0 {
			continue
		}
		lg := make([]int, len(run.glyphs))
		for i := range lg {
			lg[i] = i
		}
		if run.direction == DirectionRTL {
			for a, b := 0, len(lg)-1; a < b; a, b = a+1, b-1 {
				lg[a], lg[b] = lg[b], lg[a]
			}
		}
		groupStart := 0
		for pos := 1; pos <= len(lg); pos++ {
			if pos == len(lg) || run.glyphs[lg[pos]].clusterStart != run.glyphs[lg[groupStart]].clusterStart {
				first := run.glyphs[lg[groupStart]]
				startGlyph, endGlyph := -1, 0
				for _, k := range lg[groupStart:pos] {
					idx := visualIndex[goTextRef{run: ri, glyph: k}]
					if startGlyph < 0 || idx < startGlyph {
						startGlyph = idx
					}
					if idx+1 > endGlyph {
						endGlyph = idx + 1
					}
				}
				e0 := fixed.Int26_6(0)
				e1 := fixed.Int26_6(0)
				if first.clusterStart >= 0 && first.clusterStart < len(layout.CaretOffsets) {
					e0 = layout.CaretOffsets[first.clusterStart]
				}
				if first.clusterEnd >= 0 && first.clusterEnd < len(layout.CaretOffsets) {
					e1 = layout.CaretOffsets[first.clusterEnd]
				}
				layout.Clusters = append(layout.Clusters, Cluster{
					StartRune:  first.clusterStart,
					EndRune:    first.clusterEnd,
					StartGlyph: startGlyph,
					EndGlyph:   endGlyph,
					X0:         min(e0, e1),
					X1:         max(e0, e1),
				})
				groupStart = pos
			}
		}
	}
}

// noteMissingRunes records runes no resolved face covers; they drew notdef.
func (s *goTextShaper) noteMissingRunes(layout *LineLayout, runes []rune, runs []goTextRun) {
	for i := range runs {
		run := &runs[i]
		for r := run.startRune; r < run.endRune && r < len(runes); r++ {
			if r < 0 {
				continue
			}
			if _, ok := run.face.NominalGlyph(runes[r]); !ok {
				layout.noteMissing(runes[r])
			}
		}
	}
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
