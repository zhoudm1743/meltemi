package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

// ColumnLayout stacks its children vertically from top to bottom with an
// even gap between them, wrapped in padding. Children are stretched
// horizontally to the content width and keep their measured heights. The
// concrete type is exported so applications can name it in variables and
// helper signatures; construct it with Column.
type ColumnLayout struct {
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
	fillLast        bool
}

// Column creates a vertical layout holding the given children in order.
func Column(children ...Component) *ColumnLayout {
	c := &ColumnLayout{}
	for _, child := range children {
		c.children = append(c.children, asComponent(child))
	}
	return c
}

// ID sets the stylesheet ID selector metadata and returns the column.
func (c *ColumnLayout) ID(value string) *ColumnLayout {
	c.id = value
	return c
}

// Class sets the stylesheet class selector metadata and returns the column.
func (c *ColumnLayout) Class(value string) *ColumnLayout {
	c.class = value
	return c
}

func (c *ColumnLayout) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{Type: "Column", ID: c.id, Class: c.class}
}

func (c *ColumnLayout) setResolved(resolved internalstyle.Resolved) {
	c.resolved = resolved
}

// Gap sets the vertical space between children; negative values clamp to
// zero. An explicit Go value takes priority over stylesheet gap values.
func (c *ColumnLayout) Gap(value float64) *ColumnLayout {
	c.gap = max(value, 0)
	c.gapExplicit = true
	return c
}

// Padding sets the uniform space around the children; negative values
// clamp to zero. An explicit Go value takes priority over stylesheet padding.
func (c *ColumnLayout) Padding(value float64) *ColumnLayout {
	c.padding = max(value, 0)
	c.paddingExplicit = true
	return c
}

// measure sizes the column: children measure inside the padding, the
// content width is the widest child, the content height is the child
// heights plus the gaps between them, and padding is added on every side
// before clamping to the constraints. The per-child sizes are cached for
// the following arrange.
//
// The vertical minimum belongs to the column as a whole: it clamps the
// total, never each child. Each child measures with its own inner
// constraints — the vertical minimum cleared and the remaining vertical
// budget written into Max.Height — so nested trees measure against the
// real finite budget. When a bounded dimension has no room left the
// child cannot be measured, because the public Constraints have no
// bounded-zero Max; it collapses to a zero size instead of measuring
// unbounded.
//
// Spacers are not measured at all: after the loop the leftover height is
// shared equally among the direct Spacer children, so an unbounded
// column leaves them empty and a column whose budget ran out collapses
// them to zero instead of a negative size.
func (c *ColumnLayout) measure(constraints Constraints) Size {
	style := containerComponentStyle(c.gap, c.padding, c.gapExplicit, c.paddingExplicit, c.resolved)
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
	c.sizes = c.sizes[:0]
	spacers := 0
	fixed := 0.0
	remaining := space.height
	for i, child := range c.children {
		if i > 0 {
			content.Height += style.gap
		}
		if isSpacerComponent(child) {
			spacers++
			c.sizes = append(c.sizes, Size{})
			continue
		}
		inner := constraints
		inner.Min.Width = max(inner.Min.Width-padding.horizontal(), 0)
		inner.Min.Height = 0
		if inner.Max.Width > 0 {
			inner.Max.Width = max(inner.Max.Width-padding.horizontal(), 0)
		}
		if inner.Max.Height > 0 {
			inner.Max.Height = max(inner.Max.Height-padding.vertical(), 0)
		}
		if (space.boundedWidth && space.width <= 0) || (space.boundedHeight && remaining <= 0) {
			c.sizes = append(c.sizes, Size{})
			continue
		}
		if space.boundedWidth {
			inner.Max.Width = space.width
		}
		if space.boundedHeight {
			inner.Max.Height = remaining
		}
		size := child.measure(inner)
		c.sizes = append(c.sizes, size)
		fixed += size.Height
		content.Width = max(content.Width, size.Width)
		content.Height += size.Height
		remaining -= size.Height + style.gap
	}
	if spacers > 0 && space.boundedHeight {
		if extra := space.height - fixed - style.gap*float64(len(c.children)-1); extra > 0 {
			share := extra / float64(spacers)
			for i, child := range c.children {
				if isSpacerComponent(child) {
					c.sizes[i].Height = share
				}
			}
			content.Height += extra
		}
	}
	return clampSize(Size{
		Width:  content.Width + padding.horizontal(),
		Height: content.Height + padding.vertical(),
	}, constraints)
}

// arrange positions each child at the top of the column, stacked
// downward, stretched horizontally to the content width and keeping the
// measured height. It must follow a measure with the same constraints.
// The content box is the parent bounds minus the padding, intersected
// with the parent bounds; every child rect is intersected with the
// content box, so degenerate bounds collapse children to zero-size
// points inside the column instead of sizes that draw outside it.
func (c *ColumnLayout) arrange(bounds render.Rect) {
	style := containerComponentStyle(c.gap, c.padding, c.gapExplicit, c.paddingExplicit, c.resolved)
	padding := style.padding
	c.bounds = bounds
	content := intersectRects(render.Rect{
		X:      bounds.X + padding.left,
		Y:      bounds.Y + padding.top,
		Width:  bounds.Width - padding.horizontal(),
		Height: bounds.Height - padding.vertical(),
	}, bounds)
	y := content.Y
	for i, child := range c.children {
		height := 0.0
		if i < len(c.sizes) {
			height = c.sizes[i].Height
		}
		if c.fillLast && i == len(c.children)-1 {
			height = max(content.Y+content.Height-y, 0)
		}
		child.arrange(intersectRects(render.Rect{
			X:      content.X,
			Y:      y,
			Width:  content.Width,
			Height: height,
		}, content))
		y += height + style.gap
	}
}

// draw paints the children in order; a column has no visual of its own.
// drawResolvedComponent scopes each child's opacity and restores the column's
// composed alpha between siblings.
func (c *ColumnLayout) draw(ctx *drawContext) {
	for _, child := range c.children {
		drawResolvedComponent(ctx, child)
	}
}

// handleEvent forwards the event to the children in order and aggregates
// the outcomes: handled reports whether any child consumed it and redraw
// whether any child asked for a repaint.
func (c *ColumnLayout) handleEvent(e driver.Event) (handled, redraw bool) {
	for _, child := range c.children {
		h, r := child.handleEvent(e)
		handled = handled || h
		redraw = redraw || r
	}
	return handled, redraw
}

// walk visits the column first and then its descendants in order.
func (c *ColumnLayout) walk(visit func(component)) {
	visit(c)
	for _, child := range c.children {
		child.walk(visit)
	}
}

// childComponents exposes the children to the package-internal container
// traversal used by style resolution and hit testing.
func (c *ColumnLayout) childComponents() []component { return c.children }

// arrangedBounds returns the rect recorded by the last arrange.
func (c *ColumnLayout) arrangedBounds() render.Rect { return c.bounds }

func (c *ColumnLayout) isUIComponent() {}
