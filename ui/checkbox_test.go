package ui

import (
	"strings"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

func arrangeCheckbox(t *testing.T, cb *CheckboxWidget, extra ...Component) (*eventRouter, driver.Point) {
	t.Helper()
	column := Column(append([]Component{cb}, extra...)...)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)
	return router, center(cb.bounds)
}

func TestCheckboxBindingAndCallbacks(t *testing.T) {
	// A nil binding is allowed: the checkbox keeps its own value.
	unbound := Checkbox("Accept", nil)
	if unbound.Value() {
		t.Fatal("a new checkbox with a nil binding must start unchecked")
	}
	unbound.SetValue(true)
	if !unbound.Value() {
		t.Error("SetValue(true) with a nil binding did not update the checkbox")
	}

	// A non-nil binding seeds the initial value and receives every change.
	value := false
	cb := Checkbox("Accept terms", &value)
	if cb.Value() != false {
		t.Fatal("binding with false must start unchecked")
	}
	if got := cb.Text(); got != "Accept terms" {
		t.Fatalf("Text() = %q, want %q", got, "Accept terms")
	}
	if got := cb.ID("agree").Class("field"); got != cb {
		t.Error("ID/Class did not return the checkbox itself")
	}

	var changes []bool
	cb.OnChange(func(v bool) { changes = append(changes, v) })
	cb.SetValue(true)
	if value != true {
		t.Error("SetValue(true) did not synchronize the binding pointer")
	}
	if !cb.Value() {
		t.Error("SetValue(true) did not update the checkbox value")
	}
	// Repeating the same value is not a change: no callback.
	cb.SetValue(true)
	cb.SetValue(true)
	if len(changes) != 1 || changes[0] != true {
		t.Errorf("changes after repeated SetValue(true) = %v, want [true]", changes)
	}
	cb.SetValue(false)
	if value != false || cb.Value() {
		t.Errorf("SetValue(false): binding = %v value = %v, want both false", value, cb.Value())
	}
	if want := []bool{true, false}; !equalBools(changes, want) {
		t.Errorf("changes = %v, want %v", changes, want)
	}

	// A nil handler makes changes quiet instead of panicking.
	quiet := Checkbox("Quiet", nil)
	quiet.SetValue(true)
	quiet.OnChange(nil)
	quiet.SetValue(false)
	if quiet.Value() {
		t.Error("nil OnChange changed the value")
	}
}

func TestCheckboxKeyboardAndPointer(t *testing.T) {
	value := false
	cb := Checkbox("Accept", &value)
	router, inside := arrangeCheckbox(t, cb)

	// A click is a press inside followed by a release inside; it toggles and
	// synchronizes the binding.
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: inside})
	handled, redraw := router.Handle(leftPress(inside))
	if !handled || !redraw {
		t.Errorf("press = (%v, %v), want (true, true)", handled, redraw)
	}
	if !cb.pressed {
		t.Fatal("press inside did not arm the checkbox")
	}
	handled, redraw = router.Handle(leftRelease(inside))
	if !handled || !redraw {
		t.Errorf("release = (%v, %v), want (true, true)", handled, redraw)
	}
	if value != true || !cb.Value() {
		t.Fatalf("click did not toggle: binding = %v value = %v", value, cb.Value())
	}

	// The click also took focus: Enter toggles through activate.
	handled, redraw = router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeyEnter, Action: driver.ActionPress})
	if !handled || !redraw {
		t.Errorf("Enter = (%v, %v), want (true, true)", handled, redraw)
	}
	if value != false || cb.Value() {
		t.Errorf("Enter did not toggle back: binding = %v value = %v", value, cb.Value())
	}

	// Space toggles while focused; release and auto-repeat do not.
	router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeySpace, Action: driver.ActionPress})
	if value != true {
		t.Error("Space press did not toggle the checkbox")
	}
	router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeySpace, Action: driver.ActionRepeat})
	router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeySpace, Action: driver.ActionRelease})
	if value != true {
		t.Error("Space repeat/release must not toggle again")
	}

	// Focus loss: Tab moves the focus to the button, and Space no longer
	// reaches the checkbox.
	value2 := false
	cbB := Checkbox("Agree", &value2)
	button := Button("Other")
	routerB, insideB := arrangeCheckbox(t, cbB, button)
	routerB.Handle(leftPress(insideB))
	routerB.Handle(leftRelease(insideB))
	if !cbB.focused {
		t.Fatal("click did not focus the checkbox")
	}
	afterClick := value2
	if handled, _ := routerB.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeyTab, Action: driver.ActionPress}); !handled {
		t.Fatal("Tab was not handled")
	}
	if cbB.focused || !button.focused {
		t.Fatalf("focus after Tab = (%v, %v), want (false, true)", cbB.focused, button.focused)
	}
	if handled, _ := routerB.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeySpace, Action: driver.ActionPress}); handled {
		t.Error("Space was consumed with the checkbox unfocused")
	}
	if value2 != afterClick {
		t.Error("Space toggled the checkbox after it lost focus")
	}
}

