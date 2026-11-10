package ui

import (
	"errors"
	"slices"
	"unicode"
)

// errNilTextClipboard is returned by Copy/Cut/Paste when the clipboard
// argument is nil, so a missing system clipboard never silently drops data.
var errNilTextClipboard = errors.New("ui: nil text clipboard")

// textClipboard abstracts the system clipboard for text editing. A failing
// implementation must leave the textBuffer unchanged.
type textClipboard interface {
	Text() (string, error)
	SetText(string) error
}

// maxEditHistory caps the number of edit units retained for undo; the oldest
// units are dropped once the cap is exceeded.
const maxEditHistory = 100

// editSnapshot captures the full editable state of a textBuffer so undo and
// redo can restore exactly what an edit changed.
type editSnapshot struct {
	value  string
	cursor int
	anchor int
}

// editUnit is a single undoable edit: the state before it ran and the state
// after it completed. Redo replays the after state of a unit popped back off
// the redo branch.
type editUnit struct {
	before editSnapshot
	after  editSnapshot
}

// textBuffer is the shared editing model for single-line (Input) and
// password fields. All indexes are rune offsets clamped to [0, len]; the
// selection is the half-open range [anchor, cursor) (normalized to
// start <= end by Selection). Editing with an active selection replaces it,
// mirroring platform text fields.
//
// Caret indexes always land on editable boundaries: whole grapheme clusters
// under the documented subset in grapheme.go, further restricted by the
// shaped clusters of the displayed text when the widget reports them (via
// boundaryHook), so a caret can never park inside a combining sequence, an
// emoji ZWJ sequence, a regional-indicator flag or a shaped ligature.
// Internal storage stays plain runes, so bindings and undo are unaffected.
//
// Every real modification records an edit unit holding the state before and
// after the edit; consecutive plain inserts merge into one unit, while
// Backspace/Delete, selection replacement, newline, Cut, Paste and SetValue
// each start a fresh unit. Caret movement and selection changes never record,
// but they end the current merge group so the next insert starts a fresh
// unit. Undo and redo restore the value, caret and anchor of the affected
// unit.
type textBuffer struct {
	runes  []rune
	cursor int
	anchor int

	history   []editUnit
	redo      []editUnit
	mergeable bool

	// boundaryHook, when set by a text widget, returns the editable
	// boundaries of the current content refined with the shaped clusters
	// of the displayed text (see shapedTextBoundaries). It may return nil
	// when no layout is available; the buffer then falls back to the
	// grapheme subset alone, which is always safe.
	boundaryHook func() []int
}

func newTextBuffer(value string) *textBuffer {
	b := &textBuffer{}
	b.runes = []rune(value)
	b.cursor = len(b.runes)
	b.anchor = b.cursor
	return b
}

// boundaries returns the editable boundaries for the current content: the
// widget's shaped refinement when offered, else the grapheme subset.
func (b *textBuffer) boundaries() []int {
	if b.boundaryHook != nil {
		if shaped := b.boundaryHook(); len(shaped) > 0 {
			return shaped
		}
	}
	return graphemeBoundaries(b.runes)
}

// snapshot captures the current value, caret and anchor.
func (b *textBuffer) snapshot() editSnapshot {
	return editSnapshot{value: string(b.runes), cursor: b.cursor, anchor: b.anchor}
}

// apply restores a snapshot, clamping the caret and anchor in case the value
// shrank since the snapshot was taken.
func (b *textBuffer) apply(s editSnapshot) {
	b.runes = []rune(s.value)
	b.cursor = clampTextIndex(s.cursor, len(b.runes))
	b.anchor = clampTextIndex(s.anchor, len(b.runes))
}

// recordEdit stores the transition from before to the current state. Edits
// that changed nothing are dropped entirely — including redo invalidation —
// so a no-op never consumes an undo unit or destroys a redo branch. merge
// joins a plain insert into the newest unit; any other edit starts a fresh
// unit, breaking the merge chain. Every recorded edit clears the redo
// branch and sets whether the next plain insert may merge.
func (b *textBuffer) recordEdit(before editSnapshot, merge bool) {
	after := b.snapshot()
	if before == after {
		return
	}
	if merge && b.mergeable && len(b.history) > 0 {
		b.history[len(b.history)-1].after = after
	} else {
		b.history = append(b.history, editUnit{before: before, after: after})
		if len(b.history) > maxEditHistory {
			b.history = b.history[1:]
		}
	}
	b.redo = b.redo[:0]
	b.mergeable = merge
}

