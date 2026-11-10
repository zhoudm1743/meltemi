package ui

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// errFakeClipboard makes fakeTextClipboard fail to model a broken system
// clipboard: Cut must roll back and Paste must leave the buffer alone.
var errFakeClipboard = errors.New("fake clipboard failure")

type fakeTextClipboard struct {
	text string
	fail bool
}

func (f *fakeTextClipboard) Text() (string, error) {
	if f.fail {
		return "", errFakeClipboard
	}
	return f.text, nil
}

func (f *fakeTextClipboard) SetText(s string) error {
	if f.fail {
		return errFakeClipboard
	}
	f.text = s
	return nil
}

func TestTextBufferInsertBackspaceDelete(t *testing.T) {
	b := newTextBuffer("")
	b.Insert('a')
	b.Insert('b')
	if got := b.Value(); got != "ab" {
		t.Fatalf("Value() = %q, want %q", got, "ab")
	}
	if got := b.Cursor(); got != 2 {
		t.Fatalf("Cursor() = %d, want 2", got)
	}

	b.Backspace()
	if got := b.Value(); got != "a" {
		t.Errorf("Value() after Backspace = %q, want %q", got, "a")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after Backspace = %d, want 1", got)
	}

	// Backspace on an empty buffer is a no-op.
	b.Backspace()
	b.Backspace()
	if got := b.Value(); got != "" {
		t.Errorf("Value() after Backspace past start = %q, want empty", got)
	}
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after Backspace past start = %d, want 0", got)
	}

	// Insert in the middle and Delete forward.
	b.Insert('x')
	b.Home(false)
	b.Insert('y')
	if got := b.Value(); got != "yx" {
		t.Fatalf("Value() after middle insert = %q, want %q", got, "yx")
	}
	if got := b.Cursor(); got != 1 {
		t.Fatalf("Cursor() after middle insert = %d, want 1", got)
	}
	b.Delete()
	if got := b.Value(); got != "y" {
		t.Errorf("Value() after Delete = %q, want %q", got, "y")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after Delete = %d, want 1 (unchanged)", got)
	}

	// Delete at the end of the buffer is a no-op.
	b.Delete()
	if got := b.Value(); got != "y" {
		t.Errorf("Value() after Delete at end = %q, want %q", got, "y")
	}

	// Backspace and Delete remove the selection instead of a single rune.
	b.SetValue("a中b")
	b.StartSelection(1)
	b.ExtendSelection(2)
	b.Backspace()
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after Backspace with selection = %q, want %q", got, "ab")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after Backspace with selection = %d, want 1", got)
	}

	b.SetValue("a中b")
	b.StartSelection(1)
	b.ExtendSelection(2)
	b.Delete()
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after Delete with selection = %q, want %q", got, "ab")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after Delete with selection = %d, want 1", got)
	}
}

func TestTextBufferUnicodeEditing(t *testing.T) {
	b := newTextBuffer("a中🙂b")
	if got := utf8.RuneCountInString(b.Value()); got != 4 {
		t.Fatalf("rune length = %d, want 4 for %q", got, "a中🙂b")
	}
	if got := b.Cursor(); got != 4 {
		t.Fatalf("Cursor() = %d, want 4 (end of initial value)", got)
	}

	// Cursor movement counts runes, not bytes: 🙂 is 4 bytes / 1 rune.
	b.Move(-1, false)
	if got := b.Cursor(); got != 3 {
		t.Fatalf("Cursor() after Move(-1) = %d, want 3", got)
	}
	b.Backspace()
	if got := b.Value(); got != "a中b" {
		t.Errorf("Value() after Backspace on 🙂 = %q, want %q", got, "a中b")
	}
	if got := b.Cursor(); got != 2 {
		t.Errorf("Cursor() after Backspace on 🙂 = %d, want 2", got)
	}

	// Inserting non-ASCII runes keeps rune-index integrity.
	b.Home(false)
	b.Insert('🙂')
	if got := b.Value(); got != "🙂a中b" {
		t.Errorf("Value() after inserting 🙂 = %q, want %q", got, "🙂a中b")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after inserting 🙂 = %d, want 1", got)
	}

	// SetValue normalizes invalid UTF-8 to U+FFFD instead of split bytes.
	b.SetValue(string([]byte{'a', 0xC3, '(', 'b'}))
	if got, want := b.Value(), "a\uFFFD(b"; got != want {
		t.Errorf("SetValue with 0xC3 0x28 = %q, want %q", got, want)
	}
	if got := b.Cursor(); got != utf8.RuneCountInString(b.Value()) {
		t.Errorf("Cursor() = %d, want rune length %d", got, utf8.RuneCountInString(b.Value()))
	}

	// A truncated multi-byte sequence collapses to one U+FFFD per bad byte.
	b.SetValue("ab\xe4\xb8")
	if got, want := b.Value(), "ab\uFFFD\uFFFD"; got != want {
		t.Errorf("SetValue with truncated 中 = %q, want %q", got, want)
	}
}

