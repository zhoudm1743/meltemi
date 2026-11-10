package ui

import (
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

func newMenuTestFixture(t *testing.T, content Component, width, height int, menus ...*MenuModel) *overlayTestFixture {
	t.Helper()
	backend := newUITestBackend()
	app, factory := newUITestApp(backend, &uiTestLog{})
	app.SetLastWindowAction(LastWindowKeepRunning)
	done := runUIAsync(app)
	window := app.Window("menus").Size(width, height).Content(content).MenuBar(menus...)
	target, renderer := showUI(t, window, backend, factory)
	fixture := &overlayTestFixture{
		app: app, backend: backend, factory: factory,
		window: window, target: target, renderer: renderer,
		state: compositionTestStateFor(target), done: done,
	}
	t.Cleanup(func() {
		fixture.finish(t)
		compositionTestStates.Delete(target)
	})
	return fixture
}

func installedMenuBar(t *testing.T, window *Window) (childContainer, component) {
	t.Helper()
	root, ok := window.root.(childContainer)
	if !ok {
		t.Fatalf("window root %T is not a menu container", window.root)
	}
	children := root.childComponents()
	if len(children) != 2 {
		t.Fatalf("menu root has %d children, want bar and content", len(children))
	}
	bar, ok := children[0].(childContainer)
	if !ok {
		t.Fatalf("menu bar %T is not a container", children[0])
	}
	return bar, children[1]
}

func menuTitleAt(t *testing.T, window *Window, index int) component {
	t.Helper()
	bar, _ := installedMenuBar(t, window)
	children := bar.childComponents()
	if index < 0 || index >= len(children) {
		t.Fatalf("menu title index %d outside %d titles", index, len(children))
	}
	return children[index]
}

func menuPopupItemAt(t *testing.T, layer *overlay, index int) component {
	t.Helper()
	popup, ok := layer.root.(childContainer)
	if !ok {
		t.Fatalf("popup root %T is not a container", layer.root)
	}
	children := popup.childComponents()
	if index < 0 || index >= len(children) {
		t.Fatalf("menu item index %d outside %d items", index, len(children))
	}
	return children[index]
}

func menuClickT(t *testing.T, fixture *overlayTestFixture, target component) {
	t.Helper()
	menuClickWindowT(t, fixture, fixture.target, target)
}

func menuClickWindowT(t *testing.T, fixture *overlayTestFixture, native *uiTestTarget, target component) {
	t.Helper()
	fixture.post(t, func() {
		point := center(target.(boundsReporter).arrangedBounds())
		native.handler(leftPress(point))
		native.handler(leftRelease(point))
	})
}

func menuHoverT(t *testing.T, fixture *overlayTestFixture, target component) {
	t.Helper()
	fixture.post(t, func() {
		fixture.target.handler(driver.Event{Kind: driver.EventMouseMove, Position: center(target.(boundsReporter).arrangedBounds())})
	})
}

func menuKeyT(t *testing.T, fixture *overlayTestFixture, key driver.Key, mods driver.Modifiers) {
	t.Helper()
	fixture.post(t, func() {
		fixture.target.handler(keyEvent(key, mods))
		fixture.target.handler(driver.Event{Kind: driver.EventKey, Key: key, Action: driver.ActionRelease, Mods: mods})
	})
}

func requireMenuPopupInside(t *testing.T, layer *overlay, width, height int) {
	t.Helper()
	bounds := layer.bounds
	if bounds.X < 0 || bounds.Y < 0 || bounds.Width <= 0 || bounds.Height <= 0 ||
		bounds.X+bounds.Width > float64(width) || bounds.Y+bounds.Height > float64(height) {
		t.Errorf("popup bounds %+v escape viewport %dx%d", bounds, width, height)
	}
}

func TestMenuBarLayoutAndPopup(t *testing.T) {
	t.Run("initial popup activation and small viewport clamp", func(t *testing.T) {
		content := fake("content", 40, 20)
		opens := 0
		open := MenuItem("Open").OnClick(func() { opens++ })
		fixture := newMenuTestFixture(t, content, 240, 140,
			Menu("File", open, MenuItem("Quit")), Menu("Other", MenuItem("About")))
		bar, _ := installedMenuBar(t, fixture.window)
		barBounds := bar.(boundsReporter).arrangedBounds()
		if barBounds.X != 0 || barBounds.Y != 0 || barBounds.Width != 240 || barBounds.Height <= 0 || barBounds.Height >= 140 {
			t.Fatalf("menu bar bounds = %+v, want a positive-height strip across the window", barBounds)
		}
		wantContent := rect(0, barBounds.Height, 240, 140-barBounds.Height)
		if got := content.bounds; got != wantContent {
			t.Errorf("content bounds = %+v, want remaining window %+v", got, wantContent)
		}

		title := menuTitleAt(t, fixture.window, 0)
		menuClickT(t, fixture, title)
		if len(fixture.window.overlays) != 1 {
			t.Fatalf("clicking the title left %d overlays, want one popup", len(fixture.window.overlays))
		}
		popup := fixture.window.overlays[0]
		requireMenuPopupInside(t, popup, 240, 140)
		if popup.bounds.X != title.(boundsReporter).arrangedBounds().X || popup.bounds.Y != barBounds.Y+barBounds.Height {
			t.Errorf("popup origin = (%v,%v), want title x and bar bottom (%v,%v)",
				popup.bounds.X, popup.bounds.Y, title.(boundsReporter).arrangedBounds().X, barBounds.Y+barBounds.Height)
		}
		menuClickT(t, fixture, menuTitleAt(t, fixture.window, 1))
		if len(fixture.window.overlays) != 1 {
			t.Fatalf("clicking another title did not switch directly to its popup: overlays=%d", len(fixture.window.overlays))
		}
		if got := menuPopupItemAt(t, fixture.window.overlays[0], 0).(interface{ Text() string }).Text(); got != "About" {
			t.Errorf("switched popup first item = %q, want About", got)
		}
		menuClickT(t, fixture, title)
		popup = fixture.window.overlays[0]
		menuClickT(t, fixture, menuPopupItemAt(t, popup, 0))
		if opens != 1 || !open.Checked() {
			t.Errorf("Open activation: callbacks=%d checked=%v, want 1/true", opens, open.Checked())
		}
		if len(fixture.window.overlays) != 0 {
			t.Errorf("leaf activation left %d overlays, want none", len(fixture.window.overlays))
		}

		replacements := 0
		fixture.post(t, func() {
			fixture.window.MenuBar(Menu("Edit", MenuItem("Replace").OnClick(func() { replacements++ })))
		})
		if got := menuTitleAt(t, fixture.window, 0).(interface{ Text() string }); got.Text() != "Edit" {
			t.Errorf("replacement title = %q, want Edit", got.Text())
		}
		menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
		menuClickT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[0], 0))
		if replacements != 1 {
			t.Errorf("replacement callback count = %d, want 1", replacements)
		}
	})

	t.Run("popup dimensions and origin are clamped to a cramped viewport", func(t *testing.T) {
		fixture := newMenuTestFixture(t, Label("body"), 120, 92, Menu(
			"A very long top-level menu title",
			MenuItem("An unusually long first popup item"),
			MenuItem("A second unusually long popup item"),
			MenuItem("A third unusually long popup item"),
		))
		menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
		if len(fixture.window.overlays) != 1 {
			t.Fatalf("popup count = %d, want 1", len(fixture.window.overlays))
		}
		popup := fixture.window.overlays[0]
		requireMenuPopupInside(t, popup, 120, 92)
		if popup.bounds.X+popup.bounds.Width != 120 || popup.bounds.Y+popup.bounds.Height != 92 {
			t.Errorf("oversized popup bounds = %+v, want right and bottom clamped to 120x92", popup.bounds)
		}
	})

	t.Run("CSS properties and interactive state metadata reach drawing", func(t *testing.T) {
		disabled := MenuItem("Disabled").SetEnabled(false)
		checked := MenuItem("Checked").SetChecked(true)
		fixture := newMenuTestFixture(t, Label("body"), 260, 180,
			Menu("File", disabled, checked))
		if err := fixture.app.LoadStyles(writeStyleSheet(t,
			"MenuBar { height: 40px; padding: 2px; background: #f0f0f0; opacity: 0.75; } "+
				"MenuItem { width: 120px; height: 24px; padding: 2px 4px; border: 1px solid #404040; border-radius: 3px; } "+
				"MenuItem:disabled { color: #010203; } "+
				"MenuItem:checked { color: #040506; } "+
				"MenuItem:pressed { background: #070809; }")); err != nil {
			t.Fatalf("LoadStyles: %v", err)
		}
		fixture.sync(t)
		bar, content := installedMenuBar(t, fixture.window)
		if got := bar.(boundsReporter).arrangedBounds().Height; got != 40 {
			t.Errorf("styled menu bar height = %v, want 40", got)
		}
		if got := content.(boundsReporter).arrangedBounds().Y; got != 40 {
			t.Errorf("styled content y = %v, want 40", got)
		}
		if got := resolvedComponentOpacity(bar.(component)); got != 0.75 {
			t.Errorf("menu bar opacity = %v, want 0.75", got)
		}

		title := menuTitleAt(t, fixture.window, 0)
		menuClickT(t, fixture, title)
		if len(fixture.window.overlays) != 1 {
			t.Fatalf("styled title click left %d overlays, want 1; title=%+v app error=%v",
				len(fixture.window.overlays), title.(boundsReporter).arrangedBounds(), fixture.app.err())
		}
		popup := fixture.window.overlays[0]
		disabledView := menuPopupItemAt(t, popup, 0)
		checkedView := menuPopupItemAt(t, popup, 1)
		disabledMeta := disabledView.(resolvedStyleReceiver).styleMeta()
		checkedMeta := checkedView.(resolvedStyleReceiver).styleMeta()
		if disabledMeta.Type != "MenuItem" || !disabledMeta.Disabled || disabledMeta.Checked {
			t.Errorf("disabled metadata = %+v", disabledMeta)
		}
		if checkedMeta.Type != "MenuItem" || checkedMeta.Disabled || !checkedMeta.Checked {
			t.Errorf("checked metadata = %+v", checkedMeta)
		}
		disabledPainter := &fakePainter{}
		drawResolvedComponent(&drawContext{face: fixture.app.face, painter: disabledPainter}, disabledView)
		wantDisabled, _ := styleColor(map[string]string{"color": "#010203"}, "color")
		foundDisabledColor := false
		for _, op := range disabledPainter.ops {
			if op.kind == "text" && op.text == "Disabled" && op.color == wantDisabled {
				foundDisabledColor = true
			}
		}
		if !foundDisabledColor {
			t.Errorf("disabled paint ops = %+v, want disabled text color", disabledPainter.ops)
		}

		menuHoverT(t, fixture, checkedView)
		point := center(checkedView.(boundsReporter).arrangedBounds())
		fixture.post(t, func() { fixture.target.handler(leftPress(point)) })
		pressedMeta := checkedView.(resolvedStyleReceiver).styleMeta()
		if !pressedMeta.Hover || !pressedMeta.Pressed || !pressedMeta.Checked {
			t.Errorf("pressed metadata = %+v, want hover, pressed and checked", pressedMeta)
		}
		painter := &fakePainter{}
		drawResolvedComponent(&drawContext{face: fixture.app.face, painter: painter}, checkedView)
		foundCheck, foundPressedColor := false, false
		wantPressed, _ := styleColor(map[string]string{"color": "#070809"}, "color")
		for _, op := range painter.ops {
			if op.kind == "text" && op.text == "✓" {
				foundCheck = true
			}
			if (op.kind == "roundedRect" || op.kind == "rect") && op.color == wantPressed {
				foundPressedColor = true
			}
		}
		if !foundCheck || !foundPressedColor {
			t.Errorf("checked pressed paint ops = %+v, want check mark and pressed background", painter.ops)
		}
		fixture.post(t, func() { fixture.target.handler(leftRelease(point)) })
		if len(fixture.window.overlays) != 0 || checked.Checked() {
			t.Errorf("checked click left overlays=%d checked=%v, want 0/false", len(fixture.window.overlays), checked.Checked())
		}
	})
}

