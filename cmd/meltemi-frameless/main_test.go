package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/internal/app"
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
	"github.com/zhoudm1743/meltemi/internal/windowframe"
)

// recorder collects named events so tests can assert the exact release order
// of the close and cleanup paths.
type recorder struct{ events []string }

func (r *recorder) add(event string) { r.events = append(r.events, event) }

// fakeFrameTarget is a driver.FrameWindow and render.Target double. Geometry
// mutators apply synchronously and record their calls, so tests can pin that
// the smoke script drives the window through Controller events rather than by
// calling native controls directly.
//
// The iconified state is a small poll-driven machine that models a window
// manager applying Iconify/Restore asynchronously: the state lags the call by
// a number of IsIconified polls, or never changes at all (no-op fakes), so
// tests can prove the smoke script waits for real state instead of assuming
// instant transitions.
type fakeFrameTarget struct {
	rec       *recorder
	name      string
	size      driver.Size
	pos       driver.Point
	maximized bool

	makeErr   error
	destroyed bool

	iconifyCalls   int
	maximizeCalls  int
	restoreCalls   int
	setSizeLog     []driver.Size
	setPositionLog []driver.Point

	lastAction  int // 0 none, 1 iconify, 2 restore: which control was called last
	actionPolls int // IsIconified polls since the last control call
	iconifyLag  int // polls before Iconify shows in IsIconified
	restoreLag  int // polls before Restore shows in IsIconified
	iconifyNoop bool
	restoreNoop bool
}

func (f *fakeFrameTarget) Show()                     {}
func (f *fakeFrameTarget) Hide()                     {}
func (f *fakeFrameTarget) SetHandler(driver.Handler) {}
func (f *fakeFrameTarget) Destroy() {
	f.destroyed = true
	f.rec.add("destroy:" + f.name)
}
func (f *fakeFrameTarget) Size() driver.Size { return f.size }
func (f *fakeFrameTarget) SetSize(s driver.Size) error {
	f.size = s
	f.setSizeLog = append(f.setSizeLog, s)
	return nil
}
func (f *fakeFrameTarget) Position() driver.Point { return f.pos }
func (f *fakeFrameTarget) SetPosition(p driver.Point) {
	f.pos = p
	f.setPositionLog = append(f.setPositionLog, p)
}
func (f *fakeFrameTarget) Iconify() {
	f.iconifyCalls++
	f.lastAction = 1
	f.actionPolls = 0
}
func (f *fakeFrameTarget) Maximize() { f.maximized = true; f.maximizeCalls++ }
func (f *fakeFrameTarget) Restore() {
	f.restoreCalls++
	f.maximized = false
	f.lastAction = 2
	f.actionPolls = 0
}
func (f *fakeFrameTarget) IsMaximized() bool {
	return f.maximized
}
func (f *fakeFrameTarget) IsIconified() bool {
	f.actionPolls++
	if f.iconifyNoop {
		return false
	}
	if f.restoreNoop {
		return true
	}
	switch f.lastAction {
	case 1:
		return f.actionPolls > f.iconifyLag
	case 2:
		return f.actionPolls <= f.restoreLag
	}
	return false
}
func (f *fakeFrameTarget) MakeCurrent() error {
	if f.makeErr != nil {
		return f.makeErr
	}
	f.rec.add("makecurrent:" + f.name)
	return nil
}
func (f *fakeFrameTarget) FramebufferSize() driver.Size { return f.size }
func (f *fakeFrameTarget) Present()                     {}

type renderOperation struct {
	kind string
	rect render.Rect
	text string
}

// fakeRenderer implements the demo's renderer seam and records drawing and
// Close. The error fields let a test fail an individual frame stage.
type fakeRenderer struct {
	rec      *recorder
	name     string
	closed   bool
	frames   int
	ops      []renderOperation
	beginErr error
	drawErr  error
	endErr   error
}

