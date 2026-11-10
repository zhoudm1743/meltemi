package ui

import (
	"strings"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
	"github.com/zhoudm1743/meltemi/internal/text"
)

const (
	inputNaturalWidth = 200.0
	inputHeight       = 36.0
)

// InputWidget is a single-line text editor. Its value and selection always
// contain the real text; password masking affects only drawing and metrics.
type InputWidget struct {
	binding     *string
	buffer      *textBuffer
	placeholder string
	password    bool
	id          string
	class       string

	bounds    render.Rect
	face      *text.Face
	clipboard textClipboard
	metrics   *inputMetrics
	resolved  internalstyle.Resolved

	enabled                 bool
	focused                 bool
	dragging                bool
	scrollX                 float64
	composition             compositionState
	compositionFocusHandler func(bool)
	onError                 func(error)
}

// Input creates an enabled single-line input bound to value. A nil value
// creates an unbound input. Direct writes through the pointer do not notify
// the component; use SetValue to synchronize both explicitly.
func Input(value *string) *InputWidget {
	return newInput(value, false)
}

// Password creates an enabled input whose drawing masks every rune with *.
// Masking covers the committed text only: while an IME composition is
// active, the preedit string is drawn in plaintext, matching the Linux
// Fcitx/IBus convention (see InputWidget.composedDisplay for the rationale
// and the macOS secure-field difference).
func Password(value *string) *InputWidget {
	return newInput(value, true)
}

func newInput(value *string, password bool) *InputWidget {
	initial := ""
	if value != nil {
		initial = *value
	}
	input := &InputWidget{
		binding:  value,
		buffer:   newTextBuffer(normalizeSingleLine(initial)),
		password: password,
		enabled:  true,
	}
	// The buffer edits by editable boundaries: grapheme clusters refined
	// with the shaped clusters of the displayed text (masked 1:1 for a
	// password, so the real text's graphemes rule underneath the mask).
	input.buffer.boundaryHook = func() []int { return input.editableBoundaries() }
	input.syncBinding()
	return input
}

// ID sets the stylesheet ID selector metadata and returns the input.
func (i *InputWidget) ID(value string) *InputWidget {
	i.id = value
	return i
}

// Class sets the stylesheet class selector metadata and returns the input.
func (i *InputWidget) Class(value string) *InputWidget {
	i.class = value
	return i
}

func (i *InputWidget) styleMeta() internalstyle.Meta {
	typeName := "Input"
	if i.password {
		typeName = "Password"
	}
	return internalstyle.Meta{
		Type: typeName, ID: i.id, Class: i.class,
		Focus: i.focused, Disabled: !i.enabled,
	}
}

func (i *InputWidget) setResolved(resolved internalstyle.Resolved) {
	i.resolved = resolved
}

// Placeholder sets the text shown while the input is empty.
func (i *InputWidget) Placeholder(text string) *InputWidget {
	i.placeholder = text
	return i
}

// Value returns the real, unmasked text.
func (i *InputWidget) Value() string {
	return i.buffer.Value()
}

// SetValue replaces the text, updates the optional binding, and moves the
// caret to the end with no selection. CRLF becomes one space; any other CR
// or LF becomes one space.
func (i *InputWidget) SetValue(value string) {
	i.buffer.SetValue(normalizeSingleLine(value))
	i.syncBinding()
	i.invalidateText()
	i.updateScroll()
}

// Undo restores the previous edit state and synchronizes the optional binding.
func (i *InputWidget) Undo() bool {
	if !i.buffer.Undo() {
		return false
	}
	i.syncBinding()
	i.invalidateText()
	i.updateScroll()
	return true
}

// Redo restores the next undone edit state and synchronizes the binding.
func (i *InputWidget) Redo() bool {
	if !i.buffer.Redo() {
		return false
	}
	i.syncBinding()
	i.invalidateText()
	i.updateScroll()
	return true
}

// OnError sets the callback that receives clipboard operation failures
// (copy, cut and paste). A failing operation leaves the text and selection
// untouched and only reports through this callback, exactly once per
// failure; a nil callback keeps the failure silent. The callback runs on
// the UI thread inside event handling, so it may safely reenter the input.
func (i *InputWidget) OnError(handler func(error)) *InputWidget {
	i.onError = handler
	return i
}

// reportClipboardError delivers a clipboard failure to the OnError
// callback when one is installed. The buffer has already rolled back by
// the time this runs, so the callback observes the unchanged state.
func (i *InputWidget) reportClipboardError(err error) {
	if err != nil && i.onError != nil {
		i.onError(err)
	}
}

// Enabled reports whether the input accepts focus and editing.
func (i *InputWidget) Enabled() bool {
	return i.enabled
}

// SetEnabled toggles editing. Disabling immediately drops focus, cancels any
// composition and drops any in-flight mouse gesture while preserving the text and selection.
func (i *InputWidget) SetEnabled(enabled bool) {
	if i.enabled == enabled {
		return
	}
	i.enabled = enabled
	if !enabled {
		i.setFocused(false)
		i.dragging = false
	}
}

func (i *InputWidget) measure(constraints Constraints) Size {
	style := inputComponentStyle(i, i.resolved)
	return clampSize(Size{Width: style.width, Height: style.height}, constraints)
}

func (i *InputWidget) arrange(bounds render.Rect) {
	i.bounds = bounds
	i.updateScroll()
}

func (i *InputWidget) setFace(face *text.Face) {
	if i.face == face {
		return
	}
	i.face = face
	i.invalidateText()
	i.updateScroll()
}

