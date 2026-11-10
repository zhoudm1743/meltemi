package ui

import (
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/image/font/gofont/goregular"

	"math"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

type compositionTestState struct {
	text        string
	caret       int
	active      bool
	enabled     bool
	cancelCalls int
	spots       [][2]int
}

var compositionTestStates sync.Map

func compositionTestStateFor(target *uiTestTarget) *compositionTestState {
	state, _ := compositionTestStates.LoadOrStore(target, &compositionTestState{enabled: true})
	return state.(*compositionTestState)
}

func (w *uiTestTarget) Preedit() (text string, caret int, active bool) {
	state := compositionTestStateFor(w)
	return state.text, state.caret, state.active
}

func (w *uiTestTarget) SetSpot(x, y int) {
	state := compositionTestStateFor(w)
	state.spots = append(state.spots, [2]int{x, y})
}

func (w *uiTestTarget) SetCompositionEnabled(enabled bool) {
	state := compositionTestStateFor(w)
	if !enabled && state.enabled {
		state.cancelCalls++
	}
	state.enabled = enabled
	if enabled || !state.active {
		return
	}
	state.text = ""
	state.caret = 0
	state.active = false
	if w.handler != nil {
		w.handler(driver.Event{Kind: driver.EventCompositionEnd, Window: w})
	}
}

type compositionTestFixture struct {
	app      *App
	window   *Window
	target   *uiTestTarget
	state    *compositionTestState
	done     <-chan error
	finished bool
}

func newCompositionTestFixture(t *testing.T, content Component, width, height int) *compositionTestFixture {
	t.Helper()
	backend := newUITestBackend()
	app, factory := newUITestApp(backend, &uiTestLog{})
	app.SetLastWindowAction(LastWindowKeepRunning)
	done := runUIAsync(app)
	window := app.Window("composition").Size(width, height).Content(content)
	target, _ := showUI(t, window, backend, factory)
	fixture := &compositionTestFixture{
		app: app, window: window, target: target,
		state: compositionTestStateFor(target), done: done,
	}
	t.Cleanup(func() {
		if !fixture.finished {
			app.Quit()
			if err := waitUIReturn(t, done); err != nil {
				t.Errorf("Run: %v", err)
			}
		}
		compositionTestStates.Delete(target)
	})
	return fixture
}

func (f *compositionTestFixture) finish(t *testing.T) {
	t.Helper()
	f.app.Quit()
	if err := waitUIReturn(t, f.done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	f.finished = true
}

func (f *compositionTestFixture) barrier(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	if err := f.app.core.Post(func() { close(done) }); err != nil {
		t.Fatalf("Post completion barrier: %v", err)
	}
	receiveUI(t, done, "posted operation and redraw completion")
}

func compositionTestEvent(target *uiTestTarget, kind driver.EventKind) driver.Event {
	return driver.Event{Kind: kind, Window: target}
}

func setCompositionTestPreedit(target *uiTestTarget, text string, caret int) {
	state := compositionTestStateFor(target)
	state.text = text
	state.caret = caret
	state.active = true
}

func TestInputPreeditDoesNotChangeBinding(t *testing.T) {
	bound := "ab"
	input := Input(&bound)
	fixture := newCompositionTestFixture(t, input, 120, 36)
	if err := fixture.app.Post(func() {
		fixture.target.handler(keyEvent(driver.KeyTab, 0))
		fixture.target.handler(keyEvent(driver.KeyHome, 0))
		fixture.target.handler(keyEvent(driver.KeyRight, 0))
		setCompositionTestPreedit(fixture.target, "拼", 1)
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
	}); err != nil {
		t.Fatalf("Post preedit: %v", err)
	}
	fixture.barrier(t)

	if input.Value() != "ab" || bound != "ab" {
		t.Fatalf("preedit changed committed state: value=%q binding=%q", input.Value(), bound)
	}
	if got := len(input.buffer.history); got != 0 {
		t.Fatalf("preedit recorded %d history units, want 0", got)
	}
	painter := &fakePainter{}
	if err := fixture.app.Post(func() {
		input.draw(&drawContext{face: input.face, painter: painter})
	}); err != nil {
		t.Fatalf("Post paint probe: %v", err)
	}
	fixture.barrier(t)
	textFound := false
	underlineFound := false
	content := input.contentRect()
	for _, op := range painter.ops {
		if op.kind == "text" && op.text == "a拼b" {
			textFound = true
		}
		if op.kind == "rect" && op.color == defaultTheme.focusBorder && op.rect.Height == 1 && op.rect.Width > 1 && op.rect.Y >= content.Y+content.Height-2 {
			underlineFound = true
		}
	}
	if !textFound || !underlineFound {
		t.Fatalf("preedit paint: composed text=%v underline=%v; ops=%v", textFound, underlineFound, painter.ops)
	}
}

func TestCompositionEndInsertsOnce(t *testing.T) {
	input := Input(nil)
	fixture := newCompositionTestFixture(t, input, 120, 36)
	var startHandled bool
	if err := fixture.app.Post(func() {
		fixture.target.handler(keyEvent(driver.KeyTab, 0))
		setCompositionTestPreedit(fixture.target, "你", 1)
		startHandled, _ = fixture.window.router.Handle(compositionTestEvent(fixture.target, driver.EventCompositionStart))
	}); err != nil {
		t.Fatalf("Post composition start: %v", err)
	}
	fixture.barrier(t)
	if !startHandled {
		t.Fatal("CompositionStart was not handled by the focused Input")
	}

	if err := fixture.app.Post(func() {
		fixture.state.text = ""
		fixture.state.caret = 0
		fixture.state.active = false
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionEnd))
		fixture.target.handler(runeInputEvent('你'))
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionEnd))
	}); err != nil {
		t.Fatalf("Post composition commit: %v", err)
	}
	fixture.barrier(t)
	if got := input.Value(); got != "你" {
		t.Fatalf("Value after one commit = %q, want exactly 你", got)
	}
}

