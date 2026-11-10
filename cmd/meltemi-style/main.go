// Command meltemi-style is the externally styled login demo: the same
// chained-API login window as meltemi-login — account/password pointer
// bindings, Input/Password fields, a login Button and a status Label
// inside a Gap(12).Padding(20) Column — but every color, border, radius,
// padding, size and font comes from examples/login.css through
// App.LoadStyles, not from Go code. The "SourceHan" family the sheet
// names is registered with App.RegisterFont from a TTF/OTF file (--font,
// defaulting to the system Source Han Sans); when that file is unreadable
// the built-in GoRegular data is registered under the same name so the
// demo still runs, with a warning that CJK glyphs will be missing. A
// successful login disables the button through the public
// Button.SetEnabled, so the sheet's #login:disabled rule — gray
// background, dull border and inert clicks — is part of the normal demo
// flow, both on screen and under --smoke. With --smoke a timer-driven
// script Posts to the UI thread, types through the public Input.SetValue,
// clicks the button through Button.Click, asserts the bindings, the
// status text and that a disabled button neither fires again nor rewrites
// the status, closes the window and relies on the default
// last-window-quit policy to end Run.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/ui"
)

const (
	windowTitle     = "登录"
	stylesheetPath  = "examples/login.css"
	fontFamily      = "SourceHan"
	defaultFontPath = "/usr/share/fonts/opentype/source-han-cjk/SourceHanSansSC-Regular.otf"
	smokeAccount    = "demo-用户"
	smokePassword   = "s3cr3t-密码"
	smokeStartDelay = 500 * time.Millisecond
	smokeStepDelay  = 150 * time.Millisecond
	smokeDeadline   = 15 * time.Second
)

type smokeRuntime interface {
	Post(func()) error
	Quit()
	Windows() []*ui.Window
	Run() error
}

type smokeTimer interface {
	Stop() bool
}

// demo owns the app, the bound account/password strings and the component
// handles the smoke script and the click handler touch. The component
// handles, the bound values and logins are confined to the UI thread:
// every reader and writer runs inside a function passed to App.Post. err
// is the first failure of any step; it is mutex-protected because a timer
// goroutine may need to record a Post failure.
type demo struct {
	app      *ui.App
	smokeApp smokeRuntime
	win      *ui.Window

	account  string
	password string
	logins   int

	accountInput  *ui.InputWidget
	passwordInput *ui.InputWidget
	loginBtn      *ui.ButtonWidget
	status        *ui.LabelWidget

	afterFunc          func(time.Duration, func()) smokeTimer
	smokeStepsComplete bool

	mu  sync.Mutex
	err error
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

// init builds the login window in the public chained shape: account and
// password pointers, placeholders, class-styled fields, an ID-styled
// OnClick button and a Gap(12).Padding(20) column, plus the status label
// reporting the click result. Posted before Run, so the window exists and
// drew its first frame before any timer fires.
func (d *demo) init() {
	d.accountInput = ui.Input(&d.account).Placeholder("账号").Class("field")
	d.passwordInput = ui.Password(&d.password).Placeholder("密码").Class("field")
	d.loginBtn = ui.Button("登录").ID("login").OnClick(d.login)
	d.status = ui.Label("").ID("status")
	d.win = d.app.Window(windowTitle).
		Size(360, 240).
		Content(
			ui.Column(
				d.accountInput,
				d.passwordInput,
				d.loginBtn,
				d.status,
			).ID("form").Gap(12).Padding(20),
		)
	d.win.Show()
}

// login is the button's click handler: it validates the bound values and
// reports the outcome without putting the password in the status label. A
// successful login counts itself and disables the button through the
// public SetEnabled, so the stylesheet's #login:disabled rule takes over
// and the button cannot fire a second time.
func (d *demo) login() {
	switch {
	case d.account == "":
		d.status.SetText("请输入账号")
	case d.password == "":
		d.status.SetText("请输入密码")
	default:
		d.logins++
		d.status.SetText(fmt.Sprintf("账号 %s 登录成功", d.account))
		d.loginBtn.SetEnabled(false)
	}
}

// after schedules the next smoke step: the timer goroutine only calls the
// goroutine-safe App.Post, so UI state stays on the UI thread. A Post
// failure is recorded and Quit keeps an unattended run from hanging.
func (d *demo) after(delay time.Duration, step func()) {
	d.afterFunc(delay, func() {
		if err := d.smokeApp.Post(step); err != nil {
			d.noteErr(fmt.Errorf("meltemi-style: smoke: post step: %w", err))
			d.smokeApp.Quit()
		}
	})
}

// abort ends the smoke run early when a step already failed: the first error
// is kept, Quit makes Run return, and main turns the recorded error into a
// non-zero exit.
func (d *demo) abort() {
	d.smokeApp.Quit()
}

// smokeTypeStep fills the two inputs through the public SetValue — the same
// path typed runes take — and clicks the button. It then asserts the bound
// values, the expected status text and that the status label omits the
// password. The successful click also disables the button, so the step
// asserts the disabled flag and that a further Click is inert: neither the
// login count nor the status text may change.
func (d *demo) smokeTypeStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.accountInput.SetValue(smokeAccount)
	d.passwordInput.SetValue(smokePassword)
	if d.account != smokeAccount || d.password != smokePassword {
		d.noteErr(fmt.Errorf("meltemi-style: smoke: bindings = %q/%q after SetValue, want %q/%q",
			d.account, d.password, smokeAccount, smokePassword))
		d.abort()
		return
	}
	d.loginBtn.Click()
	want := fmt.Sprintf("账号 %s 登录成功", smokeAccount)
	if got := d.status.Text(); got != want {
		d.noteErr(fmt.Errorf("meltemi-style: smoke: status label = %q after Click, want %q", got, want))
		d.abort()
		return
	}
	if strings.Contains(d.status.Text(), smokePassword) {
		d.noteErr(errors.New("meltemi-style: smoke: status label contains the password"))
		d.abort()
		return
	}
	if d.loginBtn.Enabled() {
		d.noteErr(errors.New("meltemi-style: smoke: login button still enabled after successful login"))
		d.abort()
		return
	}
	if d.logins != 1 {
		d.noteErr(fmt.Errorf("meltemi-style: smoke: login count = %d after first Click, want 1", d.logins))
		d.abort()
		return
	}
	d.loginBtn.Click()
	if d.logins != 1 {
		d.noteErr(fmt.Errorf("meltemi-style: smoke: disabled Click fired the handler: login count = %d, want 1", d.logins))
		d.abort()
		return
	}
	if got := d.status.Text(); got != want {
		d.noteErr(fmt.Errorf("meltemi-style: smoke: status label = %q after disabled Click, want it unchanged as %q", got, want))
		d.abort()
		return
	}
	d.after(smokeStepDelay, d.smokeCloseStep)
}

