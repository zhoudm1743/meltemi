package ui

import (
	"reflect"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

type overlayTestFixture struct {
	app      *App
	backend  *uiTestBackend
	factory  *uiTestRendererFactory
	window   *Window
	target   *uiTestTarget
	renderer *uiTestRenderer
	state    *compositionTestState
	done     <-chan error
	finished bool
}

func newOverlayTestFixture(t *testing.T, content Component, width, height int) *overlayTestFixture {
	t.Helper()
	backend := newUITestBackend()
	app, factory := newUITestApp(backend, &uiTestLog{})
	app.SetLastWindowAction(LastWindowKeepRunning)
	done := runUIAsync(app)
	window := app.Window("overlay").Size(width, height).Content(content)
	target, renderer := showUI(t, window, backend, factory)
	fixture := &overlayTestFixture{
		app: app, backend: backend, factory: factory,
		window: window, target: target, renderer: renderer,
		state: compositionTestStateFor(target), done: done,
	}
	t.Cleanup(func() {
		fixture.finish(t)
		compositionTestStates.Delete(target)
	})
	return fixture
}

func (f *overlayTestFixture) finish(t *testing.T) {
	t.Helper()
	if f.finished {
		return
	}
	f.app.Quit()
	if err := waitUIReturn(t, f.done); err != nil {
		t.Errorf("Run: %v", err)
	}
	f.finished = true
}

func (f *overlayTestFixture) sync(t *testing.T) {
	t.Helper()
	syncStyleUI(t, f.app)
	drainStyleFrames(f.renderer)
}

func (f *overlayTestFixture) post(t *testing.T, fn func()) {
	t.Helper()
	if err := f.app.Post(fn); err != nil {
		t.Fatalf("Post overlay operation: %v", err)
	}
	f.sync(t)
	f.sync(t)
}

func (f *overlayTestFixture) core(t *testing.T, fn func()) {
	t.Helper()
	if err := f.app.core.Post(fn); err != nil {
		t.Fatalf("Post overlay core operation: %v", err)
	}
	f.sync(t)
}

type overlayRecordingRenderer struct {
	renderer
	fakePainter
}

func (r *overlayRecordingRenderer) DrawRect(bounds render.Rect, color render.Color) {
	r.fakePainter.DrawRect(bounds, color)
}

func (r *overlayRecordingRenderer) DrawRoundedRect(bounds render.Rect, color render.Color, radius float64) {
	r.fakePainter.DrawRoundedRect(bounds, color, radius)
}

func (r *overlayRecordingRenderer) DrawRoundedBorder(bounds render.Rect, color render.Color, radius, width float64) {
	r.fakePainter.DrawRoundedBorder(bounds, color, radius, width)
}

func (r *overlayRecordingRenderer) DrawText(value string, at driver.Point, color render.Color, face *text.Face) error {
	return r.fakePainter.DrawText(value, at, color, face)
}

func (r *overlayRecordingRenderer) DrawImage(source *render.Image, destination render.Rect, opacity float64) error {
	return r.fakePainter.DrawImage(source, destination, opacity)
}

func (r *overlayRecordingRenderer) SetClip(bounds render.Rect) {
	r.fakePainter.SetClip(bounds)
}

func (r *overlayRecordingRenderer) ClearClip() {
	r.fakePainter.ClearClip()
}

func TestOverlayDrawOrderAndClip(t *testing.T) {
	t.Run("root then parent and child with independent balanced clips", func(t *testing.T) {
		var draws []drawRecord
		root := fake("root", 200, 140)
		root.log = &draws
		fixture := newOverlayTestFixture(t, root, 200, 140)
		parent := fake("parent", 60, 40)
		parent.log = &draws
		child := fake("child", 50, 30)
		child.log = &draws
		parentBounds := rect(10, 15, 60, 40)
		childBounds := rect(90, 75, 50, 30)
		fixture.post(t, func() {
			parentLayer := fixture.window.pushOverlay(parent, parentBounds, nil, nil)
			fixture.window.pushOverlay(child, childBounds, nil, parentLayer)
		})

		recorder := &overlayRecordingRenderer{renderer: fixture.renderer}
		draws = nil
		fixture.core(t, func() {
			fixture.window.renderer = recorder
			if err := fixture.window.redraw(); err != nil {
				t.Errorf("redraw: %v", err)
			}
		})
		wantDraws := []drawRecord{
			{name: "root", rect: rect(0, 0, 200, 140)},
			{name: "parent", rect: parentBounds},
			{name: "child", rect: childBounds},
		}
		if !reflect.DeepEqual(draws, wantDraws) {
			t.Errorf("draw order = %v, want %v", draws, wantDraws)
		}
		wantOps := []paintOp{
			{kind: "clip", rect: parentBounds},
			{kind: "clearClip"},
			{kind: "clip", rect: childBounds},
			{kind: "clearClip"},
		}
		if !reflect.DeepEqual(recorder.ops, wantOps) {
			t.Errorf("clip operations = %v, want independent overlay clips %v", recorder.ops, wantOps)
		}
	})

	t.Run("styles apply on push and stylesheet replacement", func(t *testing.T) {
		fixture := newOverlayTestFixture(t, Label("root"), 240, 120)
		if err := fixture.app.LoadStyles(writeStyleSheet(t, "#popup-label { width: 144px; height: 31px; }")); err != nil {
			t.Fatalf("LoadStyles initial: %v", err)
		}
		fixture.sync(t)
		styled := Label("popup").ID("popup-label")
		fixture.post(t, func() {
			fixture.window.pushOverlay(styled, rect(20, 20, 180, 60), nil, nil)
		})
		if got, want := styled.measure(Constraints{}), size(144, 31); got != want {
			t.Fatalf("initial overlay style size = %v, want %v", got, want)
		}

		if err := fixture.app.LoadStyles(writeStyleSheet(t, "#popup-label { width: 151px; height: 37px; }")); err != nil {
			t.Fatalf("LoadStyles replacement: %v", err)
		}
		fixture.sync(t)
		if got, want := styled.measure(Constraints{}), size(151, 37); got != want {
			t.Errorf("replacement overlay style size = %v, want %v", got, want)
		}
	})
}

func TestOverlayNestedDismissAndPointerCapture(t *testing.T) {
	t.Run("parent chain outside press and captured release", func(t *testing.T) {
		rootClicks := 0
		root := Button("root").OnClick(func() { rootClicks++ })
		fixture := newOverlayTestFixture(t, root, 240, 180)
		parentClicks := 0
		parent := Button("parent").OnClick(func() { parentClicks++ })
		childClicks := 0
		child := Button("child").OnClick(func() { childClicks++ })
		var dismissals []string
		parentBounds := rect(20, 20, 100, 60)
		childBounds := rect(60, 60, 100, 60)
		var parentLayer *overlay
		fixture.post(t, func() {
			parentLayer = fixture.window.pushOverlay(parent, parentBounds, func() {
				dismissals = append(dismissals, "parent")
			}, nil)
			fixture.window.pushOverlay(child, childBounds, func() {
				dismissals = append(dismissals, "child")
			}, parentLayer)
			fixture.target.handler(leftPress(center(childBounds)))
			fixture.target.handler(leftRelease(center(childBounds)))
		})
		if childClicks != 1 || parentClicks != 0 || rootClicks != 0 {
			t.Fatalf("overlapping click counts = child %d parent %d root %d, want 1/0/0", childClicks, parentClicks, rootClicks)
		}

		parentPoint := driver.Point{X: 30, Y: 30}
		fixture.post(t, func() {
			fixture.target.handler(leftPress(parentPoint))
			fixture.target.handler(leftRelease(parentPoint))
		})
		if len(fixture.window.overlays) != 1 || fixture.window.overlays[0] != parentLayer {
			t.Fatalf("parent click left overlay stack %v, want only its parent", fixture.window.overlays)
		}
		if parentClicks != 1 || childClicks != 1 || rootClicks != 0 {
			t.Errorf("parent click counts = parent %d child %d root %d, want 1/1/0", parentClicks, childClicks, rootClicks)
		}
		if !reflect.DeepEqual(dismissals, []string{"child"}) {
			t.Errorf("dismissals after parent click = %v, want child only", dismissals)
		}

		fixture.post(t, func() {
			fixture.window.pushOverlay(child, childBounds, func() {
				dismissals = append(dismissals, "child")
			}, parentLayer)
			outside := driver.Point{X: 210, Y: 150}
			fixture.target.handler(leftPress(outside))
			fixture.target.handler(leftRelease(outside))
		})
		if len(fixture.window.overlays) != 0 {
			t.Errorf("outside press left %d overlays, want none", len(fixture.window.overlays))
		}
		if rootClicks != 0 || parentClicks != 1 || childClicks != 1 {
			t.Errorf("outside press penetrated: root %d parent %d child %d", rootClicks, parentClicks, childClicks)
		}
		if root.pressed {
			t.Error("outside release armed the underlying root button")
		}
		if !reflect.DeepEqual(dismissals, []string{"child", "child", "parent"}) {
			t.Errorf("outside dismissals = %v, want child then parent", dismissals)
		}

		fixture.post(t, func() {
			parentLayer = fixture.window.pushOverlay(parent, parentBounds, nil, nil)
			fixture.window.pushOverlay(child, childBounds, nil, parentLayer)
			outside := driver.Point{X: 215, Y: 155}
			fixture.target.handler(leftPress(center(childBounds)))
			fixture.target.handler(driver.Event{Kind: driver.EventMouseMove, Position: outside})
			fixture.target.handler(leftRelease(outside))
		})
		if rootClicks != 0 || childClicks != 1 {
			t.Errorf("captured outside release clicked root or child: root %d child %d", rootClicks, childClicks)
		}
		if len(fixture.window.overlays) != 2 {
			t.Errorf("captured release dismissed overlays: %d remain, want 2", len(fixture.window.overlays))
		}
		if fixture.window.router.press != nil || child.pressed {
			t.Error("captured release left press state armed")
		}
	})

	t.Run("push and pop cancel armed presses without activating them", func(t *testing.T) {
		rootClicks := 0
		root := Button("root").OnClick(func() { rootClicks++ })
		fixture := newOverlayTestFixture(t, root, 200, 120)
		popupClicks := 0
		popup := Button("popup").OnClick(func() { popupClicks++ })
		bounds := rect(20, 20, 100, 60)
		fixture.post(t, func() {
			point := center(bounds)
			fixture.target.handler(leftPress(point))
			fixture.window.pushOverlay(popup, bounds, nil, nil)
			fixture.target.handler(leftPress(point))
			fixture.window.popOverlay()
			fixture.target.handler(leftRelease(point))
		})
		if root.pressed || popup.pressed {
			t.Errorf("press flags survived overlay transitions: root=%v popup=%v", root.pressed, popup.pressed)
		}
		if rootClicks != 0 || popupClicks != 0 {
			t.Errorf("cancelled presses activated: root=%d popup=%d", rootClicks, popupClicks)
		}
		if fixture.window.router.press != nil {
			t.Error("router retained an armed press after pop")
		}
	})

	t.Run("wheel stays in the topmost overlay under the cursor", func(t *testing.T) {
		rootScroll := Scroll(fake("root-content", 100, 300))
		fixture := newOverlayTestFixture(t, rootScroll, 120, 100)
		parentScroll := Scroll(fake("parent-content", 100, 300))
		childScroll := Scroll(fake("child-content", 100, 300))
		parentBounds := rect(0, 0, 70, 70)
		childBounds := rect(30, 30, 70, 70)
		fixture.post(t, func() {
			parent := fixture.window.pushOverlay(parentScroll, parentBounds, nil, nil)
			fixture.window.pushOverlay(childScroll, childBounds, nil, parent)
			fixture.target.handler(scrollWheelEvent(driver.Point{X: 50, Y: 50}, 0, -1))
		})
		if x, y := childScroll.Offset(); x != 0 || y != 40 {
			t.Fatalf("child offset = (%v,%v), want (0,40)", x, y)
		}
		if _, y := parentScroll.Offset(); y != 0 {
			t.Errorf("parent scrolled under child: y=%v", y)
		}
		if _, y := rootScroll.Offset(); y != 0 {
			t.Errorf("root scrolled under child: y=%v", y)
		}

		fixture.post(t, func() {
			fixture.target.handler(scrollWheelEvent(driver.Point{X: 10, Y: 10}, 0, -1))
		})
		if _, y := parentScroll.Offset(); y != 40 {
			t.Errorf("parent offset after direct wheel = %v, want 40", y)
		}
		if _, y := rootScroll.Offset(); y != 0 {
			t.Errorf("root scrolled over parent: y=%v", y)
		}

		fixture.post(t, func() {
			childScroll.SetOffset(0, 1000)
			fixture.target.handler(scrollWheelEvent(driver.Point{X: 50, Y: 50}, 0, -1))
			fixture.target.handler(scrollWheelEvent(driver.Point{X: 110, Y: 90}, 0, -1))
		})
		if _, y := parentScroll.Offset(); y != 40 {
			t.Errorf("at-limit child wheel fell through to parent: y=%v", y)
		}
		if _, y := rootScroll.Offset(); y != 0 {
			t.Errorf("wheel outside overlays reached root: y=%v", y)
		}
		if len(fixture.window.overlays) != 2 {
			t.Errorf("wheel dismissed overlays: %d remain", len(fixture.window.overlays))
		}
	})
}

func TestOverlayEscapeAndFocus(t *testing.T) {
	t.Run("Tab Enter and Escape press repeat release", func(t *testing.T) {
		root := Button("root")
		fixture := newOverlayTestFixture(t, root, 240, 180)
		cancels := 0
		fixture.window.OnCancel(func() { cancels++ })
		parentFirst := Button("parent-first")
		parentSecond := Button("parent-second")
		childFirst := Button("child-first")
		childClicks := 0
		childSecond := Button("child-second").OnClick(func() { childClicks++ })
		parentBounds := rect(20, 20, 160, 80)
		childBounds := rect(50, 40, 160, 80)
		fixture.post(t, func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			parent := fixture.window.pushOverlay(Column(parentFirst, parentSecond), parentBounds, nil, nil)
			fixture.window.pushOverlay(Column(childFirst, childSecond), childBounds, nil, parent)
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
		})
		if root.focused || parentFirst.focused || parentSecond.focused {
			t.Error("focus escaped the top overlay")
		}
		if fixture.window.router.focus != childFirst || !childFirst.focused {
			t.Fatalf("first overlay Tab focus = %v, want child-first", fixture.window.router.focus)
		}
		fixture.post(t, func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			fixture.target.handler(keyEvent(driver.KeyEnter, 0))
		})
		if childClicks != 1 {
			t.Errorf("Enter clicks = %d, want 1 on child-second", childClicks)
		}
		fixture.post(t, func() {
			fixture.target.handler(keyEvent(driver.KeyTab, driver.ModShift))
			fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: driver.KeyEscape, Action: driver.ActionPress})
		})
		if len(fixture.window.overlays) != 1 {
			t.Fatalf("Escape press left %d overlays, want parent only", len(fixture.window.overlays))
		}
		if childFirst.focused || childSecond.focused || fixture.window.router.focus != nil {
			t.Error("Escape left child focus attached")
		}
		fixture.post(t, func() {
			fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: driver.KeyEscape, Action: driver.ActionRepeat})
			fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: driver.KeyEscape, Action: driver.ActionRelease})
		})
		if len(fixture.window.overlays) != 1 || cancels != 0 {
			t.Errorf("Escape repeat/release closed parent or cancelled window: overlays=%d cancels=%d", len(fixture.window.overlays), cancels)
		}
		fixture.post(t, func() {
			fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: driver.KeyEscape, Action: driver.ActionPress})
			fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: driver.KeyEscape, Action: driver.ActionPress})
			fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: driver.KeyEscape, Action: driver.ActionRepeat})
			fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: driver.KeyEscape, Action: driver.ActionRelease})
		})
		if len(fixture.window.overlays) != 0 || cancels != 1 {
			t.Errorf("final Escape state: overlays=%d cancels=%d, want 0 and 1", len(fixture.window.overlays), cancels)
		}
	})

	t.Run("composition transfers cancel old state and gate shortcuts only while active", func(t *testing.T) {
		rootInput := Input(nil)
		fixture := newOverlayTestFixture(t, rootInput, 220, 120)
		popupInput := Input(nil)
		popupButton := Button("done")
		shortcuts := 0
		item := MenuItem("save").Shortcut(ModCtrl, 's').OnClick(func() { shortcuts++ })
		cancels := 0
		fixture.window.OnCancel(func() { cancels++ })
		fixture.post(t, func() {
			fixture.window.router.setMenuRoots(Menu("File", item))
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			setCompositionTestPreedit(fixture.target, "根", 1)
			fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
			fixture.window.pushOverlay(Column(popupInput, popupButton), rect(20, 20, 180, 80), nil, nil)
			fixture.target.handler(keyEvent(driver.KeyS, driver.ModCtrl))
		})
		if rootInput.focused || rootInput.composition.active {
			t.Error("pushing overlay retained root focus or preedit")
		}
		if fixture.window.router.composing {
			t.Fatal("pushing overlay left the router composition gate latched")
		}
		if shortcuts != 1 {
			t.Fatalf("shortcut after overlay push = %d, want 1", shortcuts)
		}
		if fixture.state.enabled || fixture.state.cancelCalls == 0 {
			t.Fatalf("overlay push backend state: enabled=%v cancelCalls=%d", fixture.state.enabled, fixture.state.cancelCalls)
		}

		fixture.post(t, func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			setCompositionTestPreedit(fixture.target, "子", 1)
			fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
			fixture.target.handler(keyEvent(driver.KeyS, driver.ModCtrl))
		})
		if !popupInput.composition.active || rootInput.composition.active {
			t.Errorf("composition routed to wrong tree: popup=%+v root=%+v", popupInput.composition, rootInput.composition)
		}
		if shortcuts != 1 {
			t.Errorf("shortcut fired during overlay composition: %d", shortcuts)
		}
		fixture.post(t, func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			fixture.target.handler(keyEvent(driver.KeyS, driver.ModCtrl))
		})
		if fixture.window.router.focus != popupButton || popupInput.composition.active || fixture.window.router.composing {
			t.Error("overlay focus transfer retained old input composition")
		}
		if shortcuts != 2 {
			t.Errorf("shortcut after overlay focus transfer = %d, want 2", shortcuts)
		}

		fixture.post(t, func() {
			fixture.target.handler(keyEvent(driver.KeyTab, driver.ModShift))
			setCompositionTestPreedit(fixture.target, "闭", 1)
			fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
			fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: driver.KeyEscape, Action: driver.ActionPress})
			fixture.target.handler(keyEvent(driver.KeyS, driver.ModCtrl))
		})
		if len(fixture.window.overlays) != 0 || cancels != 0 {
			t.Errorf("Escape during composition: overlays=%d cancels=%d", len(fixture.window.overlays), cancels)
		}
		if popupInput.focused || popupInput.composition.active || fixture.window.router.composing {
			t.Error("Escape during composition left overlay input state latched")
		}
		if shortcuts != 3 {
			t.Errorf("shortcut after composition Escape = %d, want 3", shortcuts)
		}
	})
}