// Undo reverts the newest edit unit, restoring the value, caret and anchor
// recorded before it ran, and moves the unit to the redo branch. It reports
// whether a unit was undone. The merge chain breaks, so further typing
// starts a fresh unit instead of joining the undone one.
func (b *textBuffer) Undo() bool {
	if len(b.history) == 0 {
		return false
	}
	unit := b.history[len(b.history)-1]
	b.history = b.history[:len(b.history)-1]
	b.redo = append(b.redo, unit)
	b.apply(unit.before)
	b.mergeable = false
	return true
}

// Redo replays the newest undone unit, restoring the value, caret and anchor
// recorded after it ran, and moves the unit back onto the history stack. It
// reports whether a unit was redone and breaks the merge chain like Undo.
func (b *textBuffer) Redo() bool {
	if len(b.redo) == 0 {
		return false
	}
	unit := b.redo[len(b.redo)-1]
	b.redo = b.redo[:len(b.redo)-1]
	b.history = append(b.history, unit)
	if len(b.history) > maxEditHistory {
		b.history = b.history[1:]
	}
	b.apply(unit.after)
	b.mergeable = false
	return true
}

// SetValue replaces the whole content, normalizing invalid UTF-8 to U+FFFD
// via the rune conversion, parks the caret at the end with no selection and
// records the change as its own edit unit. Setting the identical value is
// not an edit: the caret and anchor still normalize, but no history unit is
// recorded and the redo branch stays untouched.
func (b *textBuffer) SetValue(value string) {
	runes := []rune(value)
	if slices.Equal(runes, b.runes) {
		b.cursor = len(b.runes)
		b.anchor = b.cursor
		return
	}
	before := b.snapshot()
	b.runes = runes
	b.cursor = len(b.runes)
	b.anchor = b.cursor
	b.recordEdit(before, false)
}

func (b *textBuffer) Value() string {
	return string(b.runes)
}

func (b *textBuffer) Cursor() int {
	return b.cursor
}

// Selection reports the selected rune range. ok is false when the anchor and
// cursor coincide, i.e. there is no selection; start == end == cursor then.
func (b *textBuffer) Selection() (start, end int, ok bool) {
	if b.anchor == b.cursor {
		return b.cursor, b.cursor, false
	}
	return min(b.anchor, b.cursor), max(b.anchor, b.cursor), true
}

// Insert types one rune at the caret, replacing any selection. Plain typing
// merges into the newest edit unit so a typed run can be undone in one step;
// inserting a newline or replacing a selection starts a fresh unit. The
// caret lands on the first editable boundary at or after the inserted rune,
// so typing a combining mark keeps the caret on the enlarged cluster
// instead of inside it.
func (b *textBuffer) Insert(r rune) {
	_, _, hadSelection := b.Selection()
	before := b.snapshot()
	b.deleteSelection()
	b.runes = slices.Insert(b.runes, b.cursor, r)
	b.cursor = boundaryAtOrAfter(b.boundaries(), b.cursor+1)
	b.anchor = b.cursor
	b.recordEdit(before, !hadSelection && r != '\n')
}

// Backspace removes the selection, or the whole grapheme cluster before the
// caret. Each call is its own edit unit; a no-op at the start of the buffer
// records nothing.
func (b *textBuffer) Backspace() {
	before := b.snapshot()
	if b.deleteSelection() {
		b.recordEdit(before, false)
		return
	}
	if b.cursor == 0 {
		return
	}
	start := prevTextBoundary(b.boundaries(), b.cursor)
	b.runes = slices.Delete(b.runes, start, b.cursor)
	b.cursor = start
	b.anchor = b.cursor
	b.recordEdit(before, false)
}

// Delete removes the selection, or the whole grapheme cluster after the
// caret. Each call is its own edit unit; a no-op at the end of the buffer
// records nothing.
func (b *textBuffer) Delete() {
	before := b.snapshot()
	if b.deleteSelection() {
		b.recordEdit(before, false)
		return
	}
	if b.cursor >= len(b.runes) {
		return
	}
	end := nextTextBoundary(b.boundaries(), b.cursor)
	b.runes = slices.Delete(b.runes, b.cursor, end)
	b.anchor = b.cursor
	b.recordEdit(before, false)
}

