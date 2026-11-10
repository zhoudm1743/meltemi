package ui

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

func runeInputEvent(r rune) driver.Event {
	return driver.Event{Kind: driver.EventRune, Rune: r}
}

func arrangedInput(t *testing.T, input *InputWidget, width float64) *eventRouter {
	t.Helper()
	root := Column(input)
	setTreeFace(root, testFace(t))
	laid := root.measure(Constraints{})
	if width > 0 {
		laid.Width = width
	}
	root.arrange(rect(0, 0, laid.Width, laid.Height))
	return newEventRouter(root)
}

func requireInputSelection(t *testing.T, input *InputWidget, wantStart, wantEnd int) {
	t.Helper()
	start, end, ok := input.buffer.Selection()
	if !ok || start != wantStart || end != wantEnd {
		t.Fatalf("selection = (%d, %d, %v), want (%d, %d, true)", start, end, ok, wantStart, wantEnd)
	}
}

func requireNoInputSelection(t *testing.T, input *InputWidget, wantCursor int) {
	t.Helper()
	start, end, ok := input.buffer.Selection()
	if ok || start != wantCursor || end != wantCursor {
		t.Fatalf("selection = (%d, %d, %v), want collapsed cursor %d", start, end, ok, wantCursor)
	}
}

func TestInputTypingCursorAndSelection(t *testing.T) {
	bound := "A中🙂"
	input := Input(&bound)
	router := arrangedInput(t, input, 0)
	router.Handle(keyEvent(driver.KeyTab, 0))

	if handled, redraw := router.Handle(runeInputEvent('!')); !handled || !redraw {
		t.Fatalf("rune input = (%v, %v), want (true, true)", handled, redraw)
	}
	if got := input.Value(); got != "A中🙂!" {
		t.Fatalf("Value() after typing = %q, want %q", got, "A中🙂!")
	}
	if bound != "A中🙂!" {
		t.Fatalf("bound value after typing = %q, want updated binding", bound)
	}
	requireNoInputSelection(t, input, 4)

	router.Handle(keyEvent(driver.KeyHome, 0))
	router.Handle(keyEvent(driver.KeyRight, driver.ModShift))
	router.Handle(keyEvent(driver.KeyRight, driver.ModShift))
	requireInputSelection(t, input, 0, 2)

	// Typing replaces two whole runes, never the bytes occupied by 中.
	router.Handle(runeInputEvent('好'))
	if got := input.Value(); got != "好🙂!" {
		t.Fatalf("Value() after selection replacement = %q, want %q", got, "好🙂!")
	}
	requireNoInputSelection(t, input, 1)

	router.Handle(keyEvent(driver.KeyEnd, driver.ModShift))
	requireInputSelection(t, input, 1, 3)
	router.Handle(keyEvent(driver.KeyBackspace, 0))
	if got := input.Value(); got != "好" {
		t.Fatalf("Value() after selected Backspace = %q, want 好", got)
	}
	router.Handle(keyEvent(driver.KeyLeft, 0))
	router.Handle(keyEvent(driver.KeyDelete, 0))
	if got := input.Value(); got != "" {
		t.Fatalf("Value() after Delete = %q, want empty", got)
	}
	if bound != "" {
		t.Fatalf("bound value after editing = %q, want empty", bound)
	}
}

func TestInputSetValueAndBinding(t *testing.T) {
	account := "初始"
	input := Input(&account)
	if got := input.Placeholder("账号"); got != input {
		t.Fatal("Placeholder did not return the same InputWidget")
	}
	if got := input.Value(); got != "初始" {
		t.Fatalf("initial Value() = %q, want 初始", got)
	}
	if got := input.measure(Constraints{}); got != (Size{Width: 200, Height: 36}) {
		t.Fatalf("natural size = %v, want 200x36", got)
	}

	// Direct pointer writes do not notify the component; SetValue is the
	// explicit operation that synchronizes both sides.
	account = "外部修改"
	if got := input.Value(); got != "初始" {
		t.Fatalf("Value() after direct pointer write = %q, want retained component value", got)
	}
	input.SetValue("新账号🙂")
	if got := input.Value(); got != "新账号🙂" {
		t.Fatalf("Value() after SetValue = %q, want 新账号🙂", got)
	}
	if account != "新账号🙂" {
		t.Fatalf("bound value after SetValue = %q, want 新账号🙂", account)
	}
	requireNoInputSelection(t, input, len([]rune(account)))

	column := Column(input)
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, 320, laid.Height))
	if got := input.bounds; got.Width != 320 || got.Height != 36 {
		t.Fatalf("arranged input bounds = %+v, want width 320 and height 36", got)
	}

	// A nil binding is valid for an unbound field.
	unbound := Input(nil)
	unbound.SetValue("standalone")
	if got := unbound.Value(); got != "standalone" {
		t.Fatalf("unbound Value() = %q, want standalone", got)
	}
}

