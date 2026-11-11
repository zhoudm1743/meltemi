// Command meltemi-tray demonstrates a native tray menu and restoring an existing
// CloseHide window. Native tray menus are rendered by the desktop; the window
// MenuBar CSS does not style them. With --smoke it activates Restore window
// through the platform tray (D-Bus on Linux, the Win32 tray window on Windows)
// and exits under a watchdog.
package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"sync"
	"time"

	"github.com/zhoudm1743/meltemi/ui"
)

const (
	traySmokeStartDelay = 400 * time.Millisecond
	traySmokeStepDelay  = 200 * time.Millisecond
	traySmokeDeadline   = 15 * time.Second
)

type smokeRuntime interface {
	Post(func()) error
	Quit()
	Run() error
}

type smokeTimer interface{ Stop() bool }

type trayDemoWindow interface {
	Show()
	Close() error
}

type demo struct {
	app      *ui.App
	smokeApp smokeRuntime
	window   trayDemoWindow
	tray     *ui.Tray

	menu            *ui.MenuModel
	checked         *ui.MenuItemModel
	disabled        *ui.MenuItemModel
	restoreCalls    int
	activateRestore func() error

	afterFunc    func(time.Duration, func()) smokeTimer
	smokeFailure func(error)

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
		smokeFailure: func(err error) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		},
	}
	d.activateRestore = activateRestore
	return d
}

func (d *demo) initModels() {
	restore := ui.MenuItem("Restore window").OnClick(func() {
		d.restoreCalls++
		if d.window != nil {
			d.window.Show()
		}
	})
	d.checked = ui.MenuItem("Checked tool")
	d.disabled = ui.MenuItem("Disabled tool")
	nested := ui.MenuItem("More tools").Submenu(
		ui.MenuItem("Nested action").OnClick(func() {
			d.checked.SetChecked(!d.checked.Checked())
		}),
	)
	quit := ui.MenuItem("Quit").OnClick(func() {
		d.smokeApp.Quit()
	})
	d.menu = ui.Menu("not shown in the native root",
		restore,
		d.checked,
		d.disabled,
		ui.MenuSeparator(),
		nested,
		quit,
	)
}

func (d *demo) init() {
	d.initModels()
	d.app.SetLastWindowAction(ui.LastWindowKeepRunning)
	window := d.app.Window("Meltemi native tray").
		Size(520, 260).
		Content(ui.Column(
			ui.Label("Close hides this window; Restore window in the tray brings it back."),
			ui.Label("The native tray menu is desktop-rendered; MenuBar CSS does not apply."),
		).Gap(12).Padding(20)).
		CloseAction(ui.CloseHide)
	d.window = window
	window.Show()

	icon := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			icon.SetNRGBA(x, y, color.NRGBA{R: 40, G: 110, B: 220, A: 255})
		}
	}
	tray, err := ui.NewTray(icon, "Meltemi tray — restore the existing window")
	if err != nil {
		d.abortSmoke(fmt.Errorf("meltemi-tray: new tray: %w", err))
		return
	}
	d.tray = tray
	if err := tray.SetMenu(d.menu); err != nil {
		d.abortSmoke(err)
		return
	}
	if err := d.app.ShowTray(tray); err != nil {
		d.abortSmoke(fmt.Errorf("meltemi-tray: native tray unavailable: %w", err))
		return
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

func (d *demo) after(delay time.Duration, step func()) {
	d.afterFunc(delay, func() {
		if err := d.smokeApp.Post(step); err != nil {
			d.noteErr(fmt.Errorf("meltemi-tray: smoke: post step: %w", err))
			d.smokeApp.Quit()
		}
	})
}

func (d *demo) abortSmoke(err error) {
	d.noteErr(err)
	d.smokeApp.Quit()
}

func (d *demo) smokeCloseStep() {
	if d.window == nil || d.menu == nil || d.checked == nil || d.disabled == nil {
		d.abortSmoke(errors.New("meltemi-tray: smoke: demo was not initialized"))
		return
	}
	if err := d.window.Close(); err != nil {
		d.abortSmoke(fmt.Errorf("meltemi-tray: smoke: CloseHide: %w", err))
		return
	}
	d.checked.SetChecked(true)
	d.disabled.SetEnabled(false)
	d.after(traySmokeStepDelay, d.smokeActivateStep)
}

func (d *demo) smokeActivateStep() {
	if d.activateRestore == nil {
		d.abortSmoke(errors.New("meltemi-tray: smoke: no native activation function"))
		return
	}
	if err := d.activateRestore(); err != nil {
		d.abortSmoke(fmt.Errorf("meltemi-tray: smoke: native Restore window activation: %w", err))
		return
	}
	d.after(traySmokeStepDelay, d.smokeFinishStep)
}

func (d *demo) smokeFinishStep() {
	if d.restoreCalls != 1 {
		d.abortSmoke(fmt.Errorf("meltemi-tray: smoke: restore callback count = %d, want 1", d.restoreCalls))
		return
	}
	d.smokeStepsComplete = true
	d.smokeApp.Quit()
}

func (d *demo) smokeWatchdog() {
	if d.hasRunReturned() {
		return
	}
	err := errors.New("meltemi-tray: smoke: deadline exceeded, Run has not returned")
	d.noteErr(err)
	if d.smokeFailure != nil {
		d.smokeFailure(err)
	}
}

func (d *demo) startSmoke() smokeTimer {
	watchdog := d.afterFunc(traySmokeDeadline, d.smokeWatchdog)
	d.after(traySmokeStartDelay, d.smokeCloseStep)
	return watchdog
}

func (d *demo) runSmoke() error {
	watchdog := d.startSmoke()
	runErr := d.smokeApp.Run()
	d.markRunReturned()
	watchdog.Stop()
	d.noteErr(runErr)
	if !d.smokeStepsComplete && d.firstErr() == nil {
		d.noteErr(errors.New("meltemi-tray: smoke: Run returned before smoke steps completed"))
	}
	return d.firstErr()
}

func main() {
	smoke := flag.Bool("smoke", false, "CloseHide, activate Restore window through the native tray, verify restoration and quit")
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
