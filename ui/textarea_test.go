package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

func arrangedTextArea(t *testing.T, area *TextAreaWidget, width, height float64) *eventRouter {
	t.Helper()
	setTreeFace(area, testFace(t))
	if width == 0 {
		width = 320
	}
	if height == 0 {
		height = 160
	}
	area.arrange(rect(0, 0, width, height))
	return newEventRouter(area)
}

func requireTextAreaSelection(t *testing.T, area *TextAreaWidget, wantStart, wantEnd int) {
	t.Helper()
	start, end, ok := area.buffer.Selection()
	if !ok || start != wantStart || end != wantEnd {
		t.Fatalf("selection = (%d, %d, %v), want (%d, %d, true)", start, end, ok, wantStart, wantEnd)
	}
}

func requireNoTextAreaSelection(t *testing.T, area *TextAreaWidget, wantCursor int) {
	t.Helper()
	start, end, ok := area.buffer.Selection()
	if ok || start != wantCursor || end != wantCursor {
		t.Fatalf("selection = (%d, %d, %v), want collapsed cursor %d", start, end, ok, wantCursor)
	}
}

func TestTextAreaEditingBindingAndHistory(t *testing.T) {
	bound := "甲\nbc"
	area := TextArea(&bound)
	if got := area.Placeholder("说明"); got != area {
		t.Fatal("Placeholder did not return the same TextAreaWidget")
	}
	if got := area.measure(Constraints{}); got != (Size{Width: 320, Height: 160}) {
		t.Fatalf("natural size = %v, want 320x160", got)
	}
	column := Column(area)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, 480, laid.Height))
	if area.bounds.Width != 480 || area.bounds.Height != 160 {
		t.Fatalf("Column arranged TextArea bounds = %+v, want 480x160", area.bounds)
	}
	router := newEventRouter(column)
	router.Handle(keyEvent(driver.KeyTab, 0))

	router.Handle(runeInputEvent('好'))
	if got := area.Value(); got != "甲\nbc好" {
		t.Fatalf("Value() after typing = %q, want 甲\\nbc好", got)
	}
	if handled, redraw := router.Handle(keyEvent(driver.KeyEnter, 0)); !handled || !redraw {
		t.Fatalf("Enter = (%v, %v), want (true, true)", handled, redraw)
	}
	router.Handle(runeInputEvent('二'))
	if got := area.Value(); got != "甲\nbc好\n二" || bound != got {
		t.Fatalf("after Enter and typing: value=%q bound=%q", got, bound)
	}
	requireNoTextAreaSelection(t, area, 7)

	router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl))
	if got := area.Value(); got != "甲\nbc好\n" || bound != got {
		t.Fatalf("after Ctrl+Z: value=%q bound=%q, want typed rune undone", got, bound)
	}
	router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl|driver.ModShift))
	if got := area.Value(); got != "甲\nbc好\n二" || bound != got {
		t.Fatalf("after Ctrl+Shift+Z: value=%q bound=%q, want typed rune redone", got, bound)
	}
	if !area.Undo() || area.Value() != "甲\nbc好\n" || bound != area.Value() {
		t.Fatalf("public Undo did not synchronize value and binding: %q / %q", area.Value(), bound)
	}
	if !area.Redo() || area.Value() != "甲\nbc好\n二" || bound != area.Value() {
		t.Fatalf("public Redo did not synchronize value and binding: %q / %q", area.Value(), bound)
	}
	router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl))
	router.Handle(keyEvent(driver.KeyY, driver.ModCtrl))
	if got := area.Value(); got != "甲\nbc好\n二" {
		t.Fatalf("Value() after Ctrl+Y = %q, want redo", got)
	}

	// Newline is its own history unit, separate from adjacent plain typing.
	router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl)) // 二
	router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl)) // newline
	if got := area.Value(); got != "甲\nbc好" {
		t.Fatalf("Value() after undoing newline = %q, want 甲\\nbc好", got)
	}
	router.Handle(keyEvent(driver.KeyY, driver.ModCtrl))
	if got := area.Value(); got != "甲\nbc好\n" {
		t.Fatalf("Value() after redoing newline = %q", got)
	}

	area.SetValue("重\n置🙂")
	if got := area.Value(); got != "重\n置🙂" || bound != got {
		t.Fatalf("SetValue: value=%q bound=%q", got, bound)
	}
	requireNoTextAreaSelection(t, area, 4)
	if !area.Undo() || area.Value() != "甲\nbc好\n" || bound != area.Value() {
		t.Fatalf("Undo after SetValue did not restore the prior state: %q / %q", area.Value(), bound)
	}
}