func TestOverlayWindowLifecycleCleanup(t *testing.T) {
	tests := []struct {
		name       string
		operate    func(*overlayTestFixture) error
		wantRouter bool
		wantError  bool
	}{
		{name: "Content", operate: func(f *overlayTestFixture) error {
			f.window.Content(Label("replacement"))
			return nil
		}, wantRouter: true},
		{name: "Hide", operate: func(f *overlayTestFixture) error {
			f.window.Hide()
			return nil
		}, wantRouter: true},
		{name: "CloseHide", operate: func(f *overlayTestFixture) error {
			f.window.CloseAction(CloseHide)
			return f.window.Close()
		}, wantRouter: true},
		{name: "CloseDestroy", operate: func(f *overlayTestFixture) error {
			return f.window.Close()
		}},
		{name: "destroy", operate: func(f *overlayTestFixture) error {
			return f.window.destroy(false)
		}},
		{name: "destroy MakeCurrent failure", operate: func(f *overlayTestFixture) error {
			f.target.makeErr = errOverlayTestMakeCurrent
			defer func() { f.target.makeErr = nil }()
			return f.window.destroy(false)
		}, wantRouter: true, wantError: true},
		{name: "finalize", operate: func(f *overlayTestFixture) error {
			return f.window.finalize()
		}},
		{name: "finalize MakeCurrent failure", operate: func(f *overlayTestFixture) error {
			f.target.makeErr = errOverlayTestMakeCurrent
			return f.window.finalize()
		}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOverlayTestFixture(t, Button("root"), 220, 120)
			popupInput := Input(nil)
			childBounds := rect(30, 30, 160, 40)
			var dismissals []string
			fixture.post(t, func() {
				parent := fixture.window.pushOverlay(Button("parent"), rect(10, 10, 180, 80), func() {
					dismissals = append(dismissals, "parent")
				}, nil)
				fixture.window.pushOverlay(popupInput, childBounds, func() {
					dismissals = append(dismissals, "child")
				}, parent)
				fixture.target.handler(keyEvent(driver.KeyTab, 0))
				fixture.target.handler(leftPress(center(childBounds)))
				setCompositionTestPreedit(fixture.target, "清", 1)
				fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
				fixture.state.active = false
			})
			if fixture.window.router.focus != popupInput || fixture.window.router.press != popupInput || !fixture.window.router.composing {
				t.Fatal("test setup did not arm overlay focus, press and composition")
			}

			var operationErr error
			if test.wantError {
				fixture.core(t, func() { operationErr = test.operate(fixture) })
			} else {
				fixture.post(t, func() { operationErr = test.operate(fixture) })
			}
			if (operationErr != nil) != test.wantError {
				t.Errorf("lifecycle operation error = %v, wantError=%v", operationErr, test.wantError)
			}
			if len(fixture.window.overlays) != 0 {
				t.Errorf("%s left %d overlays", test.name, len(fixture.window.overlays))
			}
			if !reflect.DeepEqual(dismissals, []string{"child", "parent"}) {
				t.Errorf("%s dismissals = %v, want child then parent", test.name, dismissals)
			}
			if popupInput.focused || popupInput.composition.active || popupInput.compositionFocusHandler != nil {
				t.Errorf("%s left popup input state: focused=%v composition=%+v handler=%v", test.name, popupInput.focused, popupInput.composition, popupInput.compositionFocusHandler != nil)
			}
			if fixture.state.enabled || fixture.state.cancelCalls == 0 {
				t.Errorf("%s left native composition enabled=%v cancelCalls=%d", test.name, fixture.state.enabled, fixture.state.cancelCalls)
			}
			if (fixture.window.router != nil) != test.wantRouter {
				t.Errorf("%s router presence = %v, want %v", test.name, fixture.window.router != nil, test.wantRouter)
			}
			if router := fixture.window.router; router != nil {
				if router.focus != nil || router.press != nil || router.hover != nil || router.composing {
					t.Errorf("%s left router interaction: focus=%v press=%v hover=%v composing=%v", test.name, router.focus, router.press, router.hover, router.composing)
				}
			}
		})
	}

	t.Run("one window cleanup preserves another window overlay and composition", func(t *testing.T) {
		fixture := newOverlayTestFixture(t, Button("first-root"), 200, 100)
		secondWindow := fixture.app.Window("second").Size(200, 100).Content(Button("second-root"))
		secondTarget, secondRenderer := showUI(t, secondWindow, fixture.backend, fixture.factory)
		secondState := compositionTestStateFor(secondTarget)
		t.Cleanup(func() { compositionTestStates.Delete(secondTarget) })
		firstInput := Input(nil)
		secondInput := Input(nil)
		fixture.post(t, func() {
			fixture.window.pushOverlay(firstInput, rect(10, 10, 160, 40), nil, nil)
			secondWindow.pushOverlay(secondInput, rect(15, 15, 160, 40), nil, nil)
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			secondTarget.handler(keyEvent(driver.KeyTab, 0))
			setCompositionTestPreedit(fixture.target, "一", 1)
			fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
			setCompositionTestPreedit(secondTarget, "二", 1)
			secondTarget.handler(compositionTestEvent(secondTarget, driver.EventCompositionStart))
			fixture.state.active = false
		})
		drainStyleFrames(secondRenderer)
		if !firstInput.composition.active || !secondInput.composition.active {
			t.Fatal("test setup did not start independent compositions")
		}

		fixture.post(t, func() { fixture.window.Hide() })
		drainStyleFrames(secondRenderer)
		if len(fixture.window.overlays) != 0 || firstInput.focused || firstInput.composition.active || fixture.window.router.composing {
			t.Error("first window retained overlay interaction after Hide")
		}
		if len(secondWindow.overlays) != 1 || secondWindow.router.focus != secondInput || !secondInput.focused || !secondInput.composition.active || secondWindow.router.composing != true {
			t.Errorf("second window interaction changed: overlays=%d focus=%v inputFocused=%v composition=%+v composing=%v", len(secondWindow.overlays), secondWindow.router.focus == secondInput, secondInput.focused, secondInput.composition, secondWindow.router.composing)
		}
		if fixture.state.enabled || !secondState.enabled {
			t.Errorf("native composition isolation: first enabled=%v second enabled=%v", fixture.state.enabled, secondState.enabled)
		}
	})
}