func TestMenuBarSubmenuAndOutsideDismiss(t *testing.T) {
	rootClicks := 0
	root := Button("root").OnClick(func() { rootClicks++ })
	leaves := 0
	leaf := MenuItem("Leaf").OnClick(func() { leaves++ })
	menu := Menu("File", MenuItem("Recent").Submenu(
		MenuItem("More").Submenu(leaf),
		MenuItem("Document"),
	))
	fixture := newMenuTestFixture(t, root, 260, 180, menu)

	openChain := func() {
		t.Helper()
		menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
		menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[0], 0))
		menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[1], 0))
		if len(fixture.window.overlays) != 3 {
			t.Fatalf("submenu chain length = %d, want 3", len(fixture.window.overlays))
		}
		for index, layer := range fixture.window.overlays {
			requireMenuPopupInside(t, layer, 260, 180)
			if index > 0 && layer.parent != fixture.window.overlays[index-1] {
				t.Errorf("layer %d has wrong parent", index)
			}
		}
	}

	openChain()
	menuClickT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[2], 0))
	if leaves != 1 || !leaf.Checked() || len(fixture.window.overlays) != 0 {
		t.Errorf("nested leaf: callbacks=%d checked=%v overlays=%d, want 1/true/0",
			leaves, leaf.Checked(), len(fixture.window.overlays))
	}

	openChain()
	outside := driver.Point{X: 250, Y: 170}
	fixture.post(t, func() {
		fixture.target.handler(leftPress(outside))
		fixture.target.handler(leftRelease(outside))
	})
	if len(fixture.window.overlays) != 0 || rootClicks != 0 {
		t.Errorf("outside dismissal: overlays=%d root clicks=%d, want 0/0", len(fixture.window.overlays), rootClicks)
	}
	menuClickT(t, fixture, root)
	if rootClicks != 1 {
		t.Errorf("ordinary click after outside dismissal = %d, want 1", rootClicks)
	}

	openChain()
	menuKeyT(t, fixture, driver.KeyEscape, 0)
	if len(fixture.window.overlays) != 2 {
		t.Errorf("first Escape left %d overlays, want 2", len(fixture.window.overlays))
	}
	menuKeyT(t, fixture, driver.KeyEscape, 0)
	if len(fixture.window.overlays) != 1 {
		t.Errorf("second Escape left %d overlays, want 1", len(fixture.window.overlays))
	}
	menuKeyT(t, fixture, driver.KeyEscape, 0)
	if len(fixture.window.overlays) != 0 {
		t.Errorf("third Escape left %d overlays, want none", len(fixture.window.overlays))
	}
}

