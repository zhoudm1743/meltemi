// Command meltemi-text is the complex-text demo: one window of Chinese,
// ligatures, combining marks, emoji, Arabic and mixed-direction labels, plus
// an Input, a Password and a TextArea that all edit by grapheme cluster
// through the shared Face.Layout stack. Real Fcitx input works in every
// editor exactly as in the other demos: click an editor and type.
//
// --smoke is an opt-in, bounded validation: a timer-posted script drives the
// public ui API (cluster-safe selection and deletion, undo/redo, password
// masking underneath the real binding), asserts every intermediate state and
// closes the window; a watchdog judges hangs by whether Run has returned.
// It requires MELTEMI_GLFW_DESKTOP_TESTS=1 and an X11 DISPLAY, like the other
// demos' desktop smokes. Real IME/visual verification stays a separate
// manual step — the smoke never claims glyph-level proof.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zhoudm1743/meltemi/ui"
)

const (
	windowTitle = "Meltemi 复杂文本"

	defaultFontPath = "/usr/share/fonts/opentype/source-han-cjk/SourceHanSansSC-Regular.otf"

	statusReady = "就绪：点击任意输入框，Fcitx 可直接输入中文"
	// statusSmokeDone terminates the smoke script's status line.
	statusSmokeDone = "smoke 完成"

	smokeStartDelay = 500 * time.Millisecond
	smokeStepDelay  = 150 * time.Millisecond
	smokeDeadline   = 20 * time.Second

	uiWidth  = 720
	uiHeight = 640
)

// smokeText1 mixes CJK, Arabic, a combining cluster and emoji across three
// lines (18 runes; newlines at 5 and 14). The "e + combining acute" on line
// two is runes [12, 14) — one grapheme cluster and the smoke's deletion
// target. smokeTextDeleted is what remains after that cluster is replaced
// by Backspace.
const (
	smokeText1       = "中文abc\nمرحبا é\n🙂👍🏽"
	smokeTextDeleted = "中文abc\nمرحبا \n🙂👍🏽"
	smokeInputValue  = "é中🙂"
	// smokePasswordValue exercises a masked editor holding complex text;
	// Value() must round-trip the real text.
	smokePasswordValue = "sécret密码"
)

// smokeRuntime is the demo's event-loop seam: the real *ui.App satisfies it
// and smoke tests substitute a fake, so watchdog behavior is testable
// without a display.
type smokeRuntime interface {
	Post(func()) error
	Quit()
	Run() error
}

// smokeTimer is the subset of time.Timer the demo needs, so tests can hand
// out fake timers.
type smokeTimer interface {
	Stop() bool
}

// demo owns the app, the bound strings and the widget handles the smoke
// script and the window content touch. The component handles and the bound
// values are confined to the UI thread: every reader and writer runs inside
// a function passed to App.Post. err is the first failure of any step; it
// is mutex-protected because a timer goroutine may need to record a Post
// failure. runReturned is likewise mutex-protected: the watchdog bases its
// timeout decision on it instead of on the window list.
type demo struct {
	app      *ui.App
	smokeApp smokeRuntime
	win      *ui.Window

	input    *ui.InputWidget
	password *ui.InputWidget
	area     *ui.TextAreaWidget
	status   *ui.LabelWidget

	inputText    string
	passwordText string
	areaText     string

	afterFunc func(time.Duration, func()) smokeTimer

	smokeStepsComplete bool
	runReturned        bool

	mu  sync.Mutex
	err error
}

// newDemo wires the real app as the smoke runtime; tests replace both.
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

// buildWidgets creates the editors and the status label without touching
// the app, so the smoke script can run headless in tests.
func (d *demo) buildWidgets() {
	d.input = ui.Input(&d.inputText).Placeholder("单行输入：中文 / emoji / 阿拉伯文，Fcitx 直接输入")
	d.password = ui.Password(&d.passwordText).Placeholder("密码：真实文本按簇编辑，显示掩码")
	d.area = ui.TextArea(&d.areaText).Placeholder("多行文本：组合字符 e + ◌́、表情 👍🏽、 mixing مرحبا")
	d.status = ui.Label(statusReady)
}

// demoLabels are the feature rows: each line exercises one complex-text
// aspect through the shared label stack (no per-row code).
func demoLabels() []ui.Component {
	return []ui.Component{
		ui.Label("中文：风雨晦明之间，俯仰百变。"),
		ui.Label("连字：fi ffi fl — office vs office"),
		ui.Label("组合字符：café (e + U+0301)，x́y 与预组合 e"),
		ui.Label("Emoji: 🙂 👍🏽 👨‍👩‍👧 🇨🇳"),
		ui.Label("阿拉伯文：مرحبا بالعالم"),
		ui.Label("混合方向：abc مرحبا 123 def"),
	}
}

