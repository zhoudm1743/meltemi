//go:build linux

package desktop

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/godbus/dbus/v5"
)

const (
	dbusMenuInterface = "com.canonical.dbusmenu"
	dbusMenuPath      = dbus.ObjectPath("/MenuBar")
)

var errTrayMenuIDExhausted = errors.New("desktop: tray menu item IDs exhausted")

type dbusMenuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

type dbusMenuItemProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}

type dbusMenuNode struct {
	id       int32
	item     TrayMenuItem
	children []*dbusMenuNode
}

type dbusMenuHandler struct {
	service *linuxTrayService
}

func sameMenuSource(first, second interface{}) bool {
	if first == nil || second == nil {
		return false
	}
	typeOfFirst := reflect.TypeOf(first)
	return typeOfFirst == reflect.TypeOf(second) && typeOfFirst.Comparable() && first == second
}

func (s *linuxTrayService) SetMenu(menu TrayMenu) error {
	if s == nil {
		return errTrayClosed
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return errTrayClosed
	}

	s.mu.Lock()
	preserveIDs := sameMenuSource(s.menuSource, menu.Source)
	oldNodes := s.menuByID
	nextID := s.nextMenuID
	s.mu.Unlock()
	used := make(map[*dbusMenuNode]bool)
	byID := make(map[int32]*dbusMenuNode)
	var build func([]TrayMenuItem) ([]*dbusMenuNode, error)
	build = func(items []TrayMenuItem) ([]*dbusMenuNode, error) {
		nodes := make([]*dbusMenuNode, 0, len(items))
		for _, item := range items {
			var previous *dbusMenuNode
			if preserveIDs && item.Source != nil {
				for _, candidate := range oldNodes {
					if used[candidate] || !sameMenuSource(candidate.item.Source, item.Source) {
						continue
					}
					if previous == nil || candidate.id < previous.id {
						previous = candidate
					}
				}
			}
			id := int32(0)
			if previous != nil {
				id = previous.id
				used[previous] = true
			} else {
				if nextID >= math.MaxInt32 {
					return nil, errTrayMenuIDExhausted
				}
				nextID++
				id = int32(nextID)
			}
			copyItem := item
			copyItem.Children = nil
			if item.Shortcut != nil {
				shortcut := *item.Shortcut
				copyItem.Shortcut = &shortcut
			}
			children, err := build(item.Children)
			if err != nil {
				return nil, err
			}
			node := &dbusMenuNode{id: id, item: copyItem, children: children}
			byID[id] = node
			nodes = append(nodes, node)
		}
		return nodes, nil
	}
	rootChildren, err := build(menu.Items)
	if err != nil {
		return err
	}
	root := &dbusMenuNode{children: rootChildren}

	s.mu.Lock()
	s.menuSource = menu.Source
	s.menuRoot = root
	s.menuByID = byID
	s.nextMenuID = nextID
	s.menuRevision++
	revision := s.menuRevision
	visible := s.visible
	conn := s.conn
	s.mu.Unlock()
	if visible && conn != nil {
		if err := conn.Emit(dbusMenuPath, dbusMenuInterface+".LayoutUpdated", revision, int32(0)); err != nil {
			return fmt.Errorf("desktop: emit tray menu LayoutUpdated: %w", err)
		}
	}
	return nil
}

func menuPropertyIncluded(names map[string]bool, name string) bool {
	return len(names) == 0 || names[name]
}

func dbusMenuProperties(item TrayMenuItem, names []string) map[string]dbus.Variant {
	included := make(map[string]bool, len(names))
	for _, name := range names {
		included[name] = true
	}
	values := make(map[string]interface{})
	if item.Text != "" {
		values["label"] = item.Text
	}
	values["enabled"] = item.Enabled
	values["visible"] = true
	switch item.Kind {
	case TrayMenuSeparator:
		values["type"] = "separator"
	case TrayMenuSubmenu:
		values["children-display"] = "submenu"
	default:
		values["toggle-type"] = "checkmark"
		values["toggle-state"] = int32(0)
		if item.Checked {
			values["toggle-state"] = int32(1)
		}
		if item.Shortcut != nil {
			chord := make([]string, 0, 5)
			modifiers := item.Shortcut.Modifiers
			if modifiers&TrayMenuModCtrl != 0 {
				chord = append(chord, "Control")
			}
			if modifiers&TrayMenuModAlt != 0 {
				chord = append(chord, "Alt")
			}
			if modifiers&TrayMenuModShift != 0 {
				chord = append(chord, "Shift")
			}
			if modifiers&TrayMenuModSuper != 0 {
				chord = append(chord, "Super")
			}
			key := item.Shortcut.Key
			if key >= 'a' && key <= 'z' {
				key -= 'a' - 'A'
			}
			chord = append(chord, string(key))
			values["shortcut"] = [][]string{chord}
		}
	}
	properties := make(map[string]dbus.Variant, len(values))
	for name, value := range values {
		if menuPropertyIncluded(included, name) {
			properties[name] = dbus.MakeVariant(value)
		}
	}
	return properties
}

