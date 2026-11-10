//go:build linux

package desktop

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type trayBusCall struct {
	destination string
	path        dbus.ObjectPath
	method      string
	args        []interface{}
	hasDeadline bool
}

type fakeTrayBusObject struct {
	dbus.BusObject
	bus         *fakeTrayBus
	destination string
	path        dbus.ObjectPath
}

func (o *fakeTrayBusObject) CallWithContext(ctx context.Context, method string, _ dbus.Flags, args ...interface{}) *dbus.Call {
	_, hasDeadline := ctx.Deadline()
	o.bus.mu.Lock()
	o.bus.calls = append(o.bus.calls, trayBusCall{
		destination: o.destination,
		path:        o.path,
		method:      method,
		args:        append([]interface{}(nil), args...),
		hasDeadline: hasDeadline,
	})
	ownerErr := o.bus.ownerErr
	watcherOwner := o.bus.watcherOwner
	registerErr := o.bus.registerErr
	onRegister := o.bus.onRegister
	o.bus.mu.Unlock()

	switch method {
	case "org.freedesktop.DBus.GetNameOwner":
		if ownerErr != nil {
			return &dbus.Call{Err: ownerErr}
		}
		if watcherOwner == "" {
			return &dbus.Call{Err: errors.New("org.freedesktop.DBus.Error.NameHasNoOwner")}
		}
		return &dbus.Call{Body: []interface{}{watcherOwner}}
	case "org.freedesktop.DBus.RequestName":
		reply, err := o.bus.requestName(ctx, args[0].(string), args[1].(dbus.RequestNameFlags))
		return &dbus.Call{Body: []interface{}{uint32(reply)}, Err: err}
	case "org.freedesktop.DBus.ReleaseName":
		reply, err := o.bus.releaseName(ctx, args[0].(string))
		return &dbus.Call{Body: []interface{}{uint32(reply)}, Err: err}
	case "org.kde.StatusNotifierWatcher.RegisterStatusNotifierItem":
		if onRegister != nil {
			onRegister()
		}
		if registerErr != nil {
			return &dbus.Call{Err: registerErr}
		}
		return &dbus.Call{}
	default:
		return &dbus.Call{Err: fmt.Errorf("unexpected D-Bus method %s", method)}
	}
}

type trayExport struct {
	value interface{}
	path  dbus.ObjectPath
	iface string
}

type traySignal struct {
	path dbus.ObjectPath
	name string
	body []interface{}
}

type fakeTrayBus struct {
	mu sync.Mutex

	addMatchCalls    int
	removeMatchCalls int
	signalChannel    chan<- *dbus.Signal

	watcherOwner   string
	ownerErr       error
	registerErr    error
	requestErr     error
	requestReply   dbus.RequestNameReply
	releaseErr     error
	exportErr      map[string]error
	unexportErr    map[string]error
	emitErr        error
	emitErrAt      map[int]error
	onRegister     func()
	blockRequest   bool
	blockRelease   bool
	requestStarted chan struct{}
	releaseStarted chan struct{}
	requestOnce    sync.Once
	releaseOnce    sync.Once

	calls         []trayBusCall
	requested     []string
	requestFlags  []dbus.RequestNameFlags
	released      []string
	exports       []trayExport
	signals       []traySignal
	closeCalls    int
	watcherObject *fakeTrayBusObject
	daemonObject  *fakeTrayBusObject
}

func newFakeTrayBus() *fakeTrayBus {
	bus := &fakeTrayBus{
		watcherOwner: ":1.99",
		requestReply: dbus.RequestNameReplyPrimaryOwner,
		exportErr:    make(map[string]error),
		unexportErr:  make(map[string]error),
		emitErrAt:    make(map[int]error),
	}
	bus.watcherObject = &fakeTrayBusObject{bus: bus, destination: statusNotifierWatcherName}
	bus.daemonObject = &fakeTrayBusObject{bus: bus, destination: "org.freedesktop.DBus", path: "/org/freedesktop/DBus"}
	return bus
}

func (b *fakeTrayBus) Object(destination string, path dbus.ObjectPath) dbus.BusObject {
	b.watcherObject.path = path
	if destination == statusNotifierWatcherName {
		return b.watcherObject
	}
	return &fakeTrayBusObject{bus: b, destination: destination, path: path}
}

func (b *fakeTrayBus) BusObject() dbus.BusObject { return b.daemonObject }