func TestTextBufferCursorHomeEndAndShift(t *testing.T) {
	b := newTextBuffer("a中🙂b")
	if got := b.Cursor(); got != 4 {
		t.Fatalf("Cursor() = %d, want 4", got)
	}

	// Movement clamps to [0, rune length].
	b.Move(1, false)
	if got := b.Cursor(); got != 4 {
		t.Errorf("Cursor() after Move(1) at end = %d, want 4 (clamped)", got)
	}
	b.Move(-100, false)
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after Move(-100) = %d, want 0 (clamped)", got)
	}

	b.End(false)
	if got := b.Cursor(); got != 4 {
		t.Errorf("Cursor() after End = %d, want 4", got)
	}
	b.Home(false)
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after Home = %d, want 0", got)
	}

	// Shift+End selects from the cursor to the end.
	b.Home(false)
	b.End(true)
	start, end, ok := b.Selection()
	if !ok || start != 0 || end != 4 {
		t.Errorf("Selection() after Home+Shift+End = (%d, %d, %v), want (0, 4, true)", start, end, ok)
	}
	if got := b.Cursor(); got != 4 {
		t.Errorf("Cursor() after Shift+End = %d, want 4", got)
	}

	// A plain move collapses the shift selection.
	b.Home(false)
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after plain Home = selected, want collapsed")
	}

	// Shift+Home selects backwards from the cursor.
	b.End(false)
	b.Home(true)
	start, end, ok = b.Selection()
	if !ok || start != 0 || end != 4 {
		t.Errorf("Selection() after End+Shift+Home = (%d, %d, %v), want (0, 4, true)", start, end, ok)
	}
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after Shift+Home = %d, want 0", got)
	}

	// Shift+Move extends one rune at a time; the anchor stays put.
	b.Home(false)
	b.Move(1, true)
	b.Move(1, true)
	start, end, ok = b.Selection()
	if !ok || start != 0 || end != 2 {
		t.Errorf("Selection() after two Shift+Move = (%d, %d, %v), want (0, 2, true)", start, end, ok)
	}
	b.Move(-100, true)
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after clamped Shift+Move = %d, want 0", got)
	}
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after Shift+Move back to anchor = selected, want collapsed")
	}
}

func TestTextBufferSelectionCopyCutPaste(t *testing.T) {
	cb := &fakeTextClipboard{}

	b := newTextBuffer("a中🙂b")
	b.SelectAll()
	start, end, ok := b.Selection()
	if !ok || start != 0 || end != 4 {
		t.Fatalf("Selection() after SelectAll = (%d, %d, %v), want (0, 4, true)", start, end, ok)
	}

	if err := b.Copy(cb); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if cb.text != "a中🙂b" {
		t.Errorf("clipboard text after Copy = %q, want %q", cb.text, "a中🙂b")
	}
	if got := b.Value(); got != "a中🙂b" {
		t.Errorf("Value() after Copy = %q, want unchanged %q", got, "a中🙂b")
	}

	if err := b.Cut(cb); err != nil {
		t.Fatalf("Cut() error = %v", err)
	}
	if got := b.Value(); got != "" {
		t.Errorf("Value() after Cut = %q, want empty", got)
	}
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after Cut = %d, want 0", got)
	}
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after Cut = selected, want cleared")
	}

	// Paste inserts at the cursor.
	cb.text = "xy"
	if err := b.Paste(cb); err != nil {
		t.Fatalf("Paste() error = %v", err)
	}
	if got := b.Value(); got != "xy" {
		t.Errorf("Value() after Paste = %q, want %q", got, "xy")
	}
	if got := b.Cursor(); got != 2 {
		t.Errorf("Cursor() after Paste = %d, want 2", got)
	}

	// Paste replaces the current selection.
	b.SelectAll()
	cb.text = "Z"
	if err := b.Paste(cb); err != nil {
		t.Fatalf("Paste() with selection error = %v", err)
	}
	if got := b.Value(); got != "Z" {
		t.Errorf("Value() after Paste over selection = %q, want %q", got, "Z")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after Paste over selection = %d, want 1", got)
	}

	// Cut/Copy with no selection leave the text and clipboard untouched.
	b = newTextBuffer("ab")
	cb.text = "keep"
	if err := b.Copy(cb); err != nil {
		t.Fatalf("Copy() without selection error = %v", err)
	}
	if err := b.Cut(cb); err != nil {
		t.Fatalf("Cut() without selection error = %v", err)
	}
	if cb.text != "keep" {
		t.Errorf("clipboard text after no-op Copy/Cut = %q, want untouched %q", cb.text, "keep")
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after no-op Copy/Cut = %q, want %q", got, "ab")
	}

	// A nil clipboard never modifies the text.
	b.SelectAll()
	if err := b.Copy(nil); err == nil {
		t.Error("Copy(nil) error = nil, want non-nil")
	}
	if err := b.Cut(nil); err == nil {
		t.Error("Cut(nil) error = nil, want non-nil")
	}
	if err := b.Paste(nil); err == nil {
		t.Error("Paste(nil) error = nil, want non-nil")
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after nil-clipboard ops = %q, want unchanged %q", got, "ab")
	}

	// A failing clipboard: Cut rolls back, Paste leaves text and selection.
	fail := &fakeTextClipboard{fail: true}
	b = newTextBuffer("a中🙂b")
	b.SelectAll()
	if err := b.Cut(fail); !errors.Is(err, errFakeClipboard) {
		t.Fatalf("Cut() with failing clipboard error = %v, want %v", err, errFakeClipboard)
	}
	if got := b.Value(); got != "a中🙂b" {
		t.Errorf("Value() after failed Cut = %q, want unchanged %q", got, "a中🙂b")
	}
	if _, _, ok := b.Selection(); !ok {
		t.Error("Selection() after failed Cut = collapsed, want kept")
	}
	if err := b.Paste(fail); !errors.Is(err, errFakeClipboard) {
		t.Fatalf("Paste() with failing clipboard error = %v, want %v", err, errFakeClipboard)
	}
	if got := b.Value(); got != "a中🙂b" {
		t.Errorf("Value() after failed Paste = %q, want unchanged %q", got, "a中🙂b")
	}
}

