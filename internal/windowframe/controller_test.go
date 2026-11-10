package windowframe

import (
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// fakeFrameWindow is an in-memory driver.FrameWindow for controller tests.
type fakeFrameWindow struct {
	size          driver.Size
	pos           driver.Point
	maximized     bool
	iconifyCalls  int
	maximizeCalls int
	restoreCalls  int
}

func (f *fakeFrameWindow) Show()                     {}
func (f *fakeFrameWindow) Hide()                     {}
func (f *fakeFrameWindow) Destroy()                  {}
func (f *fakeFrameWindow) Size() driver.Size         { return f.size }
func (f *fakeFrameWindow) SetHandler(driver.Handler) {}
func (f *fakeFrameWindow) SetSize(s driver.Size) error {
	f.size = s
	return nil
}
func (f *fakeFrameWindow) Position() driver.Point     { return f.pos }
func (f *fakeFrameWindow) SetPosition(p driver.Point) { f.pos = p }
func (f *fakeFrameWindow) Iconify()                   { f.iconifyCalls++ }
func (f *fakeFrameWindow) IsIconified() bool {
	return false
}
func (f *fakeFrameWindow) Maximize() {
	f.maximized = true
	f.maximizeCalls++
}
func (f *fakeFrameWindow) Restore() {
	f.maximized = false
	f.restoreCalls++
}
func (f *fakeFrameWindow) IsMaximized() bool { return f.maximized }

func pressAt(p driver.Point) driver.Event {
	return driver.Event{Kind: driver.EventMouseButton, Button: driver.MouseButtonLeft, Action: driver.ActionPress, Position: p}
}

func releaseAt(p driver.Point) driver.Event {
	return driver.Event{Kind: driver.EventMouseButton, Button: driver.MouseButtonLeft, Action: driver.ActionRelease, Position: p}
}

func moveAt(p driver.Point) driver.Event {
	return driver.Event{Kind: driver.EventMouseMove, Position: p}
}

func add(p, q driver.Point) driver.Point {
	return driver.Point{X: p.X + q.X, Y: p.Y + q.Y}
}

func leaveEvent() driver.Event {
	return driver.Event{Kind: driver.EventCursorLeave}
}

func TestCursorLeaveClearsHoverOnce(t *testing.T) {
	s := DefaultStyle()
	w := &fakeFrameWindow{size: driver.Size{Width: 400, Height: 300}}
	c := NewController(s)
	center := mid(s.Layout(w.Size()).Close)
	now := time.Now()

	c.HandleEvent(moveAt(center), w, center, now)
	if c.Hover() != ButtonClose {
		t.Fatalf("hover = %v, want ButtonClose before leave", c.Hover())
	}
	r := c.HandleEvent(leaveEvent(), w, driver.Point{}, now.Add(10*time.Millisecond))
	if !r.Redraw {
		t.Error("clearing hover on cursor leave must request a redraw")
	}
	if c.Hover() != ButtonNone {
		t.Errorf("hover after leave = %v, want ButtonNone", c.Hover())
	}
	if c.Active() != ButtonNone {
		t.Errorf("hover-only leave changed active to %v", c.Active())
	}
	if r := c.HandleEvent(leaveEvent(), w, driver.Point{}, now.Add(20*time.Millisecond)); r != (Result{}) {
		t.Errorf("repeated leave result = %v, want zero once hover is already clear", r)
	}
}

func TestCursorLeavePreservesPressUntilOutsideRelease(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	pos := driver.Point{X: 100, Y: 50}
	w := &fakeFrameWindow{size: size, pos: pos}
	c := NewController(s)
	center := mid(s.Layout(size).Close)
	outside := driver.Point{X: -10, Y: 20}
	now := time.Now()

	c.HandleEvent(pressAt(center), w, add(pos, center), now)
	r := c.HandleEvent(leaveEvent(), w, driver.Point{}, now.Add(10*time.Millisecond))
	if !r.Redraw || r.Close {
		t.Errorf("pressed leave result = %v, want redraw without close", r)
	}
	if c.Hover() != ButtonNone || c.Active() != ButtonClose {
		t.Fatalf("after leave hover=%v active=%v, want none/close", c.Hover(), c.Active())
	}
	r = c.HandleEvent(releaseAt(outside), w, add(pos, outside), now.Add(20*time.Millisecond))
	if r.Close {
		t.Error("release outside after cursor leave must cancel, not close")
	}
	if !r.Redraw || c.Active() != ButtonNone {
		t.Errorf("outside release result=%v active=%v, want redraw and no active button", r, c.Active())
	}
	if w.iconifyCalls != 0 || w.maximizeCalls != 0 || w.restoreCalls != 0 {
		t.Error("outside release invoked a native window control")
	}
}

func TestCursorLeavePreservesDrag(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	pos := driver.Point{X: 100, Y: 50}
	w := &fakeFrameWindow{size: size, pos: pos}
	c := NewController(s)
	title := driver.Point{X: 60, Y: 18}
	now := time.Now()

	c.HandleEvent(pressAt(title), w, add(pos, title), now)
	if r := c.HandleEvent(leaveEvent(), w, driver.Point{}, now.Add(10*time.Millisecond)); r != (Result{}) {
		t.Errorf("leave during drag without hover = %v, want zero", r)
	}
	outside := driver.Point{X: -20, Y: 40}
	screen := driver.Point{X: pos.X + title.X + 40, Y: pos.Y + title.Y + 30}
	c.HandleEvent(moveAt(outside), w, screen, now.Add(20*time.Millisecond))
	if want := (driver.Point{X: pos.X + 40, Y: pos.Y + 30}); w.pos != want {
		t.Errorf("position after outside drag = %v, want %v", w.pos, want)
	}
	c.HandleEvent(releaseAt(outside), w, screen, now.Add(30*time.Millisecond))
	c.HandleEvent(moveAt(outside), w, add(screen, driver.Point{X: 50, Y: 50}), now.Add(40*time.Millisecond))
	if want := (driver.Point{X: pos.X + 40, Y: pos.Y + 30}); w.pos != want {
		t.Errorf("drag continued after outside release: pos=%v, want %v", w.pos, want)
	}
}

func TestButtonReleaseOutsideCancels(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	pos := driver.Point{X: 100, Y: 50}
	l := s.Layout(size)
	start := time.Now()
	outside := driver.Point{X: 200, Y: 100} // title bar drag area, no button

	cases := []struct {
		name string
		rect render.Rect
		btn  Button
	}{
		{"minimize", l.Minimize, ButtonMinimize},
		{"maximize", l.Maximize, ButtonMaximize},
		{"close", l.Close, ButtonClose},
	}
	for _, tc := range cases {
		t.Run(tc.name+" outside", func(t *testing.T) {
			w := &fakeFrameWindow{size: size, pos: pos}
			c := NewController(s)
			center := mid(tc.rect)
			r := c.HandleEvent(pressAt(center), w, add(pos, center), start)
			if !r.Redraw || c.Active() != tc.btn {
				t.Fatalf("press: result %v active %v, want redraw with %v active", r, c.Active(), tc.btn)
			}
			r = c.HandleEvent(moveAt(outside), w, add(pos, outside), start.Add(10*time.Millisecond))
			if !r.Redraw {
				t.Error("hover change should request a redraw")
			}
			if c.Active() != tc.btn {
				t.Fatal("moving out of the button must not clear the active state")
			}
			r = c.HandleEvent(releaseAt(outside), w, add(pos, outside), start.Add(20*time.Millisecond))
			if r.Close {
				t.Fatal("release outside the button must not close the window")
			}
			if !r.Redraw {
				t.Error("releasing an active button should request a redraw")
			}
			if w.iconifyCalls != 0 || w.maximizeCalls != 0 || w.restoreCalls != 0 {
				t.Error("window control called on release outside the button")
			}
			if w.Size() != size || w.pos != pos {
				t.Errorf("geometry changed: size %v pos %v", w.Size(), w.pos)
			}
			if c.Active() != ButtonNone {
				t.Error("active state not cleared after release")
			}
		})
	}

	t.Run("release inside triggers the control", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		center := mid(l.Minimize)
		c.HandleEvent(pressAt(center), w, add(pos, center), start)
		r := c.HandleEvent(releaseAt(center), w, add(pos, center), start.Add(10*time.Millisecond))
		if w.iconifyCalls != 1 {
			t.Fatalf("minimize: Iconify called %d times, want 1", w.iconifyCalls)
		}
		if r.Close || c.Active() != ButtonNone {
			t.Errorf("result %v active %v, want no close and no active button", r, c.Active())
		}

		w = &fakeFrameWindow{size: size, pos: pos}
		c = NewController(s)
		center = mid(l.Maximize)
		c.HandleEvent(pressAt(center), w, add(pos, center), start)
		c.HandleEvent(releaseAt(center), w, add(pos, center), start.Add(10*time.Millisecond))
		if w.maximizeCalls != 1 || w.restoreCalls != 0 || !w.IsMaximized() {
			t.Fatalf("maximize: maximize %d restore %d maximized %v", w.maximizeCalls, w.restoreCalls, w.IsMaximized())
		}
		c.HandleEvent(pressAt(center), w, add(pos, center), start.Add(20*time.Millisecond))
		c.HandleEvent(releaseAt(center), w, add(pos, center), start.Add(30*time.Millisecond))
		if w.restoreCalls != 1 || w.IsMaximized() {
			t.Fatalf("second click should restore: maximize %d restore %d maximized %v", w.maximizeCalls, w.restoreCalls, w.IsMaximized())
		}

		w = &fakeFrameWindow{size: size, pos: pos}
		c = NewController(s)
		center = mid(l.Close)
		c.HandleEvent(pressAt(center), w, add(pos, center), start)
		r = c.HandleEvent(releaseAt(center), w, add(pos, center), start.Add(10*time.Millisecond))
		if !r.Close {
			t.Fatal("release inside the close button must set Result.Close")
		}
		if w.iconifyCalls != 0 || w.maximizeCalls != 0 || w.restoreCalls != 0 {
			t.Error("close button must not touch native controls")
		}
	})
}

