//go:build linux

package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	statusNotifierWatcherName     = "org.kde.StatusNotifierWatcher"
	statusNotifierWatcherPath     = dbus.ObjectPath("/StatusNotifierWatcher")
	statusNotifierWatcherIface    = "org.kde.StatusNotifierWatcher"
	statusNotifierItemInterface   = "org.freedesktop.StatusNotifierItem"
	statusNotifierItemCompatIface = "org.kde.StatusNotifierItem"
	statusNotifierItemPath        = dbus.ObjectPath("/StatusNotifierItem")
	dbusPropertiesInterface       = "org.freedesktop.DBus.Properties"
	dbusIntrospectInterface       = "org.freedesktop.DBus.Introspectable"
	trayOperationTimeout          = 2 * time.Second
)

var trayNameSequence atomic.Uint64

type dbusConnection interface {
	Object(string, dbus.ObjectPath) dbus.BusObject
	BusObject() dbus.BusObject
	RequestName(string, dbus.RequestNameFlags) (dbus.RequestNameReply, error)
	ReleaseName(string) (dbus.ReleaseNameReply, error)
	Export(interface{}, dbus.ObjectPath, string) error
	Emit(dbus.ObjectPath, string, ...interface{}) error
	AddMatchSignalContext(context.Context, ...dbus.MatchOption) error
	RemoveMatchSignalContext(context.Context, ...dbus.MatchOption) error
	Signal(chan<- *dbus.Signal)
	RemoveSignal(chan<- *dbus.Signal)
	Names() []string
	Close() error
}

type trayExportedObject struct {
	path  dbus.ObjectPath
	iface string
}

type linuxTrayService struct {
	opMu     sync.Mutex
	mu       sync.Mutex
	conn     dbusConnection
	connect  func(context.Context) (dbusConnection, error)
	config   TrayConfig
	name     string
	id       string
	visible  bool
	closed   bool
	exported []trayExportedObject

	menuSource   interface{}
	menuRoot     *dbusMenuNode
	menuByID     map[int32]*dbusMenuNode
	nextMenuID   int64
	menuRevision uint32

	watchGeneration uint64
	watchStop       chan struct{}
	signalChannel   chan *dbus.Signal
	matchActive     bool
}

func newPlatformTrayService() TrayService {
	return &linuxTrayService{connect: connectSessionBus}
}

func newTrayServiceWithConn(conn dbusConnection) TrayService {
	return &linuxTrayService{conn: conn}
}

func (s *linuxTrayService) isVisible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.visible
}

func (s *linuxTrayService) setVisible(visible bool) {
	s.mu.Lock()
	s.visible = visible
	s.mu.Unlock()
}

func (s *linuxTrayService) currentConfig() TrayConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTrayConfig(s.config)
}

func (s *linuxTrayService) currentName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

func (s *linuxTrayService) Capabilities() Capabilities {
	if s == nil {
		return Capabilities{}
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return Capabilities{}
	}
	if _, err := s.connectionLocked(); err != nil {
		return Capabilities{}
	}
	if _, err := s.watcherOwnerLocked(); err != nil {
		return Capabilities{}
	}
	return Capabilities{Tray: true}
}

func (s *linuxTrayService) connectionLocked() (dbusConnection, error) {
	if s.closed {
		return nil, errTrayClosed
	}
	if s.conn != nil {
		return s.conn, nil
	}
	if s.connect == nil {
		return nil, fmt.Errorf("%w: no session bus connector", ErrTrayUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), trayOperationTimeout)
	defer cancel()
	conn, err := s.connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: connect session bus: %v", ErrTrayUnavailable, err)
	}
	if conn == nil {
		return nil, fmt.Errorf("%w: connect session bus returned nil", ErrTrayUnavailable)
	}
	s.conn = conn
	return conn, nil
}

func (s *linuxTrayService) watcherOwnerLocked() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), trayOperationTimeout)
	defer cancel()
	var owner string
	err := s.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, statusNotifierWatcherName).Store(&owner)
	if err != nil {
		return "", fmt.Errorf("%w: StatusNotifierWatcher owner: %v", ErrTrayUnavailable, err)
	}
	if owner == "" {
		return "", fmt.Errorf("%w: StatusNotifierWatcher has an empty owner", ErrTrayUnavailable)
	}
	return owner, nil
}

