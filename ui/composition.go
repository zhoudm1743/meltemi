package ui

import "github.com/zhoudm1743/meltemi/internal/text"

type compositionState struct {
	text   string
	caret  int
	active bool
}

func newCompositionState(value string, caret int, active bool) compositionState {
	if !active {
		return compositionState{}
	}
	runes := []rune(value)
	for index, r := range runes {
		if r == '\r' || r == '\n' {
			runes[index] = ' '
		}
	}
	// The IME moves the preedit caret by runes; snap it to an editable
	// boundary so a caret inside a combining sequence or ZWJ cluster is
	// never drawn or reported back to the IME.
	caret = snapToTextBoundary(graphemeBoundaries(runes), caret)
	return compositionState{
		text: string(runes), caret: clampTextIndex(caret, len(runes)), active: true,
	}
}

type compositionTarget interface {
	component
	setComposition(string, int, bool) bool
	clearComposition() bool
	compositionSpot() (int, int, bool)
}

type compositionFocusReceiver interface {
	setCompositionFocusHandler(func(bool))
}

func setTreeCompositionFocusHandler(root component, handler func(bool)) {
	if root == nil {
		return
	}
	root.walk(func(c component) {
		if receiver, ok := c.(compositionFocusReceiver); ok {
			receiver.setCompositionFocusHandler(handler)
		}
	})
}

func detachTreeComposition(root component) {
	if root == nil {
		return
	}
	setTreeCompositionFocusHandler(root, nil)
	root.walk(func(c component) {
		if focused, ok := c.(focusableComponent); ok && focused.isFocused() {
			focused.setFocused(false)
		}
	})
}

// compositionTextMetrics shapes the composed display through Face.Layout
// and returns the bitmap, the caret offsets and the editable boundaries of
// the composed text. With no face (or a layout failure) the offsets stay
// zero and the boundaries fall back to the grapheme subset, mirroring the
// committed-text metrics.
func compositionTextMetrics(face *text.Face, value string) (text.Bitmap, []float64, []int) {
	var bitmap text.Bitmap
	runes := []rune(value)
	offsets := make([]float64, len(runes)+1)
	boundaries := graphemeBoundaries(runes)
	if face == nil {
		return bitmap, offsets, boundaries
	}
	if layout, err := face.Layout(value); err == nil {
		bitmap = layout.Bitmap
		for index, offset := range layout.CaretOffsets {
			offsets[index] = float64(offset) / 64
		}
		boundaries = shapedTextBoundaries(runes, layout.Clusters)
	}
	return bitmap, offsets, boundaries
}

func (i *InputWidget) setCompositionFocusHandler(handler func(bool)) {
	i.compositionFocusHandler = handler
}

func (i *InputWidget) setComposition(value string, caret int, active bool) bool {
	next := newCompositionState(value, caret, active)
	if i.composition == next {
		return false
	}
	i.composition = next
	return true
}

func (i *InputWidget) clearComposition() bool {
	return i.setComposition("", 0, false)
}

// composedDisplay splices the IME preedit runes into the committed text at
// the cursor. The preedit is shown verbatim, including in password fields:
// masking applies only to committed runes (see InputWidget.password and
// displayValue). This follows the Linux Fcitx/IBus convention that the
// in-composition string is visible plaintext — it has not been committed
// and is routinely shown by other toolkits' password entries too — unlike
// macOS secure fields, which mask the preedit. Applications that treat
// every visible glyph as sensitive should be aware of this platform choice.
func (i *InputWidget) composedDisplay() (value string, start, end, caret int, active bool) {
	committed := []rune(i.displayValue())
	cursor := clampTextIndex(i.buffer.cursor, len(committed))
	if !i.composition.active {
		return string(committed), cursor, cursor, cursor, false
	}
	preedit := []rune(i.composition.text)
	composed := make([]rune, 0, len(committed)+len(preedit))
	composed = append(composed, committed[:cursor]...)
	composed = append(composed, preedit...)
	composed = append(composed, committed[cursor:]...)
	return string(composed), cursor, cursor + len(preedit), cursor + i.composition.caret, true
}

func (i *InputWidget) compositionSpot() (int, int, bool) {
	if !i.focused || !i.enabled {
		return 0, 0, false
	}
	style := i.currentComponentStyle()
	content := styleContentRect(i.bounds, style)
	return int(i.caretXWithStyle(style)), int(content.Y + content.Height), true
}

func (t *TextAreaWidget) setCompositionFocusHandler(handler func(bool)) {
	t.compositionFocusHandler = handler
}

func (t *TextAreaWidget) setComposition(value string, caret int, active bool) bool {
	next := newCompositionState(value, caret, active)
	if t.composition == next {
		return false
	}
	t.composition = next
	return true
}

func (t *TextAreaWidget) clearComposition() bool {
	return t.setComposition("", 0, false)
}

func (t *TextAreaWidget) composedLine(line int) (value string, start, end, caret int, active bool) {
	lines := t.buffer.Lines()
	line = clampTextIndex(line, len(lines)-1)
	committed := []rune(lines[line])
	cursorLine := t.buffer.LineIndex(t.buffer.cursor)
	column := clampTextIndex(t.buffer.Column(t.buffer.cursor), len(committed))
	if !t.composition.active || line != cursorLine {
		return string(committed), column, column, column, false
	}
	preedit := []rune(t.composition.text)
	composed := make([]rune, 0, len(committed)+len(preedit))
	composed = append(composed, committed[:column]...)
	composed = append(composed, preedit...)
	composed = append(composed, committed[column:]...)
	return string(composed), column, column + len(preedit), column + t.composition.caret, true
}

func (t *TextAreaWidget) compositionSpot() (int, int, bool) {
	if !t.focused || !t.enabled {
		return 0, 0, false
	}
	caret := t.caretRectWithStyle(t.currentComponentStyle())
	return int(caret.X), int(caret.Y + caret.Height), true
}
