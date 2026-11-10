package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

// RowLayout arranges its children horizontally from left to right with an
// even gap between them, wrapped in padding. Children keep their measured
// widths and are stretched vertically to the content height; direct
// Spacer children share the leftover width equally. The concrete type is
// exported so applications can name it in variables and helper
// signatures; construct it with Row.
type RowLayout struct {
	children []component
	gap      float64
	padding  float64
	bounds   render.Rect
	sizes    []Size // per-child measured sizes from the last measure

	id              string
	class           string
	resolved        internalstyle.Resolved
	gapExplicit     bool
	paddingExplicit bool
}

// Row creates a horizontal layout holding the given children in order.
func Row(children ...Component) *RowLayout {
	r := &RowLayout{}
	for _, child := range children {
		r.children = append(r.children, asComponent(child))
	}
	return r
}

// ID sets the stylesheet ID selector metadata and returns the row.
func (r *RowLayout) ID(value string) *RowLayout {
	r.id = value
	return r
}

// Class sets the stylesheet class selector metadata and returns the row.
func (r *RowLayout) Class(value string) *RowLayout {
	r.class = value
	return r
}

func (r *RowLayout) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{Type: "Row", ID: r.id, Class: r.class}
}

func (r *RowLayout) setResolved(resolved internalstyle.Resolved) {
	r.resolved = resolved
}

// Gap sets the horizontal space between children; negative values clamp
// to zero. An explicit Go value takes priority over stylesheet gap values.
func (r *RowLayout) Gap(value float64) *RowLayout {
	r.gap = max(value, 0)
	r.gapExplicit = true
	return r
}

// Padding sets the uniform space around the children; negative values
// clamp to zero. An explicit Go value takes priority over stylesheet padding.
func (r *RowLayout) Padding(value float64) *RowLayout {
	r.padding = max(value, 0)
	r.paddingExplicit = true
	return r
}

// measure sizes the row: children measure inside the padding, the content
// height is the tallest child, the content width is the child widths plus
// the gaps between them, and padding is added on every side before
// clamping to the constraints. The per-child sizes are cached for the
// following arrange.
//
// Like the column's vertical minimum, the horizontal minimum belongs to
// the row as a whole: it clamps the total, never each child. Each child
// measures with its own inner constraints — the horizontal minimum
// cleared and the remaining horizontal budget written into Max.Width — so
// nested trees measure against the real finite budget. Spacers are not
// measured at all: after the loop the leftover width is shared equally
// among them, so an unbounded row leaves them empty and a row whose
// budget ran out collapses them to zero instead of a negative size.
func (r *RowLayout) measure(constraints Constraints) Size {
	style := containerComponentStyle(r.gap, r.padding, r.gapExplicit, r.paddingExplicit, r.resolved)
	padding := style.padding
	space := contentSpace{}
	if constraints.Max.Width > 0 {
		space.boundedWidth = true
		space.width = constraints.Max.Width - padding.horizontal()
	}
	if constraints.Max.Height > 0 {
		space.boundedHeight = true
		space.height = constraints.Max.Height - padding.vertical()
	}
	content := Size{}
	r.sizes = r.sizes[:0]
	spacers := 0
	fixed := 0.0
	remaining := space.width
	for i, child := range r.children {
		if i > 0 {
			content.Width += style.gap
		}
		if isSpacerComponent(child) {
			spacers++
			r.sizes = append(r.sizes, Size{})
			continue
		}
		inner := constraints
		inner.Min.Width = 0
		inner.Min.Height = max(inner.Min.Height-padding.vertical(), 0)
		if inner.Max.Width > 0 {
			inner.Max.Width = max(inner.Max.Width-padding.horizontal(), 0)
		}
		if inner.Max.Height > 0 {
			inner.Max.Height = max(inner.Max.Height-padding.vertical(), 0)
		}
		if (space.boundedHeight && space.height <= 0) || (space.boundedWidth && remaining <= 0) {
			r.sizes = append(r.sizes, Size{})
			continue
		}
		if space.boundedHeight {
			inner.Max.Height = space.height
		}
		if space.boundedWidth {
			inner.Max.Width = remaining
		}
		size := child.measure(inner)
		r.sizes = append(r.sizes, size)
		fixed += size.Width
		content.Height = max(content.Height, size.Height)
		content.Width += size.Width
		remaining -= size.Width + style.gap
	}
	if spacers > 0 && space.boundedWidth {
		if extra := space.width - fixed - style.gap*float64(len(r.children)-1); extra > 0 {
			share := extra / float64(spacers)
			for i, child := range r.children {
				if isSpacerComponent(child) {
					r.sizes[i].Width = share
				}
			}
			content.Width += extra
		}
	}
	return clampSize(Size{
		Width:  content.Width + padding.horizontal(),
		Height: content.Height + padding.vertical(),
	}, constraints)
}

// arrange positions each child at the left edge of the row, laid out to
// the right, stretched vertically to the content height and keeping the
// measured width. It must follow a measure with the same constraints.
// The content box is the parent bounds minus the padding, intersected
// with the parent bounds; every child rect is intersected with the
// content box, so degenerate bounds collapse children to zero-size
// points inside the row instead of sizes that draw outside it.
func (r *RowLayout) arrange(bounds render.Rect) {
	style := containerComponentStyle(r.gap, r.padding, r.gapExplicit, r.paddingExplicit, r.resolved)
	padding := style.padding
	r.bounds = bounds
	content := intersectRects(render.Rect{
		X:      bounds.X + padding.left,
		Y:      bounds.Y + padding.top,
		Width:  bounds.Width - padding.horizontal(),
		Height: bounds.Height - padding.vertical(),
	}, bounds)
	x := content.X
	for i, child := range r.children {
		width := 0.0
		if i < len(r.sizes) {
			width = r.sizes[i].Width
		}
		child.arrange(intersectRects(render.Rect{
			X:      x,
			Y:      content.Y,
			Width:  width,
			Height: content.Height,
		}, content))
		x += width + style.gap
	}
}

// draw paints the children in order; a row has no visual of its own.
func (r *RowLayout) draw(ctx *drawContext) {
	for _, child := range r.children {
		drawResolvedComponent(ctx, child)
	}
}

// handleEvent forwards the event to the children in order and aggregates
// the outcomes: handled reports whether any child consumed it and redraw
// whether any child asked for a repaint.
func (r *RowLayout) handleEvent(e driver.Event) (handled, redraw bool) {
	for _, child := range r.children {
		h, r := child.handleEvent(e)
		handled = handled || h
		redraw = redraw || r
	}
	return handled, redraw
}

// walk visits the row first and then its descendants in order.
func (r *RowLayout) walk(visit func(component)) {
	visit(r)
	for _, child := range r.children {
		child.walk(visit)
	}
}

// childComponents exposes the children to the package-internal container
// traversal used by style resolution and hit testing.
func (r *RowLayout) childComponents() []component { return r.children }

// arrangedBounds returns the rect recorded by the last arrange.
func (r *RowLayout) arrangedBounds() render.Rect { return r.bounds }

func (r *RowLayout) isUIComponent() {}
