package ui

import "sync"

// Modifiers is a bitmask of the modifier keys held while pressing a menu
// shortcut. The constants mirror the driver modifier bits one-to-one, so the
// event router can convert a backend event for matching without a remapping
// table; the public type keeps menu descriptions independent of the driver.
type Modifiers uint8

const (
	ModShift Modifiers = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
)

var menuListenersMu sync.Mutex

type menuSubscription struct {
	root     *MenuModel
	models   []*MenuModel
	items    []*MenuItemModel
	callback func()
	active   bool
}

func subscribeMenuChanges(root *MenuModel, callback func()) *menuSubscription {
	if root == nil || callback == nil {
		return nil
	}
	subscription := &menuSubscription{root: root, callback: callback, active: true}
	menuListenersMu.Lock()
	subscription.refreshLocked()
	menuListenersMu.Unlock()
	return subscription
}

func (s *menuSubscription) refreshLocked() {
	for _, model := range s.models {
		delete(model.subscriptions, s)
	}
	s.models = nil
	for _, item := range s.items {
		delete(item.listeners, s)
	}
	s.items = nil
	seenModels := make(map[*MenuModel]bool)
	seenItems := make(map[*MenuItemModel]bool)
	var visit func(*MenuModel)
	visit = func(model *MenuModel) {
		if model == nil || seenModels[model] {
			return
		}
		seenModels[model] = true
		s.models = append(s.models, model)
		if model.subscriptions == nil {
			model.subscriptions = make(map[*menuSubscription]struct{})
		}
		model.subscriptions[s] = struct{}{}
		for _, item := range model.Items {
			if item == nil || seenItems[item] {
				continue
			}
			seenItems[item] = true
			s.items = append(s.items, item)
			if item.listeners == nil {
				item.listeners = make(map[*menuSubscription]struct{})
			}
			item.listeners[s] = struct{}{}
			if item.submenu != nil {
				visit(item.submenu)
			}
		}
	}
	visit(s.root)
}

func (s *menuSubscription) unsubscribe() {
	if s == nil {
		return
	}
	menuListenersMu.Lock()
	defer menuListenersMu.Unlock()
	if !s.active {
		return
	}
	s.active = false
	for _, model := range s.models {
		delete(model.subscriptions, s)
	}
	s.models = nil
	for _, item := range s.items {
		delete(item.listeners, s)
	}
	s.items = nil
	s.root = nil
	s.callback = nil
}

func activeMenuSubscriptionsLocked(listeners map[*menuSubscription]struct{}) []*menuSubscription {
	subscriptions := make([]*menuSubscription, 0, len(listeners))
	for subscription := range listeners {
		if subscription.active {
			subscriptions = append(subscriptions, subscription)
		}
	}
	return subscriptions
}

func notifyMenuSubscriptions(subscriptions []*menuSubscription) {
	for _, subscription := range subscriptions {
		menuListenersMu.Lock()
		active := subscription.active
		callback := subscription.callback
		menuListenersMu.Unlock()
		if active && callback != nil {
			callback()
		}
	}
}

func notifyMenuChanged(listeners map[*menuSubscription]struct{}) {
	menuListenersMu.Lock()
	subscriptions := activeMenuSubscriptionsLocked(listeners)
	menuListenersMu.Unlock()
	notifyMenuSubscriptions(subscriptions)
}

func (i *MenuItemModel) notifyMenuChanged() {
	notifyMenuChanged(i.listeners)
}

func refreshMenuSubscriptions(listeners map[*menuSubscription]struct{}) {
	menuListenersMu.Lock()
	subscriptions := activeMenuSubscriptionsLocked(listeners)
	for _, subscription := range subscriptions {
		subscription.refreshLocked()
	}
	menuListenersMu.Unlock()
	notifyMenuSubscriptions(subscriptions)
}

func (i *MenuItemModel) refreshMenuSubscriptions() {
	refreshMenuSubscriptions(i.listeners)
}

// MenuModel describes one drop-down menu: a title and its items in display
// order. A window attaches the model for shortcut dispatch and for its
// self-drawn menu bar and popups. Model mutations are confined to the UI
// thread, or to single-threaded configuration before Run.
type MenuModel struct {
	Title string
	// Items is public for declarative construction. Use SetItems for live
	// updates; direct replacement requires reinstalling via Tray.SetMenu.
	Items         []*MenuItemModel
	subscriptions map[*menuSubscription]struct{}
}