func (i *InputWidget) setClipboard(clipboard textClipboard) {
	i.clipboard = clipboard
}

func (i *InputWidget) invalidateText() {
	i.metrics = nil
}

func (i *InputWidget) displayValue() string {
	if i.password {
		return strings.Repeat("*", len(i.buffer.runes))
	}
	return i.buffer.Value()
}

func (i *InputWidget) syncBinding() {
	if i.binding != nil {
		*i.binding = i.buffer.Value()
	}
}

type inputVisibleState struct {
	value   string
	cursor  int
	anchor  int
	scrollX float64
}

func (i *InputWidget) visibleState() inputVisibleState {
	return inputVisibleState{
		value:   i.buffer.Value(),
		cursor:  i.buffer.cursor,
		anchor:  i.buffer.anchor,
		scrollX: i.scrollX,
	}
}

func (i *InputWidget) handleEvent(e driver.Event) (handled, redraw bool) {
	if !i.enabled {
		return false, false
	}
	before := i.visibleState()
	handled = i.handleInputEvent(e)
	if !handled {
		return false, false
	}
	if before.value != i.buffer.Value() {
		i.syncBinding()
		i.invalidateText()
	}
	i.updateScroll()
	return true, before != i.visibleState()
}

func (i *InputWidget) handleInputEvent(e driver.Event) bool {
	switch e.Kind {
	case driver.EventRune:
		if !i.focused || e.Rune == '\n' || e.Rune == '\r' {
			return false
		}
		i.buffer.Insert(e.Rune)
		return true
	case driver.EventKey:
		return i.handleKey(e)
	case driver.EventMouseMove:
		if !i.dragging {
			return false
		}
		i.buffer.ExtendSelection(i.runeIndexAt(e.Position))
		return true
	case driver.EventMouseButton:
		return i.handleMouseButton(e)
	default:
		return false
	}
}

func (i *InputWidget) handleKey(e driver.Event) bool {
	if !i.focused || (e.Action != driver.ActionPress && e.Action != driver.ActionRepeat) {
		return false
	}
	if e.Mods&driver.ModCtrl != 0 {
		// Word-level shortcuts would move the caret or mutate the buffer
		// underneath an active preedit; while a composition is running they
		// belong to the IME and are left unconsumed.
		if i.composition.active {
			return false
		}
		extend := e.Mods&driver.ModShift != 0
		switch e.Key {
		case driver.KeyA:
			i.buffer.SelectAll()
			return true
		case driver.KeyC:
			i.reportClipboardError(i.buffer.Copy(i.clipboard))
			return true
		case driver.KeyX:
			i.reportClipboardError(i.buffer.Cut(i.clipboard))
			return true
		case driver.KeyV:
			i.reportClipboardError(i.buffer.PasteNormalized(i.clipboard))
			return true
		case driver.KeyZ:
			if e.Mods&driver.ModShift != 0 {
				i.buffer.Redo()
			} else {
				i.buffer.Undo()
			}
			return true
		case driver.KeyY:
			i.buffer.Redo()
			return true
		case driver.KeyLeft:
			i.buffer.MoveWord(-1, extend)
			return true
		case driver.KeyRight:
			i.buffer.MoveWord(1, extend)
			return true
		case driver.KeyBackspace:
			i.buffer.BackspaceWord()
			return true
		case driver.KeyDelete:
			i.buffer.DeleteWord()
			return true
		default:
			return false
		}
	}

	extend := e.Mods&driver.ModShift != 0
	switch e.Key {
	case driver.KeyLeft:
		i.buffer.Move(-1, extend)
	case driver.KeyRight:
		i.buffer.Move(1, extend)
	case driver.KeyHome:
		i.buffer.Home(extend)
	case driver.KeyEnd:
		i.buffer.End(extend)
	case driver.KeyBackspace:
		i.buffer.Backspace()
	case driver.KeyDelete:
		i.buffer.Delete()
	default:
		return false
	}
	return true
}

func (i *InputWidget) handleMouseButton(e driver.Event) bool {
	if e.Button != driver.MouseButtonLeft {
		return false
	}
	switch e.Action {
	case driver.ActionPress:
		if !boundsContain(i.bounds, e.Position) {
			return false
		}
		i.dragging = true
		i.buffer.StartSelection(i.runeIndexAt(e.Position))
		return true
	case driver.ActionRelease:
		if !i.dragging {
			return false
		}
		i.buffer.ExtendSelection(i.runeIndexAt(e.Position))
		i.dragging = false
		return true
	default:
		return false
	}
}

func (i *InputWidget) walk(visit func(component)) {
	visit(i)
}

func (i *InputWidget) isUIComponent() {}

// arrangedBounds returns the rect recorded by the last arrange.
func (i *InputWidget) arrangedBounds() render.Rect { return i.bounds }

func (i *InputWidget) acceptsPointer() bool { return i.enabled }

func (i *InputWidget) focusable() bool { return i.enabled }

func (i *InputWidget) isFocused() bool { return i.focused }

func (i *InputWidget) setFocused(focused bool) {
	wasFocused := i.focused
	hadComposition := i.composition.active
	i.focused = focused && i.enabled
	if !i.focused {
		i.clearComposition()
	}
	if i.compositionFocusHandler != nil && (wasFocused != i.focused || (!i.focused && hadComposition)) {
		i.compositionFocusHandler(i.focused)
	}
}