func TestOverlayMandatoryTeardownReentrancy(t *testing.T) {
	tests := []struct {
		name       string
		operate    func(*overlayTestFixture) error
		wantRouter bool
		wantError  bool
	}{
		{name: "Content", operate: func(f *overlayTestFixture) error {
			f.window.Content(Label("replacement"))
			return nil
		}, wantRouter: true},
		{name: "Hide", operate: func(f *overlayTestFixture) error {
			f.window.Hide()
			return nil
		}, wantRouter: true},
		{name: "CloseHide", operate: func(f *overlayTestFixture) error {
			f.window.CloseAction(CloseHide)
			return f.window.Close()
		}, wantRouter: true},
		{name: "CloseDestroy", operate: func(f *overlayTestFixture) error {
			return f.window.Close()
		}},
		{name: "destroy", operate: func(f *overlayTestFixture) error {
			return f.window.destroy(false)
		}},
		{name: "destroy MakeCurrent failure", operate: func(f *overlayTestFixture) error {
			f.target.makeErr = errOverlayTestMakeCurrent
			defer func() { f.target.makeErr = nil }()
			return f.window.destroy(false)
		}, wantRouter: true, wantError: true},
		{name: "finalize", operate: func(f *overlayTestFixture) error {
			return f.window.finalize()
		}},
		{name: "finalize MakeCurrent failure", operate: func(f *overlayTestFixture) error {
			f.target.makeErr = errOverlayTestMakeCurrent
			return f.window.finalize()
		}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOverlayTestFixture(t, Button("root"), 220, 120)
			replacementInput := Input(nil)
			fixture.post(t, func() {
				fixture.window.pushOverlay(Button("original"), rect(10, 10, 180, 70), func() {
					fixture.window.pushOverlay(replacementInput, rect(20, 20, 160, 40), nil, nil)
				}, nil)
			})

			var operationErr error
			if test.wantError {
				fixture.core(t, func() { operationErr = test.operate(fixture) })
			} else {
				fixture.post(t, func() { operationErr = test.operate(fixture) })
			}
			if (operationErr != nil) != test.wantError {
				t.Errorf("lifecycle operation error = %v, wantError=%v", operationErr, test.wantError)
			}
			if len(fixture.window.overlays) != 0 {
				t.Errorf("callback reopened %d overlays during mandatory teardown", len(fixture.window.overlays))
			}
			if replacementInput.compositionFocusHandler != nil || replacementInput.clipboard != nil {
				t.Errorf("callback-created input retained hooks: compositionHandler=%v clipboard=%v", replacementInput.compositionFocusHandler != nil, replacementInput.clipboard)
			}
			if (fixture.window.router != nil) != test.wantRouter {
				t.Errorf("router presence = %v, want %v", fixture.window.router != nil, test.wantRouter)
			}
			if router := fixture.window.router; router != nil {
				if router.root != fixture.window.root || router.root == replacementInput {
					t.Errorf("router root does not match surviving window root")
				}
				if router.focus != nil || router.press != nil || router.composing {
					t.Errorf("router retained callback interaction: focus=%v press=%v composing=%v", router.focus, router.press, router.composing)
				}
			}
		})
	}

	t.Run("ordinary clear preserves a callback-created replacement", func(t *testing.T) {
		fixture := newOverlayTestFixture(t, Button("root"), 220, 120)
		replacementInput := Input(nil)
		var replacement *overlay
		fixture.post(t, func() {
			fixture.window.pushOverlay(Button("original"), rect(10, 10, 180, 70), func() {
				replacement = fixture.window.pushOverlay(replacementInput, rect(20, 20, 160, 40), nil, nil)
			}, nil)
			fixture.window.clearOverlays()
		})
		if replacement == nil || len(fixture.window.overlays) != 1 || fixture.window.overlays[0] != replacement {
			t.Fatalf("ordinary dismissal did not preserve callback replacement: %v", fixture.window.overlays)
		}
		if fixture.window.router.root != replacement.root || replacementInput.compositionFocusHandler == nil || replacementInput.clipboard != fixture.target {
			t.Error("ordinary replacement is not active with live input hooks")
		}
		fixture.post(t, func() { fixture.target.handler(keyEvent(driver.KeyTab, 0)) })
		if fixture.window.router.focus != replacementInput || !replacementInput.focused {
			t.Error("ordinary callback replacement cannot receive keyboard focus")
		}
	})
}