// Menu creates a menu with the given title and items in display order. The
// constructor takes the concise name while the type carries a Model suffix,
// the same pattern as Input/InputWidget and Button/ButtonWidget.
func Menu(title string, items ...*MenuItemModel) *MenuModel {
	return &MenuModel{Title: title, Items: items}
}

// SetItems replaces the items on the UI thread, refreshes native subscriptions
// for the new tree and publishes a fresh snapshot to installed menus.
func (m *MenuModel) SetItems(items ...*MenuItemModel) *MenuModel {
	m.Items = items
	refreshMenuSubscriptions(m.subscriptions)
	return m
}

// MenuItemModel is one entry of a MenuModel: an ordinary clickable item, a
// separator, or an item owning a submenu (its label). All state lives on the
// item itself; separate windows never share items unless the application
// hands the same pointers to both.
type MenuItemModel struct {
	// Text is public for declarative construction. Use SetText for live
	// updates; direct replacement requires reinstalling via Tray.SetMenu.
	Text      string
	separator bool
	submenu   *MenuModel

	onClick func()
	enabled bool
	checked bool

	shortcutMods Modifiers
	shortcutKey  rune
	hasShortcut  bool

	listeners map[*menuSubscription]struct{}
}

// MenuItemKind identifies how a backend should present a menu item.
type MenuItemKind uint8

const (
	MenuItemAction MenuItemKind = iota
	MenuItemSeparator
	MenuItemSubmenu
)

// MenuShortcut is the backend-neutral description of an item shortcut.
type MenuShortcut struct {
	Modifiers Modifiers
	Key       rune
}

// MenuSnapshot is a detached, read-only-by-convention view of a menu. Changing
// snapshot data does not change the live model.
type MenuSnapshot struct {
	Title string
	Items []MenuItemSnapshot
}

// MenuItemSnapshot is a detached item description suitable for native menu and
// tray adapters. Children contains submenu entries in display order.
type MenuItemSnapshot struct {
	Text     string
	Kind     MenuItemKind
	Enabled  bool
	Checked  bool
	Shortcut *MenuShortcut
	Children []MenuItemSnapshot

	root      *MenuModel
	model     *MenuItemModel
	ancestors []*MenuItemModel
}

// Snapshot copies the menu's presentation data while retaining private live
// references used only to guard a later Activate call.
func (m *MenuModel) Snapshot() MenuSnapshot {
	if m == nil {
		return MenuSnapshot{}
	}
	return MenuSnapshot{Title: m.Title, Items: snapshotMenuItems(m, m.Items, nil)}
}