func TestResizeClampsMinimumAndPosition(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	pos := driver.Point{X: 100, Y: 100}
	start := time.Now()

	cases := []struct {
		name     string
		press    driver.Point // window-local
		moveTo   driver.Point // screen coordinates
		wantSize driver.Size
		wantPos  driver.Point
	}{
		// Dragging the left edge right by 300 would shrink to 100 wide; the
		// size clamps to 240 and the left edge follows the actual shrink of 160.
		{"left clamps and moves window", driver.Point{X: 2, Y: 150}, driver.Point{X: 402, Y: 250}, driver.Size{Width: 240, Height: 300}, driver.Point{X: 260, Y: 100}},
		{"top clamps and moves window", driver.Point{X: 200, Y: 2}, driver.Point{X: 300, Y: 352}, driver.Size{Width: 400, Height: 120}, driver.Point{X: 100, Y: 280}},
		{"top-left corner clamps both", driver.Point{X: 2, Y: 2}, driver.Point{X: 302, Y: 302}, driver.Size{Width: 240, Height: 120}, driver.Point{X: 260, Y: 280}},
		{"right-bottom grows freely", driver.Point{X: 397, Y: 297}, driver.Point{X: 547, Y: 447}, driver.Size{Width: 450, Height: 350}, driver.Point{X: 100, Y: 100}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeFrameWindow{size: size, pos: pos}
			c := NewController(s)
			c.HandleEvent(pressAt(tc.press), w, add(pos, tc.press), start)
			r := c.HandleEvent(moveAt(tc.press), w, tc.moveTo, start.Add(10*time.Millisecond))
			if !r.Redraw {
				t.Error("resize move should request a redraw")
			}
			if w.Size() != tc.wantSize {
				t.Errorf("size = %v, want %v", w.Size(), tc.wantSize)
			}
			if w.pos != tc.wantPos {
				t.Errorf("pos = %v, want %v", w.pos, tc.wantPos)
			}
			// Releasing ends the gesture: further moves must not resize.
			c.HandleEvent(releaseAt(tc.press), w, tc.moveTo, start.Add(20*time.Millisecond))
			c.HandleEvent(moveAt(tc.press), w, add(tc.moveTo, driver.Point{X: 50, Y: 50}), start.Add(30*time.Millisecond))
			if w.Size() != tc.wantSize || w.pos != tc.wantPos {
				t.Errorf("resize continued after release: size %v pos %v", w.Size(), w.pos)
			}
		})
	}
}

