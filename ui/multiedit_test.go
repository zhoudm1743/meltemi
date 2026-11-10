package ui

import "testing"

// TestMultiBufferLinesAndTrailingEmptyLine pins the line model: Lines keeps
// a trailing empty line, line boundaries are rune offsets, and out-of-range
// line indexes clamp instead of panicking.
func TestMultiBufferLinesAndTrailingEmptyLine(t *testing.T) {
	// "a中\n🙂\n" is three lines and the third one is empty.
	m := newMultiTextBuffer("a中\n🙂\n")
	lines := m.Lines()
	want := []string{"a中", "🙂", ""}
	if len(lines) != len(want) {
		t.Fatalf("Lines() = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("Lines()[%d] = %q, want %q", i, lines[i], want[i])
		}
	}

	// No trailing newline: no trailing empty line.
	m = newMultiTextBuffer("a中\n🙂")
	if got := m.Lines(); len(got) != 2 || got[0] != "a中" || got[1] != "🙂" {
		t.Errorf("Lines() = %v, want [\"a中\" \"🙂\"]", got)
	}

	// The empty buffer is a single empty line.
	m = newMultiTextBuffer("")
	if got := m.Lines(); len(got) != 1 || got[0] != "" {
		t.Errorf("Lines() of empty buffer = %v, want [\"\"]", got)
	}
	if got := m.LineIndex(0); got != 0 {
		t.Errorf("LineIndex(0) of empty buffer = %d, want 0", got)
	}
	if got := m.Column(0); got != 0 {
		t.Errorf("Column(0) of empty buffer = %d, want 0", got)
	}

	// Blank lines are real lines: "\n\n" is three empty lines.
	m = newMultiTextBuffer("\n\n")
	if got := m.Lines(); len(got) != 3 || got[0] != "" || got[1] != "" || got[2] != "" {
		t.Fatalf("Lines() of \"\\n\\n\" = %v, want three empty lines", got)
	}
	if got := m.LineStart(1); got != 1 {
		t.Errorf("LineStart(1) = %d, want 1", got)
	}
	if got := m.LineEnd(1); got != 1 {
		t.Errorf("LineEnd(1) = %d, want 1", got)
	}
	if got := m.LineIndex(1); got != 1 {
		t.Errorf("LineIndex(1) = %d, want 1", got)
	}
	if got := m.Column(1); got != 0 {
		t.Errorf("Column(1) = %d, want 0", got)
	}

	// The terminating newline ends its own line: runes are
	// a0 中1 \n2 🙂3 \n4 (len 5). Line content spans are [0,2) [3,4) [5,5).
	m = newMultiTextBuffer("a中\n🙂\n")
	starts := []int{0, 3, 5}
	ends := []int{2, 4, 5}
	for i := range starts {
		if got := m.LineStart(i); got != starts[i] {
			t.Errorf("LineStart(%d) = %d, want %d", i, got, starts[i])
		}
		if got := m.LineEnd(i); got != ends[i] {
			t.Errorf("LineEnd(%d) = %d, want %d", i, got, ends[i])
		}
	}

	// Out-of-range line indexes clamp to the first/last line.
	if got := m.LineStart(-1); got != 0 {
		t.Errorf("LineStart(-1) = %d, want 0 (clamped)", got)
	}
	if got := m.LineEnd(99); got != 5 {
		t.Errorf("LineEnd(99) = %d, want 5 (clamped to last line)", got)
	}
}