func (f *fakeRenderer) Begin(render.Color) error {
	f.frames++
	return f.beginErr
}
func (f *fakeRenderer) DrawRect(rect render.Rect, _ render.Color) {
	f.ops = append(f.ops, renderOperation{kind: "rect", rect: rect})
}
func (f *fakeRenderer) DrawText(value string, _ driver.Point, _ render.Color, _ *text.Face) error {
	f.ops = append(f.ops, renderOperation{kind: "text", text: value})
	return f.drawErr
}
func (f *fakeRenderer) SetClip(rect render.Rect) {
	f.ops = append(f.ops, renderOperation{kind: "clip", rect: rect})
}
func (f *fakeRenderer) ClearClip() {
	f.ops = append(f.ops, renderOperation{kind: "clear-clip"})
}
func (f *fakeRenderer) End() error { return f.endErr }
func (f *fakeRenderer) Close() {
	f.closed = true
	f.rec.add("close-renderer:" + f.name)
}

// queueBackend serves one scripted window per NewWindow call.
type queueBackend struct{ queue []driver.Window }

func (b *queueBackend) Init() error { return nil }
func (b *queueBackend) Terminate()  {}
func (b *queueBackend) PollEvents() {}
func (b *queueBackend) WaitEvents() {}
func (b *queueBackend) Wake()       {}
func (b *queueBackend) NewWindow(driver.Config) (driver.Window, error) {
	if len(b.queue) == 0 {
		return nil, errors.New("fake: no window scripted")
	}
	w := b.queue[0]
	b.queue = b.queue[1:]
	return w, nil
}

// newFakeDemo builds a demo whose single window is the fakeFrameTarget and
// whose renderer is the fakeRenderer, both recording into rec.
func newFakeDemo(rec *recorder, w *fakeFrameTarget) (*demo, *fakeRenderer) {
	r := &fakeRenderer{rec: rec, name: w.name}
	a := app.New(&queueBackend{queue: []driver.Window{w}})
	d := newDemo(a)
	d.newRenderer = func(render.Target) (renderer, error) { return r, nil }
	return d, r
}

func TestScreenPoint(t *testing.T) {
	w := &fakeFrameTarget{rec: &recorder{}, name: "win", pos: driver.Point{X: 100, Y: 50}}

	got := screenPoint(w, driver.Point{X: 12, Y: 8})
	want := driver.Point{X: 112, Y: 58}
	if got != want {
		t.Fatalf("screenPoint = %v, want %v", got, want)
	}
}

func TestDemoCursorLeaveClearsHoverAndRedraws(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{rec: rec, name: "win", size: driver.Size{Width: 720, Height: 480}}
	d, rend := newFakeDemo(rec, tw)
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}
	center := mid(d.style.Layout(tw.Size()).Close)
	d.handleEvent(driver.Event{Kind: driver.EventMouseMove, Window: tw, Position: center})
	if d.ctl.Hover() != windowframe.ButtonClose {
		t.Fatalf("hover before leave = %v, want ButtonClose", d.ctl.Hover())
	}
	before := rend.frames

	d.handleEvent(driver.Event{Kind: driver.EventCursorLeave, Window: tw})

	if d.ctl.Hover() != windowframe.ButtonNone {
		t.Errorf("hover after leave = %v, want ButtonNone", d.ctl.Hover())
	}
	if rend.frames != before+1 {
		t.Errorf("frame count after leave = %d, want %d (exactly one redraw)", rend.frames, before+1)
	}
}

func TestCloseWindowMakeCurrentFailurePreservesState(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{rec: rec, name: "win", size: driver.Size{Width: 720, Height: 480}}
	d, rend := newFakeDemo(rec, tw)
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}

	makeErr := errors.New("fake: make current failed")
	tw.makeErr = makeErr

	err := d.CloseWindow(tw)
	if !errors.Is(err, makeErr) {
		t.Fatalf("CloseWindow error = %v, want it to wrap %v", err, makeErr)
	}
	if rend.closed {
		t.Error("renderer.Close ran although the context never became current")
	}
	if tw.destroyed {
		t.Error("window was destroyed although the context never became current")
	}
	if d.st == nil {
		t.Error("window state was deleted although the window is still open")
	}
	if d.face == nil {
		t.Error("face was closed although the window is still open")
	}
	if d.win == nil {
		t.Error("demo forgot the window although the close failed")
	}
	if got := d.a.Windows(); len(got) != 1 {
		t.Fatalf("app owns %d windows, want 1 (close must not unregister)", len(got))
	}
	for _, event := range rec.events {
		if event == "close-renderer:win" || event == "destroy:win" {
			t.Fatalf("release event %q recorded despite MakeCurrent failure", event)
		}
	}
}

