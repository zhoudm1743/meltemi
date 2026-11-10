package ui

import (
	"strings"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
	"github.com/zhoudm1743/meltemi/internal/text"
)

const (
	menuBarDefaultHeight  = 32.0
	menuItemDefaultHeight = 30.0
	menuPopupMinWidth     = 160.0
	menuSeparatorHeight   = 9.0
	menuCheckWidth        = 20.0
	menuArrowWidth        = 16.0
)

type menuBarWidget struct {
	window     *Window
	children   []component
	sizes      []Size
	bounds     render.Rect
	horizontal bool
	popup      bool
	resolved   internalstyle.Resolved
}

func newMenuBarWidget(window *Window, menus []*MenuModel) *menuBarWidget {
	bar := &menuBarWidget{window: window, horizontal: true}
	for _, model := range menus {
		if model == nil {
			continue
		}
		bar.children = append(bar.children, &menuItemWidget{
			textBlock: textBlock{text: model.Title},
			menu:      model,
			window:    window,
			model:     MenuItem(model.Title),
		})
	}
	return bar
}

func newMenuPopupWidget(window *Window, items []*MenuItemModel) *menuBarWidget {
	popup := &menuBarWidget{window: window, popup: true}
	for _, model := range items {
		if model == nil {
			continue
		}
		popup.children = append(popup.children, &menuItemWidget{
			textBlock: textBlock{text: model.Text},
			model:     model,
			window:    window,
		})
	}
	return popup
}

func (b *menuBarWidget) styleMeta() internalstyle.Meta {
	class := ""
	if b.popup {
		class = "popup"
	}
	return internalstyle.Meta{Type: "MenuBar", Class: class}
}

func (b *menuBarWidget) setResolved(resolved internalstyle.Resolved) {
	b.resolved = resolved
}

func (b *menuBarWidget) measure(constraints Constraints) Size {
	style := menuBarComponentStyle(b, b.resolved)
	inset := style.borderWidth
	inner := deflate(constraints, inset)
	inner.Min = Size{}
	inner.Max.Width = subtractConstraint(inner.Max.Width, style.padding.horizontal())
	inner.Max.Height = subtractConstraint(inner.Max.Height, style.padding.vertical())
	b.sizes = b.sizes[:0]
	content := Size{}
	for _, child := range b.children {
		size := Size{}
		if !constraintExhausted(constraints, style) {
			size = child.measure(inner)
		}
		b.sizes = append(b.sizes, size)
		if b.horizontal {
			content.Width += size.Width
			content.Height = max(content.Height, size.Height)
		} else {
			content.Width = max(content.Width, size.Width)
			content.Height += size.Height
		}
	}
	natural := Size{
		Width:  content.Width + style.padding.horizontal() + 2*style.borderWidth,
		Height: content.Height + style.padding.vertical() + 2*style.borderWidth,
	}
	if _, ok := styleDimension(b.resolved.Values, "width"); !ok {
		style.width = natural.Width
	}
	if _, ok := styleDimension(b.resolved.Values, "height"); !ok {
		style.height = natural.Height
		if b.horizontal {
			style.height = max(menuBarDefaultHeight, style.height)
		}
	}
	return clampSize(Size{Width: style.width, Height: style.height}, constraints)
}

func subtractConstraint(value, inset float64) float64 {
	if value <= 0 {
		return 0
	}
	return max(value-inset, 0)
}

func constraintExhausted(constraints Constraints, style componentStyle) bool {
	insetWidth := style.padding.horizontal() + 2*style.borderWidth
	insetHeight := style.padding.vertical() + 2*style.borderWidth
	return constraints.Max.Width > 0 && constraints.Max.Width <= insetWidth ||
		constraints.Max.Height > 0 && constraints.Max.Height <= insetHeight
}