// TestMultiBufferVerticalMovement pins up/down caret movement: the column is
// kept when possible and clamped to the target line end, deltas beyond the
// first/last line clamp, and Shift+vertical extends the selection across
// lines with a stable anchor.
func TestMultiBufferVerticalMovement(t *testing.T) {
	// runes: a0 b1 c2 d3 \n4 x5 y6 \n7 a8 b9 c10 d11 e12 f13 (len 14)
	m := newMultiTextBuffer("abcd\nxy\nabcdef")
	if got := m.Cursor(); got != 14 {
		t.Fatalf("Cursor() = %d, want 14", got)
	}

	// Up onto the short middle line clamps the column to its end.
	m.MoveVertical(-1, false)
	if got := m.Cursor(); got != 7 {
		t.Fatalf("Cursor() after MoveVertical(-1) = %d, want 7 (clamped to end of \"xy\")", got)
	}

	// Up again: the clamped column 2 is kept on the longer line.
	m.MoveVertical(-1, false)
	if got := m.Cursor(); got != 2 {
		t.Fatalf("Cursor() after second MoveVertical(-1) = %d, want 2 (column kept)", got)
	}

	// Down keeps the column.
	m.MoveVertical(1, false)
	if got := m.Cursor(); got != 7 {
		t.Errorf("Cursor() after MoveVertical(1) = %d, want 7", got)
	}

	// A plain vertical move collapses an active selection.
	m.StartSelection(0)
	m.ExtendSelection(2)
	m.MoveVertical(1, false)
	if _, _, ok := m.Selection(); ok {
		t.Error("Selection() after plain MoveVertical = selected, want collapsed")
	}

	// Shift+vertical extends across the newline; the anchor stays put.
	m.StartSelection(1) // line 0, column 1
	m.MoveVertical(1, true)
	start, end, ok := m.Selection()
	if !ok || start != 1 || end != 6 {
		t.Errorf("Selection() after Shift+MoveVertical(1) = (%d, %d, %v), want (1, 6, true)", start, end, ok)
	}
	if got := m.Cursor(); got != 6 {
		t.Errorf("Cursor() after Shift+MoveVertical(1) = %d, want 6", got)
	}

	// Extending back up collapses the selection onto the anchor.
	m.MoveVertical(-1, true)
	if _, _, ok := m.Selection(); ok {
		t.Error("Selection() after Shift+MoveVertical back = selected, want collapsed")
	}
	if got := m.Cursor(); got != 1 {
		t.Errorf("Cursor() after Shift+MoveVertical(-1) = %d, want 1", got)
	}

	// Deltas beyond the text clamp the target line but keep the column.
	m.MoveVertical(-100, false)
	if got := m.Cursor(); got != 1 {
		t.Errorf("Cursor() after MoveVertical(-100) = %d, want 1 (first line, column kept)", got)
	}
	m.MoveVertical(100, false)
	if got := m.Cursor(); got != 9 {
		t.Errorf("Cursor() after MoveVertical(100) = %d, want 9 (last line, column kept)", got)
	}

	// Stepping off the trailing empty line of "a中\n🙂\n".
	u := newMultiTextBuffer("a中\n🙂\n")
	u.MoveVertical(-1, false)
	if got := u.Cursor(); got != 3 {
		t.Errorf("Cursor() on the 🙂 line = %d, want 3 (column 0 kept)", got)
	}
	u.MoveVertical(-1, false)
	if got := u.Cursor(); got != 0 {
		t.Errorf("Cursor() on the first line = %d, want 0", got)
	}
	u.MoveVertical(-1, false)
	if got := u.Cursor(); got != 0 {
		t.Errorf("Cursor() after clamped MoveVertical = %d, want 0 (no-op)", got)
	}

	// A vertical move ends the typing merge group like any other move.
	u = newMultiTextBuffer("")
	u.Insert('a')
	u.Insert('b')
	u.MoveVertical(0, false)
	u.Insert('c')
	if !u.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := u.Value(); got != "ab" {
		t.Errorf("Value() after Undo = %q, want %q (MoveVertical broke the typing group)", got, "ab")
	}
}

