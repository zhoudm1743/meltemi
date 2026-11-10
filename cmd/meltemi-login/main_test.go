package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/ui"
)

type fakeSmokeRuntime struct {
	posted      []func()
	postErr     error
	liveWindows int
	quitCalls   int
	run         func() error
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

func (f *fakeSmokeRuntime) Windows() []*ui.Window {
	return make([]*ui.Window, f.liveWindows)
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

func TestSmokeWatchdogIgnoresCleanupAfterLastWindowClose(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo()
	d.smokeStepsComplete = true
	closeErr := errors.New("close cleanup failure")
	runtime.run = func() error {
		// The internal app still accepts work during its final drain, even
		// though the public window set is already empty.
		scheduler.timers[0].fired()
		if len(runtime.posted) != 1 {
			t.Fatalf("watchdog Post count = %d, want 1", len(runtime.posted))
		}
		runtime.posted[0]()
		return closeErr
	}

	if err := d.runSmoke(); !errors.Is(err, closeErr) {
		t.Errorf("runSmoke error = %v, want original close error %v", err, closeErr)
	}
	if err := d.firstErr(); err != nil {
		t.Fatalf("cleanup-after-close watchdog error = %v, want nil", err)
	}
	if runtime.quitCalls != 0 {
		t.Errorf("cleanup-after-close watchdog Quit calls = %d, want 0", runtime.quitCalls)
	}
	if !scheduler.timers[0].stopped {
		t.Error("runSmoke did not stop the watchdog after Run returned")
	}
}

func TestSmokeWatchdogStoppedAfterRunDropsRejectedPost(t *testing.T) {
	d, runtime, scheduler := newFakeSmokeDemo()
	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke error = %v, want nil", err)
	}
	if !scheduler.timers[0].stopped {
		t.Fatal("Run-return path did not stop the watchdog timer")
	}

	// A timer callback that was already running when Stop raced with it may
	// still call Post. Once Run has returned, rejection is normal shutdown.
	runtime.postErr = errors.New("application closed")
	scheduler.timers[0].fired()
	if len(runtime.posted) != 0 {
		t.Fatalf("rejected watchdog unexpectedly queued %d callbacks", len(runtime.posted))
	}
	if err := d.firstErr(); err != nil {
		t.Fatalf("post-Run watchdog error = %v, want nil", err)
	}
	if runtime.quitCalls != 0 {
		t.Errorf("post-Run watchdog Quit calls = %d, want 0", runtime.quitCalls)
	}
}

func TestSmokeWatchdogStillReportsRealTimeout(t *testing.T) {
	tests := []struct {
		name          string
		stepsComplete bool
		liveWindows   int
	}{
		{name: "steps incomplete", stepsComplete: false, liveWindows: 0},
		{name: "live window remains", stepsComplete: true, liveWindows: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, runtime, scheduler := newFakeSmokeDemo()
			d.startSmoke()
			d.smokeStepsComplete = tt.stepsComplete
			runtime.liveWindows = tt.liveWindows

			scheduler.timers[0].fired()
			if len(runtime.posted) != 1 {
				t.Fatalf("watchdog Post count = %d, want 1", len(runtime.posted))
			}
			runtime.posted[0]()

			if err := d.firstErr(); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
				t.Errorf("watchdog error = %v, want deadline exceeded", err)
			}
			if runtime.quitCalls != 1 {
				t.Errorf("watchdog Quit calls = %d, want 1", runtime.quitCalls)
			}
		})
	}
}