func TestIMESpotFollowsCaret(t *testing.T) {
	t.Run("Input horizontal scroll", func(t *testing.T) {
		input := Input(nil)
		input.SetValue("一段很长的账号文本🙂🙂🙂🙂🙂🙂🙂🙂")
		fixture := newCompositionTestFixture(t, input, 90, 36)
		if err := fixture.app.Post(func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
		}); err != nil {
			t.Fatalf("Post focus: %v", err)
		}
		fixture.barrier(t)
		if len(fixture.state.spots) == 0 {
			t.Fatal("focusing Input produced no SetSpot call")
		}
		content := input.contentRect()
		offset := inputTestCaretOffset(t, goregular.TTF, input.Value(), input.buffer.Cursor())
		want := [2]int{int(content.X + offset - input.scrollX), int(content.Y + content.Height)}
		if got := fixture.state.spots[len(fixture.state.spots)-1]; got != want {
			t.Fatalf("spot at scrolled caret = %v, want client coordinate %v", got, want)
		}

		if err := fixture.app.Post(func() {
			fixture.target.handler(keyEvent(driver.KeyHome, 0))
		}); err != nil {
			t.Fatalf("Post Home: %v", err)
		}
		fixture.barrier(t)
		want = [2]int{int(content.X), int(content.Y + content.Height)}
		if got := fixture.state.spots[len(fixture.state.spots)-1]; got != want {
			t.Fatalf("spot after Home = %v, want %v", got, want)
		}
	})

	t.Run("TextArea vertical and horizontal scroll", func(t *testing.T) {
		area := TextArea(nil)
		area.SetValue("short\n" + strings.Repeat("j", 20) + "\n三\n" + strings.Repeat("j", 20))
		fixture := newCompositionTestFixture(t, area, 76, 54)
		if err := fixture.app.Post(func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			fixture.target.handler(keyEvent(driver.KeyUp, 0))
		}); err != nil {
			t.Fatalf("Post caret movement: %v", err)
		}
		fixture.barrier(t)
		if len(fixture.state.spots) == 0 {
			t.Fatal("moving the TextArea caret produced no SetSpot call")
		}
		content := area.contentRect()
		line := area.buffer.LineIndex(area.buffer.Cursor())
		column := area.buffer.Column(area.buffer.Cursor())
		offset := inputTestCaretOffset(t, goregular.TTF, area.buffer.Lines()[line], column)
		want := [2]int{
			int(content.X + offset - area.scrollX),
			int(content.Y + (float64(line)-area.scrollY+1)*area.lineHeight()),
		}
		if got := fixture.state.spots[len(fixture.state.spots)-1]; got != want {
			t.Fatalf("spot after scrolling = %v, want latest client coordinate %v", got, want)
		}
	})
}

