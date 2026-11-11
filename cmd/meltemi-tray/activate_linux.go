//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const trayDBusTimeout = 3 * time.Second

type nativeMenuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

type nativeMenuItemProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}

func verifyMenuOnlySNI(ctx context.Context, conn *dbus.Conn, destination string) error {
	object := conn.Object(destination, dbus.ObjectPath("/StatusNotifierItem"))
	var document string
	if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&document); err != nil {
		return fmt.Errorf("SNI Introspect: %w", err)
	}
	for _, iface := range []string{"org.freedesktop.StatusNotifierItem", "org.kde.StatusNotifierItem"} {
		var itemIsMenu dbus.Variant
		if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, iface, "ItemIsMenu").Store(&itemIsMenu); err != nil {
			return fmt.Errorf("%s Get ItemIsMenu: %w", iface, err)
		}
		if itemIsMenu.Value() != true {
			return fmt.Errorf("%s Get ItemIsMenu = %v, want true", iface, itemIsMenu.Value())
		}
		var properties map[string]dbus.Variant
		if err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, iface).Store(&properties); err != nil {
			return fmt.Errorf("%s GetAll: %w", iface, err)
		}
		if properties["ItemIsMenu"].Value() != true {
			return fmt.Errorf("%s GetAll ItemIsMenu = %v, want true", iface, properties["ItemIsMenu"].Value())
		}
		start := strings.Index(document, `<interface name="`+iface+`">`)
		if start < 0 {
			return fmt.Errorf("SNI introspection omits %s", iface)
		}
		block := document[start:]
		end := strings.Index(block, "</interface>")
		if end < 0 {
			return fmt.Errorf("SNI introspection has an unterminated %s interface", iface)
		}
		block = block[:end]
		if !strings.Contains(block, `<property name="ItemIsMenu" type="b" access="read"/>`) {
			return fmt.Errorf("%s introspection does not declare ItemIsMenu as a readable bool", iface)
		}
	}
	return nil
}

func activateRestore() error {
	ctx, cancel := context.WithTimeout(context.Background(), trayDBusTimeout)
	defer cancel()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect session bus: %w", err)
	}
	defer conn.Close()
	var names []string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return fmt.Errorf("ListNames: %w", err)
	}
	prefix := fmt.Sprintf("org.freedesktop.StatusNotifierItem-%d-", os.Getpid())
	destination := ""
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			if destination != "" {
				return fmt.Errorf("multiple demo SNI names match %q", prefix)
			}
			destination = name
		}
	}
	if destination == "" {
		return fmt.Errorf("no registered SNI name matches %q", prefix)
	}
	if err := verifyMenuOnlySNI(ctx, conn, destination); err != nil {
		return err
	}
	object := conn.Object(destination, dbus.ObjectPath("/MenuBar"))
	var revision uint32
	var layout nativeMenuLayout
	if err := object.CallWithContext(ctx, "com.canonical.dbusmenu.GetLayout", 0, int32(0), int32(0), []string{}).Store(&revision, &layout); err != nil {
		return fmt.Errorf("GetLayout: %w", err)
	}
	if revision == 0 || layout.ID != 0 {
		return fmt.Errorf("GetLayout returned revision %d root %d, want nonzero/0", revision, layout.ID)
	}
	var properties []nativeMenuItemProperties
	if err := object.CallWithContext(ctx, "com.canonical.dbusmenu.GetGroupProperties", 0, []int32{1}, []string{"label", "enabled"}).Store(&properties); err != nil {
		return fmt.Errorf("GetGroupProperties: %w", err)
	}
	if len(properties) != 1 || properties[0].ID != 1 || properties[0].Properties["label"].Value() != "Restore window" || properties[0].Properties["enabled"].Value() != true {
		return fmt.Errorf("restore item properties = %+v, want enabled Restore window at stable ID 1", properties)
	}
	if err := object.CallWithContext(ctx, "com.canonical.dbusmenu.Event", 0, int32(1), "clicked", dbus.MakeVariant(uint32(0)), uint32(1)).Err; err != nil {
		return fmt.Errorf("Event clicked: %w", err)
	}
	return nil
}
