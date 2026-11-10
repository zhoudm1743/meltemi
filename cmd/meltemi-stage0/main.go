// Command meltemi-stage0 is the stage zero end-to-end demo: it opens two native
// windows on one app, draws different backgrounds, rectangles and the text
// "Meltemi stage 0" in each with an independent OpenGL renderer, and shows the
// event and cross-goroutine posting paths. With --smoke it closes both
// windows automatically and quits, for unattended verification.
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
)

// demoApp is the seam the smoke plan runs against: the real demo and the
// tests both implement it, so closeAllAndQuit needs no knowledge of
// renderers or native windows.
type demoApp interface {
	Windows() []driver.Window
	CloseWindow(driver.Window) error
	Quit()
}

// closeAllAndQuit closes every window in the copy Windows returns and only
// then quits. It runs on the UI thread; a close failure aborts the plan
// before Quit so the app keeps running instead of exiting half-closed.
func closeAllAndQuit(a demoApp) error {
	for _, w := range a.Windows() {
		if err := a.CloseWindow(w); err != nil {
			return fmt.Errorf("meltemi-stage0: close window: %w", err)
		}
	}
	a.Quit()
	return nil
}

// renderer is the slice of *gl.Renderer the demo drives; the seam exists so
// error paths can be tested deterministically without a GPU.
type renderer interface {
	Begin(render.Color) error
	DrawRect(render.Rect, render.Color)
	DrawText(string, driver.Point, render.Color, *text.Face) error
	End() error
	Close()
}

// windowState is the per-window demo: its own renderer, look and typed text.
// All fields are only touched on the UI thread.
type windowState struct {
	target  render.Target
	r       renderer
	bg      render.Color
	rect    render.Rect
	base    render.Color
	alt     render.Color
	toggled bool
	text    string
}

func (s *windowState) rectColor() render.Color {
	if s.toggled {
		return s.alt
	}
	return s.base
}

// demo owns the app and the live window states. Every method runs on the UI
// thread: from the Run loop, from Posted functions or from window handlers.
type demo struct {
	a      *app.App
	face   *text.Face
	states map[driver.Window]*windowState
	order  []driver.Window // creation order, used for ordered init cleanup
	second driver.Window   // rectangle the background goroutine toggles
	// newRenderer builds a window's renderer; the seam tests use to inject
	// GPU-free doubles and deterministic failures.
	newRenderer func(render.Target) (renderer, error)
	err         error // first error seen, read by main after Run returns
}

// newDemo returns the demo; init is Posted to the app before Run.
func newDemo(a *app.App) *demo {
	return &demo{
		a:           a,
		states:      make(map[driver.Window]*windowState),
		newRenderer: func(t render.Target) (renderer, error) { return gl.New(t) },
	}
}

// init creates the two windows, their renderers and the shared face, and
// draws the first frame of each window. Any failure is recorded and quits
// the app so Run returns instead of waiting forever.
func (d *demo) init() {
	face, err := text.NewFace(goregular.TTF, 24)
	if err != nil {
		d.fail(err)
		return
	}
	d.face = face

	_, err = d.open(driver.Config{
		Title: "Meltemi stage 0 [640x400]",
		Size:  driver.Size{Width: 640, Height: 400},
	}, windowState{
		bg:   render.Color{R: 0.13, G: 0.15, B: 0.20, A: 1},
		rect: render.Rect{X: 24, Y: 72, Width: 140, Height: 72},
		base: render.Color{R: 0.20, G: 0.60, B: 0.55, A: 1},
		alt:  render.Color{R: 0.95, G: 0.75, B: 0.20, A: 1},
	})
	if err != nil {
		d.fail(err)
		return
	}

	win2, err := d.open(driver.Config{
		Title: "Meltemi stage 0 [400x240]",
		Size:  driver.Size{Width: 400, Height: 240},
	}, windowState{
		bg:   render.Color{R: 0.17, G: 0.13, B: 0.11, A: 1},
		rect: render.Rect{X: 24, Y: 72, Width: 140, Height: 72},
		base: render.Color{R: 0.85, G: 0.40, B: 0.25, A: 1},
		alt:  render.Color{R: 0.35, G: 0.70, B: 0.30, A: 1},
	})
	if err != nil {
		d.fail(err)
		return
	}
	d.second = win2
}

// open creates one window and its renderer and draws its first frame.
func (d *demo) open(cfg driver.Config, st windowState) (driver.Window, error) {
	w, err := d.a.NewWindow(cfg)
	if err != nil {
		return nil, err
	}
	t, ok := w.(render.Target)
	if !ok {
		d.a.CloseWindow(w)
		return nil, fmt.Errorf("meltemi-stage0: window %T is not a render target", w)
	}
	r, err := d.newRenderer(t)
	if err != nil {
		d.a.CloseWindow(w)
		return nil, err
	}
	st.target = t
	st.r = r
	st.text = "Meltemi stage 0"
	d.states[w] = &st
	d.order = append(d.order, w)
	w.SetHandler(d.handleEvent)
	w.Show()
	if err := d.redraw(w); err != nil {
		return nil, fmt.Errorf("meltemi-stage0: first frame: %w", err)
	}
	return w, nil
}