func TestCloseFromEventQuitsAfterFaceCloseError(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{rec: rec, name: "win", size: driver.Size{Width: 720, Height: 480}}
	d, rend := newFakeDemo(rec, tw)
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}

	closeErr := errors.New("fake: face close failed")
	d.closeFace = func(face *text.Face) error {
		if err := face.Close(); err != nil {
			t.Fatalf("real Face.Close: %v", err)
		}
		return closeErr
	}
	d.closeFromEvent(tw)

	if !errors.Is(d.err, closeErr) {
		t.Errorf("demo.err = %v, want it to wrap %v", d.err, closeErr)
	}
	if !rend.closed || !tw.destroyed {
		t.Error("successful window destruction must still release the renderer and destroy the window")
	}
	if d.st != nil || d.win != nil || d.face != nil {
		t.Errorf("state left behind: st=%v win=%v face=%v, want all released",
			d.st != nil, d.win != nil, d.face != nil)
	}
	if got := d.a.Windows(); len(got) != 0 {
		t.Fatalf("app owns %d windows after close, want 0", len(got))
	}
	if err := d.a.Post(func() {}); !errors.Is(err, app.ErrClosed) {
		t.Errorf("Post after destroyed-window close error = %v, want app.ErrClosed", err)
	}
}

func TestFirstFrameErrorPropagatesAndReleasesInOrder(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{rec: rec, name: "win", size: driver.Size{Width: 720, Height: 480}}
	d, rend := newFakeDemo(rec, tw)
	endErr := errors.New("fake: end failed")
	rend.endErr = endErr

	d.init()

	if !errors.Is(d.err, endErr) {
		t.Fatalf("demo.err = %v, want the first-frame error %v", d.err, endErr)
	}
	if !rend.closed {
		t.Error("renderer was not closed during init cleanup")
	}
	if !tw.destroyed {
		t.Error("window was not destroyed during init cleanup")
	}
	if d.st != nil || d.win != nil {
		t.Fatalf("cleanup left st=%v win=%v, want both nil", d.st != nil, d.win != nil)
	}
	if d.face != nil {
		t.Error("face was not closed during init cleanup")
	}
	if got := d.a.Windows(); len(got) != 0 {
		t.Fatalf("app owns %d windows after cleanup, want 0", len(got))
	}
	want := []string{"makecurrent:win", "close-renderer:win", "destroy:win"}
	if !reflect.DeepEqual(rec.events, want) {
		t.Fatalf("release order = %v, want %v", rec.events, want)
	}
}

// TestSmokeScriptUsesTitleBarControls pins that runSmokeScript drives the
// window exclusively through Controller events synthesized on the demo's real
// event path: dragging moves the window by the pointer delta, corner resizing
// grows it (never below MinSize), the maximize button toggles
// maximize/restore, the iconify path is exercised, and the close button runs
// the unified gated close.
func TestSmokeScriptUsesTitleBarControls(t *testing.T) {
	rec := &recorder{}
	start := driver.Point{X: 100, Y: 50}
	tw := &fakeFrameTarget{
		rec:  rec,
		name: "win",
		size: driver.Size{Width: 720, Height: 480},
		pos:  start,
	}
	d, rend := newFakeDemo(rec, tw)
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}

	if err := runSmokeScript(d); err != nil {
		t.Fatalf("runSmokeScript: %v", err)
	}

	// Drag: the window moved by exactly the synthesized pointer delta.
	if want := (driver.Point{X: start.X + 30, Y: start.Y + 20}); tw.pos != want {
		t.Errorf("position after drag = %v, want %v (moved via Controller events)", tw.pos, want)
	}
	if len(tw.setPositionLog) == 0 {
		t.Error("no SetPosition calls recorded: drag never reached the window")
	}

	// Resize: the window grew by exactly the corner delta and respects MinSize.
	if want := (driver.Size{Width: 750, Height: 500}); tw.size != want {
		t.Errorf("size after resize = %v, want %v", tw.size, want)
	}
	if tw.size.Width < d.style.MinSize.Width || tw.size.Height < d.style.MinSize.Height {
		t.Errorf("size %v below MinSize %v", tw.size, d.style.MinSize)
	}

	// Maximize button toggled maximize and restore; the iconify step added
	// one Iconify and one more Restore.
	if tw.maximizeCalls != 1 {
		t.Errorf("Maximize called %d times, want 1 (via maximize button)", tw.maximizeCalls)
	}
	if tw.restoreCalls != 2 {
		t.Errorf("Restore called %d times, want 2 (button restore + iconify step)", tw.restoreCalls)
	}
	if tw.iconifyCalls != 1 {
		t.Errorf("Iconify called %d times, want 1", tw.iconifyCalls)
	}
	if tw.maximized {
		t.Error("window still maximized after restore")
	}

	// Every synthesized mouse event went through the Controller: 3 drag +
	// 3 resize + 4 maximize button + 2 close = 12.
	if d.handled != 12 {
		t.Errorf("controller handled %d events, want exactly 12 — the script must route every interaction through Controller events", d.handled)
	}

	// The close button ran the unified gated close: renderer released,
	// state deleted, face closed, window unregistered and destroyed.
	if !rend.closed {
		t.Error("renderer was not closed by the close-button path")
	}
	if !tw.destroyed {
		t.Error("window was not destroyed by the close-button path")
	}
	if d.st != nil || d.win != nil || d.face != nil {
		t.Errorf("state left behind: st=%v win=%v face=%v, want all released",
			d.st != nil, d.win != nil, d.face != nil)
	}
	if got := d.a.Windows(); len(got) != 0 {
		t.Fatalf("app owns %d windows after smoke close, want 0", len(got))
	}

	// Quit must have run even though everything succeeded: a closed app
	// rejects new posts.
	if err := d.a.Post(func() {}); !errors.Is(err, app.ErrClosed) {
		t.Errorf("Post after smoke = %v, want app.ErrClosed (Quit must always run)", err)
	}
}