func TestMenuBarKeyboardAndShortcut(t *testing.T) {
	input := Input(nil)
	copies, saves, checks, disabledFires := 0, 0, 0, 0
	copyItem := MenuItem("Copy").Shortcut(ModCtrl, 'C').OnClick(func() { copies++ })
	saveItem := MenuItem("Save").Shortcut(ModCtrl, 'S').OnClick(func() { saves++ })
	disabled := MenuItem("Disabled").SetEnabled(false).Shortcut(ModCtrl, 'D').OnClick(func() { disabledFires++ })
	checked := MenuItem("Wrap").SetChecked(true).Shortcut(ModCtrl, 'W').OnClick(func() { checks++ })
	fixture := newMenuTestFixture(t, input, 320, 200,
		Menu("File", disabled, MenuSeparator(), checked, copyItem, saveItem))

	// Tab focuses the first top-level title and Enter opens it without a mouse.
	menuKeyT(t, fixture, driver.KeyTab, 0)
	menuKeyT(t, fixture, driver.KeyEnter, 0)
	if len(fixture.window.overlays) != 1 {
		t.Fatalf("keyboard title activation left %d overlays, want 1", len(fixture.window.overlays))
	}
	menuKeyT(t, fixture, driver.KeyTab, 0)
	popup := fixture.window.overlays[0]
	if focus := fixture.window.router.focus; focus != menuPopupItemAt(t, popup, 2) {
		t.Errorf("first popup focus = %T %p, want enabled checked item", focus, focus)
	}
	menuKeyT(t, fixture, driver.KeyEnter, 0)
	if checks != 1 || checked.Checked() || len(fixture.window.overlays) != 0 {
		t.Errorf("keyboard item activation: checks=%d checked=%v overlays=%d, want 1/false/0",
			checks, checked.Checked(), len(fixture.window.overlays))
	}

	menuClickT(t, fixture, input)
	if fixture.window.router.focus != input {
		t.Fatal("clicking the content Input did not focus it")
	}
	menuKeyT(t, fixture, driver.KeyC, driver.ModCtrl)
	if copies != 0 {
		t.Errorf("Input-consumed Ctrl+C fired the menu %d times", copies)
	}
	menuKeyT(t, fixture, driver.KeyS, driver.ModCtrl)
	if saves != 1 {
		t.Errorf("Ctrl+S after the focused Input fired %d saves, want 1", saves)
	}
	menuKeyT(t, fixture, driver.KeyD, driver.ModCtrl)
	if disabledFires != 0 {
		t.Errorf("disabled shortcut fired %d times", disabledFires)
	}
	menuKeyT(t, fixture, driver.KeyW, driver.ModCtrl)
	if checks != 2 || !checked.Checked() {
		t.Errorf("shortcut checked activation: checks=%d checked=%v, want 2/true", checks, checked.Checked())
	}
}

