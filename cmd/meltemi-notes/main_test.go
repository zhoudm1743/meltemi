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

func newFakeSmokeDemo() (*demo, *fakeSmokeRuntime, *fakeSmokeScheduler) {
	runtime := &fakeSmokeRuntime{}
	scheduler := &fakeSmokeScheduler{}
	return &demo{smokeApp: runtime, afterFunc: scheduler.after}, runtime, scheduler
}

// TestSmokeWatchdogCatchesLastWindowDestroyedWithoutQuit is the regression
// test for the fault where the last window is already destroyed but the
// last-window-quit policy failed to fire: the window list is empty, Run waits
// forever, and the watchdog must still report the hang and quit. The old
// window-list-based check returned silently in exactly this state.
func TestSmokeWatchdogCatchesLastWindowDestroyedWithoutQuit(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo()
	d.smokeStepsComplete = true // every step finished and the window is gone
	runtime.run = func() error {
		// The deadline passes with Run still blocked: no window remains,
		// yet nothing will ever wake the loop again.
		scheduler.timers[0].fired()
		if len(runtime.posted) != 1 {
			t.Fatalf("watchdog Post count = %d, want 1", len(runtime.posted))
		}
		runtime.posted[0]()
		return nil
	}

	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke error = %v, want nil", err)
	}
	if err := d.firstErr(); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("firstErr = %v, want deadline exceeded", err)
	}
	if runtime.quitCalls != 1 {
		t.Errorf("watchdog Quit calls = %d, want 1", runtime.quitCalls)
	}
	if !scheduler.timers[0].stopped {
		t.Error("runSmoke did not stop the watchdog after Run returned")
	}
}

// TestSmokeWatchdogSilentOnceRunReturned covers the clean-return interleaving:
// after Run is back the watchdog timer is stopped, and even a callback that
// was already in flight stays silent instead of reporting a spurious timeout
// or quitting a finished app. The steps are marked complete, so the early
// return guard in runSmoke has nothing to report either.
func TestSmokeWatchdogSilentOnceRunReturned(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo()
	d.smokeStepsComplete = true // every step finished before Run returned
	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke error = %v, want nil", err)
	}
	if !scheduler.timers[0].stopped {
		t.Fatal("runSmoke did not stop the watchdog timer after Run returned")
	}

	// The flag alone silences a callback that fires before Stop took effect.
	scheduler.timers[0].fired()
	if len(runtime.posted) != 0 {
		t.Fatalf("post-Run watchdog queued %d callbacks, want 0", len(runtime.posted))
	}
	if err := d.firstErr(); err != nil {
		t.Fatalf("post-Run watchdog error = %v, want nil", err)
	}
	if runtime.quitCalls != 0 {
		t.Errorf("post-Run watchdog Quit calls = %d, want 0", runtime.quitCalls)
	}

	// A callback racing with Stop may still attempt its Post; a rejected Post
	// means the loop is already gone, which is normal shutdown.
	runtime.postErr = errors.New("ui: application closed")
	scheduler.timers[0].fired()
	if len(runtime.posted) != 0 {
		t.Fatalf("rejected watchdog queued %d callbacks, want 0", len(runtime.posted))
	}
	if err := d.firstErr(); err != nil {
		t.Fatalf("rejected-post watchdog error = %v, want nil", err)
	}
	if runtime.quitCalls != 0 {
		t.Errorf("rejected-post watchdog Quit calls = %d, want 0", runtime.quitCalls)
	}
}

// TestSmokeRunReturnedBeforeStepsComplete is the regression test for an
// early nil return from Run: no step failed and the script never finished,
// yet the run must not report success. runSmoke records the premature return
// as the first error — the nil run error alone would have let main exit 0.
func TestSmokeRunReturnedBeforeStepsComplete(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo()
	// Run returns nil before any step: the user closed the window early or
	// the loop drained on its own, and the queued first step never runs.
	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke error = %v, want nil", err)
	}
	if err := d.firstErr(); err == nil || !strings.Contains(err.Error(), "Run returned before smoke steps completed") {
		t.Errorf("firstErr = %v, want Run returned before smoke steps completed", err)
	}
	if !scheduler.timers[0].stopped {
		t.Error("runSmoke did not stop the watchdog after Run returned")
	}
	if runtime.quitCalls != 0 {
		t.Errorf("Quit calls = %d, want 0", runtime.quitCalls)
	}
}

// TestSmokeWatchdogStillReportsRealTimeout covers the plain hang: steps never
// complete and Run is still blocked when the deadline passes.
func TestSmokeWatchdogStillReportsRealTimeout(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo()
	// smokeStepsComplete stays false: the script is stuck. runSmoke arms the
	// watchdog before Run, so timers[0] exists once the fake loop starts.
	runtime.run = func() error {
		scheduler.timers[0].fired()
		if len(runtime.posted) != 1 {
			t.Fatalf("watchdog Post count = %d, want 1", len(runtime.posted))
		}
		runtime.posted[0]()
		return nil
	}

	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke error = %v, want nil", err)
	}
	if err := d.firstErr(); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("firstErr = %v, want deadline exceeded", err)
	}
	if runtime.quitCalls != 1 {
		t.Errorf("watchdog Quit calls = %d, want 1", runtime.quitCalls)
	}
	if !scheduler.timers[0].stopped {
		t.Error("runSmoke did not stop the watchdog after Run returned")
	}
}

// TestReportErrorPinsStatusLine pins the OnError wiring the demo installs on
// its editor: a clipboard failure shows the error action together with the
// message and the live counts of the untouched note.
func TestReportErrorPinsStatusLine(t *testing.T) {
	d := &demo{status: ui.Label("")}
	d.notes = "多行\n笔记"
	d.reportError(errors.New("clipboard busy"))
	want := "剪贴板错误 · clipboard busy · 共 2 行 · 5 字"
	if got := d.status.Text(); got != want {
		t.Fatalf("status after reportError = %q, want %q", got, want)
	}
}
