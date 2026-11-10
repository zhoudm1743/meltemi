package ui

import (
	"errors"
	"image"
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/text"
)

type uiBlockingInitBackend struct {
	*uiTestBackend
	initStarted chan struct{}
	releaseInit chan struct{}
	err         error
}

func (b *uiBlockingInitBackend) Init() error {
	close(b.initStarted)
	<-b.releaseInit
	return b.err
}

func settleUI(t *testing.T, a *App, r *uiTestRenderer) {
	t.Helper()
drain:
	for {
		select {
		case <-r.frames:
		default:
			break drain
		}
	}
	if err := a.Post(func() {}); err != nil {
		t.Fatalf("Post barrier: %v", err)
	}
	receiveUI(t, r.frames, "Post redraw barrier")
}

func postBarrier(t *testing.T, a *App) {
	t.Helper()
	done := make(chan struct{})
	if err := a.Post(func() { close(done) }); err != nil {
		t.Fatalf("Post completion barrier: %v", err)
	}
	receiveUI(t, done, "posted operation completion")
}

func TestWindowClosePolicies(t *testing.T) {
	t.Run("Init failure leaves queued Close unexecuted", func(t *testing.T) {
		initErr := errors.New("fake: Init failed")
		base := newUITestBackend()
		backend := &uiBlockingInitBackend{
			uiTestBackend: base,
			initStarted:   make(chan struct{}),
			releaseInit:   make(chan struct{}),
			err:           initErr,
		}
		a, _ := newUITestApp(backend, &uiTestLog{})
		done := runUIAsync(a)
		released := false
		defer func() {
			if !released {
				close(backend.releaseInit)
			}
		}()
		receiveUI(t, backend.initStarted, "blocked backend Init")
		w := a.Window("waiting close")
		closeResult := make(chan error, 1)
		go func() { closeResult <- w.Close() }()
		deadline := time.Now().Add(2 * time.Second)
		for base.WakeCalls() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if base.WakeCalls() == 0 {
			t.Fatal("Close was not posted while backend Init was blocked")
		}
		close(backend.releaseInit)
		released = true

		if err := receiveUI(t, closeResult, "Close after Init failure"); err != nil {
			t.Errorf("Close Post error = %v, want nil; the unexecuted close error belongs only to Run", err)
		}
		if err := waitUIReturn(t, done); !errors.Is(err, initErr) {
			t.Errorf("Run error = %v, want Init error %v", err, initErr)
		}
	})

	t.Run("OnClose veto", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		done := runUIAsync(a)
		calls := 0
		w := a.Window("veto").Size(240, 120).OnClose(func() bool {
			calls++
			return false
		})
		target, r := showUI(t, w, backend, factory)
		settleUI(t, a, r)

		vetoDone := make(chan struct{})
		if err := a.Post(func() {
			target.handler(driver.Event{Kind: driver.EventCloseRequested, Window: target})
			close(vetoDone)
		}); err != nil {
			t.Fatalf("Post native close request: %v", err)
		}
		receiveUI(t, vetoDone, "vetoed close callback completion")
		assertUIRunning(t, done)
		if calls != 1 {
			t.Errorf("OnClose calls = %d, want 1", calls)
		}
		if r.closed.Load() || backend.Destroyed() != 0 || len(a.Windows()) != 1 {
			t.Errorf("veto destroyed state: renderer closed=%v native destroys=%d windows=%d", r.closed.Load(), backend.Destroyed(), len(a.Windows()))
		}
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	t.Run("CloseHide", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		a.SetLastWindowAction(LastWindowKeepRunning)
		done := runUIAsync(a)
		w := a.Window("hide").Size(240, 120).CloseAction(CloseHide)
		target, r := showUI(t, w, backend, factory)
		settleUI(t, a, r)

		if err := w.Close(); err != nil {
			t.Fatalf("Close Post: %v", err)
		}
		settleUI(t, a, r)
		assertUIRunning(t, done)
		if target.hides != 1 {
			t.Errorf("Hide calls = %d, want 1", target.hides)
		}
		if r.closed.Load() || backend.Destroyed() != 0 || len(a.Windows()) != 1 {
			t.Errorf("CloseHide released state: renderer closed=%v native destroys=%d windows=%d", r.closed.Load(), backend.Destroyed(), len(a.Windows()))
		}
		begins := r.begins
		assertUIRunning(t, done)
		if r.begins != begins {
			t.Errorf("Begin calls changed from %d to %d while idle; redraw must remain event-driven", begins, r.begins)
		}
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	t.Run("LastWindowKeepRunning recreates Face", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		faceCalls := 0
		newFace := a.newFace
		a.newFace = func() (*text.Face, error) {
			faceCalls++
			return newFace()
		}
		a.SetLastWindowAction(LastWindowKeepRunning)
		done := runUIAsync(a)
		firstWindow := a.Window("first").Size(240, 120).Content(Label("first"))
		_, firstRenderer := showUI(t, firstWindow, backend, factory)
		settleUI(t, a, firstRenderer)
		firstFace := a.face

		if err := firstWindow.Close(); err != nil {
			t.Fatalf("Close first window Post: %v", err)
		}
		postBarrier(t, a)
		assertUIRunning(t, done)
		if !firstRenderer.closed.Load() || backend.Destroyed() != 1 || len(a.Windows()) != 0 {
			t.Errorf("first close state: renderer closed=%v destroys=%d windows=%d", firstRenderer.closed.Load(), backend.Destroyed(), len(a.Windows()))
		}
		if a.face != nil {
			t.Error("Face retained after the last window was destroyed")
		}
		if _, err := firstFace.Rasterize("closed"); err == nil {
			t.Error("first Face is still usable after last-window close")
		}

		secondWindow := a.Window("second").Size(240, 120).Content(Label("second"))
		_, secondRenderer := showUI(t, secondWindow, backend, factory)
		settleUI(t, a, secondRenderer)
		if faceCalls != 2 {
			t.Errorf("Face factory calls = %d, want 2", faceCalls)
		}
		if a.face == nil || a.face == firstFace {
			t.Error("new window did not receive a freshly created Face")
		}
		if len(secondRenderer.faces) == 0 || secondRenderer.faces[len(secondRenderer.faces)-1] != a.face {
			t.Error("new renderer did not draw with the rebuilt app-owned Face")
		}
		if err := secondWindow.Close(); err != nil {
			t.Fatalf("Close second window: %v", err)
		}
		assertUIRunning(t, done)
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})

	t.Run("default LastWindowQuit", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		done := runUIAsync(a)
		w := a.Window("quit").Size(240, 120).Content(Label("last"))
		_, r := showUI(t, w, backend, factory)
		settleUI(t, a, r)
		face := a.face

		var closeErr error
		if err := a.Post(func() { closeErr = w.Close() }); err != nil {
			t.Fatalf("Post Close from UI callback: %v", err)
		}
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run after last-window close: %v", err)
		}
		if closeErr != nil {
			t.Fatalf("Close from UI callback: %v", closeErr)
		}
		if !r.closed.Load() || backend.Destroyed() != 1 || len(a.Windows()) != 0 {
			t.Errorf("last close state: renderer closed=%v destroys=%d windows=%d", r.closed.Load(), backend.Destroyed(), len(a.Windows()))
		}
		if _, err := face.Rasterize("closed"); err == nil {
			t.Error("Face is still usable after default last-window quit")
		}
	})

	for _, uiCallback := range []bool{false, true} {
		name := "external Close"
		if uiCallback {
			name = "UI callback Close"
		}
		t.Run("MakeCurrent failure skips redraw/"+name, func(t *testing.T) {
			backend := newUITestBackend()
			a, factory := newUITestApp(backend, &uiTestLog{})
			done := runUIAsync(a)
			makeErr := errors.New("fake: MakeCurrent failed")
			var target *uiTestTarget
			w := a.Window("context failure").Size(240, 120).Content(Label("alive")).OnClose(func() bool {
				target.makeErr = makeErr
				return true
			})
			var r *uiTestRenderer
			target, r = showUI(t, w, backend, factory)
			settleUI(t, a, r)
			face := a.face
			target.makeFailed = make(chan struct{})
			postResult := make(chan error, 1)
			if uiCallback {
				if err := a.Post(func() { postResult <- w.Close() }); err != nil {
					t.Fatalf("Post UI callback: %v", err)
				}
			} else {
				postResult <- w.Close()
			}
			if err := receiveUI(t, postResult, "Close Post result"); err != nil {
				t.Errorf("Close Post error = %v, want nil; actual close errors belong to Run", err)
			}
			receiveUI(t, target.makeFailed, "failed Close MakeCurrent")
			begins := r.begins
			assertUIRunning(t, done)
			if r.begins != begins {
				t.Errorf("Begin calls changed from %d to %d after failed Close", begins, r.begins)
			}
			if r.closed.Load() || backend.Destroyed() != 0 || len(a.Windows()) != 1 || a.face != face || w.target != target || w.renderer != r {
				t.Errorf("failed close changed state: renderer closed=%v destroys=%d windows=%d face preserved=%v target preserved=%v renderer preserved=%v", r.closed.Load(), backend.Destroyed(), len(a.Windows()), a.face == face, w.target == target, w.renderer == r)
			}
			if r.discarded {
				t.Error("runtime Close MakeCurrent failure discarded renderer text state")
			}
			if _, err := face.Rasterize("alive"); err != nil {
				t.Errorf("Face was closed after MakeCurrent failure: %v", err)
			}
			if err := a.err(); !errors.Is(err, makeErr) {
				t.Errorf("Run first error = %v, want close error %v", err, makeErr)
			}
			reset := make(chan struct{})
			if err := a.core.Post(func() {
				target.makeErr = nil
				close(reset)
			}); err != nil {
				t.Fatalf("Post failure reset without redraw: %v", err)
			}
			receiveUI(t, reset, "MakeCurrent failure reset")
			a.Quit()
			if err := waitUIReturn(t, done); !errors.Is(err, makeErr) {
				t.Fatalf("Run error = %v, want preserved first close error %v", err, makeErr)
			}
		})
	}

	t.Run("Quit MakeCurrent failure finalizes native and public state", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		done := runUIAsync(a)
		w := a.Window("quit context failure").Size(240, 120).Content(Label("alive"))
		target, r := showUI(t, w, backend, factory)
		settleUI(t, a, r)
		face := a.face
		begins := r.begins
		makeErr := errors.New("fake: Quit MakeCurrent failed")
		injected := make(chan struct{})
		if err := a.core.Post(func() {
			target.makeErr = makeErr
			close(injected)
		}); err != nil {
			t.Fatalf("Post failure injection without redraw: %v", err)
		}
		receiveUI(t, injected, "Quit failure injection")
		a.Quit()
		if err := waitUIReturn(t, done); !errors.Is(err, makeErr) {
			t.Fatalf("Run error = %v, want cleanup error %v", err, makeErr)
		}
		if r.begins != begins {
			t.Errorf("Begin calls changed from %d to %d during failed Quit cleanup", begins, r.begins)
		}
		if r.closed.Load() {
			t.Error("renderer.Close ran without a current native context")
		}
		if !r.discarded {
			t.Error("final MakeCurrent failure did not discard renderer text state")
		}
		if got := backend.Destroyed(); got != 1 {
			t.Errorf("native Destroy calls = %d, want exactly 1", got)
		}
		if got := len(a.Windows()); got != 0 || w.target != nil || w.renderer != nil || w.router != nil {
			t.Errorf("final cleanup left public state alive: windows=%d target=%v renderer=%v router=%v", got, w.target, w.renderer, w.router)
		}
		if a.face != nil {
			t.Error("App retained a Face after every public/native window was finalized")
		}
		if _, err := face.Rasterize("closed"); err == nil {
			t.Error("Face remains usable after final cleanup")
		}
	})
}