func requestNameWithContext(conn dbusConnection, name string, flags dbus.RequestNameFlags) (dbus.RequestNameReply, error) {
	ctx, cancel := context.WithTimeout(context.Background(), trayOperationTimeout)
	defer cancel()
	var reply uint32
	err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.RequestName", 0, name, flags).Store(&reply)
	return dbus.RequestNameReply(reply), err
}

func releaseNameWithContext(conn dbusConnection, name string) (dbus.ReleaseNameReply, error) {
	ctx, cancel := context.WithTimeout(context.Background(), trayOperationTimeout)
	defer cancel()
	var reply uint32
	err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ReleaseName", 0, name).Store(&reply)
	return dbus.ReleaseNameReply(reply), err
}

func (s *linuxTrayService) Show(config TrayConfig) error {
	if s == nil {
		return errTrayClosed
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return errTrayClosed
	}
	if s.isVisible() {
		return nil
	}
	if err := validateTrayConfig(config); err != nil {
		return err
	}
	conn, err := s.connectionLocked()
	if err != nil {
		return err
	}
	if _, err := s.watcherOwnerLocked(); err != nil {
		return err
	}

	name := fmt.Sprintf("org.freedesktop.StatusNotifierItem-%d-%d", os.Getpid(), trayNameSequence.Add(1))
	reply, err := requestNameWithContext(conn, name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("%w: request name %s: %v", ErrTrayUnavailable, name, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner && reply != dbus.RequestNameReplyAlreadyOwner {
		return fmt.Errorf("%w: request name %s returned %d", ErrTrayUnavailable, name, reply)
	}
	oldConfig := s.currentConfig()
	s.mu.Lock()
	s.name = name
	if s.id == "" {
		s.id = deterministicTrayID(config)
	}
	s.config = cloneTrayConfig(config)
	s.visible = true
	s.mu.Unlock()
	if err := s.exportLocked(&sniObject{}, statusNotifierItemPath, statusNotifierItemInterface); err != nil {
		return s.registrationFailureLocked("export StatusNotifierItem", oldConfig, err)
	}
	if err := s.exportLocked(&sniObject{}, statusNotifierItemPath, statusNotifierItemCompatIface); err != nil {
		return s.registrationFailureLocked("export KDE StatusNotifierItem compatibility", oldConfig, err)
	}
	if err := s.exportLocked(&sniProperties{service: s}, statusNotifierItemPath, dbusPropertiesInterface); err != nil {
		return s.registrationFailureLocked("export properties", oldConfig, err)
	}
	if err := s.exportLocked(sniIntrospectable{}, statusNotifierItemPath, dbusIntrospectInterface); err != nil {
		return s.registrationFailureLocked("export introspection", oldConfig, err)
	}
	if err := s.exportLocked(&dbusMenuHandler{service: s}, dbusMenuPath, dbusMenuInterface); err != nil {
		return s.registrationFailureLocked("export dbusmenu", oldConfig, err)
	}
	if err := s.exportLocked(dbusMenuIntrospectable{}, dbusMenuPath, dbusIntrospectInterface); err != nil {
		return s.registrationFailureLocked("export dbusmenu introspection", oldConfig, err)
	}
	if err := s.subscribeWatcherLocked(); err != nil {
		return s.registrationFailureLocked("subscribe to StatusNotifierWatcher", oldConfig, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), trayOperationTimeout)
	defer cancel()
	err = conn.Object(statusNotifierWatcherName, statusNotifierWatcherPath).CallWithContext(
		ctx, statusNotifierWatcherIface+".RegisterStatusNotifierItem", 0, name,
	).Err
	if err != nil {
		return s.registrationFailureLocked("RegisterStatusNotifierItem", oldConfig, fmt.Errorf("%w: %v", ErrTrayUnavailable, err))
	}
	return nil
}

func (s *linuxTrayService) registrationFailureLocked(operation string, oldConfig TrayConfig, cause error) error {
	s.mu.Lock()
	s.visible = false
	s.config = oldConfig
	s.mu.Unlock()
	cleanup := s.unsubscribeWatcherLocked()
	cleanup = errors.Join(cleanup, s.unexportLocked())
	if s.currentName() != "" {
		if err := s.releaseNameLocked(); err != nil {
			cleanup = errors.Join(cleanup, err)
		}
	}
	if cleanup != nil {
		return errors.Join(fmt.Errorf("%w: %s: %v", ErrTrayUnavailable, operation, cause), cleanup)
	}
	return fmt.Errorf("%w: %s: %v", ErrTrayUnavailable, operation, cause)
}

func (s *linuxTrayService) exportLocked(value interface{}, path dbus.ObjectPath, iface string) error {
	if err := s.conn.Export(value, path, iface); err != nil {
		return err
	}
	s.exported = append(s.exported, trayExportedObject{path: path, iface: iface})
	return nil
}

func (s *linuxTrayService) unexportLocked() error {
	var result error
	remaining := make([]trayExportedObject, 0, len(s.exported))
	for i := len(s.exported) - 1; i >= 0; i-- {
		exported := s.exported[i]
		if err := s.conn.Export(nil, exported.path, exported.iface); err != nil {
			remaining = append([]trayExportedObject{exported}, remaining...)
			result = errors.Join(result, fmt.Errorf("unexport %s at %s: %w", exported.iface, exported.path, err))
		}
	}
	s.exported = remaining
	return result
}

func (s *linuxTrayService) releaseNameLocked() error {
	name := s.currentName()
	reply, err := releaseNameWithContext(s.conn, name)
	if err != nil {
		return fmt.Errorf("release name %s: %w", name, err)
	}
	if reply != dbus.ReleaseNameReplyReleased && reply != dbus.ReleaseNameReplyNotOwner && reply != dbus.ReleaseNameReplyNonExistent {
		return fmt.Errorf("release name %s returned %d", name, reply)
	}
	s.mu.Lock()
	s.name = ""
	s.mu.Unlock()
	return nil
}

func watcherMatchOptions() []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchObjectPath("/org/freedesktop/DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, statusNotifierWatcherName),
	}
}

func (s *linuxTrayService) subscribeWatcherLocked() error {
	if s.matchActive {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), trayOperationTimeout)
	defer cancel()
	if err := s.conn.AddMatchSignalContext(ctx, watcherMatchOptions()...); err != nil {
		return fmt.Errorf("%w: add watcher signal match: %v", ErrTrayUnavailable, err)
	}
	channel := make(chan *dbus.Signal, 8)
	stop := make(chan struct{})
	s.conn.Signal(channel)
	s.watchGeneration++
	generation := s.watchGeneration
	s.signalChannel = channel
	s.watchStop = stop
	s.matchActive = true
	go s.watchWatcher(channel, stop, generation)
	return nil
}