func TestTextAreaVerticalNavigationAndSelection(t *testing.T) {
	area := TextArea(nil)
	area.SetValue("abcd\n中🙂\nx")
	router := arrangedTextArea(t, area, 0, 0)
	router.Handle(keyEvent(driver.KeyTab, 0))
	area.buffer.StartSelection(1)

	router.Handle(keyEvent(driver.KeyDown, 0))
	requireNoTextAreaSelection(t, area, 6)
	router.Handle(keyEvent(driver.KeyDown, 0))
	requireNoTextAreaSelection(t, area, 9)
	router.Handle(keyEvent(driver.KeyUp, 0))
	requireNoTextAreaSelection(t, area, 6)
	router.Handle(keyEvent(driver.KeyHome, 0))
	requireNoTextAreaSelection(t, area, 5)
	router.Handle(keyEvent(driver.KeyEnd, 0))
	requireNoTextAreaSelection(t, area, 7)
	router.Handle(keyEvent(driver.KeyUp, driver.ModShift))
	requireTextAreaSelection(t, area, 2, 7)
	if got := string(area.buffer.runes[2:7]); got != "cd\n中🙂" {
		t.Fatalf("cross-line selection = %q, want cd\\n中🙂", got)
	}

	// A captured drag selects across lines, clamps outside the widget and
	// releases without moving focus or changing the bound text.
	area.SetValue("ab\ncd")
	press := driver.Point{X: area.contentRect().X, Y: area.contentRect().Y + 1}
	router.Handle(leftPress(press))
	if !area.dragging || router.focus != area {
		t.Fatalf("press did not focus and arm TextArea: dragging=%v focus=%p", area.dragging, router.focus)
	}
	outside := driver.Point{X: area.bounds.X + area.bounds.Width + 30, Y: area.bounds.Y + area.bounds.Height + area.lineHeight()}
	if handled, _ := router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: outside}); !handled {
		t.Fatal("captured cross-line drag was not handled")
	}
	if handled, _ := router.Handle(leftRelease(outside)); !handled {
		t.Fatal("captured outside release was not handled")
	}
	requireTextAreaSelection(t, area, 0, 5)
	if area.dragging || router.focus != area || !area.focused {
		t.Fatalf("outside release lost gesture/focus state: dragging=%v focus=%p focused=%v", area.dragging, router.focus, area.focused)
	}
	if got := area.Value(); got != "ab\ncd" {
		t.Fatalf("drag changed value to %q", got)
	}
}

