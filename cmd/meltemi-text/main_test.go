package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/ui"
)

// fakeSmokeRuntime is an event loop the tests fully control: Post queues,
// Quit counts, and Run runs an injected script. It never needs a display.
type fakeSmokeRuntime struct {
	posted    []func()
	postErr   error
	quitCalls int
	run       func() error
}

func (f *fakeSmokeRuntime) Post(fn func()) error {
	if f.postErr != nil {
		return f.postErr
	}
	f.posted = append(f.posted, fn)
	return nil
}

func (f *fakeSmokeRuntime) Quit() {
	f.quitCalls++
}

func (f *fakeSmokeRuntime) Run() error {
	if f.run != nil {
		return f.run()
	}
	return nil
}

type fakeSmokeTimer struct {
	fired   func()
	stopped bool
}

func (f *fakeSmokeTimer) Stop() bool {
	f.stopped = true
	return true
}

type fakeSmokeScheduler struct {
	timers []*fakeSmokeTimer
}

func (f *fakeSmokeScheduler) after(_ time.Duration, fn func()) smokeTimer {
	timer := &fakeSmokeTimer{fired: fn}
	f.timers = append(f.timers, timer)
	return timer
}

// newFakeSmokeDemo builds the demo on the fake runtime without an app or
// window, but with the real widgets, so the smoke script itself runs
// headless through the public ui API.
func newFakeSmokeDemo(t *testing.T) (*demo, *fakeSmokeRuntime, *fakeSmokeScheduler) {
	t.Helper()
	runtime := &fakeSmokeRuntime{}
	scheduler := &fakeSmokeScheduler{}
	d := &demo{smokeApp: runtime, afterFunc: scheduler.after}
	d.buildWidgets()
	return d, runtime, scheduler
}

// TestSmokeScriptRunsHeadless drives the whole timer-posted smoke script
// through the fake runtime: every step posts the next one, and firing the
// scheduled timers in order (skipping the watchdog, which runSmoke stops
// once Run is back) executes the full cluster-safe edit, undo and password
// walk without a display.
func TestSmokeScriptRunsHeadless(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo(t)
	d.startSmoke()
	if len(scheduler.timers) != 2 {
		t.Fatalf("timers after startSmoke = %d, want 2 (watchdog + first step)", len(scheduler.timers))
	}
	// Fire every timer but the watchdog, draining posted steps in between:
	// each step schedules the next timer, so the loop walks the chain.
	for index := 1; index < len(scheduler.timers); index++ {
		scheduler.timers[index].fired()
		for len(runtime.posted) > 0 {
			step := runtime.posted[0]
			runtime.posted = runtime.posted[1:]
			step()
			if err := d.firstErr(); err != nil {
				t.Fatalf("smoke step failed: %v", err)
			}
		}
	}
	if !d.smokeStepsComplete {
		t.Error("smokeStepsComplete = false after the full chain, want true")
	}
	if err := d.firstErr(); err != nil {
		t.Errorf("firstErr() = %v, want nil", err)
	}
}

