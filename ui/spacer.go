package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

// SpacerWidget is flexible empty space inside Row and Column: it has no
// natural size, draws nothing and never receives input. Row and Column
// split their leftover main-axis space equally among their direct Spacer
// children and stretch them across the cross axis, so a Spacer only takes
// the space the layout has left over.
type SpacerWidget struct {
	bounds   render.Rect
	resolved internalstyle.Resolved
}

// Spacer creates an empty flexible component for Row and Column layouts.
func Spacer() *SpacerWidget { return &SpacerWidget{} }

func (s *SpacerWidget) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{Type: "Spacer"}
}

func (s *SpacerWidget) setResolved(resolved internalstyle.Resolved) {
	s.resolved = resolved
}

// measure reports the spacer's lack of natural size; the constraints
// still clamp the result like any other component.
func (s *SpacerWidget) measure(constraints Constraints) Size {
	return clampSize(Size{}, constraints)
}

func (s *SpacerWidget) arrange(bounds render.Rect) { s.bounds = bounds }

// draw paints nothing; a spacer is invisible space.
func (s *SpacerWidget) draw(*drawContext) {}

// handleEvent consumes nothing; a spacer never arms hover or press.
func (s *SpacerWidget) handleEvent(driver.Event) (handled, redraw bool) {
	return false, false
}

// walk visits the spacer itself; it holds no children.
func (s *SpacerWidget) walk(visit func(component)) { visit(s) }

func (s *SpacerWidget) isUIComponent() {}