func snapshotMenuItems(root *MenuModel, items []*MenuItemModel, ancestors []*MenuItemModel) []MenuItemSnapshot {
	snapshots := make([]MenuItemSnapshot, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		kind := MenuItemAction
		switch {
		case item.separator:
			kind = MenuItemSeparator
		case item.submenu != nil:
			kind = MenuItemSubmenu
		}
		snapshot := MenuItemSnapshot{
			Text: item.Text, Kind: kind, Enabled: item.enabled, Checked: item.checked,
			root: root, model: item, ancestors: append([]*MenuItemModel(nil), ancestors...),
		}
		if item.hasShortcut {
			snapshot.Shortcut = &MenuShortcut{Modifiers: item.shortcutMods, Key: item.shortcutKey}
		}
		if item.submenu != nil {
			nextAncestors := append(append([]*MenuItemModel(nil), ancestors...), item)
			snapshot.Children = snapshotMenuItems(root, item.submenu.Items, nextAncestors)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

// Activate triggers an ordinary item on the UI thread. Native adapters whose
// callbacks arrive on another thread must call Activate inside App.Post. The
// live model is rechecked, so disabled, separator, submenu and detached items
// stay inert even when the snapshot's copied fields say otherwise.
func (s MenuItemSnapshot) Activate() {
	item := s.model
	if s.root == nil || item == nil || !item.enabled || item.separator || item.submenu != nil {
		return
	}
	child := item
	for index := len(s.ancestors) - 1; index >= 0; index-- {
		owner := s.ancestors[index]
		if owner == nil || !owner.enabled || owner.separator || owner.submenu == nil ||
			!menuContainsItem(owner.submenu.Items, child) {
			return
		}
		child = owner
	}
	if !menuContainsItem(s.root.Items, child) {
		return
	}
	item.trigger()
}

func menuContainsItem(items []*MenuItemModel, wanted *MenuItemModel) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

// MenuItem creates an ordinary enabled, unchecked menu item labelled text.
func MenuItem(text string) *MenuItemModel {
	return &MenuItemModel{Text: text, enabled: true}
}

// SetText replaces the item label on the UI thread and publishes a fresh
// snapshot to installed native menus.
func (i *MenuItemModel) SetText(text string) *MenuItemModel {
	i.Text = text
	i.notifyMenuChanged()
	return i
}

// MenuSeparator creates an inert separator entry. It never activates, and it
// starts disabled because it is not an actionable item at all.
func MenuSeparator() *MenuItemModel {
	return &MenuItemModel{separator: true}
}

// OnClick installs the callback fired when the item activates. Activation
// runs on the UI thread, inside the event dispatch that triggered it.
func (i *MenuItemModel) OnClick(fn func()) *MenuItemModel {
	i.onClick = fn
	return i
}

// SetEnabled controls whether the item can activate. Disabled items never
// fire, and a disabled submenu silences its whole subtree during shortcut
// matching.
func (i *MenuItemModel) SetEnabled(enabled bool) *MenuItemModel {
	i.enabled = enabled
	i.notifyMenuChanged()
	return i
}

// Enabled reports whether the item can activate.
func (i *MenuItemModel) Enabled() bool { return i.enabled }

// SetChecked sets the item's checked state. Pointer, keyboard and shortcut
// activation toggle the state on their own, so this is for initialization and
// explicit model updates.
func (i *MenuItemModel) SetChecked(checked bool) *MenuItemModel {
	i.checked = checked
	i.notifyMenuChanged()
	return i
}

// Checked reports the item's checked state.
func (i *MenuItemModel) Checked() bool { return i.checked }

// Shortcut assigns the keyboard shortcut that activates the item. The key
// must be an ASCII letter or digit; letters are normalized to lowercase, so
// 'S' and 's' describe the same shortcut while Shift stays part of the exact
// modifier match. Any other rune — non-ASCII text, spaces, control runes —
// leaves the item without a shortcut instead of panicking, and because every
// call replaces the previous assignment it also clears an earlier valid one.
func (i *MenuItemModel) Shortcut(mods Modifiers, key rune) *MenuItemModel {
	switch {
	case key >= 'a' && key <= 'z', key >= '0' && key <= '9':
		// Already normalized.
	case key >= 'A' && key <= 'Z':
		key += 'a' - 'A'
	default:
		i.shortcutMods, i.shortcutKey, i.hasShortcut = 0, 0, false
		i.notifyMenuChanged()
		return i
	}
	i.shortcutMods, i.shortcutKey, i.hasShortcut = mods, key, true
	i.notifyMenuChanged()
	return i
}

// Submenu attaches a submenu built from items, labelled by this item's text.
// A submenu item never activates directly — not even with its own shortcut —
// but its children are matched depth-first, so nested items can own
// shortcuts.
func (i *MenuItemModel) Submenu(items ...*MenuItemModel) *MenuItemModel {
	i.submenu = Menu(i.Text, items...)
	i.refreshMenuSubscriptions()
	return i
}

// trigger activates the item: the checked state toggles first so the callback
// observes the new value, then the callback fires. Separators, submenu owners
// and disabled items never reach trigger.
func (i *MenuItemModel) trigger() {
	i.checked = !i.checked
	i.notifyMenuChanged()
	if i.onClick != nil {
		i.onClick()
	}
}

// shortcutMatch reports whether the item's active shortcut equals mods and
// key exactly.
func (i *MenuItemModel) shortcutMatch(mods Modifiers, key rune) bool {
	return i.hasShortcut && i.shortcutMods == mods && i.shortcutKey == key
}
