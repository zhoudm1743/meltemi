package ui

import (
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// center is the middle of an arranged rect, a safe point for synthesized
// pointer input.
func center(bounds render.Rect) driver.Point {
	return driver.Point{X: bounds.X + bounds.Width/2, Y: bounds.Y + bounds.Height/2}
}

func leftPress(p driver.Point) driver.Event {
	return driver.Event{Kind: driver.EventMouseButton, Button: driver.MouseButtonLeft, Action: driver.ActionPress, Position: p}
}

func leftRelease(p driver.Point) driver.Event {
	return driver.Event{Kind: driver.EventMouseButton, Button: driver.MouseButtonLeft, Action: driver.ActionRelease, Position: p}
}

func keyEvent(key driver.Key, mods driver.Modifiers) driver.Event {
	return driver.Event{Kind: driver.EventKey, Key: key, Action: driver.ActionPress, Mods: mods}
}

func TestKeyboardFocusAndEnter(t *testing.T) {
	var firstClicks, secondClicks, thirdClicks int
	first := Button("One").OnClick(func() { firstClicks++ })
	second := Button("Two").OnClick(func() { secondClicks++ })
	second.SetEnabled(false)
	third := Button("Three").OnClick(func() { thirdClicks++ })

	column := Column(Label("title"), first, second, third).Gap(10)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	tab := keyEvent(driver.KeyTab, 0)
	shiftTab := keyEvent(driver.KeyTab, driver.ModShift)
	enter := keyEvent(driver.KeyEnter, 0)

	// Enter with nothing focused does nothing.
	if handled, _ := router.Handle(enter); handled {
		t.Error("Enter without focus must not be handled")
	}

	// Tab walks the component tree in order and skips disabled buttons.
	router.Handle(tab)
	if router.focus != first {
		t.Fatalf("focus after first Tab = %v, want the first button", router.focus)
	}
	if !first.focused {
		t.Error("the first button must know it is focused")
	}
	router.Handle(tab)
	if router.focus != third {
		t.Fatalf("focus after second Tab = %v, want the third button (second is disabled)", router.focus)
	}
	if first.focused {
		t.Error("focus must move away from the first button")
	}

	// Enter activates exactly the focused button.
	handled, redraw := router.Handle(enter)
	if !handled || !redraw {
		t.Errorf("Enter = (%v, %v), want (true, true)", handled, redraw)
	}
	if firstClicks != 0 || thirdClicks != 1 {
		t.Errorf("clicks = (%d, %d), want (0, 1)", firstClicks, thirdClicks)
	}

	// Shift+Tab walks backwards; Tab wraps around the ends.
	router.Handle(shiftTab)
	if router.focus != first {
		t.Fatalf("focus after Shift+Tab = %v, want the first button", router.focus)
	}
	router.Handle(tab) // first -> third, skipping the disabled second
	router.Handle(tab) // third -> wraps to first
	if router.focus != first {
		t.Fatalf("focus after wrapping Tab = %v, want the first button", router.focus)
	}

	// A press moves focus to the pressed button and the following release
	// activates it with the mouse.
	router.Handle(leftPress(center(first.bounds)))
	router.Handle(leftRelease(center(first.bounds)))
	if firstClicks != 1 {
		t.Errorf("first clicks after mouse activation = %d, want 1", firstClicks)
	}
	router.Handle(leftPress(center(third.bounds)))
	router.Handle(leftRelease(center(third.bounds)))
	if thirdClicks != 2 {
		t.Errorf("third clicks = %d, want 2", thirdClicks)
	}

	// Tab continues from the mouse-focused component.
	router.Handle(tab)
	if router.focus != first {
		t.Errorf("focus after Tab from the third button = %v, want the first button", router.focus)
	}

	// Pressing empty space or a disabled button never moves focus.
	router.Handle(leftPress(driver.Point{X: -20, Y: -20}))
	if router.focus != first {
		t.Error("pressing empty space moved the focus")
	}
	router.Handle(leftPress(center(second.bounds)))
	if router.focus == second {
		t.Error("the disabled button took focus")
	}
	router.Handle(leftRelease(center(second.bounds)))
	if secondClicks != 0 {
		t.Errorf("disabled clicks = %d, want 0", secondClicks)
	}

	// Key release and unmapped keys never activate.
	if handled, _ := router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeyEnter, Action: driver.ActionRelease}); handled {
		t.Error("Enter release must not activate")
	}
	if handled, _ := router.Handle(keyEvent(driver.KeyA, 0)); handled {
		t.Error("an unmapped key must not be handled")
	}
	if firstClicks != 1 || thirdClicks != 2 || secondClicks != 0 {
		t.Errorf("clicks = (%d, %d, %d), want (1, 0, 2)", firstClicks, secondClicks, thirdClicks)
	}
}