func (b *fakeTrayBus) RequestName(name string, flags dbus.RequestNameFlags) (dbus.RequestNameReply, error) {
	return b.requestName(context.Background(), name, flags)
}

func (b *fakeTrayBus) requestName(ctx context.Context, name string, flags dbus.RequestNameFlags) (dbus.RequestNameReply, error) {
	b.mu.Lock()
	b.requested = append(b.requested, name)
	b.requestFlags = append(b.requestFlags, flags)
	block := b.blockRequest
	started := b.requestStarted
	err := b.requestErr
	reply := b.requestReply
	b.mu.Unlock()
	if block {
		b.requestOnce.Do(func() {
			if started != nil {
				close(started)
			}
		})
		<-ctx.Done()
		return 0, ctx.Err()
	}
	if err != nil {
		return 0, err
	}
	return reply, nil
}

func (b *fakeTrayBus) ReleaseName(name string) (dbus.ReleaseNameReply, error) {
	return b.releaseName(context.Background(), name)
}

func (b *fakeTrayBus) releaseName(ctx context.Context, name string) (dbus.ReleaseNameReply, error) {
	b.mu.Lock()
	b.released = append(b.released, name)
	block := b.blockRelease
	started := b.releaseStarted
	err := b.releaseErr
	b.mu.Unlock()
	if block {
		b.releaseOnce.Do(func() {
			if started != nil {
				close(started)
			}
		})
		<-ctx.Done()
		return 0, ctx.Err()
	}
	if err != nil {
		return 0, err
	}
	return dbus.ReleaseNameReplyReleased, nil
}

func (b *fakeTrayBus) Export(value interface{}, path dbus.ObjectPath, iface string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.exports = append(b.exports, trayExport{value: value, path: path, iface: iface})
	if value == nil && b.unexportErr[iface] != nil {
		return b.unexportErr[iface]
	}
	if value != nil && b.exportErr[iface] != nil {
		return b.exportErr[iface]
	}
	return nil
}

func (b *fakeTrayBus) Emit(path dbus.ObjectPath, name string, values ...interface{}) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	index := len(b.signals)
	b.signals = append(b.signals, traySignal{path: path, name: name, body: append([]interface{}(nil), values...)})
	if err := b.emitErrAt[index]; err != nil {
		return err
	}
	return b.emitErr
}

func (b *fakeTrayBus) AddMatchSignalContext(context.Context, ...dbus.MatchOption) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addMatchCalls++
	return nil
}

func (b *fakeTrayBus) RemoveMatchSignalContext(context.Context, ...dbus.MatchOption) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removeMatchCalls++
	return nil
}

func (b *fakeTrayBus) Signal(channel chan<- *dbus.Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.signalChannel = channel
}

func (b *fakeTrayBus) RemoveSignal(channel chan<- *dbus.Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.signalChannel == channel {
		b.signalChannel = nil
	}
}

func (b *fakeTrayBus) Names() []string { return []string{b.watcherOwner} }

func (b *fakeTrayBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeCalls++
	return nil
}

func (b *fakeTrayBus) registerCallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, call := range b.calls {
		if call.method == statusNotifierWatcherIface+".RegisterStatusNotifierItem" {
			count++
		}
	}
	return count
}

