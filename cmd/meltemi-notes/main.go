// Command meltemi-notes is the multiline editing demo: one window composed from
// the public App/Window/Column/TextArea/Button/Label pieces. The editor binds
// a caller-owned string through a pointer, the Undo/Redo buttons drive the
// public TextArea history, and the status label reports the outcome together
// with live line and rune counts; the editor's OnError callback routes
// clipboard failures to the same label without touching the note. It runs
// unmodified on the built-in GoRegular
// face; pass --font with a TTF/OTF path (e.g. Source Han Sans) to replace the
// default face data so the Chinese placeholder, buttons and labels render as
// real glyphs. With --smoke a timer-driven script Posts to the UI thread, sets
// multiline text through the public TextArea.SetValue, builds a cross-line
// selection through the public TextArea.SetSelection, replaces it through
// TextArea.Backspace, walks the public Undo/Redo history, asserts the binding,
// the selection, the caret, the editor value and the status text, closes the
// window and relies on the default last-window-quit policy to end Run. The
// watchdog judges hangs by whether Run has returned — never by the window
// list — so a last window that was destroyed without triggering the
// last-window-quit policy is still reported and quit. No driver, renderer or
// internal type is touched here; everything goes through the public ui API.
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
	windowTitle = "笔记"
	placeholder = "输入笔记"
	undoText    = "撤销"
	redoText    = "重做"

	actionReady  = "就绪"
	actionUndo   = "已撤销"
	actionRedo   = "已重做"
	actionNoUndo = "没有可撤销的操作"
	actionNoRedo = "没有可重做的操作"
	// actionClipboardError prefixes a clipboard failure reported through the
	// editor's OnError callback: the note text stays untouched.
	actionClipboardError = "剪贴板错误"

	smokeStartDelay = 500 * time.Millisecond
	smokeStepDelay  = 150 * time.Millisecond
	smokeDeadline   = 15 * time.Second
)

// smokeNote1 mixes CJK, ASCII and an emoji across three lines (22 runes; the
// newlines sit at offsets 5 and 17); smokeNote2 extends it with a fourth line
// as the plain edit; smokeNoteSel is what remains after a cross-line selection
// of the first two lines [0, 18) is replaced by Backspace; smokeNote3
// restarts the content so a fresh edit can prove the redo branch is dropped.
var (
	smokeNote1   = "第一行笔记\nsecond line\n第三行🙂"
	smokeNote2   = smokeNote1 + "\n第四行"
	smokeNoteSel = "第三行🙂\n第四行"
	smokeNote3   = "重新开始\n的笔记"
)

// smokeRuntime is the demo's event-loop seam: the real *ui.App satisfies it
// and smoke tests substitute a fake, so watchdog behavior is testable without
// a display.
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