func TestInputClipboardShortcuts(t *testing.T) {
	bound := "甲乙丙"
	input := Input(&bound)
	clipboard := &fakeTextClipboard{}
	setTreeClipboard(input, clipboard)
	router := arrangedInput(t, input, 0)
	router.Handle(keyEvent(driver.KeyTab, 0))

	router.Handle(keyEvent(driver.KeyA, driver.ModCtrl))
	requireInputSelection(t, input, 0, 3)
	router.Handle(keyEvent(driver.KeyC, driver.ModCtrl))
	if clipboard.text != "甲乙丙" {
		t.Fatalf("clipboard after Ctrl+C = %q, want 甲乙丙", clipboard.text)
	}
	router.Handle(keyEvent(driver.KeyX, driver.ModCtrl))
	if input.Value() != "" || bound != "" || clipboard.text != "甲乙丙" {
		t.Fatalf("after Ctrl+X: value=%q bound=%q clipboard=%q, want empty values and copied selection", input.Value(), bound, clipboard.text)
	}

	clipboard.text = "粘贴🙂"
	router.Handle(keyEvent(driver.KeyV, driver.ModCtrl))
	if got := input.Value(); got != "粘贴🙂" {
		t.Fatalf("Value() after Ctrl+V = %q, want 粘贴🙂", got)
	}
	if bound != "粘贴🙂" {
		t.Fatalf("bound value after paste = %q, want 粘贴🙂", bound)
	}
	requireNoInputSelection(t, input, 3)

	router.Handle(keyEvent(driver.KeyHome, 0))
	router.Handle(keyEvent(driver.KeyRight, driver.ModShift))
	router.Handle(keyEvent(driver.KeyX, driver.ModCtrl))
	if got := input.Value(); got != "贴🙂" {
		t.Fatalf("Value() after cutting one rune = %q, want 贴🙂", got)
	}
	if clipboard.text != "粘" {
		t.Fatalf("clipboard after one-rune cut = %q, want 粘", clipboard.text)
	}
}

func TestInputDragSelectionAndOutsideRelease(t *testing.T) {
	input := Input(nil)
	input.SetValue("abcdef")
	otherClicks := 0
	other := Button("other").OnClick(func() { otherClicks++ })
	root := Column(input, other).Gap(10)
	setTreeFace(root, testFace(t))
	laid := root.measure(Constraints{})
	root.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(root)

	press := driver.Point{X: input.bounds.X + 2, Y: center(input.bounds).Y}
	outside := center(other.bounds)
	outside.X = input.bounds.X + input.bounds.Width + 40
	router.Handle(leftPress(press))
	if handled, _ := router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: outside}); !handled {
		t.Fatal("captured drag move was not handled by the pressed Input")
	}
	router.Handle(leftRelease(outside))
	requireInputSelection(t, input, 0, 6)
	if input.dragging {
		t.Fatal("outside release left the Input dragging")
	}
	if router.focus != input || !input.focused {
		t.Fatal("outside release changed or cleared the Input focus")
	}
	if otherClicks != 0 || other.pressed || other.hover {
		t.Fatalf("captured gesture leaked to sibling: clicks=%d pressed=%v hover=%v", otherClicks, other.pressed, other.hover)
	}
	if got := input.Value(); got != "abcdef" {
		t.Fatalf("drag changed text to %q", got)
	}

	// A reverse drag clamps to rune zero even after leaving the left edge.
	router.Handle(leftPress(driver.Point{X: input.bounds.X + input.bounds.Width - 1, Y: center(input.bounds).Y}))
	leftOutside := driver.Point{X: input.bounds.X - 50, Y: center(input.bounds).Y}
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: leftOutside})
	router.Handle(leftRelease(leftOutside))
	requireInputSelection(t, input, 0, 6)
	if input.buffer.Cursor() != 0 || input.buffer.anchor != 6 {
		t.Fatalf("reverse drag cursor/anchor = %d/%d, want 0/6", input.buffer.Cursor(), input.buffer.anchor)
	}
}