func (s *linuxTrayService) unsubscribeWatcherLocked() error {
	if !s.matchActive {
		return nil
	}
	s.matchActive = false
	s.watchGeneration++
	channel := s.signalChannel
	stop := s.watchStop
	s.signalChannel = nil
	s.watchStop = nil
	ctx, cancel := context.WithTimeout(context.Background(), trayOperationTimeout)
	defer cancel()
	err := s.conn.RemoveMatchSignalContext(ctx, watcherMatchOptions()...)
	s.conn.RemoveSignal(channel)
	close(stop)
	if err != nil {
		return fmt.Errorf("remove watcher signal match: %w", err)
	}
	return nil
}

func (s *linuxTrayService) watchWatcher(channel <-chan *dbus.Signal, stop <-chan struct{}, generation uint64) {
	for {
		select {
		case signal, ok := <-channel:
			if !ok {
				return
			}
			if signal == nil || signal.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(signal.Body) != 3 {
				continue
			}
			name, nameOK := signal.Body[0].(string)
			newOwner, ownerOK := signal.Body[2].(string)
			if !nameOK || !ownerOK || name != statusNotifierWatcherName || newOwner == "" {
				continue
			}
			s.reregisterWatcher(generation)
		case <-stop:
			return
		}
	}
}

func (s *linuxTrayService) reregisterWatcher(generation uint64) {
	s.opMu.Lock()
	if s.closed || !s.matchActive || generation != s.watchGeneration {
		s.opMu.Unlock()
		return
	}
	s.mu.Lock()
	visible := s.visible
	name := s.name
	conn := s.conn
	s.mu.Unlock()
	if !visible || name == "" || conn == nil {
		s.opMu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), trayOperationTimeout)
	err := conn.Object(statusNotifierWatcherName, statusNotifierWatcherPath).CallWithContext(
		ctx, statusNotifierWatcherIface+".RegisterStatusNotifierItem", 0, name,
	).Err
	cancel()
	s.opMu.Unlock()
	if err != nil {
		s.reportError(fmt.Errorf("%w: re-register after watcher restart: %v", ErrTrayUnavailable, err))
	}
}

func (s *linuxTrayService) reportError(err error) {
	s.mu.Lock()
	onError := s.config.OnError
	s.mu.Unlock()
	if onError != nil {
		onError(err)
	}
}

