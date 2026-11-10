package ui_test

import (
	"testing"

	"github.com/zhoudm1743/meltemi/ui"
)

type adapterTestItem struct {
	text     string
	kind     ui.MenuItemKind
	enabled  bool
	checked  bool
	shortcut *ui.MenuShortcut
	children []adapterTestItem
	source   ui.MenuItemSnapshot
}

func adaptMenuItems(items []ui.MenuItemSnapshot) []adapterTestItem {
	adapted := make([]adapterTestItem, 0, len(items))
	for _, item := range items {
		adapted = append(adapted, adapterTestItem{
			text: item.Text, kind: item.Kind, enabled: item.Enabled,
			checked: item.Checked, shortcut: item.Shortcut,
			children: adaptMenuItems(item.Children), source: item,
		})
	}
	return adapted
}

func (i adapterTestItem) Activate() { i.source.Activate() }

func TestMenuSnapshotExternalTrayAdapter(t *testing.T) {
	leafCalls, separatorCalls, ownerCalls, disabledCalls, checkedCalls := 0, 0, 0, 0, 0
	var leaf *ui.MenuItemModel
	leaf = ui.MenuItem("Leaf").OnClick(func() {
		leafCalls++
		if !leaf.Checked() {
			t.Error("leaf callback ran before its checked state toggled")
		}
	})
	separator := ui.MenuSeparator().OnClick(func() { separatorCalls++ })
	owner := ui.MenuItem("Recent").OnClick(func() { ownerCalls++ }).Submenu(
		ui.MenuItem("More").Submenu(leaf),
	)
	disabled := ui.MenuItem("Disabled").SetEnabled(false).OnClick(func() { disabledCalls++ })
	var checked *ui.MenuItemModel
	checked = ui.MenuItem("Checked").Shortcut(ui.ModCtrl|ui.ModShift, 'K').OnClick(func() {
		checkedCalls++
		if !checked.Checked() {
			t.Error("checked callback ran before its checked state toggled")
		}
	})
	snapshot := ui.Menu("Tray", owner, separator, disabled, checked).Snapshot()
	if snapshot.Title != "Tray" || len(snapshot.Items) != 4 {
		t.Fatalf("snapshot = %+v, want Tray with four items", snapshot)
	}
	adapted := adaptMenuItems(snapshot.Items)
	if adapted[0].kind != ui.MenuItemSubmenu || len(adapted[0].children) != 1 ||
		adapted[0].children[0].kind != ui.MenuItemSubmenu || len(adapted[0].children[0].children) != 1 {
		t.Fatalf("adapter did not receive the nested submenu chain: %+v", adapted[0])
	}
	if adapted[1].kind != ui.MenuItemSeparator || adapted[1].enabled {
		t.Errorf("separator snapshot = %+v, want an inert separator", adapted[1])
	}
	if adapted[2].kind != ui.MenuItemAction || adapted[2].enabled {
		t.Errorf("disabled snapshot = %+v, want a disabled ordinary item", adapted[2])
	}
	if adapted[3].shortcut == nil || adapted[3].shortcut.Modifiers != ui.ModCtrl|ui.ModShift ||
		adapted[3].shortcut.Key != 'k' {
		t.Errorf("shortcut snapshot = %+v, want Ctrl+Shift+K", adapted[3].shortcut)
	}

	adapted[0].Activate()
	adapted[1].Activate()
	adapted[2].Activate()
	if ownerCalls != 0 || separatorCalls != 0 || disabledCalls != 0 {
		t.Errorf("inert activations fired owner=%d separator=%d disabled=%d", ownerCalls, separatorCalls, disabledCalls)
	}
	owner.SetEnabled(false)
	adapted[0].children[0].children[0].Activate()
	if leafCalls != 0 {
		t.Errorf("leaf under a newly disabled ancestor fired %d times", leafCalls)
	}
	owner.SetEnabled(true)
	adapted[0].children[0].children[0].Activate()
	if leafCalls != 1 || !leaf.Checked() {
		t.Errorf("enabled nested activation: calls=%d checked=%v, want 1/true", leafCalls, leaf.Checked())
	}
	adapted[3].Activate()
	if checkedCalls != 1 || !checked.Checked() {
		t.Errorf("checked activation: calls=%d checked=%v, want exactly 1/true", checkedCalls, checked.Checked())
	}
}
