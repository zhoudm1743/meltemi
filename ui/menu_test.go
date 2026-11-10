package ui

import (
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

// arrangedMenuRouter builds an arranged component tree with its event router
// and attaches menu roots for shortcut dispatch, mirroring how the window
// loop drives dispatch once MenuBar installation exists.
func arrangedMenuRouter(t *testing.T, content Component, menus ...*MenuModel) *eventRouter {
	t.Helper()
	root := Column(content)
	setTreeFace(root, testFace(t))
	laid := root.measure(Constraints{})
	root.arrange(rect(0, 0, laid.Width, laid.Height))
	router := newEventRouter(root)
	router.setMenuRoots(menus...)
	return router
}

func TestMenuModelAndShortcutNormalization(t *testing.T) {
	// The public modifier bits must mirror the driver bits so the router can
	// convert a driver shortcut for matching without any remapping table.
	if Modifiers(driver.ModShift) != ModShift || Modifiers(driver.ModCtrl) != ModCtrl ||
		Modifiers(driver.ModAlt) != ModAlt || Modifiers(driver.ModSuper) != ModSuper {
		t.Fatal("ui.Modifiers must mirror the driver modifier bits")
	}

	t.Run("construction and defaults", func(t *testing.T) {
		sep := MenuSeparator()
		if sep.Enabled() || sep.Checked() {
			t.Errorf("separator enabled=%v checked=%v, want false/false", sep.Enabled(), sep.Checked())
		}
		item := MenuItem("Open")
		if !item.Enabled() || item.Checked() {
			t.Errorf("new item enabled=%v checked=%v, want true/false", item.Enabled(), item.Checked())
		}
		item.SetEnabled(false)
		if item.Enabled() {
			t.Error("SetEnabled(false) not reported by Enabled")
		}
		item.SetChecked(true)
		if !item.Checked() {
			t.Error("SetChecked(true) not reported by Checked")
		}
		m := Menu("File", item, sep)
		if m.Title != "File" || len(m.Items) != 2 || m.Items[0] != item || m.Items[1] != sep {
			t.Errorf("menu = %+v, want title File with items in declaration order", m)
		}
	})

	t.Run("letter case normalizes", func(t *testing.T) {
		// 'S' and 's' describe the same shortcut and both fire on Ctrl+S.
		for _, key := range []rune{'S', 's'} {
			fires := 0
			item := MenuItem("Run").Shortcut(ModCtrl, key).OnClick(func() { fires++ })
			router := arrangedMenuRouter(t, Label("inert"), Menu("Tools", item))
			handled, _ := router.Handle(keyEvent(driver.KeyS, driver.ModCtrl))
			if !handled || fires != 1 {
				t.Errorf("Shortcut(ModCtrl, %q): handled=%v fires=%d, want true/1", key, handled, fires)
			}
		}
	})

	t.Run("digits match", func(t *testing.T) {
		fires := 0
		item := MenuItem("Tab 7").Shortcut(ModAlt, '7').OnClick(func() { fires++ })
		router := arrangedMenuRouter(t, Label("inert"), Menu("Tabs", item))
		handled, _ := router.Handle(keyEvent(driver.Key7, driver.ModAlt))
		if !handled || fires != 1 {
			t.Errorf("digit shortcut: handled=%v fires=%d, want true/1", handled, fires)
		}
	})

	t.Run("modifiers match exactly", func(t *testing.T) {
		fires := 0
		item := MenuItem("Save").Shortcut(ModCtrl, 'S').OnClick(func() { fires++ })
		router := arrangedMenuRouter(t, Label("inert"), Menu("File", item))
		// Shift is never folded into the rune case: only the exact mask fires.
		for _, mods := range []driver.Modifiers{
			0, driver.ModShift, driver.ModCtrl | driver.ModShift, driver.ModAlt, driver.ModSuper,
		} {
			if handled, _ := router.Handle(keyEvent(driver.KeyS, mods)); handled {
				t.Errorf("mods %d fired a ModCtrl shortcut", mods)
			}
		}
		if handled, _ := router.Handle(keyEvent(driver.KeyS, driver.ModCtrl)); !handled || fires != 1 {
			t.Errorf("exact mods: fires=%d, want 1", fires)
		}
		// A different key with the right mods must not fire either.
		if handled, _ := router.Handle(keyEvent(driver.KeyX, driver.ModCtrl)); handled || fires != 1 {
			t.Errorf("wrong key fired the shortcut: handled fires=%d", fires)
		}
	})

	t.Run("invalid shortcuts stay inactive without panic", func(t *testing.T) {
		invalid := []rune{'中', '€', 'Ｓ', ' ', '\t', '\n', 0}
		for _, key := range invalid {
			fires := 0
			item := MenuItem("Bad").Shortcut(ModCtrl, key).OnClick(func() { fires++ })
			router := arrangedMenuRouter(t, Label("inert"), Menu("M", item))
			handled, _ := router.Handle(keyEvent(driver.KeyS, driver.ModCtrl))
			if handled || fires != 0 {
				t.Errorf("invalid shortcut %q: handled=%v fires=%d, want false/0", key, handled, fires)
			}
		}
		// Each Shortcut call replaces the previous one; an invalid rune
		// therefore also clears an earlier valid shortcut.
		fires := 0
		item := MenuItem("Save").Shortcut(ModCtrl, 'S').OnClick(func() { fires++ })
		item.Shortcut(ModCtrl, '中')
		router := arrangedMenuRouter(t, Label("inert"), Menu("File", item))
		router.Handle(keyEvent(driver.KeyS, driver.ModCtrl))
		if fires != 0 {
			t.Errorf("invalid shortcut replaced a valid one but %d fired", fires)
		}
	})

	t.Run("no match stays unhandled", func(t *testing.T) {
		fires := 0
		item := MenuItem("Save").Shortcut(ModCtrl, 'S').OnClick(func() { fires++ })
		router := arrangedMenuRouter(t, Label("inert"), Menu("File", item))
		if handled, _ := router.Handle(keyEvent(driver.KeyQ, driver.ModCtrl)); handled {
			t.Error("an unmatched shortcut key must stay unhandled")
		}
		if handled, _ := router.Handle(keyEvent(driver.KeyEscape, 0)); handled {
			t.Error("Escape with no menus and no focus must stay unhandled")
		}
		bare := arrangedMenuRouter(t, Label("inert"))
		if handled, _ := bare.Handle(keyEvent(driver.KeyS, driver.ModCtrl)); handled {
			t.Error("a router without menu roots must stay unhandled")
		}
		if fires != 0 {
			t.Errorf("no-match dispatches fired %d callbacks", fires)
		}
	})
}

func TestMenuShortcutAfterFocusedComponent(t *testing.T) {
	input := Input(nil)
	var copies, saves int
	edit := Menu("Edit", MenuItem("Copy").Shortcut(ModCtrl, 'C').OnClick(func() { copies++ }))
	file := Menu("File", MenuItem("Save").Shortcut(ModCtrl, 'S').OnClick(func() { saves++ }))
	router := arrangedMenuRouter(t, input, edit, file)
	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != input {
		t.Fatalf("focus = %v, want the input", router.focus)
	}

	// The focused Input consumes Ctrl+C as copy; the menu item must not fire.
	if handled, _ := router.Handle(keyEvent(driver.KeyC, driver.ModCtrl)); !handled {
		t.Error("Ctrl+C must stay handled by the focused Input")
	}
	if copies != 0 {
		t.Errorf("menu Copy fired %d times although the Input consumed Ctrl+C", copies)
	}

	// Ctrl+S is left unconsumed by the Input and reaches the menu exactly once.
	handled, redraw := router.Handle(keyEvent(driver.KeyS, driver.ModCtrl))
	if !handled || !redraw {
		t.Errorf("Ctrl+S = (%v, %v), want (true, true)", handled, redraw)
	}
	if saves != 1 {
		t.Fatalf("saves = %d, want 1", saves)
	}

	// No double dispatch: the key release and the character event for the
	// same physical press must not fire the shortcut again.
	router.Handle(driver.Event{Kind: driver.EventKey, Key: driver.KeyS, Action: driver.ActionRelease, Mods: driver.ModCtrl})
	router.Handle(runeInputEvent('s'))
	if saves != 1 {
		t.Errorf("saves = %d after release+rune, want 1", saves)
	}

	// Multiple roots: the first matching item in attachment order wins and
	// the identical shortcut in the later root never fires.
	var firstSaves, secondSaves int
	first := Menu("File", MenuItem("Save").Shortcut(ModCtrl, 'S').OnClick(func() { firstSaves++ }))
	second := Menu("Also", MenuItem("Save As").Shortcut(ModCtrl, 'S').OnClick(func() { secondSaves++ }))
	router = arrangedMenuRouter(t, Input(nil), first, second)
	router.Handle(keyEvent(driver.KeyTab, 0))
	router.Handle(keyEvent(driver.KeyS, driver.ModCtrl))
	if firstSaves != 1 || secondSaves != 0 {
		t.Errorf("multiple roots fired first=%d second=%d, want 1/0", firstSaves, secondSaves)
	}
}

func TestMenuDisabledAndCheckedItems(t *testing.T) {
	t.Run("disabled item never fires and is skipped", func(t *testing.T) {
		var disabledFires, enabledFires int
		disabled := MenuItem("Copy").SetEnabled(false).Shortcut(ModCtrl, 'C').OnClick(func() { disabledFires++ })
		enabled := MenuItem("Clone").Shortcut(ModCtrl, 'C').OnClick(func() { enabledFires++ })
		router := arrangedMenuRouter(t, Label("inert"),
			Menu("Edit", disabled), Menu("Tools", enabled))
		handled, _ := router.Handle(keyEvent(driver.KeyC, driver.ModCtrl))
		if !handled || disabledFires != 0 || enabledFires != 1 {
			t.Errorf("disabled match: handled=%v disabled=%d enabled=%d, want true/0/1",
				handled, disabledFires, enabledFires)
		}
	})

	t.Run("checked toggles before the callback", func(t *testing.T) {
		var observed []bool
		var item *MenuItemModel
		item = MenuItem("Wrap").Shortcut(ModCtrl, 'W').OnClick(func() { observed = append(observed, item.Checked()) })
		router := arrangedMenuRouter(t, Label("inert"), Menu("View", item))
		router.Handle(keyEvent(driver.KeyW, driver.ModCtrl))
		router.Handle(keyEvent(driver.KeyW, driver.ModCtrl))
		if len(observed) != 2 || observed[0] != true || observed[1] != false {
			t.Errorf("callback observed checked %v, want [true false] (toggled before each callback)", observed)
		}
		if item.Checked() != false {
			t.Errorf("Checked() = %v after two activations, want false", item.Checked())
		}
	})

	t.Run("separator and submenu never activate directly", func(t *testing.T) {
		var sepFires, parentFires, childFires, leafFires int
		sep := MenuSeparator()
		sep.Shortcut(ModCtrl, 'P') // even a shortcut on a separator stays inert
		parent := MenuItem("Recent").Shortcut(ModCtrl, 'R').Submenu(
			MenuItem("Doc").Shortcut(ModCtrl, 'D').OnClick(func() { childFires++ }),
		)
		parent.OnClick(func() { parentFires++ })
		recent := MenuItem("More").Submenu(
			MenuItem("Deep").Submenu(
				MenuItem("Leaf").Shortcut(ModCtrl, 'L').OnClick(func() { leafFires++ }),
			),
		)
		router := arrangedMenuRouter(t, Label("inert"), Menu("File", sep, parent, recent))

		router.Handle(keyEvent(driver.KeyP, driver.ModCtrl))
		if sepFires != 0 {
			t.Errorf("separator fired %d times", sepFires)
		}
		router.Handle(keyEvent(driver.KeyR, driver.ModCtrl))
		if parentFires != 0 {
			t.Errorf("submenu item fired %d times directly", parentFires)
		}
		router.Handle(keyEvent(driver.KeyD, driver.ModCtrl))
		if childFires != 1 {
			t.Errorf("nested submenu child fired %d times, want 1", childFires)
		}
		router.Handle(keyEvent(driver.KeyL, driver.ModCtrl))
		if leafFires != 1 {
			t.Errorf("two-level nested leaf fired %d times, want 1", leafFires)
		}
	})

	t.Run("disabled submenu silences its subtree", func(t *testing.T) {
		var childFires int
		parent := MenuItem("Recent").SetEnabled(false).Submenu(
			MenuItem("Doc").Shortcut(ModCtrl, 'D').OnClick(func() { childFires++ }),
		)
		router := arrangedMenuRouter(t, Label("inert"), Menu("File", parent))
		router.Handle(keyEvent(driver.KeyD, driver.ModCtrl))
		if childFires != 0 {
			t.Errorf("child of a disabled submenu fired %d times", childFires)
		}
	})
}

func TestMenuShortcutDuringComposition(t *testing.T) {
	input := Input(nil)
	var newFires int
	// A plain-letter shortcut: no modifiers at all.
	item := MenuItem("New").Shortcut(0, 'n').OnClick(func() { newFires++ })
	router := arrangedMenuRouter(t, input, Menu("File", item))
	router.Handle(keyEvent(driver.KeyTab, 0))
	if router.focus != input {
		t.Fatalf("focus = %v, want the input", router.focus)
	}

	// Sanity check: without composition the shortcut fires.
	router.Handle(keyEvent(driver.KeyN, 0))
	if newFires != 1 {
		t.Fatalf("newFires = %d, want 1", newFires)
	}

	// While a composition owns the keyboard, the same key must not reach menu
	// matching even though the focused Input leaves it unconsumed. Keys the
	// IME consumed never become events at all; this gate covers the remaining
	// key events delivered during an active composition session.
	router.Handle(driver.Event{Kind: driver.EventCompositionStart})
	router.Handle(keyEvent(driver.KeyN, 0))
	router.Handle(driver.Event{Kind: driver.EventCompositionUpdate})
	router.Handle(keyEvent(driver.KeyN, 0))
	if newFires != 1 {
		t.Errorf("shortcut fired during composition: newFires = %d, want 1", newFires)
	}
	// Committed text arrives as rune events, never as shortcut triggers.
	router.Handle(runeInputEvent('n'))
	if newFires != 1 {
		t.Errorf("rune event fired the shortcut: newFires = %d, want 1", newFires)
	}

	router.Handle(driver.Event{Kind: driver.EventCompositionEnd})
	router.Handle(keyEvent(driver.KeyN, 0))
	if newFires != 2 {
		t.Errorf("newFires = %d after composition end, want 2", newFires)
	}
}