// TestMultiBufferLineHomeEndAndShift pins that Home/End act on the current
// line rather than the whole text, that Shift+Home/Shift+End select within
// the line, and that they end the typing merge group like any caret move.
func TestMultiBufferLineHomeEndAndShift(t *testing.T) {
	// runes: a0 b1 c2 \n3 🙂4 d5 e6 \n7 (len 8)
	m := newMultiTextBuffer("abc\n🙂de\n")
	if got := m.Cursor(); got != 8 {
		t.Fatalf("Cursor() = %d, want 8", got)
	}
	m.MoveVertical(-1, false) // line 1, column 0 -> cursor 4 (start of "🙂de")
	m.Move(1, false)          // cursor 5, mid-line

	// Home/End stay on the current line.
	m.Home(false)
	if got := m.Cursor(); got != 4 {
		t.Fatalf("Cursor() after Home = %d, want 4 (start of \"🙂de\")", got)
	}
	m.End(false)
	if got := m.Cursor(); got != 7 {
		t.Fatalf("Cursor() after End = %d, want 7 (end of \"🙂de\")", got)
	}

	// On line 0 the same ops must not jump across the newline.
	m.MoveVertical(-1, false) // column 7-4=3 kept -> cursor 3 (end of "abc")
	if got := m.Cursor(); got != 3 {
		t.Fatalf("Cursor() after MoveVertical(-1) = %d, want 3", got)
	}
	m.Home(false)
	if got := m.Cursor(); got != 0 {
		t.Errorf("Cursor() after Home on line 0 = %d, want 0", got)
	}
	m.End(false)
	if got := m.Cursor(); got != 3 {
		t.Errorf("Cursor() after End on line 0 = %d, want 3 (not buffer end 8)", got)
	}

	// Shift+End selects from the caret to the end of the current line.
	m.Home(false)
	m.End(true)
	start, end, ok := m.Selection()
	if !ok || start != 0 || end != 3 {
		t.Errorf("Selection() after Shift+End = (%d, %d, %v), want (0, 3, true)", start, end, ok)
	}
	if got := m.Cursor(); got != 3 {
		t.Errorf("Cursor() after Shift+End = %d, want 3", got)
	}

	// Shift+Home selects back to the line start.
	m.End(false)
	m.Home(true)
	start, end, ok = m.Selection()
	if !ok || start != 0 || end != 3 {
		t.Errorf("Selection() after Shift+Home = (%d, %d, %v), want (0, 3, true)", start, end, ok)
	}
	if got := m.Cursor(); got != 0 {
		t.Errorf("Cursor() after Shift+Home = %d, want 0", got)
	}

	// A plain End collapses the shift selection.
	m.End(false)
	if _, _, ok := m.Selection(); ok {
		t.Error("Selection() after plain End = selected, want collapsed")
	}

	// Home/End end the typing merge group like any other caret move.
	m = newMultiTextBuffer("")
	m.Insert('a')
	m.Insert('b')
	m.Home(false)
	m.Insert('c')
	if !m.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := m.Value(); got != "ab" {
		t.Errorf("Value() after Undo = %q, want %q (Home broke the typing group)", got, "ab")
	}
	if !m.Undo() {
		t.Fatal("second Undo() = false, want true")
	}
	if got := m.Value(); got != "" {
		t.Errorf("Value() after second Undo = %q, want empty", got)
	}
}