// TestSmokeScriptAssertsRealEditing replays the script's editing semantics
// directly (no timers) and pins every intermediate state the script checks,
// so a regression names the exact public-API behavior that broke.
func TestSmokeScriptAssertsRealEditing(t *testing.T) {
	d, _, _ := newFakeSmokeDemo(t)

	d.area.SetValue(smokeText1)
	if got := d.areaText; got != smokeText1 {
		t.Fatalf("binding after SetValue = %q, want %q", got, smokeText1)
	}

	// Cluster-safe selection: [12, 14) is exactly the "e + combining mark"
	// cluster on the Arabic line.
	d.area.SetSelection(12, 14)
	if start, end, ok := d.area.Selection(); start != 12 || end != 14 || !ok {
		t.Fatalf("Selection() = (%d, %d, %v), want (12, 14, true)", start, end, ok)
	}
	d.area.Backspace()
	if got, want := d.area.Value(), smokeTextDeleted; got != want {
		t.Fatalf("Value() after cluster Backspace = %q, want %q", got, want)
	}
	if start, end, ok := d.area.Selection(); start != 12 || end != 12 || ok {
		t.Fatalf("Selection() after cluster Backspace = (%d, %d, %v), want collapsed (12, 12, false)", start, end, ok)
	}

	d.area.Undo()
	if got := d.area.Value(); got != smokeText1 {
		t.Errorf("Value() after Undo = %q, want %q", got, smokeText1)
	}
	if start, end, ok := d.area.Selection(); start != 12 || end != 14 || !ok {
		t.Errorf("Selection() after Undo = (%d, %d, %v), want restored (12, 14, true)", start, end, ok)
	}
	if got := d.area.Cursor(); got != 14 {
		t.Errorf("Cursor() after Undo = %d, want 14", got)
	}
	d.area.Redo()
	if got, want := d.area.Value(), smokeTextDeleted; got != want {
		t.Errorf("Value() after Redo = %q, want %q", got, want)
	}

	d.input.SetValue(smokeInputValue)
	if got := d.input.Value(); got != smokeInputValue {
		t.Fatalf("Input.Value() = %q, want %q", got, smokeInputValue)
	}
	if !d.input.Undo() || d.input.Value() != "" {
		t.Errorf("Input after Undo = %q, want empty", d.input.Value())
	}
	if !d.input.Redo() || d.input.Value() != smokeInputValue {
		t.Errorf("Input after Redo = %q, want %q", d.input.Value(), smokeInputValue)
	}

	d.password.SetValue(smokePasswordValue)
	if got := d.password.Value(); got != smokePasswordValue {
		t.Errorf("Password.Value() = %q, want the real text %q", got, smokePasswordValue)
	}
	if !d.password.Undo() || d.password.Value() != "" {
		t.Errorf("Password after Undo = %q, want empty", d.password.Value())
	}
}

func TestSmokeWatchdogCatchesHang(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo(t)
	d.smokeStepsComplete = true
	runtime.run = func() error {
		scheduler.timers[0].fired()
		if len(runtime.posted) != 1 {
			t.Fatalf("watchdog Post count = %d, want 1", len(runtime.posted))
		}
		runtime.posted[0]()
		if runtime.quitCalls != 1 {
			t.Errorf("Quit calls = %d, want 1", runtime.quitCalls)
		}
		return nil
	}
	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke() = %v, want nil", err)
	}
	if d.firstErr() == nil {
		t.Error("firstErr() = nil, want the watchdog hang error recorded")
	}
}

func TestSmokeRunReturnedBeforeStepsComplete(t *testing.T) {
	d, runtime, _ := newFakeSmokeDemo(t)
	runtime.run = func() error { return nil }
	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke() = %v, want nil", err)
	}
	if err := d.firstErr(); err == nil || !strings.Contains(err.Error(), "before smoke steps completed") {
		t.Errorf("firstErr() = %v, want premature-return error", err)
	}
}

func TestSmokePostFailureEndsRun(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo(t)
	postErr := errors.New("loop gone")
	runtime.postErr = postErr
	d.startSmoke()
	scheduler.timers[1].fired()
	if d.firstErr() == nil {
		t.Error("firstErr() = nil, want the Post failure recorded")
	}
	if runtime.quitCalls == 0 {
		t.Error("Quit calls = 0, want 1 after the Post failure")
	}
}

// TestDemoWidgetsBuild proves the demo widgets compose from the public ui
// pieces and are created without an app.
func TestDemoWidgetsBuild(t *testing.T) {
	d, _, _ := newFakeSmokeDemo(t)
	if d.input == nil || d.password == nil || d.area == nil || d.status == nil {
		t.Fatal("demo widgets are not all built")
	}
	if d.password == d.input {
		t.Error("password and input share a widget")
	}
	var _ *ui.LabelWidget = d.status
}
