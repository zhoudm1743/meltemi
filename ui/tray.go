package ui

import (
	"errors"
	"fmt"
	"image"
	"sync"

	"github.com/zhoudm1743/meltemi/internal/desktop"
)

var ErrTrayUnavailable = desktop.ErrTrayUnavailable

var (
	errTrayDestroyed = errors.New("ui: tray destroyed")
	errTrayOwned     = errors.New("ui: tray belongs to another app")
	errAppQuitting   = errors.New("ui: application is quitting")
)

// Tray is an application-independent icon, tooltip and menu configuration
// until it is shown by an App. Icon, tooltip and lifecycle methods are safe to
// call from any goroutine. SetMenu follows MenuModel's UI-thread confinement.
type Tray struct {
	mu        sync.Mutex
	icon      desktop.Icon
	tooltip   string
	app       *App
	service   desktop.TrayService
	visible   bool
	destroyed bool

	menu             *MenuModel
	menuSnapshot     MenuSnapshot
	menuSubscription *menuSubscription
	menuGeneration   uint64
}

// NewTray validates and copies icon. An empty tooltip is valid. On Linux the
// SNI Id hashes the executable base name and first-shown configuration, then
// remains stable across updates and Hide/Show rather than following the PID.
// Identical trays share that Id, and changing the initial icon or tooltip for
// a later tray changes it; hosts can therefore conflate duplicate trays or
// lose persisted settings after an initial-configuration change.
func NewTray(icon image.Image, tooltip string) (*Tray, error) {
	encoded, err := desktop.EncodeIcon(icon)
	if err != nil {
		return nil, fmt.Errorf("ui: new tray: %w", err)
	}
	return &Tray{icon: encoded, tooltip: tooltip}, nil
}

// ShowTray synchronously exports and registers tray with the platform watcher.
// It may be called before or during Run.
func (a *App) ShowTray(tray *Tray) error {
	if a == nil {
		return errors.New("ui: show tray: nil app")
	}
	if tray == nil {
		return errors.New("ui: show tray: nil tray")
	}
	tray.mu.Lock()
	defer tray.mu.Unlock()
	if tray.destroyed {
		return errTrayDestroyed
	}
	if tray.app != nil && tray.app != a {
		return errTrayOwned
	}
	if a.trayClosing() {
		return errAppQuitting
	}
	if tray.visible {
		return nil
	}

	service := tray.service
	newService := service == nil
	if newService {
		if a.newTrayService == nil {
			return fmt.Errorf("ui: show tray: %w: no platform service", ErrTrayUnavailable)
		}
		service = a.newTrayService()
		if service == nil {
			return fmt.Errorf("ui: show tray: %w: nil platform service", ErrTrayUnavailable)
		}
	}
	if !a.trackTray(tray) {
		if newService {
			if err := service.Close(); err != nil {
				return errors.Join(errAppQuitting, fmt.Errorf("ui: show tray rollback: %w", err))
			}
		}
		return errAppQuitting
	}
	tray.app = a
	tray.service = service
	if !service.Capabilities().Tray {
		return tray.failShowLocked(fmt.Errorf("ui: show tray: %w: StatusNotifierWatcher unavailable", ErrTrayUnavailable))
	}
	if newService {
		if err := service.SetMenu(tray.desktopMenuLocked(service, tray.menuGeneration)); err != nil {
			return tray.failShowLocked(fmt.Errorf("ui: show tray menu: %w", err))
		}
	}
	if err := service.Show(tray.configLocked()); err != nil {
		return tray.failShowLocked(fmt.Errorf("ui: show tray: %w", err))
	}
	tray.visible = true
	return nil
}

func (t *Tray) failShowLocked(cause error) error {
	app, service := t.app, t.service
	t.app = nil
	t.service = nil
	t.visible = false
	if app != nil {
		app.untrackTray(t)
	}
	if service != nil {
		if err := service.Close(); err != nil {
			return errors.Join(cause, fmt.Errorf("ui: show tray rollback: %w", err))
		}
	}
	return cause
}