// Move shifts the cursor by delta editable boundaries (whole grapheme
// clusters, plus whatever the shaped-cluster refinement adds), clamped to
// [0, len]. A non-extending move collapses any selection onto the caret; an
// extending one keeps the anchor so the selection grows or shrinks cluster
// by cluster. Any move ends the current merge group, so a later insert
// starts a fresh edit unit.
func (b *textBuffer) Move(delta int, extend bool) {
	boundaries := b.boundaries()
	for steps := 0; steps < len(boundaries) && delta != 0; steps++ {
		if delta > 0 {
			b.cursor = nextTextBoundary(boundaries, b.cursor)
			delta--
		} else {
			b.cursor = prevTextBoundary(boundaries, b.cursor)
			delta++
		}
	}
	if !extend {
		b.anchor = b.cursor
	}
	b.mergeable = false
}

// Home moves the caret to the start of the buffer and ends the current merge
// group; with extend it grows the selection instead.
func (b *textBuffer) Home(extend bool) {
	b.cursor = 0
	if !extend {
		b.anchor = b.cursor
	}
	b.mergeable = false
}

// End moves the caret to the end of the buffer and ends the current merge
// group; with extend it grows the selection instead.
func (b *textBuffer) End(extend bool) {
	b.cursor = len(b.runes)
	if !extend {
		b.anchor = b.cursor
	}
	b.mergeable = false
}

// SelectAll selects the whole content and ends the current merge group.
func (b *textBuffer) SelectAll() {
	b.anchor = 0
	b.cursor = len(b.runes)
	b.mergeable = false
}

// StartSelection begins a mouse drag at index, collapsing any selection and
// ending the current merge group. The index snaps to the nearest editable
// boundary, so a drag can never anchor inside a cluster.
func (b *textBuffer) StartSelection(index int) {
	index = clampTextIndex(index, len(b.runes))
	b.cursor = snapToTextBoundary(b.boundaries(), index)
	b.anchor = b.cursor
	b.mergeable = false
}

// ExtendSelection drags the active end of a mouse selection to index; the
// anchor set by StartSelection stays put. The index snaps to the nearest
// editable boundary and the change ends the current merge group.
func (b *textBuffer) ExtendSelection(index int) {
	index = clampTextIndex(index, len(b.runes))
	b.cursor = snapToTextBoundary(b.boundaries(), index)
	b.mergeable = false
}

// Copy writes the selected text to cb. With no selection it is a no-op that
// leaves cb untouched; a nil cb fails without changing anything.
func (b *textBuffer) Copy(cb textClipboard) error {
	start, end, ok := b.Selection()
	if !ok {
		return nil
	}
	if cb == nil {
		return errNilTextClipboard
	}
	return cb.SetText(string(b.runes[start:end]))
}

// Cut copies the selection to cb and then removes it. The clipboard write
// happens first: if it fails, the text (and selection) roll back untouched.
func (b *textBuffer) Cut(cb textClipboard) error {
	start, end, ok := b.Selection()
	if !ok {
		return nil
	}
	if cb == nil {
		return errNilTextClipboard
	}
	if err := cb.SetText(string(b.runes[start:end])); err != nil {
		return err
	}
	before := b.snapshot()
	b.runes = slices.Delete(b.runes, start, end)
	b.cursor = start
	b.anchor = b.cursor
	b.recordEdit(before, false)
	return nil
}

// Paste reads clipboard text and inserts it at the caret, replacing any
// selection. A nil cb or a read error leaves the buffer unchanged.
func (b *textBuffer) Paste(cb textClipboard) error {
	return b.paste(cb, false)
}

// PasteNormalized pastes with single-line CR/LF normalization. Paste retains
// the shared buffer's original behavior for multi-line consumers.
func (b *textBuffer) PasteNormalized(cb textClipboard) error {
	return b.paste(cb, true)
}

func (b *textBuffer) paste(cb textClipboard, normalize bool) error {
	if cb == nil {
		return errNilTextClipboard
	}
	text, err := cb.Text()
	if err != nil {
		return err
	}
	if normalize {
		text = normalizeSingleLine(text)
	}
	before := b.snapshot()
	b.deleteSelection()
	inserted := []rune(text)
	b.runes = slices.Insert(b.runes, b.cursor, inserted...)
	// The caret advances past the pasted text and snaps forward to an
	// editable boundary: pasted text ending before a combining mark, or a
	// paste whose last rune attaches to what followed, still leaves the
	// caret on a cluster edge.
	b.cursor = boundaryAtOrAfter(b.boundaries(), b.cursor+len(inserted))
	b.anchor = b.cursor
	b.recordEdit(before, false)
	return nil
}

func normalizeSingleLine(value string) string {
	normalized := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '\r':
			if index+1 < len(value) && value[index+1] == '\n' {
				index++
			}
			normalized = append(normalized, ' ')
		case '\n':
			normalized = append(normalized, ' ')
		default:
			normalized = append(normalized, value[index])
		}
	}
	return string(normalized)
}

