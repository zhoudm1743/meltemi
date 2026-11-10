package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
	"github.com/zhoudm1743/meltemi/internal/text"
)

const (
	textAreaNaturalWidth  = 320.0
	textAreaNaturalHeight = 160.0
)

// TextAreaWidget is a multi-line text editor. Its buffer, selection and mouse
// hit testing use rune offsets, while newlines delimit the lines used for
// vertical movement and drawing.
type TextAreaWidget struct {
	binding     *string
	buffer      *multiTextBuffer
	placeholder string
	id          string
	class       string

	bounds    render.Rect
	face      *text.Face
	clipboard textClipboard
	metrics   *textAreaMetrics
	resolved  internalstyle.Resolved

	enabled                 bool
	focused                 bool
	dragging                bool
	scrollX                 float64
	scrollY                 float64
	composition             compositionState
	compositionFocusHandler func(bool)
	onError                 func(error)
}

// TextArea creates an enabled multi-line editor bound to value. A nil value
// creates an unbound editor. Direct writes through the pointer do not notify
// the component; use SetValue to synchronize both explicitly.
func TextArea(value *string) *TextAreaWidget {
	initial := ""
	if value != nil {
		initial = *value
	}
	area := &TextAreaWidget{
		binding: value,
		buffer:  newMultiTextBuffer(initial),
		enabled: true,
	}
	// Editing snaps to editable boundaries built from every line's shaped
	// clusters (see editableBoundaries).
	area.buffer.boundaryHook = func() []int { return area.editableBoundaries() }
	area.syncBinding()
	return area
}

// ID sets the stylesheet ID selector metadata and returns the editor.
func (t *TextAreaWidget) ID(value string) *TextAreaWidget {
	t.id = value
	return t
}

// Class sets the stylesheet class selector metadata and returns the editor.
func (t *TextAreaWidget) Class(value string) *TextAreaWidget {
	t.class = value
	return t
}

func (t *TextAreaWidget) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{
		Type: "TextArea", ID: t.id, Class: t.class,
		Focus: t.focused, Disabled: !t.enabled,
	}
}

func (t *TextAreaWidget) setResolved(resolved internalstyle.Resolved) {
	t.resolved = resolved
}

// Placeholder sets the text shown while the editor is empty.
func (t *TextAreaWidget) Placeholder(text string) *TextAreaWidget {
	t.placeholder = text
	return t
}

// Value returns the complete multi-line text.
func (t *TextAreaWidget) Value() string {
	return t.buffer.Value()
}

// SetValue replaces the text, updates the optional binding, and moves the
// caret to the end with no selection. Line endings are preserved.
func (t *TextAreaWidget) SetValue(value string) {
	t.buffer.SetValue(value)
	t.syncBinding()
	t.invalidateText()
	t.updateScroll()
}

// OnError sets the callback that receives clipboard operation failures
// (copy, cut and paste). A failing operation leaves the text and selection
// untouched and only reports through this callback, exactly once per
// failure; a nil callback keeps the failure silent. The callback runs on
// the UI thread inside event handling, so it may safely reenter the
// editor.
func (t *TextAreaWidget) OnError(handler func(error)) *TextAreaWidget {
	t.onError = handler
	return t
}

// reportClipboardError delivers a clipboard failure to the OnError
// callback when one is installed. The buffer has already rolled back by
// the time this runs, so the callback observes the unchanged state.
func (t *TextAreaWidget) reportClipboardError(err error) {
	if err != nil && t.onError != nil {
		t.onError(err)
	}
}

// Enabled reports whether the editor accepts focus and editing.
func (t *TextAreaWidget) Enabled() bool {
	return t.enabled
}

// SetEnabled toggles editing. Disabling immediately drops focus, cancels any
// composition and drops any in-flight mouse gesture while preserving the text and selection.
func (t *TextAreaWidget) SetEnabled(enabled bool) {
	if t.enabled == enabled {
		return
	}
	t.enabled = enabled
	if !enabled {
		t.setFocused(false)
		t.dragging = false
	}
}

// Undo restores the previous edit state and synchronizes the optional binding.
func (t *TextAreaWidget) Undo() bool {
	if !t.buffer.Undo() {
		return false
	}
	t.syncBinding()
	t.invalidateText()
	t.updateScroll()
	return true
}