func TestTextBufferPastePreservesLineEndings(t *testing.T) {
	clipboard := &fakeTextClipboard{text: "a\r\nb\rc\nd"}
	buffer := newTextBuffer("")
	if err := buffer.Paste(clipboard); err != nil {
		t.Fatalf("Paste error = %v", err)
	}
	if got := buffer.Value(); got != "a\r\nb\rc\nd" {
		t.Fatalf("Paste changed shared-buffer line endings to %q", got)
	}
}

func TestTextBufferMouseAnchorAndExtend(t *testing.T) {
	b := newTextBuffer("abcd")

	// Starting a drag only positions the caret; nothing is selected yet.
	b.StartSelection(1)
	if got := b.Cursor(); got != 1 {
		t.Fatalf("Cursor() after StartSelection(1) = %d, want 1", got)
	}
	if _, _, ok := b.Selection(); ok {
		t.Fatal("Selection() after StartSelection alone = selected, want none")
	}

	// Extending forward selects [anchor, cursor).
	b.ExtendSelection(3)
	start, end, ok := b.Selection()
	if !ok || start != 1 || end != 3 {
		t.Errorf("Selection() after ExtendSelection(3) = (%d, %d, %v), want (1, 3, true)", start, end, ok)
	}

	// Extending backwards past the anchor still yields start <= end.
	b.ExtendSelection(0)
	start, end, ok = b.Selection()
	if !ok || start != 0 || end != 1 {
		t.Errorf("Selection() after ExtendSelection(0) = (%d, %d, %v), want (0, 1, true)", start, end, ok)
	}
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after ExtendSelection(0) = %d, want 0", got)
	}

	// Inserting with a mouse selection replaces it.
	b.Insert('X')
	if got := b.Value(); got != "Xbcd" {
		t.Errorf("Value() after Insert with selection = %q, want %q", got, "Xbcd")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after Insert with selection = %d, want 1", got)
	}
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after Insert = selected, want cleared")
	}

	// Out-of-range mouse indexes clamp to [0, len].
	b.StartSelection(99)
	if got := b.Cursor(); got != 4 {
		t.Errorf("Cursor() after StartSelection(99) = %d, want 4 (clamped)", got)
	}
	b.ExtendSelection(-5)
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after ExtendSelection(-5) = %d, want 0 (clamped)", got)
	}
	start, end, ok = b.Selection()
	if !ok || start != 0 || end != 4 {
		t.Errorf("Selection() after clamped extend = (%d, %d, %v), want (0, 4, true)", start, end, ok)
	}

	// A plain move collapses a mouse selection; Shift+Move extends its anchor.
	b.Move(1, false)
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after plain Move = selected, want collapsed")
	}
	b.StartSelection(1)
	b.ExtendSelection(2)
	b.Move(1, true)
	start, end, ok = b.Selection()
	if !ok || start != 1 || end != 3 {
		t.Errorf("Selection() after Shift+Move from mouse anchor = (%d, %d, %v), want (1, 3, true)", start, end, ok)
	}
}

// TestHistoryUndoRedoRestoresValueCursorAnchor pins that undo and redo restore
// the full editing state — value, caret and selection anchor — and that the
// boolean result reports whether anything was undone or redone.
func TestHistoryUndoRedoRestoresValueCursorAnchor(t *testing.T) {
	// The initial value set by the constructor is not an undoable edit.
	b := newTextBuffer("hello")
	if b.Undo() {
		t.Fatal("Undo() on a fresh buffer = true, want false")
	}
	if b.Redo() {
		t.Fatal("Redo() on a fresh buffer = true, want false")
	}

	// Three consecutive plain inserts form one edit unit.
	b = newTextBuffer("")
	b.Insert('a')
	b.Insert('b')
	b.Insert('c')
	if got := b.Value(); got != "abc" {
		t.Fatalf("Value() = %q, want %q", got, "abc")
	}
	if !b.Undo() {
		t.Fatal("Undo() after typing abc = false, want true")
	}
	if got := b.Value(); got != "" {
		t.Errorf("Value() after one Undo = %q, want %q (typed runes merge into one unit)", got, "")
	}
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after one Undo = %d, want 0", got)
	}
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after one Undo = selected, want collapsed")
	}
	if b.Undo() {
		t.Error("second Undo() = true, want false (history exhausted)")
	}

	// Each Backspace is its own unit: two deletions need two undos.
	b = newTextBuffer("abc")
	b.Backspace()
	b.Backspace()
	if got := b.Value(); got != "a" {
		t.Fatalf("Value() after two Backspace = %q, want %q", got, "a")
	}
	if !b.Undo() {
		t.Fatal("Undo() after two Backspace = false, want true")
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after first Undo = %q, want %q", got, "ab")
	}
	if got := b.Cursor(); got != 2 {
		t.Errorf("Cursor() after first Undo = %d, want 2", got)
	}
	if !b.Undo() {
		t.Fatal("second Undo() = false, want true")
	}
	if got := b.Value(); got != "abc" {
		t.Errorf("Value() after second Undo = %q, want %q", got, "abc")
	}
	if got := b.Cursor(); got != 3 {
		t.Errorf("Cursor() after second Undo = %d, want 3", got)
	}
	if b.Undo() {
		t.Error("third Undo() = true, want false (history exhausted)")
	}

	// Redo replays the popped units and restores cursor and anchor too.
	if !b.Redo() {
		t.Fatal("Redo() = false, want true")
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after first Redo = %q, want %q", got, "ab")
	}
	if got := b.Cursor(); got != 2 {
		t.Errorf("Cursor() after first Redo = %d, want 2", got)
	}
	if !b.Redo() {
		t.Fatal("second Redo() = false, want true")
	}
	if got := b.Value(); got != "a" {
		t.Errorf("Value() after second Redo = %q, want %q", got, "a")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after second Redo = %d, want 1", got)
	}
	if b.Redo() {
		t.Error("third Redo() = true, want false (redo branch exhausted)")
	}

	// Undo restores the caret position recorded before the edit, not merely
	// the text: Backspace at the middle must put the caret back mid-run.
	b = newTextBuffer("ab")
	b.Move(-1, false)
	b.Backspace()
	if got := b.Value(); got != "b" {
		t.Fatalf("Value() after middle Backspace = %q, want %q", got, "b")
	}
	if !b.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after Undo = %q, want %q", got, "ab")
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after Undo = %d, want 1 (caret before the edit)", got)
	}
}