// demo owns the app, the bound notes string and the component handles the
// smoke script and the click handlers touch. The component handles and the
// bound value are confined to the UI thread: every reader and writer runs
// inside a function passed to App.Post. err is the first failure of any step;
// it is mutex-protected because a timer goroutine may need to record a Post
// failure. runReturned is likewise mutex-protected: the watchdog bases its
// timeout decision on it instead of on the window list.
type demo struct {
	app      *ui.App
	smokeApp smokeRuntime
	win      *ui.Window
	area     *ui.TextAreaWidget
	undo     *ui.ButtonWidget
	redo     *ui.ButtonWidget
	status   *ui.LabelWidget

	notes string

	afterFunc func(time.Duration, func()) smokeTimer

	smokeStepsComplete bool
	runReturned        bool

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

// statusLine renders the status label content for an action and the current
// bound notes: the action outcome followed by live line and rune counts.
func statusLine(action, notes string) string {
	return fmt.Sprintf("%s · 共 %d 行 · %d 字", action, strings.Count(notes, "\n")+1, utf8.RuneCountInString(notes))
}

// report refreshes the status label from an action and the live binding.
func (d *demo) report(action string) {
	d.status.SetText(statusLine(action, d.notes))
}

// reportError is the editor's OnError callback: a failing clipboard
// operation leaves the note untouched, so the status label names the
// failure and the live counts describe the unchanged note. It runs on the
// UI thread inside event handling, like every other handler here.
func (d *demo) reportError(err error) {
	d.status.SetText(fmt.Sprintf("%s · %v · 共 %d 行 · %d 字", actionClipboardError, err, strings.Count(d.notes, "\n")+1, utf8.RuneCountInString(d.notes)))
}

// init builds the notes window exactly in the public chained shape: a
// placeholder-bound TextArea whose OnError reports clipboard failures to the
// status label, the Undo/Redo buttons and the status label in a
// Gap(12).Padding(20) column. Posted before Run, so the window exists and drew
// its first frame before any smoke step fires.
func (d *demo) init() {
	d.area = ui.TextArea(&d.notes).Placeholder(placeholder).OnError(d.reportError)
	d.undo = ui.Button(undoText).OnClick(d.undoClick)
	d.redo = ui.Button(redoText).OnClick(d.redoClick)
	d.status = ui.Label(statusLine(actionReady, d.notes))
	d.win = d.app.Window(windowTitle).
		Size(420, 420).
		Content(
			ui.Column(
				d.area,
				d.undo,
				d.redo,
				d.status,
			).Gap(12).Padding(20),
		)
	d.win.Show()
}

// undoClick is the 撤销 button handler: it drives the public TextArea.Undo
// and reports whether history actually moved, so the status label reflects
// the real editing state rather than the bare click.
func (d *demo) undoClick() {
	if d.area.Undo() {
		d.report(actionUndo)
		return
	}
	d.report(actionNoUndo)
}

// redoClick is the 重做 button handler: the public TextArea.Redo mirror of
// undoClick.
func (d *demo) redoClick() {
	if d.area.Redo() {
		d.report(actionRedo)
		return
	}
	d.report(actionNoRedo)
}

// after schedules the next smoke step: the timer goroutine only calls the
// goroutine-safe App.Post, so UI state stays on the UI thread. A Post failure
// is recorded and Quit keeps an unattended run from hanging.
func (d *demo) after(delay time.Duration, step func()) {
	d.afterFunc(delay, func() {
		if err := d.smokeApp.Post(step); err != nil {
			d.noteErr(fmt.Errorf("meltemi-notes: smoke: post step: %w", err))
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

// requireNotes asserts the binding and the editor value together, so a
// desynchronized TextArea is caught as readily as a stale binding.
func (d *demo) requireNotes(want string) error {
	if d.notes != want || d.area.Value() != want {
		return fmt.Errorf("meltemi-notes: smoke: binding/value = %q/%q, want %q", d.notes, d.area.Value(), want)
	}
	return nil
}

// requireSelection asserts the public selection range and caret.
func (d *demo) requireSelection(what string, wantStart, wantEnd int, wantOK bool, wantCursor int) error {
	start, end, ok := d.area.Selection()
	if start != wantStart || end != wantEnd || ok != wantOK {
		return fmt.Errorf("meltemi-notes: smoke: %s: selection = (%d, %d, %v), want (%d, %d, %v)", what, start, end, ok, wantStart, wantEnd, wantOK)
	}
	if got := d.area.Cursor(); got != wantCursor {
		return fmt.Errorf("meltemi-notes: smoke: %s: cursor = %d, want %d", what, got, wantCursor)
	}
	return nil
}

// fail records an assertion failure and ends the run; every caller returns
// right after, so the first error always wins.
func (d *demo) fail(err error) {
	d.noteErr(err)
	d.abort()
}

// smokeEditStep sets the multiline note through the public SetValue and
// performs a second plain edit the same way: SetValue establishes the initial
// content a programmatic driver builds on, while the cross-line selection
// replacement is driven by SetSelection plus Backspace in smokeSelectionStep.
// It asserts binding and value after each edit plus the untouched ready
// status.
func (d *demo) smokeEditStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.area.SetValue(smokeNote1)
	if err := d.requireNotes(smokeNote1); err != nil {
		d.fail(err)
		return
	}
	if got, want := d.status.Text(), statusLine(actionReady, ""); got != want {
		d.fail(fmt.Errorf("meltemi-notes: smoke: status label = %q after edits, want %q", got, want))
		return
	}
	d.area.SetValue(smokeNote2)
	if err := d.requireNotes(smokeNote2); err != nil {
		d.fail(err)
		return
	}
	d.after(smokeStepDelay, d.smokeSelectionStep)
}

// smokeSelectionStep builds a non-empty cross-line selection over the first
// two lines through the public SetSelection, replaces it with a Backspace —
// the selection-replacement edit — and then undoes and redoes through the
// buttons, asserting the selection range, the caret, the binding and the
// status label at every point.
func (d *demo) smokeSelectionStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.area.SetSelection(0, 18)
	if err := d.requireNotes(smokeNote2); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after SetSelection: %w", err))
		return
	}
	if err := d.requireSelection("after SetSelection", 0, 18, true, 18); err != nil {
		d.fail(err)
		return
	}

	d.area.Backspace()
	if err := d.requireNotes(smokeNoteSel); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after selection Backspace: %w", err))
		return
	}
	if err := d.requireSelection("after selection Backspace", 0, 0, false, 0); err != nil {
		d.fail(err)
		return
	}

	d.undo.Click()
	if err := d.requireNotes(smokeNote2); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after undo of selection edit: %w", err))
		return
	}
	if err := d.requireSelection("after undo of selection edit", 0, 18, true, 18); err != nil {
		d.fail(err)
		return
	}
	if got, want := d.status.Text(), statusLine(actionUndo, smokeNote2); got != want {
		d.fail(fmt.Errorf("meltemi-notes: smoke: status label = %q after undo of selection edit, want %q", got, want))
		return
	}

	d.redo.Click()
	if err := d.requireNotes(smokeNoteSel); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after redo of selection edit: %w", err))
		return
	}
	if err := d.requireSelection("after redo of selection edit", 0, 0, false, 0); err != nil {
		d.fail(err)
		return
	}
	if got, want := d.status.Text(), statusLine(actionRedo, smokeNoteSel); got != want {
		d.fail(fmt.Errorf("meltemi-notes: smoke: status label = %q after redo of selection edit, want %q", got, want))
		return
	}
	d.after(smokeStepDelay, d.smokeUndoButtonStep)
}

