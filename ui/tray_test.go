package ui

import (
	"errors"
	"image"
	stdcolor "image/color"
	"sync"
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/internal/desktop"
)

type fakeUITrayService struct {
	mu           sync.Mutex
	available    bool
	showErr      error
	updateErr    error
	menuErr      error
	hideErr      error
	configs      []desktop.TrayConfig
	menus        []desktop.TrayMenu
	showCalls    int
	updateCalls  int
	setMenuCalls int
	hideCalls    int
	closeCalls   int
}

func (s *fakeUITrayService) Capabilities() desktop.Capabilities {
	return desktop.Capabilities{Tray: s.available}
}

func (s *fakeUITrayService) Show(config desktop.TrayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.showCalls++
	if s.showErr != nil {
		return s.showErr
	}
	s.configs = append(s.configs, cloneUITrayConfig(config))
	return nil
}

func (s *fakeUITrayService) SetMenu(menu desktop.TrayMenu) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setMenuCalls++
	if s.menuErr != nil {
		return s.menuErr
	}
	s.menus = append(s.menus, menu)
	return nil
}

func (s *fakeUITrayService) Update(config desktop.TrayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	if s.updateErr != nil {
		return s.updateErr
	}
	s.configs = append(s.configs, cloneUITrayConfig(config))
	return nil
}

func (s *fakeUITrayService) Hide() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hideCalls++
	return s.hideErr
}

func (s *fakeUITrayService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
	return nil
}

func cloneUITrayConfig(config desktop.TrayConfig) desktop.TrayConfig {
	config.Icon.Pixels = append([]byte(nil), config.Icon.Pixels...)
	return config
}

func testTrayImage() *image.NRGBA {
	icon := image.NewNRGBA(image.Rect(3, 5, 4, 6))
	icon.SetNRGBA(3, 5, stdcolor.NRGBA{R: 10, G: 20, B: 30, A: 255})
	return icon
}

func TestCapabilities(t *testing.T) {
	service := &fakeUITrayService{available: true}
	app := New()
	app.newTrayService = func() desktop.TrayService { return service }

	capabilities := app.Capabilities()
	if !capabilities.MenuBar {
		t.Error("MenuBar = false, want true for the self-drawn window menu bar")
	}
	if !capabilities.Tray {
		t.Error("Tray = false, want the injected service availability")
	}
	if service.closeCalls != 1 {
		t.Errorf("capability service Close calls = %d, want 1", service.closeCalls)
	}

	app.newTrayService = func() desktop.TrayService { return &fakeUITrayService{} }
	if capabilities := app.Capabilities(); !capabilities.MenuBar || capabilities.Tray {
		t.Errorf("unavailable capabilities = %+v, want MenuBar true and Tray false", capabilities)
	}
}

func TestTrayUnavailable(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "")
	if err != nil {
		t.Fatalf("NewTray with an empty tooltip: %v", err)
	}

	t.Run("watcher unavailable", func(t *testing.T) {
		service := &fakeUITrayService{}
		app := New()
		app.newTrayService = func() desktop.TrayService { return service }
		if err := app.ShowTray(tray); !errors.Is(err, ErrTrayUnavailable) {
			t.Fatalf("ShowTray error = %v, want ErrTrayUnavailable", err)
		}
		if service.showCalls != 0 {
			t.Errorf("Show calls = %d, want 0 when capabilities report no watcher", service.showCalls)
		}
		if service.closeCalls != 1 {
			t.Errorf("Close calls = %d, want rollback", service.closeCalls)
		}
	})

	t.Run("registration failure", func(t *testing.T) {
		registrationErr := errors.New("RegisterStatusNotifierItem rejected")
		service := &fakeUITrayService{available: true, showErr: registrationErr}
		app := New()
		app.newTrayService = func() desktop.TrayService { return service }
		if err := app.ShowTray(tray); !errors.Is(err, registrationErr) {
			t.Fatalf("ShowTray error = %v, want registration cause", err)
		}
		if service.closeCalls != 1 {
			t.Errorf("Close calls = %d, want rollback", service.closeCalls)
		}
	})
}

