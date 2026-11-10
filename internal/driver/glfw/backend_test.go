package glfw

import (
	"testing"
	"time"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

var _ driver.Backend = (*Backend)(nil)
var _ driver.Window = (*Window)(nil)

// TestCreateWindowFailureCleansUp feeds NewWindow a config that cannot create
// a window and requires an error with no residual usable window, and that the
// backend keeps working afterwards.
func TestCreateWindowFailureCleansUp(t *testing.T) {
	b := newTestBackend(t)

	w, err := b.NewWindow(driver.Config{Title: "bad", Size: driver.Size{Width: 0, Height: 600}})
	if err == nil {
		t.Fatal("expected error for invalid window config")
	}
	if w != nil {
		t.Fatalf("expected no residual window, got %v", w)
	}

	good, err := b.NewWindow(driver.DefaultConfig())
	if err != nil {
		t.Fatalf("backend unusable after failed window creation: %v", err)
	}
	good.Destroy()
}

func TestWindowLifecycle(t *testing.T) {
	b := newTestBackend(t)

	cfg := driver.DefaultConfig()
	win, err := b.NewWindow(cfg)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if got := win.Size(); got != cfg.Size {
		t.Fatalf("Size() = %v, want %v", got, cfg.Size)
	}
	native := win.(*Window)

	win.SetHandler(func(driver.Event) {})

	win.Show()
	win.Hide()
	b.PollEvents()

	// The Wake signal comes from another goroutine while this goroutine
	// blocks in WaitEvents, so a failed Wake would hang the test body. The
	// watchdog independently posts a native empty event to unwind WaitEvents
	// and reports the failure.
	woken := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		b.Wake()
		close(woken)
	}()
	watchdog := time.AfterFunc(2*time.Second, func() {
		glfw3.PostEmptyEvent()
		t.Error("Wake did not unblock WaitEvents within 2s")
	})
	defer watchdog.Stop()

	b.WaitEvents()
	<-woken

	win.Destroy()
	win.Destroy() // must be idempotent
	if native.callbackClears != 1 {
		t.Fatalf("callbacks not cleared by Destroy: callbackClears = %d, want 1", native.callbackClears)
	}
	if native.nativeDestroyCount != 0 {
		t.Fatal("native destroy ran synchronously inside Destroy")
	}
	b.PollEvents() // the backend flushes queued native destroys
	if native.nativeDestroyCount != 1 {
		t.Fatalf("native destroy count = %d, want exactly 1", native.nativeDestroyCount)
	}
}

// TestDestroyFromHandlerDefersNativeDestroy pins the GLFW rule that a window
// must not be destroyed from inside its own callback stack: when a handler
// destroys windows, both the handler's own window and another window get
// their GLFW callbacks cleared immediately while the native destroy stays
// queued until the stack unwinds.
func TestDestroyFromHandlerDefersNativeDestroy(t *testing.T) {
	b := newTestBackend(t)

	own, err := b.NewWindow(driver.DefaultConfig())
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	other, err := b.NewWindow(driver.DefaultConfig())
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	w1 := own.(*Window)
	w2 := other.(*Window)

	var insideOwnNative, insideOtherNative int
	var insideOwnClears, insideOtherClears int
	w1.SetHandler(func(e driver.Event) {
		if e.Kind != driver.EventCloseRequested {
			return
		}
		w1.Destroy()
		w2.Destroy()
		insideOwnNative = w1.nativeDestroyCount
		insideOtherNative = w2.nativeDestroyCount
		insideOwnClears = w1.callbackClears
		insideOtherClears = w2.callbackClears
	})

	// emit is the entry point every GLFW callback funnels through, so this
	// runs the handler exactly where a callback would: synchronously on the
	// UI thread, inside the callback stack.
	w1.emit(driver.Event{Kind: driver.EventCloseRequested})

	// GLFW callbacks must already be cleared at destroy request time, while
	// the native destroy is still deferred until the callback stack unwinds.
	if insideOwnClears != 1 || insideOtherClears != 1 {
		t.Fatalf("callbacks not cleared at Destroy: own=%d other=%d", insideOwnClears, insideOtherClears)
	}
	if insideOwnNative != 0 || insideOtherNative != 0 {
		t.Fatalf("native destroy ran inside the callback stack: own=%d other=%d", insideOwnNative, insideOtherNative)
	}

	// The callback stack has unwound; the next backend call flushes the
	// queue. Neither clearing nor native destroy may repeat.
	b.PollEvents()
	if w1.callbackClears != 1 || w2.callbackClears != 1 {
		t.Fatalf("callbacks cleared more than once: own=%d other=%d", w1.callbackClears, w2.callbackClears)
	}
	if w1.nativeDestroyCount != 1 || w2.nativeDestroyCount != 1 {
		t.Fatalf("native destroy count after flush: own=%d other=%d, want 1/1", w1.nativeDestroyCount, w2.nativeDestroyCount)
	}

	w1.Destroy()
	w2.Destroy()
	b.PollEvents()
	if w1.callbackClears != 1 || w2.callbackClears != 1 || w1.nativeDestroyCount != 1 || w2.nativeDestroyCount != 1 {
		t.Fatalf("destroy ran more than once: clears=%d/%d native=%d/%d",
			w1.callbackClears, w2.callbackClears, w1.nativeDestroyCount, w2.nativeDestroyCount)
	}
}

// TestTerminateFlushesPendingDestroy pins that App-style exit cleanup, which
// destroys windows right before Terminate, still frees every native window
// because Terminate drains the destroy queue first.
func TestTerminateFlushesPendingDestroy(t *testing.T) {
	lockTestThread(t)
	b := New()
	if err := b.Init(); err != nil {
		t.Skipf("glfw init failed: %v", err)
	}
	terminated := false
	defer func() {
		if !terminated {
			b.Terminate()
		}
	}()

	win, err := b.NewWindow(driver.DefaultConfig())
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	win.Destroy()
	native := win.(*Window)
	if native.nativeDestroyCount != 0 {
		t.Fatal("native destroy ran synchronously in Destroy")
	}

	b.Terminate()
	terminated = true
	if native.nativeDestroyCount != 1 {
		t.Fatalf("Terminate did not flush pending destroy: native destroy count = %d, want 1", native.nativeDestroyCount)
	}
}

// TestWaitEventsFlushesPendingDestroy pins that a destroy requested outside
// the callback stack (e.g. by a UI task) is freed by WaitEvents itself rather
// than waiting for an extra event cycle.
func TestWaitEventsFlushesPendingDestroy(t *testing.T) {
	b := newTestBackend(t)

	win, err := b.NewWindow(driver.DefaultConfig())
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	win.Destroy()
	native := win.(*Window)

	b.Wake() // give WaitEvents a reason to return on an idle display
	b.WaitEvents()
	if native.nativeDestroyCount != 1 {
		t.Fatalf("WaitEvents did not flush pending destroy: native destroy count = %d, want 1", native.nativeDestroyCount)
	}
}

// TestWakeBeforeInit pins the app contract that Wake may be called before
// Init completes (Post before Run) and must not panic.
func TestWakeBeforeInit(t *testing.T) {
	lockTestThread(t)
	b := New()
	b.Wake()
	if err := b.Init(); err != nil {
		t.Skipf("glfw init failed: %v", err)
	}
	b.Terminate()
}
