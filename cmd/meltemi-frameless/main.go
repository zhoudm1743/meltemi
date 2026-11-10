// Command meltemi-frameless is the frameless window demo: it opens one native
// window without system decorations and draws its own title bar — icon,
// title and minimize/maximize/close buttons with hover and press states —
// using the windowframe package for layout and interaction. The title bar
// drags the window, double-click toggles maximize, and the window resizes
// from its edges and corners. With --smoke it drives all of that through
// synthesized Controller events and quits, for unattended verification.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/app"
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/driver/glfw"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/render/gl"
	"github.com/zhoudm1743/meltemi/internal/text"
	"github.com/zhoudm1743/meltemi/internal/windowframe"
)

// windowTitle is drawn in the title bar and passed to the native backend,
// which uses it for task bar entries and other shell chrome.
const windowTitle = "Meltemi frameless"

// demo owns the app and the single frameless window's state. Every method
// runs on the UI thread: from the Run loop, from Posted functions or from
// window handlers.
type demo struct {
	a     *app.App
	face  *text.Face
	style windowframe.Style
	ctl   *windowframe.Controller

	win driver.FrameWindow // the single frameless window
	st  *windowState

	// newRenderer builds the window's renderer; the seam tests use to inject
	// GPU-free doubles and deterministic failures.
	newRenderer func(render.Target) (renderer, error)
	// closeFace releases the shared face; tests inject a failure after the real
	// close to exercise cleanup that partially succeeds.
	closeFace func(*text.Face) error
	// pump processes pending native events; main wires it to the backend so
	// the smoke script can wait on asynchronously applied window-manager
	// state. Tests leave it nil: fakes apply state synchronously.
	pump func()
	// smokeTimeout bounds each smoke-script state wait; tests shrink it so
	// no-op state fakes hit the deadline quickly.
	smokeTimeout time.Duration
	// handled counts the mouse and cursor events routed through the Controller;
	// the tests pin the smoke script on this to prove it never bypasses the
	// drawn title bar with direct native calls.
	handled int

	err error // first error seen, read by main after Run returns
}

// newDemo returns the demo; init is Posted to the app before Run.
func newDemo(a *app.App) *demo {
	return &demo{
		a:            a,
		style:        windowframe.DefaultStyle(),
		newRenderer:  func(t render.Target) (renderer, error) { return gl.New(t) },
		closeFace:    func(f *text.Face) error { return f.Close() },
		smokeTimeout: smokeWait,
	}
}

// screenPoint converts a window-local event position into screen coordinates
// by adding the window's position, which is what the Controller's drag and
// resize deltas are measured in.
func screenPoint(w driver.FrameWindow, p driver.Point) driver.Point {
	pos := w.Position()
	return driver.Point{X: pos.X + p.X, Y: pos.Y + p.Y}
}

// init creates the frameless window, its renderer, the shared face and the
// title bar controller, and draws the first frame. Any failure is recorded
// and quits the app so Run returns instead of waiting forever.
func (d *demo) init() {
	face, err := text.NewFace(goregular.TTF, 16)
	if err != nil {
		d.fail(err)
		return
	}
	d.face = face
	d.ctl = windowframe.NewController(d.style)

	w, err := d.a.NewWindow(driver.Config{
		Title:     windowTitle,
		Size:      driver.Size{Width: 720, Height: 480},
		Frameless: true,
	})
	if err != nil {
		d.fail(err)
		return
	}
	if err := d.adopt(w); err != nil {
		d.fail(err)
		return
	}
}

// adopt takes ownership of a freshly created window: it checks the window
// supports rendering and frame controls, builds its renderer, installs the
// event handler, shows the window and draws the first frame. A failure here
// fails init; the partially adopted window is released by fail's cleanup
// through the regular close path.
func (d *demo) adopt(w driver.Window) error {
	abort := func(err error) error {
		_ = d.a.CloseWindow(w)
		return err
	}
	t, ok := w.(render.Target)
	if !ok {
		return abort(fmt.Errorf("meltemi-frameless: window %T is not a render target", w))
	}
	fw, ok := w.(driver.FrameWindow)
	if !ok {
		return abort(fmt.Errorf("meltemi-frameless: window %T is not a frame window", w))
	}
	r, err := d.newRenderer(t)
	if err != nil {
		return abort(err)
	}
	d.win = fw
	d.st = &windowState{target: t, r: r}
	w.SetHandler(d.handleEvent)
	w.Show()
	if err := d.drawFrame(); err != nil {
		return fmt.Errorf("meltemi-frameless: first frame: %w", err)
	}
	return nil
}