// TestMultiBufferNewlineAndUnicode pins rune-based line/column queries on
// multibyte text, InsertNewline splitting lines (including replacing a
// selection), and the history grouping: a newline is its own edit unit and
// never merges into surrounding typed input.
func TestMultiBufferNewlineAndUnicode(t *testing.T) {
	// Rune, not byte, offsets: a0 中1 \n2 🙂3 \n4 (len 5).
	m := newMultiTextBuffer("a中\n🙂\n")
	lineIndexes := []struct {
		index int
		want  int
	}{{0, 0}, {1, 0}, {2, 0}, {3, 1}, {4, 1}, {5, 2}}
	for _, tc := range lineIndexes {
		if got := m.LineIndex(tc.index); got != tc.want {
			t.Errorf("LineIndex(%d) = %d, want %d", tc.index, got, tc.want)
		}
	}
	columns := []struct {
		index int
		want  int
	}{{0, 0}, {1, 1}, {2, 2}, {3, 0}, {4, 1}, {5, 0}}
	for _, tc := range columns {
		if got := m.Column(tc.index); got != tc.want {
			t.Errorf("Column(%d) = %d, want %d", tc.index, got, tc.want)
		}
	}

	// Out-of-range offsets clamp into the buffer.
	if got := m.LineIndex(99); got != 2 {
		t.Errorf("LineIndex(99) = %d, want 2 (clamped)", got)
	}
	if got := m.Column(-3); got != 0 {
		t.Errorf("Column(-3) = %d, want 0 (clamped)", got)
	}

	// InsertNewline at the end of the text opens the trailing empty line.
	m = newMultiTextBuffer("a中")
	m.InsertNewline()
	if got := m.Value(); got != "a中\n" {
		t.Fatalf("Value() after InsertNewline = %q, want %q", got, "a中\n")
	}
	if got := m.Cursor(); got != 3 {
		t.Errorf("Cursor() after InsertNewline = %d, want 3", got)
	}
	if got := m.Lines(); len(got) != 2 || got[0] != "a中" || got[1] != "" {
		t.Errorf("Lines() after InsertNewline = %v, want [\"a中\" \"\"]", got)
	}

	// InsertNewline mid-line splits the line in front of the caret.
	m = newMultiTextBuffer("ab")
	m.Home(false)
	m.Move(1, false)
	m.InsertNewline()
	if got := m.Value(); got != "a\nb" {
		t.Fatalf("Value() after mid-line InsertNewline = %q, want %q", got, "a\nb")
	}
	if got := m.Cursor(); got != 2 {
		t.Errorf("Cursor() after mid-line InsertNewline = %d, want 2", got)
	}
	if got := m.LineStart(1); got != 2 {
		t.Errorf("LineStart(1) = %d, want 2", got)
	}
	if got := m.LineEnd(1); got != 3 {
		t.Errorf("LineEnd(1) = %d, want 3", got)
	}

	// History: "ab" merges into one unit, the newline is its own unit, and
	// "cd" after it merges into a third unit — three units, three undos.
	m = newMultiTextBuffer("")
	m.Insert('a')
	m.Insert('b')
	m.InsertNewline()
	m.Insert('c')
	m.Insert('d')
	if got := m.Value(); got != "ab\ncd" {
		t.Fatalf("Value() = %q, want %q", got, "ab\ncd")
	}
	want := []struct {
		value  string
		cursor int
	}{
		{"ab\n", 3},
		{"ab", 2},
		{"", 0},
	}
	for i, step := range want {
		if !m.Undo() {
			t.Fatalf("Undo() #%d = false, want true", i+1)
		}
		if got := m.Value(); got != step.value {
			t.Errorf("Value() after Undo #%d = %q, want %q", i+1, got, step.value)
		}
		if got := m.Cursor(); got != step.cursor {
			t.Errorf("Cursor() after Undo #%d = %d, want %d", i+1, got, step.cursor)
		}
	}
	if m.Undo() {
		t.Error("fourth Undo() = true, want false (history exhausted)")
	}
	for i := 0; i < 3; i++ {
		if !m.Redo() {
			t.Fatalf("Redo() #%d = false, want true", i+1)
		}
	}
	if got := m.Value(); got != "ab\ncd" {
		t.Errorf("Value() after redos = %q, want %q", got, "ab\ncd")
	}
	if got := m.Cursor(); got != 5 {
		t.Errorf("Cursor() after redos = %d, want 5", got)
	}

	// InsertNewline replaces a selection and undo restores it with the
	// original anchor and cursor. Replacing "ab" leaves the original '\n'.
	m = newMultiTextBuffer("ab\ncd")
	m.StartSelection(0)
	m.ExtendSelection(2)
	m.InsertNewline()
	if got := m.Value(); got != "\n\ncd" {
		t.Fatalf("Value() after newline over selection = %q, want %q", got, "\n\ncd")
	}
	if got := m.Cursor(); got != 1 {
		t.Errorf("Cursor() after newline over selection = %d, want 1", got)
	}
	if !m.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := m.Value(); got != "ab\ncd" {
		t.Errorf("Value() after Undo = %q, want %q", got, "ab\ncd")
	}
	start, end, ok := m.Selection()
	if !ok || start != 0 || end != 2 {
		t.Errorf("Selection() after Undo = (%d, %d, %v), want (0, 2, true) (anchor restored)", start, end, ok)
	}

	// A multibyte rune typed after the newline stays one whole rune.
	m = newMultiTextBuffer("ab\nc")
	m.Move(-1, false) // before 'c', right after the newline
	m.Insert('🙂')
	if got := m.Value(); got != "ab\n🙂c" {
		t.Fatalf("Value() after inserting 🙂 = %q, want %q", got, "ab\n🙂c")
	}
	if got := m.Cursor(); got != 4 {
		t.Errorf("Cursor() after inserting 🙂 = %d, want 4", got)
	}
	if got := m.LineIndex(4); got != 1 {
		t.Errorf("LineIndex(4) = %d, want 1", got)
	}
	if got := m.Column(4); got != 1 {
		t.Errorf("Column(4) = %d, want 1", got)
	}
}