// TestHistoryGroupingAndSelectionRestore pins the grouping rules: plain
// inserts merge into one unit, a newline and a selection replacement break
// the merge chain, and undoing a selection replacement restores the replaced
// text together with the anchor of the original selection.
func TestHistoryGroupingAndSelectionRestore(t *testing.T) {
	// Plain inserts merge into one unit.
	b := newTextBuffer("")
	b.Insert('a')
	b.Insert('b')
	if got := b.Value(); got != "ab" {
		t.Fatalf("Value() = %q, want %q", got, "ab")
	}
	if !b.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := b.Value(); got != "" {
		t.Errorf("Value() after one Undo = %q, want empty (typed run merges into one unit)", got)
	}
	if b.Undo() {
		t.Error("second Undo() = true, want false (single merged unit)")
	}

	// A newline forms its own unit and breaks the merge chain: the runes
	// typed after it must not join the typing unit before it.
	b = newTextBuffer("")
	b.Insert('a')
	b.Insert('b')
	b.Insert('\n')
	b.Insert('c')
	if got := b.Value(); got != "ab\nc" {
		t.Fatalf("Value() = %q, want %q", got, "ab\nc")
	}
	if !b.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := b.Value(); got != "ab\n" {
		t.Errorf("Value() after first Undo = %q, want %q (trailing insert is its own unit)", got, "ab\n")
	}
	if !b.Undo() {
		t.Fatal("second Undo() = false, want true")
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after second Undo = %q, want %q (newline is its own unit)", got, "ab")
	}
	if !b.Undo() {
		t.Fatal("third Undo() = false, want true")
	}
	if got := b.Value(); got != "" {
		t.Errorf("Value() after third Undo = %q, want %q", got, "")
	}
	if b.Undo() {
		t.Error("fourth Undo() = true, want false (history exhausted)")
	}

	// Replacing a selection is one independent unit: undoing it restores the
	// replaced text plus the original anchor and cursor.
	b = newTextBuffer("hello")
	b.End(false)
	b.Insert('!')
	b.Home(false)
	b.Move(1, true)
	if start, end, ok := b.Selection(); !ok || start != 0 || end != 1 {
		t.Fatalf("Selection() = (%d, %d, %v), want (0, 1, true)", start, end, ok)
	}
	b.Insert('X')
	if got := b.Value(); got != "Xello!" {
		t.Fatalf("Value() after selection replace = %q, want %q", got, "Xello!")
	}
	if !b.Undo() {
		t.Fatal("Undo() after selection replace = false, want true")
	}
	if got := b.Value(); got != "hello!" {
		t.Errorf("Value() after Undo = %q, want %q", got, "hello!")
	}
	start, end, ok := b.Selection()
	if !ok || start != 0 || end != 1 {
		t.Errorf("Selection() after Undo = (%d, %d, %v), want (0, 1, true) (anchor restored)", start, end, ok)
	}
	if got := b.Cursor(); got != 1 {
		t.Errorf("Cursor() after Undo = %d, want 1", got)
	}
	// The typing unit before the replacement is still intact underneath.
	if !b.Undo() {
		t.Fatal("second Undo() = false, want true")
	}
	if got := b.Value(); got != "hello" {
		t.Errorf("Value() after second Undo = %q, want %q", got, "hello")
	}
	if got := b.Cursor(); got != 5 {
		t.Errorf("Cursor() after second Undo = %d, want 5", got)
	}
	if b.Undo() {
		t.Error("third Undo() = true, want false (constructor value is not a unit)")
	}
}