func TestOverlayPreShowActivationAndClipboard(t *testing.T) {
	backend := newUITestBackend()
	app, factory := newUITestApp(backend, &uiTestLog{})
	app.SetLastWindowAction(LastWindowKeepRunning)
	done := runUIAsync(app)
	finished := false
	t.Cleanup(func() {
		if !finished {
			app.Quit()
			if err := waitUIReturn(t, done); err != nil {
				t.Errorf("Run: %v", err)
			}
		}
	})
	rootClicks := 0
	root := Button("root").OnClick(func() { rootClicks++ })
	window := app.Window("pre-show overlay").Size(220, 120).Content(root)
	postBarrier(t, app)
	popupInput := Input(nil)
	popupClicks := 0
	popupButton := Button("popup").OnClick(func() { popupClicks++ })
	bounds := rect(20, 20, 180, 80)
	var layer *overlay
	if err := app.Post(func() {
		layer = window.pushOverlay(Column(popupInput, popupButton), bounds, nil, nil)
		window.Show()
	}); err != nil {
		t.Fatalf("Post pre-Show overlay and Show: %v", err)
	}
	target := receiveUI(t, backend.created, "pre-Show native window creation")
	renderer := receiveUI(t, factory.created, "pre-Show renderer creation")
	receiveUI(t, target.shown, "pre-Show native Show")
	receiveUI(t, renderer.frames, "pre-Show first frame")
	postBarrier(t, app)
	drainStyleFrames(renderer)
	t.Cleanup(func() { compositionTestStates.Delete(target) })
	if layer == nil || window.router.root != layer.root {
		t.Errorf("Show did not activate the pre-existing overlay")
	}
	if popupInput.clipboard != target {
		t.Errorf("pre-Show input clipboard = %v, want native target", popupInput.clipboard)
	}
	if popupInput.compositionFocusHandler == nil || popupInput.face == nil {
		t.Error("pre-Show overlay lost composition or font setup")
	}

	fixture := &overlayTestFixture{app: app, window: window, target: target, renderer: renderer}
	fixture.post(t, func() {
		point := center(popupButton.bounds)
		target.handler(leftPress(point))
		target.handler(leftRelease(point))
	})
	if popupClicks != 1 || rootClicks != 0 {
		t.Errorf("pre-Show pointer clicks = popup %d root %d, want 1/0", popupClicks, rootClicks)
	}
	keyboardFocusOK := false
	fixture.post(t, func() {
		target.handler(keyEvent(driver.KeyTab, driver.ModShift))
		keyboardFocusOK = window.router.focus == popupInput && popupInput.focused
		target.handler(keyEvent(driver.KeyTab, 0))
		target.handler(keyEvent(driver.KeyEnter, 0))
	})
	if !keyboardFocusOK || window.router.focus != popupButton {
		t.Error("pre-Show overlay did not retain keyboard focus routing")
	}
	if popupClicks != 2 || rootClicks != 0 {
		t.Errorf("pre-Show keyboard clicks = popup %d root %d, want 2/0", popupClicks, rootClicks)
	}
	app.Quit()
	if err := waitUIReturn(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	finished = true
}

func TestOverlayMixedChainsPreserveIndependentLayers(t *testing.T) {
	t.Run("inactive descendant removal preserves focused independent top", func(t *testing.T) {
		fixture := newOverlayTestFixture(t, Button("root"), 240, 120)
		a := Button("A")
		b := Button("B")
		c := Button("C")
		cClicks := 0
		c.OnClick(func() { cClicks++ })
		var dismissals []string
		var layerA, layerC *overlay
		fixture.post(t, func() {
			layerA = fixture.window.pushOverlay(a, rect(10, 10, 70, 60), func() {
				dismissals = append(dismissals, "A")
			}, nil)
			layerB := fixture.window.pushOverlay(b, rect(80, 20, 70, 60), func() {
				dismissals = append(dismissals, "B")
			}, layerA)
			layerC = fixture.window.pushOverlay(c, rect(150, 30, 70, 60), func() {
				dismissals = append(dismissals, "C")
			}, nil)
			_ = layerB
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			fixture.target.handler(leftPress(driver.Point{X: 20, Y: 20}))
			fixture.target.handler(leftRelease(driver.Point{X: 20, Y: 20}))
		})
		if len(fixture.window.overlays) != 2 || fixture.window.overlays[0] != layerA || fixture.window.overlays[1] != layerC {
			t.Errorf("mixed stack after A click = %v, want [A C]", fixture.window.overlays)
		}
		if !reflect.DeepEqual(dismissals, []string{"B"}) {
			t.Errorf("dismissals = %v, want B only", dismissals)
		}
		if fixture.window.router.root != layerC.root || fixture.window.router.focus != c || !c.focused {
			t.Error("independent top lost its active root or focus")
		}
		fixture.post(t, func() { fixture.target.handler(keyEvent(driver.KeyEnter, 0)) })
		if cClicks != 1 {
			t.Errorf("independent top Enter clicks = %d, want 1", cClicks)
		}
	})

	t.Run("active descendant chain removal activates surviving independent top", func(t *testing.T) {
		fixture := newOverlayTestFixture(t, Button("root"), 240, 120)
		a := Button("A")
		b := Button("B")
		c := Button("C")
		d := Button("D")
		cClicks := 0
		c.OnClick(func() { cClicks++ })
		var dismissals []string
		var layerA, layerC *overlay
		fixture.post(t, func() {
			layerA = fixture.window.pushOverlay(a, rect(10, 10, 70, 60), nil, nil)
			layerC = fixture.window.pushOverlay(c, rect(80, 20, 70, 60), func() {
				dismissals = append(dismissals, "C")
			}, nil)
			layerB := fixture.window.pushOverlay(b, rect(150, 30, 70, 60), func() {
				dismissals = append(dismissals, "B")
			}, layerA)
			fixture.window.pushOverlay(d, rect(150, 30, 70, 60), func() {
				dismissals = append(dismissals, "D")
			}, layerB)
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			fixture.target.handler(leftPress(driver.Point{X: 20, Y: 20}))
			fixture.target.handler(leftRelease(driver.Point{X: 20, Y: 20}))
		})
		if len(fixture.window.overlays) != 2 || fixture.window.overlays[0] != layerA || fixture.window.overlays[1] != layerC {
			t.Errorf("mixed stack after active-chain trim = %v, want [A C]", fixture.window.overlays)
		}
		if !reflect.DeepEqual(dismissals, []string{"D", "B"}) {
			t.Errorf("dismissals = %v, want D then B", dismissals)
		}
		if fixture.window.router.root != layerC.root || fixture.window.router.focus != nil || d.focused {
			t.Error("surviving independent top did not receive clean active routing")
		}
		fixture.post(t, func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			fixture.target.handler(keyEvent(driver.KeyEnter, 0))
		})
		if cClicks != 1 {
			t.Errorf("surviving independent keyboard clicks = %d, want 1", cClicks)
		}
	})
}