// Redo restores the next undone edit state and synchronizes the binding.
func (t *TextAreaWidget) Redo() bool {
	if !t.buffer.Redo() {
		return false
	}
	t.syncBinding()
	t.invalidateText()
	t.updateScroll()
	return true
}

// Cursor returns the caret rune offset. Like every editor-state method it is
// confined to the UI thread.
func (t *TextAreaWidget) Cursor() int {
	return t.buffer.Cursor()
}

// Selection returns the selected rune range as the half-open interval
// [start, end); ok is false when the anchor and cursor coincide, and
// start == end == Cursor then.
func (t *TextAreaWidget) Selection() (start, end int, ok bool) {
	return t.buffer.Selection()
}

// SetSelection moves the anchor and caret to the given rune offsets, both
// snapped to the nearest editable boundary and clamped into the buffer, and
// keeps the caret visible. It changes no text and records no edit history —
// undo treats it as nothing — but it ends the current merge group like any
// other selection change, so the next insert starts a fresh edit unit.
func (t *TextAreaWidget) SetSelection(anchor, cursor int) {
	boundaries := t.buffer.boundaries()
	length := len(t.buffer.runes)
	t.buffer.anchor = clampTextIndex(snapToTextBoundary(boundaries, anchor), length)
	t.buffer.cursor = clampTextIndex(snapToTextBoundary(boundaries, cursor), length)
	t.buffer.mergeable = false
	t.invalidateText()
	t.updateScroll()
}

// Backspace removes the selection, or one rune before the caret, following
// the shared editing semantics of the Backspace key: a selection replacement
// records its own edit unit, a no-op at the start of the buffer records
// nothing, and the binding is synchronized afterwards.
func (t *TextAreaWidget) Backspace() {
	t.buffer.Backspace()
	t.syncBinding()
	t.invalidateText()
	t.updateScroll()
}

func (t *TextAreaWidget) measure(constraints Constraints) Size {
	style := textAreaComponentStyle(t, t.resolved)
	return clampSize(Size{Width: style.width, Height: style.height}, constraints)
}

func (t *TextAreaWidget) arrange(bounds render.Rect) {
	t.bounds = bounds
	t.updateScroll()
}

func (t *TextAreaWidget) setFace(face *text.Face) {
	if t.face == face {
		return
	}
	t.face = face
	t.invalidateText()
	t.updateScroll()
}

func (t *TextAreaWidget) setClipboard(clipboard textClipboard) {
	t.clipboard = clipboard
}

func (t *TextAreaWidget) invalidateText() {
	t.metrics = nil
}

func (t *TextAreaWidget) syncBinding() {
	if t.binding != nil {
		*t.binding = t.buffer.Value()
	}
}

type textAreaVisibleState struct {
	value   string
	cursor  int
	anchor  int
	scrollX float64
	scrollY float64
}

func (t *TextAreaWidget) visibleState() textAreaVisibleState {
	return textAreaVisibleState{
		value:   t.buffer.Value(),
		cursor:  t.buffer.cursor,
		anchor:  t.buffer.anchor,
		scrollX: t.scrollX,
		scrollY: t.scrollY,
	}
}

func (t *TextAreaWidget) handleEvent(e driver.Event) (handled, redraw bool) {
	if !t.enabled {
		return false, false
	}
	before := t.visibleState()
	handled = t.handleTextAreaEvent(e)
	if !handled {
		return false, false
	}
	if before.value != t.buffer.Value() {
		t.syncBinding()
		t.invalidateText()
	}
	t.updateScroll()
	return true, before != t.visibleState()
}

func (t *TextAreaWidget) handleTextAreaEvent(e driver.Event) bool {
	switch e.Kind {
	case driver.EventRune:
		if !t.focused {
			return false
		}
		if e.Rune == '\n' || e.Rune == '\r' {
			t.buffer.InsertNewline()
		} else {
			t.buffer.Insert(e.Rune)
		}
		return true
	case driver.EventKey:
		return t.handleKey(e)
	case driver.EventMouseMove:
		if !t.dragging {
			return false
		}
		t.buffer.ExtendSelection(t.runeIndexAt(e.Position))
		return true
	case driver.EventMouseButton:
		return t.handleMouseButton(e)
	default:
		return false
	}
}