func TestTrayValidationAndImmutableConfig(t *testing.T) {
	if _, err := NewTray(nil, "nil"); err == nil {
		t.Error("NewTray(nil) succeeded, want validation error")
	}
	if _, err := NewTray(image.NewNRGBA(image.Rectangle{}), "empty"); err == nil {
		t.Error("NewTray with empty bounds succeeded, want validation error")
	}

	icon := testTrayImage()
	tray, err := NewTray(icon, "initial")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	icon.SetNRGBA(3, 5, stdcolor.NRGBA{R: 200, G: 100, B: 50, A: 255})
	service := &fakeUITrayService{available: true}
	app := New()
	app.newTrayService = func() desktop.TrayService { return service }
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}
	if len(service.configs) != 1 {
		t.Fatalf("stored configs = %d, want 1", len(service.configs))
	}
	want := []byte{255, 10, 20, 30}
	if got := service.configs[0].Icon.Pixels; !equalBytes(got, want) {
		t.Errorf("shown pixels = %v, want copied original %v", got, want)
	}

	if err := tray.SetIcon(nil); err == nil {
		t.Error("SetIcon(nil) succeeded, want validation error")
	}
	if err := tray.SetIcon(image.NewNRGBA(image.Rectangle{})); err == nil {
		t.Error("SetIcon(empty) succeeded, want validation error")
	}
	if service.updateCalls != 0 {
		t.Errorf("Update calls after invalid icons = %d, want 0", service.updateCalls)
	}
	if err := tray.SetTooltip(""); err != nil {
		t.Errorf("SetTooltip(empty): %v", err)
	}
	if got := service.configs[len(service.configs)-1].Tooltip; got != "" {
		t.Errorf("updated tooltip = %q, want empty", got)
	}
	if err := tray.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestTrayShowUpdateHideDestroy(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "tip")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	service := &fakeUITrayService{available: true}
	app := New()
	app.newTrayService = func() desktop.TrayService { return service }
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray before Run: %v", err)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("repeated ShowTray: %v", err)
	}
	if service.showCalls != 1 {
		t.Errorf("Show calls = %d, want 1", service.showCalls)
	}

	replacement := image.NewNRGBA(image.Rect(-2, -1, -1, 0))
	replacement.SetNRGBA(-2, -1, stdcolor.NRGBA{R: 1, G: 2, B: 3, A: 4})
	if err := tray.SetIcon(replacement); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}
	if service.updateCalls != 1 {
		t.Errorf("Update calls = %d, want 1", service.updateCalls)
	}
	if err := tray.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := tray.Hide(); err != nil {
		t.Fatalf("repeated Hide: %v", err)
	}
	if service.hideCalls != 1 {
		t.Errorf("Hide calls = %d, want 1", service.hideCalls)
	}
	if err := tray.SetTooltip("hidden"); err != nil {
		t.Fatalf("SetTooltip while hidden: %v", err)
	}
	if service.updateCalls != 1 {
		t.Errorf("hidden Update calls = %d, want 1", service.updateCalls)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("re-show: %v", err)
	}
	if got := service.configs[len(service.configs)-1].Tooltip; got != "hidden" {
		t.Errorf("re-shown tooltip = %q, want hidden", got)
	}

	if err := tray.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if err := tray.Destroy(); err != nil {
		t.Fatalf("repeated Destroy: %v", err)
	}
	if service.closeCalls != 1 {
		t.Errorf("Close calls = %d, want 1", service.closeCalls)
	}
	if err := app.ShowTray(tray); err == nil {
		t.Error("ShowTray after Destroy succeeded")
	}
}