func TestOverlayOutsideDismissAllMouseButtons(t *testing.T) {
	for _, button := range []driver.MouseButton{driver.MouseButtonLeft, driver.MouseButtonMiddle, driver.MouseButtonRight} {
		t.Run(string(rune('0'+button)), func(t *testing.T) {
			rootClicks := 0
			root := Button("root").OnClick(func() { rootClicks++ })
			fixture := newOverlayTestFixture(t, root, 220, 120)
			dismissals := 0
			fixture.post(t, func() {
				fixture.window.pushOverlay(Button("popup"), rect(20, 20, 80, 50), func() { dismissals++ }, nil)
				outside := driver.Point{X: 180, Y: 90}
				fixture.target.handler(driver.Event{Kind: driver.EventMouseButton, Button: button, Action: driver.ActionPress, Position: outside})
				fixture.target.handler(driver.Event{Kind: driver.EventMouseButton, Button: button, Action: driver.ActionRelease, Position: outside})
			})
			if len(fixture.window.overlays) != 0 || dismissals != 1 {
				t.Errorf("button %d outside dismissal: overlays=%d dismissals=%d", button, len(fixture.window.overlays), dismissals)
			}
			if rootClicks != 0 || root.pressed || fixture.window.router.press != nil {
				t.Errorf("button %d outside press penetrated: clicks=%d pressed=%v routerPress=%v", button, rootClicks, root.pressed, fixture.window.router.press)
			}
			if fixture.window.overlayDismissedPress {
				t.Errorf("button %d matching release left dismissal capture latched", button)
			}
			fixture.post(t, func() {
				point := center(root.bounds)
				fixture.target.handler(leftPress(point))
				fixture.target.handler(leftRelease(point))
			})
			if rootClicks != 1 {
				t.Errorf("root left click after button %d cleanup = %d, want 1", button, rootClicks)
			}
		})
	}

	t.Run("non-left inside press preserves overlay and armed left capture semantics", func(t *testing.T) {
		rootClicks := 0
		root := Button("root").OnClick(func() { rootClicks++ })
		fixture := newOverlayTestFixture(t, root, 220, 120)
		popup := Button("popup")
		dismissals := 0
		bounds := rect(20, 20, 100, 60)
		fixture.post(t, func() {
			fixture.window.pushOverlay(popup, bounds, func() { dismissals++ }, nil)
			for _, button := range []driver.MouseButton{driver.MouseButtonMiddle, driver.MouseButtonRight} {
				fixture.target.handler(driver.Event{Kind: driver.EventMouseButton, Button: button, Action: driver.ActionPress, Position: center(bounds)})
				fixture.target.handler(driver.Event{Kind: driver.EventMouseButton, Button: button, Action: driver.ActionRelease, Position: center(bounds)})
			}
		})
		if len(fixture.window.overlays) != 1 || dismissals != 0 || popup.pressed || rootClicks != 0 {
			t.Errorf("non-left inside semantics changed: overlays=%d dismissals=%d pressed=%v rootClicks=%d", len(fixture.window.overlays), dismissals, popup.pressed, rootClicks)
		}
		fixture.post(t, func() {
			outside := driver.Point{X: 190, Y: 100}
			fixture.target.handler(leftPress(center(bounds)))
			fixture.target.handler(driver.Event{Kind: driver.EventMouseMove, Position: outside})
			fixture.target.handler(leftRelease(outside))
		})
		if len(fixture.window.overlays) != 1 || popup.pressed || rootClicks != 0 {
			t.Error("left armed capture regressed after non-left inside events")
		}
	})
}

