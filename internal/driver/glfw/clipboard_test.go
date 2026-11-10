package glfw

import (
	"fmt"
	"testing"
	"time"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

var _ driver.Clipboard = (*Window)(nil)

// TestMapClipboardKeys pins that the Ctrl-shortcut keys used for clipboard
// operations map to their driver keys, while keys outside the driver set
// still fall back to KeyUnknown.
func TestMapClipboardKeys(t *testing.T) {
	cases := []struct {
		name string
		in   glfw3.Key
		want driver.Key
	}{
		{"C", glfw3.KeyC, driver.KeyC},
		{"V", glfw3.KeyV, driver.KeyV},
		{"X", glfw3.KeyX, driver.KeyX},
		{"unmapped still falls back to unknown", glfw3.KeyF12, driver.KeyUnknown},
	}
	for _, c := range cases {
		if got := mapKey(c.in); got != c.want {
			t.Errorf("mapKey(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestClipboardKeyEventsKeepRunePath pins that a Ctrl+C-style press arrives
// only as an EventKey carrying KeyC and ModCtrl — the key path never
// synthesizes text — and that the character callback still delivers its
// EventRune unchanged.
func TestClipboardKeyEventsKeepRunePath(t *testing.T) {
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	native := win.(*Window)

	var events []driver.Event
	win.SetHandler(func(e driver.Event) { events = append(events, e) })

	keyCallback := native.win.SetKeyCallback(nil)
	if keyCallback == nil {
		t.Fatal("window has no GLFW key callback")
	}
	native.win.SetKeyCallback(keyCallback)
	keyCallback(native.win, glfw3.KeyC, 0, glfw3.Press, glfw3.ModControl)

	if len(events) != 1 {
		t.Fatalf("Ctrl+C produced %d events, want exactly one EventKey: %v", len(events), events)
	}
	e := events[0]
	if e.Kind != driver.EventKey || e.Key != driver.KeyC || e.Mods != driver.ModCtrl || e.Action != driver.ActionPress {
		t.Fatalf("Ctrl+C event = %+v, want EventKey{Key: KeyC, Mods: ModCtrl, Action: Press}", e)
	}

	charCallback := native.win.SetCharCallback(nil)
	if charCallback == nil {
		t.Fatal("window has no GLFW char callback")
	}
	native.win.SetCharCallback(charCallback)
	events = nil
	charCallback(native.win, 'c')

	if len(events) != 1 || events[0].Kind != driver.EventRune || events[0].Rune != 'c' {
		t.Fatalf("char callback events = %v, want one EventRune{'c'}", events)
	}
}

// TestClipboardAfterDestroy pins the destroyed-window contract without ever
// touching the system clipboard: the native destroy is flushed first, so any
// native clipboard call would trip GLFW's freed-state checks.
func TestClipboardAfterDestroy(t *testing.T) {
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	native := win.(*Window)

	win.Destroy()
	b.PollEvents() // flush the native destroy so freed state must not be touched

	if _, err := native.Text(); err != errDestroyed {
		t.Fatalf("Text after Destroy = %v, want %v", err, errDestroyed)
	}
	if err := native.SetText("must not reach the native clipboard"); err != errDestroyed {
		t.Fatalf("SetText after Destroy = %v, want %v", err, errDestroyed)
	}
}

// TestClipboardRoundTrip drives the real system clipboard, so it only runs on
// explicit opt-in. It saves the previous clipboard text, writes a unique test
// value, reads it back and restores the old text — both mid-test and again in
// cleanup so a failure cannot leave the user's clipboard overwritten.
func TestClipboardRoundTrip(t *testing.T) {
	if !desktopTests() {
		t.Skip("rewrites the user's system clipboard; set MELTEMI_GLFW_DESKTOP_TESTS=1 to run it")
	}
	t.Log("desktop tests enabled: this test overwrites and then restores the system clipboard text")
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	native := win.(*Window)

	old, err := native.Text()
	if err != nil {
		t.Fatalf("read old clipboard text: %v", err)
	}
	restore := func() {
		if err := native.SetText(old); err != nil {
			t.Errorf("restore clipboard text: %v", err)
		}
	}
	t.Cleanup(restore) // registered after newTestWindow's cleanup, so the window is still live

	want := fmt.Sprintf("wind clipboard round-trip %d", time.Now().UnixNano())
	if err := native.SetText(want); err != nil {
		t.Fatalf("SetText(%q): %v", want, err)
	}
	got, err := native.Text()
	if err != nil {
		t.Fatalf("Text after SetText: %v", err)
	}
	if got != want {
		t.Fatalf("clipboard round trip = %q, want %q", got, want)
	}

	restore()
	got, err = native.Text()
	if err != nil {
		t.Fatalf("Text after restore: %v", err)
	}
	if got != old {
		t.Fatalf("clipboard after restore = %q, want old text %q", got, old)
	}
}