// TestHistoryLimitAndRedoInvalidation pins the 100-unit cap — the oldest
// units are dropped once the cap is exceeded — and that a new edit clears
// the redo branch.
func TestHistoryLimitAndRedoInvalidation(t *testing.T) {
	// 105 independent SetValue edits exceed the cap of 100; the five oldest
	// units must be dropped, so 100 undos land on the value from edit six
	// and the 101st undo has nothing left.
	b := newTextBuffer("")
	for i := 0; i < 105; i++ {
		b.SetValue(strings.Repeat("x", i+1))
	}
	if got := len([]rune(b.Value())); got != 105 {
		t.Fatalf("Value() length = %d, want 105", got)
	}
	for i := 0; i < 100; i++ {
		if !b.Undo() {
			t.Fatalf("Undo() #%d = false, want true", i+1)
		}
	}
	if got := len([]rune(b.Value())); got != 5 {
		t.Errorf("Value() length after 100 undos = %d, want 5 (oldest units dropped)", got)
	}
	if b.Undo() {
		t.Error("Undo() #101 = true, want false (cap keeps only the newest 100 units)")
	}
	for i := 0; i < 100; i++ {
		if !b.Redo() {
			t.Fatalf("Redo() #%d = false, want true", i+1)
		}
	}
	if got := len([]rune(b.Value())); got != 105 {
		t.Errorf("Value() length after 100 redos = %d, want 105", got)
	}
	if b.Redo() {
		t.Error("Redo() #101 = true, want false")
	}

	// A new edit after an undo invalidates the redo branch.
	b = newTextBuffer("")
	b.Insert('a')
	b.Insert('b')
	b.Undo()
	if got := b.Value(); got != "" {
		t.Fatalf("Value() after Undo = %q, want empty", got)
	}
	b.Insert('x')
	if got := b.Value(); got != "x" {
		t.Fatalf("Value() after new edit = %q, want %q", got, "x")
	}
	if b.Redo() {
		t.Error("Redo() after a new edit = true, want false (redo branch cleared)")
	}
	if !b.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := b.Value(); got != "" {
		t.Errorf("Value() after Undo = %q, want empty", got)
	}
	if b.Undo() {
		t.Error("Undo() past the cleared redo boundary = true, want false")
	}

	// Undo/redo walk the same units in mirror order.
	b = newTextBuffer("")
	b.Insert('a')
	b.Backspace()
	if got := b.Value(); got != "" {
		t.Fatalf("Value() = %q, want empty", got)
	}
	b.Undo()
	if got := b.Value(); got != "a" {
		t.Errorf("Value() after Undo = %q, want %q", got, "a")
	}
	b.Undo()
	if got := b.Value(); got != "" {
		t.Errorf("Value() after second Undo = %q, want empty", got)
	}
	if b.Undo() {
		t.Error("third Undo() = true, want false")
	}
	if !b.Redo() {
		t.Fatal("Redo() = false, want true")
	}
	if got := b.Value(); got != "a" {
		t.Errorf("Value() after Redo = %q, want %q", got, "a")
	}
	if !b.Redo() {
		t.Fatal("second Redo() = false, want true")
	}
	if got := b.Value(); got != "" {
		t.Errorf("Value() after second Redo = %q, want empty", got)
	}
	if b.Redo() {
		t.Error("third Redo() = true, want false")
	}
}

// TestHistorySkipsNoopEdits pins that edits which change nothing — a
// Backspace at the start, a Delete at the end, caret moves, clipboard reads
// and pasting an empty string over no selection — never consume an undo
// unit, and that SetValue with an identical value is not recorded either.
func TestHistorySkipsNoopEdits(t *testing.T) {
	b := newTextBuffer("ab")
	cb := &fakeTextClipboard{}

	b.Home(false)
	b.Backspace()
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after no-op Backspace = %q, want %q", got, "ab")
	}
	b.End(false)
	b.Delete()
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after no-op Delete = %q, want %q", got, "ab")
	}

	// Caret movement and selection changes are not edits.
	b.Move(-100, false)
	b.SelectAll()
	b.Move(1, true)
	b.StartSelection(1)
	b.ExtendSelection(2)
	if err := b.Copy(cb); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if cb.text != "b" {
		t.Errorf("clipboard after Copy of (1,2) = %q, want %q", cb.text, "b")
	}

	// Cut and Paste that change nothing must not be recorded.
	b.Home(false)
	if err := b.Cut(cb); err != nil {
		t.Fatalf("Cut() without selection error = %v", err)
	}
	if cb.text != "b" {
		t.Errorf("clipboard after no-op Cut = %q, want untouched %q", cb.text, "b")
	}
	empty := &fakeTextClipboard{text: ""}
	if err := b.Paste(empty); err != nil {
		t.Fatalf("Paste() of empty text error = %v", err)
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after no-op Paste = %q, want %q", got, "ab")
	}

	// SetValue with the identical value parks the caret at the end, which is
	// already the case here, so nothing is recorded.
	b.End(false)
	b.SetValue("ab")

	if b.Undo() {
		t.Error("Undo() after only no-op edits = true, want false")
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() = %q, want %q", got, "ab")
	}
}