func TestInputDisabledAndFocus(t *testing.T) {
	first := Input(nil)
	first.SetValue("a")
	second := Input(nil)
	second.SetValue("b")
	root := Column(first, second).Gap(8)
	setTreeFace(root, testFace(t))
	laid := root.measure(Constraints{})
	root.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(root)

	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != first || !first.focused {
		t.Fatal("first Input did not receive focus")
	}
	press := driver.Point{X: first.bounds.X + 2, Y: center(first.bounds).Y}
	router.Handle(leftPress(press))
	if !first.dragging {
		t.Fatal("press did not start an Input gesture")
	}
	first.SetEnabled(false)
	if first.Enabled() || first.focused || first.dragging {
		t.Fatalf("disabled Input state: enabled=%v focused=%v dragging=%v", first.Enabled(), first.focused, first.dragging)
	}
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: driver.Point{X: first.bounds.X + 150, Y: center(first.bounds).Y}})
	router.Handle(leftRelease(driver.Point{X: first.bounds.X + 150, Y: center(first.bounds).Y}))
	router.Handle(runeInputEvent('x'))
	router.Handle(keyEvent(driver.KeyBackspace, 0))
	if got := first.Value(); got != "a" {
		t.Fatalf("disabled Input was edited to %q", got)
	}
	if router.focus != nil {
		t.Fatal("stale disabled focus survived keyboard input")
	}

	router.Handle(leftPress(center(first.bounds)))
	if router.focus != nil {
		t.Fatal("disabled Input took focus by mouse")
	}
	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != second || !second.focused {
		t.Fatal("Tab did not skip the disabled Input")
	}
	router.Handle(runeInputEvent('y'))
	if got := second.Value(); got != "by" {
		t.Fatalf("focused second Input value = %q, want by", got)
	}

	first.SetEnabled(true)
	if router.focus != second || first.focused {
		t.Fatal("re-enabling resurrected stale focus")
	}
	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != first || !first.focused {
		t.Fatal("normal focus cycling did not reach the re-enabled Input")
	}
}

func TestPasswordMaskAndClipboard(t *testing.T) {
	bound := "密🙂abc"
	input := Password(&bound).Placeholder("密码")
	if got := input.Value(); got != "密🙂abc" {
		t.Fatalf("Password Value() = %q, want the real secret", got)
	}
	if got := input.displayValue(); got != "*****" {
		t.Fatalf("Password display value = %q, want five mask runes", got)
	}
	clipboard := &fakeTextClipboard{}
	setTreeClipboard(input, clipboard)
	router := arrangedInput(t, input, 0)
	router.Handle(keyEvent(driver.KeyTab, 0))
	router.Handle(keyEvent(driver.KeyA, driver.ModCtrl))
	router.Handle(keyEvent(driver.KeyC, driver.ModCtrl))
	if clipboard.text != "密🙂abc" {
		t.Fatalf("Password clipboard = %q, want real selected text", clipboard.text)
	}

	painter := &fakePainter{}
	input.draw(&drawContext{face: input.face, painter: painter})
	for _, op := range painter.ops {
		if op.kind == "text" && op.text != "*****" {
			t.Errorf("Password painted text %q, want only the mask", op.text)
		}
		if strings.Contains(op.text, "密🙂abc") {
			t.Fatal("Password paint stream leaked the real secret")
		}
	}

	router.Handle(keyEvent(driver.KeyX, driver.ModCtrl))
	if input.Value() != "" || bound != "" {
		t.Fatal("Password cut did not update the real value and binding")
	}
	clipboard.text = "新🔐"
	router.Handle(keyEvent(driver.KeyV, driver.ModCtrl))
	if input.Value() != "新🔐" || bound != "新🔐" {
		t.Fatalf("Password paste: value=%q bound=%q, want 新🔐", input.Value(), bound)
	}
	if got := input.displayValue(); got != "**" {
		t.Fatalf("Password mask after paste = %q, want two runes", got)
	}
}

func TestInputPlaceholderAndHorizontalScroll(t *testing.T) {
	input := Input(nil).Placeholder("账号")
	router := arrangedInput(t, input, 90)
	router.Handle(keyEvent(driver.KeyTab, 0))

	painter := &fakePainter{}
	input.draw(&drawContext{face: input.face, painter: painter})
	foundPlaceholder := false
	for _, op := range painter.ops {
		if op.kind == "text" && op.text == "账号" {
			foundPlaceholder = true
		}
	}
	if !foundPlaceholder {
		t.Fatal("empty Input did not paint its placeholder")
	}

	input.SetValue("一段很长的账号文本🙂🙂🙂🙂🙂🙂🙂🙂")
	if input.scrollX <= 0 {
		t.Fatal("moving the caret to the end of long text did not scroll horizontally")
	}
	content := input.contentRect()
	caretX := input.caretX()
	if caretX < content.X || caretX > content.X+content.Width {
		t.Fatalf("scrolled caret X = %v, want inside content [%v, %v]", caretX, content.X, content.X+content.Width)
	}

	router.Handle(keyEvent(driver.KeyHome, 0))
	if input.scrollX != 0 {
		t.Fatalf("scrollX after Home = %v, want 0", input.scrollX)
	}
	router.Handle(keyEvent(driver.KeyEnd, 0))
	if input.scrollX <= 0 {
		t.Fatal("End did not reveal the caret in long text")
	}

	painter.ops = nil
	input.SetValue("")
	input.draw(&drawContext{face: input.face, painter: painter})
	for _, op := range painter.ops {
		if op.kind == "text" && op.text != "账号" {
			t.Errorf("empty Input painted %q instead of only its placeholder", op.text)
		}
	}
}

