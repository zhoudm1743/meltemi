//go:build linux

package desktop

import (
	"errors"
	"image"
	"reflect"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type testMenuIdentity struct{ name string }

func testTrayIcon(t *testing.T) Icon {
	t.Helper()
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	return icon
}

func exportAt(t *testing.T, bus *fakeTrayBus, path dbus.ObjectPath, iface string) interface{} {
	t.Helper()
	for i := len(bus.exports) - 1; i >= 0; i-- {
		export := bus.exports[i]
		if export.path == path && export.iface == iface && export.value != nil {
			return export.value
		}
	}
	t.Fatalf("%s was not exported at %s", iface, path)
	return nil
}

func dbusMenuObject(t *testing.T, bus *fakeTrayBus) interface {
	GetLayout(int32, int32, []string) (uint32, dbusMenuLayout, *dbus.Error)
	GetGroupProperties([]int32, []string) ([]dbusMenuItemProperties, *dbus.Error)
	Event(int32, string, dbus.Variant, uint32) *dbus.Error
	AboutToShow(int32) (bool, *dbus.Error)
} {
	t.Helper()
	object, ok := exportAt(t, bus, dbusMenuPath, dbusMenuInterface).(interface {
		GetLayout(int32, int32, []string) (uint32, dbusMenuLayout, *dbus.Error)
		GetGroupProperties([]int32, []string) ([]dbusMenuItemProperties, *dbus.Error)
		Event(int32, string, dbus.Variant, uint32) *dbus.Error
		AboutToShow(int32) (bool, *dbus.Error)
	})
	if !ok {
		t.Fatal("exported /MenuBar object does not implement the dbusmenu methods")
	}
	return object
}

func getTestLayout(t *testing.T, object interface {
	GetLayout(int32, int32, []string) (uint32, dbusMenuLayout, *dbus.Error)
}, parent, depth int32) (uint32, dbusMenuLayout) {
	t.Helper()
	revision, layout, dbusErr := object.GetLayout(parent, depth, nil)
	if dbusErr != nil {
		t.Fatalf("GetLayout(%d, %d): %v", parent, depth, dbusErr)
	}
	return revision, layout
}

func TestDBusMenuLayoutAndEvents(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	rootID := &testMenuIdentity{name: "root"}
	restoreID := &testMenuIdentity{name: "restore"}
	disabledID := &testMenuIdentity{name: "disabled"}
	separatorID := &testMenuIdentity{name: "separator"}
	checkedID := &testMenuIdentity{name: "checked"}
	submenuID := &testMenuIdentity{name: "submenu"}
	childID := &testMenuIdentity{name: "child"}
	var restoreCalls, disabledCalls, separatorCalls, checkedCalls, submenuCalls, childCalls int
	menu := TrayMenu{
		Source: rootID,
		Items: []TrayMenuItem{
			{Source: restoreID, Text: "Restore window", Kind: TrayMenuAction, Enabled: true, Activate: func() { restoreCalls++ }},
			{Source: disabledID, Text: "Disabled", Kind: TrayMenuAction, Enabled: false, Activate: func() { disabledCalls++ }},
			{Source: separatorID, Kind: TrayMenuSeparator, Activate: func() { separatorCalls++ }},
			{Source: checkedID, Text: "Checked", Kind: TrayMenuAction, Enabled: true, Checked: true, Activate: func() { checkedCalls++ }},
			{Source: submenuID, Text: "More", Kind: TrayMenuSubmenu, Enabled: true, Activate: func() { submenuCalls++ }, Children: []TrayMenuItem{
				{Source: childID, Text: "Deep leaf", Kind: TrayMenuAction, Enabled: true, Shortcut: &TrayMenuShortcut{Modifiers: TrayMenuModCtrl | TrayMenuModShift, Key: 'x'}, Activate: func() { childCalls++ }},
			}},
		},
	}
	if err := service.SetMenu(menu); err != nil {
		t.Fatalf("SetMenu before Show: %v", err)
	}
	menu.Items[0].Text = "mutated after installation"
	menu.Items[4].Children[0].Text = "also mutated"
	if err := service.Show(TrayConfig{Icon: testTrayIcon(t), Tooltip: "menu"}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	defer service.Close()

	properties := exportedProperties(t, bus)
	assertProperty(t, properties, "Menu", dbusMenuPath)
	object := dbusMenuObject(t, bus)
	revision, layout := getTestLayout(t, object, 0, -1)
	if revision != 1 {
		t.Errorf("initial revision = %d, want 1", revision)
	}
	if layout.ID != 0 || len(layout.Properties) != 0 || len(layout.Children) != 5 {
		t.Fatalf("root layout = %+v, want ID 0, no title/properties and five children", layout)
	}
	if got := dbus.SignatureOf(layout).String(); got != "(ia{sv}av)" {
		t.Errorf("layout D-Bus signature = %q, want (ia{sv}av)", got)
	}

	children := make([]dbusMenuLayout, len(layout.Children))
	for i, child := range layout.Children {
		value, ok := child.Value().(dbusMenuLayout)
		if !ok {
			t.Fatalf("child %d has wire type %T, want dbusMenuLayout", i, child.Value())
		}
		children[i] = value
	}
	for i, child := range children {
		if child.ID != int32(i+1) {
			t.Errorf("top-level child %d ID = %d, want %d", i, child.ID, i+1)
		}
	}
	if got := children[0].Properties["label"].Value(); got != "Restore window" {
		t.Errorf("restore label = %#v, want detached original", got)
	}
	if got := children[0].Properties["enabled"].Value(); got != true {
		t.Errorf("restore enabled = %#v (%T), want bool true", got, got)
	}
	if got := children[1].Properties["enabled"].Value(); got != false {
		t.Errorf("disabled enabled = %#v, want false", got)
	}
	if got := children[2].Properties["type"].Value(); got != "separator" {
		t.Errorf("separator type = %#v, want separator", got)
	}
	if got := children[3].Properties["toggle-type"].Value(); got != "checkmark" {
		t.Errorf("checked toggle-type = %#v, want checkmark", got)
	}
	if got := children[3].Properties["toggle-state"].Value(); got != int32(1) {
		t.Errorf("checked toggle-state = %#v (%T), want int32(1)", got, got)
	}
	if got := children[4].Properties["children-display"].Value(); got != "submenu" {
		t.Errorf("submenu children-display = %#v, want submenu", got)
	}
	if len(children[4].Children) != 1 {
		t.Fatalf("recursive submenu children = %d, want 1", len(children[4].Children))
	}
	leaf := children[4].Children[0].Value().(dbusMenuLayout)
	if leaf.ID != 6 || leaf.Properties["label"].Value() != "Deep leaf" {
		t.Errorf("deep leaf = ID %d label %#v, want 6/Deep leaf", leaf.ID, leaf.Properties["label"].Value())
	}
	shortcut := leaf.Properties["shortcut"].Value()
	if !reflect.DeepEqual(shortcut, [][]string{{"Control", "Shift", "X"}}) {
		t.Errorf("shortcut = %#v (%T), want aas [[Control Shift X]]", shortcut, shortcut)
	}
	if got := dbus.SignatureOf(shortcut).String(); got != "aas" {
		t.Errorf("shortcut signature = %q, want aas", got)
	}

	_, shallow := getTestLayout(t, object, 0, 0)
	if len(shallow.Children) != 0 {
		t.Errorf("depth 0 returned %d children, want 0", len(shallow.Children))
	}
	_, oneLevel := getTestLayout(t, object, 0, 1)
	oneSubmenu := oneLevel.Children[4].Value().(dbusMenuLayout)
	if len(oneLevel.Children) != 5 || len(oneSubmenu.Children) != 0 {
		t.Errorf("depth 1 children/submenu = %d/%d, want 5/0", len(oneLevel.Children), len(oneSubmenu.Children))
	}

	groups, dbusErr := object.GetGroupProperties([]int32{4, 6}, nil)
	if dbusErr != nil {
		t.Fatalf("GetGroupProperties: %v", dbusErr)
	}
	if got := dbus.SignatureOf(groups).String(); got != "a(ia{sv})" {
		t.Errorf("group properties signature = %q, want a(ia{sv})", got)
	}
	if len(groups) != 2 || groups[0].ID != 4 || groups[1].ID != 6 || groups[0].Properties["toggle-state"].Value() != int32(1) {
		t.Errorf("group properties = %+v, want checked ID 4 and leaf ID 6", groups)
	}
	filtered, dbusErr := object.GetGroupProperties([]int32{4}, []string{"enabled"})
	if dbusErr != nil || len(filtered) != 1 || len(filtered[0].Properties) != 1 || filtered[0].Properties["enabled"].Value() != true {
		t.Errorf("filtered properties = %+v, %v; want only enabled=true", filtered, dbusErr)
	}

	for _, id := range []int32{0, 2, 3, 5, 999} {
		if dbusErr := object.Event(id, "clicked", dbus.MakeVariant(uint32(0)), 10); dbusErr != nil {
			t.Errorf("inert Event(%d): %v", id, dbusErr)
		}
	}
	if restoreCalls != 0 || disabledCalls != 0 || separatorCalls != 0 || checkedCalls != 0 || submenuCalls != 0 || childCalls != 0 {
		t.Fatalf("inert events activated callbacks: %d %d %d %d %d %d", restoreCalls, disabledCalls, separatorCalls, checkedCalls, submenuCalls, childCalls)
	}
	for i := 0; i < 2; i++ {
		if dbusErr := object.Event(1, "clicked", dbus.MakeVariant(uint32(0)), uint32(20+i)); dbusErr != nil {
			t.Fatalf("restore clicked: %v", dbusErr)
		}
	}
	if restoreCalls != 2 {
		t.Errorf("two deliberate restore clicks produced %d callbacks, want 2", restoreCalls)
	}
	if dbusErr := object.Event(6, "clicked", dbus.MakeVariant("leaf"), 30); dbusErr != nil || childCalls != 1 {
		t.Errorf("nested clicked error/calls = %v/%d, want nil/1", dbusErr, childCalls)
	}
	if needUpdate, dbusErr := object.AboutToShow(5); dbusErr != nil || needUpdate {
		t.Errorf("AboutToShow = %v, %v; want false, nil", needUpdate, dbusErr)
	}
	if currentRevision, _ := getTestLayout(t, object, 0, -1); currentRevision != revision {
		t.Errorf("events changed revision from %d to %d", revision, currentRevision)
	}
}

func TestTrayMenuRevisionAndWatcherRestart(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	rootID := &testMenuIdentity{name: "root"}
	itemID := &testMenuIdentity{name: "item"}
	var calls int
	install := func(enabled bool, checked bool) {
		t.Helper()
		if err := service.SetMenu(TrayMenu{Source: rootID, Items: []TrayMenuItem{{
			Source: itemID, Text: "Toggle", Kind: TrayMenuAction, Enabled: enabled, Checked: checked,
			Activate: func() { calls++ },
		}}}); err != nil {
			t.Fatalf("SetMenu: %v", err)
		}
	}
	install(true, false)
	if err := service.Show(TrayConfig{Icon: testTrayIcon(t)}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	object := dbusMenuObject(t, bus)
	_, initial := getTestLayout(t, object, 0, -1)
	firstID := initial.Children[0].Value().(dbusMenuLayout).ID
	if bus.addMatchCalls != 1 {
		t.Fatalf("AddMatch calls = %d, want one watcher subscription", bus.addMatchCalls)
	}

	install(false, true)
	revision, updated := getTestLayout(t, object, 0, -1)
	updatedItem := updated.Children[0].Value().(dbusMenuLayout)
	if revision != 2 || updatedItem.ID != firstID || updatedItem.Properties["enabled"].Value() != false || updatedItem.Properties["toggle-state"].Value() != int32(1) {
		t.Errorf("updated revision/item = %d/%+v, want revision 2, stable ID %d, disabled and checked", revision, updatedItem, firstID)
	}
	lastSignal := bus.signals[len(bus.signals)-1]
	if lastSignal.path != dbusMenuPath || lastSignal.name != dbusMenuInterface+".LayoutUpdated" || !reflect.DeepEqual(lastSignal.body, []interface{}{uint32(2), int32(0)}) {
		t.Errorf("update signal = %+v, want /MenuBar LayoutUpdated(uint32(2), int32(0))", lastSignal)
	}

	bus.mu.Lock()
	signalChannel := bus.signalChannel
	bus.mu.Unlock()
	if signalChannel == nil {
		t.Fatal("service did not register a D-Bus signal channel")
	}
	signalChannel <- &dbus.Signal{
		Sender: "org.freedesktop.DBus",
		Path:   "/org/freedesktop/DBus",
		Name:   "org.freedesktop.DBus.NameOwnerChanged",
		Body:   []interface{}{statusNotifierWatcherName, ":1.99", ":1.100"},
	}
	waitFor(t, time.Second, func() bool { return bus.registerCallCount() == 2 })
	if bus.addMatchCalls != 1 {
		t.Errorf("watcher restart duplicated signal matches: %d", bus.addMatchCalls)
	}
	if afterRestart, _ := getTestLayout(t, object, 0, -1); afterRestart != 2 {
		t.Errorf("watcher restart changed menu revision to %d, want 2", afterRestart)
	}
	if dbusErr := object.Event(firstID, "clicked", dbus.MakeVariant(uint32(0)), 1); dbusErr != nil {
		t.Fatalf("clicked before replacement: %v", dbusErr)
	}
	if calls != 0 {
		t.Error("disabled current item activated")
	}

	var replacementCalls int
	if err := service.SetMenu(TrayMenu{Source: &testMenuIdentity{name: "replacement"}, Items: []TrayMenuItem{{
		Source: itemID, Text: "Replacement", Kind: TrayMenuAction, Enabled: true, Activate: func() { replacementCalls++ },
	}}}); err != nil {
		t.Fatalf("replace menu: %v", err)
	}
	replacementRevision, replacement := getTestLayout(t, object, 0, -1)
	replacementID := replacement.Children[0].Value().(dbusMenuLayout).ID
	if replacementRevision != 3 || replacementID <= firstID {
		t.Errorf("replacement revision/ID = %d/%d, want 3/an ID above %d", replacementRevision, replacementID, firstID)
	}
	if dbusErr := object.Event(firstID, "clicked", dbus.MakeVariant(uint32(0)), 2); dbusErr != nil {
		t.Fatalf("late old event: %v", dbusErr)
	}
	if calls != 0 || replacementCalls != 0 {
		t.Errorf("late replacement event side effects = old %d new %d, want 0/0", calls, replacementCalls)
	}

	if err := service.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if bus.removeMatchCalls != 1 {
		t.Errorf("RemoveMatch calls after Hide = %d, want 1", bus.removeMatchCalls)
	}
	signalChannel <- &dbus.Signal{Name: "org.freedesktop.DBus.NameOwnerChanged", Body: []interface{}{statusNotifierWatcherName, ":1.100", ":1.101"}}
	time.Sleep(30 * time.Millisecond)
	if got := bus.registerCallCount(); got != 2 {
		t.Errorf("late watcher signal after Hide caused %d registrations, want 2", got)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if dbusErr := object.Event(replacementID, "clicked", dbus.MakeVariant(uint32(0)), 3); dbusErr != nil {
		t.Fatalf("late event after Close returned a D-Bus error: %v", dbusErr)
	}
	if replacementCalls != 0 {
		t.Error("late event after Close activated the replacement")
	}
}

func TestDBusMenuRejectsUnrepresentableID(t *testing.T) {
	service := &linuxTrayService{nextMenuID: int64(^uint32(0) >> 1)}
	err := service.SetMenu(TrayMenu{Items: []TrayMenuItem{{Text: "overflow", Kind: TrayMenuAction, Enabled: true}}})
	if err == nil {
		t.Fatal("SetMenu succeeded after exhausting positive int32 item IDs")
	}
	if !errors.Is(err, errTrayMenuIDExhausted) {
		t.Fatalf("SetMenu error = %v, want errTrayMenuIDExhausted", err)
	}
}

func TestDBusMenuRegistrationAllowsSynchronousLayoutRead(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	if err := service.SetMenu(TrayMenu{Items: []TrayMenuItem{{Text: "Restore", Kind: TrayMenuAction, Enabled: true}}}); err != nil {
		t.Fatalf("SetMenu: %v", err)
	}
	readDone := make(chan error, 1)
	bus.onRegister = func() {
		object := dbusMenuObject(t, bus)
		revision, layout, dbusErr := object.GetLayout(0, -1, nil)
		if dbusErr != nil {
			readDone <- errors.New(dbusErr.Error())
			return
		}
		if revision != 1 || len(layout.Children) != 1 {
			readDone <- errors.New("watcher read an incomplete menu during registration")
			return
		}
		readDone <- nil
	}
	showDone := make(chan error, 1)
	go func() { showDone <- service.Show(TrayConfig{Icon: testTrayIcon(t)}) }()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("watcher GetLayout deadlocked during RegisterStatusNotifierItem")
	}
	select {
	case err := <-showDone:
		if err != nil {
			t.Fatalf("Show: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Show did not return after the watcher layout read")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDBusMenuGetGroupPropertiesEmptyIDsReturnsAllRecursively(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	if err := service.SetMenu(TrayMenu{Items: []TrayMenuItem{
		{Text: "First", Kind: TrayMenuAction, Enabled: true},
		{Text: "More", Kind: TrayMenuSubmenu, Enabled: true, Children: []TrayMenuItem{
			{Text: "Nested", Kind: TrayMenuAction, Enabled: true, Checked: true},
		}},
		{Kind: TrayMenuSeparator},
	}}); err != nil {
		t.Fatalf("SetMenu: %v", err)
	}
	if err := service.Show(TrayConfig{Icon: testTrayIcon(t)}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	defer service.Close()
	object := dbusMenuObject(t, bus)
	for _, ids := range [][]int32{nil, {}} {
		groups, dbusErr := object.GetGroupProperties(ids, nil)
		if dbusErr != nil {
			t.Fatalf("GetGroupProperties(%v): %v", ids, dbusErr)
		}
		if len(groups) != 4 {
			t.Fatalf("GetGroupProperties(%v) returned %d items, want all four recursive non-root items: %+v", ids, len(groups), groups)
		}
		wantLabels := []string{"First", "More", "Nested", ""}
		for i, group := range groups {
			if group.ID != int32(i+1) {
				t.Errorf("item %d ID = %d, want depth-first ID %d", i, group.ID, i+1)
			}
			label := ""
			if variant, ok := group.Properties["label"]; ok {
				label = variant.Value().(string)
			}
			if label != wantLabels[i] {
				t.Errorf("item %d label = %q, want %q", i, label, wantLabels[i])
			}
		}
		if groups[2].Properties["toggle-state"].Value() != int32(1) || groups[3].Properties["type"].Value() != "separator" {
			t.Errorf("recursive checked/separator properties = %+v/%+v", groups[2], groups[3])
		}
		if got := dbus.SignatureOf(groups).String(); got != "a(ia{sv})" {
			t.Errorf("all-item properties signature = %q, want a(ia{sv})", got)
		}
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