func TestTextAreaScrollingAndOrigin(t *testing.T) {
	area := TextArea(nil)
	setTreeFace(area, testFace(t))
	area.arrange(rect(10, 20, 76, 54))
	area.SetValue("short\n" + strings.Repeat("j", 20) + "\n三\n" + strings.Repeat("j", 20))
	area.focused = true
	if area.scrollY <= 0 || area.scrollY != math.Trunc(area.scrollY) {
		t.Fatalf("scrollY at final line = %v, want a positive whole-line offset", area.scrollY)
	}
	if area.scrollX <= 0 {
		t.Fatalf("scrollX at end of long current line = %v, want positive", area.scrollX)
	}

	painter := &fakePainter{}
	area.draw(&drawContext{face: area.face, painter: painter})
	content := area.contentRect()
	caretFound := false
	for _, op := range painter.ops {
		if op.kind == "rect" && op.color == inputCaretColor && op.rect.Width == 1 {
			caretFound = true
			if op.rect.X < content.X || op.rect.X > content.X+content.Width || op.rect.Y < content.Y || op.rect.Y+op.rect.Height > content.Y+content.Height+0.001 {
				t.Errorf("scrolled caret rect = %+v outside content %+v", op.rect, content)
			}
		}
	}
	if !caretFound {
		t.Fatal("focused TextArea did not paint its scrolled caret")
	}

	router := newEventRouter(area)
	router.Handle(keyEvent(driver.KeyTab, 0))
	router.Handle(keyEvent(driver.KeyHome, 0))
	if area.scrollX != 0 {
		t.Fatalf("scrollX after line Home = %v, want 0", area.scrollX)
	}
	area.buffer.StartSelection(0)
	area.updateScroll()
	if area.scrollX != 0 || area.scrollY != 0 {
		t.Fatalf("scroll at document start = (%v, %v), want (0, 0)", area.scrollX, area.scrollY)
	}
}

func TestTextAreaClipboardAndDisabled(t *testing.T) {
	bound := "甲\n乙丙"
	area := TextArea(&bound)
	clipboard := &fakeTextClipboard{}
	setTreeClipboard(area, clipboard)
	router := arrangedTextArea(t, area, 0, 0)
	router.Handle(keyEvent(driver.KeyTab, 0))
	router.Handle(keyEvent(driver.KeyA, driver.ModCtrl))
	router.Handle(keyEvent(driver.KeyC, driver.ModCtrl))
	if clipboard.text != "甲\n乙丙" {
		t.Fatalf("clipboard after Ctrl+C = %q", clipboard.text)
	}
	router.Handle(keyEvent(driver.KeyX, driver.ModCtrl))
	if area.Value() != "" || bound != "" {
		t.Fatalf("Ctrl+X left value/binding %q / %q", area.Value(), bound)
	}
	clipboard.text = "粘\n🙂"
	router.Handle(keyEvent(driver.KeyV, driver.ModCtrl))
	if got := area.Value(); got != "粘\n🙂" || bound != got {
		t.Fatalf("multiline paste: value=%q bound=%q", got, bound)
	}

	router.Handle(leftPress(driver.Point{X: area.contentRect().X, Y: area.contentRect().Y + 1}))
	if !area.dragging {
		t.Fatal("press did not start TextArea gesture")
	}
	area.SetEnabled(false)
	if area.Enabled() || area.focused || area.dragging {
		t.Fatalf("disabled state: enabled=%v focused=%v dragging=%v", area.Enabled(), area.focused, area.dragging)
	}
	before := area.Value()
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: driver.Point{X: 200, Y: 200}})
	router.Handle(leftRelease(driver.Point{X: 200, Y: 200}))
	router.Handle(runeInputEvent('x'))
	router.Handle(keyEvent(driver.KeyBackspace, 0))
	router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl))
	if area.Value() != before || bound != before {
		t.Fatalf("disabled TextArea changed to %q / %q, want %q", area.Value(), bound, before)
	}
	if router.focus != nil {
		t.Fatal("stale disabled TextArea focus survived key routing")
	}
	area.SetEnabled(true)
	router.Handle(runeInputEvent('y'))
	if area.Value() != before || area.focused {
		t.Fatal("re-enabling resurrected stale focus or allowed editing")
	}
	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != area || !area.focused {
		t.Fatal("re-enabled TextArea did not rejoin normal focus traversal")
	}
}