// TestHistoryPasteAndSetValueAreIndependent pins that Paste, Cut and SetValue
// each form their own edit unit: they never merge with typed input or with
// each other, and Cut restores the removed selection on undo.
func TestHistoryPasteAndSetValueAreIndependent(t *testing.T) {
	cb := &fakeTextClipboard{text: "xy"}
	b := newTextBuffer("")
	b.Paste(cb)
	b.Insert('z')
	cb.text = "UV"
	b.Paste(cb)
	b.SetValue("reset")
	if got := b.Value(); got != "reset" {
		t.Fatalf("Value() = %q, want %q", got, "reset")
	}

	want := []struct {
		value  string
		cursor int
	}{
		{"xyzUV", 5},
		{"xyz", 3},
		{"xy", 2},
		{"", 0},
	}
	for i, step := range want {
		if !b.Undo() {
			t.Fatalf("Undo() #%d = false, want true", i+1)
		}
		if got := b.Value(); got != step.value {
			t.Errorf("Value() after Undo #%d = %q, want %q", i+1, got, step.value)
		}
		if got := b.Cursor(); got != step.cursor {
			t.Errorf("Cursor() after Undo #%d = %d, want %d", i+1, got, step.cursor)
		}
	}
	if b.Undo() {
		t.Error("fifth Undo() = true, want false (history exhausted)")
	}
	for i := 0; i < 4; i++ {
		if !b.Redo() {
			t.Fatalf("Redo() #%d = false, want true", i+1)
		}
	}
	if got := b.Value(); got != "reset" {
		t.Errorf("Value() after redos = %q, want %q", got, "reset")
	}

	// Two pastes are two units: undoing the second leaves the first in place.
	b = newTextBuffer("")
	cb.text = "one"
	b.Paste(cb)
	cb.text = "two"
	b.Paste(cb)
	if !b.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := b.Value(); got != "one" {
		t.Errorf("Value() after Undo = %q, want %q (second paste undone only)", got, "one")
	}
	if !b.Undo() {
		t.Fatal("second Undo() = false, want true")
	}
	if got := b.Value(); got != "" {
		t.Errorf("Value() after second Undo = %q, want empty", got)
	}

	// Cut is its own unit and undoing it restores the removed selection.
	b = newTextBuffer("a中🙂b")
	b.SelectAll()
	cutClip := &fakeTextClipboard{}
	if err := b.Cut(cutClip); err != nil {
		t.Fatalf("Cut() error = %v", err)
	}
	if got := b.Value(); got != "" {
		t.Fatalf("Value() after Cut = %q, want empty", got)
	}
	if !b.Undo() {
		t.Fatal("Undo() after Cut = false, want true")
	}
	if got := b.Value(); got != "a中🙂b" {
		t.Errorf("Value() after Undo = %q, want %q", got, "a中🙂b")
	}
	start, end, ok := b.Selection()
	if !ok || start != 0 || end != 4 {
		t.Errorf("Selection() after Undo = (%d, %d, %v), want (0, 4, true) (cut selection restored)", start, end, ok)
	}
	if got := cutClip.text; got != "a中🙂b" {
		t.Errorf("clipboard after Cut = %q, want %q", got, "a中🙂b")
	}
}

// TestHistoryMovementBreaksInsertGrouping pins that caret movement and
// selection changes end the current typing group: typing, moving, and typing
// again yields two edit units. The cursor motion produced by the edits
// themselves must not break continuous typing.
func TestHistoryMovementBreaksInsertGrouping(t *testing.T) {
	breakers := []struct {
		name  string
		apply func(b *textBuffer)
	}{
		{"Move", func(b *textBuffer) { b.Move(-1, false) }},
		{"Shift+Move", func(b *textBuffer) { b.Move(-1, true); b.Move(1, false) }},
		{"Home", func(b *textBuffer) { b.Home(false) }},
		{"End", func(b *textBuffer) { b.End(false) }},
		{"SelectAll", func(b *textBuffer) { b.SelectAll(); b.Home(false) }},
		{"StartSelection", func(b *textBuffer) { b.StartSelection(0) }},
		{"ExtendSelection", func(b *textBuffer) { b.ExtendSelection(1); b.Move(1, false) }},
	}
	for _, br := range breakers {
		b := newTextBuffer("")
		b.Insert('a')
		b.Insert('b')
		br.apply(b)
		b.Insert('c')

		if !b.Undo() {
			t.Fatalf("%s: Undo() = false, want true", br.name)
		}
		if got := b.Value(); got != "ab" {
			t.Errorf("%s: Value() after Undo = %q, want %q (movement must end the typing group)", br.name, got, "ab")
		}
		if !b.Undo() {
			t.Fatalf("%s: second Undo() = false, want true", br.name)
		}
		if got := b.Value(); got != "" {
			t.Errorf("%s: Value() after second Undo = %q, want empty", br.name, got)
		}
		if b.Undo() {
			t.Errorf("%s: third Undo() = true, want false", br.name)
		}
	}

	// The cursor motion produced by the edits themselves never breaks the
	// group: three plain inserts still merge into a single edit unit.
	b := newTextBuffer("")
	b.Insert('a')
	b.Insert('b')
	b.Insert('c')
	if !b.Undo() {
		t.Fatal("plain typing: Undo() = false, want true")
	}
	if got := b.Value(); got != "" {
		t.Errorf("plain typing: Value() after one Undo = %q, want empty (edits' own cursor motion keeps the group)", got)
	}
	if b.Undo() {
		t.Error("plain typing: second Undo() = true, want false (single merged unit)")
	}
}

