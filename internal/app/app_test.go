package app_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/internal/app"
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/driver/fake"
)

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func runAsync(a *app.App) <-chan error {
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	return done
}

func waitRunReturned(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s")
	}
}

func TestPrepostedTaskRunsBeforeWait(t *testing.T) {
	b := fake.New(nil)
	a := app.New(b)

	ran := make(chan struct{})
	waitsAtTask := -1
	if err := a.Post(func() {
		waitsAtTask = b.WaitCalls()
		close(ran)
	}); err != nil {
		t.Fatalf("Post before Run: %v", err)
	}
	select {
	case <-ran:
		t.Fatal("preposted task executed before Run was called")
	default:
	}

	done := runAsync(a)

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("preposted task did not run within 2s")
	}
	if waitsAtTask != 0 {
		t.Fatalf("task ran after %d WaitEvents calls, want 0 (before first wait)", waitsAtTask)
	}

	a.Quit()
	waitRunReturned(t, done)
}

func TestPostFromGoroutineWakesRun(t *testing.T) {
	b := fake.New(nil)
	a := app.New(b)
	done := runAsync(a)

	waitFor(t, 2*time.Second, func() bool { return b.WaitCalls() > 0 })

	ran := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := a.Post(func() { close(ran) }); err != nil {
			t.Errorf("Post from goroutine: %v", err)
		}
	}()

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("task posted from goroutine did not run within 2s")
	}
	wg.Wait()

	a.Quit()
	waitRunReturned(t, done)
}

func TestPostAfterQuit(t *testing.T) {
	b := fake.New(nil)
	a := app.New(b)
	done := runAsync(a)

	waitFor(t, 2*time.Second, func() bool { return b.WaitCalls() > 0 })
	a.Quit()
	waitRunReturned(t, done)

	executed := false
	err := a.Post(func() { executed = true })
	if !errors.Is(err, app.ErrClosed) {
		t.Fatalf("Post after Quit = %v, want ErrClosed", err)
	}
	if executed {
		t.Fatal("posted function executed after Quit")
	}
}