// TestMultiBufferSelectionAcrossLines pins selections that span newlines:
// deletion and undo across lines, and Shift+vertical / Shift+Home movement
// growing or flipping the selection with the anchor fixed.
func TestMultiBufferSelectionAcrossLines(t *testing.T) {
	m := newMultiTextBuffer("a中\n🙂\n")

	// Select across the newline: runes [1,4) are "中\n🙂".
	m.StartSelection(1)
	m.ExtendSelection(4)
	start, end, ok := m.Selection()
	if !ok || start != 1 || end != 4 {
		t.Fatalf("Selection() across lines = (%d, %d, %v), want (1, 4, true)", start, end, ok)
	}
	cb := &fakeTextClipboard{}
	if err := m.Copy(cb); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if cb.text != "中\n🙂" {
		t.Errorf("clipboard after Copy = %q, want %q", cb.text, "中\n🙂")
	}

	// Backspace removes the whole selection spanning the newline.
	m.Backspace()
	if got := m.Value(); got != "a\n" {
		t.Fatalf("Value() after cross-line Backspace = %q, want %q", got, "a\n")
	}
	if got := m.Cursor(); got != 1 {
		t.Errorf("Cursor() after cross-line Backspace = %d, want 1", got)
	}
	if got := m.Lines(); len(got) != 2 || got[0] != "a" || got[1] != "" {
		t.Errorf("Lines() after cross-line Backspace = %v, want [\"a\" \"\"]", got)
	}

	// Undo restores the removed text together with anchor and cursor.
	if !m.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := m.Value(); got != "a中\n🙂\n" {
		t.Errorf("Value() after Undo = %q, want %q", got, "a中\n🙂\n")
	}
	start, end, ok = m.Selection()
	if !ok || start != 1 || end != 4 {
		t.Errorf("Selection() after Undo = (%d, %d, %v), want (1, 4, true)", start, end, ok)
	}
	if got := m.Cursor(); got != 4 {
		t.Errorf("Cursor() after Undo = %d, want 4", got)
	}

	// Shift+vertical grows the selection across lines.
	m = newMultiTextBuffer("abc\ndef")
	m.StartSelection(1) // line 0, column 1
	m.MoveVertical(1, true)
	start, end, ok = m.Selection()
	if !ok || start != 1 || end != 5 {
		t.Errorf("Selection() after Shift+MoveVertical(1) = (%d, %d, %v), want (1, 5, true)", start, end, ok)
	}

	// Moving back up collapses onto the anchor.
	m.MoveVertical(-1, true)
	if _, _, ok := m.Selection(); ok {
		t.Error("Selection() after Shift+MoveVertical back = selected, want collapsed")
	}
	if got := m.Cursor(); got != 1 {
		t.Errorf("Cursor() after Shift+MoveVertical(-1) = %d, want 1", got)
	}

	// Shift+Home then Shift+down flips the selection across the newline.
	m = newMultiTextBuffer("ab\ncd")
	m.StartSelection(1)
	m.Home(true)            // select "a": anchor 1, cursor 0
	m.MoveVertical(1, true) // column 0 of line 1 -> cursor 3, anchor stays 1
	start, end, ok = m.Selection()
	if !ok || start != 1 || end != 3 {
		t.Errorf("Selection() after Shift+Home then Shift+down = (%d, %d, %v), want (1, 3, true)", start, end, ok)
	}
	if got := m.Cursor(); got != 3 {
		t.Errorf("Cursor() after Shift+Home then Shift+down = %d, want 3", got)
	}
	cb = &fakeTextClipboard{}
	if err := m.Copy(cb); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if cb.text != "b\n" {
		t.Errorf("clipboard of flipped selection = %q, want %q", cb.text, "b\n")
	}
}

