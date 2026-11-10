// Command meltemi-dialog demonstrates the public App.OpenFile/SaveFile native
// file dialogs through the XDG desktop portal: open, multi-select and save
// buttons with filter lists, and a status line fed by UI-thread callbacks.
// With --smoke it creates a real portal dialog, closes it programmatically
// and reports the exactly-once cancel callback under a watchdog; unsupported,
// canceled or failed runs are reported honestly instead of claimed as
// success.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zhoudm1743/meltemi/ui"
)

const (
	dialogSmokeStartDelay = 400 * time.Millisecond
	dialogSmokeCloseDelay = 800 * time.Millisecond
	dialogSmokeDeadline   = 15 * time.Second
)

var (
	demoFilters = []ui.FileFilter{
		{
			Name: "Text",
			Patterns: []ui.FileFilterPattern{
				{Kind: ui.FileFilterPatternGlob, Pattern: "*.txt"},
				{Kind: ui.FileFilterPatternGlob, Pattern: "*.md"},
				{Kind: ui.FileFilterPatternMIMEType, Pattern: "text/plain"},
			},
		},
		{Name: "All files", Patterns: []ui.FileFilterPattern{{Kind: ui.FileFilterPatternGlob, Pattern: "*"}}},
	}
	demoTextFilter = ui.FileFilter{
		Name: "Text",
		Patterns: []ui.FileFilterPattern{
			{Kind: ui.FileFilterPatternGlob, Pattern: "*.txt"},
			{Kind: ui.FileFilterPatternGlob, Pattern: "*.md"},
			{Kind: ui.FileFilterPatternMIMEType, Pattern: "text/plain"},
		},
	}
)

type smokeRuntime interface {
	Post(func()) error
	Quit()
	Run() error
}

type smokeTimer interface{ Stop() bool }

// dialogHandle is the demo-level seam over *ui.FileDialog so smoke tests can
// drive the programmatic close without a portal.
type dialogHandle interface{ Close() error }

type demo struct {
	app      *ui.App
	smokeApp smokeRuntime
	window   *ui.Window
	status   *ui.LabelWidget

	openButton         *ui.ButtonWidget
	openMultipleButton *ui.ButtonWidget
	saveButton         *ui.ButtonWidget

	openFile     func(ui.OpenFileOptions, func([]string, error)) (dialogHandle, error)
	saveFile     func(ui.SaveFileOptions, func(string, error)) (dialogHandle, error)
	capabilities func() ui.Capabilities

	afterFunc func(time.Duration, func()) smokeTimer

	smokeOutcomeValue string
	smokeDialog       dialogHandle
	smokeClosedByStep bool

	smokeStepsComplete bool
	runReturned        bool
	mu                 sync.Mutex
	err                error
}

func newDemo(app *ui.App) *demo {
	d := &demo{
		app:      app,
		smokeApp: app,
		afterFunc: func(delay time.Duration, fn func()) smokeTimer {
			return time.AfterFunc(delay, fn)
		},
	}
	if app != nil {
		d.openFile = func(options ui.OpenFileOptions, callback func([]string, error)) (dialogHandle, error) {
			return app.OpenFile(options, callback)
		}
		d.saveFile = func(options ui.SaveFileOptions, callback func(string, error)) (dialogHandle, error) {
			return app.SaveFile(options, callback)
		}
		d.capabilities = app.Capabilities
	}
	return d
}

func (d *demo) initModels() {
	d.status = ui.Label("Pick a dialog; results land here on the UI thread.")
	d.openButton = ui.Button("Open file…").OnClick(d.openSingle)
	d.openMultipleButton = ui.Button("Open multiple…").OnClick(d.openMultiple)
	d.saveButton = ui.Button("Save as…").OnClick(d.save)
}