func TestDisableThenReenableBeforeKeyDropsFocus(t *testing.T) {
	// The interleave the other tests skip: disable and re-enable the
	// focused button with no key event in between. The router's focus
	// pointer outlives the disable, and the button is focusable again —
	// but its focus flag was cleared for good, so the old focus must not
	// resurrect.
	var firstClicks, secondClicks int
	first := Button("One").OnClick(func() { firstClicks++ })
	second := Button("Two").OnClick(func() { secondClicks++ })
	column := Column(first, second).Gap(8)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != first {
		t.Fatalf("focus after Tab = %v, want the first button", router.focus)
	}

	first.SetEnabled(false)
	first.SetEnabled(true)
	if !first.focusable() {
		t.Fatal("the button must be focusable again after re-enable")
	}
	if first.focused {
		t.Error("re-enable must not resurrect the focus flag")
	}

	// Enter sees no live focus: not handled, nothing activates, and the
	// stale router focus is cleared rather than kept.
	if handled, redraw := router.Handle(keyEvent(driver.KeyEnter, 0)); handled || redraw {
		t.Errorf("Enter after re-enable = (%v, %v), want (false, false)", handled, redraw)
	}
	if router.focus != nil {
		t.Error("the stale focus survived Enter")
	}
	if firstClicks != 0 || secondClicks != 0 {
		t.Errorf("clicks = (%d, %d), want (0, 0)", firstClicks, secondClicks)
	}

	// Tab treats the tree as unfocused and lands on the first focusable
	// component — the re-enabled first button — which then works normally.
	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != first || !first.focused {
		t.Fatalf("focus after Tab = %v (focused=%v), want the first button focused", router.focus, first.focused)
	}
	router.Handle(keyEvent(driver.KeyEnter, 0))
	if firstClicks != 1 {
		t.Errorf("first clicks = %d, want 1", firstClicks)
	}
}

func TestPointerEventsMissGapAndPadding(t *testing.T) {
	// The fake leaf records every event it receives, so it counts how
	// many components a gap or padding pointer event reaches.
	top := fake("top", 60, 24)
	bottom := Button("Bottom")
	column := Column(top, bottom).Gap(40).Padding(12)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	// One point inside the 40px gap between the children, one inside the
	// 12px padding ring. Both are inside the column's own bounds, which
	// is exactly where the old broadcast leak happened.
	gap := driver.Point{X: top.bounds.X + 4, Y: top.bounds.Y + top.bounds.Height + 20}
	padding := driver.Point{X: column.bounds.X + 4, Y: column.bounds.Y + 4}
	for name, p := range map[string]driver.Point{"gap": gap, "padding": padding} {
		router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: p})
		router.Handle(leftPress(p))
		router.Handle(leftRelease(p))
		if len(top.events) != 0 {
			t.Fatalf("%s: pointer events reached %d non-target components, want 0", name, len(top.events))
		}
		if bottom.hover || bottom.pressed {
			t.Fatalf("%s: pointer events leaked into the button (hover=%v pressed=%v)", name, bottom.hover, bottom.pressed)
		}
	}
}