func (b *menuBarWidget) arrange(bounds render.Rect) {
	b.bounds = bounds
	style := menuBarComponentStyle(b, b.resolved)
	content := styleContentRect(bounds, style)
	x, y := content.X, content.Y
	for index, child := range b.children {
		size := Size{}
		if index < len(b.sizes) {
			size = b.sizes[index]
		}
		childBounds := render.Rect{}
		if b.horizontal {
			childBounds = render.Rect{X: x, Y: y, Width: size.Width, Height: content.Height}
			x += size.Width
		} else {
			childBounds = render.Rect{X: x, Y: y, Width: content.Width, Height: size.Height}
			y += size.Height
		}
		child.arrange(intersectRects(childBounds, content))
	}
}

func (b *menuBarWidget) draw(ctx *drawContext) {
	drawComponentBox(ctx, b.bounds, menuBarComponentStyle(b, b.resolved))
	for _, child := range b.children {
		drawResolvedComponent(ctx, child)
	}
}

func (b *menuBarWidget) handleEvent(event driver.Event) (handled, redraw bool) {
	for _, child := range b.children {
		childHandled, childRedraw := child.handleEvent(event)
		handled = handled || childHandled
		redraw = redraw || childRedraw
	}
	return handled, redraw
}

func (b *menuBarWidget) walk(visit func(component)) {
	visit(b)
	for _, child := range b.children {
		child.walk(visit)
	}
}

func (b *menuBarWidget) childComponents() []component { return b.children }
func (b *menuBarWidget) arrangedBounds() render.Rect  { return b.bounds }
func (b *menuBarWidget) isUIComponent()               {}

type menuItemWidget struct {
	textBlock
	shortcutBlock textBlock
	checkBlock    textBlock
	arrowBlock    textBlock
	model         *MenuItemModel
	menu          *MenuModel
	window        *Window
	layer         *overlay
	child         *overlay
	bounds        render.Rect
	resolved      internalstyle.Resolved
	hover         bool
	pressed       bool
	focused       bool
	open          bool
}

func (i *menuItemWidget) Text() string {
	if i.menu != nil {
		return i.menu.Title
	}
	if i.model == nil {
		return ""
	}
	return i.model.Text
}

func (i *menuItemWidget) refreshText() {
	i.setText(i.Text())
	if i.menu == nil && i.model != nil {
		i.shortcutBlock.setText(menuShortcutText(i.model))
		if i.model.checked {
			i.checkBlock.setText("✓")
		} else {
			i.checkBlock.setText("")
		}
		if i.model.submenu != nil {
			i.arrowBlock.setText("›")
		} else {
			i.arrowBlock.setText("")
		}
	}
}

func (i *menuItemWidget) setFace(face *text.Face) {
	i.textBlock.setFace(face)
	i.shortcutBlock.setFace(face)
	i.checkBlock.setFace(face)
	i.arrowBlock.setFace(face)
}

func (i *menuItemWidget) clearDisabledInteraction() {
	if i.model == nil || i.model.enabled {
		return
	}
	i.hover = false
	i.pressed = false
	i.focused = false
}

func (i *menuItemWidget) styleMeta() internalstyle.Meta {
	i.clearDisabledInteraction()
	disabled, checked, separator := false, false, false
	if i.model != nil {
		disabled = !i.model.enabled
		checked = i.model.checked
		separator = i.model.separator
	}
	return internalstyle.Meta{
		Type: "MenuItem", Hover: i.hover, Focus: i.focused,
		Disabled: disabled || separator, Checked: checked,
		Pressed: i.pressed || i.open,
	}
}

func (i *menuItemWidget) setResolved(resolved internalstyle.Resolved) {
	i.resolved = resolved
}

func (i *menuItemWidget) measure(constraints Constraints) Size {
	i.clearDisabledInteraction()
	i.refreshText()
	style := menuItemComponentStyle(i, i.resolved)
	return clampSize(Size{Width: style.width, Height: style.height}, constraints)
}

func (i *menuItemWidget) arrange(bounds render.Rect) {
	i.bounds = bounds
}