func (d *demo) init() {
	d.initModels()
	d.window = d.app.Window("Meltemi file dialogs").
		Size(580, 240).
		Content(ui.Column(
			ui.Label("Native open/save dialogs via the XDG desktop portal; Meltemi CSS does not style them."),
			d.status,
			ui.Row(d.openButton, d.openMultipleButton, d.saveButton).Gap(8),
		).Gap(12).Padding(20))
	d.window.Show()
	if d.capabilities == nil {
		return
	}
	if d.capabilities().FileDialog {
		d.setStatus("Native file dialogs: available.")
	} else {
		d.setStatus("Native file dialogs: unavailable (no XDG portal FileChooser).")
	}
}

func (d *demo) setStatus(text string) {
	if d.status != nil {
		d.status.SetText(text)
	}
}

func (d *demo) openSingle() {
	_, err := d.openFile(ui.OpenFileOptions{
		Title:         "Open file",
		Filters:       demoFilters,
		CurrentFilter: &demoTextFilter,
		Parent:        d.window,
	}, func(paths []string, err error) {
		switch {
		case errors.Is(err, ui.ErrDialogCanceled):
			d.setStatus("Open canceled.")
		case err != nil:
			d.setStatus("Open failed: " + err.Error())
		default:
			d.setStatus("Opened: " + strings.Join(paths, ", "))
		}
	})
	if err != nil {
		d.setStatus("Open unavailable: " + err.Error())
	}
}

func (d *demo) openMultiple() {
	_, err := d.openFile(ui.OpenFileOptions{
		Title:         "Open multiple",
		Multiple:      true,
		Filters:       demoFilters,
		CurrentFilter: &demoTextFilter,
		Parent:        d.window,
	}, func(paths []string, err error) {
		switch {
		case errors.Is(err, ui.ErrDialogCanceled):
			d.setStatus("Open multiple canceled.")
		case err != nil:
			d.setStatus("Open multiple failed: " + err.Error())
		default:
			d.setStatus(fmt.Sprintf("Opened %d files: %s", len(paths), strings.Join(paths, ", ")))
		}
	})
	if err != nil {
		d.setStatus("Open multiple unavailable: " + err.Error())
	}
}

func (d *demo) save() {
	_, err := d.saveFile(ui.SaveFileOptions{
		Title:         "Save file",
		CurrentName:   "notes.txt",
		Filters:       demoFilters,
		CurrentFilter: &demoTextFilter,
		Parent:        d.window,
	}, func(path string, err error) {
		switch {
		case errors.Is(err, ui.ErrDialogCanceled):
			d.setStatus("Save canceled.")
		case err != nil:
			d.setStatus("Save failed: " + err.Error())
		default:
			d.setStatus("Saved: " + path)
		}
	})
	if err != nil {
		d.setStatus("Save unavailable: " + err.Error())
	}
}

func (d *demo) noteErr(err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err == nil {
		d.err = err
	}
}

func (d *demo) firstErr() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

func (d *demo) markRunReturned() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.runReturned = true
}

func (d *demo) hasRunReturned() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runReturned
}

func (d *demo) smokeOutcome() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.smokeOutcomeValue
}

func (d *demo) after(delay time.Duration, step func()) {
	d.afterFunc(delay, func() {
		if err := d.smokeApp.Post(step); err != nil {
			d.noteErr(fmt.Errorf("meltemi-dialog: smoke: post step: %w", err))
			d.smokeApp.Quit()
		}
	})
}

func (d *demo) completeSmoke(outcome string) {
	d.mu.Lock()
	d.smokeOutcomeValue = outcome
	d.smokeStepsComplete = true
	d.mu.Unlock()
	fmt.Println("meltemi-dialog: smoke: OK: portal dialog created and closed programmatically; cancel callback observed")
	d.smokeApp.Quit()
}

func (d *demo) finishSmoke(outcome string, err error) {
	d.mu.Lock()
	if d.smokeOutcomeValue == "" {
		d.smokeOutcomeValue = outcome
	}
	d.mu.Unlock()
	d.noteErr(err)
	fmt.Fprintf(os.Stderr, "meltemi-dialog: smoke: %s: %v\n", outcome, err)
	d.smokeApp.Quit()
}

