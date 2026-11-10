// Package glfw implements the window and input driver backend on top of
// GLFW. Every method must be called on the UI thread — the goroutine that
// called Init and stays locked to its OS thread — except Wake, which is safe
// from any goroutine.
//
// Window destruction is deferred: Destroy marks the window, clears the GLFW
// callbacks, closes composition and queues the native destroy, which the
// backend runs from PollEvents, WaitEvents and Terminate once the GLFW callback
// stack has unwound, because GLFW forbids destroying a window from inside its
// own callbacks.
package glfw

import (
	"fmt"
	"sync"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

// Backend is the GLFW implementation of driver.Backend.
type Backend struct {
	// mu guards inited, which Wake reads from arbitrary goroutines. App's
	// wait-group already serializes Wake against Terminate; the mutex only
	// covers Wake racing Init or making Wake cheap to reason about.
	mu     sync.Mutex
	inited bool

	// pendingDestroy holds windows awaiting native destruction outside the
	// GLFW callback stack. It is only touched on the UI thread.
	pendingDestroy []*Window

	ximFactory      func() (ximClient, error)
	ximErrorHandler func(error)
	imeEnabled      bool
	initialXIM      ximClient
	windows         map[*Window]struct{}
}

// New returns a GLFW backend. The backend is not initialized until Init is
// called.
func New() *Backend {
	return &Backend{ximFactory: newDBusXIMClient}
}

// Init initializes GLFW on the calling thread.
func (b *Backend) Init() error {
	if err := glfw3.Init(); err != nil {
		return fmt.Errorf("glfw: init: %w", err)
	}
	if err := b.initXIM(); err != nil {
		glfw3.Terminate()
		return fmt.Errorf("glfw: init IME: %w", err)
	}
	b.mu.Lock()
	b.inited = true
	b.mu.Unlock()
	return nil
}

// Terminate cancels every composition, closes every input context, drains any
// pending native destroys and releases every GLFW resource. It must be the
// last backend call on the UI thread.
func (b *Backend) Terminate() {
	b.mu.Lock()
	b.inited = false
	b.mu.Unlock()
	b.imeEnabled = false
	b.closeAllXIM()
	b.flushDestroy()
	glfw3.Terminate()
}

// NewWindow validates cfg, applies the decoration hint chosen by
// cfg.Frameless, then the OpenGL 3.3 core profile window hints, and creates a
// window. A failed creation returns nil and an error and leaves the backend
// initialized and usable.
func (b *Backend) NewWindow(cfg driver.Config) (driver.Window, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// GLFW keeps window hints across window creation, so the decoration hint
	// must be set explicitly every time: a frameless window must not strip
	// decorations from later decorated ones, and a decorated window must not
	// inherit a frameless one's hint.
	if cfg.Frameless {
		glfw3.WindowHint(glfw3.Decorated, glfw3.False)
	} else {
		glfw3.WindowHint(glfw3.Decorated, glfw3.True)
	}
	glfw3.WindowHint(glfw3.ContextVersionMajor, 3)
	glfw3.WindowHint(glfw3.ContextVersionMinor, 3)
	glfw3.WindowHint(glfw3.OpenGLProfile, glfw3.OpenGLCoreProfile)
	glfw3.WindowHint(glfw3.OpenGLForwardCompatible, glfw3.True)
	win, err := glfw3.CreateWindow(cfg.Size.Width, cfg.Size.Height, cfg.Title, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("glfw: create window: %w", err)
	}
	applyWindowConfig(win, cfg)
	w := &Window{win: win, backend: b, compositionEnabled: true}
	if b.imeEnabled {
		client := b.initialXIM
		if client != nil {
			b.initialXIM = nil
		} else {
			client, err = b.createXIMClient()
			if err != nil {
				win.Destroy()
				return nil, fmt.Errorf("glfw: create IME client: %w", err)
			}
		}
		w.attachXIM(client)
	}
	if b.windows == nil {
		b.windows = make(map[*Window]struct{})
	}
	b.windows[w] = struct{}{}
	w.installCallbacks()
	return w, nil
}

// PollEvents dispatches all pending window and input events to their
// handlers, refreshes composition state, then runs queued native destroys.
// UI thread only.
func (b *Backend) PollEvents() {
	glfw3.PollEvents()
	b.refreshCompositions()
	b.flushDestroy()
}

// WaitEvents runs queued native destroys, blocks until at least one event
// arrives and dispatches it, then refreshes composition state and runs any
// native destroys queued while waiting. UI thread only.
func (b *Backend) WaitEvents() {
	b.flushDestroy()
	glfw3.WaitEvents()
	b.refreshCompositions()
	b.flushDestroy()
}

// Wake interrupts a blocking WaitEvents. It is the only method callable from
// any goroutine, and it is a no-op before Init and after Terminate, matching
// the app contract that Wake is never required to reach GLFW outside the
// initialized lifetime.
func (b *Backend) Wake() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inited {
		glfw3.PostEmptyEvent()
	}
}

// enqueueDestroy records a window whose native destroy must run outside the
// GLFW callback stack. UI thread only.
func (b *Backend) enqueueDestroy(w *Window) {
	b.pendingDestroy = append(b.pendingDestroy, w)
}

// flushDestroy performs the queued native destroys. Call only from points
// outside the GLFW callback stack. UI thread only.
func (b *Backend) flushDestroy() {
	q := b.pendingDestroy
	b.pendingDestroy = nil
	for _, w := range q {
		w.destroyNative()
	}
}