func TestWindowMenuLifecycleIsolation(t *testing.T) {
	t.Run("MenuBar Content and removal replacement clean interaction and shortcuts", func(t *testing.T) {
		oldInput := Input(nil)
		oldFires, newFires := 0, 0
		oldItem := MenuItem("Old").Shortcut(ModCtrl, 'O').OnClick(func() { oldFires++ })
		newItem := MenuItem("New").Shortcut(ModCtrl, 'N').OnClick(func() { newFires++ })
		fixture := newMenuTestFixture(t, oldInput, 300, 180, Menu("Old menu", oldItem))

		menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
		oldPopup := fixture.window.overlays[0]
		oldView := menuPopupItemAt(t, oldPopup, 0)
		point := center(oldView.(boundsReporter).arrangedBounds())
		fixture.post(t, func() {
			fixture.target.handler(leftPress(point))
			fixture.window.MenuBar(Menu("New menu", newItem))
		})
		if len(fixture.window.overlays) != 0 || oldFires != 0 {
			t.Errorf("MenuBar replacement left overlays=%d old fires=%d, want 0/0", len(fixture.window.overlays), oldFires)
		}
		if fixture.window.router.focus != nil || fixture.window.router.press != nil || fixture.window.router.composing {
			t.Error("MenuBar replacement retained focus, press or composition")
		}
		menuKeyT(t, fixture, driver.KeyO, driver.ModCtrl)
		menuKeyT(t, fixture, driver.KeyN, driver.ModCtrl)
		if oldFires != 0 || newFires != 1 {
			t.Errorf("shortcut roots after replacement: old=%d new=%d, want 0/1", oldFires, newFires)
		}

		newContent := Input(nil)
		menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
		fixture.post(t, func() { fixture.window.Content(newContent) })
		if len(fixture.window.overlays) != 0 {
			t.Errorf("Content replacement left %d overlays", len(fixture.window.overlays))
		}
		_, content := installedMenuBar(t, fixture.window)
		if content != newContent || newContent.clipboard != fixture.target || newContent.compositionFocusHandler == nil {
			t.Error("Content replacement did not preserve the bar and attach the new content")
		}
		menuKeyT(t, fixture, driver.KeyN, driver.ModCtrl)
		if newFires != 2 {
			t.Errorf("preserved menu shortcut count = %d, want 2", newFires)
		}

		menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
		fixture.post(t, func() { fixture.window.MenuBar() })
		if len(fixture.window.overlays) != 0 {
			t.Errorf("MenuBar removal left %d overlays", len(fixture.window.overlays))
		}
		if fixture.window.root != newContent {
			t.Errorf("root after MenuBar removal = %p, want content %p", fixture.window.root, newContent)
		}
		barHeight := newContent.bounds.Y
		if barHeight != 0 || newContent.bounds.Height != 180 {
			t.Errorf("content after bar removal = %+v, want full 300x180 window", newContent.bounds)
		}
		menuKeyT(t, fixture, driver.KeyN, driver.ModCtrl)
		if newFires != 2 {
			t.Errorf("removed menu shortcut fired: count=%d, want 2", newFires)
		}
	})

	t.Run("hidden and destroyed windows are inert and other windows stay independent", func(t *testing.T) {
		firstFires, secondFires := 0, 0
		first := newMenuTestFixture(t, Label("first"), 240, 140,
			Menu("First", MenuItem("Action").Shortcut(ModCtrl, 'A').OnClick(func() { firstFires++ })))
		secondWindow := first.app.Window("second").Size(260, 160).Content(Label("second")).MenuBar(
			Menu("Second", MenuItem("Action").Shortcut(ModCtrl, 'B').OnClick(func() { secondFires++ })))
		secondTarget, secondRenderer := showUI(t, secondWindow, first.backend, first.factory)
		firstWindow := first.window
		menuClickT(t, first, menuTitleAt(t, firstWindow, 0))
		menuClickWindowT(t, first, secondTarget, menuTitleAt(t, secondWindow, 0))
		if len(firstWindow.overlays) != 1 || len(secondWindow.overlays) != 1 {
			t.Fatalf("initial independent overlays = %d/%d, want 1/1", len(firstWindow.overlays), len(secondWindow.overlays))
		}

		first.post(t, func() { firstWindow.Hide() })
		if len(firstWindow.overlays) != 0 || len(secondWindow.overlays) != 1 {
			t.Errorf("Hide isolation: first overlays=%d second=%d, want 0/1", len(firstWindow.overlays), len(secondWindow.overlays))
		}
		first.post(t, func() { first.target.handler(keyEvent(driver.KeyA, driver.ModCtrl)) })
		if firstFires != 0 {
			t.Errorf("hidden first window fired %d callbacks", firstFires)
		}
		first.post(t, func() { secondTarget.handler(keyEvent(driver.KeyB, driver.ModCtrl)) })
		if secondFires != 1 || len(firstWindow.overlays) != 0 {
			t.Errorf("second window after first Hide: fires=%d first overlays=%d, want 1/0", secondFires, len(firstWindow.overlays))
		}

		first.post(t, func() { firstWindow.Show() })
		first.post(t, func() { first.target.handler(keyEvent(driver.KeyA, driver.ModCtrl)) })
		if firstFires != 1 {
			t.Errorf("reshown first window fired %d callbacks, want 1", firstFires)
		}
		first.post(t, func() { _ = secondWindow.Close() })
		if secondWindow.router != nil || len(secondWindow.overlays) != 0 {
			t.Error("destroyed second window retained router or overlays")
		}
		first.post(t, func() { secondTarget.handler(keyEvent(driver.KeyB, driver.ModCtrl)) })
		if secondFires != 1 {
			t.Errorf("destroyed second window fired %d callbacks, want 1", secondFires)
		}
		if !secondRenderer.closed.Load() {
			t.Error("second renderer was not closed")
		}
	})
}