func TestTextAreaPlaceholderAndFocus(t *testing.T) {
	area := TextArea(nil).Placeholder("说明")
	router := arrangedTextArea(t, area, 0, 0)
	painter := &fakePainter{}
	area.draw(&drawContext{face: area.face, painter: painter})
	placeholderFound := false
	for _, op := range painter.ops {
		if op.kind == "text" && op.text == "说明" {
			placeholderFound = true
		}
		if op.kind == "rect" && op.color == inputCaretColor {
			t.Error("unfocused empty TextArea painted a caret")
		}
	}
	if !placeholderFound {
		t.Fatal("empty TextArea did not paint its placeholder")
	}

	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != area || !area.focused {
		t.Fatal("Tab did not focus TextArea")
	}
	painter.ops = nil
	area.draw(&drawContext{face: area.face, painter: painter})
	caretFound, focusFound := false, false
	for _, op := range painter.ops {
		if op.kind == "rect" && op.color == inputCaretColor && op.rect.Width == 1 {
			caretFound = true
		}
		if op.kind == "rect" && op.color == defaultTheme.focusBorder {
			focusFound = true
		}
	}
	if !caretFound || !focusFound {
		t.Fatalf("focused TextArea paint: caret=%v focus=%v", caretFound, focusFound)
	}

	area.SetValue("内容")
	painter.ops = nil
	area.draw(&drawContext{face: area.face, painter: painter})
	for _, op := range painter.ops {
		if op.kind == "text" && op.text == "说明" {
			t.Fatal("non-empty TextArea still painted its placeholder")
		}
	}
	area.SetEnabled(false)
	painter.ops = nil
	area.draw(&drawContext{face: area.face, painter: painter})
	for _, op := range painter.ops {
		if op.kind == "rect" && (op.color == inputCaretColor || op.color == defaultTheme.focusBorder) {
			t.Fatalf("disabled TextArea painted caret/focus op %+v", op)
		}
	}
}

func TestTextAreaSetSelectionClampsAndInspects(t *testing.T) {
	var bound string
	area := TextArea(&bound)
	area.SetValue("ab\ncd\nef") // 8 runes
	if got := area.Cursor(); got != 8 {
		t.Fatalf("Cursor() after SetValue = %d, want caret at end 8", got)
	}

	area.SetSelection(-10, 99)
	if start, end, ok := area.Selection(); start != 0 || end != 8 || !ok {
		t.Fatalf("Selection() after clamping SetSelection = (%d, %d, %v), want (0, 8, true)", start, end, ok)
	}
	if got := area.Cursor(); got != 8 {
		t.Errorf("Cursor() after SetSelection = %d, want 8", got)
	}

	// Reversed anchor/cursor normalizes to [start, end) while Cursor reports
	// the active end.
	area.SetSelection(3, 1)
	if start, end, ok := area.Selection(); start != 1 || end != 3 || !ok {
		t.Errorf("Selection() after reversed SetSelection = (%d, %d, %v), want (1, 3, true)", start, end, ok)
	}
	if got := area.Cursor(); got != 1 {
		t.Errorf("Cursor() = %d, want active end 1", got)
	}

	area.SetSelection(2, 2)
	if start, end, ok := area.Selection(); start != 2 || end != 2 || ok {
		t.Errorf("Selection() after collapsed SetSelection = (%d, %d, %v), want (2, 2, false)", start, end, ok)
	}
	if got := area.Cursor(); got != 2 {
		t.Errorf("Cursor() = %d, want 2", got)
	}

	// SetSelection changes no text and does not touch the binding.
	if bound != "ab\ncd\nef" || area.Value() != bound {
		t.Errorf("SetSelection changed text: value=%q bound=%q", area.Value(), bound)
	}
}