// TestHistorySetValueSameValueKeepsCaretAndRedo pins that SetValue with the
// identical text value is not an edit at all: it still parks the caret and
// collapses the selection, but records no history unit and leaves the redo
// branch untouched. A changed value records its own unit as before.
func TestHistorySetValueSameValueKeepsCaretAndRedo(t *testing.T) {
	// Same value with the caret mid-text: the caret still normalizes to the
	// end, and no history unit is created.
	b := newTextBuffer("ab")
	b.StartSelection(1)
	b.SetValue("ab")
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after same-value SetValue = %q, want %q", got, "ab")
	}
	if got := b.Cursor(); got != 2 {
		t.Errorf("Cursor() after same-value SetValue = %d, want 2 (caret parked at end)", got)
	}
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after same-value SetValue = selected, want collapsed")
	}
	if b.Undo() {
		t.Error("Undo() after same-value SetValue = true, want false")
	}

	// Same value over an active selection: the selection collapses, no record.
	b = newTextBuffer("ab")
	b.SelectAll()
	b.SetValue("ab")
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after same-value SetValue over selection = selected, want collapsed")
	}
	if got := b.Cursor(); got != 2 {
		t.Errorf("Cursor() after same-value SetValue over selection = %d, want 2", got)
	}
	if b.Undo() {
		t.Error("Undo() after same-value SetValue over selection = true, want false")
	}

	// After an undo, a same-value SetValue must not destroy the redo branch.
	b = newTextBuffer("")
	b.Insert('a')
	b.Insert('b')
	b.Undo() // "" ; the merged unit sits on the redo branch
	if got := b.Value(); got != "" {
		t.Fatalf("Value() after Undo = %q, want empty", got)
	}
	b.SetValue("") // identical value: no record, redo branch must survive
	if !b.Redo() {
		t.Fatal("Redo() after same-value SetValue = false, want true (redo branch kept)")
	}
	if got := b.Value(); got != "ab" {
		t.Errorf("Value() after Redo = %q, want %q", got, "ab")
	}

	// A changed value still records its own unit and clears the redo branch.
	b = newTextBuffer("")
	b.Insert('a')
	b.Undo()
	b.SetValue("reset")
	if b.Redo() {
		t.Error("Redo() after changed-value SetValue = true, want false (redo cleared)")
	}
	if !b.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := b.Value(); got != "" {
		t.Errorf("Value() after Undo = %q, want empty", got)
	}
	if b.Undo() {
		t.Error("second Undo() = true, want false")
	}
}