func TestTraySettersRetainConfigAfterSignalError(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "old")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	service := &fakeUITrayService{available: true, updateErr: errors.New("signal failed")}
	app := New()
	app.newTrayService = func() desktop.TrayService { return service }
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}
	newImage := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	newImage.SetNRGBA(0, 0, stdcolor.NRGBA{R: 1, G: 2, B: 3, A: 5})
	if err := tray.SetIcon(newImage); err == nil {
		t.Fatal("SetIcon succeeded, want signal error")
	}
	if err := tray.SetTooltip("new"); err == nil {
		t.Fatal("SetTooltip succeeded, want signal error")
	}
	service.updateErr = nil
	if err := tray.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("re-show: %v", err)
	}
	got := service.configs[len(service.configs)-1]
	if got.Tooltip != "new" || !equalBytes(got.Icon.Pixels, []byte{5, 1, 2, 3}) {
		t.Errorf("re-shown config = tooltip %q pixels %v, want retained new values", got.Tooltip, got.Icon.Pixels)
	}
	if err := tray.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestTrayHideFailureKeepsOwnershipForRetry(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "tip")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	service := &fakeUITrayService{available: true, hideErr: errors.New("cleanup failed")}
	app := New()
	app.newTrayService = func() desktop.TrayService { return service }
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}
	if err := tray.Hide(); err == nil {
		t.Fatal("Hide succeeded, want injected failure")
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray during failed Hide: %v", err)
	}
	if service.showCalls != 1 {
		t.Errorf("Show calls during failed Hide = %d, want no duplicate registration", service.showCalls)
	}

	service.hideErr = nil
	if err := tray.Hide(); err != nil {
		t.Fatalf("Hide retry: %v", err)
	}
	if service.hideCalls != 2 {
		t.Errorf("Hide attempts = %d, want initial + retry", service.hideCalls)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray after successful retry: %v", err)
	}
	if service.showCalls != 2 {
		t.Errorf("Show calls after retry = %d, want initial + re-show", service.showCalls)
	}
	if err := tray.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestTrayShowWhileRunActive(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "running")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	service := &fakeUITrayService{available: true}
	app, _ := newUITestApp(newUITestBackend(), &uiTestLog{})
	app.newTrayService = func() desktop.TrayService { return service }
	done := runUIAsync(app)
	running := make(chan struct{})
	if err := app.Post(func() { close(running) }); err != nil {
		t.Fatalf("Post running marker: %v", err)
	}
	select {
	case <-running:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not process the running marker")
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray while Run is active: %v", err)
	}
	if service.showCalls != 1 {
		t.Errorf("Show calls = %d, want synchronous registration", service.showCalls)
	}
	app.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after Quit")
	}
	if service.closeCalls != 1 {
		t.Errorf("Close calls = %d, want 1", service.closeCalls)
	}
}

func TestTrayQuitClosesWithoutUIQueueWait(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "tip")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	service := &fakeUITrayService{available: true}
	app, _ := newUITestApp(newUITestBackend(), &uiTestLog{})
	app.newTrayService = func() desktop.TrayService { return service }
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}

	done := runUIAsync(app)
	quitReturned := make(chan struct{})
	if err := app.Post(func() {
		app.Quit()
		close(quitReturned)
	}); err != nil {
		t.Fatalf("Post Quit: %v", err)
	}
	select {
	case <-quitReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Quit deadlocked on its own UI queue")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after Quit")
	}
	if service.closeCalls != 1 {
		t.Errorf("Close calls after Quit = %d, want 1", service.closeCalls)
	}
}

func TestTrayRegistrationFailureCanRetry(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "tip")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	failure := &fakeUITrayService{available: true, showErr: errors.New("failed")}
	success := &fakeUITrayService{available: true}
	services := []desktop.TrayService{failure, success}
	app := New()
	app.newTrayService = func() desktop.TrayService {
		service := services[0]
		services = services[1:]
		return service
	}
	if err := app.ShowTray(tray); err == nil {
		t.Fatal("first ShowTray succeeded, want injected failure")
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("retry ShowTray: %v", err)
	}
	if failure.closeCalls != 1 || success.closeCalls != 0 {
		t.Errorf("Close calls failure/success = %d/%d, want 1/0", failure.closeCalls, success.closeCalls)
	}
	if err := tray.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestTrayShowDuringQuitDoesNotLeak(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "tip")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	service := &fakeUITrayService{available: true}
	app, _ := newUITestApp(newUITestBackend(), &uiTestLog{})
	factoryEntered := make(chan struct{})
	allowFactory := make(chan struct{})
	app.newTrayService = func() desktop.TrayService {
		close(factoryEntered)
		<-allowFactory
		return service
	}
	showDone := make(chan error, 1)
	go func() { showDone <- app.ShowTray(tray) }()
	<-factoryEntered
	app.Quit()
	close(allowFactory)
	if err := <-showDone; !errors.Is(err, errAppQuitting) {
		t.Errorf("ShowTray during Quit error = %v, want errAppQuitting", err)
	}
	if service.showCalls != 0 {
		t.Errorf("Show calls after Quit = %d, want 0", service.showCalls)
	}
	if service.closeCalls != 1 {
		t.Errorf("Close calls after rejected Show = %d, want 1", service.closeCalls)
	}
}

