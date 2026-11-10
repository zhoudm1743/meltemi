package windowframe

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// Button identifies one of the window control buttons.
type Button uint8

const (
	ButtonNone Button = iota
	ButtonMinimize
	ButtonMaximize
	ButtonClose
)

// Edges is a bitmask of the window borders a point is near.
type Edges uint8

const (
	EdgeLeft Edges = 1 << iota
	EdgeRight
	EdgeTop
	EdgeBottom
)

// Title bar inner geometry: a 16x16 icon with an 8px margin on both sides.
const (
	iconSize   = 16
	iconMargin = 8
)

// Layout is the title bar geometry derived from one window size.
type Layout struct {
	TitleBar render.Rect
	Icon     render.Rect
	Title    render.Rect
	Minimize render.Rect
	Maximize render.Rect
	Close    render.Rect
}

// Layout places the title bar across the top of the window, right-aligns the
// three control buttons in minimize/maximize/close order, and fits the icon
// and title text into the remaining space on the left. Rectangles are
// half-open: a point on the right or bottom edge of a rect is outside it.
func (s Style) Layout(size driver.Size) Layout {
	w := float64(size.Width)
	close := render.Rect{X: w - s.ButtonWidth, Y: 0, Width: s.ButtonWidth, Height: s.Height}
	maximize := render.Rect{X: close.X - s.ButtonWidth, Y: 0, Width: s.ButtonWidth, Height: s.Height}
	minimize := render.Rect{X: maximize.X - s.ButtonWidth, Y: 0, Width: s.ButtonWidth, Height: s.Height}
	icon := render.Rect{X: iconMargin, Y: (s.Height - iconSize) / 2, Width: iconSize, Height: iconSize}
	titleX := icon.X + icon.Width + iconMargin
	return Layout{
		TitleBar: render.Rect{X: 0, Y: 0, Width: w, Height: s.Height},
		Icon:     icon,
		Title:    render.Rect{X: titleX, Y: 0, Width: minimize.X - titleX, Height: s.Height},
		Minimize: minimize,
		Maximize: maximize,
		Close:    close,
	}
}

// ButtonAt reports which control button contains p, if any. Buttons take
// priority over resize edges: a point inside a button is never a resize.
func (l Layout) ButtonAt(p driver.Point) Button {
	switch {
	case inRect(p, l.Close):
		return ButtonClose
	case inRect(p, l.Maximize):
		return ButtonMaximize
	case inRect(p, l.Minimize):
		return ButtonMinimize
	default:
		return ButtonNone
	}
}

// ResizeEdges reports which window borders are within the style's resize
// margin of p. Corner points combine the two adjacent edges. The check is
// purely geometric; callers that also have buttons decide priority.
func (s Style) ResizeEdges(size driver.Size, p driver.Point) Edges {
	var e Edges
	w := float64(size.Width)
	h := float64(size.Height)
	if p.X < s.ResizeMargin {
		e |= EdgeLeft
	}
	if p.X >= w-s.ResizeMargin {
		e |= EdgeRight
	}
	if p.Y < s.ResizeMargin {
		e |= EdgeTop
	}
	if p.Y >= h-s.ResizeMargin {
		e |= EdgeBottom
	}
	return e
}

func inRect(p driver.Point, r render.Rect) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}