func TestTitleDragAndDoubleClick(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	pos := driver.Point{X: 100, Y: 50}
	base := time.Now()
	title := driver.Point{X: 60, Y: 18} // title bar drag area, no button

	t.Run("drag follows screen delta and resets on release", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		c.HandleEvent(pressAt(title), w, add(pos, title), base)
		r := c.HandleEvent(moveAt(driver.Point{X: 100, Y: 50}), w, driver.Point{X: 200, Y: 100}, base.Add(10*time.Millisecond))
		if r != (Result{}) {
			t.Errorf("drag move result = %v, want zero (window move needs no redraw)", r)
		}
		if w.pos != (driver.Point{X: 140, Y: 82}) {
			t.Errorf("pos = %v, want (140,82)", w.pos)
		}
		if c.Active() != ButtonNone {
			t.Error("dragging must not activate a button")
		}
		r = c.HandleEvent(releaseAt(driver.Point{X: 100, Y: 50}), w, driver.Point{X: 200, Y: 100}, base.Add(20*time.Millisecond))
		if r != (Result{}) {
			t.Errorf("drag release result = %v, want zero", r)
		}
		c.HandleEvent(moveAt(driver.Point{X: 150, Y: 80}), w, driver.Point{X: 250, Y: 130}, base.Add(30*time.Millisecond))
		if w.pos != (driver.Point{X: 140, Y: 82}) {
			t.Errorf("position moved after release: %v", w.pos)
		}
	})

	t.Run("second press within 300ms toggles maximize without dragging", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		c.HandleEvent(pressAt(title), w, add(pos, title), base)
		c.HandleEvent(releaseAt(title), w, add(pos, title), base.Add(10*time.Millisecond))
		r := c.HandleEvent(pressAt(title), w, add(pos, title), base.Add(200*time.Millisecond))
		if !r.Redraw {
			t.Error("double click should request a redraw")
		}
		if w.maximizeCalls != 1 || !w.IsMaximized() {
			t.Fatalf("double click should maximize: maximize %d maximized %v", w.maximizeCalls, w.IsMaximized())
		}
		c.HandleEvent(moveAt(title), w, add(pos, driver.Point{X: 160, Y: 68}), base.Add(210*time.Millisecond))
		if w.pos != pos {
			t.Error("double click must not start a drag")
		}
		c.HandleEvent(releaseAt(title), w, add(pos, title), base.Add(220*time.Millisecond))
		c.HandleEvent(pressAt(title), w, add(pos, title), base.Add(300*time.Millisecond))
		c.HandleEvent(releaseAt(title), w, add(pos, title), base.Add(310*time.Millisecond))
		c.HandleEvent(pressAt(title), w, add(pos, title), base.Add(400*time.Millisecond))
		if w.restoreCalls != 1 || w.IsMaximized() {
			t.Fatalf("second double click should restore: restore %d maximized %v", w.restoreCalls, w.IsMaximized())
		}
	})

	t.Run("second press after 300ms drags instead", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		c.HandleEvent(pressAt(title), w, add(pos, title), base)
		c.HandleEvent(releaseAt(title), w, add(pos, title), base.Add(10*time.Millisecond))
		c.HandleEvent(pressAt(title), w, add(pos, title), base.Add(400*time.Millisecond))
		if w.maximizeCalls != 0 {
			t.Fatal("slow second press must not toggle maximize")
		}
		c.HandleEvent(moveAt(title), w, add(pos, driver.Point{X: 110, Y: 28}), base.Add(410*time.Millisecond))
		if w.pos != (driver.Point{X: 150, Y: 60}) {
			t.Errorf("pos = %v, want (150,60)", w.pos)
		}
	})

	t.Run("button press between clicks invalidates the double click", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		l := s.Layout(size)
		closeCenter := mid(l.Close)
		c.HandleEvent(pressAt(title), w, add(pos, title), base)
		c.HandleEvent(releaseAt(title), w, add(pos, title), base.Add(10*time.Millisecond))
		c.HandleEvent(pressAt(closeCenter), w, add(pos, closeCenter), base.Add(150*time.Millisecond))
		if c.Active() != ButtonClose {
			t.Fatalf("active = %v, want ButtonClose (buttons win over the pending double click)", c.Active())
		}
		if w.maximizeCalls != 0 {
			t.Fatal("button press toggled maximize")
		}
		r := c.HandleEvent(releaseAt(driver.Point{X: 200, Y: 100}), w, add(pos, driver.Point{X: 200, Y: 100}), base.Add(160*time.Millisecond))
		if r.Close {
			t.Fatal("release outside the close button must not close")
		}
		// The pending title press was invalidated: this press is a drag start.
		c.HandleEvent(pressAt(title), w, add(pos, title), base.Add(200*time.Millisecond))
		if w.maximizeCalls != 0 {
			t.Fatal("stale pending press toggled maximize")
		}
		c.HandleEvent(moveAt(title), w, add(pos, driver.Point{X: 110, Y: 28}), base.Add(210*time.Millisecond))
		if w.pos != (driver.Point{X: 150, Y: 60}) {
			t.Errorf("pos = %v, want (150,60)", w.pos)
		}
	})

	t.Run("press in the content area between clicks invalidates the double click", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		content := driver.Point{X: 200, Y: 150} // interior, no button, no edge
		c.HandleEvent(pressAt(title), w, add(pos, title), base)
		c.HandleEvent(releaseAt(title), w, add(pos, title), base.Add(10*time.Millisecond))
		c.HandleEvent(pressAt(content), w, add(pos, content), base.Add(150*time.Millisecond))
		c.HandleEvent(releaseAt(content), w, add(pos, content), base.Add(160*time.Millisecond))
		// Still within 300ms of the first title press, but the pending double
		// click was broken: this press starts a drag instead.
		c.HandleEvent(pressAt(title), w, add(pos, title), base.Add(250*time.Millisecond))
		if w.maximizeCalls != 0 {
			t.Fatal("intervening content press toggled maximize")
		}
		c.HandleEvent(moveAt(title), w, add(pos, driver.Point{X: 110, Y: 28}), base.Add(260*time.Millisecond))
		if w.pos != (driver.Point{X: 150, Y: 60}) {
			t.Errorf("pos = %v, want (150,60)", w.pos)
		}
	})
}

