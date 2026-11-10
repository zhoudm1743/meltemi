// Command meltemi-tray demonstrates a native StatusNotifierItem menu and restoring
// an existing CloseHide window. Native tray menus are rendered by the desktop;
// the window MenuBar CSS does not style them. With --smoke it uses real D-Bus
// calls to activate Restore window and exits under a watchdog.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/zhoudm1743/meltemi/ui"
)

const (
	traySmokeStartDelay = 400 * time.Millisecond
	traySmokeStepDelay  = 200 * time.Millisecond
	traySmokeDeadline   = 15 * time.Second
	trayDBusTimeout     = 3 * time.Second
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
	d.activateRestore = activateRestoreOverDBus
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

type nativeMenuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

type nativeMenuItemProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}

func verifyMenuOnlySNI(ctx context.Context, conn *dbus.Conn, destination string) error {
	object := conn.Object(destination, dbus.ObjectPath("/StatusNotifierItem"))
	var document string
	if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&document); err != nil {
		return fmt.Errorf("SNI Introspect: %w", err)
	}
	for _, iface := range []string{"org.freedesktop.StatusNotifierItem", "org.kde.StatusNotifierItem"} {
		var itemIsMenu dbus.Variant
		if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, iface, "ItemIsMenu").Store(&itemIsMenu); err != nil {
			return fmt.Errorf("%s Get ItemIsMenu: %w", iface, err)
		}
		if itemIsMenu.Value() != true {
			return fmt.Errorf("%s Get ItemIsMenu = %v, want true", iface, itemIsMenu.Value())
		}
		var properties map[string]dbus.Variant
		if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, iface).Store(&properties); err != nil {
			return fmt.Errorf("%s GetAll: %w", iface, err)
		}
		if properties["ItemIsMenu"].Value() != true {
			return fmt.Errorf("%s GetAll ItemIsMenu = %v, want true", iface, properties["ItemIsMenu"].Value())
		}
		start := strings.Index(document, `<interface name="`+iface+`">`)
		if start < 0 {
			return fmt.Errorf("SNI introspection omits %s", iface)
		}
		block := document[start:]
		end := strings.Index(block, "</interface>")
		if end < 0 {
			return fmt.Errorf("SNI introspection has an unterminated %s interface", iface)
		}
		block = block[:end]
		if !strings.Contains(block, `<property name="ItemIsMenu" type="b" access="read"/>`) {
			return fmt.Errorf("%s introspection does not declare ItemIsMenu as a readable bool", iface)
		}
	}
	return nil
}

func activateRestoreOverDBus() error {
	ctx, cancel := context.WithTimeout(context.Background(), trayDBusTimeout)
	defer cancel()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect session bus: %w", err)
	}
	defer conn.Close()
	var names []string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return fmt.Errorf("ListNames: %w", err)
	}
	prefix := fmt.Sprintf("org.freedesktop.StatusNotifierItem-%d-", os.Getpid())
	destination := ""
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			if destination != "" {
				return fmt.Errorf("multiple demo SNI names match %q", prefix)
			}
			destination = name
		}
	}
	if destination == "" {
		return fmt.Errorf("no registered SNI name matches %q", prefix)
	}
	if err := verifyMenuOnlySNI(ctx, conn, destination); err != nil {
		return err
	}
	object := conn.Object(destination, dbus.ObjectPath("/MenuBar"))
	var revision uint32
	var layout nativeMenuLayout
	if err := object.CallWithContext(ctx, "com.canonical.dbusmenu.GetLayout", 0, int32(0), int32(0), []string{}).Store(&revision, &layout); err != nil {
		return fmt.Errorf("GetLayout: %w", err)
	}
	if revision == 0 || layout.ID != 0 {
		return fmt.Errorf("GetLayout returned revision %d root %d, want nonzero/0", revision, layout.ID)
	}
	var properties []nativeMenuItemProperties
	if err := object.CallWithContext(ctx, "com.canonical.dbusmenu.GetGroupProperties", 0, []int32{1}, []string{"label", "enabled"}).Store(&properties); err != nil {
		return fmt.Errorf("GetGroupProperties: %w", err)
	}
	if len(properties) != 1 || properties[0].ID != 1 || properties[0].Properties["label"].Value() != "Restore window" || properties[0].Properties["enabled"].Value() != true {
		return fmt.Errorf("restore item properties = %+v, want enabled Restore window at stable ID 1", properties)
	}
	if err := object.CallWithContext(ctx, "com.canonical.dbusmenu.Event", 0, int32(1), "clicked", dbus.MakeVariant(uint32(0)), uint32(1)).Err; err != nil {
		return fmt.Errorf("Event clicked: %w", err)
	}
	return nil
}

func main() {
	smoke := flag.Bool("smoke", false, "CloseHide, activate Restore window over real D-Bus, verify restoration and quit")
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