func (i *menuItemWidget) draw(ctx *drawContext) {
	i.clearDisabledInteraction()
	i.refreshText()
	style := menuItemComponentStyle(i, i.resolved)
	drawComponentBox(ctx, i.bounds, style)
	content := styleContentRect(i.bounds, style)
	if i.model != nil && i.model.separator {
		thickness := max(style.borderWidth, 1)
		ctx.DrawRect(render.Rect{
			X: content.X, Y: content.Y + (content.Height-thickness)/2,
			Width: content.Width, Height: thickness,
		}, style.borderColor)
		return
	}
	bitmap := i.rasterize()
	if bitmap.Width == 0 && bitmap.Height == 0 {
		return
	}
	if i.menu != nil {
		destination := centeredBitmapRect(content, *bitmap)
		ctx.DrawText(i.text, driver.Point{X: destination.X, Y: destination.Y}, style.text, i.face)
		return
	}

	mainContent := content
	mainContent.X += menuCheckWidth
	mainContent.Width = max(mainContent.Width-menuCheckWidth, 0)
	if i.shortcutBlock.text != "" {
		shortcut := i.shortcutBlock.rasterize()
		mainContent.Width = max(mainContent.Width-float64(shortcut.Width)-12, 0)
		destination := centeredBitmapRect(content, *shortcut)
		destination.X = content.X + content.Width - float64(shortcut.Width)
		if i.arrowBlock.text != "" {
			destination.X -= menuArrowWidth
		}
		ctx.DrawText(i.shortcutBlock.text, driver.Point{X: destination.X, Y: destination.Y}, style.text, i.shortcutBlock.face)
	}
	destination := centeredBitmapRect(mainContent, *bitmap)
	destination.X = mainContent.X
	ctx.DrawText(i.text, driver.Point{X: destination.X, Y: destination.Y}, style.text, i.face)
	if i.checkBlock.text != "" {
		check := i.checkBlock.rasterize()
		checkDestination := centeredBitmapRect(render.Rect{X: content.X, Y: content.Y, Width: menuCheckWidth, Height: content.Height}, *check)
		ctx.DrawText(i.checkBlock.text, driver.Point{X: checkDestination.X, Y: checkDestination.Y}, style.text, i.checkBlock.face)
	}
	if i.arrowBlock.text != "" {
		arrow := i.arrowBlock.rasterize()
		arrowDestination := centeredBitmapRect(render.Rect{
			X: content.X + content.Width - menuArrowWidth, Y: content.Y,
			Width: menuArrowWidth, Height: content.Height,
		}, *arrow)
		ctx.DrawText(i.arrowBlock.text, driver.Point{X: arrowDestination.X, Y: arrowDestination.Y}, style.text, i.arrowBlock.face)
	}
}

func menuShortcutText(item *MenuItemModel) string {
	if item == nil || !item.hasShortcut {
		return ""
	}
	parts := make([]string, 0, 5)
	if item.shortcutMods&ModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if item.shortcutMods&ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if item.shortcutMods&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	if item.shortcutMods&ModSuper != 0 {
		parts = append(parts, "Super")
	}
	parts = append(parts, strings.ToUpper(string(item.shortcutKey)))
	return strings.Join(parts, "+")
}

func menuOverlayOwner(parent, child *overlay) *menuItemWidget {
	popup, ok := parent.root.(*menuBarWidget)
	if !ok || !popup.popup {
		return nil
	}
	for _, candidate := range popup.children {
		item := candidate.(*menuItemWidget)
		if item.child == child {
			return item
		}
	}
	return nil
}

func (i *menuItemWidget) enabledAncestry() bool {
	for layer := i.layer; layer != nil && layer.parent != nil; layer = layer.parent {
		owner := menuOverlayOwner(layer.parent, layer)
		if owner == nil || owner.model == nil || !owner.model.enabled {
			return false
		}
	}
	return true
}

func (i *menuItemWidget) actionable() bool {
	i.clearDisabledInteraction()
	return i.model != nil && i.model.enabled && !i.model.separator && i.enabledAncestry()
}