func TestClipboardUnavailable(t *testing.T) {
	bound := "保持🙂"
	input := Input(&bound)
	setTreeClipboard(input, nil)
	router := arrangedInput(t, input, 0)
	router.Handle(keyEvent(driver.KeyTab, 0))
	router.Handle(keyEvent(driver.KeyA, driver.ModCtrl))
	for _, key := range []driver.Key{driver.KeyC, driver.KeyX, driver.KeyV} {
		if handled, _ := router.Handle(keyEvent(key, driver.ModCtrl)); !handled {
			t.Fatalf("Ctrl shortcut %v with nil clipboard was not recognized", key)
		}
		if got := input.Value(); got != "保持🙂" {
			t.Fatalf("nil clipboard shortcut %v changed value to %q", key, got)
		}
	}

	failing := &fakeTextClipboard{fail: true}
	setTreeClipboard(input, failing)
	router.Handle(keyEvent(driver.KeyX, driver.ModCtrl))
	router.Handle(keyEvent(driver.KeyV, driver.ModCtrl))
	if got := input.Value(); got != "保持🙂" {
		t.Fatalf("failing clipboard changed value to %q", got)
	}
	requireInputSelection(t, input, 0, 3)
	setTreeClipboard(input, nil)

	// Clipboard failure never disables ordinary editing.
	router.Handle(runeInputEvent('好'))
	if got := input.Value(); got != "好" {
		t.Fatalf("ordinary edit after clipboard failure = %q, want 好", got)
	}
	if bound != "好" {
		t.Fatalf("binding after ordinary edit = %q, want 好", bound)
	}
}

func TestInputSingleLineNormalization(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "CR", value: "a\rb", want: "a b"},
		{name: "LF", value: "a\nb", want: "a b"},
		{name: "CRLF", value: "a\r\nb", want: "a b"},
		{name: "mixed", value: "a\r\nb\rc\nd", want: "a b c d"},
	}
	for _, password := range []bool{false, true} {
		for _, tt := range cases {
			name := tt.name
			if password {
				name += "/Password"
			} else {
				name += "/Input"
			}
			t.Run(name, func(t *testing.T) {
				bound := tt.value
				var input *InputWidget
				if password {
					input = Password(&bound)
				} else {
					input = Input(&bound)
				}
				if got := input.Value(); got != tt.want {
					t.Errorf("initial Value() = %q, want %q", got, tt.want)
				}
				if bound != tt.want {
					t.Errorf("initial binding = %q, want normalized %q", bound, tt.want)
				}
				if password {
					if got := input.displayValue(); got != strings.Repeat("*", len([]rune(tt.want))) {
						t.Errorf("initial mask = %q, want %d stars", got, len([]rune(tt.want)))
					}
				}

				input.SetValue(tt.value)
				if got := input.Value(); got != tt.want {
					t.Errorf("SetValue Value() = %q, want %q", got, tt.want)
				}
				if bound != tt.want {
					t.Errorf("binding after SetValue = %q, want %q", bound, tt.want)
				}

				input.SetValue("")
				clipboard := &fakeTextClipboard{text: tt.value}
				setTreeClipboard(input, clipboard)
				router := arrangedInput(t, input, 0)
				router.Handle(keyEvent(driver.KeyTab, 0))
				router.Handle(keyEvent(driver.KeyV, driver.ModCtrl))
				if got := input.Value(); got != tt.want {
					t.Errorf("Value() after paste = %q, want %q", got, tt.want)
				}
				if bound != tt.want {
					t.Errorf("binding after paste = %q, want %q", bound, tt.want)
				}
				if password && strings.ContainsAny(input.displayValue(), "\r\n") {
					t.Errorf("Password mask contains a line ending: %q", input.displayValue())
				}
			})
		}
	}
}