func TestCheckboxDisabledAndReleaseOutside(t *testing.T) {
	value := false
	var changes []bool
	cb := Checkbox("Accept", &value)
	cb.OnChange(func(v bool) { changes = append(changes, v) })
	router, inside := arrangeCheckbox(t, cb)
	outside := driver.Point{X: cb.bounds.X - 40, Y: cb.bounds.Y - 40}

	// Disabled: pointer, Enter and Space are all inert, and no focus or hover
	// is taken.
	cb.SetEnabled(false)
	if cb.Enabled() {
		t.Fatal("SetEnabled(false) did not disable the checkbox")
	}
	if handled, _ := router.Handle(leftPress(inside)); handled {
		t.Error("disabled checkbox consumed the press")
	}
	router.Handle(leftRelease(inside))
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: inside})
	router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeyEnter, Action: driver.ActionPress})
	router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeySpace, Action: driver.ActionPress})
	if value || len(changes) != 0 {
		t.Errorf("disabled checkbox reacted: value = %v changes = %v", value, changes)
	}
	if cb.hover || cb.focused || router.focus != nil {
		t.Errorf("disabled state leaked: hover = %v focused = %v router focus = %v", cb.hover, cb.focused, router.focus)
	}

	// Re-enabled: an inside press arms the checkbox, and an outside release
	// cancels it without toggling.
	cb.SetEnabled(true)
	router.Handle(leftPress(inside))
	if !cb.pressed {
		t.Fatal("press inside did not arm the checkbox")
	}
	router.Handle(leftRelease(outside))
	if value || len(changes) != 0 {
		t.Errorf("outside release toggled: value = %v changes = %v", value, changes)
	}
	if cb.pressed {
		t.Error("outside release left the checkbox armed")
	}

	// Disabling mid-press disarms it, so the later inside release is inert.
	router.Handle(leftPress(inside))
	cb.SetEnabled(false)
	router.Handle(leftRelease(inside))
	if value || len(changes) != 0 {
		t.Errorf("release after disable toggled: value = %v changes = %v", value, changes)
	}
	if cb.pressed {
		t.Error("disable mid-press left the checkbox armed")
	}

	// Disabling while focused drops the focus flag.
	cb.SetEnabled(true)
	router.Handle(leftPress(inside))
	router.Handle(leftRelease(inside))
	if !cb.focused {
		t.Fatal("click did not focus the checkbox")
	}
	valueBefore := value
	cb.SetEnabled(false)
	if cb.focused {
		t.Error("disabling a focused checkbox kept the focus flag")
	}
	// The router must not resurrect the old focus on the next key event.
	if handled, _ := router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeySpace, Action: driver.ActionPress}); handled {
		t.Error("Space reached the disabled checkbox through a stale focus")
	}
	if value != valueBefore {
		t.Error("stale focus toggled the disabled checkbox")
	}
}

