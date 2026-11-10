package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

type overlay struct {
	root    component
	bounds  render.Rect
	dismiss func()
	parent  *overlay
}

func (w *Window) pushOverlay(root component, bounds render.Rect, dismiss func(), parent *overlay) *overlay {
	if root == nil || w.overlayTeardown > 0 {
		return nil
	}
	if w.app != nil && w.app.face != nil {
		if err := w.app.applyStyleTree(root, w.app.sheet); err != nil {
			w.app.noteErr(err)
			return nil
		}
	}
	layoutComponent(root, bounds)
	layer := &overlay{root: root, bounds: bounds, dismiss: dismiss, parent: parent}
	w.overlays = append(w.overlays, layer)
	setTreeClipboard(root, targetClipboard(w.target))
	setTreeCompositionFocusHandler(root, w.setCompositionEnabled)
	if w.router != nil {
		w.router.activateRoot(root)
	}
	return layer
}

func (w *Window) popOverlay() {
	w.removeOverlays(len(w.overlays) - 1)
}

func (w *Window) clearOverlays() {
	w.overlayDismissedPress = false
	w.removeOverlays(0)
}

func (w *Window) clearOverlaysForTeardown() {
	w.overlayTeardown++
	defer func() { w.overlayTeardown-- }()
	w.clearOverlays()
}

func (w *Window) removeOverlays(first int) {
	if first < 0 || first >= len(w.overlays) {
		return
	}
	removed := append([]*overlay(nil), w.overlays[first:]...)
	w.overlays = w.overlays[:first]
	w.finishOverlayRemoval(removed)
}

func (w *Window) removeOverlayDescendants(ancestor *overlay) bool {
	ancestorIndex := -1
	for i, layer := range w.overlays {
		if layer == ancestor {
			ancestorIndex = i
			break
		}
	}
	if ancestorIndex < 0 {
		return false
	}
	var removed []*overlay
	survivors := make([]*overlay, 0, len(w.overlays))
	for i, layer := range w.overlays {
		if i > ancestorIndex && overlayIsDescendant(layer, ancestor) {
			removed = append(removed, layer)
			continue
		}
		survivors = append(survivors, layer)
	}
	if len(removed) == 0 {
		return false
	}
	w.overlays = survivors
	w.finishOverlayRemoval(removed)
	return true
}

func overlayIsDescendant(layer, ancestor *overlay) bool {
	for parent := layer.parent; parent != nil; parent = parent.parent {
		if parent == ancestor {
			return true
		}
	}
	return false
}

func (w *Window) finishOverlayRemoval(removed []*overlay) {
	if w.router != nil {
		activeRemoved := false
		for _, layer := range removed {
			if layer.root == w.router.root {
				activeRemoved = true
				break
			}
		}
		if activeRemoved {
			w.router.activateRoot(w.activeRoot())
		}
	}
	for i := len(removed) - 1; i >= 0; i-- {
		detachTreeComposition(removed[i].root)
		setTreeClipboard(removed[i].root, nil)
	}
	for i := len(removed) - 1; i >= 0; i-- {
		if removed[i].dismiss != nil {
			removed[i].dismiss()
		}
	}
}

func (w *Window) activeRoot() component {
	if len(w.overlays) != 0 {
		return w.overlays[len(w.overlays)-1].root
	}
	return w.root
}

func (w *Window) prepareStyles(transaction *styleTransaction, sheet *internalstyle.Sheet) error {
	if err := transaction.prepare(w.root, sheet); err != nil {
		return err
	}
	for _, layer := range w.overlays {
		if err := transaction.prepare(layer.root, sheet); err != nil {
			return err
		}
	}
	return nil
}

func (o *overlay) draw(ctx *drawContext) {
	ctx.SetClip(o.bounds)
	defer ctx.ClearClip()
	drawResolvedComponent(ctx, o.root)
}

func (w *Window) overlayAt(point driver.Point) int {
	for i := len(w.overlays) - 1; i >= 0; i-- {
		if boundsContain(w.overlays[i].bounds, point) {
			return i
		}
	}
	return -1
}

func (w *Window) routeOverlayMouseMove(event driver.Event) (handled, redraw bool) {
	changed := false
	if w.router.press == nil {
		if index := w.overlayAt(event.Position); index >= 0 {
			selected := w.overlays[index]
			active := w.overlays[len(w.overlays)-1]
			if selected != active && overlayIsDescendant(active, selected) {
				changed = w.removeOverlayDescendants(selected)
			}
			if len(w.overlays) != 0 && w.overlays[len(w.overlays)-1] == selected {
				handled, redraw = w.router.Handle(event)
				return handled, changed || redraw
			}
		}
	}
	handled, redraw = w.router.Handle(event)
	return handled, changed || redraw
}

func (w *Window) routeOverlayEvent(event driver.Event) (handled, redraw, routed bool) {
	if w.overlayDismissedPress && (event.Kind == driver.EventMouseButton || event.Kind == driver.EventMouseMove) {
		if event.Kind == driver.EventMouseButton && event.Button == w.overlayDismissedButton && event.Action == driver.ActionRelease {
			w.overlayDismissedPress = false
		}
		return true, false, true
	}
	if len(w.overlays) == 0 {
		return false, false, false
	}
	if handled, redraw, routed := w.routeMenuTitlePress(event); routed {
		return handled, redraw, true
	}
	if event.Kind == driver.EventKey && event.Key == driver.KeyEscape {
		if event.Action == driver.ActionPress {
			w.popOverlay()
			return true, true, true
		}
		return true, false, true
	}
	if event.Kind == driver.EventScroll {
		index := w.overlayAt(event.Position)
		if index < 0 {
			return true, false, true
		}
		for _, consumer := range scrollConsumersAt(w.overlays[index].root, event.Position) {
			if consumed, changed := consumer.consumeScroll(event); consumed {
				return true, changed, true
			}
		}
		return true, false, true
	}
	if event.Kind == driver.EventMouseButton && event.Action == driver.ActionPress {
		index := w.overlayAt(event.Position)
		if index < 0 {
			w.clearOverlays()
			w.overlayDismissedPress = true
			w.overlayDismissedButton = event.Button
			return true, true, true
		}
		if event.Button == driver.MouseButtonLeft {
			selected := w.overlays[index]
			changed := w.removeOverlayDescendants(selected)
			if len(w.overlays) == 0 || w.overlays[len(w.overlays)-1] != selected {
				return true, changed, true
			}
			_, targetRedraw := w.router.Handle(event)
			return true, changed || targetRedraw, true
		}
	}
	if event.Kind == driver.EventMouseMove {
		_, redraw := w.routeOverlayMouseMove(event)
		return true, redraw, true
	}
	if event.Kind == driver.EventMouseButton || event.Kind == driver.EventCursorLeave {
		_, redraw := w.router.Handle(event)
		return true, redraw, true
	}
	handled, redraw = w.router.Handle(event)
	return handled, redraw, true
}