func TestResizeFractionalDeltaAnchorsOppositeEdge(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	pos := driver.Point{X: 100, Y: 100}
	start := time.Now()

	cases := []struct {
		name     string
		press    driver.Point // window-local
		moveTo   driver.Point // screen coordinates
		wantSize driver.Size
		wantPos  driver.Point
	}{
		// dx=0.25 would shrink to 399.75, which rounds back to 400: the
		// position must be recomputed from the actual (rounded) size, so it
		// stays put and the right edge remains anchored.
		{"left sub-pixel delta keeps size and position", driver.Point{X: 2, Y: 150}, driver.Point{X: 102.25, Y: 250}, driver.Size{Width: 400, Height: 300}, driver.Point{X: 100, Y: 100}},
		// dx=0.6 shrinks to 399.4, which rounds to 399: the left edge moves
		// by the full 101 so the right edge stays at 500.
		{"left fraction anchors the rounded size", driver.Point{X: 2, Y: 150}, driver.Point{X: 102.6, Y: 250}, driver.Size{Width: 399, Height: 300}, driver.Point{X: 101, Y: 100}},
		{"top sub-pixel delta keeps size and position", driver.Point{X: 200, Y: 2}, driver.Point{X: 300, Y: 102.25}, driver.Size{Width: 400, Height: 300}, driver.Point{X: 100, Y: 100}},
		{"corner fraction uses rounded size for both axes", driver.Point{X: 2, Y: 2}, driver.Point{X: 102.25, Y: 102.6}, driver.Size{Width: 400, Height: 299}, driver.Point{X: 100, Y: 101}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeFrameWindow{size: size, pos: pos}
			c := NewController(s)
			c.HandleEvent(pressAt(tc.press), w, add(pos, tc.press), start)
			c.HandleEvent(moveAt(tc.press), w, tc.moveTo, start.Add(10*time.Millisecond))
			if w.Size() != tc.wantSize {
				t.Errorf("size = %v, want %v", w.Size(), tc.wantSize)
			}
			if w.pos != tc.wantPos {
				t.Errorf("pos = %v, want %v (right/bottom edge must stay anchored)", w.pos, tc.wantPos)
			}
		})
	}
}