func TestTrayMenuPreShowMutationsRefreshSavedSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MenuItemModel)
		verify func(*testing.T, desktop.TrayMenu)
	}{
		{
			name:   "SetChecked",
			mutate: func(item *MenuItemModel) { item.SetChecked(true) },
			verify: func(t *testing.T, menu desktop.TrayMenu) {
				if !menu.Items[0].Checked {
					t.Error("ShowTray exported the stale unchecked pre-Show snapshot")
				}
			},
		},
		{
			name:   "SetEnabled",
			mutate: func(item *MenuItemModel) { item.SetEnabled(false) },
			verify: func(t *testing.T, menu desktop.TrayMenu) {
				if menu.Items[0].Enabled {
					t.Error("ShowTray exported the stale enabled pre-Show snapshot")
				}
			},
		},
		{
			name:   "Shortcut",
			mutate: func(item *MenuItemModel) { item.Shortcut(ModCtrl, 'x') },
			verify: func(t *testing.T, menu desktop.TrayMenu) {
				shortcut := menu.Items[0].Shortcut
				if shortcut == nil || shortcut.Modifiers != desktop.TrayMenuModCtrl || shortcut.Key != 'x' {
					t.Errorf("ShowTray exported shortcut %+v, want Ctrl+X from the latest pre-Show model", shortcut)
				}
			},
		},
		{
			name: "Submenu",
			mutate: func(item *MenuItemModel) {
				item.Submenu(MenuItem("Latest child"))
			},
			verify: func(t *testing.T, menu desktop.TrayMenu) {
				item := menu.Items[0]
				if item.Kind != desktop.TrayMenuSubmenu || len(item.Children) != 1 || item.Children[0].Text != "Latest child" {
					t.Errorf("ShowTray exported submenu %+v, want the latest pre-Show child", item)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tray, err := NewTray(testTrayImage(), "pre-show")
			if err != nil {
				t.Fatalf("NewTray: %v", err)
			}
			item := MenuItem("Item")
			if err := tray.SetMenu(Menu("", item)); err != nil {
				t.Fatalf("SetMenu: %v", err)
			}
			service := &fakeUITrayService{available: true}
			app := New()
			app.newTrayService = func() desktop.TrayService { return service }
			test.mutate(item)
			if service.setMenuCalls != 0 {
				t.Fatalf("backend SetMenu calls before Show = %d, want 0", service.setMenuCalls)
			}
			if err := app.ShowTray(tray); err != nil {
				t.Fatalf("ShowTray: %v", err)
			}
			if service.setMenuCalls != 1 || len(service.menus) != 1 || len(service.menus[0].Items) != 1 {
				t.Fatalf("exported menus = %d/%+v, want one item from one installation", service.setMenuCalls, service.menus)
			}
			test.verify(t, service.menus[0])
			if err := tray.Destroy(); err != nil {
				t.Fatalf("Destroy: %v", err)
			}
		})
	}
}