// handleEvent dispatches one window event. Native close requests and Escape
// close through the unified CloseWindow; resize, framebuffer resize and paint
// repaint on demand; mouse and cursor-leave events go to the title bar
// Controller, which answers with a redraw or a close request. Nothing here
// redraws continuously.
func (d *demo) handleEvent(e driver.Event) {
	switch e.Kind {
	case driver.EventCloseRequested:
		d.closeFromEvent(e.Window)
	case driver.EventResize, driver.EventFramebufferResize, driver.EventPaint:
		d.noteErr(d.drawFrame())
	case driver.EventKey:
		if e.Key == driver.KeyEscape && e.Action == driver.ActionPress {
			d.closeFromEvent(e.Window)
		}
	case driver.EventMouseMove, driver.EventMouseButton, driver.EventCursorLeave:
		if d.win == nil || d.st == nil {
			return
		}
		screen := screenPoint(d.win, e.Position)
		d.handled++
		res := d.ctl.HandleEvent(e, d.win, screen, time.Now())
		switch {
		case res.Close:
			d.closeFromEvent(e.Window)
		case res.Redraw:
			d.noteErr(d.drawFrame())
		}
	}
}

// closeFromEvent is the unified close path for the native close request,
// Escape and the drawn close button. The single-window demo quits once the
// window is gone, even when releasing its face reported an error. A failed
// close keeps the app alive only while the window remains registered.
func (d *demo) closeFromEvent(w driver.Window) {
	d.noteErr(d.CloseWindow(w))
	for _, open := range d.a.Windows() {
		if open == w {
			return
		}
	}
	d.a.Quit()
}

// CloseWindow releases the window's renderer while its context is still
// current and valid — Renderer.Close frees GL text state too — then closes
// the face and destroys the window. Binding the context is the gate for
// everything that follows: when MakeCurrent fails the current context may
// belong to another independent window, and deleting GL objects then could
// free that renderer's objects of the same name. The close aborts untouched
// — no renderer release, no state removal, no window destroy, face kept.
func (d *demo) CloseWindow(w driver.Window) error {
	if d.win != nil && w == d.win && d.st != nil {
		st := d.st
		if err := st.target.MakeCurrent(); err != nil {
			return fmt.Errorf("meltemi-frameless: make context current: %w", err)
		}
		st.r.Close()
		d.st = nil
		var first error
		if d.face != nil {
			if err := d.closeFace(d.face); err != nil {
				first = err
			}
			d.face = nil
		}
		if err := d.a.CloseWindow(w); err != nil {
			if first == nil {
				first = err
			}
			return first
		}
		d.win = nil
		return first
	}
	// A window the demo never finished adopting: no renderer or face to
	// release, just unregister and destroy it.
	if err := d.a.CloseWindow(w); err != nil {
		return err
	}
	if d.win != nil && w == d.win {
		d.win = nil
	}
	return nil
}

func (d *demo) noteErr(err error) {
	if err != nil && d.err == nil {
		d.err = err
	}
}

// fail records an init error, releases everything init established and quits
// so Run returns promptly. The original error wins: cleanup problems are only
// recorded while no earlier error exists.
func (d *demo) fail(err error) {
	d.noteErr(err)
	d.cleanup()
	d.a.Quit()
}

// cleanup unwinds init through the regular close path so nothing established
// is left to process teardown. It is best-effort: a window whose context
// cannot be made current keeps its renderer and registration, and that failure
// does not stop the remaining releases.
func (d *demo) cleanup() {
	if d.win != nil {
		if err := d.CloseWindow(d.win); err != nil {
			d.noteErr(err)
		}
	}
	if d.face != nil {
		if err := d.closeFace(d.face); err != nil {
			d.noteErr(err)
		}
		d.face = nil
	}
}

func main() {
	smoke := flag.Bool("smoke", false, "drive the drawn title bar controls with synthesized events, then quit")
	flag.Parse()

	backend := glfw.New()
	a := app.New(backend)
	d := newDemo(a)
	d.pump = backend.PollEvents

	if err := a.Post(d.init); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *smoke {
		// Posted after init: the app drains tasks in FIFO order, so the
		// script starts on the UI thread only after the window exists and its
		// first frame has been drawn. No timer goroutine ever touches UI
		// state; runSmokeScript always ends in Quit, and its error — the
		// first one, per step — becomes the process exit code via d.err.
		if err := a.Post(func() { d.noteErr(runSmokeScript(d)) }); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if err := a.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if d.err != nil {
		fmt.Fprintln(os.Stderr, d.err)
		os.Exit(1)
	}
}
