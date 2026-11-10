package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// pointerTarget marks leaf components that consume pointer events. Only
// these can be the target of a targeted pointer dispatch: containers
// route structurally and never implement this, so a pointer resting in a
// column's gap or padding hits nothing instead of broadcasting to every
// child through the column's aggregate handleEvent.
type pointerTarget interface {
	acceptsPointer() bool
}

// focusableComponent is the internal contract of components that can take
// keyboard focus. Only interactive controls implement it; the router
// refuses to focus a component whose focusable method reports false.
type focusableComponent interface {
	component
	focusable() bool
	isFocused() bool
	setFocused(bool)
}

// hoverClearer is implemented by components that track the cursor and
// must drop that state when the router retargets a move.
type hoverClearer interface {
	clearHover() bool
}

// activatableComponent is the internal contract of components that react
// to Enter while focused.
type activatableComponent interface {
	activate()
}

// scrollConsumer is implemented by containers that consume wheel events
// over their viewport. The router resolves the chain of nested consumers
// under the cursor and offers the event to the innermost first; one that
// cannot move — already at an edge, or a viewport as large as its content —
// leaves the event unhandled so the enclosing scroll consumes it.
type scrollConsumer interface {
	consumeScroll(e driver.Event) (handled, redraw bool)
}

// eventRouter is the single entry point that turns the broadcast
// component tree into targeted input routing: pointer events go to the
// interactive leaf under the cursor, keyboard events to the focused one,
// and every event is delivered at most once. The window loop owns one
// router and asks it whether an event was handled and whether the frame
// needs a repaint.
type eventRouter struct {
	root              component
	focus             component // focused component, nil until something takes focus
	hover             component // interactive leaf under the cursor, nil off every leaf
	press             component // leaf that armed the last left press, nil when none
	compositionWindow driver.CompositionWindow
	menus             []*MenuModel // menu roots consulted for shortcuts, nil until a window attaches them
	composing         bool         // an IME composition session owns the keyboard
}

// newEventRouter creates the router for an arranged component tree.
func newEventRouter(root component) *eventRouter {
	if root == nil {
		panic("ui: event router requires a root component")
	}
	return &eventRouter{root: root}
}

// Handle routes one window event and reports whether it was handled and
// whether it changed something visible.
func (r *eventRouter) Handle(e driver.Event) (handled, redraw bool) {
	switch e.Kind {
	case driver.EventMouseMove:
		return r.handleMouseMove(e)
	case driver.EventMouseButton:
		return r.handleMouseButton(e)
	case driver.EventCursorLeave:
		// The cursor left the window: every component drops its hover
		// state, while an armed press survives so the matching release
		// can still cancel instead of vanishing. This is the one pointer
		// path that broadcasts: there is no "under the cursor" to
		// target, and every leaf must clear.
		r.hover = nil
		return r.root.handleEvent(e)
	case driver.EventKey:
		return r.handleKey(e)
	case driver.EventRune:
		return r.handleRune(e)
	case driver.EventCompositionStart, driver.EventCompositionUpdate, driver.EventCompositionEnd:
		return r.handleComposition(e)
	case driver.EventScroll:
		return r.handleScroll(e)
	default:
		return false, false
	}
}

func (r *eventRouter) handleMouseMove(e driver.Event) (handled, redraw bool) {
	target := r.hitTest(e.Position)
	if r.press != nil {
		target = r.press
	}
	if r.hover != nil && r.hover != target {
		if clear, ok := r.hover.(hoverClearer); ok && clear.clearHover() {
			redraw = true
		}
	}
	r.hover = target
	if target == nil {
		return false, redraw
	}
	handled, targetRedraw := target.handleEvent(e)
	return handled, redraw || targetRedraw
}