// smokeUndoButtonStep clicks the Undo button and asserts the binding walked
// back to the second note and the status label reports the undo with live
// counts.
func (d *demo) smokeUndoButtonStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.undo.Click()
	if err := d.requireNotes(smokeNote2); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after undo click: %w", err))
		return
	}
	if got, want := d.status.Text(), statusLine(actionUndo, smokeNote2); got != want {
		d.fail(fmt.Errorf("meltemi-notes: smoke: status label = %q after undo click, want %q", got, want))
		return
	}
	d.after(smokeStepDelay, d.smokeRedoButtonStep)
}

// smokeRedoButtonStep clicks the Redo button and asserts the binding replays
// the selection edit and the status label reports the redo.
func (d *demo) smokeRedoButtonStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	d.redo.Click()
	if err := d.requireNotes(smokeNoteSel); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after redo click: %w", err))
		return
	}
	if got, want := d.status.Text(), statusLine(actionRedo, smokeNoteSel); got != want {
		d.fail(fmt.Errorf("meltemi-notes: smoke: status label = %q after redo click, want %q", got, want))
		return
	}
	d.after(smokeStepDelay, d.smokeHistoryStep)
}

// smokeHistoryStep walks the public TextArea.Undo/Redo methods directly and
// asserts every return value together with the bound text, the selection
// range and the caret each step restores. The selection edit sits in the
// middle of the stack: undoing it must bring the cross-line selection back,
// undoing the plain edits collapses the caret where SetValue parked it, and
// the redo side mirrors it.
func (d *demo) smokeHistoryStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	steps := []struct {
		redo      bool
		want      bool
		text      string
		selStart  int
		selEnd    int
		selOK     bool
		selCursor int
	}{
		{false, true, smokeNote2, 0, 18, true, 18}, // undo the selection edit: range restored
		{false, true, smokeNote1, 22, 22, false, 22},
		{false, true, "", 0, 0, false, 0},
		{false, false, "", 0, 0, false, 0},
		{true, true, smokeNote1, 22, 22, false, 22},
		{true, true, smokeNote2, 26, 26, false, 26},
		{true, true, smokeNoteSel, 0, 0, false, 0}, // redo the selection edit: caret at range start
		{true, false, smokeNoteSel, 0, 0, false, 0},
	}
	for i, step := range steps {
		var ok bool
		if step.redo {
			ok = d.area.Redo()
		} else {
			ok = d.area.Undo()
		}
		if ok != step.want {
			d.fail(fmt.Errorf("meltemi-notes: smoke: history step %d return = %v, want %v", i, ok, step.want))
			return
		}
		if err := d.requireNotes(step.text); err != nil {
			d.fail(fmt.Errorf("meltemi-notes: smoke: history step %d: %w", i, err))
			return
		}
		if err := d.requireSelection(fmt.Sprintf("history step %d", i), step.selStart, step.selEnd, step.selOK, step.selCursor); err != nil {
			d.fail(err)
			return
		}
	}
	d.after(smokeStepDelay, d.smokeBranchStep)
}