func TestCloseOneOfTwoWindows(t *testing.T) {
	b := fake.New(nil)
	a := app.New(b)
	done := runAsync(a)

	type outcome struct {
		remaining []driver.Window
		err       error
	}
	result := make(chan outcome, 1)
	var w2 driver.Window
	err := a.Post(func() {
		cfg := driver.DefaultConfig()
		w1, err := a.NewWindow(cfg)
		if err != nil {
			result <- outcome{err: err}
			return
		}
		w2, err = a.NewWindow(cfg)
		if err != nil {
			result <- outcome{err: err}
			return
		}
		if err := a.CloseWindow(w1); err != nil {
			result <- outcome{err: err}
			return
		}
		result <- outcome{remaining: a.Windows()}
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	select {
	case o := <-result:
		if o.err != nil {
			t.Fatalf("window operations: %v", o.err)
		}
		if len(o.remaining) != 1 {
			t.Fatalf("Windows() after close = %d windows, want 1", len(o.remaining))
		}
		if o.remaining[0] != w2 {
			t.Fatal("Windows() does not contain the surviving window")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("window operations did not run within 2s")
	}

	if b.Created() != 2 {
		t.Fatalf("backend created %d windows, want 2", b.Created())
	}
	if b.Destroyed() != 1 {
		t.Fatalf("backend destroyed %d windows, want 1", b.Destroyed())
	}

	// The application must keep running with a single window left.
	alive := make(chan struct{})
	if err := a.Post(func() { close(alive) }); err != nil {
		t.Fatalf("Post liveness task: %v", err)
	}
	select {
	case <-alive:
	case <-time.After(2 * time.Second):
		t.Fatal("application stopped processing tasks after one window closed")
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned early with %v", err)
	default:
	}

	a.Quit()
	waitRunReturned(t, done)

	if b.Destroyed() != 2 {
		t.Fatalf("backend destroyed %d windows after shutdown, want 2", b.Destroyed())
	}
	if b.Terminated() != 1 {
		t.Fatalf("backend terminated %d times, want 1", b.Terminated())
	}
}

func TestInitFailure(t *testing.T) {
	sentinel := errors.New("fake driver init failure")
	b := fake.New(sentinel)
	a := app.New(b)

	if err := a.Run(); !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v, want sentinel %v", err, sentinel)
	}
	if b.Inited() != 1 {
		t.Fatalf("backend initialized %d times, want 1", b.Inited())
	}
	if b.Terminated() != 1 {
		t.Fatalf("backend terminated %d times after Init failure, want 1", b.Terminated())
	}
}

// spinBackend returns from WaitEvents immediately so the run loop keeps
// cycling, maximizing overlap between posting workers and shutdown.
type spinBackend struct {
	*fake.Backend
}

func (s *spinBackend) WaitEvents() {}

func TestPostDoesNotExecuteTaskInCaller(t *testing.T) {
	b := fake.New(nil)
	a := app.New(b)
	done := runAsync(a)

	waitFor(t, 2*time.Second, func() bool { return b.WaitCalls() > 0 })

	secondRan := make(chan struct{})
	entered := make(chan struct{})
	var ranBeforePostReturned bool
	if err := a.Post(func() {
		if err := a.Post(func() { close(secondRan) }); err != nil {
			t.Errorf("inner Post: %v", err)
		}
		// Post has returned here. If it executed fn inline, secondRan is
		// already closed; a queued task can only be advanced by the loop.
		select {
		case <-secondRan:
			ranBeforePostReturned = true
		default:
		}
		close(entered)
	}); err != nil {
		t.Fatalf("Post: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first task did not run within 2s")
	}
	if ranBeforePostReturned {
		t.Fatal("Post ran the task inline in the caller goroutine")
	}
	select {
	case <-secondRan:
	case <-time.After(2 * time.Second):
		t.Fatal("queued task was never advanced by the event loop")
	}

	a.Quit()
	waitRunReturned(t, done)
}

func TestAcceptedPostRunsAfterQuit(t *testing.T) {
	b := fake.New(nil)
	a := app.New(b)
	done := runAsync(a)

	waitFor(t, 2*time.Second, func() bool { return b.WaitCalls() > 0 })

	ran := make(chan struct{})
	if err := a.Post(func() { close(ran) }); err != nil {
		t.Fatalf("Post accepted before Quit: %v", err)
	}
	a.Quit()

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("task accepted before Quit never executed")
	}
	waitRunReturned(t, done)
}

func TestConcurrentPostQuitRunsEveryAcceptedTask(t *testing.T) {
	b := &spinBackend{Backend: fake.New(nil)}
	a := app.New(b)
	done := runAsync(a)

	var accepted, executed atomic.Int64
	const target = 4096
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			if err := a.Post(func() { executed.Add(1) }); err != nil {
				return // ErrClosed: shutdown is sealed.
			}
			if accepted.Add(1) >= target {
				return
			}
		}
	}()

	// Race the worker against shutdown: quit only once it has stopped
	// accepting new work, then every accepted task must have run.
	waitFor(t, 2*time.Second, func() bool { return accepted.Load() >= target })
	a.Quit()
	waitRunReturned(t, done)
	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("posting worker did not stop after Quit")
	}

	if got, want := executed.Load(), accepted.Load(); got != want {
		t.Fatalf("executed %d tasks, want %d accepted before shutdown", got, want)
	}
	if got := b.WakesAfterTerminate(); got != 0 {
		t.Fatalf("backend got %d Wake calls after Terminate", got)
	}
}

func TestQuitIsIdempotent(t *testing.T) {
	b := fake.New(nil)
	a := app.New(b)

	a.Quit()
	a.Quit()
	a.Quit()
	if got := b.WakeCalls(); got != 1 {
		t.Fatalf("Wake called %d times for three Quit calls, want 1", got)
	}

	done := runAsync(a)
	waitRunReturned(t, done)
	if b.Terminated() != 1 {
		t.Fatalf("backend terminated %d times, want 1", b.Terminated())
	}

	wakes := b.WakeCalls()
	a.Quit()
	if got := b.WakeCalls(); got != wakes {
		t.Fatalf("Quit after Run returned added %d Wake calls, want 0", got-wakes)
	}
	if got := b.WakesAfterTerminate(); got != 0 {
		t.Fatalf("backend got %d Wake calls after Terminate", got)
	}
}

func TestNoWakeAfterTerminate(t *testing.T) {
	b := fake.New(nil)
	a := app.New(b)
	done := runAsync(a)

	waitFor(t, 2*time.Second, func() bool { return b.WaitCalls() > 0 })
	a.Quit()
	waitRunReturned(t, done)

	wakes := b.WakeCalls()
	executed := false
	if err := a.Post(func() { executed = true }); !errors.Is(err, app.ErrClosed) {
		t.Fatalf("Post after shutdown = %v, want ErrClosed", err)
	}
	a.Quit()
	if executed {
		t.Fatal("posted function executed after shutdown")
	}
	if got := b.WakeCalls(); got != wakes {
		t.Fatalf("Wake calls after shutdown = %d, want 0", got-wakes)
	}
	if got := b.WakesAfterTerminate(); got != 0 {
		t.Fatalf("backend got %d Wake calls after Terminate", got)
	}
}
