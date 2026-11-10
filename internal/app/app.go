// Package app implements the UI-thread event loop that owns the driver
// backend and every top-level window created through it.
package app

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

// ErrClosed is returned by Post after the application has quit or its run
// loop has finished.
var ErrClosed = errors.New("ui: application closed")

// App owns the driver backend and the set of live windows. All backend calls
// happen on the UI thread locked by Run; Post is safe from any goroutine.
type App struct {
	backend driver.Backend

	mu     sync.Mutex
	queue  []func()
	closed bool
	// pending tracks in-flight backend.Wake calls so Terminate never runs
	// before a started wake completes.
	pending sync.WaitGroup

	windows map[driver.Window]struct{}
}

// New returns an App that drives the given backend. The backend is not
// initialized until Run is called.
func New(backend driver.Backend) *App {
	return &App{
		backend: backend,
		windows: make(map[driver.Window]struct{}),
	}
}

// Run locks the calling goroutine to its OS thread, initializes the backend,
// drains tasks posted before Run, and then loops over queued tasks,
// PollEvents and WaitEvents until Quit is called. Before returning it drains
// once more so no accepted task is lost, destroys every remaining window,
// and terminates the backend. Closing all windows does not stop the loop.
func (a *App) Run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := a.backend.Init(); err != nil {
		a.markClosed()
		a.pending.Wait()
		a.backend.Terminate()
		return err
	}

	a.drain()
	for {
		a.drain()
		if a.isClosed() {
			break
		}
		a.backend.PollEvents()
		a.backend.WaitEvents()
	}
	// The loop only breaks once closed is set, and Post rejects new tasks
	// once closed is set, so this final drain runs every task accepted
	// before shutdown without a loss window.
	a.drain()

	for w := range a.windows {
		w.Destroy()
	}
	a.windows = make(map[driver.Window]struct{})
	a.markClosed()
	a.pending.Wait()
	a.backend.Terminate()
	return nil
}

// Post queues fn for execution on the UI thread during Run and wakes the
// loop. It never runs fn in the caller's goroutine. Once the app is closed,
// Post returns ErrClosed and fn is dropped.
func (a *App) Post(fn func()) error {
	if fn == nil {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrClosed
	}
	a.queue = append(a.queue, fn)
	a.pending.Add(1)
	a.mu.Unlock()
	a.wake()
	return nil
}

// Quit asks Run to finish: already queued tasks run to completion, the
// remaining windows are destroyed and the backend is terminated. Quit is
// idempotent and only the first call wakes the loop.
func (a *App) Quit() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.pending.Add(1)
	a.mu.Unlock()
	a.wake()
}

// NewWindow creates a window through the backend and registers it with the
// app. It must be called on the UI thread, e.g. from a function passed to
// Post.
func (a *App) NewWindow(c driver.Config) (driver.Window, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	w, err := a.backend.NewWindow(c)
	if err != nil {
		return nil, err
	}
	a.windows[w] = struct{}{}
	return w, nil
}

// CloseWindow destroys w and unregisters it. It must be called on the UI
// thread.
func (a *App) CloseWindow(w driver.Window) error {
	if _, ok := a.windows[w]; !ok {
		return fmt.Errorf("ui: window %v is not owned by this app", w)
	}
	w.Destroy()
	delete(a.windows, w)
	return nil
}

// Windows returns a copy of the live window set. It must be called on the UI
// thread.
func (a *App) Windows() []driver.Window {
	out := make([]driver.Window, 0, len(a.windows))
	for w := range a.windows {
		out = append(out, w)
	}
	return out
}

// drain runs every queued task in FIFO order.
func (a *App) drain() {
	for {
		a.mu.Lock()
		if len(a.queue) == 0 {
			a.mu.Unlock()
			return
		}
		fn := a.queue[0]
		a.queue[0] = nil
		a.queue = a.queue[1:]
		a.mu.Unlock()
		fn()
	}
}

// wake delivers a pending wake to the backend. Every caller must hold one
// pending count, taken while a.mu was held, so Run's final pending.Wait
// blocks until this wake completes before Terminate runs.
func (a *App) wake() {
	a.backend.Wake()
	a.pending.Done()
}

func (a *App) markClosed() {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
}

func (a *App) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}
