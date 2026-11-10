package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/app"
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// fakeWindow is a driver.Window double that does nothing; identity is the
// name so tests can assert which window was closed.
type fakeWindow string

func (fakeWindow) Show()                     {}
func (fakeWindow) Hide()                     {}
func (fakeWindow) Destroy()                  {}
func (fakeWindow) Size() driver.Size         { return driver.Size{} }
func (fakeWindow) SetHandler(driver.Handler) {}

// fakeApp records the calls closeAllAndQuit makes so tests can assert the
// smoke plan ordering without opening real windows.
type fakeApp struct {
	windows []driver.Window
	events  []string
	quits   int
	failOn  driver.Window
}

func (f *fakeApp) Windows() []driver.Window {
	return append([]driver.Window(nil), f.windows...)
}

func (f *fakeApp) CloseWindow(w driver.Window) error {
	if w == f.failOn {
		return errors.New("fake: close failed")
	}
	f.events = append(f.events, "close:"+string(w.(fakeWindow)))
	for i, x := range f.windows {
		if x == w {
			f.windows = append(f.windows[:i], f.windows[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeApp) Quit() {
	f.quits++
	f.events = append(f.events, "quit")
}

func TestSmokePlanClosesBothWindows(t *testing.T) {
	fa := &fakeApp{windows: []driver.Window{fakeWindow("alpha"), fakeWindow("beta")}}

	if err := closeAllAndQuit(fa); err != nil {
		t.Fatalf("closeAllAndQuit: %v", err)
	}

	want := []string{"close:alpha", "close:beta", "quit"}
	if !reflect.DeepEqual(fa.events, want) {
		t.Fatalf("event order = %v, want both windows closed before the single quit %v", fa.events, want)
	}
	if fa.quits != 1 {
		t.Fatalf("Quit called %d times, want exactly 1", fa.quits)
	}
	if len(fa.windows) != 0 {
		t.Fatalf("%d windows still open, want 0", len(fa.windows))
	}
}

func TestSmokePlanCloseFailureStopsBeforeQuit(t *testing.T) {
	fa := &fakeApp{
		windows: []driver.Window{fakeWindow("alpha"), fakeWindow("beta")},
		failOn:  fakeWindow("beta"),
	}

	if err := closeAllAndQuit(fa); err == nil {
		t.Fatal("closeAllAndQuit: expected error from failing CloseWindow, got nil")
	}
	if fa.quits != 0 {
		t.Fatalf("Quit called %d times after a close failure, want 0", fa.quits)
	}
	want := []string{"close:alpha"}
	if !reflect.DeepEqual(fa.events, want) {
		t.Fatalf("event order = %v, want %v and no quit", fa.events, want)
	}
	if len(fa.windows) != 1 {
		t.Fatalf("%d windows closed, want 1 (the failing window must stay open)", 2-len(fa.windows))
	}
}

// --- error-path fakes: deterministic, no GPU and no window server ---

// recorder collects named events so tests can assert the exact release
// order of the close and cleanup paths.
type recorder struct{ events []string }

func (r *recorder) add(event string) { r.events = append(r.events, event) }

// fakeTargetWindow is a driver.Window and render.Target double whose
// MakeCurrent can be set to fail and whose Destroy records an event.
type fakeTargetWindow struct {
	rec       *recorder
	name      string
	makeErr   error
	destroyed bool
}

func (f *fakeTargetWindow) Show()                     {}
func (f *fakeTargetWindow) Hide()                     {}
func (f *fakeTargetWindow) Destroy()                  { f.destroyed = true; f.rec.add("destroy:" + f.name) }
func (f *fakeTargetWindow) Size() driver.Size         { return driver.Size{Width: 640, Height: 400} }
func (f *fakeTargetWindow) SetHandler(driver.Handler) {}
func (f *fakeTargetWindow) MakeCurrent() error {
	if f.makeErr != nil {
		return f.makeErr
	}
	f.rec.add("makecurrent:" + f.name)
	return nil
}
func (f *fakeTargetWindow) FramebufferSize() driver.Size { return f.Size() }
func (f *fakeTargetWindow) Present()                     {}

// fakeRenderer implements the demo's renderer seam and records Close. The
// error fields let a test fail an individual frame stage deterministically.
type fakeRenderer struct {
	rec      *recorder
	name     string
	closed   bool
	beginErr error
	drawErr  error
	endErr   error
}

func (f *fakeRenderer) Begin(render.Color) error           { return f.beginErr }
func (f *fakeRenderer) DrawRect(render.Rect, render.Color) {}
func (f *fakeRenderer) DrawText(string, driver.Point, render.Color, *text.Face) error {
	return f.drawErr
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

func TestCloseWindowMakeCurrentFailureLeavesWindowUntouched(t *testing.T) {
	rec := &recorder{}
	tw := &fakeTargetWindow{rec: rec, name: "win1"}
	tw2 := &fakeTargetWindow{rec: rec, name: "win2"}
	rend := &fakeRenderer{rec: rec, name: "win1"}
	rend2 := &fakeRenderer{rec: rec, name: "win2"}

	a := app.New(&queueBackend{queue: []driver.Window{tw, tw2}})
	d := newDemo(a)
	calls := 0
	d.newRenderer = func(render.Target) (renderer, error) {
		calls++
		if calls == 1 {
			return rend, nil
		}
		return rend2, nil
	}
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}

	makeErr := errors.New("fake: make current failed")
	tw.makeErr = makeErr
	w := d.order[0]

	err := d.CloseWindow(w)
	if !errors.Is(err, makeErr) {
		t.Fatalf("CloseWindow error = %v, want it to wrap %v", err, makeErr)
	}
	if rend.closed {
		t.Error("renderer.Close ran although the context never became current")
	}
	if tw.destroyed {
		t.Error("window was destroyed although the context never became current")
	}
	if d.states[w] == nil {
		t.Error("window state was deleted although the window is still open")
	}
	if got := a.Windows(); len(got) != 2 {
		t.Fatalf("app owns %d windows, want 2 (close must not unregister)", len(got))
	}
	for _, event := range rec.events {
		if event == "close-renderer:win1" || event == "destroy:win1" {
			t.Fatalf("release event %q recorded despite MakeCurrent failure", event)
		}
	}
}

func TestInitFailureReleasesEstablishedResourcesInOrder(t *testing.T) {
	rec := &recorder{}
	tw1 := &fakeTargetWindow{rec: rec, name: "win1"}
	tw2 := &fakeTargetWindow{rec: rec, name: "win2"}
	rend1 := &fakeRenderer{rec: rec, name: "win1"}
	openErr := errors.New("fake: renderer creation failed")

	a := app.New(&queueBackend{queue: []driver.Window{tw1, tw2}})
	d := newDemo(a)
	calls := 0
	d.newRenderer = func(render.Target) (renderer, error) {
		calls++
		if calls == 1 {
			return rend1, nil
		}
		return nil, openErr
	}
	d.init()

	if !errors.Is(d.err, openErr) {
		t.Fatalf("demo.err = %v, want the original init error %v", d.err, openErr)
	}
	if !rend1.closed {
		t.Error("established renderer of the first window was not closed")
	}
	if !tw1.destroyed {
		t.Error("first window was not destroyed during init cleanup")
	}
	if len(d.states) != 0 || len(a.Windows()) != 0 {
		t.Fatalf("cleanup left %d states and %d app windows, want 0 and 0", len(d.states), len(a.Windows()))
	}
	if d.face != nil {
		t.Error("face was not closed during init cleanup")
	}
	want := []string{"destroy:win2", "makecurrent:win1", "close-renderer:win1", "destroy:win1"}
	if !reflect.DeepEqual(rec.events, want) {
		t.Fatalf("release order = %v, want %v", rec.events, want)
	}
}

func TestInitFailureKeepsFirstErrorWhenCleanupAlsoFails(t *testing.T) {
	rec := &recorder{}
	makeErr := errors.New("fake: make current failed")
	tw1 := &fakeTargetWindow{rec: rec, name: "win1", makeErr: makeErr}
	tw2 := &fakeTargetWindow{rec: rec, name: "win2"}
	rend1 := &fakeRenderer{rec: rec, name: "win1"}
	openErr := errors.New("fake: renderer creation failed")

	a := app.New(&queueBackend{queue: []driver.Window{tw1, tw2}})
	d := newDemo(a)
	calls := 0
	d.newRenderer = func(render.Target) (renderer, error) {
		calls++
		if calls == 1 {
			return rend1, nil
		}
		return nil, openErr
	}
	d.init()

	if !errors.Is(d.err, openErr) {
		t.Fatalf("demo.err = %v, want the first error %v, not the cleanup error %v", d.err, openErr, makeErr)
	}
	if rend1.closed || tw1.destroyed {
		t.Error("cleanup must stop at the MakeCurrent failure and leave the window untouched")
	}
	if len(d.states) != 1 || len(a.Windows()) != 1 {
		t.Fatalf("failing window must stay registered: states=%d app=%d, want 1 and 1", len(d.states), len(a.Windows()))
	}
	if d.face != nil {
		t.Error("face must still be released even when a window close fails")
	}
	want := []string{"destroy:win2"}
	if !reflect.DeepEqual(rec.events, want) {
		t.Fatalf("events = %v, want %v", rec.events, want)
	}
}

func TestInitFailureClosesFaceWhenFirstWindowFails(t *testing.T) {
	rec := &recorder{}
	tw1 := &fakeTargetWindow{rec: rec, name: "win1"}
	openErr := errors.New("fake: renderer creation failed")

	a := app.New(&queueBackend{queue: []driver.Window{tw1}})
	d := newDemo(a)
	d.newRenderer = func(render.Target) (renderer, error) { return nil, openErr }
	d.init()

	if !errors.Is(d.err, openErr) {
		t.Fatalf("demo.err = %v, want %v", d.err, openErr)
	}
	if d.face != nil {
		t.Error("face was not closed when the first window failed")
	}
	if len(d.states) != 0 || len(a.Windows()) != 0 {
		t.Fatalf("states=%d app=%d, want 0 and 0", len(d.states), len(a.Windows()))
	}
	want := []string{"destroy:win1"}
	if !reflect.DeepEqual(rec.events, want) {
		t.Fatalf("events = %v, want %v", rec.events, want)
	}
}

// TestFirstFrameFailureAbortsInitAndReleasesInOrder pins that a failed first
// frame — Begin, DrawText or End — fails init exactly like a failed renderer
// creation: the original frame error is the one init reports, and cleanup
// releases every established window through the regular close path in reverse
// creation order, failing window first.
func TestFirstFrameFailureAbortsInitAndReleasesInOrder(t *testing.T) {
	beginErr := errors.New("fake: begin failed")
	drawErr := errors.New("fake: draw text failed")
	endErr := errors.New("fake: end failed")
	cases := []struct {
		name   string
		inject func(r *fakeRenderer)
		want   error
	}{
		{"Begin", func(r *fakeRenderer) { r.beginErr = beginErr }, beginErr},
		{"DrawText", func(r *fakeRenderer) { r.drawErr = drawErr }, drawErr},
		{"End", func(r *fakeRenderer) { r.endErr = endErr }, endErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			tw1 := &fakeTargetWindow{rec: rec, name: "win1"}
			tw2 := &fakeTargetWindow{rec: rec, name: "win2"}
			rend1 := &fakeRenderer{rec: rec, name: "win1"}
			rend2 := &fakeRenderer{rec: rec, name: "win2"}
			tc.inject(rend2)

			a := app.New(&queueBackend{queue: []driver.Window{tw1, tw2}})
			d := newDemo(a)
			calls := 0
			d.newRenderer = func(render.Target) (renderer, error) {
				calls++
				if calls == 1 {
					return rend1, nil
				}
				return rend2, nil
			}
			d.init()

			if !errors.Is(d.err, tc.want) {
				t.Fatalf("demo.err = %v, want the first-frame error %v", d.err, tc.want)
			}
			if !rend2.closed || !tw2.destroyed {
				t.Error("failing window's renderer and window were not released")
			}
			if !rend1.closed || !tw1.destroyed {
				t.Error("first window's established resources were not released")
			}
			if len(d.states) != 0 || len(a.Windows()) != 0 {
				t.Fatalf("cleanup left %d states and %d app windows, want 0 and 0", len(d.states), len(a.Windows()))
			}
			if d.face != nil {
				t.Error("face was not closed during init cleanup")
			}
			want := []string{
				"makecurrent:win2", "close-renderer:win2", "destroy:win2",
				"makecurrent:win1", "close-renderer:win1", "destroy:win1",
			}
			if !reflect.DeepEqual(rec.events, want) {
				t.Fatalf("release order = %v, want %v", rec.events, want)
			}
		})
	}
}
