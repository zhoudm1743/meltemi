package ui

import "github.com/zhoudm1743/meltemi/internal/render"

// Size is the width and height of a component in logical units.
type Size struct {
	Width  float64
	Height float64
}

// Constraints bound the size a component may measure into. A zero Max
// dimension leaves that direction unbounded. Min is always satisfied, so
// a conflicting Min wins over Max.
type Constraints struct {
	Min, Max Size
}

// clampSize constrains a measured size to the limits. Max is applied
// first with zero meaning unbounded; Min is applied last so a minimum is
// never undercut.
func clampSize(size Size, c Constraints) Size {
	if c.Max.Width > 0 {
		size.Width = min(size.Width, c.Max.Width)
	}
	if c.Max.Height > 0 {
		size.Height = min(size.Height, c.Max.Height)
	}
	size.Width = max(size.Width, c.Min.Width)
	size.Height = max(size.Height, c.Min.Height)
	return size
}

// deflate narrows constraints by a uniform padding on every side, used to
// measure the children inside a padded container. Zero Max stays
// unbounded; it cannot express a bounded space of zero, so callers that
// need that distinction enforce it on measured results via contentSpace.
func deflate(c Constraints, padding float64) Constraints {
	c.Min.Width = max(c.Min.Width-2*padding, 0)
	c.Min.Height = max(c.Min.Height-2*padding, 0)
	if c.Max.Width > 0 {
		c.Max.Width = max(c.Max.Width-2*padding, 0)
	}
	if c.Max.Height > 0 {
		c.Max.Height = max(c.Max.Height-2*padding, 0)
	}
	return c
}

// contentSpace is the space left inside a container's padding for its
// children. A finite parent Max stays bounded even when the padding
// consumes it entirely, so "bounded with no room" never degrades into
// the unbounded zero Max of the public Constraints.
type contentSpace struct {
	width, height               float64
	boundedWidth, boundedHeight bool
}

// measureSpace derives the children's content space from the parent
// constraints and a uniform padding.
func measureSpace(c Constraints, padding float64) contentSpace {
	var s contentSpace
	if c.Max.Width > 0 {
		s.boundedWidth = true
		s.width = c.Max.Width - 2*padding
	}
	if c.Max.Height > 0 {
		s.boundedHeight = true
		s.height = c.Max.Height - 2*padding
	}
	return s
}

// isSpacerComponent reports whether c is a Spacer widget. Row and Column
// use it to split their leftover main-axis space among their direct
// Spacer children.
func isSpacerComponent(c component) bool {
	_, ok := c.(*SpacerWidget)
	return ok
}

// intersectRects returns the intersection of a and b. When the rects do
// not overlap, the result degenerates to a zero-size rect at the top-left
// of a clamped into b, so even an empty intersection stays inside b.
func intersectRects(a, b render.Rect) render.Rect {
	x := max(a.X, b.X)
	y := max(a.Y, b.Y)
	right := min(a.X+a.Width, b.X+b.Width)
	bottom := min(a.Y+a.Height, b.Y+b.Height)
	if right < x || bottom < y {
		return render.Rect{
			X:      min(max(a.X, b.X), b.X+b.Width),
			Y:      min(max(a.Y, b.Y), b.Y+b.Height),
			Width:  0,
			Height: 0,
		}
	}
	return render.Rect{X: x, Y: y, Width: right - x, Height: bottom - y}
}