func (r *eventRouter) handleMouseButton(e driver.Event) (handled, redraw bool) {
	if e.Button != driver.MouseButtonLeft {
		return false, false
	}
	if e.Action == driver.ActionPress {
		target := r.hitTest(e.Position)
		r.press = target
		var redraw bool
		if f, ok := target.(focusableComponent); ok && f.focusable() {
			redraw = r.setFocus(f)
		}
		if target == nil {
			return false, redraw
		}
		handled, targetRedraw := target.handleEvent(e)
		return handled, redraw || targetRedraw
	}
	if e.Action != driver.ActionRelease {
		return false, false
	}
	// The release goes to the leaf that armed the press, even when the
	// cursor has moved elsewhere; that is what turns an outside release
	// into a cancel instead of a click. A release with no armed press
	// falls back to whatever is under the cursor.
	target := r.press
	r.press = nil
	if target == nil {
		target = r.hitTest(e.Position)
	}
	if target == nil {
		return false, false
	}
	return target.handleEvent(e)
}

// setMenuRoots attaches the menu trees whose shortcuts this router dispatches
// after the focused component leaves a key unconsumed. It is the internal
// seam the menu bar installation uses; routers without menu roots dispatch
// exactly as before.
func (r *eventRouter) setMenuRoots(menus ...*MenuModel) {
	r.menus = menus
}

// matchMenuShortcut searches the attached menu roots in attachment order,
// depth-first through each tree, for an item whose shortcut exactly equals
// the key event's modifier mask and normalized rune. Separators, submenu
// owners, disabled items and disabled subtrees stay inert; the first
// activatable match fires once, toggling its checked state before the
// callback runs.
func (r *eventRouter) matchMenuShortcut(e driver.Event) bool {
	shortcut, ok := e.Shortcut()
	if !ok {
		return false
	}
	mods, key := Modifiers(shortcut.Mods), shortcut.Key
	for _, menu := range r.menus {
		if menu == nil {
			continue
		}
		if item := matchMenuItem(menu.Items, mods, key); item != nil {
			item.trigger()
			return true
		}
	}
	return false
}

// matchMenuItem returns the first activatable item in items whose shortcut
// matches exactly, or nil. Disabled items are skipped — a later enabled item
// with the same shortcut can still fire — while a disabled submenu item
// silences its whole subtree.
func matchMenuItem(items []*MenuItemModel, mods Modifiers, key rune) *MenuItemModel {
	for _, item := range items {
		if item == nil || !item.enabled {
			continue
		}
		if item.submenu != nil {
			if found := matchMenuItem(item.submenu.Items, mods, key); found != nil {
				return found
			}
			continue
		}
		if item.separator {
			continue
		}
		if item.shortcutMatch(mods, key) {
			return item
		}
	}
	return nil
}

func (r *eventRouter) handleKey(e driver.Event) (handled, redraw bool) {
	if e.Action != driver.ActionPress && e.Action != driver.ActionRepeat {
		return false, false
	}
	switch e.Key {
	case driver.KeyTab:
		if e.Action != driver.ActionPress {
			return false, false
		}
		r.validateFocus()
		return r.moveFocus(e.Mods&driver.ModShift != 0)
	case driver.KeyEnter:
		if e.Action != driver.ActionPress || !r.validateFocus() {
			return false, false
		}
		if target, ok := r.focus.(activatableComponent); ok {
			target.activate()
			return true, true
		}
		return false, false
	default:
		var handled, redraw bool
		if r.validateFocus() {
			handled, redraw = r.focus.handleEvent(e)
		}
		if handled || r.composing {
			// Keys delivered during an active composition belong to the IME
			// and must not reach menu matching; keys the IME consumed never
			// become events at all, and this gate covers the rest.
			return handled, redraw
		}
		if r.matchMenuShortcut(e) {
			return true, true
		}
		return false, redraw
	}
}

func (r *eventRouter) handleComposition(e driver.Event) (handled, redraw bool) {
	if e.Kind == driver.EventCompositionEnd {
		r.composing = false
		redraw = r.clearComposition()
		return redraw, redraw
	}
	r.composing = true
	if !r.validateFocus() {
		return false, false
	}
	target, ok := r.focus.(compositionTarget)
	if !ok {
		return false, false
	}
	source := r.compositionWindow
	if native, ok := e.Window.(driver.CompositionWindow); ok {
		source = native
	}
	if source == nil {
		return false, false
	}
	value, caret, active := source.Preedit()
	changed := target.setComposition(value, caret, active)
	return true, changed
}

