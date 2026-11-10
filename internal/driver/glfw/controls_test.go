package glfw

import (
	"os"
	"runtime"
	"testing"
	"time"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

var _ driver.FrameWindow = (*Window)(nil)

// desktopTests reports whether tests that visibly interfere with the user's
// live desktop session — warping the real cursor or driving window-state
// animations (minimize/maximize) — may run. They are opt-in via
// MELTEMI_GLFW_DESKTOP_TESTS=1 so that a plain `go test ./...` has no visible
// side effects beyond the short-lived test windows every window test
// already creates. WIND_GLFW_DESKTOP_TESTS remains accepted for older scripts.
func desktopTests() bool {
	return os.Getenv("MELTEMI_GLFW_DESKTOP_TESTS") == "1" || os.Getenv("WIND_GLFW_DESKTOP_TESTS") == "1"
}

// lockTestThread pins the actual test goroutine. Cleanups registered later,
// including native window destruction and backend termination, run first.
func lockTestThread(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
}

// newTestBackend returns an initialized backend on a locked test thread,
// skipping when GLFW cannot initialize, and schedules Terminate as cleanup.
func newTestBackend(t *testing.T) *Backend {
	t.Helper()
	lockTestThread(t)
	b := New()
	if err := b.Init(); err != nil {
		t.Skipf("glfw init failed: %v", err)
	}
	t.Cleanup(b.Terminate)
	return b
}

// newTestWindow creates a window and schedules its destruction plus the
// backend's pending-destroy flush as cleanup, so a test never leaves a native
// window behind.
func newTestWindow(t *testing.T, b *Backend, cfg driver.Config) driver.Window {
	t.Helper()
	win, err := b.NewWindow(cfg)
	if err != nil {
		t.Fatalf("NewWindow(%+v): %v", cfg, err)
	}
	t.Cleanup(func() {
		win.Destroy()
		b.PollEvents() // flush the queued native destroy
	})
	return win
}

// waitFor polls cond — pumping GLFW events on every iteration — until it
// holds or the deadline passes. On X11 a size or position change is applied
// by the window manager asynchronously, so the immediate post-call state is
// not assertable; eventual consistency within the deadline is.
func waitFor(t *testing.T, b *Backend, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.PollEvents()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForWindowSettled pumps events until the window position reads the same
// on several consecutive polls, i.e. the window manager has finished mapping
// and placing the window. Only then are later operations that depend on the
// settled native window — such as warping the pointer onto it — reliable.
func waitForWindowSettled(t *testing.T, b *Backend, w driver.FrameWindow) {
	t.Helper()
	prev := w.Position()
	stable := 0
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.PollEvents()
		cur := w.Position()
		if cur == prev {
			stable++
			if stable >= 3 {
				return
			}
		} else {
			stable = 0
		}
		prev = cur
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for window position to settle (last %v)", cur)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestFrameWindowControls pins that a Config.Frameless window satisfies
// driver.FrameWindow and that the native frame controls behave: SetSize and
// SetPosition take effect, and Iconify/Maximize/Restore are callable.
func TestFrameWindowControls(t *testing.T) {
	b := newTestBackend(t)
	cfg := driver.DefaultConfig()
	cfg.Frameless = true
	win := newTestWindow(t, b, cfg)

	fw, ok := win.(driver.FrameWindow)
	if !ok {
		t.Fatalf("frameless window %T does not satisfy driver.FrameWindow", win)
	}

	if err := fw.SetSize(driver.Size{Width: 0, Height: 480}); err == nil {
		t.Fatal("SetSize with non-positive width returned nil error")
	}
	if err := fw.SetSize(driver.Size{Width: 640, Height: -1}); err == nil {
		t.Fatal("SetSize with negative height returned nil error")
	}

	fw.SetSize(driver.Size{Width: 640, Height: 480})
	wantSize := driver.Size{Width: 640, Height: 480}
	waitFor(t, b, "size 640x480 after SetSize", func() bool { return fw.Size() == wantSize })

	fw.SetPosition(driver.Point{X: 21, Y: 34})
	wantPos := driver.Point{X: 21, Y: 34}
	waitFor(t, b, "position (21,34) after SetPosition", func() bool { return fw.Position() == wantPos })

	// Iconify, Maximize and Restore drive real window-manager state — task
	// bar entries and animations on the live desktop — so they run only on
	// explicit opt-in. Each call is followed by a state wait so the test
	// verifies the control actually took effect, not just that it was
	// callable. The iconify and maximize round trips run separately: a
	// restore from iconified keeps any maximized state, so stacking both
	// would never satisfy a "fully normal" wait.
	if desktopTests() {
		fw.Iconify()
		waitFor(t, b, "iconified state after Iconify", fw.IsIconified)
		fw.Restore()
		waitFor(t, b, "normal state after Restore from iconified", func() bool {
			return !fw.IsIconified()
		})

		fw.Maximize()
		waitFor(t, b, "maximized state after Maximize", fw.IsMaximized)
		fw.Restore()
		waitFor(t, b, "normal state after Restore from maximized", func() bool {
			return !fw.IsMaximized()
		})
	} else {
		t.Log("skipping Iconify/Maximize/Restore: they animate the live desktop; set MELTEMI_GLFW_DESKTOP_TESTS=1 to run them")
	}
}

// TestFrameWindowControlsAfterDestroy pins that every frame control is safe
// once the window is destroyed: mutators become no-ops, getters return zero
// values and SetSize reports errDestroyed, all without touching the freed
// native window.
func TestFrameWindowControlsAfterDestroy(t *testing.T) {
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	fw, ok := win.(driver.FrameWindow)
	if !ok {
		t.Fatalf("window %T does not satisfy driver.FrameWindow", win)
	}

	win.Destroy()
	b.PollEvents() // flush the native destroy so freed state must not be touched

	if err := fw.SetSize(driver.Size{Width: 320, Height: 240}); err != errDestroyed {
		t.Fatalf("SetSize after Destroy = %v, want %v", err, errDestroyed)
	}
	if got := fw.Position(); got != (driver.Point{}) {
		t.Fatalf("Position after Destroy = %v, want zero Point", got)
	}
	if got := fw.Size(); got != (driver.Size{}) {
		t.Fatalf("Size after Destroy = %v, want zero Size", got)
	}
	fw.SetPosition(driver.Point{X: 1, Y: 1})
	fw.Iconify()
	fw.Maximize()
	fw.Restore()
	if fw.IsMaximized() {
		t.Fatal("IsMaximized after Destroy = true, want false")
	}
	if fw.IsIconified() {
		t.Fatal("IsIconified after Destroy = true, want false")
	}
}

// TestDecorationHintDoesNotLeak pins that NewWindow restores GLFW_DECORATED
// for decorated windows: GLFW 3.3 keeps window hints across window creation,
// so a frameless window must not strip decorations from later ones.
func TestDecorationHintDoesNotLeak(t *testing.T) {
	b := newTestBackend(t)

	cfg := driver.DefaultConfig()
	cfg.Frameless = true
	frameless := newTestWindow(t, b, cfg)
	if got := frameless.(*Window).win.GetAttrib(glfw3.Decorated); got != glfw3.False {
		t.Fatalf("frameless window GLFW_DECORATED attrib = %d, want %d", got, glfw3.False)
	}
	frameless.Destroy()
	b.PollEvents() // flush the native destroy before the decorated window checks hints

	decorated := newTestWindow(t, b, driver.DefaultConfig())
	if got := decorated.(*Window).win.GetAttrib(glfw3.Decorated); got != glfw3.True {
		t.Fatalf("default window GLFW_DECORATED attrib = %d, want %d: decoration hint leaked from the frameless window",
			got, glfw3.True)
	}
}
