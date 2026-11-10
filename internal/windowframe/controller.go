package windowframe

import (
	"math"
	"time"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

// doubleClickWindow is the maximum delay between two presses in the title
// bar drag area for the second press to toggle maximize/restore.
const doubleClickWindow = 300 * time.Millisecond

// Result tells the caller what a handled event changed: Redraw requests a
// repaint of the title bar, Close asks the application to release resources
// and close the window.
type Result struct {
	Redraw, Close bool
}

// Controller turns mouse events into title bar interactions. It tracks hover
// and pressed buttons, title bar drags and edge resizes, and title bar
// double clicks. All window control calls go through the driver.FrameWindow
// passed to HandleEvent; the controller itself holds no native resources.
type Controller struct {
	style Style

	hover  Button
	active Button

	dragging    bool
	resizeEdges Edges
	startScreen driver.Point
	startPos    driver.Point
	startSize   driver.Size

	lastTitlePress    time.Time
	hasLastTitlePress bool
}

// NewController returns a Controller using the given style.
func NewController(style Style) *Controller {
	return &Controller{style: style}
}

// Hover reports the button currently under the cursor.
func (c *Controller) Hover() Button { return c.hover }

// Active reports the button currently held down, if any.
func (c *Controller) Active() Button { return c.active }

// HandleEvent feeds one window event into the state machine. screen is the
// cursor position in screen coordinates (window position plus the event's
// local position) and drives drag and resize deltas; now timestamps the
// event for double-click detection. Cursor leave clears hover without
// canceling a pressed button or an in-flight drag/resize. Other unrelated
// events leave the state untouched.
func (c *Controller) HandleEvent(e driver.Event, w driver.FrameWindow, screen driver.Point, now time.Time) Result {
	switch e.Kind {
	case driver.EventCursorLeave:
		if c.hover != ButtonNone {
			c.hover = ButtonNone
			return Result{Redraw: true}
		}
	case driver.EventMouseMove:
		return c.handleMove(e, w, screen)
	case driver.EventMouseButton:
		if e.Button != driver.MouseButtonLeft {
			return Result{}
		}
		switch e.Action {
		case driver.ActionPress:
			return c.handlePress(e, w, screen, now)
		case driver.ActionRelease:
			return c.handleRelease(e, w)
		}
	}
	return Result{}
}

func (c *Controller) handlePress(e driver.Event, w driver.FrameWindow, screen driver.Point, now time.Time) Result {
	// A fresh press supersedes any gesture still in flight: without this, a
	// second title press (double click) would toggle maximize yet keep the
	// first press's drag alive, and button/resize/drag states could stack.
	c.active = ButtonNone
	c.dragging = false
	c.resizeEdges = 0

	size := w.Size()
	layout := c.style.Layout(size)
	p := e.Position

	// Buttons win over resize edges and over a pending title double click.
	if btn := layout.ButtonAt(p); btn != ButtonNone {
		c.clearTitlePress()
		c.hover = btn
		c.active = btn
		return Result{Redraw: true}
	}
	if edges := c.style.ResizeEdges(size, p); edges != 0 {
		c.clearTitlePress()
		c.resizeEdges = edges
		c.startScreen = screen
		c.startPos = w.Position()
		c.startSize = size
		return Result{}
	}
	if !inRect(p, layout.TitleBar) {
		// A press anywhere outside the drag area breaks a pending double click.
		c.clearTitlePress()
		return Result{}
	}
	if c.hasLastTitlePress && now.Sub(c.lastTitlePress) <= doubleClickWindow {
		c.clearTitlePress()
		if w.IsMaximized() {
			w.Restore()
		} else {
			w.Maximize()
		}
		return Result{Redraw: true}
	}
	c.lastTitlePress = now
	c.hasLastTitlePress = true
	c.dragging = true
	c.startScreen = screen
	c.startPos = w.Position()
	return Result{}
}

func (c *Controller) handleMove(e driver.Event, w driver.FrameWindow, screen driver.Point) Result {
	if c.dragging {
		w.SetPosition(driver.Point{
			X: c.startPos.X + screen.X - c.startScreen.X,
			Y: c.startPos.Y + screen.Y - c.startScreen.Y,
		})
		return Result{}
	}
	if c.resizeEdges != 0 {
		c.resizeTo(w, screen)
		return Result{Redraw: true}
	}
	hover := c.style.Layout(w.Size()).ButtonAt(e.Position)
	if hover != c.hover {
		c.hover = hover
		return Result{Redraw: true}
	}
	return Result{}
}

func (c *Controller) handleRelease(e driver.Event, w driver.FrameWindow) Result {
	var res Result
	if c.active != ButtonNone {
		// A button only fires when the release lands back inside it.
		if c.style.Layout(w.Size()).ButtonAt(e.Position) == c.active {
			switch c.active {
			case ButtonMinimize:
				w.Iconify()
			case ButtonMaximize:
				if w.IsMaximized() {
					w.Restore()
				} else {
					w.Maximize()
				}
			case ButtonClose:
				res.Close = true
			}
		}
		res.Redraw = true
	}
	c.active = ButtonNone
	c.dragging = false
	c.resizeEdges = 0
	return res
}

// resizeTo applies the accumulated screen delta to the size and, for left
// and top edges, the position. Sizes clamp to the style's minimum; the size
// is rounded first, and the left/top position is then recomputed from that
// actual size so the opposite edge stays anchored.
func (c *Controller) resizeTo(w driver.FrameWindow, screen driver.Point) {
	dx := screen.X - c.startScreen.X
	dy := screen.Y - c.startScreen.Y
	startW := float64(c.startSize.Width)
	startH := float64(c.startSize.Height)
	newW := startW
	newH := startH
	pos := c.startPos
	if c.resizeEdges&EdgeRight != 0 {
		newW = startW + dx
	}
	if c.resizeEdges&EdgeBottom != 0 {
		newH = startH + dy
	}
	if c.resizeEdges&EdgeLeft != 0 {
		newW = startW - dx
	}
	if c.resizeEdges&EdgeTop != 0 {
		newH = startH - dy
	}
	if min := float64(c.style.MinSize.Width); newW < min {
		newW = min
	}
	if min := float64(c.style.MinSize.Height); newH < min {
		newH = min
	}
	actual := driver.Size{Width: int(math.Round(newW)), Height: int(math.Round(newH))}
	aw := float64(actual.Width)
	ah := float64(actual.Height)
	if c.resizeEdges&EdgeLeft != 0 {
		pos.X = c.startPos.X + startW - aw
	}
	if c.resizeEdges&EdgeTop != 0 {
		pos.Y = c.startPos.Y + startH - ah
	}
	w.SetSize(actual)
	if pos != c.startPos {
		w.SetPosition(pos)
	}
}

func (c *Controller) clearTitlePress() {
	c.lastTitlePress = time.Time{}
	c.hasLastTitlePress = false
}