func TestTrayIconEncoding(t *testing.T) {
	icon := image.NewNRGBA(image.Rect(5, 7, 7, 9))
	colors := []color.NRGBA{
		{R: 1, G: 2, B: 3, A: 4},
		{R: 10, G: 20, B: 30, A: 255},
		{R: 100, G: 150, B: 200, A: 128},
		{R: 0, G: 0, B: 0, A: 0},
	}
	for i, value := range colors {
		icon.SetNRGBA(5+i%2, 7+i/2, value)
	}

	encoded, err := EncodeIcon(icon)
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	if encoded.Width != 2 || encoded.Height != 2 {
		t.Errorf("dimensions = %dx%d, want 2x2", encoded.Width, encoded.Height)
	}
	want := []byte{
		4, 1, 2, 3,
		255, 10, 20, 30,
		128, 100, 150, 200,
		0, 0, 0, 0,
	}
	if string(encoded.Pixels) != string(want) {
		t.Errorf("packed ARGB = %v, want %v", encoded.Pixels, want)
	}
	icon.SetNRGBA(5, 7, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	if encoded.Pixels[0] != 4 || encoded.Pixels[1] != 1 {
		t.Error("encoded pixels alias the source image")
	}

	if _, err := EncodeIcon(nil); err == nil {
		t.Error("EncodeIcon(nil) succeeded")
	}
	if _, err := EncodeIcon(image.NewNRGBA(image.Rect(2, 2, 2, 3))); err == nil {
		t.Error("EncodeIcon with zero width succeeded")
	}
}

func TestTrayRegistrationAndProperties(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	if !service.Capabilities().Tray {
		t.Fatal("Capabilities.Tray = false with a watcher owner")
	}
	if err := service.Show(TrayConfig{Icon: icon, Tooltip: "hello"}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(bus.requested) != 1 {
		t.Fatalf("RequestName calls = %d, want 1", len(bus.requested))
	}
	name := bus.requested[0]
	if !strings.HasPrefix(name, "org.freedesktop.StatusNotifierItem-") {
		t.Errorf("service name = %q, want unique SNI name", name)
	}
	if bus.requestFlags[0] != dbus.NameFlagDoNotQueue {
		t.Errorf("RequestName flags = %v, want DoNotQueue without replacement", bus.requestFlags[0])
	}
	if len(bus.calls) == 0 {
		t.Fatal("no watcher calls")
	}
	lastCall := bus.calls[len(bus.calls)-1]
	if lastCall.destination != "org.kde.StatusNotifierWatcher" || lastCall.path != "/StatusNotifierWatcher" {
		t.Errorf("watcher destination/path = %s %s, want org.kde.StatusNotifierWatcher /StatusNotifierWatcher", lastCall.destination, lastCall.path)
	}
	if lastCall.method != "org.kde.StatusNotifierWatcher.RegisterStatusNotifierItem" || len(lastCall.args) != 1 || lastCall.args[0] != name {
		t.Errorf("registration call = %+v, want unique service name", lastCall)
	}

	properties := exportedProperties(t, bus)
	assertProperty(t, properties, "Category", "ApplicationStatus")
	assertProperty(t, properties, "Status", "Active")
	assertProperty(t, properties, "IconName", "")
	pixmaps := propertyValue(t, properties, "IconPixmap").([]sniIconPixmap)
	if len(pixmaps) != 1 || pixmaps[0].Width != 1 || pixmaps[0].Height != 1 || len(pixmaps[0].Data) != 4 {
		t.Errorf("IconPixmap = %+v, want one 1x1 ARGB pixel", pixmaps)
	}
	tooltip := propertyValue(t, properties, "ToolTip").(sniToolTip)
	if tooltip.Title != "hello" || tooltip.SubTitle != "" || tooltip.IconName != "" || len(tooltip.IconPixmap) != 0 {
		t.Errorf("ToolTip = %+v, want title-only encoding", tooltip)
	}

	updated, err := EncodeIcon(image.NewNRGBA(image.Rect(8, 9, 10, 10)))
	if err != nil {
		t.Fatalf("EncodeIcon update: %v", err)
	}
	if err := service.Update(TrayConfig{Icon: updated, Tooltip: ""}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	wantSignals := []string{
		statusNotifierItemInterface + ".NewIcon",
		"org.kde.StatusNotifierItem.NewIcon",
		statusNotifierItemInterface + ".NewAttentionIcon",
		"org.kde.StatusNotifierItem.NewAttentionIcon",
		statusNotifierItemInterface + ".NewToolTip",
		"org.kde.StatusNotifierItem.NewToolTip",
	}
	if len(bus.signals) != len(wantSignals) {
		t.Fatalf("signals = %d, want both interfaces for all three updates", len(bus.signals))
	}
	for i, want := range wantSignals {
		if bus.signals[i].name != want {
			t.Errorf("signal %d = %q, want %q", i, bus.signals[i].name, want)
		}
	}
	tooltip = propertyValue(t, properties, "ToolTip").(sniToolTip)
	if tooltip.Title != "" {
		t.Errorf("empty ToolTip title = %q, want empty", tooltip.Title)
	}

	if err := service.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := service.Hide(); err != nil {
		t.Fatalf("repeated Hide: %v", err)
	}
	if len(bus.released) != 1 || bus.released[0] != name {
		t.Errorf("released names = %v, want [%s]", bus.released, name)
	}
	assertProperty(t, properties, "Status", "Passive")
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if bus.closeCalls != 1 {
		t.Errorf("Close calls = %d, want 1", bus.closeCalls)
	}

	secondBus := newFakeTrayBus()
	second := newTrayServiceWithConn(secondBus)
	if err := second.Show(TrayConfig{Icon: icon}); err != nil {
		t.Fatalf("second Show: %v", err)
	}
	if secondBus.requested[0] == name {
		t.Errorf("two services both requested fixed name %q", name)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTrayCompatibilityInterface(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	if err := service.Show(TrayConfig{Icon: icon, Tooltip: "compatible"}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	properties := exportedProperties(t, bus)
	getAll := properties.(interface {
		GetAll(string) (map[string]dbus.Variant, *dbus.Error)
	})
	compatibility := []struct {
		iface       string
		windowID    interface{}
		windowIDSig string
	}{
		{iface: statusNotifierItemInterface, windowID: uint32(0), windowIDSig: "u"},
		{iface: "org.kde.StatusNotifierItem", windowID: int32(0), windowIDSig: "i"},
	}
	for _, test := range compatibility {
		iface := test.iface
		var value interface{}
		for _, export := range bus.exports {
			if export.iface == iface && export.value != nil {
				value = export.value
			}
		}
		activator, ok := value.(interface {
			Activate(int32, int32) *dbus.Error
		})
		if !ok {
			t.Errorf("%s methods were not exported", iface)
		} else if dbusErr := activator.Activate(1, 2); dbusErr != nil {
			t.Errorf("%s Activate: %v", iface, dbusErr)
		}
		category, dbusErr := properties.Get(iface, "Category")
		if dbusErr != nil || category.Value() != "ApplicationStatus" {
			t.Errorf("%s Category = %v, %v", iface, category.Value(), dbusErr)
		}
		windowID, windowIDErr := properties.Get(iface, "WindowId")
		if windowIDErr != nil {
			t.Errorf("%s Get WindowId: %v", iface, windowIDErr)
		} else if got := windowID.Value(); got != test.windowID {
			t.Errorf("%s Get WindowId = %T(%v), want %T(%v)", iface, got, got, test.windowID, test.windowID)
		}
		itemIsMenu, itemIsMenuErr := properties.Get(iface, "ItemIsMenu")
		if itemIsMenuErr != nil || itemIsMenu.Value() != true {
			t.Errorf("%s Get ItemIsMenu = %v, %v, want true for the menu-only Tray API", iface, itemIsMenu.Value(), itemIsMenuErr)
		}
		all, dbusErr := getAll.GetAll(iface)
		if dbusErr != nil || all["ToolTip"].Value().(sniToolTip).Title != "compatible" {
			t.Errorf("%s GetAll ToolTip = %v, %v", iface, all["ToolTip"], dbusErr)
		}
		if dbusErr == nil {
			if got := all["WindowId"].Value(); got != test.windowID {
				t.Errorf("%s GetAll WindowId = %T(%v), want %T(%v)", iface, got, got, test.windowID, test.windowID)
			}
			if got := all["ItemIsMenu"].Value(); got != true {
				t.Errorf("%s GetAll ItemIsMenu = %v, want true for the menu-only Tray API", iface, got)
			}
		}
	}

	var introspecter interface {
		Introspect() (string, *dbus.Error)
	}
	for _, export := range bus.exports {
		if export.path == statusNotifierItemPath && export.iface == dbusIntrospectInterface && export.value != nil {
			introspecter = export.value.(interface {
				Introspect() (string, *dbus.Error)
			})
		}
	}
	if introspecter == nil {
		t.Fatal("Introspectable was not exported")
	}
	document, dbusErr := introspecter.Introspect()
	if dbusErr != nil {
		t.Fatalf("Introspect: %v", dbusErr)
	}
	for _, test := range compatibility {
		start := strings.Index(document, `<interface name="`+test.iface+`">`)
		if start < 0 {
			t.Errorf("introspection omits %s", test.iface)
			continue
		}
		block := document[start:]
		if end := strings.Index(block, "</interface>"); end >= 0 {
			block = block[:end]
		}
		signature := `<property name="WindowId" type="` + test.windowIDSig + `" access="read"/>`
		if !strings.Contains(block, signature) {
			t.Errorf("%s introspection WindowId signature = %q, want %q", test.iface, block, signature)
		}
		const itemIsMenuSignature = `<property name="ItemIsMenu" type="b" access="read"/>`
		if !strings.Contains(block, itemIsMenuSignature) {
			t.Errorf("%s introspection does not declare ItemIsMenu as a readable bool", test.iface)
		}
	}

	if err := service.Update(TrayConfig{Icon: icon, Tooltip: "updated"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	wantSignals := []string{
		statusNotifierItemInterface + ".NewIcon",
		"org.kde.StatusNotifierItem.NewIcon",
		statusNotifierItemInterface + ".NewAttentionIcon",
		"org.kde.StatusNotifierItem.NewAttentionIcon",
		statusNotifierItemInterface + ".NewToolTip",
		"org.kde.StatusNotifierItem.NewToolTip",
	}
	if len(bus.signals) != len(wantSignals) {
		t.Fatalf("signals = %+v, want both interfaces", bus.signals)
	}
	for i, want := range wantSignals {
		if bus.signals[i].name != want {
			t.Errorf("signal %d = %q, want %q", i, bus.signals[i].name, want)
		}
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTrayIDStableAcrossSessions(t *testing.T) {
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	config := TrayConfig{Icon: icon, Tooltip: "stable"}
	firstBus := newFakeTrayBus()
	first := newTrayServiceWithConn(firstBus)
	if err := first.Show(config); err != nil {
		t.Fatalf("first Show: %v", err)
	}
	firstID := propertyValue(t, exportedProperties(t, firstBus), "Id").(string)
	firstName := firstBus.requested[0]
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	secondBus := newFakeTrayBus()
	second := newTrayServiceWithConn(secondBus)
	if err := second.Show(config); err != nil {
		t.Fatalf("second Show: %v", err)
	}
	secondID := propertyValue(t, exportedProperties(t, secondBus), "Id").(string)
	if firstID != secondID {
		t.Errorf("SNI Id changed across services = %q / %q", firstID, secondID)
	}
	if firstID == firstName || secondID == secondBus.requested[0] {
		t.Errorf("SNI Id tracks per-session bus names: %q / %q", firstID, secondID)
	}
	if !strings.HasPrefix(secondID, "meltemi-") {
		t.Errorf("SNI Id = %q, want deterministic Meltemi identity", secondID)
	}
	if err := second.Update(TrayConfig{Icon: icon, Tooltip: "changed"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := propertyValue(t, exportedProperties(t, secondBus), "Id").(string); got != secondID {
		t.Errorf("SNI Id after Update = %q, want stable %q", got, secondID)
	}
	if err := second.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := second.Show(config); err != nil {
		t.Fatalf("re-show: %v", err)
	}
	if got := propertyValue(t, exportedProperties(t, secondBus), "Id").(string); got != secondID {
		t.Errorf("SNI Id after re-show = %q, want stable %q", got, secondID)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTrayDeterministicIDConfigurationLimitations(t *testing.T) {
	config := TrayConfig{
		Icon:    Icon{Width: 1, Height: 1, Pixels: []byte{4, 3, 2, 1}},
		Tooltip: "same initial configuration",
	}
	first := deterministicTrayID(config)
	second := deterministicTrayID(config)
	if first != second {
		t.Errorf("identical initial configurations produced different IDs %q and %q", first, second)
	}

	changedTooltip := config
	changedTooltip.Tooltip = "different initial configuration"
	if changed := deterministicTrayID(changedTooltip); changed == first {
		t.Errorf("changed initial tooltip retained ID %q, documenting a collision rather than the expected configuration sensitivity", changed)
	}
	changedIcon := config
	changedIcon.Icon.Pixels = []byte{4, 3, 2, 9}
	if changed := deterministicTrayID(changedIcon); changed == first {
		t.Errorf("changed initial icon retained ID %q, documenting a collision rather than the expected configuration sensitivity", changed)
	}
}

func TestTrayUnavailable(t *testing.T) {
	bus := newFakeTrayBus()
	bus.watcherOwner = ""
	service := newTrayServiceWithConn(bus)
	if service.Capabilities().Tray {
		t.Error("Capabilities.Tray = true without a watcher owner")
	}
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	if err := service.Show(TrayConfig{Icon: icon}); !errors.Is(err, ErrTrayUnavailable) {
		t.Fatalf("Show error = %v, want ErrTrayUnavailable", err)
	}
	if len(bus.requested) != 0 {
		t.Errorf("RequestName calls without watcher = %d, want 0", len(bus.requested))
	}
}

func TestTrayRegistrationAllowsSynchronousPropertyRead(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	type readResult struct {
		status  string
		tooltip string
		err     error
	}
	readDone := make(chan readResult, 1)
	bus.onRegister = func() {
		var properties interface {
			Get(string, string) (dbus.Variant, *dbus.Error)
		}
		for _, export := range bus.exports {
			if export.iface == dbusPropertiesInterface && export.value != nil {
				properties = export.value.(interface {
					Get(string, string) (dbus.Variant, *dbus.Error)
				})
			}
		}
		if properties == nil {
			readDone <- readResult{err: errors.New("properties not exported before registration")}
			return
		}
		status, statusErr := properties.Get(statusNotifierItemInterface, "Status")
		if statusErr != nil {
			readDone <- readResult{err: statusErr}
			return
		}
		tooltip, tooltipErr := properties.Get(statusNotifierItemInterface, "ToolTip")
		if tooltipErr != nil {
			readDone <- readResult{err: tooltipErr}
			return
		}
		readDone <- readResult{
			status:  status.Value().(string),
			tooltip: tooltip.Value().(sniToolTip).Title,
		}
	}
	showDone := make(chan error, 1)
	go func() { showDone <- service.Show(TrayConfig{Icon: icon, Tooltip: "reentrant"}) }()
	select {
	case result := <-readDone:
		if result.err != nil {
			t.Errorf("synchronous property read: %v", result.err)
		}
		if result.status != "Active" || result.tooltip != "reentrant" {
			t.Errorf("registration properties = Status %q ToolTip %q, want Active/reentrant", result.status, result.tooltip)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("watcher property read deadlocked during RegisterStatusNotifierItem")
	}
	select {
	case err := <-showDone:
		if err != nil {
			t.Fatalf("Show: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Show did not return after synchronous watcher read")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTrayShowBoundsBlockedRequestName(t *testing.T) {
	bus := newFakeTrayBus()
	bus.blockRequest = true
	bus.requestStarted = make(chan struct{})
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	showDone := make(chan error, 1)
	go func() { showDone <- service.Show(TrayConfig{Icon: icon}) }()
	<-bus.requestStarted
	select {
	case err := <-showDone:
		if err == nil {
			t.Fatal("Show succeeded with blocked RequestName")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Show remained blocked in RequestName beyond its context deadline")
	}
	foundDeadline := false
	for _, call := range bus.calls {
		if call.method == "org.freedesktop.DBus.RequestName" && call.hasDeadline {
			foundDeadline = true
		}
	}
	if !foundDeadline {
		t.Error("RequestName was not issued through CallWithContext with a deadline")
	}
}

func TestTrayCloseBoundsBlockedReleaseName(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	if err := service.Show(TrayConfig{Icon: icon}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	bus.blockRelease = true
	bus.releaseStarted = make(chan struct{})
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close() }()
	<-bus.releaseStarted
	select {
	case err := <-closeDone:
		if err == nil {
			t.Fatal("Close succeeded with blocked ReleaseName")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close remained blocked in ReleaseName beyond its context deadline")
	}
	foundDeadline := false
	for _, call := range bus.calls {
		if call.method == "org.freedesktop.DBus.ReleaseName" && call.hasDeadline {
			foundDeadline = true
		}
	}
	if !foundDeadline {
		t.Error("ReleaseName was not issued through CallWithContext with a deadline")
	}
	if bus.closeCalls != 1 {
		t.Errorf("connection Close calls = %d, want 1 after name timeout", bus.closeCalls)
	}
}

func TestTrayUpdateSignalFailureKeepsNewConfig(t *testing.T) {
	for failureIndex := 0; failureIndex < 6; failureIndex++ {
		t.Run(fmt.Sprintf("signal_%d", failureIndex), func(t *testing.T) {
			bus := newFakeTrayBus()
			service := newTrayServiceWithConn(bus)
			oldIcon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
			if err != nil {
				t.Fatalf("EncodeIcon old: %v", err)
			}
			if err := service.Show(TrayConfig{Icon: oldIcon, Tooltip: "old"}); err != nil {
				t.Fatalf("Show: %v", err)
			}
			newImage := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			newImage.SetNRGBA(0, 0, color.NRGBA{R: 9, G: 8, B: 7, A: 6})
			newIcon, err := EncodeIcon(newImage)
			if err != nil {
				t.Fatalf("EncodeIcon new: %v", err)
			}
			emissionErr := errors.New("host unavailable")
			bus.emitErrAt[failureIndex] = emissionErr
			if err := service.Update(TrayConfig{Icon: newIcon, Tooltip: "new"}); !errors.Is(err, emissionErr) {
				t.Fatalf("Update error = %v, want emission failure", err)
			}
			if len(bus.signals) != 6 {
				t.Errorf("emission attempts = %d, want all 6 despite failure", len(bus.signals))
			}
			properties := exportedProperties(t, bus)
			for _, iface := range []string{statusNotifierItemInterface, statusNotifierItemCompatIface} {
				tooltip, dbusErr := properties.Get(iface, "ToolTip")
				if dbusErr != nil || tooltip.Value().(sniToolTip).Title != "new" {
					t.Errorf("%s ToolTip = %v, %v, want new", iface, tooltip.Value(), dbusErr)
				}
				pixmap, dbusErr := properties.Get(iface, "IconPixmap")
				if dbusErr != nil {
					t.Errorf("%s IconPixmap: %v", iface, dbusErr)
				} else {
					data := pixmap.Value().([]sniIconPixmap)[0].Data
					if string(data) != string([]byte{6, 9, 8, 7}) {
						t.Errorf("%s IconPixmap = %v, want new ARGB", iface, data)
					}
				}
			}
			if err := service.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

func TestTrayHideUnexportFailureRetry(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	config := TrayConfig{Icon: icon, Tooltip: "retry"}
	if err := service.Show(config); err != nil {
		t.Fatalf("Show: %v", err)
	}
	properties := exportedProperties(t, bus)
	bus.unexportErr[statusNotifierItemInterface] = errors.New("unexport blocked")
	if err := service.Hide(); err == nil {
		t.Fatal("Hide succeeded, want unexport failure")
	}
	assertProperty(t, properties, "Status", "Active")
	if got := service.(*linuxTrayService).currentName(); got != bus.requested[0] {
		t.Errorf("service name after failed Hide = %q, want retained %q", got, bus.requested[0])
	}
	if len(bus.released) != 0 {
		t.Errorf("ReleaseName calls after failed unexport = %d, want 0", len(bus.released))
	}
	if err := service.Show(config); err != nil {
		t.Fatalf("Show during failed Hide: %v", err)
	}
	if len(bus.requested) != 1 {
		t.Errorf("RequestName calls during failed Hide = %d, want no duplicate registration", len(bus.requested))
	}

	delete(bus.unexportErr, statusNotifierItemInterface)
	if err := service.Hide(); err != nil {
		t.Fatalf("Hide retry: %v", err)
	}
	assertProperty(t, properties, "Status", "Passive")
	if len(bus.released) != 1 {
		t.Errorf("ReleaseName calls after retry = %d, want 1", len(bus.released))
	}
	unexportCalls := 0
	for _, export := range bus.exports {
		if export.iface == statusNotifierItemInterface && export.value == nil {
			unexportCalls++
		}
	}
	if unexportCalls != 2 {
		t.Errorf("failed interface unexport attempts = %d, want initial + retry", unexportCalls)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTrayHideReleaseFailureRetry(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	config := TrayConfig{Icon: icon}
	if err := service.Show(config); err != nil {
		t.Fatalf("Show: %v", err)
	}
	properties := exportedProperties(t, bus)
	bus.releaseErr = errors.New("release blocked")
	if err := service.Hide(); err == nil {
		t.Fatal("Hide succeeded, want release failure")
	}
	assertProperty(t, properties, "Status", "Active")
	if got := service.(*linuxTrayService).currentName(); got != bus.requested[0] {
		t.Errorf("service name after failed Hide = %q, want retained %q", got, bus.requested[0])
	}
	if err := service.Show(config); err != nil {
		t.Fatalf("Show during failed Hide: %v", err)
	}
	if len(bus.requested) != 1 {
		t.Errorf("RequestName calls during failed release = %d, want no duplicate registration", len(bus.requested))
	}

	bus.releaseErr = nil
	if err := service.Hide(); err != nil {
		t.Fatalf("Hide retry: %v", err)
	}
	assertProperty(t, properties, "Status", "Passive")
	if len(bus.released) != 2 {
		t.Errorf("ReleaseName attempts = %d, want initial + retry", len(bus.released))
	}
	if len(bus.requested) != 1 {
		t.Errorf("RequestName calls after retry = %d, want 1", len(bus.requested))
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTrayRegistrationRollback(t *testing.T) {
	bus := newFakeTrayBus()
	bus.registerErr = errors.New("registration rejected")
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	if err := service.Show(TrayConfig{Icon: icon}); !errors.Is(err, ErrTrayUnavailable) {
		t.Fatalf("Show error = %v, want ErrTrayUnavailable", err)
	}
	if len(bus.released) != 1 || bus.released[0] != bus.requested[0] {
		t.Errorf("rollback releases = %v, requested = %v", bus.released, bus.requested)
	}
	for _, iface := range []string{statusNotifierItemInterface, "org.freedesktop.DBus.Properties"} {
		found := false
		for _, export := range bus.exports {
			if export.iface == iface && export.value == nil {
				found = true
			}
		}
		if !found {
			t.Errorf("rollback did not unexport %s", iface)
		}
	}

	bus.registerErr = nil
	if err := service.Show(TrayConfig{Icon: icon}); err != nil {
		t.Fatalf("Show after rollback: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTrayRequestNameCollision(t *testing.T) {
	bus := newFakeTrayBus()
	bus.requestReply = dbus.RequestNameReplyExists
	service := newTrayServiceWithConn(bus)
	icon, err := EncodeIcon(image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatalf("EncodeIcon: %v", err)
	}
	if err := service.Show(TrayConfig{Icon: icon}); !errors.Is(err, ErrTrayUnavailable) {
		t.Fatalf("Show error = %v, want ErrTrayUnavailable", err)
	}
	if len(bus.released) != 0 {
		t.Errorf("ReleaseName calls for unowned collision = %d, want 0", len(bus.released))
	}
	if len(bus.exports) != 0 {
		t.Errorf("Export calls for unowned collision = %d, want 0", len(bus.exports))
	}
}

func exportedProperties(t *testing.T, bus *fakeTrayBus) interface {
	Get(string, string) (dbus.Variant, *dbus.Error)
} {
	t.Helper()
	for i := len(bus.exports) - 1; i >= 0; i-- {
		export := bus.exports[i]
		if export.iface != "org.freedesktop.DBus.Properties" || export.value == nil {
			continue
		}
		properties, ok := export.value.(interface {
			Get(string, string) (dbus.Variant, *dbus.Error)
		})
		if !ok {
			t.Fatalf("exported properties object %T has no Get", export.value)
		}
		return properties
	}
	t.Fatal("org.freedesktop.DBus.Properties was not exported")
	return nil
}

func propertyValue(t *testing.T, properties interface {
	Get(string, string) (dbus.Variant, *dbus.Error)
}, name string) interface{} {
	t.Helper()
	value, dbusErr := properties.Get(statusNotifierItemInterface, name)
	if dbusErr != nil {
		t.Fatalf("Get(%s): %v", name, dbusErr)
	}
	return value.Value()
}

func TestDBusMenuIntrospectionWireSignatures(t *testing.T) {
	bus := newFakeTrayBus()
	service := newTrayServiceWithConn(bus)
	if err := service.SetMenu(TrayMenu{}); err != nil {
		t.Fatalf("SetMenu: %v", err)
	}
	if err := service.Show(TrayConfig{Icon: testTrayIcon(t)}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	defer service.Close()
	introspecter := exportAt(t, bus, dbusMenuPath, dbusIntrospectInterface).(interface {
		Introspect() (string, *dbus.Error)
	})
	document, dbusErr := introspecter.Introspect()
	if dbusErr != nil {
		t.Fatalf("Introspect: %v", dbusErr)
	}
	checks := []string{
		`<arg name="properties" type="a(ia{sv})" direction="out"/>`,
		`<signal name="LayoutUpdated"><arg name="revision" type="u"/><arg name="parent" type="i"/></signal>`,
		`<signal name="ItemsPropertiesUpdated"><arg name="updatedProps" type="a(ia{sv})"/><arg name="removedProps" type="a(ias)"/></signal>`,
	}
	for _, check := range checks {
		if !strings.Contains(document, check) {
			t.Errorf("dbusmenu introspection lacks canonical wire fragment %s", check)
		}
	}
}

func TestTrayWatcherSignalChannelCloseTerminates(t *testing.T) {
	service := &linuxTrayService{}
	channel := make(chan *dbus.Signal)
	stop := make(chan struct{})
	close(channel)
	done := make(chan struct{})
	go func() {
		service.watchWatcher(channel, stop, 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		close(stop)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("watchWatcher did not stop even after stop was closed")
		}
		t.Fatal("watchWatcher spun on a closed signal channel instead of returning")
	}
}

func assertProperty(t *testing.T, properties interface {
	Get(string, string) (dbus.Variant, *dbus.Error)
}, name string, want interface{}) {
	t.Helper()
	if got := propertyValue(t, properties, name); got != want {
		t.Errorf("%s = %#v, want %#v", name, got, want)
	}
}
