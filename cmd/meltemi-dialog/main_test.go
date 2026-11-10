package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/ui"
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

// fakeDialogHandle records programmatic closes.
type fakeDialogHandle struct {
	closeCalls int
	closeErr   error
}

func (f *fakeDialogHandle) Close() error {
	f.closeCalls++
	return f.closeErr
}

// fakeDialogOpeners captures the demo's dialog calls without any portal.
type fakeDialogOpeners struct {
	openPathsCallback func([]string, error)
	openErr           error
	saveErr           error
	openHandle        *fakeDialogHandle
	saveHandle        *fakeDialogHandle
	openCalls         int
	saveCalls         int
	lastOpenOptions   ui.OpenFileOptions
	lastSaveOptions   ui.SaveFileOptions
}

func (f *fakeDialogOpeners) open(options ui.OpenFileOptions, callback func([]string, error)) (dialogHandle, error) {
	f.openCalls++
	f.lastOpenOptions = options
	if f.openErr != nil {
		return nil, f.openErr
	}
	f.openPathsCallback = callback
	if f.openHandle == nil {
		f.openHandle = &fakeDialogHandle{}
	}
	return f.openHandle, nil
}

func (f *fakeDialogOpeners) save(options ui.SaveFileOptions, callback func(string, error)) (dialogHandle, error) {
	f.saveCalls++
	f.lastSaveOptions = options
	if f.saveErr != nil {
		return nil, f.saveErr
	}
	if f.saveHandle == nil {
		f.saveHandle = &fakeDialogHandle{}
	}
	return f.saveHandle, nil
}

func newControlledSmokeDemo(openers *fakeDialogOpeners, fileDialog bool) (*demo, *fakeSmokeRuntime, *fakeSmokeScheduler) {
	runtime := &fakeSmokeRuntime{}
	scheduler := &fakeSmokeScheduler{}
	d := newDemo(nil)
	d.smokeApp = runtime
	d.afterFunc = scheduler.after
	d.openFile = openers.open
	d.saveFile = openers.save
	d.capabilities = func() ui.Capabilities {
		return ui.Capabilities{MenuBar: true, FileDialog: fileDialog}
	}
	return d, runtime, scheduler
}

func TestSmokeCreatesPortalDialogAndClosesIt(t *testing.T) {
	openers := &fakeDialogOpeners{}
	d, runtime, scheduler := newControlledSmokeDemo(openers, true)
	runtime.run = func() error {
		if len(scheduler.timers) != 2 {
			t.Fatalf("timers at Run = %d, want watchdog and open step", len(scheduler.timers))
		}
		scheduler.timers[1].fired()
		runtime.drain()
		if openers.openCalls != 1 {
			t.Fatalf("open calls = %d, want 1", openers.openCalls)
		}
		if len(scheduler.timers) != 3 {
			t.Fatalf("timers after open step = %d, want the programmatic close step", len(scheduler.timers))
		}
		scheduler.timers[2].fired()
		runtime.drain()
		// Closing the dialog delivers the cancel to the callback.
		openers.openPathsCallback(nil, ui.ErrDialogCanceled)
		return nil
	}

	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke: %v", err)
	}
	if !d.smokeStepsComplete {
		t.Error("smoke steps did not complete")
	}
	if quit := runtime.quitCalls; quit != 1 {
		t.Errorf("Quit calls = %d, want 1", quit)
	}
	if !scheduler.timers[0].stopped {
		t.Error("watchdog was not stopped after Run returned")
	}
	if openers.openHandle.closeCalls != 1 {
		t.Errorf("programmatic Close calls = %d, want 1", openers.openHandle.closeCalls)
	}
	if got := d.smokeOutcome(); got != "closed" {
		t.Errorf("smoke outcome = %q, want closed", got)
	}
}

func TestSmokeCancelCallbackCompletesAfterProgrammaticClose(t *testing.T) {
	openers := &fakeDialogOpeners{}
	d, runtime, scheduler := newControlledSmokeDemo(openers, true)
	runtime.run = func() error {
		scheduler.timers[1].fired()
		runtime.drain()
		scheduler.timers[2].fired()
		runtime.drain()
		// The ui layer posts the user callback after Close cancels the dialog.
		openers.openPathsCallback(nil, ui.ErrDialogCanceled)
		return nil
	}

	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke: %v", err)
	}
	if got := d.smokeOutcome(); got != "closed" {
		t.Errorf("smoke outcome = %q, want closed", got)
	}
}

func TestSmokeReportsUnsupportedCapability(t *testing.T) {
	openers := &fakeDialogOpeners{}
	d, runtime, scheduler := newControlledSmokeDemo(openers, false)
	runtime.run = func() error {
		scheduler.timers[1].fired()
		runtime.drain()
		return nil
	}

	err := d.runSmoke()
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("runSmoke error = %v, want an honest unavailable report", err)
	}
	if openers.openCalls != 0 {
		t.Errorf("open calls = %d, want 0 on unsupported platforms", openers.openCalls)
	}
	if got := d.smokeOutcome(); got != "unsupported" {
		t.Errorf("smoke outcome = %q, want unsupported", got)
	}
	if runtime.quitCalls != 1 {
		t.Errorf("Quit calls = %d, want 1", runtime.quitCalls)
	}
}

