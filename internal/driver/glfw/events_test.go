package glfw

import (
	"testing"
	"time"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

func TestMapKey(t *testing.T) {
	cases := []struct {
		name string
		in   glfw3.Key
		want driver.Key
	}{
		{"A", glfw3.KeyA, driver.KeyA},
		{"Escape", glfw3.KeyEscape, driver.KeyEscape},
		{"Enter", glfw3.KeyEnter, driver.KeyEnter},
		{"Tab", glfw3.KeyTab, driver.KeyTab},
		{"Backspace", glfw3.KeyBackspace, driver.KeyBackspace},
		{"Delete", glfw3.KeyDelete, driver.KeyDelete},
		{"Left", glfw3.KeyLeft, driver.KeyLeft},
		{"Right", glfw3.KeyRight, driver.KeyRight},
		{"Up", glfw3.KeyUp, driver.KeyUp},
		{"Down", glfw3.KeyDown, driver.KeyDown},
		{"Home", glfw3.KeyHome, driver.KeyHome},
		{"End", glfw3.KeyEnd, driver.KeyEnd},
		{"C", glfw3.KeyC, driver.KeyC},
		{"V", glfw3.KeyV, driver.KeyV},
		{"X", glfw3.KeyX, driver.KeyX},
		{"Z", glfw3.KeyZ, driver.KeyZ},
		{"Y", glfw3.KeyY, driver.KeyY},
		{"Space", glfw3.KeySpace, driver.KeySpace},
		{"unmapped falls back to unknown", glfw3.KeyF12, driver.KeyUnknown},
	}
	for _, c := range cases {
		if got := mapKey(c.in); got != c.want {
			t.Errorf("mapKey(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestMenuShortcutKeyData pins the backend-neutral shortcut conversion the
// menu router relies on: every ASCII letter and digit key maps to its own
// driver key appended after the existing ones (never renumbered), and a key
// event exposes the exact modifier mask plus the normalized rune (lowercase
// for letters, so Shift never hides in the rune case). Non-letter/digit keys
// and non-key events carry no shortcut data at all.
func TestMenuShortcutKeyData(t *testing.T) {
	cases := []struct {
		key  glfw3.Key
		want driver.Key
	}{
		{glfw3.KeyB, driver.KeyB},
		{glfw3.KeyD, driver.KeyD},
		{glfw3.KeyE, driver.KeyE},
		{glfw3.KeyF, driver.KeyF},
		{glfw3.KeyG, driver.KeyG},
		{glfw3.KeyH, driver.KeyH},
		{glfw3.KeyI, driver.KeyI},
		{glfw3.KeyJ, driver.KeyJ},
		{glfw3.KeyK, driver.KeyK},
		{glfw3.KeyL, driver.KeyL},
		{glfw3.KeyM, driver.KeyM},
		{glfw3.KeyN, driver.KeyN},
		{glfw3.KeyO, driver.KeyO},
		{glfw3.KeyP, driver.KeyP},
		{glfw3.KeyQ, driver.KeyQ},
		{glfw3.KeyR, driver.KeyR},
		{glfw3.KeyS, driver.KeyS},
		{glfw3.KeyT, driver.KeyT},
		{glfw3.KeyU, driver.KeyU},
		{glfw3.KeyW, driver.KeyW},
		{glfw3.Key0, driver.Key0},
		{glfw3.Key7, driver.Key7},
		{glfw3.Key9, driver.Key9},
	}
	for _, c := range cases {
		if got := mapKey(c.key); got != c.want {
			t.Errorf("mapKey(%d) = %d, want %d", c.key, got, c.want)
		}
		if c.want <= driver.KeySpace {
			t.Errorf("shortcut key %d not appended after the existing keys", c.want)
		}
	}
	if got := mapKey(glfw3.KeyF12); got != driver.KeyUnknown {
		t.Errorf("mapKey(KeyF12) = %d, want KeyUnknown", got)
	}

	short, ok := (driver.Event{Kind: driver.EventKey, Key: driver.KeyS, Mods: driver.ModCtrl}).Shortcut()
	if !ok || short.Mods != driver.ModCtrl || short.Key != 's' {
		t.Errorf("Ctrl+S shortcut = %+v, %v; want {Ctrl s}, true", short, ok)
	}
	short, ok = (driver.Event{Kind: driver.EventKey, Key: driver.Key7, Mods: driver.ModShift | driver.ModAlt}).Shortcut()
	if !ok || short.Mods != driver.ModShift|driver.ModAlt || short.Key != '7' {
		t.Errorf("Shift+Alt+7 shortcut = %+v, %v; want exact mods and '7'", short, ok)
	}
	for _, e := range []driver.Event{
		{Kind: driver.EventKey, Key: driver.KeyEscape},
		{Kind: driver.EventKey, Key: driver.KeyEnter, Mods: driver.ModCtrl},
		{Kind: driver.EventRune, Rune: 's'},
		{Kind: driver.EventMouseMove},
	} {
		if short, ok := e.Shortcut(); ok {
			t.Errorf("%+v reported shortcut %+v; none wanted", e, short)
		}
	}
}

// TestMapUndoRedoKeys pins the undo/redo key mapping: Z and Y map to their
// own driver keys so Input/TextArea can bind Ctrl+Z and Ctrl+Y, and the
// new keys are appended after the existing ones without renumbering them.
func TestMapUndoRedoKeys(t *testing.T) {
	if got := mapKey(glfw3.KeyZ); got != driver.KeyZ {
		t.Errorf("mapKey(KeyZ) = %d, want %d", got, driver.KeyZ)
	}
	if got := mapKey(glfw3.KeyY); got != driver.KeyY {
		t.Errorf("mapKey(KeyY) = %d, want %d", got, driver.KeyY)
	}
	if driver.KeyZ <= driver.KeyX || driver.KeyY <= driver.KeyZ {
		t.Errorf("KeyZ/KeyY not appended after KeyX: KeyX=%d KeyZ=%d KeyY=%d", driver.KeyX, driver.KeyZ, driver.KeyY)
	}
}

// TestMapSpaceKey pins the Space key mapping: Checkbox activation needs a
// backend-independent Space key, appended after the existing ones without
// renumbering them.
func TestMapSpaceKey(t *testing.T) {
	if got := mapKey(glfw3.KeySpace); got != driver.KeySpace {
		t.Errorf("mapKey(KeySpace) = %d, want %d", got, driver.KeySpace)
	}
	if driver.KeySpace <= driver.KeyY {
		t.Errorf("KeySpace not appended after KeyY: KeyY=%d KeySpace=%d", driver.KeyY, driver.KeySpace)
	}
}

func TestMapMouseButton(t *testing.T) {
	cases := []struct {
		name string
		in   glfw3.MouseButton
		want driver.MouseButton
		ok   bool
	}{
		{"left", glfw3.MouseButtonLeft, driver.MouseButtonLeft, true},
		{"middle", glfw3.MouseButtonMiddle, driver.MouseButtonMiddle, true},
		{"right", glfw3.MouseButtonRight, driver.MouseButtonRight, true},
		{"side button 4 dropped", glfw3.MouseButton4, 0, false},
		{"side button 5 dropped", glfw3.MouseButton5, 0, false},
	}
	for _, c := range cases {
		got, ok := mapMouseButton(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("mapMouseButton(%s) = %d, %v; want %d, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestMouseSideButtonEmitsNoEvent pins that mouse buttons outside the driver
// contract never surface as a faked left-button event. The window is real
// because button events now sample the native cursor position.
func TestMouseSideButtonEmitsNoEvent(t *testing.T) {
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	native := win.(*Window)

	var events []driver.Event
	win.SetHandler(func(e driver.Event) { events = append(events, e) })

	native.handleMouseButton(glfw3.MouseButton4, glfw3.Press, 0)
	native.handleMouseButton(glfw3.MouseButtonLeft, glfw3.Press, 0)

	if len(events) != 1 || events[0].Kind != driver.EventMouseButton || events[0].Button != driver.MouseButtonLeft {
		t.Fatalf("side button must not emit a left-button event; got %v", events)
	}
}

// TestMouseButtonIncludesCursorPosition pins that press and release events
// carry the current cursor position rather than a zero Point.
func TestMouseButtonIncludesCursorPosition(t *testing.T) {
	if !desktopTests() {
		t.Skip("warps the real cursor onto the test window and leaves it there; set MELTEMI_GLFW_DESKTOP_TESTS=1 to run desktop-interfering input tests")
	}
	t.Log("desktop tests enabled: this test moves the real cursor and does not restore it")
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	native := win.(*Window)

	// XWarpPointer (GLFW SetCursorPos) only moves the pointer onto the window
	// once the window manager has finished mapping and placing it.
	waitForWindowSettled(t, b, native)

	// The pointer only sits on a warped position until the next physical
	// mouse move, and the display may be live, so re-warp on every attempt
	// and accept the first sample whose button events observe the warped
	// position.
	want := driver.Point{X: 37, Y: 91}
	var events []driver.Event
	win.SetHandler(func(e driver.Event) {
		if e.Kind == driver.EventMouseButton {
			events = append(events, e)
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		native.win.SetCursorPos(want.X, want.Y)
		if x, y := native.win.GetCursorPos(); x == want.X && y == want.Y {
			events = nil
			native.handleMouseButton(glfw3.MouseButtonLeft, glfw3.Press, 0)
			native.handleMouseButton(glfw3.MouseButtonLeft, glfw3.Release, 0)
			if len(events) == 2 && events[0].Position == want && events[1].Position == want {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("button events never carried the current cursor position %v; last events: %v", want, events)
		}
	}

	if events[0].Action != driver.ActionPress || events[1].Action != driver.ActionRelease {
		t.Fatalf("expected press then release, got %v", events)
	}
}

func TestCursorLeaveCallbackEmitsEventAndDestroyClearsIt(t *testing.T) {
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	native := win.(*Window)

	var events []driver.Event
	win.SetHandler(func(e driver.Event) { events = append(events, e) })
	callback := native.win.SetCursorEnterCallback(nil)
	if callback == nil {
		t.Fatal("window has no GLFW cursor-enter callback")
	}
	native.win.SetCursorEnterCallback(callback)
	callback(native.win, true)
	callback(native.win, false)
	if len(events) != 1 || events[0].Kind != driver.EventCursorLeave || events[0].Window != win {
		t.Fatalf("cursor enter/leave events = %v, want one EventCursorLeave for this window", events)
	}

	win.Destroy()
	if previous := native.win.SetCursorEnterCallback(nil); previous != nil {
		t.Error("Destroy did not clear the GLFW cursor-enter callback")
	}
}

// TestGLFWScrollEventMapping pins the GLFW scroll-callback conversion: one
// wheel line is a unit delta (touchpads emit smooth fractions, and the
// opposite direction is negative), the event carries the current cursor
// position like button events do, and Destroy clears the callback.
func TestGLFWScrollEventMapping(t *testing.T) {
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	native := win.(*Window)

	var events []driver.Event
	win.SetHandler(func(e driver.Event) { events = append(events, e) })

	native.handleScroll(0, 1)
	native.handleScroll(1.5, -2.5)
	native.handleScroll(-1, 0)

	want := []struct{ x, y float64 }{{0, 1}, {1.5, -2.5}, {-1, 0}}
	if len(events) != len(want) {
		t.Fatalf("scroll events = %d, want %d", len(events), len(want))
	}
	for i, w := range want {
		e := events[i]
		if e.Kind != driver.EventScroll || e.ScrollX != w.x || e.ScrollY != w.y {
			t.Errorf("event %d = {Kind:%d ScrollX:%v ScrollY:%v}, want {Kind:%d ScrollX:%v ScrollY:%v}",
				i, e.Kind, e.ScrollX, e.ScrollY, driver.EventScroll, w.x, w.y)
		}
	}

	win.Destroy()
	if previous := native.win.SetScrollCallback(nil); previous != nil {
		t.Error("Destroy did not clear the GLFW scroll callback")
	}
}

func TestMapAction(t *testing.T) {
	cases := []struct {
		in   glfw3.Action
		want driver.Action
	}{
		{glfw3.Press, driver.ActionPress},
		{glfw3.Release, driver.ActionRelease},
		{glfw3.Repeat, driver.ActionRepeat},
	}
	for _, c := range cases {
		if got := mapAction(c.in); got != c.want {
			t.Errorf("mapAction(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestMapModifiers(t *testing.T) {
	cases := []struct {
		name string
		in   glfw3.ModifierKey
		want driver.Modifiers
	}{
		{"none", 0, 0},
		{"shift", glfw3.ModShift, driver.ModShift},
		{"ctrl", glfw3.ModControl, driver.ModCtrl},
		{"alt", glfw3.ModAlt, driver.ModAlt},
		{"super", glfw3.ModSuper, driver.ModSuper},
		{"all", glfw3.ModShift | glfw3.ModControl | glfw3.ModAlt | glfw3.ModSuper,
			driver.ModShift | driver.ModCtrl | driver.ModAlt | driver.ModSuper},
	}
	for _, c := range cases {
		if got := mapModifiers(c.in); got != c.want {
			t.Errorf("mapModifiers(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestPageKeyMapping pins the PageUp/PageDown key mapping the TextArea page
// navigation binds: both map to their own driver keys, appended after the
// existing keys (never renumbered), and they carry no shortcut rune.
func TestPageKeyMapping(t *testing.T) {
	if got := mapKey(glfw3.KeyPageUp); got != driver.KeyPageUp {
		t.Errorf("mapKey(KeyPageUp) = %d, want %d", got, driver.KeyPageUp)
	}
	if got := mapKey(glfw3.KeyPageDown); got != driver.KeyPageDown {
		t.Errorf("mapKey(KeyPageDown) = %d, want %d", got, driver.KeyPageDown)
	}
	if driver.KeyPageUp <= driver.Key9 || driver.KeyPageDown <= driver.KeyPageUp {
		t.Errorf("page keys not appended after Key9: Key9=%d KeyPageUp=%d KeyPageDown=%d",
			driver.Key9, driver.KeyPageUp, driver.KeyPageDown)
	}
	for _, key := range []driver.Key{driver.KeyPageUp, driver.KeyPageDown} {
		if short, ok := (driver.Event{Kind: driver.EventKey, Key: key, Mods: driver.ModCtrl}).Shortcut(); ok {
			t.Errorf("Ctrl+page key %d reported shortcut %+v; page keys carry no shortcut rune", key, short)
		}
	}
}