func TestMenuBarHoverBacktrackingAndSiblingSubmenus(t *testing.T) {
	fixture := newMenuTestFixture(t, Label("body"), 380, 220, Menu("File",
		MenuItem("Recent").Submenu(
			MenuItem("More").Submenu(MenuItem("Deep leaf")),
			MenuItem("Other").Submenu(MenuItem("Other leaf")),
		),
		MenuItem("Alternate").Submenu(MenuItem("Alternate leaf")),
		MenuItem("Plain"),
	))
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[0], 0))
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[1], 0))
	if len(fixture.window.overlays) != 3 {
		t.Fatalf("initial chain length = %d, want 3", len(fixture.window.overlays))
	}

	// Backtrack one level from More to its sibling Other in Recent.
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[1], 1))
	if len(fixture.window.overlays) != 3 {
		t.Fatalf("sibling hover left %d overlays, want a replacement third layer", len(fixture.window.overlays))
	}
	if got := menuPopupItemAt(t, fixture.window.overlays[2], 0).(interface{ Text() string }).Text(); got != "Other leaf" {
		t.Errorf("sibling submenu first item = %q, want Other leaf", got)
	}

	// Backtrack two levels to File and open a different first-level submenu.
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[0], 1))
	if len(fixture.window.overlays) != 2 {
		t.Fatalf("multi-level backtrack left %d overlays, want File and Alternate", len(fixture.window.overlays))
	}
	if got := menuPopupItemAt(t, fixture.window.overlays[1], 0).(interface{ Text() string }).Text(); got != "Alternate leaf" {
		t.Errorf("alternate submenu first item = %q, want Alternate leaf", got)
	}
	if fixture.window.overlays[1].parent != fixture.window.overlays[0] {
		t.Error("alternate submenu was not parented to the File layer")
	}

	// Moving onto an ordinary File item closes all descendants without opening
	// a replacement, and the File layer becomes active again.
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[0], 2))
	if len(fixture.window.overlays) != 1 || fixture.window.router.root != fixture.window.overlays[0].root {
		t.Errorf("ordinary parent-item hover left overlays=%d router root=%p, want one active File layer",
			len(fixture.window.overlays), fixture.window.router.root)
	}
}

func TestMenuBarSameTitleCloseAndReopen(t *testing.T) {
	fixture := newMenuTestFixture(t, Label("body"), 240, 140,
		Menu("File", MenuItem("Open")), Menu("Other", MenuItem("About")))
	file := menuTitleAt(t, fixture.window, 0)
	other := menuTitleAt(t, fixture.window, 1)

	menuClickT(t, fixture, file)
	if len(fixture.window.overlays) != 1 {
		t.Fatalf("first File click left %d overlays, want 1", len(fixture.window.overlays))
	}
	menuClickT(t, fixture, file)
	if len(fixture.window.overlays) != 0 {
		t.Errorf("second click on the open File title left %d overlays, want none", len(fixture.window.overlays))
	}
	menuClickT(t, fixture, file)
	if len(fixture.window.overlays) != 1 {
		t.Fatalf("third File click did not reopen: overlays=%d", len(fixture.window.overlays))
	}
	menuClickT(t, fixture, other)
	if len(fixture.window.overlays) != 1 {
		t.Fatalf("different-title switch left %d overlays, want Other open", len(fixture.window.overlays))
	}
	menuClickT(t, fixture, other)
	if len(fixture.window.overlays) != 0 {
		t.Errorf("second click on the open Other title left %d overlays, want none", len(fixture.window.overlays))
	}
}

func TestMenuBarRuntimeDisableClearsPointerActivation(t *testing.T) {
	fires := 0
	item := MenuItem("Action").OnClick(func() { fires++ })
	fixture := newMenuTestFixture(t, Label("body"), 260, 160, Menu("File", item))
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	view := menuPopupItemAt(t, fixture.window.overlays[0], 0).(*menuItemWidget)
	menuHoverT(t, fixture, view)
	point := center(view.bounds)
	fixture.post(t, func() { fixture.target.handler(leftPress(point)) })
	if !view.pressed || !view.focused || !view.hover {
		t.Fatalf("test setup did not arm all transient states: pressed=%v focused=%v hover=%v", view.pressed, view.focused, view.hover)
	}

	fixture.post(t, func() { item.SetEnabled(false) })
	if view.pressed || view.focused || view.hover {
		t.Errorf("disable retained transient state: pressed=%v focused=%v hover=%v", view.pressed, view.focused, view.hover)
	}
	fixture.post(t, func() { item.SetEnabled(true) })
	fixture.post(t, func() { fixture.target.handler(leftRelease(point)) })
	if fires != 0 || len(fixture.window.overlays) != 1 {
		t.Errorf("stale press after re-enable fired=%d overlays=%d, want 0/1", fires, len(fixture.window.overlays))
	}

	menuClickT(t, fixture, view)
	if fires != 1 || len(fixture.window.overlays) != 0 {
		t.Errorf("fresh press after re-enable fired=%d overlays=%d, want 1/0", fires, len(fixture.window.overlays))
	}
}