func (t *Tray) configLocked() desktop.TrayConfig {
	config := desktop.TrayConfig{Icon: t.icon, Tooltip: t.tooltip, OnError: t.reportServiceError}
	config.Icon.Pixels = append([]byte(nil), config.Icon.Pixels...)
	return config
}

func (t *Tray) reportServiceError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	app := t.app
	destroyed := t.destroyed
	t.mu.Unlock()
	if app != nil && !destroyed {
		app.noteErr(fmt.Errorf("ui: tray service: %w", err))
	}
}

// SetMenu replaces the native menu shown for this tray. The top-level title is
// intentionally omitted; its items become children of dbusmenu's root. Like
// other live model updates, SetMenu runs on the UI thread or before Run.
func (t *Tray) SetMenu(menu *MenuModel) error {
	if t == nil {
		return errors.New("ui: set tray menu: nil tray")
	}
	snapshot := menu.Snapshot()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.destroyed {
		return errTrayDestroyed
	}
	t.menuGeneration++
	t.menu = menu
	t.menuSnapshot = snapshot
	t.bindMenuSubscriptionLocked()
	if t.service != nil {
		if err := t.service.SetMenu(t.desktopMenuLocked(t.service, t.menuGeneration)); err != nil {
			return fmt.Errorf("ui: set tray menu: %w", err)
		}
	}
	return nil
}

func (t *Tray) bindMenuSubscriptionLocked() {
	t.menuSubscription.unsubscribe()
	t.menuSubscription = nil
	if t.menu == nil {
		return
	}
	root := t.menu
	generation := t.menuGeneration
	t.menuSubscription = subscribeMenuChanges(root, func() {
		t.menuChanged(root, generation)
	})
}

func (t *Tray) menuChanged(root *MenuModel, generation uint64) {
	t.mu.Lock()
	valid := !t.destroyed && t.menu == root && t.menuGeneration == generation
	t.mu.Unlock()
	if !valid {
		return
	}
	snapshot := root.Snapshot()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.destroyed || t.menu != root || t.menuGeneration != generation {
		return
	}
	t.menuSnapshot = snapshot
	if t.service == nil {
		return
	}
	if err := t.service.SetMenu(t.desktopMenuLocked(t.service, generation)); err != nil && t.app != nil {
		t.app.noteErr(fmt.Errorf("ui: update tray menu: %w", err))
	}
}

func (t *Tray) desktopMenuLocked(service desktop.TrayService, generation uint64) desktop.TrayMenu {
	return desktop.TrayMenu{
		Source: t.menu,
		Items:  t.desktopMenuItemsLocked(service, generation, t.menuSnapshot.Items),
	}
}

func (t *Tray) desktopMenuItemsLocked(service desktop.TrayService, generation uint64, snapshots []MenuItemSnapshot) []desktop.TrayMenuItem {
	items := make([]desktop.TrayMenuItem, 0, len(snapshots))
	for index := range snapshots {
		snapshot := snapshots[index]
		kind := desktop.TrayMenuAction
		switch snapshot.Kind {
		case MenuItemSeparator:
			kind = desktop.TrayMenuSeparator
		case MenuItemSubmenu:
			kind = desktop.TrayMenuSubmenu
		}
		item := desktop.TrayMenuItem{
			Source:  snapshot.model,
			Text:    snapshot.Text,
			Kind:    kind,
			Enabled: snapshot.Enabled,
			Checked: snapshot.Checked,
		}
		if snapshot.Shortcut != nil {
			item.Shortcut = &desktop.TrayMenuShortcut{
				Modifiers: desktop.TrayMenuModifiers(snapshot.Shortcut.Modifiers),
				Key:       snapshot.Shortcut.Key,
			}
		}
		item.Children = t.desktopMenuItemsLocked(service, generation, snapshot.Children)
		if snapshot.Kind == MenuItemAction && snapshot.model != nil && t.app != nil {
			app := t.app
			item.Activate = func() {
				_ = app.Post(func() {
					t.mu.Lock()
					valid := !t.destroyed && t.visible && t.app == app && t.service == service && t.menuGeneration == generation
					t.mu.Unlock()
					if valid {
						snapshot.Activate()
					}
				})
			}
		}
		items = append(items, item)
	}
	return items
}