func (w *Window) reconcileMenuOverlays() bool {
	changed := false
	for {
		var disabledParent *overlay
		for _, layer := range w.overlays {
			if layer.parent == nil {
				continue
			}
			owner := menuOverlayOwner(layer.parent, layer)
			if owner != nil && !owner.actionable() {
				disabledParent = layer.parent
				break
			}
		}
		if disabledParent == nil || !w.removeOverlayDescendants(disabledParent) {
			break
		}
		changed = true
	}
	if changed && w.router != nil {
		w.router.validateFocus()
	}
	return changed
}

func isMenuOverlay(layer *overlay) bool {
	popup, ok := layer.root.(*menuBarWidget)
	return ok && popup.popup
}

func (w *Window) dismissMenuOverlays() bool {
	removedSet := make(map[*overlay]bool)
	var removed []*overlay
	survivors := make([]*overlay, 0, len(w.overlays))
	for _, layer := range w.overlays {
		if isMenuOverlay(layer) {
			removedSet[layer] = true
			removed = append(removed, layer)
		} else {
			survivors = append(survivors, layer)
		}
	}
	if len(removed) == 0 {
		return false
	}
	w.overlays = survivors
	for _, layer := range survivors {
		for layer.parent != nil && removedSet[layer.parent] {
			layer.parent = layer.parent.parent
		}
	}
	w.finishOverlayRemoval(removed)
	return true
}

func (i *menuItemWidget) handleEvent(event driver.Event) (handled, redraw bool) {
	if !i.actionable() {
		return false, false
	}
	switch event.Kind {
	case driver.EventMouseMove:
		inside := boundsContain(i.bounds, event.Position)
		changed := i.hover != inside
		i.hover = inside
		if inside && i.model.submenu != nil {
			i.openSubmenu()
			changed = true
		}
		return true, changed
	case driver.EventMouseButton:
		if event.Button != driver.MouseButtonLeft {
			return false, false
		}
		switch event.Action {
		case driver.ActionPress:
			if !boundsContain(i.bounds, event.Position) {
				return false, false
			}
			changed := !i.pressed
			i.pressed = true
			if i.menu != nil {
				i.activate()
				changed = true
			}
			return true, changed
		case driver.ActionRelease:
			if !i.pressed {
				return false, false
			}
			i.pressed = false
			if boundsContain(i.bounds, event.Position) {
				i.activate()
			}
			return true, true
		}
	case driver.EventCursorLeave:
		changed := i.hover
		i.hover = false
		return changed, changed
	}
	return false, false
}

func (i *menuItemWidget) activate() {
	if !i.actionable() {
		return
	}
	i.pressed = false
	if i.menu != nil {
		i.toggleMenu()
		return
	}
	if i.model.submenu != nil {
		i.openSubmenu()
		return
	}
	i.window.clearOverlays()
	i.model.trigger()
}

func (i *menuItemWidget) toggleMenu() {
	if i.child != nil {
		i.window.clearOverlays()
		return
	}
	if len(i.menu.Items) == 0 {
		return
	}
	i.window.clearOverlays()
	popup := newMenuPopupWidget(i.window, i.menu.Items)
	barBounds := i.window.menuBar.bounds
	preferred := driver.Point{X: i.bounds.X, Y: barBounds.Y + barBounds.Height}
	bounds, ok := i.window.menuPopupBounds(popup, preferred)
	if !ok {
		return
	}
	i.open = true
	layer := i.window.pushOverlay(popup, bounds, func() {
		i.child = nil
		i.open = false
	}, nil)
	if layer == nil {
		i.open = false
		return
	}
	i.child = layer
	installMenuLayer(popup, layer)
}

func (i *menuItemWidget) openSubmenu() {
	if i.child != nil || i.layer == nil || i.model.submenu == nil || len(i.model.submenu.Items) == 0 {
		return
	}
	i.window.removeOverlayDescendants(i.layer)
	popup := newMenuPopupWidget(i.window, i.model.submenu.Items)
	preferred := driver.Point{X: i.layer.bounds.X + i.layer.bounds.Width, Y: i.bounds.Y}
	bounds, ok := i.window.menuPopupBounds(popup, preferred)
	if !ok {
		return
	}
	layer := i.window.pushOverlay(popup, bounds, func() { i.child = nil }, i.layer)
	if layer == nil {
		return
	}
	i.child = layer
	installMenuLayer(popup, layer)
}