// handleEvent redraws on demand only: resize, framebuffer resize and paint
// repaint the affected window, text input extends that window's demo text,
// Escape and the close button close only that window. Nothing here redraws
// continuously.
func (d *demo) handleEvent(e driver.Event) {
	switch e.Kind {
	case driver.EventCloseRequested:
		d.closeFromEvent(e.Window)
	case driver.EventResize, driver.EventFramebufferResize, driver.EventPaint:
		d.noteErr(d.redraw(e.Window))
	case driver.EventRune:
		if st := d.states[e.Window]; st != nil {
			st.text += string(e.Rune)
			d.noteErr(d.redraw(e.Window))
		}
	case driver.EventKey:
		if e.Key == driver.KeyEscape && e.Action == driver.ActionPress {
			d.closeFromEvent(e.Window)
		}
	}
}

// closeFromEvent closes one window and applies the demo policy: only when no
// window is left does the demo quit. Closing one window never exits the app.
func (d *demo) closeFromEvent(w driver.Window) {
	if err := d.CloseWindow(w); err != nil {
		d.noteErr(err)
	}
	if len(d.a.Windows()) == 0 {
		d.a.Quit()
	}
}

// redraw draws one window's current state and returns the first frame error,
// if any. It is only called on demand. The init path (open) treats a returned
// error as init failure; runtime callers only record it via noteErr and keep
// running.
func (d *demo) redraw(w driver.Window) error {
	st := d.states[w]
	if st == nil || d.face == nil {
		return nil
	}
	if err := st.r.Begin(st.bg); err != nil {
		return err
	}
	st.r.DrawRect(st.rect, st.rectColor())
	var first error
	if err := st.r.DrawText(st.text, driver.Point{X: 24, Y: 20}, render.Color{R: 1, G: 1, B: 1, A: 1}, d.face); err != nil {
		first = err
	}
	if err := st.r.End(); err != nil && first == nil {
		first = err
	}
	return first
}

// CloseWindow releases the window's renderer while its context is still
// current and valid — Renderer.Close frees the text cache too — and then
// destroys the window. The deferred native destroy runs later outside the
// GLFW callback stack. It implements demoApp and is the only close path.
//
// Binding the context is the gate for everything that follows: when
// MakeCurrent fails the current context may belong to another independent
// window, and deleting GL objects then could free that renderer's objects
// of the same name. The close aborts untouched — no renderer release, no
// state removal, no window destroy.
func (d *demo) CloseWindow(w driver.Window) error {
	st := d.states[w]
	if st == nil {
		return d.a.CloseWindow(w)
	}
	if err := st.target.MakeCurrent(); err != nil {
		return fmt.Errorf("meltemi-stage0: make context current: %w", err)
	}
	st.r.Close()
	delete(d.states, w)
	var first error
	if len(d.states) == 0 && d.face != nil {
		if err := d.face.Close(); err != nil {
			first = err
		}
		d.face = nil
	}
	if err := d.a.CloseWindow(w); err != nil && first == nil {
		first = err
	}
	return first
}

// Windows returns the live window set. It implements demoApp.
func (d *demo) Windows() []driver.Window { return d.a.Windows() }

// Quit implements demoApp.
func (d *demo) Quit() { d.a.Quit() }

// smokeFinish is Posted by the smoke timer: it first redraws both windows on
// the UI thread as the drawing verification, then runs the smoke plan.
func (d *demo) smokeFinish() {
	for w := range d.states {
		d.noteErr(d.redraw(w))
	}
	if err := closeAllAndQuit(d); err != nil {
		d.noteErr(err)
		d.a.Quit() // never let a failed close turn the smoke run into a hang
	}
}

func (d *demo) noteErr(err error) {
	if err != nil && d.err == nil {
		d.err = err
	}
}

// fail records an init error, releases everything init established and quits
// so Run returns promptly. The original error wins: cleanup problems are
// only recorded while no earlier error exists.
func (d *demo) fail(err error) {
	d.noteErr(err)
	d.cleanup()
	d.a.Quit()
}

// cleanup unwinds init in reverse creation order through the regular close
// path — renderer, then window, face last — so nothing established is left
// to the process teardown. It is best-effort: a window whose context cannot
// be made current stays untouched (CloseWindow guarantees that) and does not
// stop the remaining releases.
func (d *demo) cleanup() {
	for i := len(d.order) - 1; i >= 0; i-- {
		w := d.order[i]
		if _, ok := d.states[w]; !ok {
			continue
		}
		if err := d.CloseWindow(w); err != nil {
			d.noteErr(err)
		}
	}
	if d.face != nil {
		if err := d.face.Close(); err != nil {
			d.noteErr(err)
		}
		d.face = nil
	}
}

func main() {
	smoke := flag.Bool("smoke", false, "open both windows, verify drawing, close them and quit")
	flag.Parse()

	a := app.New(glfw.New())
	d := newDemo(a)
	if err := a.Post(d.init); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Prove the cross-goroutine update path: a worker goroutine Posts a
	// rectangle color toggle; the UI thread applies it and repaints.
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = a.Post(func() {
			if st := d.states[d.second]; st != nil {
				st.toggled = !st.toggled
				d.noteErr(d.redraw(d.second))
			}
		})
	}()

	// Smoke mode: after the windows have been created, drawn, toggled and
	// event-processed, verify drawing once more on the UI thread and then
	// close both windows and quit. The timer callback never touches UI
	// state directly; it only Posts to the UI thread.
	if *smoke {
		time.AfterFunc(600*time.Millisecond, func() {
			_ = a.Post(d.smokeFinish)
		})
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