func TestInputDisableCancelsComposition(t *testing.T) {
	input := Input(nil)
	fixture := newCompositionTestFixture(t, input, 120, 36)
	if err := fixture.app.Post(func() {
		fixture.target.handler(keyEvent(driver.KeyTab, 0))
		setCompositionTestPreedit(fixture.target, "拼", 1)
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
		input.SetEnabled(false)
	}); err != nil {
		t.Fatalf("Post disable: %v", err)
	}
	fixture.barrier(t)
	if fixture.state.enabled || fixture.state.cancelCalls == 0 {
		t.Fatalf("disable left backend composition enabled=%v cancelCalls=%d", fixture.state.enabled, fixture.state.cancelCalls)
	}
	painter := &fakePainter{}
	if err := fixture.app.Post(func() {
		input.draw(&drawContext{face: input.face, painter: painter})
	}); err != nil {
		t.Fatalf("Post paint probe: %v", err)
	}
	fixture.barrier(t)
	for _, op := range painter.ops {
		if op.kind == "text" && strings.Contains(op.text, "拼") {
			t.Fatalf("disabled Input still paints preedit: %+v", op)
		}
	}
}

func TestTextAreaPreeditDoesNotChangeBinding(t *testing.T) {
	bound := "ab\ncd"
	area := TextArea(&bound)
	fixture := newCompositionTestFixture(t, area, 160, 80)
	if err := fixture.app.Post(func() {
		fixture.target.handler(keyEvent(driver.KeyTab, 0))
		fixture.target.handler(keyEvent(driver.KeyLeft, 0))
		setCompositionTestPreedit(fixture.target, "拼", 1)
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
	}); err != nil {
		t.Fatalf("Post TextArea preedit: %v", err)
	}
	fixture.barrier(t)
	if area.Value() != "ab\ncd" || bound != "ab\ncd" || len(area.buffer.history) != 0 {
		t.Fatalf("TextArea preedit changed committed state: value=%q binding=%q history=%d", area.Value(), bound, len(area.buffer.history))
	}
	painter := &fakePainter{}
	if err := fixture.app.Post(func() {
		area.draw(&drawContext{face: area.face, painter: painter})
	}); err != nil {
		t.Fatalf("Post paint probe: %v", err)
	}
	fixture.barrier(t)
	found := false
	for _, op := range painter.ops {
		if op.kind == "text" && op.text == "c拼d" {
			found = true
		}
	}
	if !found {
		t.Fatalf("TextArea did not paint preedit at its caret; ops=%v", painter.ops)
	}
}

func TestCompositionFocusAndHideCancellation(t *testing.T) {
	t.Run("focus switch", func(t *testing.T) {
		input := Input(nil)
		fixture := newCompositionTestFixture(t, Column(input, Button("done")), 200, 100)
		if err := fixture.app.Post(func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			setCompositionTestPreedit(fixture.target, "拼", 1)
			fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
		}); err != nil {
			t.Fatalf("Post focus switch: %v", err)
		}
		fixture.barrier(t)
		if fixture.state.enabled || fixture.state.cancelCalls != 1 {
			t.Fatalf("focus switch backend state: enabled=%v cancelCalls=%d", fixture.state.enabled, fixture.state.cancelCalls)
		}
	})

	t.Run("hide and show", func(t *testing.T) {
		input := Input(nil)
		fixture := newCompositionTestFixture(t, input, 120, 36)
		if err := fixture.app.Post(func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			setCompositionTestPreedit(fixture.target, "拼", 1)
			fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
		}); err != nil {
			t.Fatalf("Post preedit: %v", err)
		}
		fixture.barrier(t)
		fixture.window.Hide()
		fixture.barrier(t)
		if fixture.state.enabled || fixture.state.cancelCalls != 1 {
			t.Fatalf("Hide backend state: enabled=%v cancelCalls=%d", fixture.state.enabled, fixture.state.cancelCalls)
		}
		fixture.window.Show()
		fixture.barrier(t)
		if !fixture.state.enabled {
			t.Fatal("Show did not restore composition capability")
		}
	})
}

func TestAppEnableIME(t *testing.T) {
	t.Setenv("MELTEMI_IME", "unchanged")
	t.Setenv("XMODIFIERS", "unchanged")
	app := New()
	if os.Getenv("MELTEMI_IME") != "unchanged" || os.Getenv("XMODIFIERS") != "unchanged" {
		t.Fatal("New changed the process input-method environment without EnableIME")
	}
	if err := app.EnableIME(); err != nil {
		t.Fatalf("EnableIME before Run: %v", err)
	}
	if got := os.Getenv("MELTEMI_IME"); got != "1" {
		t.Fatalf("MELTEMI_IME = %q, want 1", got)
	}
	if got := os.Getenv("XMODIFIERS"); got != "@im=none" {
		t.Fatalf("XMODIFIERS = %q, want @im=none", got)
	}
}