// smokeCloseStep closes the last window. The default LastWindowQuit policy
// then destroys the window, releases the shared face and quits Run — no Quit
// call here, so the smoke run also proves the default policy. If the policy
// never fires, the watchdog below records the hang and quits.
func (d *demo) smokeCloseStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.smokeStepsComplete = true
	d.noteErr(d.win.Close())
}

// smokeWatchdog bounds the whole run. Its callback runs on the UI thread, so
// the step flag and window set describe one point in the shutdown sequence.
// Once every step completed and the last window was removed, cleanup and the
// final drain may still accept this Post; that normal interleaving is not a
// timeout. Incomplete steps or a live window still record the deadline and
// quit. If Run already returned, Post is rejected and Run owns the outcome.
func (d *demo) smokeWatchdog() {
	_ = d.smokeApp.Post(func() {
		if d.smokeStepsComplete && len(d.smokeApp.Windows()) == 0 {
			return
		}
		d.noteErr(errors.New("meltemi-style: smoke: deadline exceeded, steps incomplete or windows still alive"))
		d.smokeApp.Quit()
	})
}

// startSmoke arms the watchdog and the first step before Run starts and
// returns the watchdog timer so runSmoke can stop it once Run returns.
func (d *demo) startSmoke() smokeTimer {
	watchdog := d.afterFunc(smokeDeadline, d.smokeWatchdog)
	d.afterFunc(smokeStartDelay, func() {
		if err := d.smokeApp.Post(d.smokeTypeStep); err != nil {
			d.noteErr(fmt.Errorf("meltemi-style: smoke: post first step: %w", err))
			d.smokeApp.Quit()
		}
	})
	return watchdog
}

func (d *demo) runSmoke() error {
	watchdog := d.startSmoke()
	runErr := d.smokeApp.Run()
	watchdog.Stop()
	return runErr
}

// registerSourceHan loads the --font data (defaulting to the system Source
// Han Sans) and registers it under the "SourceHan" family the stylesheet
// names. An unreadable file degrades to the built-in GoRegular data under
// the same name so LoadStyles validation still passes and the demo still
// runs — with a warning that CJK glyphs will be missing. Unusable data and
// a failed registration are hard errors.
func registerSourceHan(app *ui.App, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "meltemi-style: read font %s: %v; registering the built-in face as %q instead, CJK glyphs will be missing\n",
			path, err, fontFamily)
		data = goregular.TTF
	}
	if err := app.RegisterFont(fontFamily, data); err != nil {
		return err
	}
	return nil
}

func main() {
	smoke := flag.Bool("smoke", false, "drive the public API with a timer-posted script, then exit")
	font := flag.String("font", defaultFontPath, "path to TTF/OTF data registered as the CSS \"SourceHan\" family (e.g. Source Han Sans for Chinese); an unreadable file falls back to the built-in face with a warning")
	flag.Parse()

	app := ui.New()
	if err := registerSourceHan(app, *font); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := app.LoadStyles(stylesheetPath); err != nil {
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
	// A recorded smoke failure outranks the run error: abort cleanup can
	// fail during Quit and must not hide the earlier assertion that caused
	// it. The run error is still reported, as an additional error.
	if smokeErr := d.firstErr(); smokeErr != nil {
		fmt.Fprintln(os.Stderr, smokeErr)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "meltemi-style: additional run error: %v\n", runErr)
		}
		os.Exit(1)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