func TestInputUndoRedoAndSingleLineInvariant(t *testing.T) {
	for _, password := range []bool{false, true} {
		name := "Input"
		if password {
			name = "Password"
		}
		t.Run(name, func(t *testing.T) {
			bound := "a\r\nb"
			var input *InputWidget
			if password {
				input = Password(&bound)
			} else {
				input = Input(&bound)
			}
			if input.Value() != "a b" || bound != "a b" {
				t.Fatalf("initial single-line normalization = %q / %q", input.Value(), bound)
			}
			if input.Undo() || input.Redo() {
				t.Fatal("fresh Input unexpectedly has undo/redo history")
			}
			router := arrangedInput(t, input, 0)
			router.Handle(keyEvent(driver.KeyTab, 0))
			router.Handle(runeInputEvent('x'))
			router.Handle(runeInputEvent('y'))
			if got := input.Value(); got != "a bxy" {
				t.Fatalf("Value() after typing = %q", got)
			}

			router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl))
			if input.Value() != "a b" || bound != "a b" {
				t.Fatalf("Ctrl+Z left value/binding %q / %q", input.Value(), bound)
			}
			router.Handle(keyEvent(driver.KeyY, driver.ModCtrl))
			if input.Value() != "a bxy" || bound != "a bxy" {
				t.Fatalf("Ctrl+Y left value/binding %q / %q", input.Value(), bound)
			}
			router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl))
			router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl|driver.ModShift))
			if input.Value() != "a bxy" {
				t.Fatalf("Ctrl+Shift+Z did not redo: %q", input.Value())
			}
			if !input.Undo() || input.Value() != "a b" || bound != "a b" {
				t.Fatalf("public Undo left value/binding %q / %q", input.Value(), bound)
			}
			if !input.Redo() || input.Value() != "a bxy" || bound != "a bxy" {
				t.Fatalf("public Redo left value/binding %q / %q", input.Value(), bound)
			}

			for _, r := range []rune{'\r', '\n'} {
				if handled, _ := router.Handle(runeInputEvent(r)); handled {
					t.Fatalf("line ending rune %q was handled by single-line input", r)
				}
			}
			if strings.ContainsAny(input.Value(), "\r\n") {
				t.Fatalf("line-ending runes violated single-line invariant: %q", input.Value())
			}
			clipboard := &fakeTextClipboard{text: "c\nd\r\ne\r\nf"}
			setTreeClipboard(input, clipboard)
			router.Handle(keyEvent(driver.KeyA, driver.ModCtrl))
			router.Handle(keyEvent(driver.KeyV, driver.ModCtrl))
			if got := input.Value(); got != "c d e f" || bound != got {
				t.Fatalf("normalized paste left value/binding %q / %q", got, bound)
			}
			if password && input.displayValue() != "*******" {
				t.Fatalf("Password mask after normalized paste = %q", input.displayValue())
			}
			router.Handle(keyEvent(driver.KeyZ, driver.ModCtrl))
			if got := input.Value(); got != "a bxy" {
				t.Fatalf("Undo after paste = %q, want prior single-line text", got)
			}
			router.Handle(keyEvent(driver.KeyY, driver.ModCtrl))
			if got := input.Value(); got != "c d e f" {
				t.Fatalf("Redo after paste = %q, want normalized paste", got)
			}
		})
	}
}

var inputTestClipboards sync.Map

func (w *uiTestTarget) Text() (string, error) {
	clipboard, ok := inputTestClipboards.Load(w)
	if !ok {
		return "", errFakeClipboard
	}
	return clipboard.(*fakeTextClipboard).Text()
}

func (w *uiTestTarget) SetText(value string) error {
	clipboard, ok := inputTestClipboards.Load(w)
	if !ok {
		return errFakeClipboard
	}
	return clipboard.(*fakeTextClipboard).SetText(value)
}