func TestMenuBarRuntimeDisableClearsKeyboardActivation(t *testing.T) {
	fires := 0
	item := MenuItem("Action").OnClick(func() { fires++ })
	fixture := newMenuTestFixture(t, Label("body"), 260, 160, Menu("File", item))
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	menuKeyT(t, fixture, driver.KeyTab, 0)
	view := menuPopupItemAt(t, fixture.window.overlays[0], 0).(*menuItemWidget)
	if fixture.window.router.focus != view || !view.focused {
		t.Fatal("test setup did not focus the popup item")
	}

	fixture.post(t, func() { item.SetEnabled(false) })
	if view.focused || view.pressed || view.hover {
		t.Errorf("disable before key validation retained focus/press/hover: %v/%v/%v", view.focused, view.pressed, view.hover)
	}
	// Re-enable before the next key: the old focused flag must not resurrect.
	fixture.post(t, func() { item.SetEnabled(true) })
	menuKeyT(t, fixture, driver.KeyEnter, 0)
	if fires != 0 || len(fixture.window.overlays) != 1 {
		t.Errorf("stale focus after re-enable fired=%d overlays=%d, want 0/1", fires, len(fixture.window.overlays))
	}

	menuKeyT(t, fixture, driver.KeyTab, 0)
	if fixture.window.router.focus != view || !view.focused {
		t.Error("fresh Tab did not establish new focus after re-enable")
	}
	menuKeyT(t, fixture, driver.KeyEnter, 0)
	if fires != 1 || len(fixture.window.overlays) != 0 {
		t.Errorf("fresh Enter after re-enable fired=%d overlays=%d, want 1/0", fires, len(fixture.window.overlays))
	}
}

func requireMenuTextPaint(t *testing.T, fixture *overlayTestFixture, view component, text, colorHex string, wantCheck bool) {
	t.Helper()
	painter := &fakePainter{}
	drawResolvedComponent(&drawContext{face: fixture.app.face, painter: painter}, view)
	wantColor, _ := styleColor(map[string]string{"color": colorHex}, "color")
	foundText, foundCheck := false, false
	for _, op := range painter.ops {
		if op.kind == "text" && op.text == text && op.color == wantColor {
			foundText = true
		}
		if op.kind == "text" && op.text == "✓" {
			foundCheck = true
		}
	}
	if !foundText || foundCheck != wantCheck {
		t.Errorf("paint for %q = %+v, want color %s and check=%v", text, painter.ops, colorHex, wantCheck)
	}
}

func TestMenuBarRuntimeEnabledCheckedMetadataAndPaint(t *testing.T) {
	disabledFires, checkedFires := 0, 0
	disabled := MenuItem("Runtime disabled").OnClick(func() { disabledFires++ })
	checked := MenuItem("Runtime checked").OnClick(func() { checkedFires++ })
	fixture := newMenuTestFixture(t, Label("body"), 320, 180, Menu("File", disabled, checked))
	if err := fixture.app.LoadStyles(writeStyleSheet(t,
		"MenuItem:disabled { color: #010203; } MenuItem:checked { color: #040506; }")); err != nil {
		t.Fatalf("LoadStyles: %v", err)
	}
	fixture.sync(t)
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	disabledView := menuPopupItemAt(t, fixture.window.overlays[0], 0)
	checkedView := menuPopupItemAt(t, fixture.window.overlays[0], 1)

	fixture.post(t, func() {
		disabled.SetEnabled(false)
		checked.SetChecked(true)
	})
	disabledMeta := disabledView.(resolvedStyleReceiver).styleMeta()
	checkedMeta := checkedView.(resolvedStyleReceiver).styleMeta()
	if !disabledMeta.Disabled || disabledMeta.Checked || disabledMeta.Pressed || disabledMeta.Focus || disabledMeta.Hover {
		t.Errorf("runtime disabled metadata = %+v", disabledMeta)
	}
	if checkedMeta.Disabled || !checkedMeta.Checked {
		t.Errorf("runtime checked metadata = %+v", checkedMeta)
	}
	if disabledView.(focusableComponent).focusable() || !checkedView.(focusableComponent).focusable() {
		t.Error("runtime enabled state did not update actionability")
	}
	requireMenuTextPaint(t, fixture, disabledView, "Runtime disabled", "#010203", false)
	requireMenuTextPaint(t, fixture, checkedView, "Runtime checked", "#040506", true)

	menuClickT(t, fixture, disabledView)
	if disabledFires != 0 || len(fixture.window.overlays) != 1 {
		t.Errorf("runtime disabled click fired=%d overlays=%d, want 0/1", disabledFires, len(fixture.window.overlays))
	}
	menuClickT(t, fixture, checkedView)
	if checkedFires != 1 || checked.Checked() || len(fixture.window.overlays) != 0 {
		t.Errorf("runtime checked click fired=%d checked=%v overlays=%d, want 1/false/0",
			checkedFires, checked.Checked(), len(fixture.window.overlays))
	}
}

