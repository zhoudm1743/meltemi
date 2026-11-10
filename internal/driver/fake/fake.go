// Package fake provides an in-memory driver.Backend for tests. It records
// initialization, termination, window creation and destruction counts, and
// blocks in WaitEvents until Wake is called.
package fake

import (
	"sync"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

// Backend is a test double for driver.Backend.
type Backend struct {
	mu         sync.Mutex
	initErr    error
	inited     int
	terminated int
	created    int
	destroyed  int
	polls      int
	waits      int
	wakes      int
	wakesAfter int
	wakeCh     chan struct{}
}

// New returns a Backend whose Init returns initErr (nil for success).
func New(initErr error) *Backend {
	return &Backend{initErr: initErr, wakeCh: make(chan struct{}, 1)}
}

// Init records the call and returns the configured error.
func (b *Backend) Init() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inited++
	return b.initErr
}

// Terminate records the call.
func (b *Backend) Terminate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.terminated++
}

// NewWindow records the call and returns a fake window.
func (b *Backend) NewWindow(c driver.Config) (driver.Window, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.created++
	return &Window{backend: b, size: c.Size, title: c.Title}, nil
}

// PollEvents records the call.
func (b *Backend) PollEvents() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.polls++
}

// WaitEvents records the call and blocks until Wake delivers a signal.
func (b *Backend) WaitEvents() {
	b.mu.Lock()
	b.waits++
	b.mu.Unlock()
	<-b.wakeCh
}

// Wake records the call and wakes one pending (or the next) WaitEvents.
// Wakes arriving after Terminate are counted separately so tests can forbid
// them.
func (b *Backend) Wake() {
	b.mu.Lock()
	b.wakes++
	if b.terminated > 0 {
		b.wakesAfter++
	}
	b.mu.Unlock()
	select {
	case b.wakeCh <- struct{}{}:
	default:
	}
}

// Inited returns the number of Init calls.
func (b *Backend) Inited() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inited
}

// Terminated returns the number of Terminate calls.
func (b *Backend) Terminated() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.terminated
}

// Created returns the number of NewWindow calls.
func (b *Backend) Created() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.created
}

// Destroyed returns the number of Window.Destroy calls.
func (b *Backend) Destroyed() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.destroyed
}

// PollCalls returns the number of PollEvents calls.
func (b *Backend) PollCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.polls
}

// WaitCalls returns the number of WaitEvents calls.
func (b *Backend) WaitCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.waits
}

// WakeCalls returns the number of Wake calls.
func (b *Backend) WakeCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wakes
}

// WakesAfterTerminate returns the number of Wake calls that arrived after
// Terminate.
func (b *Backend) WakesAfterTerminate() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wakesAfter
}

// Window is a test double for driver.Window.
type Window struct {
	backend   *Backend
	size      driver.Size
	title     string
	shown     bool
	handler   driver.Handler
	destroyed bool
}

// Show marks the window visible.
func (w *Window) Show() { w.shown = true }

// Hide marks the window hidden.
func (w *Window) Hide() { w.shown = false }

// Destroy records the destruction on the backend.
func (w *Window) Destroy() {
	w.destroyed = true
	w.backend.mu.Lock()
	w.backend.destroyed++
	w.backend.mu.Unlock()
}

// Size returns the window size.
func (w *Window) Size() driver.Size { return w.size }

// SetHandler installs the window's event handler.
func (w *Window) SetHandler(h driver.Handler) { w.handler = h }

// SetTitle updates the fake window title unless it has been destroyed.
func (w *Window) SetTitle(title string) {
	if !w.destroyed {
		w.title = title
	}
}

// Title returns the current fake window title.
func (w *Window) Title() string { return w.title }
