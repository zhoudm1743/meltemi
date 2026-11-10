package ui

// multiTextBuffer extends the shared textBuffer with a rune-based line/column
// model for multi-line text fields. Lines are delimited by '\n' and the split
// keeps a trailing empty line, so "a中\n🙂\n" is three lines with an empty
// third. It reuses the shared editing operations — Insert, Backspace, Delete,
// Move, Selection, clipboard ops, Undo and Redo — and adds line queries,
// vertical caret movement, line-scoped Home/End and an explicit newline
// insert. Home/End shadow the shared buffer-wide ones: they move within the
// current line rather than across the whole text.
type multiTextBuffer struct {
	*textBuffer
}

func newMultiTextBuffer(value string) *multiTextBuffer {
	return &multiTextBuffer{textBuffer: newTextBuffer(value)}
}

// lineSpan is the rune-offset range [start, end) of one line's content. The
// terminating '\n', if any, sits at end and is not part of the span.
type lineSpan struct {
	start int
	end   int
}

// lineSpans returns the content span of every line in order. The last span
// always ends at len(runes), so an empty buffer yields a single empty span
// and a trailing newline yields a trailing empty span.
func (m *multiTextBuffer) lineSpans() []lineSpan {
	spans := make([]lineSpan, 0, 8)
	start := 0
	for i, r := range m.runes {
		if r == '\n' {
			spans = append(spans, lineSpan{start: start, end: i})
			start = i + 1
		}
	}
	return append(spans, lineSpan{start: start, end: len(m.runes)})
}

// lineAt clamps index to [0, len(runes)] and returns all line spans together
// with the index of the line containing index. The newline terminating a line
// belongs to that line, so the span ranges never overlap and the first span
// with end >= index is the containing one.
func (m *multiTextBuffer) lineAt(index int) ([]lineSpan, int) {
	index = clampTextIndex(index, len(m.runes))
	spans := m.lineSpans()
	for i, span := range spans {
		if index <= span.end {
			return spans, i
		}
	}
	return spans, len(spans) - 1
}

// Lines returns the buffer text split into lines, keeping a trailing empty
// line when the text ends with '\n'.
func (m *multiTextBuffer) Lines() []string {
	spans := m.lineSpans()
	lines := make([]string, len(spans))
	for i, span := range spans {
		lines[i] = string(m.runes[span.start:span.end])
	}
	return lines
}

// LineStart returns the rune offset of the first rune of line index,
// clamped to the first/last line when index is out of range.
func (m *multiTextBuffer) LineStart(index int) int {
	spans := m.lineSpans()
	index = clampTextIndex(index, len(spans)-1)
	return spans[index].start
}

// LineEnd returns the rune offset just past the last content rune of line
// index (the position of the terminating '\n', or len for the last line),
// clamped to the first/last line when index is out of range.
func (m *multiTextBuffer) LineEnd(index int) int {
	spans := m.lineSpans()
	index = clampTextIndex(index, len(spans)-1)
	return spans[index].end
}

// LineIndex returns the index of the line containing rune offset index. The
// newline terminating a line still belongs to that line, so the offset just
// after the last content rune reports the line it ends. Out-of-range indexes
// clamp into the buffer.
func (m *multiTextBuffer) LineIndex(index int) int {
	_, line := m.lineAt(index)
	return line
}

// Column returns the rune column of offset index within its line: index minus
// the line start. Out-of-range indexes clamp into the buffer.
func (m *multiTextBuffer) Column(index int) int {
	index = clampTextIndex(index, len(m.runes))
	spans, line := m.lineAt(index)
	return index - spans[line].start
}

// MoveVertical moves the caret by delta lines, keeping the current column
// when the target line is long enough and clamping to the target line end
// otherwise; the column snaps to the nearest editable boundary in the
// target line, so vertical moves never park inside a cluster. A
// non-extending move collapses any selection onto the caret; an extending
// one keeps the anchor so the selection grows across lines. Any vertical
// move ends the current merge group, so a later insert starts a fresh edit
// unit.
func (m *multiTextBuffer) MoveVertical(delta int, extend bool) {
	spans, line := m.lineAt(m.cursor)
	target := clampTextIndex(line+delta, len(spans)-1)
	column := m.cursor - spans[line].start
	cursor := spans[target].start + column
	if cursor > spans[target].end {
		cursor = spans[target].end
	}
	m.cursor = snapToTextBoundary(m.boundaries(), cursor)
	if !extend {
		m.anchor = m.cursor
	}
	m.mergeable = false
}

// Home moves the caret to the start of the current line and ends the current
// merge group; with extend it grows the selection to the line start instead.
// It shadows the shared buffer-wide Home.
func (m *multiTextBuffer) Home(extend bool) {
	m.cursor = m.LineStart(m.LineIndex(m.cursor))
	if !extend {
		m.anchor = m.cursor
	}
	m.mergeable = false
}

// End moves the caret to the end of the current line's content and ends the
// current merge group; with extend it grows the selection to the line end
// instead. It shadows the shared buffer-wide End.
func (m *multiTextBuffer) End(extend bool) {
	m.cursor = m.LineEnd(m.LineIndex(m.cursor))
	if !extend {
		m.anchor = m.cursor
	}
	m.mergeable = false
}

// InsertNewline inserts a '\n' at the caret, replacing any selection. The
// newline forms its own edit unit and never merges into a surrounding typed
// run, so undo treats it independently of the text before and after it.
func (m *multiTextBuffer) InsertNewline() {
	m.Insert('\n')
}

// DocumentHome moves the caret to the start of the whole document, unlike
// the shadowing line-scoped Home; with extend it grows the selection across
// the whole text instead.
func (m *multiTextBuffer) DocumentHome(extend bool) {
	m.textBuffer.Home(extend)
}

// DocumentEnd moves the caret to the end of the whole document, unlike the
// shadowing line-scoped End; with extend it grows the selection across the
// whole text instead.
func (m *multiTextBuffer) DocumentEnd(extend bool) {
	m.textBuffer.End(extend)
}

// MovePage moves the caret by delta pages of pageLines lines each, keeping
// the current column when the target line is long enough and clamping to
// its end otherwise; pageLines below one floor to a single line. A
// non-extending move collapses any selection onto the caret; an extending
// one keeps the anchor so the selection grows page by page. Any page move
// ends the current merge group.
func (m *multiTextBuffer) MovePage(delta, pageLines int, extend bool) {
	if pageLines < 1 {
		pageLines = 1
	}
	m.MoveVertical(delta*pageLines, extend)
}
