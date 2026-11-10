package ui

import (
	"math"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

const (
	textAreaPaddingY           = 8.0
	textAreaFallbackLineHeight = 18.0
)

type textAreaLineMetrics struct {
	bitmap     text.Bitmap
	offsets    []float64
	boundaries []int
}

type textAreaMetrics struct {
	source     string
	lines      []textAreaLineMetrics
	lineHeight float64
}

// textMetrics shapes every line once through Face.Layout, deriving each
// line's bitmap, caret offsets and editable boundaries from the same layout
// so drawing, hit-testing and caret placement agree. The cache is keyed by
// the buffer value, so an edit between frames recomputes instead of reading
// stale geometry.
func (t *TextAreaWidget) textMetrics() *textAreaMetrics {
	value := t.buffer.Value()
	if t.metrics != nil && t.metrics.source == value {
		return t.metrics
	}
	lines := t.buffer.Lines()
	metrics := &textAreaMetrics{
		source:     value,
		lines:      make([]textAreaLineMetrics, len(lines)),
		lineHeight: textAreaFallbackLineHeight,
	}
	if t.face != nil {
		if reference, err := t.face.Rasterize(" "); err == nil && reference.Height > 0 {
			metrics.lineHeight = float64(reference.Height)
		}
	}
	for index, line := range lines {
		runes := []rune(line)
		metric := textAreaLineMetrics{
			offsets:    make([]float64, len(runes)+1),
			boundaries: graphemeBoundaries(runes),
		}
		if t.face != nil {
			if layout, err := t.face.Layout(line); err == nil {
				metric.bitmap = layout.Bitmap
				for offsetIndex, offset := range layout.CaretOffsets {
					metric.offsets[offsetIndex] = float64(offset) / 64
				}
				metric.boundaries = shapedTextBoundaries(runes, layout.Clusters)
			}
		}
		metrics.lines[index] = metric
	}
	t.metrics = metrics
	return metrics
}

// editableBoundaries returns the editable boundaries of the whole buffer:
// every line's shaped refinement translated to buffer rune offsets. Line
// starts and ends are always boundaries (a newline splits clusters), so the
// merged list stays sorted and complete.
func (t *TextAreaWidget) editableBoundaries() []int {
	metrics := t.textMetrics()
	spans := t.buffer.lineSpans()
	if len(metrics.lines) != len(spans) {
		return nil
	}
	boundaries := make([]int, 0, len(spans)+len(metrics.lines))
	for index, line := range metrics.lines {
		for _, boundary := range line.boundaries {
			boundaries = append(boundaries, spans[index].start+boundary)
		}
	}
	return boundaries
}

func (t *TextAreaWidget) lineHeight() float64 {
	return t.textMetrics().lineHeight
}

// pageLineCount is the number of fully visible text lines in the viewport,
// the distance PageUp/PageDown walk; it floors at one line so a degenerate
// viewport still moves the caret.
func (t *TextAreaWidget) pageLineCount() int {
	lineHeight := t.lineHeight()
	if lineHeight <= 0 {
		return 1
	}
	return max(1, int(t.contentRect().Height/lineHeight))
}

func (t *TextAreaWidget) componentStyleWithFocus(style componentStyle) componentStyle {
	if t.focused && t.enabled && !style.borderStyled && style.borderRadius > 0 {
		style.borderColor = defaultTheme.focusBorder
		style.borderWidth = focusBorderWidth
	}
	return style
}

func (t *TextAreaWidget) currentComponentStyle() componentStyle {
	return t.componentStyleWithFocus(textAreaComponentStyle(t, t.resolved))
}

func (t *TextAreaWidget) contentRect() render.Rect {
	return styleContentRect(t.bounds, t.currentComponentStyle())
}

func (t *TextAreaWidget) updateScroll() {
	t.updateScrollWithStyle(t.currentComponentStyle())
}

func (t *TextAreaWidget) caretMetrics(line int) ([]float64, int) {
	lineMetrics := t.textMetrics().lines[line]
	if t.composition.active {
		value, _, _, caret, _ := t.composedLine(line)
		_, offsets, boundaries := compositionTextMetrics(t.face, value)
		caret = clampTextIndex(caret, len([]rune(value)))
		return offsets, snapToTextBoundary(boundaries, caret)
	}
	column := clampTextIndex(t.buffer.Column(t.buffer.cursor), len(lineMetrics.offsets)-1)
	return lineMetrics.offsets, snapToTextBoundary(lineMetrics.boundaries, column)
}

func (t *TextAreaWidget) updateScrollWithStyle(style componentStyle) {
	if t.buffer == nil {
		return
	}
	metrics := t.textMetrics()
	content := styleContentRect(t.bounds, style)
	line := t.buffer.LineIndex(t.buffer.cursor)
	if content.Height <= 0 || metrics.lineHeight <= 0 {
		t.scrollY = 0
	} else {
		visibleLines := max(1, int(content.Height/metrics.lineHeight))
		maxScroll := max(0, len(metrics.lines)-visibleLines)
		t.scrollY = min(max(t.scrollY, 0), float64(maxScroll))
		if line < int(t.scrollY) {
			t.scrollY = float64(line)
		}
		if line >= int(t.scrollY)+visibleLines {
			t.scrollY = float64(line - visibleLines + 1)
		}
		t.scrollY = min(max(t.scrollY, 0), float64(maxScroll))
	}

	if content.Width <= 0 {
		t.scrollX = 0
		return
	}
	offsets, column := t.caretMetrics(line)
	caret := offsets[column]
	// See the Input: the extent is the maximum caret offset (the
	// rightmost caret), which equals offsets[len-1] for LTR lines but
	// keeps pure RTL lines scrollable, where the final offset is 0.
	extent := 0.0
	for _, offset := range offsets {
		extent = max(extent, offset)
	}
	maxScroll := max(0, extent-content.Width+1)
	t.scrollX = min(max(t.scrollX, 0), maxScroll)
	if caret < t.scrollX {
		t.scrollX = caret
	}
	if caret-t.scrollX > content.Width-1 {
		t.scrollX = caret - content.Width + 1
	}
	t.scrollX = min(max(t.scrollX, 0), maxScroll)
}

func (t *TextAreaWidget) caretRect() render.Rect {
	return t.caretRectWithStyle(t.currentComponentStyle())
}

func (t *TextAreaWidget) caretRectWithStyle(style componentStyle) render.Rect {
	metrics := t.textMetrics()
	line := t.buffer.LineIndex(t.buffer.cursor)
	offsets, column := t.caretMetrics(line)
	content := styleContentRect(t.bounds, style)
	return render.Rect{
		X:      content.X + offsets[column] - t.scrollX,
		Y:      content.Y + (float64(line)-t.scrollY)*metrics.lineHeight,
		Width:  1,
		Height: metrics.lineHeight,
	}
}

// runeIndexAt picks the line from the vertical position and maps the
// horizontal position to the editable boundary nearest in visual offset
// space, so clicking can never park the caret inside a grapheme or shaped
// cluster on any line. Without a face the text measures as empty and every
// click lands at the end of its line, matching the empty-metrics drawing.
func (t *TextAreaWidget) runeIndexAt(point driver.Point) int {
	metrics := t.textMetrics()
	content := t.contentRect()
	line := int(t.scrollY)
	if metrics.lineHeight > 0 {
		line = int(math.Floor((point.Y-content.Y)/metrics.lineHeight + t.scrollY))
	}
	line = clampTextIndex(line, len(metrics.lines)-1)
	if t.face == nil {
		return t.buffer.LineEnd(line)
	}
	lineMetrics := metrics.lines[line]
	if len(lineMetrics.offsets) <= 1 {
		return t.buffer.LineStart(line)
	}
	x := point.X - content.X + t.scrollX
	column := nearestTextBoundary(lineMetrics.offsets, lineMetrics.boundaries, x)
	return t.buffer.LineStart(line) + column
}

func (t *TextAreaWidget) selectionRects() []render.Rect {
	return t.selectionRectsWithStyle(t.currentComponentStyle())
}

func (t *TextAreaWidget) selectionRectsWithStyle(style componentStyle) []render.Rect {
	start, end, ok := t.buffer.Selection()
	if !ok {
		return nil
	}
	metrics := t.textMetrics()
	spans := t.buffer.lineSpans()
	content := styleContentRect(t.bounds, style)
	var rects []render.Rect
	for line, span := range spans {
		selectedStart := max(start, span.start)
		selectedEnd := min(end, span.end)
		if selectedStart >= selectedEnd {
			continue
		}
		startColumn := selectedStart - span.start
		endColumn := selectedEnd - span.start
		offsets := metrics.lines[line].offsets
		// Logical columns map through caret offsets that decrease inside
		// RTL runs, so the rectangle takes the smaller offset as its left
		// edge and the absolute difference as its width.
		startX := offsets[startColumn]
		endX := offsets[endColumn]
		rects = append(rects, render.Rect{
			X:      content.X + math.Min(startX, endX) - t.scrollX,
			Y:      content.Y + (float64(line)-t.scrollY)*metrics.lineHeight,
			Width:  math.Abs(endX - startX),
			Height: metrics.lineHeight,
		})
	}
	return rects
}

func (t *TextAreaWidget) draw(ctx *drawContext) {
	t.drawStyled(ctx, unresolvedStyle())
}

func (t *TextAreaWidget) drawWithStyle(ctx *drawContext, style componentStyle) {
	style = t.componentStyleWithFocus(style)
	t.updateScrollWithStyle(style)
	drawComponentBox(ctx, t.bounds, style)

	content := styleContentRect(t.bounds, style)
	ctx.SetClip(content)
	for _, selection := range t.selectionRectsWithStyle(style) {
		if selection.Width > 0 {
			ctx.DrawRect(selection, style.selection)
		}
	}

	metrics := t.textMetrics()
	if t.buffer.Value() == "" && t.placeholder != "" && !t.composition.active {
		bitmap := text.Bitmap{}
		if t.face != nil {
			if placeholder, err := t.face.Rasterize(t.placeholder); err == nil {
				bitmap = placeholder
			}
		}
		if bitmap.Width != 0 || bitmap.Height != 0 {
			ctx.DrawText(t.placeholder, driver.Point{
				X: content.X - float64(bitmap.OriginX),
				Y: content.Y,
			}, defaultTheme.inputPlaceholder, t.face)
		}
	} else {
		for line, value := range t.buffer.Lines() {
			metric := metrics.lines[line]
			compositionStart, compositionEnd := 0, 0
			if t.composition.active {
				composed, start, end, _, active := t.composedLine(line)
				if active {
					value = composed
					compositionStart, compositionEnd = start, end
					metric.bitmap, metric.offsets, _ = compositionTextMetrics(t.face, value)
				}
			}
			lineY := content.Y + (float64(line)-t.scrollY)*metrics.lineHeight
			if value != "" && (metric.bitmap.Width != 0 || metric.bitmap.Height != 0) {
				ctx.DrawText(value, driver.Point{
					X: content.X - t.scrollX - float64(metric.bitmap.OriginX),
					Y: lineY,
				}, style.text, t.face)
			}
			if compositionEnd > compositionStart {
				// Caret offsets decrease through RTL runs, so the
				// underline uses the same min/absolute geometry as
				// selections.
				startX := metric.offsets[compositionStart]
				endX := metric.offsets[compositionEnd]
				ctx.DrawRect(render.Rect{
					X:      content.X + math.Min(startX, endX) - t.scrollX,
					Y:      lineY + metrics.lineHeight - 2,
					Width:  math.Abs(endX - startX),
					Height: 1,
				}, defaultTheme.focusBorder)
			}
		}
	}
	if t.focused && t.enabled {
		ctx.DrawRect(t.caretRectWithStyle(style), style.text)
	}
	ctx.ClearClip()

	if t.focused && t.enabled && !style.borderStyled && style.borderRadius <= 0 {
		for _, strip := range focusStrips(t.bounds, focusBorderWidth) {
			ctx.DrawRect(strip, defaultTheme.focusBorder)
		}
	}
}