func (t *TextAreaWidget) handleKey(e driver.Event) bool {
	if !t.focused || (e.Action != driver.ActionPress && e.Action != driver.ActionRepeat) {
		return false
	}
	if e.Mods&driver.ModCtrl != 0 {
		// Word- and document-level shortcuts would move the caret or mutate
		// the buffer underneath an active preedit; while a composition is
		// running they belong to the IME and are left unconsumed.
		if t.composition.active {
			return false
		}
		extend := e.Mods&driver.ModShift != 0
		switch e.Key {
		case driver.KeyA:
			t.buffer.SelectAll()
		case driver.KeyC:
			t.reportClipboardError(t.buffer.Copy(t.clipboard))
		case driver.KeyX:
			t.reportClipboardError(t.buffer.Cut(t.clipboard))
		case driver.KeyV:
			t.reportClipboardError(t.buffer.Paste(t.clipboard))
		case driver.KeyZ:
			if e.Mods&driver.ModShift != 0 {
				t.buffer.Redo()
			} else {
				t.buffer.Undo()
			}
		case driver.KeyY:
			t.buffer.Redo()
		case driver.KeyLeft:
			t.buffer.MoveWord(-1, extend)
		case driver.KeyRight:
			t.buffer.MoveWord(1, extend)
		case driver.KeyBackspace:
			t.buffer.BackspaceWord()
		case driver.KeyDelete:
			t.buffer.DeleteWord()
		case driver.KeyHome:
			t.buffer.DocumentHome(extend)
		case driver.KeyEnd:
			t.buffer.DocumentEnd(extend)
		default:
			return false
		}
		return true
	}

	// PageUp/PageDown belong to the IME while a composition is running.
	if t.composition.active {
		return false
	}
	extend := e.Mods&driver.ModShift != 0
	switch e.Key {
	case driver.KeyEnter:
		t.buffer.InsertNewline()
	case driver.KeyLeft:
		t.buffer.Move(-1, extend)
	case driver.KeyRight:
		t.buffer.Move(1, extend)
	case driver.KeyUp:
		t.buffer.MoveVertical(-1, extend)
	case driver.KeyDown:
		t.buffer.MoveVertical(1, extend)
	case driver.KeyPageUp:
		t.buffer.MovePage(-1, t.pageLineCount(), extend)
	case driver.KeyPageDown:
		t.buffer.MovePage(1, t.pageLineCount(), extend)
	case driver.KeyHome:
		t.buffer.Home(extend)
	case driver.KeyEnd:
		t.buffer.End(extend)
	case driver.KeyBackspace:
		t.buffer.Backspace()
	case driver.KeyDelete:
		t.buffer.Delete()
	default:
		return false
	}
	return true
}

// activate is the eventRouter's focused-Enter entry point. The router does
// not also forward that key to handleEvent, so the newline is inserted once.
func (t *TextAreaWidget) activate() {
	if !t.enabled || !t.focused {
		return
	}
	t.buffer.InsertNewline()
	t.syncBinding()
	t.invalidateText()
	t.updateScroll()
}

func (t *TextAreaWidget) handleMouseButton(e driver.Event) bool {
	if e.Button != driver.MouseButtonLeft {
		return false
	}
	switch e.Action {
	case driver.ActionPress:
		if !boundsContain(t.bounds, e.Position) {
			return false
		}
		t.dragging = true
		t.buffer.StartSelection(t.runeIndexAt(e.Position))
		return true
	case driver.ActionRelease:
		if !t.dragging {
			return false
		}
		t.buffer.ExtendSelection(t.runeIndexAt(e.Position))
		t.dragging = false
		return true
	default:
		return false
	}
}

func (t *TextAreaWidget) walk(visit func(component)) {
	visit(t)
}

func (t *TextAreaWidget) isUIComponent() {}

// arrangedBounds returns the rect recorded by the last arrange.
func (t *TextAreaWidget) arrangedBounds() render.Rect { return t.bounds }

func (t *TextAreaWidget) acceptsPointer() bool { return t.enabled }

func (t *TextAreaWidget) focusable() bool { return t.enabled }

func (t *TextAreaWidget) isFocused() bool { return t.focused }

func (t *TextAreaWidget) setFocused(focused bool) {
	wasFocused := t.focused
	hadComposition := t.composition.active
	t.focused = focused && t.enabled
	if !t.focused {
		t.clearComposition()
	}
	if t.compositionFocusHandler != nil && (wasFocused != t.focused || (!t.focused && hadComposition)) {
		t.compositionFocusHandler(t.focused)
	}
}