var errOverlayTestMakeCurrent = &overlayTestError{}

type overlayTestError struct{}

func (*overlayTestError) Error() string { return "overlay test: MakeCurrent failed" }

func TestOverlayHoverBacktrackingPreservesIndependentChains(t *testing.T) {
	rootClicks := 0
	root := Button("root").OnClick(func() { rootClicks++ })
	fixture := newOverlayTestFixture(t, root, 240, 120)
	a := Button("A")
	b := Button("B")
	c := Button("C")
	d := Button("D")
	var layerA, layerB, layerC, layerD *overlay
	fixture.post(t, func() {
		layerA = fixture.window.pushOverlay(a, rect(0, 0, 60, 60), nil, nil)
		layerB = fixture.window.pushOverlay(b, rect(10, 10, 60, 60), nil, layerA)
		layerC = fixture.window.pushOverlay(c, rect(100, 0, 60, 60), nil, nil)
		layerD = fixture.window.pushOverlay(d, rect(110, 10, 60, 60), nil, layerC)
	})

	// B belongs to an inactive independent chain. Hovering it must not discard
	// or retarget the active C>D chain.
	fixture.post(t, func() {
		fixture.target.handler(driver.Event{Kind: driver.EventMouseMove, Position: driver.Point{X: 20, Y: 20}})
	})
	if len(fixture.window.overlays) != 4 || fixture.window.router.root != layerD.root || b.hover {
		t.Errorf("inactive independent hover changed stack/root: overlays=%d root=%p B hover=%v",
			len(fixture.window.overlays), fixture.window.router.root, b.hover)
	}

	// C is the active layer's parent. Hover just outside D trims only D and
	// delivers the move to C, preserving the independent A>B chain.
	fixture.post(t, func() {
		fixture.target.handler(driver.Event{Kind: driver.EventMouseMove, Position: driver.Point{X: 105, Y: 5}})
	})
	if len(fixture.window.overlays) != 3 || fixture.window.overlays[0] != layerA ||
		fixture.window.overlays[1] != layerB || fixture.window.overlays[2] != layerC ||
		fixture.window.router.root != layerC.root || !c.hover {
		t.Errorf("parent hover did not trim/route correctly: overlays=%v root=%p C hover=%v",
			fixture.window.overlays, fixture.window.router.root, c.hover)
	}

	// Build a pure A>B>D chain: moving over A trims both descendants and
	// activates A. Then verify an armed D press suppresses that retargeting.
	fixture.post(t, func() {
		fixture.window.clearOverlays()
		layerA = fixture.window.pushOverlay(a, rect(0, 0, 60, 60), nil, nil)
		layerB = fixture.window.pushOverlay(b, rect(10, 10, 60, 60), nil, layerA)
		layerD = fixture.window.pushOverlay(d, rect(110, 10, 60, 60), nil, layerB)
		fixture.target.handler(driver.Event{Kind: driver.EventMouseMove, Position: driver.Point{X: 5, Y: 5}})
	})
	if len(fixture.window.overlays) != 1 || fixture.window.overlays[0] != layerA ||
		fixture.window.router.root != layerA.root || !a.hover {
		t.Errorf("pure-chain backtracking failed: overlays=%d root=%p A hover=%v",
			len(fixture.window.overlays), fixture.window.router.root, a.hover)
	}

	fixture.post(t, func() {
		layerB = fixture.window.pushOverlay(b, rect(10, 10, 60, 60), nil, layerA)
		layerD = fixture.window.pushOverlay(d, rect(110, 10, 60, 60), nil, layerB)
		point := center(d.bounds)
		fixture.target.handler(leftPress(point))
		fixture.target.handler(driver.Event{Kind: driver.EventMouseMove, Position: driver.Point{X: 5, Y: 5}})
	})
	if len(fixture.window.overlays) != 3 || fixture.window.router.root != layerD.root || !d.pressed {
		t.Error("armed pointer capture allowed hover to trim or retarget the chain")
	}
	fixture.post(t, func() {
		fixture.target.handler(leftRelease(driver.Point{X: 5, Y: 5}))
	})
	if len(fixture.window.overlays) != 3 || d.pressed || rootClicks != 0 {
		t.Errorf("captured release dismissal/penetration changed: overlays=%d pressed=%v root=%d",
			len(fixture.window.overlays), d.pressed, rootClicks)
	}
}