func (r *eventRouter) clearComposition() bool {
	changed := false
	if r.root != nil {
		r.root.walk(func(c component) {
			if target, ok := c.(compositionTarget); ok && target.clearComposition() {
				changed = true
			}
		})
	}
	return changed
}

func (r *eventRouter) handleRune(e driver.Event) (handled, redraw bool) {
	if !r.validateFocus() {
		return false, false
	}
	return r.focus.handleEvent(e)
}

// handleScroll routes a wheel event to the scroll viewport under the
// cursor. The chain is collected innermost first and offered in that order
// until one consumes it, so a wheel over inert content — a button, a
// viewport already at its edge — still scrolls the nearest enclosing
// viewport instead of vanishing.
func (r *eventRouter) handleScroll(e driver.Event) (handled, redraw bool) {
	for _, consumer := range scrollConsumersAt(r.root, e.Position) {
		if h, rd := consumer.consumeScroll(e); h {
			return true, rd
		}
	}
	return false, false
}

// validateFocus clears the focus when the focused component can no longer
// hold it. A component holds focus only while it is still focusable AND
// still carries its focus flag: disabling a focused button clears the
// flag, so re-enabling before the next key event must not resurrect the
// old focus — Enter would otherwise activate a button without a focus
// ring, and Tab would continue from a stale position. It reports whether
// a live focus remains.
func (r *eventRouter) validateFocus() bool {
	if r.focus == nil {
		return false
	}
	f, ok := r.focus.(focusableComponent)
	if ok && f.focusable() && f.isFocused() {
		return true
	}
	if ok {
		f.setFocused(false)
	}
	r.focus = nil
	return false
}

// moveFocus walks focus forward or backward through the focusable
// components in tree order, wrapping around at both ends. With no current
// focus it lands on the first component, or the last when moving
// backward.
func (r *eventRouter) moveFocus(backward bool) (handled, redraw bool) {
	var focusable []focusableComponent
	r.root.walk(func(c component) {
		if f, ok := c.(focusableComponent); ok && f.focusable() {
			focusable = append(focusable, f)
		}
	})
	if len(focusable) == 0 {
		return false, false
	}

	index := -1
	for i, f := range focusable {
		if f == r.focus {
			index = i
			break
		}
	}
	switch {
	case index == -1:
		if backward {
			return true, r.setFocus(focusable[len(focusable)-1])
		}
		return true, r.setFocus(focusable[0])
	case backward:
		return true, r.setFocus(focusable[(index+len(focusable)-1)%len(focusable)])
	default:
		return true, r.setFocus(focusable[(index+1)%len(focusable)])
	}
}

func (r *eventRouter) activateRoot(root component) bool {
	if root == nil {
		panic("ui: event router requires a root component")
	}
	changed := r.resetInteraction()
	r.root = root
	return changed
}

func (r *eventRouter) resetInteraction() bool {
	r.composing = false
	pressed := r.press
	r.press = nil
	changed := false
	if pressed != nil {
		point := driver.Point{X: -1, Y: -1}
		if reporter, ok := pressed.(boundsReporter); ok {
			bounds := reporter.arrangedBounds()
			point = driver.Point{X: bounds.X + bounds.Width, Y: bounds.Y + bounds.Height}
		}
		_, redraw := pressed.handleEvent(driver.Event{
			Kind: driver.EventMouseButton, Button: driver.MouseButtonLeft,
			Action: driver.ActionRelease, Position: point,
		})
		changed = redraw
	}
	if r.hover != nil {
		if clearer, ok := r.hover.(hoverClearer); ok && clearer.clearHover() {
			changed = true
		}
	}
	r.hover = nil
	previous := r.focus
	r.focus = nil
	if focused, ok := previous.(focusableComponent); ok {
		changed = focused.isFocused() || changed
		focused.setFocused(false)
	}
	return r.clearComposition() || changed
}

