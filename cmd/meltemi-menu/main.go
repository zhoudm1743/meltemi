// Command meltemi-menu opens two independent windows with self-drawn menu bars,
// shortcuts, disabled and checked items, separators, and nested submenus. With
// --smoke it uses the public API to replace one window's menus and the other's
// content, updates model state, closes both windows, and quits under a watchdog.
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
	smokeStartDelay = 400 * time.Millisecond
	smokeStepDelay  = 150 * time.Millisecond
	smokeDeadline   = 15 * time.Second
)

type smokeRuntime interface {
	Post(func()) error
	Quit()
	Run() error
}

type smokeTimer interface{ Stop() bool }

type demo struct {
	app      *ui.App
	smokeApp smokeRuntime
	first    *ui.Window
	second   *ui.Window

	firstStatus  *ui.LabelWidget
	secondStatus *ui.LabelWidget
	initial      []*ui.MenuModel
	secondMenus  []*ui.MenuModel
	replacement  []*ui.MenuModel

	firstChecked       *ui.MenuItemModel
	disabled           *ui.MenuItemModel
	replacementChecked *ui.MenuItemModel

	afterFunc func(time.Duration, func()) smokeTimer

	smokeStepsComplete bool
	runReturned        bool
	mu                 sync.Mutex
	err                error
}

func newDemo(app *ui.App) *demo {
	return &demo{
		app:      app,
		smokeApp: app,
		afterFunc: func(delay time.Duration, fn func()) smokeTimer {
			return time.AfterFunc(delay, fn)
		},
	}
}

func (d *demo) initModels() {
	d.firstStatus = ui.Label("First: choose a menu or press Ctrl+W")
	d.secondStatus = ui.Label("Second: independent menus")
	d.firstChecked = ui.MenuItem("Word wrap").SetChecked(true).Shortcut(ui.ModCtrl, 'W').OnClick(func() {
		d.firstStatus.SetText(fmt.Sprintf("First: word wrap = %v", d.firstChecked.Checked()))
	})
	d.disabled = ui.MenuItem("Disabled action")
	d.initial = []*ui.MenuModel{
		ui.Menu("File",
			ui.MenuItem("New").Shortcut(ui.ModCtrl, 'N').OnClick(func() {
				d.firstStatus.SetText("First: New activated")
			}),
			d.disabled,
			ui.MenuSeparator(),
			d.firstChecked,
			ui.MenuItem("Recent").Submenu(
				ui.MenuItem("Document").OnClick(func() { d.firstStatus.SetText("First: Document activated") }),
				ui.MenuItem("More").Submenu(
					ui.MenuItem("Deep leaf").OnClick(func() { d.firstStatus.SetText("First: Deep leaf activated") }),
				),
			),
		),
		ui.Menu("Edit", ui.MenuItem("Preferences").OnClick(func() {
			d.firstStatus.SetText("First: Preferences activated")
		})),
	}
	d.secondMenus = []*ui.MenuModel{
		ui.Menu("Second",
			ui.MenuItem("Independent action").Shortcut(ui.ModCtrl, 'B').OnClick(func() {
				d.secondStatus.SetText("Second: independent action activated")
			}),
			ui.MenuItem("Enabled later").SetEnabled(false),
		),
	}
}

func (d *demo) init() {
	d.initModels()
	d.first = d.app.Window("Meltemi menus — first").
		Size(520, 320).
		Content(ui.Column(
			ui.Label("First window: File > Recent opens a nested submenu"),
			d.firstStatus,
		).Gap(12).Padding(20)).
		MenuBar(d.initial...)
	d.second = d.app.Window("Meltemi menus — second").
		Size(460, 280).
		Content(ui.Column(
			ui.Label("Second window: shortcuts and overlays stay independent"),
			d.secondStatus,
		).Gap(12).Padding(20)).
		MenuBar(d.secondMenus...)
	d.first.Show()
	d.second.Show()
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
			d.noteErr(fmt.Errorf("meltemi-menu: smoke: post step: %w", err))
			d.smokeApp.Quit()
		}
	})
}

func (d *demo) abortSmoke(err error) {
	d.noteErr(err)
	d.smokeApp.Quit()
}

func (d *demo) smokeReplaceStep() {
	if d.firstStatus == nil || d.secondStatus == nil || d.firstChecked == nil || d.disabled == nil {
		d.abortSmoke(errors.New("meltemi-menu: smoke: demo models were not initialized"))
		return
	}
	d.firstChecked.SetChecked(!d.firstChecked.Checked())
	d.disabled.SetEnabled(false)
	d.replacementChecked = ui.MenuItem("Checked tool").Shortcut(ui.ModCtrl, 'T').OnClick(func() {
		d.firstStatus.SetText(fmt.Sprintf("First: checked tool = %v", d.replacementChecked.Checked()))
	})
	d.replacement = []*ui.MenuModel{
		ui.Menu("Tools",
			d.replacementChecked,
			ui.MenuSeparator(),
			ui.MenuItem("Disabled tool").SetEnabled(false),
			ui.MenuItem("More tools").Submenu(
				ui.MenuItem("Nested tool").OnClick(func() { d.firstStatus.SetText("First: nested tool activated") }),
			),
		),
	}
	if d.first != nil {
		d.first.MenuBar(d.replacement...)
	}
	if d.second != nil {
		d.second.Content(ui.Column(
			ui.Label("Content replaced; the second window's menu model is unchanged"),
			d.secondStatus,
		).Gap(12).Padding(20))
	}
	d.secondStatus.SetText("Second: content replaced, menus preserved")
	d.after(smokeStepDelay, d.smokeFinishStep)
}

func (d *demo) smokeFinishStep() {
	if len(d.replacement) != 1 || d.replacement[0].Title != "Tools" || d.replacementChecked == nil {
		d.abortSmoke(errors.New("meltemi-menu: smoke: replacement menu model is incomplete"))
		return
	}
	d.replacementChecked.SetChecked(true)
	d.firstStatus.SetText("First: replacement installed")
	d.smokeStepsComplete = true
	if d.first != nil {
		d.noteErr(d.first.Close())
	}
	if d.second != nil {
		d.noteErr(d.second.Close())
	}
	d.smokeApp.Quit()
}

func (d *demo) smokeWatchdog() {
	if d.hasRunReturned() {
		return
	}
	if err := d.smokeApp.Post(func() {
		if d.hasRunReturned() {
			return
		}
		d.noteErr(errors.New("meltemi-menu: smoke: deadline exceeded, Run has not returned"))
		d.smokeApp.Quit()
	}); err != nil {
		return
	}
}

func (d *demo) startSmoke() smokeTimer {
	watchdog := d.afterFunc(smokeDeadline, d.smokeWatchdog)
	d.after(smokeStartDelay, d.smokeReplaceStep)
	return watchdog
}

func (d *demo) runSmoke() error {
	watchdog := d.startSmoke()
	runErr := d.smokeApp.Run()
	d.markRunReturned()
	watchdog.Stop()
	d.noteErr(runErr)
	if !d.smokeStepsComplete && d.firstErr() == nil {
		d.noteErr(errors.New("meltemi-menu: smoke: Run returned before smoke steps completed"))
	}
	return d.firstErr()
}

func main() {
	smoke := flag.Bool("smoke", false, "replace menus and content through the public API, close both windows and quit")
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