func installMenuLayer(popup *menuBarWidget, layer *overlay) {
	for _, child := range popup.children {
		child.(*menuItemWidget).layer = layer
	}
}

func (w *Window) menuPopupBounds(popup *menuBarWidget, preferred driver.Point) (render.Rect, bool) {
	viewport := Size{}
	if w.target != nil {
		size := w.target.Size()
		viewport = Size{Width: float64(size.Width), Height: float64(size.Height)}
	} else if reporter, ok := w.root.(boundsReporter); ok {
		bounds := reporter.arrangedBounds()
		viewport = Size{Width: bounds.Width, Height: bounds.Height}
	}
	if viewport.Width <= 0 || viewport.Height <= 0 {
		return render.Rect{}, false
	}
	if w.app != nil && w.app.face != nil {
		if err := w.app.applyStyleTree(popup, w.app.sheet); err != nil {
			w.app.noteErr(err)
			return render.Rect{}, false
		}
	}
	measured := popup.measure(Constraints{Max: viewport})
	width := min(measured.Width, viewport.Width)
	height := min(measured.Height, viewport.Height)
	x := min(max(preferred.X, 0), viewport.Width-width)
	y := min(max(preferred.Y, 0), viewport.Height-height)
	return render.Rect{X: x, Y: y, Width: width, Height: height}, true
}

func (i *menuItemWidget) walk(visit func(component))  { visit(i) }
func (i *menuItemWidget) arrangedBounds() render.Rect { return i.bounds }
func (i *menuItemWidget) acceptsPointer() bool        { return true }
func (i *menuItemWidget) focusable() bool             { return i.actionable() }
func (i *menuItemWidget) isFocused() bool             { return i.focused }
func (i *menuItemWidget) setFocused(focused bool)     { i.focused = focused }
func (i *menuItemWidget) isUIComponent()              {}
func (i *menuItemWidget) clearHover() bool {
	hovered := i.hover
	i.hover = false
	return hovered
}

func (w *Window) routeMenuTitlePress(event driver.Event) (handled, redraw, routed bool) {
	if len(w.overlays) == 0 || w.menuBar == nil || w.router == nil ||
		event.Kind != driver.EventMouseButton || event.Button != driver.MouseButtonLeft || event.Action != driver.ActionPress {
		return false, false, false
	}
	title, _ := hitTestComponent(w.menuBar, event.Position).(*menuItemWidget)
	if title == nil || title.menu == nil {
		return false, false, false
	}
	if title.child != nil {
		w.clearOverlays()
		return true, true, true
	}
	w.clearOverlays()
	handled, _ = w.router.Handle(event)
	return handled, true, true
}

func composeMenuRoot(window *Window, content component, menus []*MenuModel) (component, *menuBarWidget) {
	hasMenus := false
	for _, model := range menus {
		hasMenus = hasMenus || model != nil
	}
	if !hasMenus {
		return content, nil
	}
	bar := newMenuBarWidget(window, menus)
	root := Column(bar, content)
	root.fillLast = true
	return root, bar
}

// MenuBar installs a self-drawn menu bar above the window content. The content
// receives the remaining logical window size. Replacing or removing the bar
// tears down open menus and resets focus, press and composition state. MenuBar
// and MenuItem CSS styles these window components only, not native system or
// tray menus.
func (w *Window) MenuBar(menus ...*MenuModel) *Window {
	if w == nil {
		return nil
	}
	menus = append([]*MenuModel(nil), menus...)
	w.app.noteErr(w.app.postResult(func() error {
		content := w.content
		if content == nil {
			content = w.root
		}
		return w.replaceRoot(content, menus)
	}))
	return w
}
