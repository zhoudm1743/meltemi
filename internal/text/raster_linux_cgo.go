//go:build linux && cgo

package text

// FreeType glyph-ID rasterization and bitmap assembly for the HarfBuzz
// engine. Shaped glyphs are loaded with FT_LOAD_RENDER (hinted gray
// coverage) and blitted into the line bitmap in visual paint order.

/*
#cgo pkg-config: freetype2
#include <stdlib.h>
#include <ft2build.h>
#include FT_FREETYPE_H
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"

	"golang.org/x/image/math/fixed"
)

// rasterBitmap is a glyph's rendered coverage plus its integer offsets from
// the pen origin: left of the ink relative to the pen, top of the ink above
// the baseline. pixels holds width*height gray bytes, rows top to bottom.
type rasterBitmap struct {
	left, top     int
	width, height int
	pixels        []byte
}

// rasterGlyph renders one glyph ID at the engine's current size. A glyph
// with no outline returns a zero bitmap; a glyph FreeType can only render in
// an unsupported pixel mode (e.g. color fonts) returns an error so the
// caller can record the rune as missing.
func (e *hbEngine) rasterGlyph(id uint32) (rasterBitmap, error) {
	if C.FT_Load_Glyph(e.face, C.FT_UInt(id), C.FT_LOAD_DEFAULT|C.FT_LOAD_RENDER) != 0 {
		return rasterBitmap{}, fmt.Errorf("text: freetype cannot load glyph %d", id)
	}
	slot := e.face.glyph
	bm := slot.bitmap
	rb := rasterBitmap{
		left:   int(slot.bitmap_left),
		top:    int(slot.bitmap_top),
		width:  int(bm.width),
		height: int(bm.rows),
	}
	if rb.width == 0 || rb.height == 0 {
		return rb, nil
	}
	if bm.pixel_mode != C.FT_PIXEL_MODE_GRAY {
		return rasterBitmap{}, fmt.Errorf("text: glyph %d renders as unsupported pixel mode %d", id, int(bm.pixel_mode))
	}
	pitch := int(bm.pitch)
	if pitch < 0 {
		return rasterBitmap{}, fmt.Errorf("text: glyph %d has a negative bitmap pitch", id)
	}
	rb.pixels = make([]byte, rb.width*rb.height)
	for y := 0; y < rb.height; y++ {
		row := unsafe.Add(unsafe.Pointer(bm.buffer), y*pitch)
		copy(rb.pixels[y*rb.width:(y+1)*rb.width], unsafe.Slice((*byte)(row), rb.width))
	}
	return rb, nil
}

// blit composites one gray glyph into the alpha mask with coverage-over so
// overlapping inks (marks over bases) stay opaque.
func blit(dst *image.Alpha, dx, dy int, src rasterBitmap) {
	bounds := dst.Bounds()
	for y := 0; y < src.height; y++ {
		py := dy + y
		if py < bounds.Min.Y || py >= bounds.Max.Y {
			continue
		}
		for x := 0; x < src.width; x++ {
			px := dx + x
			if px < bounds.Min.X || px >= bounds.Max.X {
				continue
			}
			s := src.pixels[y*src.width+x]
			if s == 0 {
				continue
			}
			di := dst.PixOffset(px, py)
			d := dst.Pix[di]
			dst.Pix[di] = d + s - d*s/255
		}
	}
}

// rasterize renders every shaped glyph, sizes and paints the line bitmap,
// and fills the layout's visual Glyphs, logical Clusters and the per-rune
// Bitmap.Glyphs. It runs after assignPens, so pens and caret edges are
// final. A glyph that fails to rasterize keeps its advance and records its
// runes as missing; blank space glyphs keep zero ink rects.
func (s *hbShaper) rasterize(layout *LineLayout, runes []rune, runs []shapedRun, primary *hbEngine, scaleX, scaleY float64) error {
	// The line box always covers the primary font's ascent/descent so blank
	// glyphs (spaces) keep the line height, exactly like the legacy box.
	ascent, descent := primary.lineBox()
	x0, x1 := 0, 0
	y0, y1 := -ascent.Ceil(), descent.Ceil()

	// Load every glyph's coverage and union the ink box in line
	// coordinates. A raster failure keeps the advance and records the
	// runes as missing.
	for i := range runs {
		run := &runs[i]
		for k := range run.glyphs {
			g := &run.glyphs[k]
			bm, err := g.eng.rasterGlyph(g.id)
			if err != nil {
				for r := g.clusterStart; r < g.clusterEnd && r < len(runes); r++ {
					layout.noteMissing(runes[r])
				}
				continue
			}
			g.ink = bm
			if bm.width == 0 || bm.height == 0 {
				continue
			}
			ix := (g.penX + g.xOffset + fixed.I(bm.left)).Round()
			// HarfBuzz y_offset is y-up while bitmap rows are y-down, so
			// the ink top lands at -yOffset - top.
			iy := (-g.yOffset - fixed.I(bm.top)).Round()
			x0 = min(x0, ix)
			x1 = max(x1, ix+bm.width)
			y0 = min(y0, iy)
			y1 = max(y1, iy+bm.height)
		}
	}

	total := fixed.Int26_6(0)
	for i := range runs {
		total += runWidth(&runs[i])
	}
	x1 = max(x1, total.Ceil())

	width := x1 - x0
	height := y1 - y0
	var img *image.Alpha
	if width > 0 && height > 0 {
		img = image.NewAlpha(image.Rect(0, 0, width, height))
	}

	// Paint in visual order and record the visual glyph list.
	layout.Glyphs = make([]Glyph, 0, len(runes))
	visualIndex := make(map[[2]int]int, len(runes))
	for i := range runs {
		run := &runs[i]
		for k := range run.glyphs {
			g := &run.glyphs[k]
			visualIndex[[2]int{i, k}] = len(layout.Glyphs)
			entry := Glyph{Rune: runes[g.clusterStart], Advance: g.advance}
			if img != nil && g.ink.width > 0 && g.ink.height > 0 {
				ix := (g.penX + g.xOffset + fixed.I(g.ink.left)).Round() - x0
				iy := (-g.yOffset - fixed.I(g.ink.top)).Round() - y0
				blit(img, ix, iy, g.ink)
				entry.X, entry.Y = ix, iy
				entry.Width, entry.Height = g.ink.width, g.ink.height
			}
			layout.Glyphs = append(layout.Glyphs, entry)
		}
	}

	// Bitmap.Glyphs: the legacy per-rune view. Every rune gets an entry;
	// runes after the first of a cluster share the ink rect and carry zero
	// advance, so the advances still sum to the line advance.
	layout.Bitmap.Glyphs = make([]Glyph, len(runes))
	for i := range runs {
		run := &runs[i]
		for k := range run.glyphs {
			g := &run.glyphs[k]
			ink := Glyph{Rune: runes[g.clusterStart], Advance: g.advance}
			if img != nil && g.ink.width > 0 && g.ink.height > 0 {
				ink.X = (g.penX + g.xOffset + fixed.I(g.ink.left)).Round() - x0
				ink.Y = (-g.yOffset - fixed.I(g.ink.top)).Round() - y0
				ink.Width, ink.Height = g.ink.width, g.ink.height
			}
			for r := g.clusterStart; r < g.clusterEnd && r < len(runes); r++ {
				entry := ink
				entry.Rune = runes[r]
				if r != g.clusterStart {
					entry.Advance = 0
				}
				layout.Bitmap.Glyphs[r] = entry
			}
		}
	}

	// Clusters in logical order: runs are in logical order, and within an
	// RTL run the logical cluster order is the reverse of the paint order.
	// StartGlyph/EndGlyph index the visual Glyphs slice.
	layout.Clusters = make([]Cluster, 0, len(runes))
	for i := range runs {
		run := &runs[i]
		logical := make([]int, len(run.glyphs))
		for k := range logical {
			logical[k] = k
		}
		if run.direction == DirectionRTL {
			for a, b := 0, len(logical)-1; a < b; a, b = a+1, b-1 {
				logical[a], logical[b] = logical[b], logical[a]
			}
		}
		groupStart := 0
		for pos := 1; pos <= len(logical); pos++ {
			if pos == len(logical) || run.glyphs[logical[pos]].clusterStart != run.glyphs[logical[groupStart]].clusterStart {
				first := run.glyphs[logical[groupStart]]
				startGlyph, endGlyph := len(layout.Glyphs), 0
				for _, k := range logical[groupStart:pos] {
					idx := visualIndex[[2]int{i, k}]
					startGlyph = min(startGlyph, idx)
					endGlyph = max(endGlyph, idx+1)
				}
				edge0 := layout.CaretOffsets[first.clusterStart]
				edge1 := layout.CaretOffsets[first.clusterEnd]
				layout.Clusters = append(layout.Clusters, Cluster{
					StartRune:  first.clusterStart,
					EndRune:    first.clusterEnd,
					StartGlyph: startGlyph,
					EndGlyph:   endGlyph,
					X0:         min(edge0, edge1),
					X1:         max(edge0, edge1),
				})
				groupStart = pos
			}
		}
	}

	// Bitmap fields: physical geometry plus the logical draw geometry a
	// renderer places the quad with.
	if img != nil {
		layout.Bitmap.Width = width
		layout.Bitmap.Height = height
		layout.Bitmap.Stride = img.Stride
		layout.Bitmap.OriginX = -x0
		layout.Bitmap.Alpha = img.Pix
	}
	layout.Bitmap.LogicalWidth = float64(width) / scaleX
	layout.Bitmap.LogicalHeight = float64(height) / scaleY
	layout.Bitmap.LogicalOriginX = float64(-x0) / scaleX
	return nil
}

// runWidth is the sum of the shaped advances of one run.
func runWidth(run *shapedRun) fixed.Int26_6 {
	var width fixed.Int26_6
	for k := range run.glyphs {
		width += run.glyphs[k].advance
	}
	return width
}
