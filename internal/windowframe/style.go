// Package windowframe lays out and drives the custom title bar of a
// frameless window: geometry and hit testing for the icon, title and window
// control buttons, resize edge detection, and the interaction state machine
// behind hover, press, drag, double-click and resize gestures. It is pure Go
// and depends only on the driver and render packages, so every behavior is
// testable without a native window.
package windowframe

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// Style configures the geometry and colors of a custom title bar. All
// colors are fully specified RGBA values in the range [0, 1]; idle button
// colors match the background so controls blend in until hovered.
type Style struct {
	Height, ButtonWidth, ResizeMargin float64
	MinSize                           driver.Size

	Background, TitleColor, IconColor      render.Color
	ButtonColor, ButtonHover, ButtonActive render.Color
	CloseColor, CloseHover, CloseActive    render.Color
}

// DefaultStyle returns the standard title bar style: a 36px bar, 46px
// buttons, a 5px resize margin and a 240x120 minimum window size.
func DefaultStyle() Style {
	return Style{
		Height:       36,
		ButtonWidth:  46,
		ResizeMargin: 5,
		MinSize:      driver.Size{Width: 240, Height: 120},

		Background: render.Color{R: 0.13, G: 0.13, B: 0.14, A: 1},
		TitleColor: render.Color{R: 0.92, G: 0.92, B: 0.92, A: 1},
		IconColor:  render.Color{R: 0.80, G: 0.80, B: 0.82, A: 1},

		ButtonColor:  render.Color{R: 0.13, G: 0.13, B: 0.14, A: 1},
		ButtonHover:  render.Color{R: 0.24, G: 0.24, B: 0.26, A: 1},
		ButtonActive: render.Color{R: 0.32, G: 0.32, B: 0.34, A: 1},

		CloseColor:  render.Color{R: 0.13, G: 0.13, B: 0.14, A: 1},
		CloseHover:  render.Color{R: 0.83, G: 0.18, B: 0.18, A: 1},
		CloseActive: render.Color{R: 0.66, G: 0.13, B: 0.13, A: 1},
	}
}