// TestSmokeScriptWaitsForIconifyAndRestoreState pins that the smoke script's
// iconify step polls the real window state — a lagging (asynchronously
// applying) window manager must be waited out, not assumed to have applied
// the transition by the time the call returns.
func TestSmokeScriptWaitsForIconifyAndRestoreState(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{
		rec:        rec,
		name:       "win",
		size:       driver.Size{Width: 720, Height: 480},
		pos:        driver.Point{X: 100, Y: 50},
		iconifyLag: 4,
		restoreLag: 4,
	}
	d, _ := newFakeDemo(rec, tw)
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}

	if err := runSmokeScript(d); err != nil {
		t.Fatalf("runSmokeScript with a lagging WM: %v", err)
	}
	// Both waits polled the state machine repeatedly instead of checking
	// once: the lag is 4 polls per transition, so any fewer than 5 polls
	// means the script never waited.
	if tw.iconifyLag > 0 && tw.actionPolls < 5 {
		t.Errorf("only %d state polls after the whole script, want at least 5 — the script did not wait out the lag", tw.actionPolls)
	}
}

// TestSmokeScriptIconifyNoopTimesOut pins that a window whose iconified state
// never becomes true fails the smoke script on a real deadline: the first
// error is the iconify-step timeout, cleanup releases the window and renderer,
// and Quit still runs so the app can never hang.
func TestSmokeScriptIconifyNoopTimesOut(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{
		rec:         rec,
		name:        "win",
		size:        driver.Size{Width: 720, Height: 480},
		pos:         driver.Point{X: 100, Y: 50},
		iconifyNoop: true,
	}
	d, rend := newFakeDemo(rec, tw)
	d.smokeTimeout = 100 * time.Millisecond
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}

	err := runSmokeScript(d)
	if err == nil {
		t.Fatal("runSmokeScript returned nil although the window never iconified")
	}
	if !strings.Contains(err.Error(), "iconified") {
		t.Errorf("error = %v, want it to name the iconified wait", err)
	}
	if tw.actionPolls < 3 {
		t.Errorf("only %d IsIconified polls, want at least 3 — the deadline must be reached by polling, not by one blind check", tw.actionPolls)
	}
	d.noteErr(err) // main's wrapper records the returned error; first error wins
	if !strings.Contains(d.err.Error(), "iconified") {
		t.Errorf("demo.err = %v, want the iconify timeout preserved as first error", d.err)
	}
	if !rend.closed || !tw.destroyed {
		t.Error("timed-out smoke must attempt best-effort renderer and window cleanup")
	}
	if d.st != nil || d.win != nil || d.face != nil {
		t.Errorf("cleanup left state behind: st=%v win=%v face=%v, want all released",
			d.st != nil, d.win != nil, d.face != nil)
	}
	if got := d.a.Windows(); len(got) != 0 {
		t.Fatalf("app owns %d windows after timed-out smoke cleanup, want 0", len(got))
	}
	if err := d.a.Post(func() {}); !errors.Is(err, app.ErrClosed) {
		t.Errorf("Post after timed-out smoke = %v, want app.ErrClosed (Quit must always run)", err)
	}
}