func TestTrayMenuUpdatesSnapshotsAndSubscriptions(t *testing.T) {
	tray, err := NewTray(testTrayImage(), "menu")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	item := MenuItem("Checkable")
	model := Menu("this title belongs to the model, not the native root", item)
	if err := tray.SetMenu(model); err != nil {
		t.Fatalf("SetMenu before Show: %v", err)
	}
	service := &fakeUITrayService{available: true}
	app := New()
	app.newTrayService = func() desktop.TrayService { return service }
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}
	if service.setMenuCalls != 1 || len(service.menus) != 1 {
		t.Fatalf("SetMenu calls/stored menus = %d/%d, want 1/1", service.setMenuCalls, len(service.menus))
	}
	initial := service.menus[0]
	if len(initial.Items) != 1 || !initial.Items[0].Enabled || initial.Items[0].Checked {
		t.Fatalf("initial desktop menu = %+v, want enabled unchecked item", initial)
	}

	item.SetEnabled(false)
	item.SetChecked(true)
	if service.setMenuCalls != 3 {
		t.Fatalf("SetMenu calls after SetEnabled/SetChecked = %d, want 3", service.setMenuCalls)
	}
	if !service.menus[0].Items[0].Enabled || service.menus[0].Items[0].Checked {
		t.Error("a model update mutated the previously installed immutable desktop DTO")
	}
	latest := service.menus[2]
	if latest.Items[0].Enabled || !latest.Items[0].Checked {
		t.Errorf("latest desktop menu = %+v, want disabled checked", latest.Items[0])
	}

	replacementItem := MenuItem("Replacement")
	if err := tray.SetMenu(Menu("", replacementItem)); err != nil {
		t.Fatalf("SetMenu replacement: %v", err)
	}
	if service.setMenuCalls != 4 {
		t.Fatalf("SetMenu calls after replacement = %d, want 4", service.setMenuCalls)
	}
	item.SetEnabled(true).SetChecked(false)
	if service.setMenuCalls != 4 {
		t.Errorf("detached model still has a tray subscription: calls = %d", service.setMenuCalls)
	}
	replacementItem.SetChecked(true)
	if service.setMenuCalls != 5 {
		t.Errorf("replacement model was not subscribed: calls = %d, want 5", service.setMenuCalls)
	}
	if err := tray.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	replacementItem.SetChecked(false)
	if service.setMenuCalls != 6 {
		t.Errorf("hidden tray did not retain its model subscription: calls = %d, want 6", service.setMenuCalls)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("re-show: %v", err)
	}
	if service.setMenuCalls != 6 {
		t.Errorf("re-show unnecessarily replaced the menu: calls = %d", service.setMenuCalls)
	}
	if err := tray.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	replacementItem.SetEnabled(false)
	if service.setMenuCalls != 6 {
		t.Errorf("destroyed tray retained a model subscription: calls = %d", service.setMenuCalls)
	}
}

func TestTrayMenuPresentationSettersSynchronizeNativeSnapshots(t *testing.T) {
	t.Run("SetText", func(t *testing.T) {
		tray, err := NewTray(testTrayImage(), "labels")
		if err != nil {
			t.Fatalf("NewTray: %v", err)
		}
		item := MenuItem("Before")
		if err := tray.SetMenu(Menu("", item)); err != nil {
			t.Fatalf("SetMenu: %v", err)
		}
		service := &fakeUITrayService{available: true}
		app := New()
		app.newTrayService = func() desktop.TrayService { return service }
		if err := app.ShowTray(tray); err != nil {
			t.Fatalf("ShowTray: %v", err)
		}
		item.SetText("After")
		if service.setMenuCalls != 2 || len(service.menus) != 2 {
			t.Fatalf("SetText native updates = %d/%d, want 2/2", service.setMenuCalls, len(service.menus))
		}
		if got := service.menus[1].Items[0].Text; got != "After" {
			t.Errorf("native label after SetText = %q, want After", got)
		}
		if got := service.menus[0].Items[0].Text; got != "Before" {
			t.Errorf("SetText mutated the previous native snapshot to %q", got)
		}
		if err := tray.Destroy(); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
	})

	t.Run("SetItems", func(t *testing.T) {
		tray, err := NewTray(testTrayImage(), "items")
		if err != nil {
			t.Fatalf("NewTray: %v", err)
		}
		old := MenuItem("Old")
		model := Menu("", old)
		if err := tray.SetMenu(model); err != nil {
			t.Fatalf("SetMenu: %v", err)
		}
		service := &fakeUITrayService{available: true}
		app := New()
		app.newTrayService = func() desktop.TrayService { return service }
		if err := app.ShowTray(tray); err != nil {
			t.Fatalf("ShowTray: %v", err)
		}

		replacement := MenuItem("Replacement")
		model.SetItems(replacement)
		if service.setMenuCalls != 2 || len(service.menus) != 2 || len(service.menus[1].Items) != 1 || service.menus[1].Items[0].Text != "Replacement" {
			t.Fatalf("native menu after SetItems = %+v, want one Replacement", service.menus)
		}
		old.SetChecked(true)
		if service.setMenuCalls != 2 {
			t.Errorf("detached item still triggers native updates: calls = %d", service.setMenuCalls)
		}
		replacement.SetChecked(true)
		if service.setMenuCalls != 3 || !service.menus[2].Items[0].Checked {
			t.Errorf("replacement item was not subscribed: calls/menu = %d/%+v", service.setMenuCalls, service.menus)
		}

		model.SetItems()
		if service.setMenuCalls != 4 || len(service.menus[3].Items) != 0 {
			t.Errorf("native menu after empty SetItems = %+v, want no items", service.menus)
		}
		afterEmpty := MenuItem("After empty")
		model.SetItems(afterEmpty)
		if service.setMenuCalls != 5 || len(service.menus[4].Items) != 1 || service.menus[4].Items[0].Text != "After empty" {
			t.Errorf("native menu after repopulating an empty model = %+v", service.menus)
		}
		afterEmpty.SetChecked(true)
		if service.setMenuCalls != 6 || !service.menus[5].Items[0].Checked {
			t.Errorf("item inserted after an empty model was not subscribed: calls = %d", service.setMenuCalls)
		}
		if err := tray.Destroy(); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
	})
}

