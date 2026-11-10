package ui

import (
	"fmt"
	"image"
	"log"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// CloseAction controls a confirmed close request.
type CloseAction uint8

const (
	// CloseDestroy releases the renderer and destroys the native window.
	CloseDestroy CloseAction = iota
	// CloseHide hides the native window but retains its renderer and state.
	CloseHide
)

// renderer is the per-window slice of the GL renderer used by the frame
// loop. The seam keeps lifecycle and error paths testable without a GPU.
type renderer interface {
	painter
	Begin(render.Color) error
	End() error
	Close()
	Discard()
}

var windowBackground = render.Color{R: 1, G: 1, B: 1, A: 1}

// Window is a chainable native window configuration and its live UI state.
// Configuration methods are intended for use before Show; lifecycle and
// Content updates are marshaled through App.Post.
type Window struct {
	app     *App
	config  driver.Config
	root    component
	content component
	menuBar *menuBarWidget
	menus   []*MenuModel

	closeAction CloseAction
	onClose     func() bool
	onFocus     func(bool)
	onResize    func(int, int)
	onCancel    func()
	visible     bool

	target       render.Target
	renderer     renderer
	router       *eventRouter
	duplicateIDs map[string]bool

	overlays               []*overlay
	overlayDismissedPress  bool
	overlayDismissedButton driver.MouseButton
	overlayTeardown        int
}

func (w *Window) reportDuplicateIDs() {
	if w.root == nil {
		return
	}
	if w.duplicateIDs == nil {
		w.duplicateIDs = make(map[string]bool)
	}
	seen := make(map[string]bool)
	w.root.walk(func(c component) {
		receiver, ok := c.(resolvedStyleReceiver)
		if !ok {
			return
		}
		id := receiver.styleMeta().ID
		if id == "" {
			return
		}
		if seen[id] && !w.duplicateIDs[id] {
			log.Printf("ui: duplicate component ID %q", id)
			w.duplicateIDs[id] = true
		}
		seen[id] = true
	})
}

// Size sets the logical native window size in pixels.
func (w *Window) Size(width, height int) *Window {
	if w == nil {
		return nil
	}
	w.config.Size = driver.Size{Width: width, Height: height}
	return w
}

// Title sets the title used when the native window is created.
func (w *Window) Title(title string) *Window {
	if w == nil {
		return nil
	}
	w.config.Title = title
	return w
}

// SetTitle queues a native title update on the UI thread. It also updates the
// configuration retained by this Window.
func (w *Window) SetTitle(title string) {
	if w == nil {
		return
	}
	w.app.noteErr(w.app.postResult(func() error {
		w.config.Title = title
		if target, ok := w.target.(driver.TitleWindow); ok {
			target.SetTitle(title)
		}
		return nil
	}))
}

// MinSize sets the minimum logical native window size in pixels. Non-positive
// dimensions leave the corresponding native constraint unspecified.
func (w *Window) MinSize(width, height int) *Window {
	if w == nil {
		return nil
	}
	w.config.MinSize = driver.Size{Width: width, Height: height}
	return w
}

// Icon selects the native window icon. The backend copies its pixels when the
// native window is created, so the caller retains ownership of icon.
func (w *Window) Icon(icon image.Image) *Window {
	if w == nil {
		return nil
	}
	w.config.Icon = icon
	return w
}

// OnFocus installs a callback for native focus changes.
func (w *Window) OnFocus(fn func(bool)) *Window {
	if w == nil {
		return nil
	}
	w.onFocus = fn
	return w
}

// OnResize installs a callback for logical native window size changes.
func (w *Window) OnResize(fn func(width, height int)) *Window {
	if w == nil {
		return nil
	}
	w.onResize = fn
	return w
}

// OnCancel installs a callback for an unconsumed Escape press. It never closes
// the window implicitly.
func (w *Window) OnCancel(fn func()) *Window {
	if w == nil {
		return nil
	}
	w.onCancel = fn
	return w
}

// Frameless selects a window without native decorations.
func (w *Window) Frameless(frameless bool) *Window {
	if w == nil {
		return nil
	}
	w.config.Frameless = frameless
	return w
}

func targetClipboard(target driver.Window) textClipboard {
	if target == nil {
		return nil
	}
	clipboard, _ := target.(driver.Clipboard)
	return clipboard
}

func (w *Window) configureComposition() {
	if w.router != nil {
		w.router.compositionWindow, _ = w.target.(driver.CompositionWindow)
	}
	setTreeCompositionFocusHandler(w.root, w.setCompositionEnabled)
	for _, layer := range w.overlays {
		setTreeCompositionFocusHandler(layer.root, w.setCompositionEnabled)
	}
}

func (w *Window) setCompositionEnabled(enabled bool) {
	if w.target == nil {
		return
	}
	if target, ok := w.target.(interface{ SetCompositionEnabled(bool) }); ok {
		target.SetCompositionEnabled(enabled)
	}
}

func (w *Window) cancelComposition() {
	if w.router != nil {
		w.router.composing = false
		w.router.clearComposition()
	}
	w.setCompositionEnabled(false)
}

func (w *Window) updateCompositionSpot() {
	if w.router == nil || w.target == nil {
		return
	}
	target, ok := w.router.focus.(compositionTarget)
	if !ok {
		return
	}
	x, y, ok := target.compositionSpot()
	if !ok {
		return
	}
	if native, ok := w.target.(driver.CompositionWindow); ok {
		native.SetSpot(x, y)
	}
}

// Content queues a component-tree replacement on the UI thread, preserving any
// installed menu bar. A live window receives a fresh event router and is redrawn
// after open overlays and old interaction state are torn down. A styling failure
// preserves the previous tree and router.
func (w *Window) Content(content Component) *Window {
	if w == nil {
		return nil
	}
	w.app.noteErr(w.app.postResult(func() error {
		return w.replaceRoot(asComponent(content), w.menus)
	}))
	return w
}

func (w *Window) replaceRoot(content component, menus []*MenuModel) error {
	root, bar := composeMenuRoot(w, content, menus)
	if w.app.face != nil {
		if err := w.app.applyStyleTree(root, w.app.sheet); err != nil {
			return err
		}
	}
	w.cancelComposition()
	w.clearOverlaysForTeardown()
	detachTreeComposition(w.root)
	w.root = root
	w.content = content
	w.menuBar = bar
	w.menus = append([]*MenuModel(nil), menus...)
	w.reportDuplicateIDs()
	setTreeClipboard(root, targetClipboard(w.target))
	if w.target != nil {
		w.router = newEventRouter(root)
		w.router.setMenuRoots(w.menus...)
	}
	w.configureComposition()
	return nil
}

// CloseAction sets whether a confirmed close destroys or only hides the
// window.
func (w *Window) CloseAction(action CloseAction) *Window {
	if w == nil {
		return nil
	}
	w.closeAction = action
	return w
}

// OnClose installs a close-request veto callback. Returning false leaves the
// native window, renderer and component state untouched.
func (w *Window) OnClose(fn func() bool) *Window {
	if w == nil {
		return nil
	}
	w.onClose = fn
	return w
}

// Show queues native creation when necessary, installs an independent
// renderer and event router, and draws the first frame. Any initialization or
// first-frame error stops Run and unwinds established resources.
func (w *Window) Show() {
	if w == nil {
		return
	}
	w.app.noteErr(w.app.postResult(func() error {
		if w.app.stopping {
			return nil
		}
		if err := w.show(); err != nil {
			err = fmt.Errorf("ui: show window: %w", err)
			w.app.fail(err)
			return err
		}
		return nil
	}))
}

// Hide queues a native hide without releasing the renderer. Hiding the last
// visible window applies the configured last-window policy.
func (w *Window) Hide() {
	if w == nil {
		return
	}
	w.app.noteErr(w.app.postResult(func() error {
		w.hide()
		return nil
	}))
}

func (w *Window) hide() {
	w.cancelComposition()
	w.clearOverlaysForTeardown()
	if w.target == nil || !w.visible {
		return
	}
	w.target.Hide()
	w.visible = false
	if w.app.visibleWindowCount() == 0 {
		w.app.applyLastWindowPolicy()
	}
}

// Close queues a close request on the UI thread and returns only the Post
// error. The actual close result is retained for App.Run. A MakeCurrent
// failure skips the following redraw and preserves the native window and
// renderer, but the confirmed close still cancels composition and overlays.
func (w *Window) Close() error {
	if w == nil {
		return nil
	}
	return w.app.postResult(w.requestClose)
}

func (w *Window) show() error {
	if w.target != nil {
		w.target.Show()
		w.visible = true
		w.setCompositionEnabled(true)
		return w.redraw()
	}

	native, err := w.app.core.NewWindow(w.config)
	if err != nil {
		return err
	}
	target, ok := native.(render.Target)
	if !ok {
		_ = w.app.core.CloseWindow(native)
		return fmt.Errorf("ui: native window %T is not a render target", native)
	}
	if w.app.face == nil {
		face, faceErr := w.app.newFace()
		if faceErr != nil {
			_ = w.app.core.CloseWindow(native)
			return faceErr
		}
		w.app.face = face
		w.app.markFaceCreated()
	}
	if err := w.app.applyWindowStyles(w, w.app.sheet); err != nil {
		_ = w.app.core.CloseWindow(native)
		if w.app.windowCount() == 0 {
			if cleanupErr := w.app.releaseFace(); cleanupErr != nil {
				return fmt.Errorf("apply styles: %w (face cleanup: %v)", err, cleanupErr)
			}
		}
		return err
	}
	clipboard := targetClipboard(native)
	setTreeClipboard(w.root, clipboard)
	for _, layer := range w.overlays {
		setTreeClipboard(layer.root, clipboard)
	}

	r, err := w.app.newRenderer(target)
	if err != nil {
		_ = w.app.core.CloseWindow(native)
		if w.app.windowCount() == 0 {
			if cleanupErr := w.app.releaseFace(); cleanupErr != nil {
				return fmt.Errorf("create renderer: %w (face cleanup: %v)", err, cleanupErr)
			}
		}
		return err
	}
	w.target = target
	w.renderer = r
	w.router = newEventRouter(w.activeRoot())
	w.router.setMenuRoots(w.menus...)
	w.configureComposition()
	w.app.addWindow(w)
	native.SetHandler(w.handleEvent)
	target.Show()
	w.visible = true
	if err := w.redraw(); err != nil {
		return fmt.Errorf("first frame: %w", err)
	}
	return nil
}

func (w *Window) requestClose() error {
	if w.onClose != nil && !w.onClose() {
		return nil
	}
	if w.closeAction == CloseHide {
		w.hide()
		return nil
	}
	return w.destroy(true)
}

// destroy binds this window's own context before touching any GL objects. If
// binding fails, nothing is closed, removed or destroyed: the current context
// may belong to another window with identically numbered GL objects. Composition
// and overlays are still cancelled because the close was already confirmed.
func (w *Window) destroy(applyLastWindowPolicy bool) error {
	w.cancelComposition()
	w.clearOverlaysForTeardown()
	if w.target == nil || w.renderer == nil {
		return nil
	}
	target := w.target
	wasVisible := w.visible
	if err := target.MakeCurrent(); err != nil {
		return fmt.Errorf("ui: make window context current: %w", err)
	}
	w.renderer.Close()

	w.target = nil
	w.renderer = nil
	w.router = nil
	w.visible = false
	w.app.removeWindow(w)
	if err := w.app.core.CloseWindow(target); err != nil {
		return err
	}

	var first error
	if w.app.windowCount() == 0 {
		first = w.app.releaseFace()
	}
	if applyLastWindowPolicy && wasVisible && w.app.visibleWindowCount() == 0 {
		w.app.applyLastWindowPolicy()
	}
	return first
}

// finalize releases one window during application shutdown. A failed context
// binding skips renderer.Close and discards only its Go-side text and image caches, but
// does not leave a public handle pretending to be alive after the internal run
// loop destroys its native window. Removing the window from the core here also
// prevents a second Destroy at Run exit.
func (w *Window) finalize() error {
	w.cancelComposition()
	w.clearOverlaysForTeardown()
	target := w.target
	r := w.renderer
	var first error
	if target != nil {
		if err := target.MakeCurrent(); err != nil {
			first = fmt.Errorf("ui: make window context current: %w", err)
			if r != nil {
				r.Discard()
			}
		} else if r != nil {
			r.Close()
		}
	}

	w.target = nil
	w.renderer = nil
	w.router = nil
	w.visible = false
	w.app.removeWindow(w)
	if target != nil {
		if err := w.app.core.CloseWindow(target); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (w *Window) redraw() error {
	if w.target == nil || w.renderer == nil || w.root == nil {
		return nil
	}
	w.reconcileMenuOverlays()
	if err := w.app.applyWindowStyles(w, w.app.sheet); err != nil {
		return err
	}
	size := w.target.Size()
	layoutComponent(w.root, render.Rect{Width: float64(size.Width), Height: float64(size.Height)})
	for _, layer := range w.overlays {
		layoutComponent(layer.root, layer.bounds)
	}

	if err := w.renderer.Begin(windowBackground); err != nil {
		return err
	}
	ctx := &drawContext{face: w.app.face, painter: w.renderer, opacity: 1}
	drawResolvedComponent(ctx, w.root)
	for _, layer := range w.overlays {
		layer.draw(ctx)
	}
	first := ctx.Err()
	if err := w.renderer.End(); err != nil && first == nil {
		first = err
	}
	if first == nil {
		w.updateCompositionSpot()
	}
	return first
}

func (w *Window) handleEvent(event driver.Event) {
	if w.app.stopping || w.target == nil || !w.visible {
		return
	}
	invalidated := w.reconcileMenuOverlays()
	switch event.Kind {
	case driver.EventCloseRequested:
		w.app.noteErr(w.requestClose())
	case driver.EventFocus:
		if w.onFocus != nil {
			w.onFocus(event.Focused)
			invalidated = true
		}
		if invalidated {
			if err := w.redraw(); err != nil {
				w.app.fail(fmt.Errorf("ui: redraw: %w", err))
			}
		}
	case driver.EventResize:
		w.dismissMenuOverlays()
		if w.onResize != nil {
			w.onResize(event.Size.Width, event.Size.Height)
		}
		if err := w.redraw(); err != nil {
			w.app.fail(fmt.Errorf("ui: redraw: %w", err))
		}
	case driver.EventFramebufferResize, driver.EventPaint:
		if err := w.redraw(); err != nil {
			w.app.fail(fmt.Errorf("ui: redraw: %w", err))
		}
	default:
		if w.router == nil {
			return
		}
		handled, redraw, routed := w.routeOverlayEvent(event)
		if !routed {
			handled, redraw = w.router.Handle(event)
		}
		if !handled && event.Kind == driver.EventKey && event.Key == driver.KeyEscape && event.Action == driver.ActionPress && w.onCancel != nil {
			w.onCancel()
			redraw = true
		}
		if redraw || invalidated {
			if err := w.redraw(); err != nil {
				w.app.fail(fmt.Errorf("ui: redraw: %w", err))
			}
		}
	}
}