// Update commits config before best-effort notifications. A non-nil error is
// the first emission failure; later signals are still attempted and config
// remains in force.
func (s *linuxTrayService) Update(config TrayConfig) error {
	if s == nil {
		return errTrayClosed
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return errTrayClosed
	}
	if !s.isVisible() {
		return errTrayNotShown
	}
	if err := validateTrayConfig(config); err != nil {
		return err
	}
	s.mu.Lock()
	s.config = cloneTrayConfig(config)
	s.mu.Unlock()
	var result error
	signals := []string{"NewIcon", "NewAttentionIcon", "NewToolTip"}
	interfaces := []string{statusNotifierItemInterface, statusNotifierItemCompatIface}
	for _, signal := range signals {
		for _, iface := range interfaces {
			if err := s.conn.Emit(statusNotifierItemPath, iface+"."+signal); err != nil && result == nil {
				result = fmt.Errorf("emit %s.%s: %w", iface, signal, err)
			}
		}
	}
	return result
}

func (s *linuxTrayService) Hide() error {
	if s == nil {
		return nil
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.hideLocked()
}

func (s *linuxTrayService) hideLocked() error {
	if s.closed || !s.isVisible() && s.currentName() == "" && len(s.exported) == 0 && !s.matchActive {
		return nil
	}
	if err := s.unexportLocked(); err != nil {
		return err
	}
	if s.currentName() != "" {
		if err := s.releaseNameLocked(); err != nil {
			return err
		}
	}
	result := s.unsubscribeWatcherLocked()
	s.setVisible(false)
	return result
}

func (s *linuxTrayService) Close() error {
	if s == nil {
		return nil
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return nil
	}
	result := s.hideLocked()
	result = errors.Join(result, s.unsubscribeWatcherLocked())
	s.mu.Lock()
	s.closed = true
	s.visible = false
	s.mu.Unlock()
	conn := s.conn
	s.conn = nil
	s.connect = nil
	if conn != nil {
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close session bus: %w", err))
		}
	}
	return result
}

type sniIconPixmap struct {
	Width  int32
	Height int32
	Data   []byte
}

type sniToolTip struct {
	IconName   string
	IconPixmap []sniIconPixmap
	Title      string
	SubTitle   string
}

func (s *linuxTrayService) pixmapLocked() []sniIconPixmap {
	return []sniIconPixmap{{
		Width:  s.config.Icon.Width,
		Height: s.config.Icon.Height,
		Data:   append([]byte(nil), s.config.Icon.Pixels...),
	}}
}

func (s *linuxTrayService) propertyLocked(iface, name string) (interface{}, bool) {
	switch name {
	case "Category":
		return "ApplicationStatus", true
	case "Id":
		return s.id, true
	case "Title", "ToolTip":
		if name == "Title" {
			return s.config.Tooltip, true
		}
		return sniToolTip{Title: s.config.Tooltip, IconPixmap: []sniIconPixmap{}}, true
	case "Status":
		if s.visible {
			return "Active", true
		}
		return "Passive", true
	case "WindowId":
		if iface == statusNotifierItemCompatIface {
			return int32(0), true
		}
		return uint32(0), true
	case "IconName", "OverlayIconName", "AttentionIconName", "AttentionMovieName":
		return "", true
	case "IconPixmap", "AttentionIconPixmap":
		return s.pixmapLocked(), true
	case "OverlayIconPixmap":
		return []sniIconPixmap{}, true
	case "ItemIsMenu":
		return true, true
	case "Menu":
		return dbusMenuPath, true
	default:
		return nil, false
	}
}

func (s *linuxTrayService) propertiesLocked(iface string) map[string]dbus.Variant {
	names := []string{
		"Category", "Id", "Title", "Status", "WindowId",
		"IconName", "IconPixmap", "OverlayIconName", "OverlayIconPixmap",
		"AttentionIconName", "AttentionIconPixmap", "AttentionMovieName",
		"ToolTip", "ItemIsMenu", "Menu",
	}
	properties := make(map[string]dbus.Variant, len(names))
	for _, name := range names {
		value, _ := s.propertyLocked(iface, name)
		properties[name] = dbus.MakeVariant(value)
	}
	return properties
}

type sniProperties struct {
	service *linuxTrayService
}

