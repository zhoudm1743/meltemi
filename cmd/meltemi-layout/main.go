// Command meltemi-layout combines Meltemi's stage-one layouts and controls in one
// native window. The stylesheet demonstrates nested and disabled opacity; the
// optional smoke mode uses timer-posted public API calls and then closes the
// window, so no driver, renderer, GLFW or OpenGL type crosses the public API.
package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zhoudm1743/meltemi/ui"
)

const (
	windowTitle     = "Meltemi Layout and Opacity"
	smokeStartDelay = 500 * time.Millisecond
	smokeStepDelay  = 150 * time.Millisecond
	smokeDeadline   = 10 * time.Second
)

const demoStyles = `
#content { opacity: 0.8; }
#title { font-size: 22px; color: #203040; }
Checkbox:disabled { opacity: 0.35; }
#art {
	opacity: 0.75;
	width: 48px;
	height: 48px;
	padding: 4px;
	background: #e8eef8;
	border: 1px solid #607898;
	border-radius: 6px;
}
#stack-back {
	width: 280px;
	height: 44px;
	background: #dce8f8;
	border-radius: 6px;
}
#stack-front { padding: 12px; color: #203858; }
#scroll { padding: 4px; opacity: 0.9; }
.scroll-item { padding: 2px 6px; color: #304860; }
`

type demo struct {
	app *ui.App
	win *ui.Window

	root     *ui.ColumnLayout
	row      *ui.RowLayout
	stack    *ui.StackLayout
	scroll   *ui.ScrollLayout
	spacer   *ui.SpacerWidget
	enabled  *ui.CheckboxWidget
	disabled *ui.CheckboxWidget
	image    *ui.ImageWidget

	enabledBound  bool
	disabledBound bool

	smokeStepsComplete bool
	runReturned        atomic.Bool
	mu                 sync.Mutex
	err                error
}

func newDemo(app *ui.App) *demo {
	d := &demo{app: app, enabledBound: true, disabledBound: true}
	d.enabled = ui.Checkbox("Enabled", &d.enabledBound)
	d.disabled = ui.Checkbox("Disabled opacity", &d.disabledBound)
	d.disabled.SetEnabled(false)
	d.image = ui.Image(demoImage()).ID("art")
	d.spacer = ui.Spacer()
	d.row = ui.Row(d.enabled, d.disabled, d.spacer, d.image).Gap(12)
	d.stack = ui.Stack(
		ui.Label("").ID("stack-back"),
		ui.Label("Stack: overlapping children").ID("stack-front"),
	)

	items := make([]ui.Component, 0, 12)
	for i := 1; i <= 12; i++ {
		items = append(items, ui.Label(fmt.Sprintf("Scroll item %02d", i)).Class("scroll-item"))
	}
	d.scroll = ui.Scroll(ui.Column(items...).Gap(4)).ID("scroll")
	d.root = ui.Column(
		ui.Label("Meltemi layout and opacity").ID("title"),
		d.row,
		d.stack,
		d.scroll,
		ui.Spacer(),
	).Gap(12).Padding(16).ID("content")
	return d
}

func demoImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 40, G: 70, B: 110, A: 255})
		}
	}
	for x, pixel := range []color.NRGBA{
		{R: 255, A: 255},
		{G: 255, A: 255},
		{B: 255, A: 255},
		{R: 255, G: 255, B: 255, A: 128},
	} {
		img.SetNRGBA(x, 0, pixel)
	}
	return img
}

func loadDemoStyles(app *ui.App) error {
	file, err := os.CreateTemp("", "meltemi-layout-*.css")
	if err != nil {
		return fmt.Errorf("meltemi-layout: create stylesheet: %w", err)
	}
	path := file.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := file.WriteString(demoStyles); err != nil {
		_ = file.Close()
		return fmt.Errorf("meltemi-layout: write stylesheet: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("meltemi-layout: close stylesheet: %w", err)
	}
	return app.LoadStyles(path)
}

func (d *demo) init() {
	d.win = d.app.Window(windowTitle).Size(640, 360).Content(d.root)
	d.win.Show()
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

func (d *demo) after(delay time.Duration, step func()) {
	time.AfterFunc(delay, func() {
		if err := d.app.Post(step); err != nil {
			d.noteErr(fmt.Errorf("meltemi-layout: smoke: post step: %w", err))
		}
	})
}

func (d *demo) fail(err error) {
	d.noteErr(err)
	d.app.Quit()
}

func (d *demo) smokeScrollStep() {
	if d.firstErr() != nil {
		d.app.Quit()
		return
	}
	d.enabled.SetValue(false)
	if d.enabledBound || d.enabled.Value() {
		d.fail(errors.New("meltemi-layout: smoke: enabled checkbox did not update its binding"))
		return
	}
	d.scroll.SetOffset(0, 40)
	if x, y := d.scroll.Offset(); x != 0 || y <= 0 {
		d.fail(fmt.Errorf("meltemi-layout: smoke: scroll offset = (%v, %v), want (0, positive)", x, y))
		return
	}
	d.after(smokeStepDelay, d.smokeCloseStep)
}

func (d *demo) smokeCloseStep() {
	if d.firstErr() != nil {
		d.app.Quit()
		return
	}
	d.smokeStepsComplete = true
	d.noteErr(d.win.Close())
}

func (d *demo) smokeWatchdog() {
	if d.runReturned.Load() {
		return
	}
	if err := d.app.Post(func() {
		if d.runReturned.Load() {
			return
		}
		d.noteErr(errors.New("meltemi-layout: smoke: deadline exceeded, Run has not returned"))
		d.app.Quit()
	}); err != nil {
		// Run has already returned or is shutting down, so it owns the outcome.
	}
}

func (d *demo) runSmoke() error {
	watchdog := time.AfterFunc(smokeDeadline, d.smokeWatchdog)
	time.AfterFunc(smokeStartDelay, func() {
		if err := d.app.Post(d.smokeScrollStep); err != nil {
			d.noteErr(fmt.Errorf("meltemi-layout: smoke: post first step: %w", err))
		}
	})
	runErr := d.app.Run()
	d.runReturned.Store(true)
	watchdog.Stop()
	if runErr == nil && d.firstErr() == nil && !d.smokeStepsComplete {
		d.noteErr(errors.New("meltemi-layout: smoke: Run returned before smoke steps completed"))
	}
	return runErr
}

func main() {
	smoke := flag.Bool("smoke", false, "exercise the checkbox and scroll through posted public API calls, then exit")
	flag.Parse()

	app := ui.New()
	if err := loadDemoStyles(app); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	d := newDemo(app)
	if err := app.Post(d.init); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var runErr error
	if *smoke {
		runErr = d.runSmoke()
	} else {
		runErr = app.Run()
	}
	if smokeErr := d.firstErr(); smokeErr != nil {
		fmt.Fprintln(os.Stderr, smokeErr)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "meltemi-layout: additional run error: %v\n", runErr)
		}
		os.Exit(1)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
