package ui

import (
	"math"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

const (
	inputPaddingX    = 8.0
	inputBorderWidth = 1.0
)

var (
	inputBackgroundColor         = color(1, 1, 1)
	inputDisabledBackgroundColor = color(0.96, 0.96, 0.96)
	inputBorderColor             = color(0.70, 0.70, 0.70)
	inputPlaceholderColor        = color(0.55, 0.55, 0.55)
	inputSelectionColor          = render.Color{R: 0.25, G: 0.47, B: 0.85, A: 0.25}
	inputCaretColor              = color(0.13, 0.13, 0.13)
)

type inputMetrics struct {
	source     string
	bitmap     text.Bitmap
	offsets    []float64
	boundaries []int
}

// textMetrics shapes the display value once through Face.Layout and derives
// the bitmap, the caret offsets and the editable boundaries from that single
// layout, so drawing, hit-testing and caret placement can never disagree.
// The cache is keyed by the display value itself, so an edit between frames
// recomputes instead of reading stale geometry; with no face (or a layout
// failure) the offsets stay zero and the boundaries fall back to the
// grapheme subset, which keeps editing safe.
func (i *InputWidget) textMetrics() *inputMetrics {
	value := i.displayValue()
	if i.metrics != nil && i.metrics.source == value {
		return i.metrics
	}
	runes := []rune(value)
	metrics := &inputMetrics{
		source:     value,
		offsets:    make([]float64, len(runes)+1),
		boundaries: graphemeBoundaries(runes),
	}
	if i.face != nil {
		if layout, err := i.face.Layout(value); err == nil {
			metrics.bitmap = layout.Bitmap
			for index, offset := range layout.CaretOffsets {
				metrics.offsets[index] = float64(offset) / 64
			}
			metrics.boundaries = shapedTextBoundaries(runes, layout.Clusters)
		}
	}
	i.metrics = metrics
	return metrics
}

// editableBoundaries returns the editable boundaries of the buffer content.
// The mask is one rune per buffer rune, so its shaped clusters impose no
// restriction and the grapheme rules of the real text decide; a regular
// input's display value is the buffer value, so its shaped refinement
// applies directly.
func (i *InputWidget) editableBoundaries() []int {
	if i.password {
		return graphemeBoundaries(i.buffer.runes)
	}
	return i.textMetrics().boundaries
}

func (i *InputWidget) componentStyleWithFocus(style componentStyle) componentStyle {
	if i.focused && i.enabled && !style.borderStyled && style.borderRadius > 0 {
		style.borderColor = defaultTheme.focusBorder
		style.borderWidth = focusBorderWidth
	}
	return style
}

func (i *InputWidget) currentComponentStyle() componentStyle {
	return i.componentStyleWithFocus(inputComponentStyle(i, i.resolved))
}

func (i *InputWidget) contentRect() render.Rect {
	return styleContentRect(i.bounds, i.currentComponentStyle())
}

func (i *InputWidget) updateScroll() {
	i.updateScrollWithStyle(i.currentComponentStyle())
}

func (i *InputWidget) caretMetrics() ([]float64, int) {
	if i.composition.active {
		value, _, _, caret, _ := i.composedDisplay()
		_, offsets, boundaries := compositionTextMetrics(i.face, value)
		caret = clampTextIndex(caret, len([]rune(value)))
		return offsets, snapToTextBoundary(boundaries, caret)
	}
	metrics := i.textMetrics()
	cursor := clampTextIndex(i.buffer.cursor, len(metrics.offsets)-1)
	return metrics.offsets, snapToTextBoundary(metrics.boundaries, cursor)
}

func (i *InputWidget) updateScrollWithStyle(style componentStyle) {
	if i.buffer == nil {
		return
	}
	offsets, cursor := i.caretMetrics()
	width := styleContentRect(i.bounds, style).Width
	if width <= 0 {
		i.scrollX = 0
		return
	}
	caret := offsets[cursor]
	// The scrollable extent is the rightmost caret position (the maximum
	// offset), not offsets[len-1]: for a pure RTL line the final offset is
	// 0 while the line still grows rightward, and an offsets[len-1]-based
	// extent would pin scrollX to 0 and clip the logical-start caret.
	// For LTR lines the maximum is exactly offsets[len-1], so behavior
	// there is unchanged.
	extent := 0.0
	for _, offset := range offsets {
		extent = max(extent, offset)
	}
	maxScroll := max(0, extent-width+1)
	i.scrollX = min(max(i.scrollX, 0), maxScroll)
	if caret < i.scrollX {
		i.scrollX = caret
	}
	if caret-i.scrollX > width-1 {
		i.scrollX = caret - width + 1
	}
	i.scrollX = min(max(i.scrollX, 0), maxScroll)
}

func (i *InputWidget) caretX() float64 {
	return i.caretXWithStyle(i.currentComponentStyle())
}

func (i *InputWidget) caretXWithStyle(style componentStyle) float64 {
	offsets, cursor := i.caretMetrics()
	return styleContentRect(i.bounds, style).X + offsets[cursor] - i.scrollX
}

// runeIndexAt maps a pointer position to the editable boundary nearest in
// visual offset space, so clicking can never park the caret inside a
// grapheme or shaped cluster; in RTL runs the nearest boundary by visual
// offset is still a logical boundary, because the caret offsets encode the
// visual direction. Without a face the text measures as empty and every
// click lands at the end of the buffer, matching the empty-metrics drawing.
func (i *InputWidget) runeIndexAt(point driver.Point) int {
	if i.face == nil {
		return len(i.buffer.runes)
	}
	metrics := i.textMetrics()
	if len(metrics.offsets) <= 1 {
		return 0
	}
	x := point.X - i.contentRect().X + i.scrollX
	return nearestTextBoundary(metrics.offsets, metrics.boundaries, x)
}

func (i *InputWidget) selectionRect() (render.Rect, bool) {
	return i.selectionRectWithStyle(i.currentComponentStyle())
}

// selectionRectWithStyle reports the selection extent. The logical range
// [start, end) maps through the caret offsets; in an RTL run offsets
// decrease, so the rectangle uses the smaller offset as its left edge and
// the absolute difference as its width.
func (i *InputWidget) selectionRectWithStyle(style componentStyle) (render.Rect, bool) {
	start, end, ok := i.buffer.Selection()
	if !ok {
		return render.Rect{}, false
	}
	metrics := i.textMetrics()
	content := styleContentRect(i.bounds, style)
	startX := metrics.offsets[start]
	endX := metrics.offsets[end]
	x := content.X + math.Min(startX, endX) - i.scrollX
	width := math.Abs(endX - startX)
	return render.Rect{X: x, Y: content.Y, Width: width, Height: content.Height}, true
}

func (i *InputWidget) draw(ctx *drawContext) {
	i.drawStyled(ctx, unresolvedStyle())
}

func (i *InputWidget) drawWithStyle(ctx *drawContext, style componentStyle) {
	style = i.componentStyleWithFocus(style)
	i.updateScrollWithStyle(style)
	drawComponentBox(ctx, i.bounds, style)

	content := styleContentRect(i.bounds, style)
	ctx.SetClip(content)
	if selection, ok := i.selectionRectWithStyle(style); ok && selection.Width > 0 {
		ctx.DrawRect(selection, style.selection)
	}

	foreground := style.text
	value := i.displayValue()
	bitmap := i.textMetrics().bitmap
	compositionStart, compositionEnd := 0, 0
	var compositionOffsets []float64
	if i.composition.active {
		value, compositionStart, compositionEnd, _, _ = i.composedDisplay()
		bitmap, compositionOffsets, _ = compositionTextMetrics(i.face, value)
	} else if i.buffer.Value() == "" && i.placeholder != "" {
		value = i.placeholder
		foreground = defaultTheme.inputPlaceholder
		bitmap = text.Bitmap{}
		if i.face != nil {
			if placeholder, err := i.face.Rasterize(value); err == nil {
				bitmap = placeholder
			}
		}
	}
	if value != "" && (bitmap.Width != 0 || bitmap.Height != 0) {
		at := driver.Point{
			X: content.X - i.scrollX - float64(bitmap.OriginX),
			Y: content.Y + (content.Height-float64(bitmap.Height))/2,
		}
		ctx.DrawText(value, at, foreground, i.face)
	}
	if compositionEnd > compositionStart {
		// Caret offsets decrease through RTL runs, so the underline uses
		// the same min/absolute geometry as selections.
		startX := compositionOffsets[compositionStart]
		endX := compositionOffsets[compositionEnd]
		ctx.DrawRect(render.Rect{
			X:      content.X + math.Min(startX, endX) - i.scrollX,
			Y:      content.Y + content.Height - 2,
			Width:  math.Abs(endX - startX),
			Height: 1,
		}, defaultTheme.focusBorder)
	}
	if i.focused && i.enabled {
		ctx.DrawRect(render.Rect{
			X:      i.caretXWithStyle(style),
			Y:      content.Y,
			Width:  1,
			Height: content.Height,
		}, style.text)
	}
	ctx.ClearClip()

	if i.focused && i.enabled && !style.borderStyled && style.borderRadius <= 0 {
		for _, strip := range focusStrips(i.bounds, focusBorderWidth) {
			ctx.DrawRect(strip, defaultTheme.focusBorder)
		}
	}
}