// init builds the window in the public chained shape. Posted before Run, so
// the window exists and drew its first frame before any smoke step fires.
func (d *demo) init() {
	d.buildWidgets()
	rows := []ui.Component{
		ui.Label("Meltemi 复杂文本：中文 · 连字 · 组合字符 · emoji · 阿拉伯文 · 混合方向"),
	}
	rows = append(rows, demoLabels()...)
	rows = append(rows,
		d.input,
		d.password,
		d.area,
		ui.Label("Fcitx：点击输入框，输入拼音后空格选字；预编辑以下划线显示，提交才进文本。"),
		d.status,
	)
	d.win = d.app.Window(windowTitle).
		Size(uiWidth, uiHeight).
		Content(ui.Column(rows...).Gap(10).Padding(20))
	d.win.Show()
}

// after schedules the next smoke step: the timer goroutine only calls the
// goroutine-safe App.Post, so UI state stays on the UI thread. A Post
// failure is recorded and Quit keeps an unattended run from hanging.
func (d *demo) after(delay time.Duration, step func()) {
	d.afterFunc(delay, func() {
		if err := d.smokeApp.Post(step); err != nil {
			d.noteErr(fmt.Errorf("meltemi-text: smoke: post step: %w", err))
			d.smokeApp.Quit()
		}
	})
}

// abort ends the smoke run early when a step already failed: the first
// error is kept and Quit makes Run return.
func (d *demo) abort() {
	d.smokeApp.Quit()
}

// fail records an assertion failure and ends the run; every caller returns
// right after, so the first error always wins.
func (d *demo) fail(err error) {
	d.noteErr(err)
	d.abort()
}

// requireArea asserts the editor value and its binding together, so a
// desynchronized TextArea is caught as readily as a stale binding.
func (d *demo) requireArea(want string) error {
	if d.areaText != want || d.area.Value() != want {
		return fmt.Errorf("meltemi-text: smoke: binding/value = %q/%q, want %q", d.areaText, d.area.Value(), want)
	}
	return nil
}

// smokeEditStep establishes the multiline mixed text and asserts the
// binding, the value and the untouched ready status.
func (d *demo) smokeEditStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.area.SetValue(smokeText1)
	if err := d.requireArea(smokeText1); err != nil {
		d.fail(fmt.Errorf("meltemi-text: smoke: after SetValue: %w", err))
		return
	}
	d.status.SetText(statusLine("已载入混合文本", d.areaText))
	d.after(smokeStepDelay, d.smokeClusterStep)
}

// smokeClusterStep selects the combining cluster "e + ◌́" (runes [12, 14))
// through the public SetSelection and replaces it with Backspace: the
// deletion must remove exactly that cluster. Undo restores value, selection
// and caret; Redo replays the deletion.
func (d *demo) smokeClusterStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.area.SetSelection(12, 14)
	if err := d.requireArea(smokeText1); err != nil {
		d.fail(fmt.Errorf("meltemi-text: smoke: after SetSelection: %w", err))
		return
	}
	d.area.Backspace()
	if err := d.requireArea(smokeTextDeleted); err != nil {
		d.fail(fmt.Errorf("meltemi-text: smoke: after cluster Backspace: %w", err))
		return
	}
	if start, end, ok := d.area.Selection(); start != 12 || end != 12 || ok {
		d.fail(fmt.Errorf("meltemi-text: smoke: selection after cluster Backspace = (%d, %d, %v), want collapsed (12, 12, false)", start, end, ok))
		return
	}
	d.area.Undo()
	if err := d.requireArea(smokeText1); err != nil {
		d.fail(fmt.Errorf("meltemi-text: smoke: after Undo: %w", err))
		return
	}
	if start, end, ok := d.area.Selection(); start != 12 || end != 14 || !ok {
		d.fail(fmt.Errorf("meltemi-text: smoke: selection after Undo = (%d, %d, %v), want restored (12, 14, true)", start, end, ok))
		return
	}
	if got := d.area.Cursor(); got != 14 {
		d.fail(fmt.Errorf("meltemi-text: smoke: cursor after Undo = %d, want 14", got))
		return
	}
	d.area.Redo()
	if err := d.requireArea(smokeTextDeleted); err != nil {
		d.fail(fmt.Errorf("meltemi-text: smoke: after Redo: %w", err))
		return
	}
	d.status.SetText(statusLine("簇删除 + 撤销/重做", d.areaText))
	d.after(smokeStepDelay, d.smokeInputStep)
}

// smokeInputStep walks the single-line editors through the public API: a
// complex value round-trips the binding, Undo/Redo restore it, and the
// password editor keeps the real text while still answering Undo.
func (d *demo) smokeInputStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.input.SetValue(smokeInputValue)
	if got := d.inputText; got != smokeInputValue {
		d.fail(fmt.Errorf("meltemi-text: smoke: input binding = %q, want %q", got, smokeInputValue))
		return
	}
	if !d.input.Undo() || d.input.Value() != "" {
		d.fail(fmt.Errorf("meltemi-text: smoke: input Undo = %q, want empty", d.input.Value()))
		return
	}
	if !d.input.Redo() || d.input.Value() != smokeInputValue {
		d.fail(fmt.Errorf("meltemi-text: smoke: input Redo = %q, want %q", d.input.Value(), smokeInputValue))
		return
	}
	d.password.SetValue(smokePasswordValue)
	if got := d.password.Value(); got != smokePasswordValue {
		d.fail(fmt.Errorf("meltemi-text: smoke: password Value() = %q, want the real text %q", got, smokePasswordValue))
		return
	}
	if got := d.passwordText; got != smokePasswordValue {
		d.fail(fmt.Errorf("meltemi-text: smoke: password binding = %q, want %q", got, smokePasswordValue))
		return
	}
	if !d.password.Undo() || d.password.Value() != "" {
		d.fail(fmt.Errorf("meltemi-text: smoke: password Undo = %q, want empty", d.password.Value()))
		return
	}
	d.status.SetText(statusSmokeDone)
	d.after(smokeStepDelay, d.smokeCloseStep)
}