func layoutForMenuNode(node *dbusMenuNode, depth int32, propertyNames []string) dbusMenuLayout {
	layout := dbusMenuLayout{ID: node.id, Children: []dbus.Variant{}}
	if node.id == 0 {
		layout.Properties = map[string]dbus.Variant{}
	} else {
		layout.Properties = dbusMenuProperties(node.item, propertyNames)
	}
	if depth == 0 {
		return layout
	}
	childDepth := depth
	if depth > 0 {
		childDepth--
	}
	layout.Children = make([]dbus.Variant, 0, len(node.children))
	for _, child := range node.children {
		layout.Children = append(layout.Children, dbus.MakeVariant(layoutForMenuNode(child, childDepth, propertyNames)))
	}
	return layout
}

func (s *linuxTrayService) menuNodeLocked(id int32) (*dbusMenuNode, bool) {
	if id == 0 {
		return s.menuRoot, s.menuRoot != nil
	}
	node, ok := s.menuByID[id]
	return node, ok
}

func (m *dbusMenuHandler) GetLayout(parentID, recursionDepth int32, propertyNames []string) (uint32, dbusMenuLayout, *dbus.Error) {
	m.service.mu.Lock()
	defer m.service.mu.Unlock()
	if m.service.closed {
		return 0, dbusMenuLayout{}, dbus.NewError("org.freedesktop.DBus.Error.Disconnected", []interface{}{errTrayClosed.Error()})
	}
	node, ok := m.service.menuNodeLocked(parentID)
	if !ok {
		return m.service.menuRevision, dbusMenuLayout{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []interface{}{fmt.Sprintf("unknown menu item %d", parentID)})
	}
	return m.service.menuRevision, layoutForMenuNode(node, recursionDepth, propertyNames), nil
}

func (m *dbusMenuHandler) GetGroupProperties(ids []int32, propertyNames []string) ([]dbusMenuItemProperties, *dbus.Error) {
	m.service.mu.Lock()
	defer m.service.mu.Unlock()
	if m.service.closed {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.Disconnected", []interface{}{errTrayClosed.Error()})
	}
	if len(ids) == 0 {
		ids = make([]int32, 0, len(m.service.menuByID))
		var appendChildren func([]*dbusMenuNode)
		appendChildren = func(nodes []*dbusMenuNode) {
			for _, node := range nodes {
				ids = append(ids, node.id)
				appendChildren(node.children)
			}
		}
		if m.service.menuRoot != nil {
			appendChildren(m.service.menuRoot.children)
		}
	}
	properties := make([]dbusMenuItemProperties, 0, len(ids))
	for _, id := range ids {
		node, ok := m.service.menuNodeLocked(id)
		if !ok {
			continue
		}
		var itemProperties map[string]dbus.Variant
		if id == 0 {
			itemProperties = map[string]dbus.Variant{}
		} else {
			itemProperties = dbusMenuProperties(node.item, propertyNames)
		}
		properties = append(properties, dbusMenuItemProperties{ID: id, Properties: itemProperties})
	}
	return properties, nil
}

func (m *dbusMenuHandler) Event(id int32, eventID string, _ dbus.Variant, _ uint32) *dbus.Error {
	m.service.mu.Lock()
	defer m.service.mu.Unlock()
	if m.service.closed || eventID != "clicked" || !m.service.visible {
		return nil
	}
	node, ok := m.service.menuNodeLocked(id)
	if !ok || id == 0 || node.item.Kind != TrayMenuAction || !node.item.Enabled || node.item.Activate == nil {
		return nil
	}
	node.item.Activate()
	return nil
}

func (m *dbusMenuHandler) AboutToShow(id int32) (bool, *dbus.Error) {
	m.service.mu.Lock()
	defer m.service.mu.Unlock()
	if m.service.closed {
		return false, nil
	}
	_, ok := m.service.menuNodeLocked(id)
	if !ok {
		return false, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []interface{}{fmt.Sprintf("unknown menu item %d", id)})
	}
	return false, nil
}

type dbusMenuIntrospectable struct{}

func (dbusMenuIntrospectable) Introspect() (string, *dbus.Error) {
	return dbusMenuIntrospection, nil
}

const dbusMenuIntrospection = `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN" "http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">
<node>
  <interface name="com.canonical.dbusmenu">
    <method name="GetLayout"><arg name="parentId" type="i" direction="in"/><arg name="recursionDepth" type="i" direction="in"/><arg name="propertyNames" type="as" direction="in"/><arg name="revision" type="u" direction="out"/><arg name="layout" type="(ia{sv}av)" direction="out"/></method>
    <method name="GetGroupProperties"><arg name="ids" type="ai" direction="in"/><arg name="propertyNames" type="as" direction="in"/><arg name="properties" type="a(ia{sv})" direction="out"/></method>
    <method name="Event"><arg name="id" type="i" direction="in"/><arg name="eventId" type="s" direction="in"/><arg name="data" type="v" direction="in"/><arg name="timestamp" type="u" direction="in"/></method>
    <method name="AboutToShow"><arg name="id" type="i" direction="in"/><arg name="needUpdate" type="b" direction="out"/></method>
    <signal name="LayoutUpdated"><arg name="revision" type="u"/><arg name="parent" type="i"/></signal>
    <signal name="ItemsPropertiesUpdated"><arg name="updatedProps" type="a(ia{sv})"/><arg name="removedProps" type="a(ias)"/></signal>
  </interface>
  <interface name="org.freedesktop.DBus.Introspectable"><method name="Introspect"><arg name="data" type="s" direction="out"/></method></interface>
</node>`