// TestDocumentAndPageNavigation pins the document-scope movements the
// multi-line buffer adds on top of its line-scoped Home/End: DocumentHome
// and DocumentEnd cross the whole text (which the shadowing line Home/End
// deliberately do not), and MovePage walks a viewport-sized page of lines
// with the column kept or clamped like vertical movement.
func TestDocumentAndPageNavigation(t *testing.T) {
	// runes: a0 b1 c2 \n3 d4 e5 f6 \n7 g8 h9 i10 (len 11)
	m := newMultiTextBuffer("abc\ndef\nghi")
	m.Move(1, false) // cursor 2, line 0

	// Document Home/End cross lines, unlike the line-scoped Home/End.
	m.DocumentHome(false)
	if got := m.Cursor(); got != 0 {
		t.Fatalf("Cursor() after DocumentHome = %d, want 0", got)
	}
	m.DocumentEnd(false)
	if got := m.Cursor(); got != 11 {
		t.Fatalf("Cursor() after DocumentEnd = %d, want 11", got)
	}
	m.Move(-1, false) // cursor 10, line 2
	m.Home(false)
	if got := m.Cursor(); got != 8 {
		t.Fatalf("Cursor() after line Home = %d, want 8 (line scope shadowing intact)", got)
	}

	// Extending selects from the anchor across the whole document.
	m.End(false) // line end: cursor 11
	m.DocumentHome(true)
	start, end, ok := m.Selection()
	if !ok || start != 0 || end != 11 {
		t.Fatalf("Selection() after Shift+DocumentHome = (%d, %d, %v), want (0, 11, true)", start, end, ok)
	}
	m.DocumentEnd(true)
	start, end, ok = m.Selection()
	if ok || start != 11 || end != 11 {
		t.Fatalf("Selection() after Shift+DocumentEnd back = (%d, %d, %v), want collapsed at 11", start, end, ok)
	}

	// Document moves end the typing merge group like any other move.
	m = newMultiTextBuffer("")
	m.Insert('a')
	m.Insert('b')
	m.DocumentHome(false)
	m.Insert('c')
	if !m.Undo() {
		t.Fatal("Undo() = false, want true")
	}
	if got := m.Value(); got != "ab" {
		t.Fatalf("Value() after Undo = %q, want %q (DocumentHome broke the typing group)", got, "ab")
	}

	// MovePage walks pageLines lines per page, keeping the column.
	m = newMultiTextBuffer("abcd\nxy\nabcdef")
	// cursor starts at 14 (line 2, column 4).
	m.MovePage(-1, 2, false)
	if got := m.Cursor(); got != 4 {
		t.Fatalf("Cursor() after MovePage(-1, 2) = %d, want 4 (column 6 clamped to line 0 end)", got)
	}
	m.MovePage(1, 2, false)
	if got := m.Cursor(); got != 12 {
		t.Fatalf("Cursor() after MovePage(1, 2) = %d, want 12 (line 2, column 4 kept)", got)
	}
	m.MovePage(100, 2, false)
	if got := m.Cursor(); got != 12 {
		t.Fatalf("Cursor() after clamped MovePage = %d, want 12 (last line, column kept)", got)
	}

	// MovePage extends with a stable anchor across pages.
	m.MovePage(-1, 2, true)
	start, end, ok = m.Selection()
	if !ok || start != 4 || end != 12 {
		t.Fatalf("Selection() after Shift+MovePage(-1, 2) = (%d, %d, %v), want (4, 12, true)", start, end, ok)
	}

	// A page move ends the typing merge group like any other move.
	m = newMultiTextBuffer("")
	m.Insert('a')
	m.Insert('b')
	m.MovePage(1, 3, false)
	m.Insert('c')
	if !m.Undo() {
		t.Fatal("Undo() after MovePage group break = false, want true")
	}
	if got := m.Value(); got != "ab" {
		t.Fatalf("Value() after Undo = %q, want %q (MovePage broke the typing group)", got, "ab")
	}

	// Degenerate page sizes move at least one line instead of trapping the
	// caret on the current line.
	m = newMultiTextBuffer("a\nb\nc")
	m.DocumentHome(false)
	m.MovePage(1, 0, false)
	if got := m.Cursor(); got != 2 {
		t.Fatalf("Cursor() after MovePage(1, 0) = %d, want 2 (page size floored to one line)", got)
	}
}