func TestCloseOneOfTwoWindows(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	done := runUIAsync(a)
	firstWindow := a.Window("first").Size(240, 120).Content(Label("first"))
	firstTarget, firstRenderer := showUI(t, firstWindow, backend, factory)
	clicks := 0
	button := Button("second").OnClick(func() { clicks++ })
	secondWindow := a.Window("second").Size(240, 120).Content(button)
	secondTarget, secondRenderer := showUI(t, secondWindow, backend, factory)
	settleUI(t, a, secondRenderer)
	sharedFace := a.face

	if err := firstWindow.Close(); err != nil {
		t.Fatalf("Close first window Post: %v", err)
	}
	settleUI(t, a, secondRenderer)
	assertUIRunning(t, done)
	if !firstRenderer.closed.Load() || secondRenderer.closed.Load() {
		t.Errorf("renderer close state = first %v, second %v; want first only", firstRenderer.closed.Load(), secondRenderer.closed.Load())
	}
	if backend.Destroyed() != 1 {
		t.Errorf("native Destroy calls = %d, want 1", backend.Destroyed())
	}
	windows := a.Windows()
	if len(windows) != 1 || windows[0] != secondWindow {
		t.Fatalf("Windows after first close = %v, want only the second window", windows)
	}
	if a.face != sharedFace {
		t.Error("closing one of two windows replaced or released the shared Face")
	}
	if _, err := sharedFace.Rasterize("alive"); err != nil {
		t.Errorf("shared Face no longer works: %v", err)
	}

	for {
		select {
		case <-secondRenderer.frames:
		default:
			goto drained
		}
	}
drained:
	if err := a.Post(func() { button.SetText("updated") }); err != nil {
		t.Fatalf("Post second-window update: %v", err)
	}
	receiveUI(t, secondRenderer.frames, "surviving renderer redraw")
	if got := secondRenderer.texts[len(secondRenderer.texts)-1]; got != "updated" {
		t.Errorf("surviving renderer text = %q, want updated", got)
	}
	if got := secondRenderer.faces[len(secondRenderer.faces)-1]; got != sharedFace {
		t.Error("surviving renderer did not keep the original shared Face")
	}
	eventsDone := make(chan struct{})
	if err := a.Post(func() {
		defer close(eventsDone)
		secondTarget.handler(driver.Event{
			Kind: driver.EventMouseButton, Window: secondTarget,
			Position: driver.Point{X: 10, Y: 10}, Button: driver.MouseButtonLeft, Action: driver.ActionPress,
		})
		secondTarget.handler(driver.Event{
			Kind: driver.EventMouseButton, Window: secondTarget,
			Position: driver.Point{X: 10, Y: 10}, Button: driver.MouseButtonLeft, Action: driver.ActionRelease,
		})
	}); err != nil {
		t.Fatalf("Post second-window input: %v", err)
	}
	receiveUI(t, eventsDone, "surviving event router input completion")
	if clicks != 1 {
		t.Errorf("second window click count = %d, want its event router to remain usable", clicks)
	}
	if firstTarget == secondTarget {
		t.Fatal("test backend reused a native window")
	}

	if err := secondWindow.Close(); err != nil {
		t.Fatalf("Close second window: %v", err)
	}
	if err := waitUIReturn(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestWindowFramelessConfiguration(t *testing.T) {
	backend := newUITestBackend()
	a, _ := newUITestApp(backend, &uiTestLog{})
	frameless := a.Window("frameless").Size(300, 180)
	if got := frameless.Frameless(true); got != frameless {
		t.Fatal("Frameless did not return the same Window for chaining")
	}
	frameless.Content(Label("frameless")).Show()
	a.Window("decorated").Size(320, 200).Content(Label("decorated")).Show()
	a.Quit()
	if err := a.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(backend.configs) != 2 {
		t.Fatalf("native configs = %d, want 2", len(backend.configs))
	}
	if !backend.configs[0].Frameless {
		t.Error("Frameless(true) was not forwarded to the native config")
	}
	if backend.configs[1].Frameless {
		t.Error("a later default window inherited Frameless=true")
	}
	if got := backend.configs[0].Size; got != (driver.Size{Width: 300, Height: 180}) {
		t.Errorf("frameless native size = %v, want 300x180", got)
	}
	if got := backend.configs[0].Title; got != "frameless" {
		t.Errorf("frameless native title = %q, want frameless", got)
	}
}

func TestWindowConfigurationAndNativeEvents(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	a.SetLastWindowAction(LastWindowKeepRunning)
	done := runUIAsync(a)
	icon := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	w := a.Window("initial").Size(400, 300)
	if got := w.Title("configured"); got != w {
		t.Fatal("Title did not return the same Window for chaining")
	}
	if got := w.MinSize(240, 120); got != w {
		t.Fatal("MinSize did not return the same Window for chaining")
	}
	if got := w.Icon(icon); got != w {
		t.Fatal("Icon did not return the same Window for chaining")
	}
	target, _ := showUI(t, w, backend, factory)
	if len(backend.configs) != 1 {
		t.Fatalf("native configs = %d, want 1", len(backend.configs))
	}
	config := backend.configs[0]
	if config.Title != "configured" || config.MinSize != (driver.Size{Width: 240, Height: 120}) || config.Icon != icon {
		t.Errorf("native config = %+v, want configured title, 240x120 minimum and selected icon", config)
	}

	w.SetTitle("updated")
	postBarrier(t, a)
	if got := target.Title(); got != "updated" {
		t.Errorf("native title after SetTitle = %q, want updated", got)
	}
	if got := w.config.Title; got != "updated" {
		t.Errorf("stored title after SetTitle = %q, want updated", got)
	}
	w.Hide()
	postBarrier(t, a)
	w.Show()
	postBarrier(t, a)
	if got := target.Title(); got != "updated" {
		t.Errorf("native title after hide/show = %q, want updated", got)
	}
	a.Quit()
	if err := waitUIReturn(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

type windowEscapeConsumer struct {
	*fakeComponent
	focused bool
}

func (c *windowEscapeConsumer) focusable() bool            { return true }
func (c *windowEscapeConsumer) isFocused() bool            { return c.focused }
func (c *windowEscapeConsumer) setFocused(focused bool)    { c.focused = focused }
func (c *windowEscapeConsumer) walk(visit func(component)) { visit(c) }

func (c *windowEscapeConsumer) handleEvent(event driver.Event) (bool, bool) {
	if event.Kind == driver.EventKey && event.Key == driver.KeyEscape {
		return true, false
	}
	return c.fakeComponent.handleEvent(event)
}

func TestWindowFocusResizeCancelCallbacks(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	a.SetLastWindowAction(LastWindowKeepRunning)
	done := runUIAsync(a)
	consumer := &windowEscapeConsumer{fakeComponent: fake("escape", 100, 30)}
	var focus []bool
	var sizes [][2]int
	cancels := 0
	w := a.Window("events").Size(240, 120).Content(consumer).
		OnFocus(func(focused bool) { focus = append(focus, focused) }).
		OnResize(func(width, height int) { sizes = append(sizes, [2]int{width, height}) }).
		OnCancel(func() { cancels++ })
	target, _ := showUI(t, w, backend, factory)
	otherFocus := 0
	otherResize := 0
	otherCancel := 0
	other := a.Window("other").Size(200, 100).
		OnFocus(func(bool) { otherFocus++ }).
		OnResize(func(int, int) { otherResize++ }).
		OnCancel(func() { otherCancel++ })
	_, otherRenderer := showUI(t, other, backend, factory)
	settleUI(t, a, otherRenderer)

	delivered := make(chan struct{})
	if err := a.Post(func() {
		target.handler(driver.Event{Kind: driver.EventFocus, Window: target, Focused: true})
		target.handler(driver.Event{Kind: driver.EventFocus, Window: target})
		target.handler(driver.Event{Kind: driver.EventResize, Window: target, Size: driver.Size{Width: 321, Height: 234}})
		target.handler(driver.Event{Kind: driver.EventFramebufferResize, Window: target, Size: driver.Size{Width: 642, Height: 468}})
		target.handler(driver.Event{Kind: driver.EventKey, Window: target, Key: driver.KeyEscape, Action: driver.ActionPress})
		target.handler(driver.Event{Kind: driver.EventKey, Window: target, Key: driver.KeyEscape, Action: driver.ActionRepeat})
		target.handler(driver.Event{Kind: driver.EventKey, Window: target, Key: driver.KeyEscape, Action: driver.ActionRelease})
		target.handler(keyEvent(driver.KeyTab, 0))
		target.handler(driver.Event{Kind: driver.EventKey, Window: target, Key: driver.KeyEscape, Action: driver.ActionPress})
		close(delivered)
	}); err != nil {
		t.Fatalf("Post window events: %v", err)
	}
	receiveUI(t, delivered, "focus, resize and cancel callbacks")
	postBarrier(t, a)
	if len(focus) != 2 || !focus[0] || focus[1] {
		t.Errorf("OnFocus values = %v, want [true false]", focus)
	}
	if len(sizes) != 1 || sizes[0] != [2]int{321, 234} {
		t.Errorf("OnResize sizes = %v, want only logical 321x234", sizes)
	}
	if cancels != 1 {
		t.Errorf("OnCancel calls = %d, want one unconsumed Escape press", cancels)
	}
	if !consumer.focused {
		t.Error("test consumer did not gain focus for consumed-Escape probe")
	}
	if len(a.Windows()) != 2 || backend.Destroyed() != 0 {
		t.Errorf("Escape closed a window: windows=%d destroys=%d", len(a.Windows()), backend.Destroyed())
	}
	if otherFocus != 0 || otherResize != 0 || otherCancel != 0 {
		t.Errorf("events leaked to other window: focus=%d resize=%d cancel=%d", otherFocus, otherResize, otherCancel)
	}

	staleHandler := target.handler
	if err := w.Close(); err != nil {
		t.Fatalf("Close events window: %v", err)
	}
	postBarrier(t, a)
	if err := a.Post(func() {
		staleHandler(driver.Event{Kind: driver.EventFocus, Window: target, Focused: true})
		staleHandler(driver.Event{Kind: driver.EventResize, Window: target, Size: driver.Size{Width: 1, Height: 1}})
		staleHandler(driver.Event{Kind: driver.EventKey, Window: target, Key: driver.KeyEscape, Action: driver.ActionPress})
	}); err != nil {
		t.Fatalf("Post stale events: %v", err)
	}
	postBarrier(t, a)
	if len(focus) != 2 || len(sizes) != 1 || cancels != 1 {
		t.Errorf("destroyed window delivered callbacks: focus=%v sizes=%v cancels=%d", focus, sizes, cancels)
	}
	a.Quit()
	if err := waitUIReturn(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestLastVisibleWindowPolicies(t *testing.T) {
	t.Run("default quit after hiding the last visible window", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		done := runUIAsync(a)
		w := a.Window("hide last").Size(240, 120)
		_, r := showUI(t, w, backend, factory)
		w.Hide()
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run after last visible Hide: %v", err)
		}
		if !r.closed.Load() || backend.Destroyed() != 1 || len(a.Windows()) != 0 {
			t.Errorf("last visible Hide cleanup: renderer closed=%v destroys=%d windows=%d", r.closed.Load(), backend.Destroyed(), len(a.Windows()))
		}
	})

	t.Run("callback priority veto and hide show", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		a.SetLastWindowAction(LastWindowQuit)
		policyCalls := 0
		a.SetLastWindowCallback(func() LastWindowAction {
			policyCalls++
			if policyCalls == 1 {
				return LastWindowKeepRunning
			}
			return LastWindowQuit
		})
		done := runUIAsync(a)
		allowClose := false
		w := a.Window("callback").Size(240, 120).CloseAction(CloseHide).OnClose(func() bool { return allowClose })
		target, r := showUI(t, w, backend, factory)

		if err := w.Close(); err != nil {
			t.Fatalf("vetoed Close: %v", err)
		}
		settleUI(t, a, r)
		assertUIRunning(t, done)
		if policyCalls != 0 || target.hides != 0 {
			t.Errorf("veto triggered policy or Hide: policy=%d hides=%d", policyCalls, target.hides)
		}

		allowClose = true
		if err := w.Close(); err != nil {
			t.Fatalf("CloseHide: %v", err)
		}
		settleUI(t, a, r)
		assertUIRunning(t, done)
		if policyCalls != 1 || target.hides != 1 || r.closed.Load() || backend.Destroyed() != 0 {
			t.Errorf("first CloseHide: policy=%d hides=%d renderer closed=%v destroys=%d", policyCalls, target.hides, r.closed.Load(), backend.Destroyed())
		}
		w.Show()
		settleUI(t, a, r)
		if err := w.Close(); err != nil {
			t.Fatalf("second CloseHide: %v", err)
		}
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run after callback quit: %v", err)
		}
		if policyCalls != 2 || target.hides != 2 || !r.closed.Load() || backend.Destroyed() != 1 {
			t.Errorf("second CloseHide: policy=%d hides=%d renderer closed=%v destroys=%d", policyCalls, target.hides, r.closed.Load(), backend.Destroyed())
		}
	})

	t.Run("only the final visible transition triggers policy", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		a.SetLastWindowAction(LastWindowQuit)
		policyCalls := 0
		a.SetLastWindowCallback(func() LastWindowAction {
			policyCalls++
			return LastWindowKeepRunning
		})
		done := runUIAsync(a)
		first := a.Window("first").Size(240, 120)
		_, firstRenderer := showUI(t, first, backend, factory)
		second := a.Window("second").Size(240, 120)
		_, secondRenderer := showUI(t, second, backend, factory)

		first.Hide()
		postBarrier(t, a)
		if policyCalls != 0 {
			t.Errorf("hiding one of two visible windows triggered policy %d times", policyCalls)
		}
		if err := first.Close(); err != nil {
			t.Fatalf("destroy hidden window: %v", err)
		}
		postBarrier(t, a)
		if policyCalls != 0 || !firstRenderer.closed.Load() || secondRenderer.closed.Load() {
			t.Errorf("hidden destroy: policy=%d first closed=%v second closed=%v", policyCalls, firstRenderer.closed.Load(), secondRenderer.closed.Load())
		}
		second.Hide()
		postBarrier(t, a)
		assertUIRunning(t, done)
		if policyCalls != 1 {
			t.Errorf("last visible Hide policy calls = %d, want 1", policyCalls)
		}
		second.Show()
		postBarrier(t, a)
		if err := second.Close(); err != nil {
			t.Fatalf("destroy last visible window: %v", err)
		}
		postBarrier(t, a)
		assertUIRunning(t, done)
		if policyCalls != 2 || len(a.Windows()) != 0 {
			t.Errorf("last visible destroy: policy=%d windows=%d, want 2 and 0", policyCalls, len(a.Windows()))
		}
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
}