func TestPointerDeliveryReachesOnlyTheHitLeaf(t *testing.T) {
	// The positive half of the routing contract: a pointer squarely on a
	// component reaches exactly that component — its sibling in the same
	// column sees nothing, so delivery can be counted on one hand.
	clicks := 0
	top := fake("top", 60, 24)
	bottom := Button("Bottom").OnClick(func() { clicks++ })
	column := Column(top, bottom).Gap(40).Padding(12)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	p := center(bottom.bounds)
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: p})
	router.Handle(leftPress(p))
	router.Handle(leftRelease(p))

	if len(top.events) != 0 {
		t.Errorf("targeted pointer events reached the sibling %d times, want 0", len(top.events))
	}
	if clicks != 1 {
		t.Errorf("clicks = %d, want 1 (the hit leaf still receives its own events)", clicks)
	}
}

func TestDisableFocusedButtonDropsFocus(t *testing.T) {
	var firstClicks, secondClicks int
	first := Button("One").OnClick(func() { firstClicks++ })
	second := Button("Two").OnClick(func() { secondClicks++ })
	column := Column(first, second).Gap(8)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != first {
		t.Fatalf("focus after Tab = %v, want the first button", router.focus)
	}

	// Disabling the focused button drops its focus flag synchronously,
	// and the router clears the stale focus before consuming the next
	// key, so Enter neither activates nor reports handled.
	first.SetEnabled(false)
	if first.focused {
		t.Error("disable must clear the button's focus flag")
	}
	if handled, redraw := router.Handle(keyEvent(driver.KeyEnter, 0)); handled || redraw {
		t.Errorf("Enter after disable = (%v, %v), want (false, false)", handled, redraw)
	}
	if router.focus != nil {
		t.Error("the stale focus survived Enter")
	}
	if firstClicks != 0 {
		t.Errorf("first clicks = %d, want 0", firstClicks)
	}

	// Tab no longer starts from the disabled button; it lands on the
	// first focusable component of the tree.
	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != second {
		t.Errorf("focus after Tab = %v, want the second button", router.focus)
	}

	// Re-enabling does not resurrect the focus by itself; normal cycling
	// reaches the button again once it is focusable.
	first.SetEnabled(true)
	if router.focus != second {
		t.Errorf("focus after re-enable = %v, want it to stay on the second button", router.focus)
	}
	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != first {
		t.Fatalf("focus after Tab from the second button = %v, want the first", router.focus)
	}
	router.Handle(keyEvent(driver.KeyEnter, 0))
	if firstClicks != 1 || secondClicks != 0 {
		t.Errorf("clicks = (%d, %d), want (1, 0)", firstClicks, secondClicks)
	}
}

func TestPointerCaptureRoutesMoveAndReleaseToPressedLeaf(t *testing.T) {
	pressedClicks, otherClicks := 0, 0
	pressed := Button("pressed").OnClick(func() { pressedClicks++ })
	other := Button("other").OnClick(func() { otherClicks++ })
	column := Column(pressed, other).Gap(10)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	inside := center(pressed.bounds)
	outside := center(other.bounds)
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: inside})
	router.Handle(leftPress(inside))
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: outside})
	router.Handle(leftRelease(outside))

	if pressed.hover || pressed.pressed {
		t.Errorf("pressed leaf did not receive its captured move/release: hover=%v pressed=%v", pressed.hover, pressed.pressed)
	}
	if pressedClicks != 0 || otherClicks != 0 || other.pressed || other.hover {
		t.Fatalf("captured events leaked or clicked: clicks=(%d,%d) other pressed=%v hover=%v", pressedClicks, otherClicks, other.pressed, other.hover)
	}
}