func (d *demo) smokeOpenStep() {
	if d.capabilities == nil || d.openFile == nil {
		d.finishSmoke("error", errors.New("meltemi-dialog: smoke: demo was not initialized"))
		return
	}
	if !d.capabilities().FileDialog {
		d.finishSmoke("unsupported", errors.New("meltemi-dialog: smoke: native file dialogs unavailable (no working XDG portal FileChooser)"))
		return
	}
	handle, err := d.openFile(ui.OpenFileOptions{
		Title:         "Smoke open",
		Multiple:      true,
		Filters:       demoFilters,
		CurrentFilter: &demoTextFilter,
		Parent:        d.window,
	}, d.smokeOpenCallback)
	if err != nil {
		if errors.Is(err, ui.ErrDialogUnavailable) {
			d.finishSmoke("unsupported", fmt.Errorf("meltemi-dialog: smoke: %w", err))
			return
		}
		d.finishSmoke("error", fmt.Errorf("meltemi-dialog: smoke: open: %w", err))
		return
	}
	d.mu.Lock()
	d.smokeDialog = handle
	d.mu.Unlock()
	d.after(dialogSmokeCloseDelay, d.smokeCloseStep)
}

func (d *demo) smokeCloseStep() {
	d.mu.Lock()
	if d.smokeOutcomeValue != "" {
		d.mu.Unlock()
		return
	}
	handle := d.smokeDialog
	d.smokeDialog = nil
	d.smokeClosedByStep = true
	d.mu.Unlock()
	if handle == nil {
		d.finishSmoke("error", errors.New("meltemi-dialog: smoke: no dialog handle to close"))
		return
	}
	if err := handle.Close(); err != nil {
		d.finishSmoke("error", fmt.Errorf("meltemi-dialog: smoke: programmatic close: %w", err))
	}
	// The cancel callback completes the smoke.
}

func (d *demo) smokeOpenCallback(paths []string, err error) {
	d.mu.Lock()
	outcome := d.smokeOutcomeValue
	closed := d.smokeClosedByStep
	d.mu.Unlock()
	if outcome != "" {
		return
	}
	switch {
	case err != nil && errors.Is(err, ui.ErrDialogCanceled) && closed:
		d.completeSmoke("closed")
	case err != nil && errors.Is(err, ui.ErrDialogCanceled):
		d.finishSmoke("canceled", errors.New("meltemi-dialog: smoke: dialog canceled before the programmatic close"))
	case err != nil:
		d.finishSmoke("error", fmt.Errorf("meltemi-dialog: smoke: dialog error: %w", err))
	default:
		d.finishSmoke("selected", fmt.Errorf("meltemi-dialog: smoke: user selected %q before the programmatic close", strings.Join(paths, ", ")))
	}
}

func (d *demo) smokeWatchdog() {
	if d.hasRunReturned() {
		return
	}
	if err := d.smokeApp.Post(func() {
		if d.hasRunReturned() {
			return
		}
		d.noteErr(errors.New("meltemi-dialog: smoke: deadline exceeded, Run has not returned"))
		d.smokeApp.Quit()
	}); err != nil {
		return
	}
}

func (d *demo) startSmoke() smokeTimer {
	watchdog := d.afterFunc(dialogSmokeDeadline, d.smokeWatchdog)
	d.after(dialogSmokeStartDelay, d.smokeOpenStep)
	return watchdog
}

func (d *demo) runSmoke() error {
	watchdog := d.startSmoke()
	runErr := d.smokeApp.Run()
	d.markRunReturned()
	watchdog.Stop()
	d.noteErr(runErr)
	if !d.smokeStepsComplete && d.firstErr() == nil {
		d.noteErr(errors.New("meltemi-dialog: smoke: Run returned before smoke steps completed"))
	}
	return d.firstErr()
}

func main() {
	smoke := flag.Bool("smoke", false, "create a real portal dialog, close it programmatically, report the cancel callback and quit")
	flag.Parse()

	app := ui.New()
	d := newDemo(app)
	if err := app.Post(d.init); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var err error
	if *smoke {
		err = d.runSmoke()
	} else {
		err = app.Run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