func (p *sniProperties) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface != statusNotifierItemInterface && iface != statusNotifierItemCompatIface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []interface{}{"unknown interface " + iface})
	}
	p.service.mu.Lock()
	defer p.service.mu.Unlock()
	value, ok := p.service.propertyLocked(iface, name)
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []interface{}{"unknown property " + name})
	}
	return dbus.MakeVariant(value), nil
}

func (p *sniProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != statusNotifierItemInterface && iface != statusNotifierItemCompatIface {
		return nil, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []interface{}{"unknown interface " + iface})
	}
	p.service.mu.Lock()
	defer p.service.mu.Unlock()
	return p.service.propertiesLocked(iface), nil
}

func (p *sniProperties) Set(_, _ string, _ dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", nil)
}

type sniObject struct{}

func (*sniObject) Activate(_, _ int32) *dbus.Error          { return nil }
func (*sniObject) SecondaryActivate(_, _ int32) *dbus.Error { return nil }
func (*sniObject) ContextMenu(_, _ int32) *dbus.Error       { return nil }
func (*sniObject) Scroll(_ int32, _ string) *dbus.Error     { return nil }

type sniIntrospectable struct{}

func (sniIntrospectable) Introspect() (string, *dbus.Error) {
	return sniIntrospection, nil
}

const sniIntrospection = `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN" "http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">
<node>
  <interface name="org.freedesktop.StatusNotifierItem">
    <method name="Activate"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
    <method name="SecondaryActivate"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
    <method name="ContextMenu"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
    <method name="Scroll"><arg name="delta" type="i" direction="in"/><arg name="orientation" type="s" direction="in"/></method>
    <signal name="NewIcon"/><signal name="NewAttentionIcon"/><signal name="NewToolTip"/>
    <property name="Category" type="s" access="read"/><property name="Id" type="s" access="read"/>
    <property name="Title" type="s" access="read"/><property name="Status" type="s" access="read"/>
    <property name="WindowId" type="u" access="read"/><property name="IconName" type="s" access="read"/>
    <property name="IconPixmap" type="a(iiay)" access="read"/><property name="OverlayIconName" type="s" access="read"/>
    <property name="OverlayIconPixmap" type="a(iiay)" access="read"/><property name="AttentionIconName" type="s" access="read"/>
    <property name="AttentionIconPixmap" type="a(iiay)" access="read"/><property name="AttentionMovieName" type="s" access="read"/>
    <property name="ToolTip" type="(sa(iiay)ss)" access="read"/><property name="ItemIsMenu" type="b" access="read"/>
    <property name="Menu" type="o" access="read"/>
  </interface>
  <interface name="org.kde.StatusNotifierItem">
    <method name="Activate"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
    <method name="SecondaryActivate"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
    <method name="ContextMenu"><arg name="x" type="i" direction="in"/><arg name="y" type="i" direction="in"/></method>
    <method name="Scroll"><arg name="delta" type="i" direction="in"/><arg name="orientation" type="s" direction="in"/></method>
    <signal name="NewIcon"/><signal name="NewAttentionIcon"/><signal name="NewToolTip"/>
    <property name="Category" type="s" access="read"/><property name="Id" type="s" access="read"/>
    <property name="Title" type="s" access="read"/><property name="Status" type="s" access="read"/>
    <property name="WindowId" type="i" access="read"/><property name="IconName" type="s" access="read"/>
    <property name="IconPixmap" type="a(iiay)" access="read"/><property name="OverlayIconName" type="s" access="read"/>
    <property name="OverlayIconPixmap" type="a(iiay)" access="read"/><property name="AttentionIconName" type="s" access="read"/>
    <property name="AttentionIconPixmap" type="a(iiay)" access="read"/><property name="AttentionMovieName" type="s" access="read"/>
    <property name="ToolTip" type="(sa(iiay)ss)" access="read"/><property name="ItemIsMenu" type="b" access="read"/>
    <property name="Menu" type="o" access="read"/>
  </interface>
  <interface name="org.freedesktop.DBus.Properties">
    <method name="Get"><arg name="interface" type="s" direction="in"/><arg name="property" type="s" direction="in"/><arg name="value" type="v" direction="out"/></method>
    <method name="GetAll"><arg name="interface" type="s" direction="in"/><arg name="properties" type="a{sv}" direction="out"/></method>
    <method name="Set"><arg name="interface" type="s" direction="in"/><arg name="property" type="s" direction="in"/><arg name="value" type="v" direction="in"/></method>
  </interface>
  <interface name="org.freedesktop.DBus.Introspectable"><method name="Introspect"><arg name="data" type="s" direction="out"/></method></interface>
</node>`