func TestStackHitTestTopmost(t *testing.T) {
	// Overlapping siblings: a stack gives every child the same content
	// rectangle, so the declaration order decides visibility — the later
	// child paints on top and must be the only one to receive the event.
	bottomClicks, topClicks := 0, 0
	bottom := Button("bottom").OnClick(func() { bottomClicks++ })
	top := Button("top").OnClick(func() { topClicks++ })
	stack := Stack(bottom, top).Padding(8)
	setTreeFace(stack, testFace(t))
	laid := stack.measure(Constraints{})
	stack.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(stack)

	p := center(top.bounds)
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: p})
	router.Handle(leftPress(p))
	router.Handle(leftRelease(p))

	if topClicks != 1 {
		t.Errorf("top clicks = %d, want 1", topClicks)
	}
	if bottomClicks != 0 || bottom.hover || bottom.pressed {
		t.Errorf("the covered sibling received events: clicks=%d hover=%v pressed=%v, want all zero", bottomClicks, bottom.hover, bottom.pressed)
	}

	// A point in the stack's padding ring hits nothing at all.
	pad := driver.Point{X: stack.bounds.X + 2, Y: stack.bounds.Y + 2}
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: pad})
	router.Handle(leftPress(pad))
	router.Handle(leftRelease(pad))
	if topClicks != 1 || bottomClicks != 0 {
		t.Errorf("padding clicks = (%d, %d), want (1, 0)", topClicks, bottomClicks)
	}
	if top.hover || top.pressed || bottom.hover || bottom.pressed {
		t.Error("the padding ring leaked pointer state into the buttons")
	}
}

func TestStackHitTestRecursesIntoNestedContainers(t *testing.T) {
	// A column inside a stack: the recursive traversal must reach the
	// leaf through both layers and deliver to exactly one component. The
	// overlay is a plain label, which never claims pointer events, so the
	// traversal falls through to the column underneath.
	deepClicks := 0
	sibling := fake("sibling", 60, 20)
	deep := Button("deep").OnClick(func() { deepClicks++ })
	column := Column(sibling, deep).Gap(4)
	stack := Stack(column, Label("overlay")).Padding(6)
	setTreeFace(stack, testFace(t))
	laid := stack.measure(Constraints{})
	stack.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(stack)

	p := center(deep.bounds)
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: p})
	router.Handle(leftPress(p))
	router.Handle(leftRelease(p))
	if deepClicks != 1 {
		t.Errorf("deep clicks = %d, want 1", deepClicks)
	}
	if len(sibling.events) != 0 {
		t.Errorf("the non-target sibling received %d events, want 0", len(sibling.events))
	}
	// The press armed and the release disarmed the button; the hover
	// flag legitimately survives while the cursor stays on the button.
	if deep.pressed {
		t.Error("the deep button kept its armed press after the release")
	}
}

func TestMousePressRestoresFocusAfterReenable(t *testing.T) {
	clicks := 0
	button := Button("Go").OnClick(func() { clicks++ })
	column := Column(button)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != button || !button.focused {
		t.Fatal("Tab did not focus the button")
	}
	button.SetEnabled(false)
	button.SetEnabled(true)
	if button.focused {
		t.Fatal("disable and re-enable unexpectedly preserved focus")
	}

	p := center(button.bounds)
	router.Handle(leftPress(p))
	if router.focus != button || !button.focused {
		t.Fatalf("mouse press did not restore focus: router focus=%v button focused=%v", router.focus, button.focused)
	}
	router.Handle(leftRelease(p))
	if clicks != 1 {
		t.Fatalf("clicks after mouse release = %d, want 1", clicks)
	}
	if handled, redraw := router.Handle(keyEvent(driver.KeyEnter, 0)); !handled || !redraw {
		t.Errorf("Enter after restored focus = (%v, %v), want (true, true)", handled, redraw)
	}
	if clicks != 2 {
		t.Errorf("clicks after Enter = %d, want 2", clicks)
	}
}