func TestTextAreaSetSelectionRecordsNoHistory(t *testing.T) {
	var bound string
	area := TextArea(&bound)
	area.SetValue("ab")
	area.SetSelection(0, 2)

	if !area.Undo() {
		t.Fatal("Undo after SetValue+SetSelection = false, want true (the SetValue unit)")
	}
	if bound != "" || area.Value() != "" {
		t.Fatalf("Undo restored %q / %q, want empty initial text", area.Value(), bound)
	}
	// The undo restored the state before SetValue: empty text, collapsed caret.
	if start, end, ok := area.Selection(); start != 0 || end != 0 || ok {
		t.Errorf("Selection() after Undo = (%d, %d, %v), want collapsed cursor 0", start, end, ok)
	}
	if area.Undo() {
		t.Error("second Undo = true, want false: SetSelection must not record a history unit")
	}

	if !area.Redo() {
		t.Fatal("Redo = false, want true")
	}
	if bound != "ab" || area.Value() != "ab" {
		t.Fatalf("Redo restored %q / %q, want ab", area.Value(), bound)
	}
	// Redo replays SetValue's after state: caret at end, no selection.
	if got := area.Cursor(); got != 2 {
		t.Errorf("Cursor() after Redo = %d, want 2", got)
	}
	if _, _, ok := area.Selection(); ok {
		t.Error("Selection() after Redo = selected, want collapsed")
	}

	// SetSelection still ends the current merge group: the insert after it
	// starts a fresh unit instead of joining the typed run.
	merged := TextArea(nil)
	merged.buffer.Insert('a')
	merged.buffer.Insert('b') // one merged unit "" -> "ab"
	merged.SetSelection(0, 0)
	merged.buffer.Insert('c') // fresh unit "ab" -> "cab"
	if got := merged.Value(); got != "cab" {
		t.Fatalf("Value() after insert past SetSelection = %q, want cab", got)
	}
	if !merged.Undo() || merged.Value() != "ab" {
		t.Fatalf("first Undo after merge break = %q, want ab as its own unit", merged.Value())
	}
	if !merged.Undo() || merged.Value() != "" {
		t.Fatalf("second Undo = %q, want empty typed-run unit", merged.Value())
	}
	if merged.Undo() {
		t.Error("third Undo = true, want false: exactly two units were recorded")
	}
}

func TestTextAreaBackspaceSelectionReplacement(t *testing.T) {
	var bound string
	area := TextArea(&bound)
	area.SetValue("ab\ncd") // runes: a0 b1 \n2 c3 d4

	// A cross-line selection replaced by Backspace records one edit unit and
	// collapses the caret onto the range start.
	area.SetSelection(1, 4) // "b\nc"
	area.Backspace()
	if bound != "ad" || area.Value() != "ad" {
		t.Fatalf("Backspace with selection: value=%q bound=%q, want ad", area.Value(), bound)
	}
	if start, end, ok := area.Selection(); start != 1 || end != 1 || ok {
		t.Errorf("Selection() after Backspace = (%d, %d, %v), want collapsed cursor 1", start, end, ok)
	}

	// Undo restores the removed text together with the replaced selection.
	if !area.Undo() {
		t.Fatal("Undo = false, want true")
	}
	if bound != "ab\ncd" || area.Value() != "ab\ncd" {
		t.Fatalf("Undo restored %q / %q, want ab\\ncd", area.Value(), bound)
	}
	if start, end, ok := area.Selection(); start != 1 || end != 4 || !ok {
		t.Errorf("Selection() after Undo = (%d, %d, %v), want replaced range (1, 4, true)", start, end, ok)
	}
	if got := area.Cursor(); got != 4 {
		t.Errorf("Cursor() after Undo = %d, want 4", got)
	}

	if !area.Redo() {
		t.Fatal("Redo = false, want true")
	}
	if bound != "ad" || area.Value() != "ad" {
		t.Fatalf("Redo restored %q / %q, want ad", area.Value(), bound)
	}
	if _, _, ok := area.Selection(); ok {
		t.Error("Selection() after Redo = selected, want collapsed")
	}

	// A collapsed caret deletes the rune before it, per the shared semantics.
	area.SetSelection(2, 2)
	area.Backspace()
	if bound != "a" || area.Value() != "a" {
		t.Fatalf("Backspace without selection: value=%q bound=%q, want a", area.Value(), bound)
	}
	if got := area.Cursor(); got != 1 {
		t.Errorf("Cursor() after plain Backspace = %d, want 1", got)
	}
}

