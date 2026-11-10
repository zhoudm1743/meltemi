package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeTraySmokeRuntime struct {
	posted    []func()
	quitCalls int
	run       func() error
}

func (f *fakeTraySmokeRuntime) Post(fn func()) error {
	f.posted = append(f.posted, fn)
	return nil
}

func (f *fakeTraySmokeRuntime) Quit() { f.quitCalls++ }
func (f *fakeTraySmokeRuntime) Run() error {
	if f.run != nil {
		return f.run()
	}
	return nil
}

func (f *fakeTraySmokeRuntime) drain() {
	for len(f.posted) != 0 {
		fn := f.posted[0]
		f.posted = f.posted[1:]
		fn()
	}
}

type fakeTraySmokeTimer struct {
	fired   func()
	stopped bool
}

func (f *fakeTraySmokeTimer) Stop() bool {
	f.stopped = true
	return true
}

type fakeTraySmokeScheduler struct{ timers []*fakeTraySmokeTimer }

func (f *fakeTraySmokeScheduler) after(_ time.Duration, fn func()) smokeTimer {
	timer := &fakeTraySmokeTimer{fired: fn}
	f.timers = append(f.timers, timer)
	return timer
}

type fakeTrayDemoWindow struct {
	showCalls  int
	closeCalls int
	closeErr   error
}

func (w *fakeTrayDemoWindow) Show() { w.showCalls++ }
func (w *fakeTrayDemoWindow) Close() error {
	w.closeCalls++
	return w.closeErr
}

func newControlledTrayDemo() (*demo, *fakeTraySmokeRuntime, *fakeTraySmokeScheduler, *fakeTrayDemoWindow) {
	runtime := &fakeTraySmokeRuntime{}
	scheduler := &fakeTraySmokeScheduler{}
	window := &fakeTrayDemoWindow{}
	d := newDemo(nil)
	d.smokeApp = runtime
	d.afterFunc = scheduler.after
	d.window = window
	d.initModels()
	d.activateRestore = func() error {
		d.menu.Snapshot().Items[0].Activate()
		return nil
	}
	return d, runtime, scheduler, window
}

func fireTraySmokeStep(t *testing.T, runtime *fakeTraySmokeRuntime, scheduler *fakeTraySmokeScheduler, index int, wantTimers int) {
	t.Helper()
	if len(scheduler.timers) != wantTimers {
		t.Fatalf("timers before step %d = %d, want %d", index, len(scheduler.timers), wantTimers)
	}
	scheduler.timers[index].fired()
	runtime.drain()
}

func TestTraySmokeCloseHideRestoreAndQuit(t *testing.T) {
	d, runtime, scheduler, window := newControlledTrayDemo()
	runtime.run = func() error {
		fireTraySmokeStep(t, runtime, scheduler, 1, 2)
		fireTraySmokeStep(t, runtime, scheduler, 2, 3)
		fireTraySmokeStep(t, runtime, scheduler, 3, 4)
		return nil
	}

	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke: %v", err)
	}
	if !d.smokeStepsComplete || runtime.quitCalls != 1 {
		t.Errorf("completion/Quit = %v/%d, want true/1", d.smokeStepsComplete, runtime.quitCalls)
	}
	if window.closeCalls != 1 || window.showCalls != 1 || d.restoreCalls != 1 {
		t.Errorf("Close/Show/restore callbacks = %d/%d/%d, want 1/1/1", window.closeCalls, window.showCalls, d.restoreCalls)
	}
	if !d.checked.Checked() || d.disabled.Enabled() {
		t.Error("smoke did not exercise checked and enabled model updates")
	}
	if !scheduler.timers[0].stopped {
		t.Error("watchdog was not stopped after Run returned")
	}
}

func TestTraySmokeReportsNativeActivationFailure(t *testing.T) {
	d, runtime, scheduler, _ := newControlledTrayDemo()
	d.activateRestore = func() error { return errors.New("StatusNotifierWatcher unavailable") }
	runtime.run = func() error {
		fireTraySmokeStep(t, runtime, scheduler, 1, 2)
		fireTraySmokeStep(t, runtime, scheduler, 2, 3)
		return nil
	}

	err := d.runSmoke()
	if err == nil || !strings.Contains(err.Error(), "StatusNotifierWatcher unavailable") {
		t.Errorf("runSmoke error = %v, want the native activation failure", err)
	}
	if d.smokeStepsComplete {
		t.Error("failed native activation was reported as a successful smoke")
	}
	if runtime.quitCalls != 1 {
		t.Errorf("Quit calls = %d, want 1 after failure", runtime.quitCalls)
	}
}

func TestTraySmokeWatchdogBoundsAStalledRun(t *testing.T) {
	d, runtime, scheduler, _ := newControlledTrayDemo()
	var processFailures []error
	d.smokeFailure = func(err error) { processFailures = append(processFailures, err) }
	runtime.run = func() error {
		scheduler.timers[0].fired()
		runtime.drain()
		return nil
	}

	if err := d.runSmoke(); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("runSmoke error = %v, want deadline exceeded", err)
	}
	if len(processFailures) != 1 || runtime.quitCalls != 0 || !scheduler.timers[0].stopped {
		t.Errorf("watchdog cleanup: failures=%d Quit=%d stopped=%v, want 1/0/true", len(processFailures), runtime.quitCalls, scheduler.timers[0].stopped)
	}
}

func TestTraySmokeWatchdogFailsWithoutDrainingPostedWork(t *testing.T) {
	d, runtime, scheduler, _ := newControlledTrayDemo()
	var processFailures []error
	d.smokeFailure = func(err error) { processFailures = append(processFailures, err) }
	runtime.run = func() error {
		scheduler.timers[1].fired()
		scheduler.timers[0].fired()
		return nil
	}

	err := d.runSmoke()
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("runSmoke error = %v, want deadline exceeded with an undrained UI queue", err)
	}
	if len(runtime.posted) != 1 {
		t.Errorf("posted callbacks = %d, want the original step to remain undrained", len(runtime.posted))
	}
	if len(processFailures) != 1 || !strings.Contains(processFailures[0].Error(), "deadline exceeded") {
		t.Errorf("process failures = %v, want one deadline failure", processFailures)
	}
	if runtime.quitCalls != 0 {
		t.Errorf("Quit calls = %d, want 0 because posted UI cleanup cannot bound a stalled queue", runtime.quitCalls)
	}
}

func TestTraySmokeRejectsRunReturningBeforeSteps(t *testing.T) {
	d, runtime, scheduler, _ := newControlledTrayDemo()
	if err := d.runSmoke(); err == nil || !strings.Contains(err.Error(), "before smoke steps completed") {
		t.Errorf("runSmoke error = %v, want premature-return failure", err)
	}
	if runtime.quitCalls != 0 {
		t.Errorf("Quit calls = %d after a plain early return, want 0", runtime.quitCalls)
	}
	if !scheduler.timers[0].stopped {
		t.Error("watchdog was not stopped after the early return")
	}
}