// TestSmokeScriptRestoreNoopTimesOut pins the symmetric timeout and its
// cleanup failure: MakeCurrent keeps the renderer and window behind the
// resource gate, and the cleanup error cannot replace the restore timeout.
func TestSmokeScriptRestoreNoopTimesOut(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{
		rec:         rec,
		name:        "win",
		size:        driver.Size{Width: 720, Height: 480},
		pos:         driver.Point{X: 100, Y: 50},
		restoreNoop: true,
	}
	d, rend := newFakeDemo(rec, tw)
	d.smokeTimeout = 100 * time.Millisecond
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}
	makeErr := errors.New("fake: cleanup make current failed")
	tw.makeErr = makeErr

	err := runSmokeScript(d)
	if err == nil {
		t.Fatal("runSmokeScript returned nil although the window never returned to normal")
	}
	if !strings.Contains(err.Error(), "normal") {
		t.Errorf("error = %v, want it to name the normal-state wait", err)
	}
	d.noteErr(err)
	if !strings.Contains(d.err.Error(), "normal") || errors.Is(d.err, makeErr) {
		t.Errorf("demo.err = %v, want the restore timeout, not cleanup error %v", d.err, makeErr)
	}
	if rend.closed || tw.destroyed {
		t.Error("MakeCurrent failure must preserve the renderer and window during smoke cleanup")
	}
	if d.st == nil || d.win == nil {
		t.Error("MakeCurrent failure must preserve the demo window state")
	}
	if got := d.a.Windows(); len(got) != 1 {
		t.Fatalf("app owns %d windows after gated cleanup, want 1", len(got))
	}
	if err := d.a.Post(func() {}); !errors.Is(err, app.ErrClosed) {
		t.Errorf("Post after timed-out smoke = %v, want app.ErrClosed (Quit must always run)", err)
	}
}

// TestSmokeScriptQuitsEvenWhenCloseFails pins that a failing close still
// leaves the app: the first error is recorded and Quit runs, so an
// unattended smoke run can never hang.
func TestSmokeScriptQuitsEvenWhenCloseFails(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{
		rec:  rec,
		name: "win",
		size: driver.Size{Width: 720, Height: 480},
		pos:  driver.Point{X: 100, Y: 50},
	}
	d, rend := newFakeDemo(rec, tw)
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}

	makeErr := errors.New("fake: make current failed")
	tw.makeErr = makeErr

	err := runSmokeScript(d)
	if err == nil {
		t.Fatal("runSmokeScript returned nil despite the close failure")
	}
	if !errors.Is(d.err, makeErr) {
		t.Errorf("demo.err = %v, want it to wrap the close error %v", d.err, makeErr)
	}
	if rend.closed || tw.destroyed {
		t.Error("failed close must release nothing")
	}
	if d.st == nil || d.win == nil {
		t.Error("failed close must preserve the demo state")
	}
	if err := d.a.Post(func() {}); !errors.Is(err, app.ErrClosed) {
		t.Errorf("Post after failed smoke = %v, want app.ErrClosed (Quit must always run)", err)
	}
}

// TestSmokeScriptFailsWithoutWindow pins that a smoke run started before the
// window exists reports an error instead of dereferencing nil, and still
// quits.
func TestSmokeScriptFailsWithoutWindow(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{rec: rec, name: "win"}
	d, _ := newFakeDemo(rec, tw)

	if err := runSmokeScript(d); err == nil {
		t.Fatal("runSmokeScript without a window returned nil, want an error")
	}
	if err := d.a.Post(func() {}); !errors.Is(err, app.ErrClosed) {
		t.Errorf("Post = %v, want app.ErrClosed (Quit must always run)", err)
	}
}

var (
	_ driver.FrameWindow = (*fakeFrameTarget)(nil)
	_ render.Target      = (*fakeFrameTarget)(nil)
	_ renderer           = (*fakeRenderer)(nil)
)