// setFocus moves the focus and notifies both ends. It reports whether the
// visible focus changed, including restoring a stale same-target focus flag.
func (r *eventRouter) setFocus(next focusableComponent) bool {
	if r.focus == next && next.isFocused() {
		return false
	}
	r.composing = false
	r.clearComposition()
	if prev, ok := r.focus.(focusableComponent); ok {
		prev.setFocused(false)
	}
	r.focus = next
	next.setFocused(true)
	return true
}

// hitTest returns the interactive leaf under p, resolved through
// recursive container traversal: each container descends only into the
// children whose arranged subtree can contain p, checks them in paint
// order — declaration order, except Stack which checks in reverse so the
// topmost child wins — and returns the first hit. A point therefore maps
// to at most one leaf no matter how siblings overlap; the old assumption
// that column siblings never overlap is gone. A point outside a
// container's arranged bounds never reaches its children, which keeps
// gaps and padding out of the hit path and is the clip boundary later
// containers such as Scroll build on. Containers are not pointer targets,
// so their own gaps and padding hit nothing.
func (r *eventRouter) hitTest(p driver.Point) component {
	return hitTestComponent(r.root, p)
}

// hitTestComponent resolves p inside one subtree and returns the deepest
// pointer-accepting leaf there, or nil when nothing qualifies.
func hitTestComponent(c component, p driver.Point) component {
	if container, ok := c.(childContainer); ok {
		if bounds, ok := c.(boundsReporter); ok && !boundsContain(bounds.arrangedBounds(), p) {
			return nil
		}
		children := container.childComponents()
		if _, stacked := c.(*StackLayout); stacked {
			// Reverse declaration order: the last child paints on top,
			// so it must be the first to receive the pointer.
			for i := len(children) - 1; i >= 0; i-- {
				if hit := hitTestComponent(children[i], p); hit != nil {
					return hit
				}
			}
			return nil
		}
		for _, child := range children {
			if hit := hitTestComponent(child, p); hit != nil {
				return hit
			}
		}
		return nil
	}
	target, ok := c.(pointerTarget)
	if !ok || !target.acceptsPointer() {
		return nil
	}
	bounds, ok := c.(boundsReporter)
	if !ok || !boundsContain(bounds.arrangedBounds(), p) {
		return nil
	}
	return c
}

// boundsContain reports whether p lies inside b. Zero-size bounds contain
// nothing, so a collapsed component never receives input.
func boundsContain(b render.Rect, p driver.Point) bool {
	return p.X >= b.X && p.X < b.X+b.Width &&
		p.Y >= b.Y && p.Y < b.Y+b.Height
}

// scrollConsumersAt returns the scroll consumers whose arranged bounds
// contain p, ordered innermost first: children are consulted before their
// container, so the deepest viewport under the cursor gets the wheel and
// an at-limit one falls through to the scroll enclosing it. The traversal
// uses the same container gating and paint order as hitTestComponent. A Stack
// selects only its topmost branch with a scroll consumer, so an at-limit top
// Scroll falls through to actual ancestors, never to a covered sibling.
func scrollConsumersAt(c component, p driver.Point) []scrollConsumer {
	if bounds, ok := c.(boundsReporter); ok && !boundsContain(bounds.arrangedBounds(), p) {
		return nil
	}
	var chain []scrollConsumer
	if container, ok := c.(childContainer); ok {
		children := container.childComponents()
		if _, stacked := c.(*StackLayout); stacked {
			for i := len(children) - 1; i >= 0; i-- {
				childChain := scrollConsumersAt(children[i], p)
				if len(childChain) > 0 {
					chain = childChain
					break
				}
			}
		} else {
			for _, child := range children {
				chain = append(chain, scrollConsumersAt(child, p)...)
			}
		}
	}
	if consumer, ok := c.(scrollConsumer); ok {
		chain = append(chain, consumer)
	}
	return chain
}
