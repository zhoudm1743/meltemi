package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

// StackLayout gives every child the same content rectangle, so children
// overlap by design and paint in declaration order — the later child is
// on top. The content box is the largest child wrapped in padding; the
// hit tester checks the children in reverse, so the topmost visible child
// always receives the pointer event. The concrete type is exported so
// applications can name it in variables and helper signatures; construct
// it with Stack.
type StackLayout struct {
	children []component
	padding  float64
	bounds   render.Rect

	id              string
	class           string
	resolved        internalstyle.Resolved
	paddingExplicit bool
}

// Stack creates an overlapping layout holding the given children in
// declaration order; the last child paints on top.
func Stack(children ...Component) *StackLayout {
	s := &StackLayout{}
	for _, child := range children {
		s.children = append(s.children, asComponent(child))
	}
	return s
}

// ID sets the stylesheet ID selector metadata and returns the stack.
func (s *StackLayout) ID(value string) *StackLayout {
	s.id = value
	return s
}

// Class sets the stylesheet class selector metadata and returns the stack.
func (s *StackLayout) Class(value string) *StackLayout {
	s.class = value
	return s
}

func (s *StackLayout) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{Type: "Stack", ID: s.id, Class: s.class}
}

func (s *StackLayout) setResolved(resolved internalstyle.Resolved) {
	s.resolved = resolved
}

// Padding sets the uniform space around the children; negative values
// clamp to zero. An explicit Go value takes priority over stylesheet padding.
func (s *StackLayout) Padding(value float64) *StackLayout {
	s.padding = max(value, 0)
	s.paddingExplicit = true
	return s
}

// measure sizes the stack: every child measures inside the padding and
// the content box is the largest child in each direction, plus padding
// on every side before clamping to the constraints. Unlike the
// directional layouts there is no gap and no main axis: every child
// receives the full content rectangle at arrange time. When a bounded
// dimension has no room left the child cannot be measured, because the
// public Constraints have no bounded-zero Max; it collapses to a zero
// size instead of measuring unbounded.
func (s *StackLayout) measure(constraints Constraints) Size {
	style := stackComponentStyle(s.padding, s.paddingExplicit, s.resolved)
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
	for _, child := range s.children {
		if (space.boundedWidth && space.width <= 0) || (space.boundedHeight && space.height <= 0) {
			continue
		}
		inner := constraints
		inner.Min.Width = max(inner.Min.Width-padding.horizontal(), 0)
		inner.Min.Height = max(inner.Min.Height-padding.vertical(), 0)
		if inner.Max.Width > 0 {
			inner.Max.Width = max(inner.Max.Width-padding.horizontal(), 0)
		}
		if inner.Max.Height > 0 {
			inner.Max.Height = max(inner.Max.Height-padding.vertical(), 0)
		}
		if space.boundedWidth {
			inner.Max.Width = space.width
		}
		if space.boundedHeight {
			inner.Max.Height = space.height
		}
		size := child.measure(inner)
		content.Width = max(content.Width, size.Width)
		content.Height = max(content.Height, size.Height)
	}
	return clampSize(Size{
		Width:  content.Width + padding.horizontal(),
		Height: content.Height + padding.vertical(),
	}, constraints)
}

// arrange gives every child the same content rectangle — the parent
// bounds minus the padding, intersected with the parent bounds — so
// degenerate bounds collapse children to zero-size points inside the
// stack instead of sizes that draw outside it. It must follow a measure
// with the same constraints.
func (s *StackLayout) arrange(bounds render.Rect) {
	style := stackComponentStyle(s.padding, s.paddingExplicit, s.resolved)
	padding := style.padding
	s.bounds = bounds
	content := intersectRects(render.Rect{
		X:      bounds.X + padding.left,
		Y:      bounds.Y + padding.top,
		Width:  bounds.Width - padding.horizontal(),
		Height: bounds.Height - padding.vertical(),
	}, bounds)
	for _, child := range s.children {
		child.arrange(content)
	}
}

// draw paints the children in declaration order, so later children
// overlap the earlier ones; a stack has no visual of its own.
func (s *StackLayout) draw(ctx *drawContext) {
	for _, child := range s.children {
		drawResolvedComponent(ctx, child)
	}
}

// handleEvent forwards the event to the children in order and aggregates
// the outcomes: handled reports whether any child consumed it and redraw
// whether any child asked for a repaint.
func (s *StackLayout) handleEvent(e driver.Event) (handled, redraw bool) {
	for _, child := range s.children {
		h, r := child.handleEvent(e)
		handled = handled || h
		redraw = redraw || r
	}
	return handled, redraw
}

// walk visits the stack first and then its descendants in order.
func (s *StackLayout) walk(visit func(component)) {
	visit(s)
	for _, child := range s.children {
		child.walk(visit)
	}
}

// childComponents exposes the children to the package-internal container
// traversal used by style resolution and hit testing.
func (s *StackLayout) childComponents() []component { return s.children }

// arrangedBounds returns the rect recorded by the last arrange.
func (s *StackLayout) arrangedBounds() render.Rect { return s.bounds }

func (s *StackLayout) isUIComponent() {}