func TestRepeatedPressSupersedesGesture(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	pos := driver.Point{X: 100, Y: 50}
	base := time.Now()
	title := driver.Point{X: 60, Y: 18} // title bar drag area, no button
	l := s.Layout(size)

	t.Run("second title press without release does not drag", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		c.HandleEvent(pressAt(title), w, add(pos, title), base)
		// No release in between: this is the double click.
		c.HandleEvent(pressAt(title), w, add(pos, title), base.Add(100*time.Millisecond))
		if w.maximizeCalls != 1 || !w.IsMaximized() {
			t.Fatalf("second press should toggle maximize: maximize %d maximized %v", w.maximizeCalls, w.IsMaximized())
		}
		if c.Active() != ButtonNone {
			t.Fatalf("active = %v, want ButtonNone after a title press", c.Active())
		}
		c.HandleEvent(moveAt(title), w, add(pos, driver.Point{X: 160, Y: 68}), base.Add(110*time.Millisecond))
		if w.pos != pos {
			t.Errorf("double click kept the first drag alive: pos = %v, want %v", w.pos, pos)
		}
	})

	t.Run("press switches from drag to resize without release", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		c.HandleEvent(pressAt(title), w, add(pos, title), base)
		c.HandleEvent(moveAt(title), w, add(pos, driver.Point{X: 110, Y: 28}), base.Add(10*time.Millisecond))
		if w.pos != (driver.Point{X: 150, Y: 60}) {
			t.Fatalf("drag did not move the window: pos %v", w.pos)
		}
		edge := driver.Point{X: 397, Y: 150} // right edge
		c.HandleEvent(pressAt(edge), w, add(w.pos, edge), base.Add(20*time.Millisecond))
		if c.Active() != ButtonNone {
			t.Fatalf("active = %v, want ButtonNone after an edge press", c.Active())
		}
		c.HandleEvent(moveAt(edge), w, add(w.pos, driver.Point{X: 447, Y: 200}), base.Add(30*time.Millisecond))
		if want := (driver.Size{Width: 450, Height: 300}); w.Size() != want {
			t.Errorf("size = %v, want %v (resize gesture owns the move)", w.Size(), want)
		}
		if w.pos != (driver.Point{X: 150, Y: 60}) {
			t.Errorf("stale drag moved the window during resize: pos = %v", w.pos)
		}
	})

	t.Run("press switches from resize to button without release", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		edge := driver.Point{X: 397, Y: 297} // right-bottom corner
		c.HandleEvent(pressAt(edge), w, add(pos, edge), base)
		c.HandleEvent(moveAt(edge), w, add(pos, driver.Point{X: 447, Y: 347}), base.Add(10*time.Millisecond))
		if want := (driver.Size{Width: 450, Height: 350}); w.Size() != want {
			t.Fatalf("size = %v, want %v", w.Size(), want)
		}
		closeCenter := mid(s.Layout(w.Size()).Close) // layout shifted after the resize
		c.HandleEvent(pressAt(closeCenter), w, add(w.pos, closeCenter), base.Add(20*time.Millisecond))
		if c.Active() != ButtonClose {
			t.Fatalf("active = %v, want ButtonClose", c.Active())
		}
		c.HandleEvent(moveAt(closeCenter), w, add(w.pos, closeCenter), base.Add(30*time.Millisecond))
		if want := (driver.Size{Width: 450, Height: 350}); w.Size() != want {
			t.Errorf("stale resize continued after the button press: size = %v", w.Size())
		}
		r := c.HandleEvent(releaseAt(driver.Point{X: 200, Y: 100}), w, add(w.pos, driver.Point{X: 200, Y: 100}), base.Add(40*time.Millisecond))
		if r.Close {
			t.Fatal("release outside the close button must not close")
		}
	})

	t.Run("press switches from button to drag without release", func(t *testing.T) {
		w := &fakeFrameWindow{size: size, pos: pos}
		c := NewController(s)
		closeCenter := mid(l.Close)
		c.HandleEvent(pressAt(closeCenter), w, add(pos, closeCenter), base)
		if c.Active() != ButtonClose {
			t.Fatalf("active = %v, want ButtonClose", c.Active())
		}
		c.HandleEvent(pressAt(title), w, add(pos, title), base.Add(10*time.Millisecond))
		if c.Active() != ButtonNone {
			t.Fatalf("stale active button after a drag press: %v", c.Active())
		}
		c.HandleEvent(moveAt(title), w, add(pos, driver.Point{X: 110, Y: 28}), base.Add(20*time.Millisecond))
		if w.pos != (driver.Point{X: 150, Y: 60}) {
			t.Errorf("drag did not start: pos = %v", w.pos)
		}
		r := c.HandleEvent(releaseAt(closeCenter), w, add(w.pos, closeCenter), base.Add(30*time.Millisecond))
		if r.Close {
			t.Fatal("stale button gesture fired on release after a drag press")
		}
	})
}