// smokeCloseStep closes the last window; the default LastWindowQuit policy
// then destroys the window and quits Run. If the policy never fires, the
// watchdog below records the hang and quits.
func (d *demo) smokeCloseStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.smokeStepsComplete = true
	if d.win != nil {
		d.noteErr(d.win.Close())
	}
}

// smokeWatchdog bounds the whole run by whether Run has returned, not by
// the window list. The runReturned flag is set by runSmoke once Run is
// back, so a watchdog that fires during the final cleanup drain finds it
// set; a Post rejection means the loop is already gone and Run owns the
// outcome. In every other case the deadline records the first error and
// quits.
func (d *demo) smokeWatchdog() {
	if d.hasRunReturned() {
		return
	}
	if err := d.smokeApp.Post(func() {
		if d.hasRunReturned() {
			return
		}
		d.noteErr(errors.New("meltemi-text: smoke: deadline exceeded, Run has not returned"))
		d.smokeApp.Quit()
	}); err != nil {
		// Run already returned or is shutting down; nothing left to report.
	}
}

// startSmoke arms the watchdog and the first step before Run starts and
// returns the watchdog timer so runSmoke can stop it once Run is back.
func (d *demo) startSmoke() smokeTimer {
	watchdog := d.afterFunc(smokeDeadline, d.smokeWatchdog)
	d.afterFunc(smokeStartDelay, func() {
		if err := d.smokeApp.Post(d.smokeEditStep); err != nil {
			d.noteErr(fmt.Errorf("meltemi-text: smoke: post first step: %w", err))
			d.smokeApp.Quit()
		}
	})
	return watchdog
}

// runSmoke runs the smoke script under the watchdog, then records that Run
// returned and stops the watchdog timer, so a late watchdog fire is inert.
// A nil run error is only a success once every step finished: if Run
// returns early — the user closed the window before the script ended, or
// the loop drained on its own — with no recorded failure, the premature
// return is recorded as the first error so main exits non-zero instead of
// reporting a smoke pass that never ran.
func (d *demo) runSmoke() error {
	watchdog := d.startSmoke()
	runErr := d.smokeApp.Run()
	d.markRunReturned()
	watchdog.Stop()
	if runErr == nil && d.firstErr() == nil && !d.smokeStepsComplete {
		d.noteErr(errors.New("meltemi-text: smoke: Run returned before smoke steps completed"))
	}
	return runErr
}

// statusLine renders the status label content for an action and the current
// bound area text: the action outcome followed by live line and rune counts.
func statusLine(action, text string) string {
	return fmt.Sprintf("%s · 共 %d 行 · %d 字", action, strings.Count(text, "\n")+1, utf8.RuneCountInString(text))
}

// checkDesktop gates the real smoke on the same opt-in the other demos use.
func checkDesktop() error {
	if os.Getenv("MELTEMI_GLFW_DESKTOP_TESTS") != "1" && os.Getenv("WIND_GLFW_DESKTOP_TESTS") != "1" {
		return errors.New("--smoke requires MELTEMI_GLFW_DESKTOP_TESTS=1 and opens a real window")
	}
	if os.Getenv("DISPLAY") == "" {
		return errors.New("--smoke requires an X11 DISPLAY")
	}
	return nil
}

func main() {
	smoke := flag.Bool("smoke", false, "drive the public API with a timer-posted script, then exit")
	font := flag.String("font", "", "path to TTF/OTF data replacing the built-in GoRegular face (e.g. Source Han Sans for Chinese); empty keeps the default")
	flag.Parse()

	if *smoke {
		if err := checkDesktop(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	app := ui.New()
	fontPath := *font
	if fontPath == "" {
		// The CJK demo reads best with a CJK primary font; fall back to
		// the built-in face when the system path is absent (missing
		// runes still render through fontconfig fallback).
		if _, err := os.Stat(defaultFontPath); err == nil {
			fontPath = defaultFontPath
		}
	}
	if fontPath != "" {
		fontData, err := os.ReadFile(fontPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "meltemi-text: read font %s: %v\n", fontPath, err)
			os.Exit(1)
		}
		if err := app.RegisterFont(ui.DefaultFont, fontData); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
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
			fmt.Fprintf(os.Stderr, "meltemi-text: additional run error: %v\n", runErr)
		}
		os.Exit(1)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