func TestCheckboxCheckedStyle(t *testing.T) {
	face := testFace(t)

	// Default natural size: the box, a gap, and the label text; empty labels
	// collapse to the box alone.
	cb := Checkbox("Accept", nil)
	setTreeFace(cb, face)
	text := rasterTextSize(t, "Accept")
	wantSize := size(checkboxBoxSize+checkboxTextGap+text.Width, max(checkboxBoxSize, text.Height))
	if got := cb.measure(Constraints{}); got != wantSize {
		t.Errorf("measure = %v, want %v", got, wantSize)
	}
	empty := Checkbox("", nil)
	setTreeFace(empty, face)
	if got, want := empty.measure(Constraints{}), size(checkboxBoxSize, checkboxBoxSize); got != want {
		t.Errorf("empty checkbox measure = %v, want %v", got, want)
	}

	// Style metadata carries the full interactive state for CSS.
	value := false
	styled := Checkbox("Accept", &value).ID("agree").Class("field")
	meta := styled.styleMeta()
	if meta.Type != "Checkbox" || meta.ID != "agree" || meta.Class != "field" {
		t.Errorf("style meta identity = %+v, want Checkbox agree/field", meta)
	}
	if meta.Checked || meta.Hover || meta.Focus || meta.Disabled || meta.Pressed {
		t.Errorf("fresh checkbox must have no states: %+v", meta)
	}
	styled.SetValue(true)
	styled.hover, styled.focused, styled.pressed = true, true, true
	meta = styled.styleMeta()
	if !meta.Checked || !meta.Hover || !meta.Focus || !meta.Pressed {
		t.Errorf("active states missing from meta: %+v", meta)
	}
	styled.SetEnabled(false)
	if meta := styled.styleMeta(); !meta.Disabled {
		t.Errorf("disabled state missing from meta: %+v", meta)
	}

	// The checked state drives resolution and drawing: only the checked rule
	// contributes the box fill, and the check mark paints only when checked.
	sheet, err := internalstyle.Parse(strings.NewReader(
		"Checkbox { color: #102030; }\nCheckbox:checked { background: #405060; }\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	bounds := rect(0, 0, wantSize.Width, wantSize.Height)
	box := render.Rect{
		X:      bounds.X,
		Y:      bounds.Y + (bounds.Height-checkboxBoxSize)/2,
		Width:  checkboxBoxSize,
		Height: checkboxBoxSize,
	}
	mark := render.Rect{
		X:      box.X + checkboxCheckInset,
		Y:      box.Y + checkboxCheckInset,
		Width:  checkboxBoxSize - 2*checkboxCheckInset,
		Height: checkboxBoxSize - 2*checkboxCheckInset,
	}
	textAt := driver.Point{
		X: bounds.X + checkboxBoxSize + checkboxTextGap,
		Y: bounds.Y + (bounds.Height-text.Height)/2,
	}
	textColor := render.Color{R: 0x10 / 255.0, G: 0x20 / 255.0, B: 0x30 / 255.0, A: 1}
	checkedFill := render.Color{R: 0x40 / 255.0, G: 0x50 / 255.0, B: 0x60 / 255.0, A: 1}

	draw := func(checked bool) []stylePaintOp {
		drawn := Checkbox("Accept", nil)
		setTreeFace(drawn, face)
		drawn.arrange(bounds)
		drawn.SetValue(checked)
		resolved := sheet.Resolve(drawn.styleMeta(), unresolvedStyle())
		painter := &stylePainter{}
		drawn.drawStyled(&drawContext{face: face, painter: painter}, resolved)
		return painter.ops
	}

	uncheckedOps := draw(false)
	wantUnchecked := []stylePaintOp{
		{kind: "rect", rect: box, color: defaultTheme.checkboxBackground},
		{kind: "border", rect: box, color: defaultTheme.checkboxBorder, width: checkboxBorderWidth},
		{kind: "text", at: textAt, text: "Accept", color: textColor, face: face},
	}
	if !equalPaintOps(uncheckedOps, wantUnchecked) {
		t.Errorf("unchecked ops = %+v, want %+v", uncheckedOps, wantUnchecked)
	}

	checkedOps := draw(true)
	// State priority: only the highest matching level contributes, so the
	// checked rule's background replaces the base rule's color and the label
	// falls back to the theme text color — the same semantics Button has.
	wantChecked := []stylePaintOp{
		{kind: "rect", rect: box, color: checkedFill},
		{kind: "rect", rect: mark, color: defaultTheme.checkboxCheckMark},
		{kind: "border", rect: box, color: defaultTheme.checkboxBorder, width: checkboxBorderWidth},
		{kind: "text", at: textAt, text: "Accept", color: defaultTheme.buttonText, face: face},
	}
	if !equalPaintOps(checkedOps, wantChecked) {
		t.Errorf("checked ops = %+v, want %+v", checkedOps, wantChecked)
	}

	// Focused checkbox draws its box border in the focus color; a disabled
	// one takes the disabled palette.
	focused := Checkbox("Accept", nil)
	setTreeFace(focused, face)
	focused.arrange(bounds)
	focused.focused = true
	resolved := sheet.Resolve(focused.styleMeta(), unresolvedStyle())
	painter := &stylePainter{}
	focused.drawStyled(&drawContext{face: face, painter: painter}, resolved)
	border := painter.ops[1]
	if border.kind != "border" || border.color != defaultTheme.focusBorder {
		t.Errorf("focused border op = %+v, want focus-colored border", border)
	}

	disabled := Checkbox("Accept", nil)
	setTreeFace(disabled, face)
	disabled.arrange(bounds)
	disabled.SetValue(true)
	disabled.SetEnabled(false)
	// Without a stylesheet the theme palette applies; a stylesheet background
	// overriding the disabled fill is the generic merge behavior Button has.
	resolved = unresolvedStyle()
	painter = &stylePainter{}
	disabled.drawStyled(&drawContext{face: face, painter: painter}, resolved)
	if got := painter.ops[0]; got.kind != "rect" || got.color != defaultTheme.checkboxBackgroundDisabled {
		t.Errorf("disabled fill op = %+v, want disabled box fill", got)
	}
	if got := painter.ops[1]; got.kind != "rect" || got.color != defaultTheme.buttonTextDisabled {
		t.Errorf("disabled mark op = %+v, want disabled-colored mark", got)
	}
	last := painter.ops[len(painter.ops)-1]
	if last.kind != "text" || last.color != defaultTheme.buttonTextDisabled {
		t.Errorf("disabled text op = %+v, want disabled text color", last)
	}

	// Zero-size bounds paint nothing and never panic.
	collapsed := Checkbox("Accept", nil)
	setTreeFace(collapsed, face)
	collapsed.arrange(rect(0, 0, 0, 0))
	collapsed.SetValue(true)
	painter = &stylePainter{}
	collapsed.draw(&drawContext{face: face, painter: painter})
	if len(painter.ops) != 0 {
		t.Errorf("zero-size checkbox painted %d ops, want 0: %+v", len(painter.ops), painter.ops)
	}
}

func TestCheckboxFocusBorderWithStyledBorderWidth(t *testing.T) {
	sheet, err := internalstyle.Parse(strings.NewReader("Checkbox { border-width: 3px; }"))
	if err != nil {
		t.Fatalf("Parse width-only Checkbox style: %v", err)
	}
	drawBorder := func(focused bool) stylePaintOp {
		t.Helper()
		cb := Checkbox("", nil)
		cb.focused = focused
		cb.arrange(rect(0, 0, 20, 20))
		resolved := sheet.Resolve(cb.styleMeta(), unresolvedStyle())
		painter := &stylePainter{}
		cb.drawStyled(&drawContext{painter: painter}, resolved)
		if len(painter.ops) != 2 || painter.ops[1].kind != "border" {
			t.Fatalf("Checkbox paint ops = %+v, want box fill and border", painter.ops)
		}
		return painter.ops[1]
	}

	if border := drawBorder(false); border.color != defaultTheme.checkboxBorder || border.width != 3 {
		t.Errorf("unfocused width-only border = %+v, want theme border color and width 3", border)
	}
	if border := drawBorder(true); border.color != defaultTheme.focusBorder || border.width != 3 {
		t.Errorf("focused width-only border = %+v, want Button-compatible focus color and width 3", border)
	}
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalPaintOps(a, b []stylePaintOp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