func TestTrayMenuRestoresWindowOnUIThread(t *testing.T) {
	service := &fakeUITrayService{available: true}
	app, _ := newUITestApp(newUITestBackend(), &uiTestLog{})
	app.newTrayService = func() desktop.TrayService { return service }
	app.SetLastWindowAction(LastWindowKeepRunning)
	window := app.Window("restore me").Size(320, 200).Content(Label("existing window")).CloseAction(CloseHide)
	tray, err := NewTray(testTrayImage(), "restore")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	restored := make(chan struct{})
	restore := MenuItem("Restore window").OnClick(func() {
		window.Show()
		close(restored)
	})
	if err := tray.SetMenu(Menu("", restore)); err != nil {
		t.Fatalf("SetMenu: %v", err)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}
	window.Show()
	if err := window.Close(); err != nil {
		t.Fatalf("CloseHide: %v", err)
	}
	done := runUIAsync(app)
	hidden := make(chan error, 1)
	if err := app.Post(func() {
		if window.visible || window.target == nil || len(app.Windows()) != 1 {
			hidden <- errors.New("CloseHide did not preserve one hidden live window")
			return
		}
		hidden <- nil
	}); err != nil {
		t.Fatalf("Post hidden check: %v", err)
	}
	if err := <-hidden; err != nil {
		t.Fatal(err)
	}

	service.menus[0].Items[0].Activate()
	select {
	case <-restored:
		t.Fatal("native activation ran the callback directly instead of posting it")
	default:
	}
	select {
	case <-restored:
	case <-time.After(2 * time.Second):
		t.Fatal("posted tray activation did not run")
	}
	visible := make(chan error, 1)
	if err := app.Post(func() {
		if !window.visible || len(app.Windows()) != 1 || app.Windows()[0] != window {
			visible <- errors.New("tray activation did not restore the existing window")
			return
		}
		visible <- nil
	}); err != nil {
		t.Fatalf("Post visible check: %v", err)
	}
	if err := <-visible; err != nil {
		t.Fatal(err)
	}
	app.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after restored-window smoke")
	}
}

func TestTrayMenuReplacementIgnoresLateActivation(t *testing.T) {
	service := &fakeUITrayService{available: true}
	app, _ := newUITestApp(newUITestBackend(), &uiTestLog{})
	app.newTrayService = func() desktop.TrayService { return service }
	tray, err := NewTray(testTrayImage(), "replace")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	oldCalled := make(chan struct{}, 1)
	oldItem := MenuItem("Old").OnClick(func() { oldCalled <- struct{}{} })
	if err := tray.SetMenu(Menu("", oldItem)); err != nil {
		t.Fatalf("SetMenu old: %v", err)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}
	oldActivate := service.menus[0].Items[0].Activate
	newCalled := make(chan struct{}, 1)
	newItem := MenuItem("New").OnClick(func() { newCalled <- struct{}{} })
	if err := tray.SetMenu(Menu("", newItem)); err != nil {
		t.Fatalf("SetMenu replacement: %v", err)
	}
	newActivate := service.menus[1].Items[0].Activate
	done := runUIAsync(app)
	oldActivate()
	newActivate()
	drained := make(chan struct{})
	if err := app.Post(func() { close(drained) }); err != nil {
		t.Fatalf("Post drain: %v", err)
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement activations did not drain")
	}
	select {
	case <-oldCalled:
		t.Error("late old-menu activation ran after replacement")
	default:
	}
	select {
	case <-newCalled:
	default:
		t.Error("current replacement activation did not run")
	}
	app.Quit()
	<-done
}