// wordRuneClass is the grouping used by the word-level operations. Word
// constituents are Unicode letters and digits — CJK ideographs count as
// letters, so "abc中文def" is a single word — while whitespace forms its own
// group and every remaining rune (punctuation, symbols) forms a third.
type wordRuneClass int

const (
	wordRuneOther wordRuneClass = iota
	wordRuneSpace
	wordRuneLetter
)

func classifyWordRune(r rune) wordRuneClass {
	switch {
	case unicode.IsLetter(r), unicode.IsDigit(r):
		return wordRuneLetter
	case unicode.IsSpace(r):
		return wordRuneSpace
	default:
		return wordRuneOther
	}
}

// MoveWord shifts the cursor across word groups: positive delta crosses
// groups forward, negative backward, one group per unit of delta and
// clamped at both ends of the buffer. The landing spot snaps to the nearest
// editable boundary, so word moves never park inside a cluster. A
// non-extending move collapses any selection onto the caret; an extending
// one keeps the anchor so the selection grows group by group. Any move ends
// the current merge group.
func (b *textBuffer) MoveWord(delta int, extend bool) {
	for delta > 0 {
		b.skipWordGroupForward()
		delta--
	}
	for delta < 0 {
		b.skipWordGroupBackward()
		delta++
	}
	b.cursor = snapToTextBoundary(b.boundaries(), b.cursor)
	if !extend {
		b.anchor = b.cursor
	}
	b.mergeable = false
}

// skipWordGroupForward moves the caret to the end of the group starting at
// it — the run of same-class runes the cursor sits on — never past the end
// of the buffer.
func (b *textBuffer) skipWordGroupForward() {
	if b.cursor >= len(b.runes) {
		return
	}
	class := classifyWordRune(b.runes[b.cursor])
	for b.cursor < len(b.runes) && classifyWordRune(b.runes[b.cursor]) == class {
		b.cursor++
	}
}

// skipWordGroupBackward moves the caret to the start of the group ending
// at it, clamped at zero.
func (b *textBuffer) skipWordGroupBackward() {
	if b.cursor <= 0 {
		return
	}
	class := classifyWordRune(b.runes[b.cursor-1])
	for b.cursor > 0 && classifyWordRune(b.runes[b.cursor-1]) == class {
		b.cursor--
	}
}

// BackspaceWord removes the selection, or the whole word group before the
// caret, widened to whole editable boundaries so a cluster partially inside
// the group is removed whole. Each call is its own edit unit; a no-op at
// the start of the buffer records nothing.
func (b *textBuffer) BackspaceWord() {
	before := b.snapshot()
	if b.deleteSelection() {
		b.recordEdit(before, false)
		return
	}
	if b.cursor == 0 {
		return
	}
	boundaries := b.boundaries()
	start := b.cursor
	class := classifyWordRune(b.runes[start-1])
	for start > 0 && classifyWordRune(b.runes[start-1]) == class {
		start--
	}
	start = boundaryAtOrBefore(boundaries, start)
	b.runes = slices.Delete(b.runes, start, b.cursor)
	b.cursor = start
	b.anchor = b.cursor
	b.recordEdit(before, false)
}

// DeleteWord removes the selection, or the whole word group after the
// caret, widened to whole editable boundaries so a cluster partially inside
// the group is removed whole. Each call is its own edit unit; a no-op at
// the end of the buffer records nothing.
func (b *textBuffer) DeleteWord() {
	before := b.snapshot()
	if b.deleteSelection() {
		b.recordEdit(before, false)
		return
	}
	if b.cursor >= len(b.runes) {
		return
	}
	boundaries := b.boundaries()
	end := b.cursor
	class := classifyWordRune(b.runes[end])
	for end < len(b.runes) && classifyWordRune(b.runes[end]) == class {
		end++
	}
	end = boundaryAtOrAfter(boundaries, end)
	b.runes = slices.Delete(b.runes, b.cursor, end)
	b.anchor = b.cursor
	b.recordEdit(before, false)
}

// deleteSelection removes the selected runes, if any, and collapses the
// caret onto the range start. It reports whether a selection was removed.
func (b *textBuffer) deleteSelection() bool {
	start, end, ok := b.Selection()
	if !ok {
		return false
	}
	b.runes = slices.Delete(b.runes, start, end)
	b.cursor = start
	b.anchor = b.cursor
	return true
}

func clampTextIndex(index, length int) int {
	return max(0, min(index, length))
}