func TestWindowMenuReplacementStyleFailureIsAtomic(t *testing.T) {
	oldFires := 0
	oldItem := MenuItem("Old").Shortcut(ModCtrl, 'O').OnClick(func() { oldFires++ })
	fixture := newMenuTestFixture(t, Label("old content"), 360, 200, Menu("Old menu", oldItem))
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	popupInput := Input(nil)
	var independent *overlay
	fixture.post(t, func() {
		independent = fixture.window.pushOverlay(popupInput, rect(190, 60, 150, 36), nil, nil)
	})
	point := driver.Point{X: popupInput.bounds.X + 3, Y: center(popupInput.bounds).Y}
	menuClickT(t, fixture, popupInput)
	setCompositionTestPreedit(fixture.target, "清", 1)
	fixture.post(t, func() {
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionStart))
		fixture.target.handler(leftPress(point))
	})
	if fixture.window.router.focus != popupInput || fixture.window.router.press != popupInput ||
		!fixture.window.router.composing || !popupInput.composition.active {
		t.Fatal("test setup did not establish focus, press and live composition in the independent overlay")
	}
	if err := fixture.app.LoadStyles(writeStyleSheet(t, "#bad { font-size: 0px; }")); err != nil {
		t.Fatalf("LoadStyles: %v", err)
	}
	fixture.sync(t)

	oldRoot, oldContent, oldBar := fixture.window.root, fixture.window.content, fixture.window.menuBar
	oldMenus := append([]*MenuModel(nil), fixture.window.menus...)
	oldOverlays := append([]*overlay(nil), fixture.window.overlays...)
	oldRouter, oldActiveRoot := fixture.window.router, fixture.window.router.root
	oldFocus, oldPress := fixture.window.router.focus, fixture.window.router.press
	oldComposition := popupInput.composition
	fixture.post(t, func() { fixture.window.Content(Label("bad").ID("bad")) })
	if fixture.app.err() == nil {
		t.Fatal("replacement with font-size 0 unexpectedly succeeded")
	}
	if fixture.window.root != oldRoot || fixture.window.content != oldContent || fixture.window.menuBar != oldBar {
		t.Error("failed replacement changed root, content or menu bar")
	}
	if len(fixture.window.menus) != len(oldMenus) {
		t.Fatalf("failed replacement changed menu model count to %d", len(fixture.window.menus))
	}
	for index := range oldMenus {
		if fixture.window.menus[index] != oldMenus[index] {
			t.Errorf("failed replacement changed menu model %d", index)
		}
	}
	if len(fixture.window.overlays) != len(oldOverlays) {
		t.Fatalf("failed replacement changed overlay count to %d", len(fixture.window.overlays))
	}
	for index := range oldOverlays {
		if fixture.window.overlays[index] != oldOverlays[index] {
			t.Errorf("failed replacement changed overlay %d", index)
		}
	}
	if fixture.window.overlays[0].parent != nil || fixture.window.overlays[1] != independent {
		t.Error("failed replacement changed menu/independent overlay relationships")
	}
	if fixture.window.router != oldRouter || fixture.window.router.root != oldActiveRoot ||
		fixture.window.router.focus != oldFocus || fixture.window.router.press != oldPress ||
		!fixture.window.router.composing || popupInput.composition != oldComposition {
		t.Error("failed replacement changed router, focus, press or composition")
	}

	// End the established interaction only after proving it survived failure;
	// the old shortcut roots must still be live as well.
	fixture.post(t, func() {
		fixture.state.active = false
		fixture.target.handler(compositionTestEvent(fixture.target, driver.EventCompositionEnd))
		fixture.target.handler(leftRelease(point))
	})
	menuKeyT(t, fixture, driver.KeyO, driver.ModCtrl)
	if oldFires != 1 {
		t.Errorf("old shortcut after failed replacement fired %d times, want 1", oldFires)
	}
	fixture.app.Quit()
	if err := waitUIReturn(t, fixture.done); err == nil {
		t.Error("Run returned nil, want the retained font-size 0 replacement error")
	}
	fixture.finished = true
}

func TestMenuBarDisableOpenSubmenuOwnerPointer(t *testing.T) {
	fires, unrelatedFires := 0, 0
	leaf := MenuItem("Deep leaf").OnClick(func() { fires++ })
	owner := MenuItem("Recent").Submenu(
		MenuItem("More").Submenu(leaf),
	)
	fixture := newMenuTestFixture(t, Label("body"), 280, 190, Menu("File", owner))
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[0], 0))
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[1], 0))
	leafView := menuPopupItemAt(t, fixture.window.overlays[2], 0).(*menuItemWidget)
	unrelated := Button("Unrelated").OnClick(func() { unrelatedFires++ })
	fixture.post(t, func() {
		fixture.window.pushOverlay(unrelated, rect(205, 135, 65, 40), nil, nil)
	})

	fixture.post(t, func() { owner.SetEnabled(false) })
	if len(fixture.window.overlays) != 2 || fixture.window.overlays[1].root != unrelated {
		t.Errorf("disable left overlays = %d, want the File popup and unrelated overlay", len(fixture.window.overlays))
	}
	point := center(leafView.bounds)
	leafView.handleEvent(leftPress(point))
	leafView.handleEvent(leftRelease(point))
	if fires != 0 {
		t.Errorf("stale descendant pointer activation fired %d times after its owner was disabled", fires)
	}
	menuClickT(t, fixture, unrelated)
	if unrelatedFires != 1 {
		t.Errorf("unrelated overlay fired %d times, want 1", unrelatedFires)
	}
}