func TestTrayDestroyIdempotentAndLateEvent(t *testing.T) {
	service := &fakeUITrayService{available: true}
	app, _ := newUITestApp(newUITestBackend(), &uiTestLog{})
	app.newTrayService = func() desktop.TrayService { return service }
	tray, err := NewTray(testTrayImage(), "destroy")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	called := make(chan struct{}, 1)
	item := MenuItem("Late").OnClick(func() { called <- struct{}{} })
	if err := tray.SetMenu(Menu("", item)); err != nil {
		t.Fatalf("SetMenu: %v", err)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}
	lateActivate := service.menus[0].Items[0].Activate
	done := runUIAsync(app)
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := app.Post(func() {
		close(entered)
		<-release
	}); err != nil {
		t.Fatalf("Post blocker: %v", err)
	}
	<-entered
	lateActivate()
	if err := tray.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if err := tray.Destroy(); err != nil {
		t.Fatalf("repeated Destroy: %v", err)
	}
	if service.closeCalls != 1 {
		t.Errorf("Close calls = %d, want exactly 1", service.closeCalls)
	}
	item.SetChecked(true).SetEnabled(false)
	if service.setMenuCalls != 1 {
		t.Errorf("model mutations after Destroy reached the service: calls = %d", service.setMenuCalls)
	}
	close(release)
	drained := make(chan struct{})
	if err := app.Post(func() { close(drained) }); err != nil {
		t.Fatalf("Post drain: %v", err)
	}
	<-drained
	select {
	case <-called:
		t.Error("activation posted before Destroy ran after teardown")
	default:
	}
	app.Quit()
	<-done
}

func TestTrayMenuEnabledAncestryAndDetachedSnapshots(t *testing.T) {
	service := &fakeUITrayService{available: true}
	app, _ := newUITestApp(newUITestBackend(), &uiTestLog{})
	app.newTrayService = func() desktop.TrayService { return service }
	tray, err := NewTray(testTrayImage(), "ancestry")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	childCalled := make(chan struct{}, 1)
	child := MenuItem("Child").OnClick(func() { childCalled <- struct{}{} })
	parent := MenuItem("Parent").Submenu(child)
	if err := tray.SetMenu(Menu("", parent)); err != nil {
		t.Fatalf("SetMenu: %v", err)
	}
	if err := app.ShowTray(tray); err != nil {
		t.Fatalf("ShowTray: %v", err)
	}
	done := runUIAsync(app)
	post := func(fn func()) {
		t.Helper()
		drained := make(chan struct{})
		if err := app.Post(func() {
			fn()
			close(drained)
		}); err != nil {
			t.Fatalf("Post: %v", err)
		}
		select {
		case <-drained:
		case <-time.After(2 * time.Second):
			t.Fatal("UI task did not drain")
		}
	}

	post(func() { parent.SetEnabled(false) })
	disabledMenu := service.menus[len(service.menus)-1]
	if disabledMenu.Items[0].Enabled || !disabledMenu.Items[0].Children[0].Enabled {
		t.Fatalf("disabled ancestry DTO = %+v, want disabled parent with copied child state", disabledMenu.Items[0])
	}
	disabledMenu.Items[0].Children[0].Activate()
	post(func() {})
	select {
	case <-childCalled:
		t.Error("child activated through disabled native ancestry")
	default:
	}

	post(func() { parent.SetEnabled(true) })
	attachedMenu := service.menus[len(service.menus)-1]
	detachedActivate := attachedMenu.Items[0].Children[0].Activate
	replacementCalled := make(chan struct{}, 1)
	replacement := MenuItem("Replacement child").OnClick(func() { replacementCalled <- struct{}{} })
	post(func() { parent.Submenu(replacement) })
	callsAfterReplacement := service.setMenuCalls
	post(func() { child.SetChecked(true) })
	if service.setMenuCalls != callsAfterReplacement {
		t.Error("detached submenu child retained its model subscription")
	}
	detachedActivate()
	post(func() {})
	select {
	case <-childCalled:
		t.Error("detached child snapshot activated after submenu replacement")
	default:
	}
	service.menus[len(service.menus)-1].Items[0].Children[0].Activate()
	post(func() {})
	select {
	case <-replacementCalled:
	default:
		t.Error("current nested replacement child did not activate")
	}
	app.Quit()
	<-done
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
