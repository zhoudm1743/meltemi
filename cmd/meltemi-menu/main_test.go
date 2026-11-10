package main

import (
	"strings"
	"testing"
	"time"
)

type fakeSmokeRuntime struct {
	posted    []func()
	quitCalls int
	run       func() error
}

func (f *fakeSmokeRuntime) Post(fn func()) error {
	f.posted = append(f.posted, fn)
	return nil
}

func (f *fakeSmokeRuntime) Quit() { f.quitCalls++ }
func (f *fakeSmokeRuntime) Run() error {
	if f.run != nil {
		return f.run()
	}
	return nil
}

func (f *fakeSmokeRuntime) drain() {
	for len(f.posted) != 0 {
		fn := f.posted[0]
		f.posted = f.posted[1:]
		fn()
	}
}

type fakeSmokeTimer struct {
	fired   func()
	stopped bool
}

func (f *fakeSmokeTimer) Stop() bool {
	f.stopped = true
	return true
}

type fakeSmokeScheduler struct{ timers []*fakeSmokeTimer }

func (f *fakeSmokeScheduler) after(_ time.Duration, fn func()) smokeTimer {
	timer := &fakeSmokeTimer{fired: fn}
	f.timers = append(f.timers, timer)
	return timer
}

func newControlledSmokeDemo() (*demo, *fakeSmokeRuntime, *fakeSmokeScheduler) {
	runtime := &fakeSmokeRuntime{}
	scheduler := &fakeSmokeScheduler{}
	d := newDemo(nil)
	d.smokeApp = runtime
	d.afterFunc = scheduler.after
	return d, runtime, scheduler
}

func TestSmokeStepsUsePublicMenuLifecycleAndQuit(t *testing.T) {
	d, runtime, scheduler := newControlledSmokeDemo()
	d.initModels()
	if !d.disabled.Enabled() {
		t.Fatal("demo item must start enabled so smoke proves a real enabled-to-disabled transition")
	}
	runtime.run = func() error {
		if len(scheduler.timers) != 2 {
			t.Fatalf("timers at Run = %d, want watchdog and first step", len(scheduler.timers))
		}
		scheduler.timers[1].fired()
		runtime.drain()
		if len(scheduler.timers) != 3 {
			t.Fatalf("timers after first step = %d, want a second step", len(scheduler.timers))
		}
		scheduler.timers[2].fired()
		runtime.drain()
		return nil
	}

	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke: %v", err)
	}
	if !d.smokeStepsComplete || runtime.quitCalls != 1 {
		t.Errorf("completion=%v Quit calls=%d, want true/1", d.smokeStepsComplete, runtime.quitCalls)
	}
	if !scheduler.timers[0].stopped {
		t.Error("watchdog was not stopped after Run returned")
	}
	if d.firstChecked.Checked() {
		t.Error("public SetChecked update in the first smoke step did not take effect")
	}
	if d.disabled.Enabled() {
		t.Error("disabled demo item became enabled")
	}
	if d.replacementChecked == nil || !d.replacementChecked.Checked() {
		t.Error("replacement checked item was not updated through its public model")
	}
	if len(d.replacement) != 1 || d.replacement[0].Title != "Tools" {
		t.Errorf("replacement menus = %+v, want one Tools menu", d.replacement)
	}
	if got := d.firstStatus.Text(); got != "First: replacement installed" {
		t.Errorf("first status = %q, want replacement confirmation", got)
	}
	if got := d.secondStatus.Text(); got != "Second: content replaced, menus preserved" {
		t.Errorf("second status = %q, want content replacement confirmation", got)
	}
}

func TestSmokeWatchdogBoundsAStalledRun(t *testing.T) {
	d, runtime, scheduler := newControlledSmokeDemo()
	runtime.run = func() error {
		scheduler.timers[0].fired()
		runtime.drain()
		return nil
	}

	if err := d.runSmoke(); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("runSmoke error = %v, want deadline exceeded", err)
	}
	if runtime.quitCalls != 1 || !scheduler.timers[0].stopped {
		t.Errorf("watchdog cleanup: Quit=%d stopped=%v, want 1/true", runtime.quitCalls, scheduler.timers[0].stopped)
	}
}

func TestSmokeRejectsRunReturningBeforeSteps(t *testing.T) {
	d, runtime, scheduler := newControlledSmokeDemo()
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