func TestAppEnableIMERejectedAfterRunStarts(t *testing.T) {
	t.Setenv("MELTEMI_IME", "unchanged")
	t.Setenv("XMODIFIERS", "unchanged")
	backend := newUITestBackend()
	app, _ := newUITestApp(backend, &uiTestLog{})
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
	started := make(chan struct{})
	if err := app.Post(func() { close(started) }); err != nil {
		t.Fatalf("Post Run barrier: %v", err)
	}
	receiveUI(t, started, "Run start")
	if err := app.EnableIME(); err == nil {
		t.Fatal("EnableIME after Run started returned nil error")
	}
	if os.Getenv("MELTEMI_IME") != "unchanged" || os.Getenv("XMODIFIERS") != "unchanged" {
		t.Fatal("rejected EnableIME changed the process environment")
	}
	app.Quit()
	if err := waitUIReturn(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	finished = true
}

func TestDisablingUnfocusedEditorPreservesComposition(t *testing.T) {
	tests := []struct {
		name    string
		editors func() (compositionTarget, Component, func())
	}{
		{
			name: "Input",
			editors: func() (compositionTarget, Component, func()) {
				first, second := Input(nil), Input(nil)
				return first, second, func() { second.SetEnabled(false) }
			},
		},
		{
			name: "TextArea",
			editors: func() (compositionTarget, Component, func()) {
				first, second := TextArea(nil), TextArea(nil)
				return first, second, func() { second.SetEnabled(false) }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first, second, disableSecond := test.editors()
			fixture := newCompositionTestFixture(t, Column(first, second), 200, 100)
			painter := &fakePainter{}
			if err := fixture.app.Post(func() {
				fixture.target.handler(keyEvent(driver.KeyTab, 0))
				setCompositionTestPreedit(fixture.target, "拼", 1)
				fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
				disableSecond()
				first.draw(&drawContext{face: fixture.app.face, painter: painter})
			}); err != nil {
				t.Fatalf("Post unfocused disable: %v", err)
			}
			fixture.barrier(t)
			if !fixture.state.enabled || fixture.state.cancelCalls != 0 {
				t.Fatalf("unfocused disable cancelled active composition: enabled=%v cancelCalls=%d", fixture.state.enabled, fixture.state.cancelCalls)
			}
			found := false
			for _, op := range painter.ops {
				if op.kind == "text" && strings.Contains(op.text, "拼") {
					found = true
				}
			}
			if !found {
				t.Fatal("unfocused disable cleared the focused editor's preedit")
			}
		})
	}
}

func TestCompositionCloseCancellation(t *testing.T) {
	t.Run("CloseHide", func(t *testing.T) {
		input := Input(nil)
		fixture := newCompositionTestFixture(t, input, 120, 36)
		if err := fixture.app.Post(func() {
			fixture.window.CloseAction(CloseHide)
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			setCompositionTestPreedit(fixture.target, "拼", 1)
			fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
		}); err != nil {
			t.Fatalf("Post preedit: %v", err)
		}
		fixture.barrier(t)
		if err := fixture.window.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		fixture.barrier(t)
		if fixture.state.enabled || fixture.state.cancelCalls != 1 || input.composition.active {
			t.Fatalf("CloseHide state: enabled=%v cancelCalls=%d active=%v", fixture.state.enabled, fixture.state.cancelCalls, input.composition.active)
		}
	})

	t.Run("CloseDestroy", func(t *testing.T) {
		input := Input(nil)
		fixture := newCompositionTestFixture(t, input, 120, 36)
		if err := fixture.app.Post(func() {
			fixture.target.handler(keyEvent(driver.KeyTab, 0))
			setCompositionTestPreedit(fixture.target, "拼", 1)
			fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
		}); err != nil {
			t.Fatalf("Post preedit: %v", err)
		}
		fixture.barrier(t)
		if err := fixture.window.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		fixture.finish(t)
		if fixture.state.enabled || fixture.state.cancelCalls != 1 || input.composition.active {
			t.Fatalf("CloseDestroy state: enabled=%v cancelCalls=%d active=%v", fixture.state.enabled, fixture.state.cancelCalls, input.composition.active)
		}
	})
}

func TestCompositionScrollKeepsCaretAndSpotVisible(t *testing.T) {
	preedit := strings.Repeat("pin", 8)

	t.Run("Input", func(t *testing.T) {
		input := Input(nil)
		fixture := newCompositionTestFixture(t, input, 76, 36)
		moveCaret := func(kind driver.EventKind, caret int) {
			t.Helper()
			if err := fixture.app.Post(func() {
				if kind == driver.EventCompositionStart {
					fixture.target.handler(keyEvent(driver.KeyTab, 0))
					setCompositionTestPreedit(fixture.target, preedit, caret)
				} else {
					fixture.state.caret = caret
				}
				fixture.target.handler(compositionTestEvent(fixture.target, kind))
			}); err != nil {
				t.Fatalf("Post composition caret: %v", err)
			}
			fixture.barrier(t)
		}
		assertPosition := func(caret int, wantScroll float64) {
			t.Helper()
			content := input.contentRect()
			if input.scrollX != wantScroll {
				t.Errorf("caret %d scrollX = %v, want composed-metrics scroll %v", caret, input.scrollX, wantScroll)
			}
			caretOffset := inputTestCaretOffset(t, goregular.TTF, preedit, caret)
			wantSpot := [2]int{int(content.X + caretOffset - wantScroll), int(content.Y + content.Height)}
			if got := fixture.state.spots[len(fixture.state.spots)-1]; got != wantSpot {
				t.Errorf("caret %d spot = %v, want %v", caret, got, wantSpot)
			}
			caretX := input.caretX()
			if caretX < content.X || caretX > content.X+content.Width {
				t.Errorf("caret %d X = %v outside clipped content [%v, %v]", caret, caretX, content.X, content.X+content.Width)
			}
		}

		content := input.contentRect()
		totalWidth := inputTestCaretOffset(t, goregular.TTF, preedit, len([]rune(preedit)))
		endScroll := max(0, totalWidth-content.Width+1)
		moveCaret(driver.EventCompositionStart, len([]rune(preedit)))
		assertPosition(len([]rune(preedit)), endScroll)
		moveCaret(driver.EventCompositionUpdate, 0)
		assertPosition(0, 0)
		moveCaret(driver.EventCompositionUpdate, len([]rune(preedit)))
		assertPosition(len([]rune(preedit)), endScroll)
	})

	t.Run("TextArea", func(t *testing.T) {
		area := TextArea(nil)
		fixture := newCompositionTestFixture(t, area, 60, 54)
		moveCaret := func(kind driver.EventKind, caret int) {
			t.Helper()
			if err := fixture.app.Post(func() {
				if kind == driver.EventCompositionStart {
					fixture.target.handler(keyEvent(driver.KeyTab, 0))
					setCompositionTestPreedit(fixture.target, preedit, caret)
				} else {
					fixture.state.caret = caret
				}
				fixture.target.handler(compositionTestEvent(fixture.target, kind))
			}); err != nil {
				t.Fatalf("Post composition caret: %v", err)
			}
			fixture.barrier(t)
		}
		assertPosition := func(caret int, wantScroll float64) {
			t.Helper()
			content := area.contentRect()
			if area.scrollX != wantScroll {
				t.Errorf("caret %d scrollX = %v, want composed-metrics scroll %v", caret, area.scrollX, wantScroll)
			}
			caretOffset := inputTestCaretOffset(t, goregular.TTF, preedit, caret)
			caretRect := area.caretRect()
			wantSpot := [2]int{int(content.X + caretOffset - wantScroll), int(caretRect.Y + caretRect.Height)}
			if got := fixture.state.spots[len(fixture.state.spots)-1]; got != wantSpot {
				t.Errorf("caret %d spot = %v, want %v", caret, got, wantSpot)
			}
			if caretRect.X < content.X || caretRect.X > content.X+content.Width {
				t.Errorf("caret %d X = %v outside clipped content [%v, %v]", caret, caretRect.X, content.X, content.X+content.Width)
			}
		}

		content := area.contentRect()
		totalWidth := inputTestCaretOffset(t, goregular.TTF, preedit, len([]rune(preedit)))
		endScroll := max(0, totalWidth-content.Width+1)
		moveCaret(driver.EventCompositionStart, len([]rune(preedit)))
		assertPosition(len([]rune(preedit)), endScroll)
		moveCaret(driver.EventCompositionUpdate, 0)
		assertPosition(0, 0)
		moveCaret(driver.EventCompositionUpdate, len([]rune(preedit)))
		assertPosition(len([]rune(preedit)), endScroll)
	})
}

func TestCompositionNormalizationPreservesRuneCaret(t *testing.T) {
	state := newCompositionState("a\r\nb", 3, true)
	if state.text != "a  b" {
		t.Fatalf("normalized preedit = %q, want %q with CR and LF replaced independently", state.text, "a  b")
	}
	if got := len([]rune(state.text)); got != 4 {
		t.Fatalf("normalized preedit rune count = %d, want 4", got)
	}
	if state.caret != 3 {
		t.Fatalf("normalized preedit caret = %d, want original rune index 3", state.caret)
	}
}

func TestContentReplacementResetsReusedInputFocus(t *testing.T) {
	input := Input(nil)
	fixture := newCompositionTestFixture(t, input, 120, 36)
	if err := fixture.app.Post(func() {
		fixture.target.handler(keyEvent(driver.KeyTab, 0))
	}); err != nil {
		t.Fatalf("Post initial focus: %v", err)
	}
	fixture.barrier(t)

	fixture.window.Content(Label("replacement"))
	fixture.barrier(t)
	if input.focused {
		t.Error("replaced Input retained focused state")
	}
	if input.compositionFocusHandler != nil {
		t.Error("replaced Input retained its window composition callback")
	}

	fixture.window.Content(input)
	fixture.barrier(t)
	if err := fixture.app.Post(func() {
		fixture.target.handler(keyEvent(driver.KeyTab, 0))
	}); err != nil {
		t.Fatalf("Post reused Input focus: %v", err)
	}
	fixture.barrier(t)
	if fixture.window.router.focus != input || !input.focused {
		t.Fatalf("reused Input focus state: router=%p input=%p focused=%v", fixture.window.router.focus, input, input.focused)
	}
	if !fixture.state.enabled {
		t.Fatal("reusing a previously focused Input did not re-enable backend composition")
	}
}

func TestDetachedEditorDisableDoesNotCancelReplacement(t *testing.T) {
	oldArea := TextArea(nil)
	fixture := newCompositionTestFixture(t, oldArea, 160, 80)
	if err := fixture.app.Post(func() {
		fixture.target.handler(keyEvent(driver.KeyTab, 0))
		setCompositionTestPreedit(fixture.target, "旧", 1)
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
	}); err != nil {
		t.Fatalf("Post old preedit: %v", err)
	}
	fixture.barrier(t)

	newArea := TextArea(nil)
	fixture.window.Content(newArea)
	fixture.barrier(t)
	if oldArea.focused {
		t.Error("detached TextArea retained focused state")
	}
	if oldArea.compositionFocusHandler != nil {
		t.Error("detached TextArea retained its window composition callback")
	}
	if err := fixture.app.Post(func() {
		fixture.target.handler(keyEvent(driver.KeyTab, 0))
		setCompositionTestPreedit(fixture.target, "新", 1)
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
		oldArea.SetEnabled(false)
	}); err != nil {
		t.Fatalf("Post detached disable: %v", err)
	}
	fixture.barrier(t)
	if !fixture.state.enabled || fixture.state.cancelCalls != 1 {
		t.Fatalf("detached disable affected current backend: enabled=%v cancelCalls=%d", fixture.state.enabled, fixture.state.cancelCalls)
	}
	if !newArea.composition.active || newArea.composition.text != "新" {
		t.Fatalf("detached disable cleared replacement preedit: %+v", newArea.composition)
	}
}

func TestComplexCompositionAndPassword(t *testing.T) {
	t.Run("preedit underline and caret use the composed layout", func(t *testing.T) {
		input := Input(nil)
		input.SetValue("ab")
		setTreeFace(input, testFace(t))
		input.arrange(rect(0, 0, 240, 36))
		input.focused = true

		// Preedit "e\u0301" commits at the caret (end of "ab"): the composed
		// display is "abe\u0301" with the preedit occupying runes [2, 4).
		input.setComposition("e\u0301", 2, true)
		if got := input.composition.caret; got != 2 {
			t.Fatalf("composition caret = %d, want 2 (boundary, unchanged)", got)
		}

		composed, start, end, caret, active := input.composedDisplay()
		if composed != "abe\u0301" || start != 2 || end != 4 || caret != 4 || !active {
			t.Fatalf("composedDisplay() = (%q, %d, %d, %d, %v), want (%q, 2, 4, 4, true)", composed, start, end, caret, active, "abe\u0301")
		}

		reference := inputTestLayout(t, goregular.TTF, composed)
		offsets := layoutOffsets(reference)
		content := input.contentRect()

		painter := &fakePainter{}
		input.draw(&drawContext{face: input.face, painter: painter})

		wantUnderline := render.Rect{
			X:      content.X + offsets[start],
			Y:      content.Y + content.Height - 2,
			Width:  offsets[end] - offsets[start],
			Height: 1,
		}
		if wantUnderline.Width <= 0 {
			t.Fatalf("test is not discriminating: preedit underline width %v", wantUnderline.Width)
		}
		wantCaret := render.Rect{X: content.X + offsets[caret], Y: content.Y, Width: 1, Height: content.Height}
		underlineFound, caretFound, textFound := false, false, false
		for _, op := range painter.ops {
			switch op.kind {
			case "text":
				if op.text == composed {
					textFound = true
				}
			case "rect":
				if op.rect == wantUnderline && op.color == defaultTheme.focusBorder {
					underlineFound = true
				}
				if op.rect == wantCaret && op.color == inputCaretColor {
					caretFound = true
				}
			}
		}
		if !textFound {
			t.Error("composed preedit text was not painted")
		}
		if !underlineFound {
			t.Errorf("no preedit underline at %+v (composed-layout offsets)", wantUnderline)
		}
		if !caretFound {
			t.Errorf("no caret at composed-layout position %+v", wantCaret)
		}
	})

	t.Run("preedit caret snaps out of cluster interiors", func(t *testing.T) {
		input := Input(nil)
		input.SetValue("ab")
		setTreeFace(input, testFace(t))
		input.arrange(rect(0, 0, 240, 36))
		input.focused = true

		// Caret 1 sits between e and its combining mark: grapheme snapping
		// parks it on the nearer boundary (tie breaks toward the cluster
		// start), so the preedit caret can never split the cluster.
		input.setComposition("e\u0301", 1, true)
		if got := input.composition.caret; got != 0 {
			t.Errorf("composition caret after snap = %d, want 0 (tie breaks toward cluster start)", got)
		}
		offsets, snapped := input.caretMetrics()
		reference := inputTestLayout(t, goregular.TTF, "abe\u0301")
		wantOffsets := layoutOffsets(reference)
		for index := range wantOffsets {
			if offsets[index] != wantOffsets[index] {
				t.Fatalf("caret offsets[%d] = %v, want composed-layout %v", index, offsets[index], wantOffsets[index])
			}
		}
		// The preedit caret 0 sits at the start of the preedit cluster in
		// the composed text: composed caret = committed 2 + preedit 0.
		if want := wantOffsets[2]; offsets[snapped] != want {
			t.Errorf("snapped caret offset = %v, want composed boundary 2 at %v", offsets[snapped], want)
		}
	})

	t.Run("commit inserts the cluster and undo restores", func(t *testing.T) {
		bound := "ab"
		input := Input(&bound)
		setTreeFace(input, testFace(t))
		input.arrange(rect(0, 0, 240, 36))
		input.focused = true

		input.setComposition("\u597D", 1, true)
		input.setComposition("", 0, false)
		input.handleEvent(driver.Event{Kind: driver.EventRune, Rune: '\u597D'})
		if got := input.Value(); got != "ab\u597D" {
			t.Fatalf("Value() after commit = %q, want %q", got, "ab\u597D")
		}
		if got := bound; got != "ab\u597D" {
			t.Fatalf("binding after commit = %q, want %q", got, "ab\u597D")
		}
		if !input.Undo() {
			t.Fatal("Undo() after commit = false, want true")
		}
		if got := input.Value(); got != "ab" || bound != "ab" {
			t.Errorf("after Undo: value/binding = %q/%q, want %q/%q", input.Value(), bound, "ab", "ab")
		}
	})

	t.Run("password masks every rune and still edits whole graphemes", func(t *testing.T) {
		password := Password(nil)
		password.SetValue("e\u0301中\U0001F44D\U0001F3FD")
		setTreeFace(password, testFace(t))
		password.arrange(rect(0, 0, 240, 36))
		password.focused = true

		if got, want := password.Value(), "e\u0301中\U0001F44D\U0001F3FD"; got != want {
			t.Fatalf("Value() = %q, want the real text %q", got, want)
		}
		if got, want := password.displayValue(), "*****"; got != want {
			t.Fatalf("displayValue() = %q, want %q (one mask rune per rune)", got, want)
		}

		painter := &fakePainter{}
		password.draw(&drawContext{face: password.face, painter: painter})
		maskedFound := false
		for _, op := range painter.ops {
			if op.kind == "text" && op.text == "e\u0301中\U0001F44D\U0001F3FD" {
				t.Error("password painted the real text")
			}
			if op.kind == "text" && op.text == "*****" {
				maskedFound = true
			}
		}
		if !maskedFound {
			t.Error("password did not paint the masked text")
		}

		// Backspace removes the whole trailing grapheme (two runes) even
		// though the mask shows two glyphs: the editing model stays
		// grapheme-safe underneath the mask.
		password.handleEvent(driver.Event{Kind: driver.EventKey, Key: driver.KeyBackspace, Action: driver.ActionPress})
		if got, want := password.Value(), "e\u0301中"; got != want {
			t.Errorf("Value() after Backspace = %q, want %q (emoji+modifier removed whole)", got, want)
		}
		if got, want := password.displayValue(), "***"; got != want {
			t.Errorf("displayValue() after Backspace = %q, want %q", got, want)
		}
		password.handleEvent(driver.Event{Kind: driver.EventKey, Key: driver.KeyLeft, Action: driver.ActionPress})
		if got := password.buffer.Cursor(); got != 2 {
			t.Errorf("Cursor() after Left = %d, want 2 (mark joins 中 into one caret step... boundary after 中)", got)
		}
	})
}

// TestComplexCompositionRTLUnderlines pins the RTL preedit underline
// geometry for both editors: the span uses the same min/absolute geometry
// as selections, because caret offsets decrease through RTL runs.
func TestComplexCompositionRTLUnderlines(t *testing.T) {
	t.Run("input rtl preedit underline spans visual extents", func(t *testing.T) {
		input := Input(nil)
		input.SetValue("abc ")
		setTreeFace(input, testFace(t))
		input.arrange(rect(0, 0, 240, 36))
		input.focused = true

		// Preedit "مرح" commits at the start of the RTL run: the composed
		// display is "abc مرح" with the preedit at runes [4, 7), whose
		// caret offsets decrease through the RTL run.
		input.setComposition("\u0645\u0631\u062D", 3, true)
		composed, start, end, _, active := input.composedDisplay()
		if !active || composed != "abc \u0645\u0631\u062D" || start != 4 || end != 7 {
			t.Fatalf("composedDisplay() = (%q, %d, %d, %v), want (%q, 4, 7, true)", composed, start, end, active, "abc \u0645\u0631\u062D")
		}

		reference := inputTestLayout(t, goregular.TTF, composed)
		offsets := layoutOffsets(reference)
		if offsets[start] == offsets[end] {
			t.Skipf("environment: composed RTL span offsets[%d] == offsets[%d] == %v", start, end, offsets[start])
		}
		content := input.contentRect()
		wantUnderline := render.Rect{
			X:      content.X + math.Min(offsets[start], offsets[end]),
			Y:      content.Y + content.Height - 2,
			Width:  math.Abs(offsets[end] - offsets[start]),
			Height: 1,
		}
		if wantUnderline.Width <= 0 {
			t.Fatalf("test is not discriminating: RTL underline width %v", wantUnderline.Width)
		}

		painter := &fakePainter{}
		input.draw(&drawContext{face: input.face, painter: painter})
		found := false
		for _, op := range painter.ops {
			if op.kind == "rect" && op.rect == wantUnderline && op.color == defaultTheme.focusBorder {
				found = true
			}
		}
		if !found {
			t.Errorf("no RTL preedit underline at min/absolute geometry %+v (offsets[%d]=%v, offsets[%d]=%v); ops=%v",
				wantUnderline, start, offsets[start], end, offsets[end], painter.ops)
		}
	})

	t.Run("textarea rtl preedit underline spans visual extents", func(t *testing.T) {
		area := TextArea(nil)
		area.SetValue("xyz")
		setTreeFace(area, testFace(t))
		area.arrange(rect(0, 0, 240, 80))
		area.focused = true
		area.SetSelection(3, 3)

		area.setComposition("\u0645\u0631\u062D", 3, true)
		composed, start, end, _, active := area.composedLine(0)
		if !active || composed != "xyz\u0645\u0631\u062D" || start != 3 || end != 6 {
			t.Fatalf("composedLine(0) = (%q, %d, %d, %v), want (%q, 3, 6, true)", composed, start, end, active, "xyz\u0645\u0631\u062D")
		}

		reference := inputTestLayout(t, goregular.TTF, composed)
		offsets := layoutOffsets(reference)
		if offsets[start] == offsets[end] {
			t.Skipf("environment: composed RTL span offsets[%d] == offsets[%d] == %v", start, end, offsets[start])
		}
		content := area.contentRect()
		lineHeight := area.lineHeight()
		wantUnderline := render.Rect{
			X:      content.X + math.Min(offsets[start], offsets[end]),
			Y:      content.Y + lineHeight - 2,
			Width:  math.Abs(offsets[end] - offsets[start]),
			Height: 1,
		}
		if wantUnderline.Width <= 0 {
			t.Fatalf("test is not discriminating: RTL underline width %v", wantUnderline.Width)
		}

		painter := &fakePainter{}
		area.draw(&drawContext{face: area.face, painter: painter})
		found := false
		for _, op := range painter.ops {
			if op.kind == "rect" && op.rect == wantUnderline && op.color == defaultTheme.focusBorder {
				found = true
			}
		}
		if !found {
			t.Errorf("no RTL preedit underline at min/absolute geometry %+v (offsets[%d]=%v, offsets[%d]=%v); ops=%v",
				wantUnderline, start, offsets[start], end, offsets[end], painter.ops)
		}
	})
}
