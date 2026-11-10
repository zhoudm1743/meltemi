package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// smokeWait is how long a smoke-script condition may take to become true
// before the step is reported as failed. Window managers apply moves, resizes
// and state changes asynchronously, so conditions are polled rather than
// asserted once.
const smokeWait = 2 * time.Second

// runSmokeScript exercises the drawn title bar end to end: it drags the
// window by the title bar, resizes it from the bottom-right corner, toggles
// maximize with the drawn button, exercises the native iconify/restore path
// and closes through the drawn close button. Every interaction is synthesized
// as mouse events on the demo's real event dispatch, so the script proves the
// Controller path rather than calling native controls directly.
//
// The first failing step ends the script with that error. It is recorded
// before best-effort cleanup so a cleanup failure cannot replace it; Quit
// always runs last, so an unattended smoke run can never hang the app.
func runSmokeScript(d *demo) (err error) {
	defer func() {
		d.noteErr(err)
		d.cleanup()
		d.a.Quit()
	}()
	if d.win == nil || d.st == nil {
		return errors.New("meltemi-frameless: smoke: window is not open")
	}
	w := d.win

	if err := d.awaitSettled(w); err != nil {
		return err
	}
	if err := smokeDrag(d, w); err != nil {
		return err
	}
	if err := smokeResize(d, w); err != nil {
		return err
	}
	if err := smokeMaximize(d, w); err != nil {
		return err
	}
	if err := smokeIconify(d, w); err != nil {
		return err
	}
	if err := smokeClose(d, w); err != nil {
		return err
	}
	if d.err != nil {
		return fmt.Errorf("meltemi-frameless: smoke: %w", d.err)
	}
	return nil
}

// smokeEvent builds a left-button mouse event the way the GLFW backend does —
// local position, window attached — so it flows through the real handleEvent
// dispatch into the Controller.
func smokeEvent(d *demo, kind driver.EventKind, p driver.Point, action driver.Action) driver.Event {
	return driver.Event{
		Kind:     kind,
		Window:   d.win,
		Position: p,
		Button:   driver.MouseButtonLeft,
		Action:   action,
	}
}

func smokePress(d *demo, p driver.Point) {
	d.handleEvent(smokeEvent(d, driver.EventMouseButton, p, driver.ActionPress))
}

func smokeRelease(d *demo, p driver.Point) {
	d.handleEvent(smokeEvent(d, driver.EventMouseButton, p, driver.ActionRelease))
}

func smokeMove(d *demo, p driver.Point) {
	d.handleEvent(smokeEvent(d, driver.EventMouseMove, p, 0))
}

// smokeDrag presses the title bar, moves the pointer 30x20 screen units and
// releases, then waits for the move to be applied.
func smokeDrag(d *demo, w driver.FrameWindow) error {
	start := w.Position()
	press := driver.Point{X: 120, Y: 10} // drag area: no icon, button or resize edge
	to := driver.Point{X: press.X + 30, Y: press.Y + 20}
	smokePress(d, press)
	smokeMove(d, to)
	smokeRelease(d, to)
	want := driver.Point{X: start.X + 30, Y: start.Y + 20}
	return d.await(func() bool { return w.Position() == want },
		fmt.Sprintf("window position %v after title bar drag", want))
}

// smokeResize drags the bottom-right corner 30x20 out, waits for the new size
// and checks it grew and never went below the style minimum.
func smokeResize(d *demo, w driver.FrameWindow) error {
	size := w.Size()
	corner := driver.Point{X: float64(size.Width) - 2, Y: float64(size.Height) - 2}
	to := driver.Point{X: corner.X + 30, Y: corner.Y + 20}
	smokePress(d, corner)
	smokeMove(d, to)
	smokeRelease(d, to)
	want := driver.Size{Width: size.Width + 30, Height: size.Height + 20}
	if err := d.await(func() bool { return w.Size() == want },
		fmt.Sprintf("window size %v after corner resize", want)); err != nil {
		return err
	}
	if got := w.Size(); got.Width < d.style.MinSize.Width || got.Height < d.style.MinSize.Height {
		return fmt.Errorf("meltemi-frameless: smoke: resized size %v below minimum %v", got, d.style.MinSize)
	}
	return nil
}

// smokeMaximize clicks the drawn maximize button twice: the first click
// maximizes, the second restores. The button center is recomputed after the
// state change because the layout depends on the window size.
func smokeMaximize(d *demo, w driver.FrameWindow) error {
	center := mid(d.style.Layout(w.Size()).Maximize)
	smokePress(d, center)
	smokeRelease(d, center)
	if err := d.await(func() bool { return w.IsMaximized() },
		"window maximized by the drawn maximize button"); err != nil {
		return err
	}
	center = mid(d.style.Layout(w.Size()).Maximize)
	smokePress(d, center)
	smokeRelease(d, center)
	return d.await(func() bool { return !w.IsMaximized() },
		"window restored by the second maximize click")
}

// smokeIconify exercises the native iconify/restore control path: Iconify,
// then wait until the window really is iconified, then Restore and wait
// until it is back to normal. Waiting for the first transition before
// issuing the second also keeps window managers from coalescing the two
// immediate calls into one no-op.
func smokeIconify(d *demo, w driver.FrameWindow) error {
	w.Iconify()
	if err := d.await(func() bool { return w.IsIconified() },
		"window to become iconified after Iconify"); err != nil {
		return err
	}
	w.Restore()
	return d.await(func() bool { return !w.IsIconified() },
		"window to return to normal after Restore")
}

// smokeClose clicks the drawn close button and expects the unified gated
// close to release the whole window state.
func smokeClose(d *demo, w driver.FrameWindow) error {
	center := mid(d.style.Layout(w.Size()).Close)
	smokePress(d, center)
	smokeRelease(d, center)
	if d.win != nil || d.st != nil {
		return errors.New("meltemi-frameless: smoke: close button did not close the window")
	}
	return nil
}

// await polls cond until it holds or the smoke deadline passes. Between polls
// it pumps native events when a backend is wired, so asynchronously applied
// window-manager state can catch up. Tests run against synchronous fakes
// with no pump, where every condition either holds at once or never will.
func (d *demo) await(cond func() bool, what string) error {
	deadline := time.Now().Add(d.smokeTimeout)
	for {
		if cond() {
			return nil
		}
		if d.pump != nil {
			d.pump()
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("meltemi-frameless: smoke: timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// awaitSettled waits until the freshly shown window's position reads stable
// across several polls, i.e. the window manager finished mapping and placing
// it; only then are later position assertions reliable.
func (d *demo) awaitSettled(w driver.FrameWindow) error {
	prev := w.Position()
	stable := 0
	return d.await(func() bool {
		cur := w.Position()
		if cur == prev {
			stable++
			return stable >= 3
		}
		stable = 0
		prev = cur
		return false
	}, "window position to settle")
}

func mid(r render.Rect) driver.Point {
	return driver.Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}
}