func TestMenuBarDisableOpenSubmenuOwnerKeyboard(t *testing.T) {
	fires := 0
	leaf := MenuItem("Deep leaf").OnClick(func() { fires++ })
	owner := MenuItem("Recent").Submenu(
		MenuItem("More").Submenu(leaf),
	)
	fixture := newMenuTestFixture(t, Label("body"), 280, 190, Menu("File", owner))
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[0], 0))
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[1], 0))
	menuKeyT(t, fixture, driver.KeyTab, 0)
	leafView := menuPopupItemAt(t, fixture.window.overlays[2], 0)
	if fixture.window.router.focus != leafView {
		t.Fatal("test setup did not focus the deep menu item")
	}

	fixture.post(t, func() { owner.SetEnabled(false) })
	if len(fixture.window.overlays) != 1 {
		t.Errorf("disable left %d menu overlays, want only the File popup", len(fixture.window.overlays))
	}
	menuKeyT(t, fixture, driver.KeyEnter, 0)
	if fires != 0 {
		t.Errorf("Enter activated a descendant of a disabled owner %d times", fires)
	}
}

func (w *uiTestTarget) setLogicalSize(size driver.Size) { w.logicalSize = size }

func (w *uiTestTarget) Size() driver.Size {
	if w.logicalSize.Width > 0 && w.logicalSize.Height > 0 {
		return w.logicalSize
	}
	return w.Window.Size()
}

func TestMenuBarLogicalResizeDismissesNestedChain(t *testing.T) {
	fixture := newMenuTestFixture(t, Label("body"), 420, 260, Menu("File",
		MenuItem("Recent").Submenu(
			MenuItem("More").Submenu(MenuItem("Deep leaf")),
		),
	))
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[0], 0))
	menuHoverT(t, fixture, menuPopupItemAt(t, fixture.window.overlays[1], 0))
	if len(fixture.window.overlays) != 3 {
		t.Fatalf("open chain length = %d, want 3", len(fixture.window.overlays))
	}
	for _, layer := range fixture.window.overlays {
		requireMenuPopupInside(t, layer, 420, 260)
	}
	leaf := menuPopupItemAt(t, fixture.window.overlays[2], 0)
	menuKeyT(t, fixture, driver.KeyTab, 0)
	fixture.post(t, func() {
		fixture.target.handler(leftPress(center(leaf.(boundsReporter).arrangedBounds())))
	})
	if fixture.window.router.focus != leaf || fixture.window.router.press != leaf {
		t.Fatal("test setup did not establish focus and press in the nested popup")
	}

	size := driver.Size{Width: 150, Height: 90}
	fixture.post(t, func() {
		fixture.target.setLogicalSize(size)
		fixture.target.handler(driver.Event{Kind: driver.EventResize, Window: fixture.target, Size: size})
	})
	if len(fixture.window.overlays) != 0 {
		t.Errorf("logical shrink left %d stale menu overlays, want none", len(fixture.window.overlays))
	}
	if fixture.window.router.root != fixture.window.root || fixture.window.router.focus != nil ||
		fixture.window.router.press != nil || fixture.window.router.hover != nil {
		t.Error("logical shrink retained menu routing, focus, press or hover")
	}
}

func TestWindowOnFocusModelChangeRedraws(t *testing.T) {
	item := MenuItem("Wrap")
	fixture := newMenuTestFixture(t, Label("body"), 260, 160, Menu("View", item))
	menuClickT(t, fixture, menuTitleAt(t, fixture.window, 0))
	fixture.renderer.texts = nil
	begins := fixture.renderer.begins
	delivered := make(chan struct{})
	if err := fixture.app.core.Post(func() {
		fixture.window.OnFocus(func(bool) { item.SetChecked(true) })
		fixture.target.handler(driver.Event{Kind: driver.EventFocus, Window: fixture.target, Focused: true})
		close(delivered)
	}); err != nil {
		t.Fatalf("Post focus event: %v", err)
	}
	receiveUI(t, delivered, "focus callback")
	if fixture.renderer.begins != begins+1 {
		t.Errorf("focus model update Begin count = %d, want %d", fixture.renderer.begins, begins+1)
	}
	foundCheck := false
	for _, text := range fixture.renderer.texts {
		foundCheck = foundCheck || text == "✓"
	}
	if !item.Checked() || !foundCheck {
		t.Errorf("focus update checked=%v frame texts=%v, want checked state and checkmark in a new frame", item.Checked(), fixture.renderer.texts)
	}
}

func TestWindowOnCancelComponentChangeRedraws(t *testing.T) {
	label := Label("before")
	fixture := newMenuTestFixture(t, label, 260, 160, Menu("File", MenuItem("Action")))
	fixture.renderer.texts = nil
	begins := fixture.renderer.begins
	delivered := make(chan struct{})
	if err := fixture.app.core.Post(func() {
		fixture.window.OnCancel(func() { label.SetText("after") })
		fixture.target.handler(driver.Event{Kind: driver.EventKey, Window: fixture.target, Key: driver.KeyEscape, Action: driver.ActionPress})
		close(delivered)
	}); err != nil {
		t.Fatalf("Post cancel event: %v", err)
	}
	receiveUI(t, delivered, "cancel callback")
	if fixture.renderer.begins != begins+1 {
		t.Errorf("cancel component update Begin count = %d, want %d", fixture.renderer.begins, begins+1)
	}
	foundAfter := false
	for _, text := range fixture.renderer.texts {
		foundAfter = foundAfter || text == "after"
	}
	if label.Text() != "after" || !foundAfter {
		t.Errorf("cancel update text=%q frame texts=%v, want updated label in a new frame", label.Text(), fixture.renderer.texts)
	}
}