// TestTextAreaDocumentAndPageKeys pins the TextArea key bindings built on
// the multi-line buffer: Ctrl+Home/Ctrl+End cross the whole document while
// plain Home/End stay line-scoped, and PageUp/PageDown walk one viewport
// page of lines with the column kept and Shift extending the selection.
func TestTextAreaDocumentAndPageKeys(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("%02d", i))
	}
	bound := strings.Join(lines, "\n")
	area := TextArea(&bound)
	router := arrangedTextArea(t, area, 0, 0)
	router.Handle(keyEvent(driver.KeyTab, 0))

	// Ctrl+Home/Ctrl+End are document scope, unlike the line-scoped Home/End.
	router.Handle(keyEvent(driver.KeyHome, driver.ModCtrl))
	requireNoTextAreaSelection(t, area, 0)
	router.Handle(keyEvent(driver.KeyEnd, driver.ModCtrl))
	requireNoTextAreaSelection(t, area, len([]rune(bound)))

	// Line-scoped Home/End still shadow the document ones without Ctrl.
	router.Handle(keyEvent(driver.KeyHome, 0))
	if got := area.buffer.Cursor(); got != 87 {
		t.Fatalf("Cursor() after plain Home = %d, want 87 (line 29 start)", got)
	}
	router.Handle(keyEvent(driver.KeyEnd, driver.ModCtrl))
	requireNoTextAreaSelection(t, area, len([]rune(bound)))

	// Shift+Ctrl+Home selects from the caret back to the document start.
	router.Handle(keyEvent(driver.KeyHome, driver.ModCtrl|driver.ModShift))
	requireTextAreaSelection(t, area, 0, 89)
	router.Handle(keyEvent(driver.KeyEnd, driver.ModCtrl|driver.ModShift))
	requireNoTextAreaSelection(t, area, 89)

	// PageDown walks one viewport page of lines: every line is three runes
	// ("NN\n" except the last), so line N starts at offset 3*N.
	pageLines := max(1, int(area.contentRect().Height/area.lineHeight()))
	router.Handle(keyEvent(driver.KeyHome, driver.ModCtrl))
	if handled, _ := router.Handle(keyEvent(driver.KeyPageDown, 0)); !handled {
		t.Fatal("PageDown was not handled")
	}
	if got, want := area.buffer.Cursor(), min(29, pageLines)*3; got != want {
		t.Fatalf("Cursor() after PageDown = %d, want %d (one page of %d lines)", got, want, pageLines)
	}
	if handled, _ := router.Handle(keyEvent(driver.KeyPageUp, 0)); !handled {
		t.Fatal("PageUp was not handled")
	}
	if got := area.buffer.Cursor(); got != 0 {
		t.Fatalf("Cursor() after PageUp = %d, want 0", got)
	}

	// Repeated pages clamp at the last line with the column kept; the
	// binding is never touched.
	for i := 0; i < 30; i++ {
		router.Handle(keyEvent(driver.KeyPageDown, 0))
	}
	if got := area.buffer.Cursor(); got != 87 {
		t.Fatalf("Cursor() after clamped pages = %d, want 87 (last line start, column kept)", got)
	}
	if bound != area.Value() {
		t.Fatalf("binding changed by page movement: %q vs %q", bound, area.Value())
	}

	// Shift+PageUp selects one page back with the column kept.
	router.Handle(keyEvent(driver.KeyPageUp, driver.ModShift))
	start, end, ok := area.buffer.Selection()
	wantStart := max(0, 29-pageLines) * 3
	if !ok || start != wantStart || end != 87 {
		t.Fatalf("Selection() after Shift+PageUp = (%d, %d, %v), want (%d, 87, true)", start, end, ok, wantStart)
	}

	// Ctrl+PageUp is not a recognized shortcut: it falls through the Ctrl
	// branch unhandled, and Alt+PageUp follows the project convention of
	// treating non-Ctrl modifier sets like the plain page key.
	if handled, _ := router.Handle(keyEvent(driver.KeyPageUp, driver.ModCtrl)); handled {
		t.Fatal("Ctrl+PageUp was handled, want unhandled")
	}
	if handled, _ := router.Handle(keyEvent(driver.KeyPageDown, driver.ModAlt)); !handled {
		t.Fatal("Alt+PageDown was not handled, want the plain PageDown behavior")
	}
}