// smokeBranchStep proves a fresh edit drops the redo branch: undo once, edit
// through the public SetValue, and Redo must report false while the binding
// keeps the new text. The selection edit's unit is consumed by the re-edit,
// so the button walk that follows drains the two plain SetValue units: undo
// to the second note, the first note, the empty initial buffer, then
// exhaustion ("没有可撤销的操作"); redo replays the first note, the second
// note and the branched note before its own exhaustion ("没有可重做的操作").
func (d *demo) smokeBranchStep() {
	if d.firstErr() != nil {
		d.abort()
		return
	}
	if !d.area.Undo() {
		d.fail(errors.New("meltemi-notes: smoke: Undo before re-edit returned false, want true"))
		return
	}
	if err := d.requireNotes(smokeNote2); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after undo before re-edit: %w", err))
		return
	}
	d.area.SetValue(smokeNote3)
	if err := d.requireNotes(smokeNote3); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after re-edit: %w", err))
		return
	}
	if d.area.Redo() {
		d.fail(errors.New("meltemi-notes: smoke: Redo after a new edit returned true, want false (redo branch must be dropped)"))
		return
	}

	d.undo.Click()
	if err := d.requireNotes(smokeNote2); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after undo of the re-edit: %w", err))
		return
	}
	d.undo.Click()
	if err := d.requireNotes(smokeNote1); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after undo to the first note: %w", err))
		return
	}
	d.undo.Click()
	if err := d.requireNotes(""); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after undo to the empty initial buffer: %w", err))
		return
	}
	d.undo.Click()
	if err := d.requireNotes(""); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after undo exhaustion: %w", err))
		return
	}
	if got, want := d.status.Text(), statusLine(actionNoUndo, ""); got != want {
		d.fail(fmt.Errorf("meltemi-notes: smoke: status label = %q after undo exhaustion, want %q", got, want))
		return
	}
	d.redo.Click()
	if err := d.requireNotes(smokeNote1); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after redo to the first note: %w", err))
		return
	}
	d.redo.Click()
	if err := d.requireNotes(smokeNote2); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after redo to the second note: %w", err))
		return
	}
	d.redo.Click()
	if err := d.requireNotes(smokeNote3); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after redo to the branched note: %w", err))
		return
	}
	d.redo.Click()
	if err := d.requireNotes(smokeNote3); err != nil {
		d.fail(fmt.Errorf("meltemi-notes: smoke: after redo exhaustion: %w", err))
		return
	}
	if got, want := d.status.Text(), statusLine(actionNoRedo, smokeNote3); got != want {
		d.fail(fmt.Errorf("meltemi-notes: smoke: status label = %q after redo exhaustion, want %q", got, want))
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

// smokeWatchdog bounds the whole run by whether Run has returned, not by the
// window list: a destroyed last window whose policy failed to quit leaves
// Windows() empty while Run waits forever, and that state must be caught.
// The runReturned flag is set by runSmoke once Run is back, so a watchdog
// that fires during the final cleanup drain finds it set; a Post rejection
// means the loop is already gone and Run owns the outcome. In every other
// case the deadline records the first error and quits.
func (d *demo) smokeWatchdog() {
	if d.hasRunReturned() {
		return
	}
	if err := d.smokeApp.Post(func() {
		if d.hasRunReturned() {
			return
		}
		d.noteErr(errors.New("meltemi-notes: smoke: deadline exceeded, Run has not returned"))
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
			d.noteErr(fmt.Errorf("meltemi-notes: smoke: post first step: %w", err))
			d.smokeApp.Quit()
		}
	})
	return watchdog
}

// runSmoke runs the smoke script under the watchdog, then records that Run
// returned and stops the watchdog timer, so a late watchdog fire is inert. A
// nil run error is only a success once every step finished: if Run returns
// early — the user closed the window before the script ended, or the loop
// drained on its own — with no recorded failure, the premature return is
// recorded as the first error so main exits non-zero instead of reporting a
// smoke pass that never ran.
func (d *demo) runSmoke() error {
	watchdog := d.startSmoke()
	runErr := d.smokeApp.Run()
	d.markRunReturned()
	watchdog.Stop()
	if runErr == nil && d.firstErr() == nil && !d.smokeStepsComplete {
		d.noteErr(errors.New("meltemi-notes: smoke: Run returned before smoke steps completed"))
	}
	return runErr
}

func main() {
	smoke := flag.Bool("smoke", false, "drive the public API with a timer-posted script, then exit")
	font := flag.String("font", "", "path to TTF/OTF data replacing the built-in GoRegular face (e.g. Source Han Sans for Chinese); empty keeps the default")
	flag.Parse()

	app := ui.New()
	if *font != "" {
		fontData, err := os.ReadFile(*font)
		if err != nil {
			fmt.Fprintf(os.Stderr, "meltemi-notes: read font %s: %v\n", *font, err)
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
			fmt.Fprintf(os.Stderr, "meltemi-notes: additional run error: %v\n", runErr)
		}
		os.Exit(1)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}