// TestWordNavigationAndDeletion pins the Unicode-aware word operations of the
// shared buffer: word constituents are Unicode letters and digits (CJK
// included, never ASCII-only), whitespace forms its own group, and every
// other rune — punctuation and symbols — forms a third group. Ctrl+arrow
// style movement crosses exactly one group, extends with a kept anchor, and
// word deletion replaces an active selection or removes one neighboring
// group, each as its own undo unit.
func TestWordNavigationAndDeletion(t *testing.T) {
	// CJK characters are letters: "abc中文def" is one single word group.
	b := newTextBuffer("abc中文def")
	b.MoveWord(-1, false)
	if got := b.Cursor(); got != 0 {
		t.Fatalf("Cursor() after MoveWord(-1) over mixed CJK/ASCII = %d, want 0 (one word group)", got)
	}
	b.MoveWord(1, false)
	if got := b.Cursor(); got != 8 {
		t.Fatalf("Cursor() after MoveWord(1) = %d, want 8 (end of the whole word)", got)
	}

	// Consecutive whitespace is one group: a0 b1 ' '2 ' '3 c4 d5.
	b = newTextBuffer("ab  cd")
	b.MoveWord(-1, false)
	if got := b.Cursor(); got != 4 {
		t.Fatalf("Cursor() after MoveWord(-1) = %d, want 4 (start of \"cd\")", got)
	}
	b.MoveWord(-1, false)
	if got := b.Cursor(); got != 2 {
		t.Fatalf("Cursor() after second MoveWord(-1) = %d, want 2 (start of the space run)", got)
	}
	b.MoveWord(-1, false)
	if got := b.Cursor(); got != 0 {
		t.Fatalf("Cursor() after third MoveWord(-1) = %d, want 0", got)
	}
	b.MoveWord(1, false)
	b.MoveWord(1, false)
	b.MoveWord(1, false)
	if got := b.Cursor(); got != 6 {
		t.Fatalf("Cursor() after three MoveWord(1) = %d, want 6 (end of buffer)", got)
	}
	b.MoveWord(1, false)
	if got := b.Cursor(); got != 6 {
		t.Errorf("Cursor() after MoveWord(1) at end = %d, want 6 (clamped no-op)", got)
	}
	b.MoveWord(-100, false)
	if got := b.Cursor(); got != 0 {
		t.Errorf("Cursor() after MoveWord(-100) = %d, want 0 (clamped)", got)
	}

	// Punctuation is its own group: f0 o1 o2 ,3 ' '4 b5 a6 r7.
	b = newTextBuffer("foo, bar")
	b.MoveWord(-1, false)
	if got := b.Cursor(); got != 5 {
		t.Fatalf("Cursor() over \"foo, bar\" = %d, want 5 (start of \"bar\")", got)
	}
	b.MoveWord(-1, false)
	if got := b.Cursor(); got != 4 {
		t.Fatalf("Cursor() after skipping \"bar\" = %d, want 4 (start of the space)", got)
	}
	b.MoveWord(-1, false)
	if got := b.Cursor(); got != 3 {
		t.Fatalf("Cursor() after skipping the space = %d, want 3 (after the comma)", got)
	}
	b.MoveWord(-1, false)
	if got := b.Cursor(); got != 0 {
		t.Fatalf("Cursor() after skipping the comma = %d, want 0 (start of \"foo\")", got)
	}

	// Extending keeps the anchor, so Shift+word selects whole groups.
	b = newTextBuffer("ab  cd")
	b.End(false)
	b.MoveWord(-1, true)
	start, end, ok := b.Selection()
	if !ok || start != 4 || end != 6 {
		t.Fatalf("Selection() after Shift+MoveWord(-1) = (%d, %d, %v), want (4, 6, true)", start, end, ok)
	}
	b.MoveWord(-1, true)
	start, end, ok = b.Selection()
	if !ok || start != 2 || end != 6 {
		t.Fatalf("Selection() after second Shift+MoveWord(-1) = (%d, %d, %v), want (2, 6, true)", start, end, ok)
	}
	b.MoveWord(1, false)
	if _, _, ok := b.Selection(); ok {
		t.Error("Selection() after plain MoveWord(1) = selected, want collapsed")
	}

	// BackspaceWord removes one group backwards; a trailing space group is
	// removed separately from the word before it.
	b = newTextBuffer("hello world")
	b.BackspaceWord()
	if got := b.Value(); got != "hello " {
		t.Fatalf("Value() after BackspaceWord = %q, want %q", got, "hello ")
	}
	if got := b.Cursor(); got != 6 {
		t.Fatalf("Cursor() after BackspaceWord = %d, want 6", got)
	}
	b.BackspaceWord()
	if got := b.Value(); got != "hello" {
		t.Fatalf("Value() after second BackspaceWord = %q, want %q", got, "hello")
	}
	b.BackspaceWord()
	if got := b.Value(); got != "" {
		t.Fatalf("Value() after third BackspaceWord = %q, want empty", got)
	}
	b.BackspaceWord()
	if got := b.Value(); got != "" {
		t.Errorf("Value() after BackspaceWord on empty buffer = %q, want empty (no-op)", got)
	}

	// DeleteWord removes one group forwards, symmetric to BackspaceWord.
	b = newTextBuffer("hello world")
	b.Home(false)
	b.DeleteWord()
	if got := b.Value(); got != " world" {
		t.Fatalf("Value() after DeleteWord = %q, want %q", got, " world")
	}
	if got := b.Cursor(); got != 0 {
		t.Fatalf("Cursor() after DeleteWord = %d, want 0", got)
	}
	b.DeleteWord()
	if got := b.Value(); got != "world" {
		t.Fatalf("Value() after second DeleteWord = %q, want %q", got, "world")
	}
	b.DeleteWord()
	if got := b.Value(); got != "" {
		t.Fatalf("Value() after third DeleteWord = %q, want empty", got)
	}
	b.DeleteWord()
	if got := b.Value(); got != "" {
		t.Errorf("Value() after DeleteWord on empty buffer = %q, want empty (no-op)", got)
	}

	// With an active selection, word deletion removes the selection exactly
	// like Backspace does.
	b = newTextBuffer("a中🙂b")
	b.StartSelection(1)
	b.ExtendSelection(3)
	b.BackspaceWord()
	if got := b.Value(); got != "ab" {
		t.Fatalf("Value() after BackspaceWord with selection = %q, want %q", got, "ab")
	}
	b = newTextBuffer("a中🙂b")
	b.StartSelection(1)
	b.ExtendSelection(3)
	b.DeleteWord()
	if got := b.Value(); got != "ab" {
		t.Fatalf("Value() after DeleteWord with selection = %q, want %q", got, "ab")
	}

	// Each word deletion is its own edit unit and undo restores the group:
	// "two" goes first, then the separating space.
	b = newTextBuffer("one two")
	b.BackspaceWord()
	b.BackspaceWord()
	if got := b.Value(); got != "one" {
		t.Fatalf("Value() after two BackspaceWord = %q, want %q", got, "one")
	}
	if !b.Undo() {
		t.Fatal("Undo() after BackspaceWord = false, want true")
	}
	if got := b.Value(); got != "one " {
		t.Fatalf("Value() after Undo = %q, want %q (one group per unit)", got, "one ")
	}
	if !b.Undo() {
		t.Fatal("second Undo() = false, want true")
	}
	if got := b.Value(); got != "one two" {
		t.Fatalf("Value() after second Undo = %q, want %q", got, "one two")
	}
	if b.Undo() {
		t.Error("third Undo() = true, want false")
	}

	// Word moves and no-op word deletions record nothing and break the
	// typing merge group like any other caret move.
	b = newTextBuffer("")
	b.Insert('a')
	b.Insert('b')
	b.MoveWord(-1, false)
	b.Insert('c')
	if !b.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := b.Value(); got != "ab" {
		t.Fatalf("Value() after Undo = %q, want %q (MoveWord broke the typing group)", got, "ab")
	}
	b = newTextBuffer("ab")
	b.Home(false)
	b.BackspaceWord()
	if b.Undo() {
		t.Error("Undo() after no-op BackspaceWord = true, want false")
	}
	b.End(false)
	b.DeleteWord()
	if b.Undo() {
		t.Error("Undo() after no-op DeleteWord = true, want false")
	}
}
