// Command meltemi-ui-basic is the public chained-API demo: one app, two decorated
// windows composed from the built-in components. The main window shows a
// counter label and a button; clicking the button increments the counter via
// Label.SetText and creates or re-shows the settings window. Both windows
// keep their native decorations and render exclusively through the public
// App/Window lifecycle — no driver, GLFW or OpenGL calls here. With --smoke a
// timer-driven script Posts to the UI thread, clicks the button through the
// public Button.Click, checks the window count, closes the windows one by one
// and relies on the default last-window-quit policy to end Run.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/zhoudm1743/meltemi/ui"
)

const (
	windowTitle     = "Meltemi UI Basic"
	settingsTitle   = "Meltemi Settings"
	smokeStartDelay = 500 * time.Millisecond
	smokeStepDelay  = 150 * time.Millisecond
	smokeDeadline   = 15 * time.Second
)

// demo owns the app and the components the smoke script and the click
// handler touch. clicks and the component handles are confined to the UI
// thread: every reader and writer runs inside a function passed to App.Post.
// err is the first failure of any step; it is mutex-protected because a
// timer goroutine may need to record a Post failure.
type demo struct {
	app      *ui.App
	mainWin  *ui.Window
	settings *ui.Window
	counter  *ui.LabelWidget
	openBtn  *ui.ButtonWidget
	clicks   int

	mu  sync.Mutex
	err error
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

// init builds the main window's column — counter label, open button,
// Gap(12).Padding(20) — and shows it. Posted before Run, so the window
// exists and drew its first frame before any timer fires.
func (d *demo) init() {
	d.counter = ui.Label("Clicks: 0")
	d.openBtn = ui.Button("Open Settings").OnClick(d.openSettings)
	content := ui.Column(d.counter, d.openBtn).Gap(12).Padding(20)
	d.mainWin = d.app.Window(windowTitle).Size(420, 200).Content(content)
	d.mainWin.Show()
}

// openSettings is the button's click handler: it runs the counter update and
// the settings-window create/show together, exactly what a real handler does.
// A second click re-shows the existing window instead of creating a new one.
func (d *demo) openSettings() {
	d.clicks++
	d.counter.SetText(fmt.Sprintf("Clicks: %d", d.clicks))
	if d.settings == nil {
		info := ui.Label("Created by the public chained API.")
		d.settings = d.app.Window(settingsTitle).Size(360, 160).Content(
			ui.Column(ui.Label("Settings"), info).Gap(12).Padding(20))
	}
	d.settings.Show()
}

// after schedules the next smoke step: the timer goroutine only calls the
// goroutine-safe App.Post, so UI state stays on the UI thread. A Post
// failure is recorded and Quit keeps an unattended run from hanging.
func (d *demo) after(delay time.Duration, step func()) {
	time.AfterFunc(delay, func() {
		if err := d.app.Post(step); err != nil {
			d.noteErr(fmt.Errorf("meltemi-ui-basic: smoke: post step: %w", err))
			d.app.Quit()
		}
	})
}

// abort ends the smoke run early when a step already failed: the first error
// is kept, Quit makes Run return, and main turns the recorded error into a
// non-zero exit.
func (d *demo) abort() {
	d.app.Quit()
}

func (d *demo) smokeClickStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.openBtn.Click()
	if d.clicks != 1 {
		d.noteErr(fmt.Errorf("meltemi-ui-basic: smoke: clicks = %d after Click, want 1", d.clicks))
		d.abort()
		return
	}
	if got := d.counter.Text(); got != "Clicks: 1" {
		d.noteErr(fmt.Errorf("meltemi-ui-basic: smoke: counter label = %q after Click, want %q", got, "Clicks: 1"))
		d.abort()
		return
	}
	d.after(smokeStepDelay, d.smokeWindowsStep)
}

func (d *demo) smokeWindowsStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	if got := len(d.app.Windows()); got != 2 {
		d.noteErr(fmt.Errorf("meltemi-ui-basic: smoke: App.Windows() = %d after opening settings, want 2", got))
		d.abort()
		return
	}
	d.after(smokeStepDelay, d.smokeCloseSettingsStep)
}

func (d *demo) smokeCloseSettingsStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.noteErr(d.settings.Close())
	d.after(smokeStepDelay, d.smokeOneWindowStep)
}

func (d *demo) smokeOneWindowStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	if got := len(d.app.Windows()); got != 1 {
		d.noteErr(fmt.Errorf("meltemi-ui-basic: smoke: App.Windows() = %d after closing settings, want 1", got))
		d.abort()
		return
	}
	d.after(smokeStepDelay, d.smokeCloseMainStep)
}

// smokeCloseMainStep closes the last window. The default LastWindowQuit
// policy then destroys the window, releases the shared face and quits Run —
// no Quit call here, so the smoke run also proves the default policy. If the
// policy never fires, the watchdog below records the hang and quits.
func (d *demo) smokeCloseMainStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.noteErr(d.mainWin.Close())
}

// smokeWatchdog bounds the whole run: if Run has not returned by the
// deadline — a stuck close, a lost step — it records the hang and quits. A
// clean run exits long before; a quit app drops the watchdog's Post, which
// is fine because there is nothing left to report.
func (d *demo) smokeWatchdog() {
	if err := d.app.Post(func() {
		d.noteErr(errors.New("meltemi-ui-basic: smoke: deadline exceeded, app did not quit after the last window closed"))
		d.app.Quit()
	}); err != nil {
		d.noteErr(fmt.Errorf("meltemi-ui-basic: smoke: watchdog post: %w", err))
	}
}

// startSmoke arms the watchdog and the first step before Run starts.
func (d *demo) startSmoke() {
	time.AfterFunc(smokeDeadline, d.smokeWatchdog)
	time.AfterFunc(smokeStartDelay, func() {
		if err := d.app.Post(d.smokeClickStep); err != nil {
			d.noteErr(fmt.Errorf("meltemi-ui-basic: smoke: post first step: %w", err))
			d.app.Quit()
		}
	})
}

func main() {
	smoke := flag.Bool("smoke", false, "drive the public API with a timer-posted script, then exit")
	flag.Parse()

	app := ui.New()
	d := &demo{app: app}
	if err := app.Post(d.init); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *smoke {
		d.startSmoke()
	}

	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := d.firstErr(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
