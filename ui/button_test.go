package ui

import (
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

func TestButtonMeasureAndClick(t *testing.T) {
	clicks := 0
	button := Button("OK").OnClick(func() { clicks++ })
	setTreeFace(button, testFace(t))
	if !button.Enabled() {
		t.Fatal("a new button must start enabled")
	}
	if got := button.Text(); got != "OK" {
		t.Fatalf("Text() = %q, want %q", got, "OK")
	}

	// Default metrics: text from Face.Rasterize plus 16px horizontal
	// padding on each side, and a fixed 36px height.
	text := rasterTextSize(t, "OK")
	if got, want := button.measure(Constraints{}), size(text.Width+32, 36); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}

	otherClicked := false
	other := Button("Ignore").OnClick(func() { otherClicked = true })
	column := Column(button, other).Gap(8)
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))

	router := newEventRouter(column)
	inside := driver.Point{X: button.bounds.X + button.bounds.Width/2, Y: button.bounds.Y + button.bounds.Height/2}

	// A click is a press inside followed by a release inside.
	handled, redraw := router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: inside})
	if !handled || !redraw {
		t.Errorf("move = (%v, %v), want (true, true)", handled, redraw)
	}
	handled, redraw = router.Handle(leftPress(inside))
	if !handled || !redraw {
		t.Errorf("press = (%v, %v), want (true, true)", handled, redraw)
	}
	handled, redraw = router.Handle(leftRelease(inside))
	if !handled || !redraw {
		t.Errorf("release = (%v, %v), want (true, true)", handled, redraw)
	}
	if clicks != 1 {
		t.Fatalf("clicks = %d, want 1", clicks)
	}
	if otherClicked {
		t.Error("the click reached the second button; the event must route exactly once")
	}

	// A release without a press is inert.
	if handled, redraw := router.Handle(leftRelease(inside)); handled || redraw {
		t.Errorf("stray release = (%v, %v), want (false, false)", handled, redraw)
	}
	if clicks != 1 {
		t.Errorf("clicks = %d, want 1", clicks)
	}

	// Pressing inside and releasing over another component cancels the
	// click and disarms the press; the other button still sees nothing.
	router.Handle(leftPress(inside))
	otherInside := driver.Point{X: other.bounds.X + other.bounds.Width/2, Y: other.bounds.Y + other.bounds.Height/2}
	router.Handle(leftRelease(otherInside))
	if clicks != 1 {
		t.Errorf("outside release clicked: clicks = %d, want 1", clicks)
	}
	if button.pressed {
		t.Error("the cancelled press left the button armed")
	}
	if otherClicked {
		t.Error("the cancelled click leaked into the second button")
	}

	// The public Click runs the handler while enabled with a non-nil
	// handler.
	button.Click()
	if clicks != 2 {
		t.Errorf("clicks after Click() = %d, want 2", clicks)
	}

	// A nil handler makes Click a no-op instead of a panic.
	button.OnClick(nil)
	button.Click()
	if clicks != 2 {
		t.Errorf("clicks after nil-handler Click = %d, want 2", clicks)
	}
}

func TestButtonDisabledAndReleaseOutside(t *testing.T) {
	clicks := 0
	button := Button("Go").OnClick(func() { clicks++ })
	setTreeFace(button, testFace(t))
	column := Column(button)
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	inside := driver.Point{X: button.bounds.X + button.bounds.Width/2, Y: button.bounds.Y + button.bounds.Height/2}
	outside := driver.Point{X: button.bounds.X - 40, Y: button.bounds.Y - 40}

	// A disabled button is inert and cannot take focus.
	button.SetEnabled(false)
	if button.Enabled() {
		t.Fatal("SetEnabled(false) did not disable the button")
	}
	if handled, _ := router.Handle(leftPress(inside)); handled {
		t.Error("disabled button consumed the press")
	}
	router.Handle(leftRelease(inside))
	if clicks != 0 {
		t.Errorf("disabled clicks = %d, want 0", clicks)
	}
	if router.focus != nil {
		t.Error("disabled button took focus")
	}
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: inside})
	if button.hover {
		t.Error("disabled button hovered")
	}

	// Re-enabled: an inside press arms the button; an outside release
	// cancels it without a click.
	button.SetEnabled(true)
	router.Handle(leftPress(inside))
	if !button.pressed {
		t.Fatal("press inside did not arm the button")
	}
	router.Handle(leftRelease(outside))
	if clicks != 0 {
		t.Errorf("outside release clicked: clicks = %d, want 0", clicks)
	}
	if button.pressed {
		t.Error("outside release left the button armed")
	}

	// Press inside then release inside fires exactly once.
	router.Handle(leftPress(inside))
	router.Handle(leftRelease(inside))
	if clicks != 1 {
		t.Errorf("clicks = %d, want 1", clicks)
	}

	// Click() on a disabled button is a no-op.
	button.SetEnabled(false)
	button.Click()
	if clicks != 1 {
		t.Errorf("disabled Click fired: clicks = %d, want 1", clicks)
	}

	// Disabling mid-press disarms the button, so the later release inside
	// cannot fire either.
	button.SetEnabled(true)
	router.Handle(leftPress(inside))
	button.SetEnabled(false)
	router.Handle(leftRelease(inside))
	if clicks != 1 {
		t.Errorf("release after disable clicked: clicks = %d, want 1", clicks)
	}
	if button.pressed {
		t.Error("disable mid-press left the button armed")
	}
}

func TestButtonHoverAndCursorLeave(t *testing.T) {
	first := Button("First")
	second := Button("Second")
	column := Column(first, second).Gap(6)
	setTreeFace(column, testFace(t))
	laid := column.measure(Constraints{})
	column.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(column)

	firstInside := center(first.bounds)
	secondInside := center(second.bounds)
	below := driver.Point{X: firstInside.X, Y: second.bounds.Y + second.bounds.Height + 4}

	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: firstInside})
	if !first.hover {
		t.Error("move onto the button must set hover")
	}

	// Moving onto another component clears the previous hover.
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: secondInside})
	if first.hover || !second.hover {
		t.Errorf("hover after moving on = (%v, %v), want (false, true)", first.hover, second.hover)
	}

	// Moving over empty space clears the hover too.
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: firstInside})
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: below})
	if first.hover {
		t.Error("hover must clear when the cursor leaves every component")
	}

	// Cursor leave clears hover but never the armed press.
	clicks := 0
	first.OnClick(func() { clicks++ })
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: firstInside})
	router.Handle(leftPress(firstInside))
	if !first.pressed {
		t.Fatal("press inside did not arm the button")
	}
	handled, redraw := router.Handle(driver.Event{Kind: driver.EventCursorLeave})
	if first.hover {
		t.Error("cursor leave must clear hover")
	}
	if !first.pressed {
		t.Error("cursor leave must not cancel the press")
	}
	if !handled || !redraw {
		t.Errorf("cursor leave = (%v, %v), want (true, true)", handled, redraw)
	}

	// With the cursor gone the release lands outside and only disarms.
	router.Handle(leftRelease(driver.Point{X: firstInside.X, Y: -30}))
	if clicks != 0 {
		t.Errorf("clicks = %d, want 0", clicks)
	}
	if first.pressed {
		t.Error("the release must disarm the button")
	}

	// A quiet cursor leave reports nothing.
	if handled, redraw := router.Handle(driver.Event{Kind: driver.EventCursorLeave}); handled || redraw {
		t.Errorf("quiet cursor leave = (%v, %v), want (false, false)", handled, redraw)
	}
}