func TestInputWindowClipboardInjection(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	done := runUIAsync(a)
	bound := "窗口"
	input := Input(&bound)
	window := a.Window("clipboard").Size(240, 120).Content(input)
	target, _ := showUI(t, window, backend, factory)
	clipboard := &fakeTextClipboard{}
	inputTestClipboards.Store(target, clipboard)
	t.Cleanup(func() { inputTestClipboards.Delete(target) })

	if input.clipboard != target {
		t.Fatal("Window.Show did not inject the native target's optional Clipboard")
	}
	if err := a.Post(func() {
		window.router.Handle(keyEvent(driver.KeyTab, 0))
		window.router.Handle(keyEvent(driver.KeyA, driver.ModCtrl))
		window.router.Handle(keyEvent(driver.KeyC, driver.ModCtrl))
	}); err != nil {
		t.Fatalf("Post clipboard shortcuts: %v", err)
	}
	postBarrier(t, a)
	if clipboard.text != "窗口" {
		t.Fatalf("window clipboard after Ctrl+C = %q, want 窗口", clipboard.text)
	}

	next := Input(nil)
	window.Content(next)
	postBarrier(t, a)
	if next.clipboard != target {
		t.Fatal("Window.Content did not inject the existing target Clipboard into the replacement tree")
	}
	a.Quit()
	if err := waitUIReturn(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

var _ driver.Clipboard = (*uiTestTarget)(nil)
var _ textClipboard = (*uiTestTarget)(nil)

// TestInputWordKeys pins the Ctrl+word key bindings of a focused Input:
// Ctrl+Left/Right cross one Unicode word group at a time, Ctrl+Shift
// extends the selection across groups, and Ctrl+Backspace/Delete remove one
// group, replacing an active selection. Modifier sets without Ctrl keep
// their legacy meaning.
func TestInputWordKeys(t *testing.T) {
	bound := "hello world 中ab"
	input := Input(&bound)
	router := arrangedInput(t, input, 0)
	router.Handle(keyEvent(driver.KeyTab, 0))

	// Alt+Left carries no Ctrl: it stays the legacy one-rune move.
	router.Handle(keyEvent(driver.KeyLeft, driver.ModAlt))
	requireNoInputSelection(t, input, 14)

	// Ctrl+Left walks the group ends: "中ab" is one group (CJK is a letter),
	// then the space, then "world", then the space, then "hello".
	wantCursor := []int{12, 11, 6, 5, 0, 0}
	for i, want := range wantCursor {
		if handled, _ := router.Handle(keyEvent(driver.KeyLeft, driver.ModCtrl)); !handled {
			t.Fatalf("Ctrl+Left #%d was not handled", i+1)
		}
		if got := input.buffer.Cursor(); got != want {
			t.Fatalf("cursor after Ctrl+Left #%d = %d, want %d", i+1, got, want)
		}
	}

	// Ctrl+Right mirrors it toward the end of the buffer.
	wantCursor = []int{5, 6, 11, 12, 15, 15}
	for i, want := range wantCursor {
		router.Handle(keyEvent(driver.KeyRight, driver.ModCtrl))
		if got := input.buffer.Cursor(); got != want {
			t.Fatalf("cursor after Ctrl+Right #%d = %d, want %d", i+1, got, want)
		}
	}

	// Ctrl+Shift+Left extends the selection over whole groups.
	router.Handle(keyEvent(driver.KeyLeft, driver.ModCtrl|driver.ModShift))
	requireInputSelection(t, input, 12, 15)
	router.Handle(keyEvent(driver.KeyLeft, driver.ModCtrl|driver.ModShift))
	requireInputSelection(t, input, 11, 15)

	// Ctrl+Backspace replaces the active selection in one edit unit.
	router.Handle(keyEvent(driver.KeyBackspace, driver.ModCtrl))
	if got := input.Value(); got != "hello world" {
		t.Fatalf("Value() after Ctrl+Backspace with selection = %q, want %q", got, "hello world")
	}
	if bound != "hello world" {
		t.Fatalf("binding after Ctrl+Backspace = %q, want %q", bound, "hello world")
	}
	requireNoInputSelection(t, input, 11)

	// Ctrl+Delete removes one forward group per press: "hello", then the
	// separating space, then "world".
	router.Handle(keyEvent(driver.KeyHome, 0))
	router.Handle(keyEvent(driver.KeyDelete, driver.ModCtrl))
	if got := input.Value(); got != " world" {
		t.Fatalf("Value() after Ctrl+Delete = %q, want %q", got, " world")
	}
	router.Handle(keyEvent(driver.KeyDelete, driver.ModCtrl))
	if got := input.Value(); got != "world" {
		t.Fatalf("Value() after second Ctrl+Delete = %q, want %q", got, "world")
	}
	router.Handle(keyEvent(driver.KeyDelete, driver.ModCtrl))
	if got := input.Value(); got != "" {
		t.Fatalf("Value() after third Ctrl+Delete = %q, want empty", got)
	}
	requireNoInputSelection(t, input, 0)
	if bound != "" {
		t.Fatalf("binding after word deletions = %q, want empty", bound)
	}

	// Each word deletion is its own undo unit; undo walks them all back.
	wantUndo := []struct {
		value string
		start int
		end   int
		ok    bool
	}{
		{"world", 0, 0, false},
		{" world", 0, 0, false},
		{"hello world", 0, 0, false},
		{"hello world 中ab", 11, 15, true}, // the selection replacement restores its range
	}
	for i, want := range wantUndo {
		if !input.Undo() {
			t.Fatalf("Undo() #%d = false, want true", i+1)
		}
		if got := input.Value(); got != want.value {
			t.Fatalf("Value() after Undo #%d = %q, want %q", i+1, got, want.value)
		}
		if want.ok {
			requireInputSelection(t, input, want.start, want.end)
		} else {
			requireNoInputSelection(t, input, want.start)
		}
	}
	if input.Undo() {
		t.Error("Undo() past the word deletions = true, want false")
	}
	if !input.Redo() {
		t.Error("Redo() after undoing word edits = false, want true")
	}
}

// TestEditorClipboardErrorCallback pins the OnError contract of Input and
// TextArea: a failing clipboard operation reports its error exactly once
// through the callback, leaves the text and selection untouched, never
// disturbs the app, and stays safe when the callback reenters the editor.
// Successful operations and a nil handler stay silent.
func TestEditorClipboardErrorCallback(t *testing.T) {
	t.Run("Input", func(t *testing.T) {
		bound := "保持🙂"
		input := Input(&bound)
		failing := &fakeTextClipboard{fail: true}
		setTreeClipboard(input, failing)
		var errs []error
		reentered := false
		input.OnError(func(err error) {
			errs = append(errs, err)
			if !reentered {
				reentered = true
				// Reenter the editor mid-callback: ordinary editing must
				// stay safe and must not swallow or duplicate the report.
				input.SetValue("回调内")
				input.Undo()
			}
		})
		router := arrangedInput(t, input, 0)
		router.Handle(keyEvent(driver.KeyTab, 0))
		router.Handle(keyEvent(driver.KeyA, driver.ModCtrl))

		router.Handle(keyEvent(driver.KeyC, driver.ModCtrl))
		if len(errs) != 1 || !errors.Is(errs[0], errFakeClipboard) {
			t.Fatalf("OnError calls after failed Ctrl+C = %v, want exactly [%v]", errs, errFakeClipboard)
		}
		if got := input.Value(); got != "保持🙂" {
			t.Fatalf("Value() after failed Ctrl+C = %q, want unchanged %q", got, "保持🙂")
		}
		requireInputSelection(t, input, 0, 3)

		router.Handle(keyEvent(driver.KeyX, driver.ModCtrl))
		router.Handle(keyEvent(driver.KeyV, driver.ModCtrl))
		if len(errs) != 3 {
			t.Fatalf("OnError calls after failed Ctrl+C/X/V = %d, want 3 (one per failure)", len(errs))
		}
		for i, err := range errs {
			if !errors.Is(err, errFakeClipboard) {
				t.Errorf("OnError call %d = %v, want %v", i, err, errFakeClipboard)
			}
		}
		if got := input.Value(); got != "保持🙂" {
			t.Fatalf("Value() after failing clipboard ops = %q, want unchanged %q", got, "保持🙂")
		}
		requireInputSelection(t, input, 0, 3)
		if got := bound; got != "保持🙂" {
			t.Fatalf("binding after failing clipboard ops = %q, want unchanged", got)
		}

		// A working clipboard stays silent.
		setTreeClipboard(input, &fakeTextClipboard{})
		router.Handle(keyEvent(driver.KeyC, driver.ModCtrl))
		if len(errs) != 3 {
			t.Fatalf("OnError calls after successful Ctrl+C = %d, want still 3", len(errs))
		}
		if clipboard := input.clipboard.(*fakeTextClipboard); clipboard.text != "保持🙂" {
			t.Fatalf("clipboard after successful Ctrl+C = %q, want 保持🙂", clipboard.text)
		}

		// A nil handler reports nowhere and keeps the shortcuts handled.
		quiet := Input(nil)
		quiet.SetValue("安静")
		setTreeClipboard(quiet, &fakeTextClipboard{fail: true})
		quietRouter := arrangedInput(t, quiet, 0)
		quietRouter.Handle(keyEvent(driver.KeyTab, 0))
		quietRouter.Handle(keyEvent(driver.KeyA, driver.ModCtrl))
		if handled, _ := quietRouter.Handle(keyEvent(driver.KeyX, driver.ModCtrl)); !handled {
			t.Fatal("Ctrl+X with nil OnError and failing clipboard was not handled")
		}
		if got := quiet.Value(); got != "安静" {
			t.Fatalf("Value() with nil OnError = %q, want unchanged 安静", got)
		}
	})

	t.Run("TextArea", func(t *testing.T) {
		bound := "多行\n内容"
		area := TextArea(&bound)
		failing := &fakeTextClipboard{fail: true}
		setTreeClipboard(area, failing)
		var errs []error
		area.OnError(func(err error) { errs = append(errs, err) })
		router := arrangedTextArea(t, area, 0, 0)
		router.Handle(keyEvent(driver.KeyTab, 0))
		router.Handle(keyEvent(driver.KeyA, driver.ModCtrl))

		router.Handle(keyEvent(driver.KeyX, driver.ModCtrl))
		router.Handle(keyEvent(driver.KeyV, driver.ModCtrl))
		if len(errs) != 2 {
			t.Fatalf("TextArea OnError calls = %d, want 2 (one per failure)", len(errs))
		}
		for i, err := range errs {
			if !errors.Is(err, errFakeClipboard) {
				t.Errorf("TextArea OnError call %d = %v, want %v", i, err, errFakeClipboard)
			}
		}
		if got := area.Value(); got != "多行\n内容" {
			t.Fatalf("TextArea value after failing clipboard ops = %q, want unchanged", got)
		}
		requireTextAreaSelection(t, area, 0, 5)
		if got := bound; got != "多行\n内容" {
			t.Fatalf("TextArea binding after failing clipboard ops = %q, want unchanged", got)
		}

		// Ordinary editing still works after the failures.
		router.Handle(runeInputEvent('好'))
		if got := area.Value(); got != "好" {
			t.Fatalf("TextArea value after ordinary edit = %q, want 好", got)
		}
	})
}

// TestWordEditingDuringComposition pins that the word-level and page-level
// shortcuts leave an active IME composition alone: the keys are not
// consumed, the committed text and caret stay put, and the preedit state
// survives for the IME to finish.
func TestWordEditingDuringComposition(t *testing.T) {
	t.Run("Input", func(t *testing.T) {
		bound := "hello world"
		input := Input(&bound)
		router := arrangedInput(t, input, 0)
		router.Handle(keyEvent(driver.KeyTab, 0))

		// Simulate an active composition session: the router owns the
		// composing flag and the widget carries the preedit state.
		input.composition = newCompositionState("pin", 3, true)
		router.composing = true

		for i, key := range []driver.Key{driver.KeyLeft, driver.KeyRight, driver.KeyBackspace, driver.KeyDelete} {
			if handled, _ := router.Handle(keyEvent(key, driver.ModCtrl)); handled {
				t.Fatalf("Ctrl+%v during composition #%d was handled, want ignored", key, i+1)
			}
		}
		if got := input.Value(); got != "hello world" {
			t.Fatalf("Value() after word keys during composition = %q, want unchanged", got)
		}
		requireNoInputSelection(t, input, 11)
		if !input.composition.active {
			t.Fatal("word keys during composition cleared the preedit state")
		}

		// Once the composition ends, word keys edit normally again.
		input.clearComposition()
		router.composing = false
		router.Handle(keyEvent(driver.KeyBackspace, driver.ModCtrl))
		if got := input.Value(); got != "hello " {
			t.Fatalf("Value() after Ctrl+Backspace past composition = %q, want %q", got, "hello ")
		}
	})

	t.Run("TextArea", func(t *testing.T) {
		bound := "one\ntwo three"
		area := TextArea(&bound)
		router := arrangedTextArea(t, area, 0, 0)
		router.Handle(keyEvent(driver.KeyTab, 0))

		area.composition = newCompositionState("pin", 3, true)
		router.composing = true

		for i, key := range []driver.Key{driver.KeyLeft, driver.KeyRight, driver.KeyBackspace, driver.KeyDelete, driver.KeyHome, driver.KeyEnd} {
			if handled, _ := router.Handle(keyEvent(key, driver.ModCtrl)); handled {
				t.Fatalf("Ctrl+%v during composition #%d was handled, want ignored", key, i+1)
			}
		}
		for i, key := range []driver.Key{driver.KeyPageUp, driver.KeyPageDown} {
			if handled, _ := router.Handle(keyEvent(key, 0)); handled {
				t.Fatalf("%v during composition #%d was handled, want ignored", key, i+1)
			}
		}
		if got := area.Value(); got != "one\ntwo three" {
			t.Fatalf("TextArea value after keys during composition = %q, want unchanged", got)
		}
		requireNoTextAreaSelection(t, area, 13)
		if !area.composition.active {
			t.Fatal("TextArea word keys during composition cleared the preedit state")
		}
	})
}