// SetIcon replaces the copied icon and synchronously updates a visible tray.
// After a valid update, an emission error reports the first failed host
// notification and leaves the new icon in force.
func (t *Tray) SetIcon(icon image.Image) error {
	if t == nil {
		return errors.New("ui: set tray icon: nil tray")
	}
	encoded, err := desktop.EncodeIcon(icon)
	if err != nil {
		return fmt.Errorf("ui: set tray icon: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.destroyed {
		return errTrayDestroyed
	}
	if t.visible {
		t.icon = encoded
		if err := t.service.Update(t.configLocked()); err != nil {
			return fmt.Errorf("ui: set tray icon: %w", err)
		}
		return nil
	}
	t.icon = encoded
	return nil
}

// SetTooltip replaces the tooltip, including with an empty string, and
// synchronously updates a visible tray. After a valid update, an emission
// error reports the first failed host notification and leaves the new tooltip
// in force.
func (t *Tray) SetTooltip(tooltip string) error {
	if t == nil {
		return errors.New("ui: set tray tooltip: nil tray")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.destroyed {
		return errTrayDestroyed
	}
	if t.visible {
		t.tooltip = tooltip
		if err := t.service.Update(t.configLocked()); err != nil {
			return fmt.Errorf("ui: set tray tooltip: %w", err)
		}
		return nil
	}
	t.tooltip = tooltip
	return nil
}

// Hide unexports and unregisters a visible tray. The copied configuration and
// platform service remain available for a later ShowTray on the same App.
func (t *Tray) Hide() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.destroyed || !t.visible {
		return nil
	}
	if err := t.service.Hide(); err != nil {
		return fmt.Errorf("ui: hide tray: %w", err)
	}
	t.visible = false
	return nil
}

// Destroy permanently releases the platform registration and connection.
// Calls after the first are no-ops.
func (t *Tray) Destroy() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.destroyed {
		return nil
	}
	t.destroyed = true
	t.menuGeneration++
	t.menuSubscription.unsubscribe()
	t.menuSubscription = nil
	t.menu = nil
	t.menuSnapshot = MenuSnapshot{}
	app, service := t.app, t.service
	t.app = nil
	t.service = nil
	t.visible = false
	if app != nil {
		app.untrackTray(t)
	}
	if service != nil {
		if err := service.Close(); err != nil {
			return fmt.Errorf("ui: destroy tray: %w", err)
		}
	}
	return nil
}

func (a *App) trayClosing() bool {
	a.traysMu.Lock()
	defer a.traysMu.Unlock()
	return a.traysClosing
}

func (a *App) trackTray(tray *Tray) bool {
	a.traysMu.Lock()
	defer a.traysMu.Unlock()
	if a.traysClosing {
		return false
	}
	for _, candidate := range a.trays {
		if candidate == tray {
			return true
		}
	}
	a.trays = append(a.trays, tray)
	return true
}

func (a *App) untrackTray(tray *Tray) {
	a.traysMu.Lock()
	defer a.traysMu.Unlock()
	for i, candidate := range a.trays {
		if candidate == tray {
			a.trays = append(a.trays[:i], a.trays[i+1:]...)
			return
		}
	}
}

func (a *App) closeTrays() error {
	a.traysMu.Lock()
	a.traysClosing = true
	trays := append([]*Tray(nil), a.trays...)
	a.traysMu.Unlock()
	var result error
	for i := len(trays) - 1; i >= 0; i-- {
		result = errors.Join(result, trays[i].Destroy())
	}
	return result
}