func TestSmokeReportsUnavailableOpenError(t *testing.T) {
	openers := &fakeDialogOpeners{openErr: ui.ErrDialogUnavailable}
	d, runtime, scheduler := newControlledSmokeDemo(openers, true)
	runtime.run = func() error {
		scheduler.timers[1].fired()
		runtime.drain()
		return nil
	}

	err := d.runSmoke()
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("runSmoke error = %v, want an honest unavailable report", err)
	}
	if got := d.smokeOutcome(); got != "unsupported" {
		t.Errorf("smoke outcome = %q, want unsupported", got)
	}
}

func TestSmokeReportsEarlyUserCancel(t *testing.T) {
	openers := &fakeDialogOpeners{}
	d, runtime, scheduler := newControlledSmokeDemo(openers, true)
	runtime.run = func() error {
		scheduler.timers[1].fired()
		runtime.drain()
		// The user dismisses the dialog before the programmatic close step.
		openers.openPathsCallback(nil, ui.ErrDialogCanceled)
		scheduler.timers[2].fired()
		runtime.drain()
		return nil
	}

	err := d.runSmoke()
	if err == nil || !strings.Contains(err.Error(), "canceled before") {
		t.Errorf("runSmoke error = %v, want an honest early-cancel report", err)
	}
	if got := d.smokeOutcome(); got != "canceled" {
		t.Errorf("smoke outcome = %q, want canceled", got)
	}
}

func TestSmokeReportsPortalError(t *testing.T) {
	openers := &fakeDialogOpeners{openErr: errors.New("portal exploded")}
	d, runtime, scheduler := newControlledSmokeDemo(openers, true)
	runtime.run = func() error {
		scheduler.timers[1].fired()
		runtime.drain()
		return nil
	}

	err := d.runSmoke()
	if err == nil || !strings.Contains(err.Error(), "portal exploded") {
		t.Errorf("runSmoke error = %v, want the portal error reported", err)
	}
	if got := d.smokeOutcome(); got != "error" {
		t.Errorf("smoke outcome = %q, want error", got)
	}
}

func TestSmokeWatchdogBoundsAStalledRun(t *testing.T) {
	openers := &fakeDialogOpeners{}
	d, runtime, scheduler := newControlledSmokeDemo(openers, true)
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

func TestButtonsUseThePublicDialogAPI(t *testing.T) {
	openers := &fakeDialogOpeners{}
	d := newDemo(ui.New())
	d.window = d.app.Window("dialog buttons")
	d.openFile = openers.open
	d.saveFile = openers.save
	d.initModels()

	if d.openButton == nil || d.openMultipleButton == nil || d.saveButton == nil {
		t.Fatal("demo buttons were not initialized")
	}
	d.openButton.Click()
	if openers.openCalls != 1 || openers.saveCalls != 0 {
		t.Fatalf("open button: open=%d save=%d, want 1/0", openers.openCalls, openers.saveCalls)
	}
	if openers.lastOpenOptions.Title != "Open file" || openers.lastOpenOptions.Multiple {
		t.Errorf("open options = %+v, want a single-selection Open file dialog", openers.lastOpenOptions)
	}
	if len(openers.lastOpenOptions.Filters) == 0 || openers.lastOpenOptions.CurrentFilter == nil {
		t.Errorf("open options carry no filter demo: %+v", openers.lastOpenOptions)
	}
	if openers.lastOpenOptions.Parent == nil {
		t.Error("open options do not declare the demo window as Parent")
	}
	openers.openPathsCallback([]string{"/tmp/picked.txt"}, nil)
	if status := d.status.Text(); !strings.Contains(status, "/tmp/picked.txt") {
		t.Errorf("status after a picked file = %q, want the path", status)
	}

	d.openMultipleButton.Click()
	if openers.openCalls != 2 {
		t.Fatalf("open multiple: open calls = %d, want 2", openers.openCalls)
	}
	if !openers.lastOpenOptions.Multiple {
		t.Error("open multiple did not set Multiple")
	}
	openers.openPathsCallback(nil, ui.ErrDialogCanceled)
	if status := d.status.Text(); !strings.Contains(status, "canceled") {
		t.Errorf("status after cancel = %q, want a cancel note", status)
	}

	d.saveButton.Click()
	if openers.saveCalls != 1 {
		t.Fatalf("save button: save calls = %d, want 1", openers.saveCalls)
	}
	if openers.lastSaveOptions.Title != "Save file" || openers.lastSaveOptions.CurrentName == "" {
		t.Errorf("save options = %+v, want a Save file dialog with a seeded name", openers.lastSaveOptions)
	}
}

func TestSmokeOpenStepUsesDialogOptions(t *testing.T) {
	openers := &fakeDialogOpeners{}
	d, runtime, scheduler := newControlledSmokeDemo(openers, true)
	runtime.run = func() error {
		scheduler.timers[1].fired()
		runtime.drain()
		scheduler.timers[2].fired()
		runtime.drain()
		openers.openPathsCallback(nil, ui.ErrDialogCanceled)
		return nil
	}
	if err := d.runSmoke(); err != nil {
		t.Fatalf("runSmoke: %v", err)
	}
	options := openers.lastOpenOptions
	if !options.Multiple {
		t.Error("smoke dialog is not multi-select")
	}
	if len(options.Filters) == 0 {
		t.Error("smoke dialog carries no filters")
	}
}
