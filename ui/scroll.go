package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

// scrollWheelStep is the fixed logical distance one wheel line moves the
// scroll offset. The driver reports scroll deltas in wheel lines, where 1
// is one wheel line and trackpads emit smooth fractions.
const scrollWheelStep = 40.0

// ScrollLayout shows a single child through a viewport: the content keeps
// its natural size and is translated by the offset, clipped to the
// viewport. The viewport size is decided by the layout constraints, so a
// Scroll only scrolls where its parent bounds it smaller than the content.
// The concrete type is exported so applications can name it in variables
// and helper signatures; construct it with Scroll.
type ScrollLayout struct {
	content component

	padding         float64
	id              string
	class           string
	resolved        internalstyle.Resolved
	paddingExplicit bool

	bounds      render.Rect // full rect recorded by the last arrange
	viewport    Size        // visible content window (bounds minus padding)
	contentSize Size        // natural size measured for the content
	offsetX     float64
	offsetY     float64
}

// Scroll creates a scrollable viewport around the given content.
func Scroll(content Component) *ScrollLayout {
	return &ScrollLayout{content: asComponent(content)}
}

// ID sets the stylesheet ID selector metadata and returns the scroll.
func (s *ScrollLayout) ID(value string) *ScrollLayout {
	s.id = value
	return s
}

// Class sets the stylesheet class selector metadata and returns the scroll.
func (s *ScrollLayout) Class(value string) *ScrollLayout {
	s.class = value
	return s
}

func (s *ScrollLayout) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{Type: "Scroll", ID: s.id, Class: s.class}
}

func (s *ScrollLayout) setResolved(resolved internalstyle.Resolved) {
	s.resolved = resolved
}

// Padding sets the uniform space around the content inside the viewport;
// negative values clamp to zero. An explicit Go value takes priority over
// stylesheet padding.
func (s *ScrollLayout) Padding(value float64) *ScrollLayout {
	s.padding = max(value, 0)
	s.paddingExplicit = true
	return s
}

// SetOffset moves the content, clamping each axis into
// [0, contentSize-viewportSize]. A zero or negative range clamps to zero.
func (s *ScrollLayout) SetOffset(x, y float64) {
	s.offsetX = min(max(x, 0), s.maxOffsetX())
	s.offsetY = min(max(y, 0), s.maxOffsetY())
}

// Offset returns the current content offset.
func (s *ScrollLayout) Offset() (x, y float64) { return s.offsetX, s.offsetY }

// measure sizes the content at its natural size, adds padding to obtain the
// desired outer size, and clamps that outer size to the constraints. The
// content is never squeezed; subtracting padding from the clamped outer size
// gives the viewport, and a stale offset re-clamps when available space changes.
func (s *ScrollLayout) measure(constraints Constraints) Size {
	s.contentSize = s.content.measure(Constraints{})
	padding := scrollComponentStyle(s.padding, s.paddingExplicit, s.resolved).padding
	naturalOuter := Size{
		Width:  s.contentSize.Width + padding.horizontal(),
		Height: s.contentSize.Height + padding.vertical(),
	}
	size := clampSize(naturalOuter, constraints)
	s.viewport = s.viewportSize(size)
	s.SetOffset(s.offsetX, s.offsetY)
	return size
}

// arrange records the viewport rect and re-clamps the offset into the new
// range, then positions the content at its natural size translated by the
// negative offset. It must follow a measure with the same constraints.
// The content rect is deliberately not intersected with the viewport: the
// layout must keep the full natural size, and drawing plus hit testing
// restrict it to the viewport instead.
func (s *ScrollLayout) arrange(bounds render.Rect) {
	s.bounds = bounds
	s.viewport = s.viewportSize(Size{Width: bounds.Width, Height: bounds.Height})
	s.SetOffset(s.offsetX, s.offsetY)
	viewport := s.viewportRect()
	s.content.arrange(render.Rect{
		X:      viewport.X - s.offsetX,
		Y:      viewport.Y - s.offsetY,
		Width:  s.contentSize.Width,
		Height: s.contentSize.Height,
	})
}

// draw clips the content to the viewport and restores the previous clip
// afterwards, so children never paint into the padding ring or outside the
// scroll and a nested scroll re-applies the enclosing clip on the way out.
func (s *ScrollLayout) draw(ctx *drawContext) {
	ctx.SetClip(s.viewportRect())
	defer ctx.ClearClip()
	drawResolvedComponent(ctx, s.content)
}

// handleEvent forwards events to the content; a scroll consumes wheel
// events through consumeScroll, not here, so the two paths never double-
// deliver.
func (s *ScrollLayout) handleEvent(e driver.Event) (handled, redraw bool) {
	return s.content.handleEvent(e)
}

// consumeScroll applies one wheel event to the offset: scrolling right
// reveals content on the right (the offset grows), scrolling up reveals
// content above (the offset shrinks), each wheel line moving the fixed
// logical step. It reports whether the offset changed — at a clamped edge
// the event is left unhandled so the router can offer it to an enclosing
// scroll.
func (s *ScrollLayout) consumeScroll(e driver.Event) (handled, redraw bool) {
	nextX := min(max(s.offsetX+e.ScrollX*scrollWheelStep, 0), s.maxOffsetX())
	nextY := min(max(s.offsetY-e.ScrollY*scrollWheelStep, 0), s.maxOffsetY())
	if nextX == s.offsetX && nextY == s.offsetY {
		return false, false
	}
	s.offsetX, s.offsetY = nextX, nextY
	return true, true
}

// walk visits the scroll first and then its content.
func (s *ScrollLayout) walk(visit func(component)) {
	visit(s)
	s.content.walk(visit)
}

// childComponents exposes the content to the package-internal container
// traversal used by style resolution and hit testing.
func (s *ScrollLayout) childComponents() []component { return []component{s.content} }

// arrangedBounds returns the viewport rect — the content box inside the
// padding — because that is where content can be hit and the wheel
// consumed; the padding ring hits and consumes nothing, matching the other
// containers.
func (s *ScrollLayout) arrangedBounds() render.Rect { return s.viewportRect() }

// viewportRect is the visible content window: the arranged bounds minus
// the padding, intersected with the bounds so degenerate sizes collapse
// instead of extending past them.
func (s *ScrollLayout) viewportRect() render.Rect {
	padding := scrollComponentStyle(s.padding, s.paddingExplicit, s.resolved).padding
	bounds := s.bounds
	return intersectRects(render.Rect{
		X:      bounds.X + padding.left,
		Y:      bounds.Y + padding.top,
		Width:  bounds.Width - padding.horizontal(),
		Height: bounds.Height - padding.vertical(),
	}, bounds)
}

// viewportSize is the size half of viewportRect for a given outer size.
func (s *ScrollLayout) viewportSize(size Size) Size {
	padding := scrollComponentStyle(s.padding, s.paddingExplicit, s.resolved).padding
	return Size{
		Width:  max(size.Width-padding.horizontal(), 0),
		Height: max(size.Height-padding.vertical(), 0),
	}
}

func (s *ScrollLayout) maxOffsetX() float64 {
	return max(s.contentSize.Width-s.viewport.Width, 0)
}

func (s *ScrollLayout) maxOffsetY() float64 {
	return max(s.contentSize.Height-s.viewport.Height, 0)
}

func (s *ScrollLayout) isUIComponent() {}
