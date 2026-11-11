//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowsTrayClass = "Meltemi.Tray"

const (
	wmNull        = 0x0000
	wmDestroy     = 0x0002
	wmCopyData    = 0x004A
	wmContextMenu = 0x007B
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmUser        = 0x0400
	wmApp         = 0x8000

	wmTrayNotify = wmApp + 1
	wmTrayWork   = wmApp + 2
	ninSelect    = wmUser
	ninKeySelect = wmUser + 1

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfPopup     = 0x0010
	mfSeparator = 0x0800

	tpmRightAlign  = 0x0008
	tpmBottomAlign = 0x0020
	tpmNoNotify    = 0x0080
	tpmReturnCmd   = 0x0100

	biRGB = 0
)

var (
	trayUser32 = windows.NewLazySystemDLL("user32.dll")
	trayGdi32  = windows.NewLazySystemDLL("gdi32.dll")
	trayShell  = windows.NewLazySystemDLL("shell32.dll")
	trayK32    = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassW      = trayUser32.NewProc("RegisterClassW")
	procCreateWindowExW     = trayUser32.NewProc("CreateWindowExW")
	procDestroyWindow       = trayUser32.NewProc("DestroyWindow")
	procDefWindowProcW      = trayUser32.NewProc("DefWindowProcW")
	procGetMessageW         = trayUser32.NewProc("GetMessageW")
	procTranslateMessage    = trayUser32.NewProc("TranslateMessage")
	procDispatchMessageW    = trayUser32.NewProc("DispatchMessageW")
	procPostQuitMessage     = trayUser32.NewProc("PostQuitMessage")
	procSendMessageW        = trayUser32.NewProc("SendMessageW")
	procFindWindowW         = trayUser32.NewProc("FindWindowW")
	procGetCursorPos        = trayUser32.NewProc("GetCursorPos")
	procSetForegroundWindow = trayUser32.NewProc("SetForegroundWindow")
	procCreatePopupMenu     = trayUser32.NewProc("CreatePopupMenu")
	procAppendMenuW         = trayUser32.NewProc("AppendMenuW")
	procDestroyMenu         = trayUser32.NewProc("DestroyMenu")
	procTrackPopupMenu      = trayUser32.NewProc("TrackPopupMenu")
	procCreateIconIndirect  = trayUser32.NewProc("CreateIconIndirect")
	procDestroyIcon         = trayUser32.NewProc("DestroyIcon")

	procCreateDIBSection = trayGdi32.NewProc("CreateDIBSection")
	procCreateBitmap     = trayGdi32.NewProc("CreateBitmap")
	procDeleteObject     = trayGdi32.NewProc("DeleteObject")
	procShellNotifyIconW = trayShell.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW = trayK32.NewProc("GetModuleHandleW")

	trayClassOnce sync.Once
	trayClassErr  error
	trayClassName *uint16
	trayWndProc   = windows.NewCallback(trayWindowProc)

	trayWndMu  sync.Mutex
	trayByHWND = map[uintptr]*windowsTrayService{}
)

type windowsTrayService struct {
	mu       sync.Mutex
	closed   bool
	visible  bool
	config   TrayConfig
	menu     TrayMenu
	hwnd     uintptr
	hicon    uintptr
	loopDone chan struct{}
	jobs     []trayJob
}

type trayJob struct {
	fn   func() error
	done chan error
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
}

type winPoint struct {
	x int32
	y int32
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
}

type copyData struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}

type wndClassW struct {
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type iconInfo struct {
	fIcon    uint32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

func newPlatformTrayService() TrayService { return &windowsTrayService{} }

func (s *windowsTrayService) Capabilities() Capabilities {
	if s == nil {
		return Capabilities{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Capabilities{}
	}
	return Capabilities{Tray: true}
}

func (s *windowsTrayService) Show(config TrayConfig) error {
	if s == nil {
		return errTrayClosed
	}
	if err := validateTrayConfig(config); err != nil {
		return err
	}
	if err := s.ensureLoop(); err != nil {
		return err
	}
	return s.call(func() error {
		if err := s.applyIcon(config); err != nil {
			return err
		}
		s.mu.Lock()
		s.visible = true
		s.config = cloneTrayConfig(config)
		s.mu.Unlock()
		return nil
	})
}

func (s *windowsTrayService) Update(config TrayConfig) error {
	if s == nil {
		return errTrayClosed
	}
	if err := validateTrayConfig(config); err != nil {
		return err
	}
	s.mu.Lock()
	visible := s.visible
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errTrayClosed
	}
	if !visible {
		return errTrayNotShown
	}
	return s.call(func() error {
		if err := s.applyIcon(config); err != nil {
			return err
		}
		s.mu.Lock()
		s.config = cloneTrayConfig(config)
		s.mu.Unlock()
		return nil
	})
}

func (s *windowsTrayService) SetMenu(menu TrayMenu) error {
	if s == nil {
		return errTrayClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errTrayClosed
	}
	s.menu = menu
	return nil
}

func (s *windowsTrayService) Hide() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed || !s.visible {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.call(func() error {
		s.deleteIcon()
		s.mu.Lock()
		s.visible = false
		s.mu.Unlock()
		return nil
	})
}

func (s *windowsTrayService) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	done := s.loopDone
	hwnd := s.hwnd
	s.mu.Unlock()
	if done == nil || hwnd == 0 {
		return nil
	}
	err := s.call(func() error {
		s.deleteIcon()
		procPostQuitMessage.Call(0)
		return nil
	})
	<-done
	return err
}

func (s *windowsTrayService) ensureLoop() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errTrayClosed
	}
	if s.loopDone != nil {
		s.mu.Unlock()
		return nil
	}
	s.loopDone = make(chan struct{})
	s.mu.Unlock()
	ready := make(chan error, 1)
	go s.loop(ready)
	if err := <-ready; err != nil {
		<-s.loopDone
		s.mu.Lock()
		s.loopDone = nil
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *windowsTrayService) loop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s.mu.Lock()
	done := s.loopDone
	s.mu.Unlock()
	defer close(done)

	if err := s.createWindow(); err != nil {
		ready <- err
		return
	}
	ready <- nil

	var msg winMsg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	s.destroyWindow()
}

func (s *windowsTrayService) createWindow() error {
	if err := registerTrayClass(); err != nil {
		return err
	}
	class, err := windows.UTF16PtrFromString(windowsTrayClass)
	if err != nil {
		return err
	}
	title, err := windows.UTF16PtrFromString(windowsTrayClass)
	if err != nil {
		return err
	}
	instance, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(title)),
		0x80000000, // WS_POPUP
		0, 0, 1, 1,
		0, 0,
		instance,
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("%w: create tray window: %v", ErrTrayUnavailable, callErr)
	}
	trayWndMu.Lock()
	trayByHWND[hwnd] = s
	trayWndMu.Unlock()
	s.mu.Lock()
	s.hwnd = hwnd
	s.mu.Unlock()
	return nil
}

func (s *windowsTrayService) destroyWindow() {
	s.mu.Lock()
	hwnd := s.hwnd
	s.hwnd = 0
	s.mu.Unlock()
	if hwnd == 0 {
		return
	}
	trayWndMu.Lock()
	delete(trayByHWND, hwnd)
	trayWndMu.Unlock()
	procDestroyWindow.Call(hwnd)
	s.deleteIcon()
}

func registerTrayClass() error {
	trayClassOnce.Do(func() {
		name, err := windows.UTF16PtrFromString(windowsTrayClass)
		if err != nil {
			trayClassErr = err
			return
		}
		trayClassName = name
		instance, _, _ := procGetModuleHandleW.Call(0)
		wc := wndClassW{
			lpfnWndProc:   trayWndProc,
			hInstance:     instance,
			lpszClassName: trayClassName,
		}
		atom, _, callErr := procRegisterClassW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			trayClassErr = fmt.Errorf("%w: register tray class: %v", ErrTrayUnavailable, callErr)
		}
	})
	return trayClassErr
}

func (s *windowsTrayService) call(fn func() error) error {
	done := make(chan error, 1)
	s.mu.Lock()
	if s.closed && fn != nil {
		// Close itself sets closed before call. Allow the quit job through
		// when the window still exists; reject later calls.
	}
	hwnd := s.hwnd
	if hwnd == 0 {
		s.mu.Unlock()
		return errTrayClosed
	}
	s.jobs = append(s.jobs, trayJob{fn: fn, done: done})
	s.mu.Unlock()
	procPostMessageW.Call(hwnd, wmTrayWork, 0, 0)
	return <-done
}

func (s *windowsTrayService) drainJobs() {
	for {
		s.mu.Lock()
		if len(s.jobs) == 0 {
			s.mu.Unlock()
			return
		}
		job := s.jobs[0]
		s.jobs = s.jobs[1:]
		s.mu.Unlock()
		var err error
		if job.fn != nil {
			err = job.fn()
		}
		job.done <- err
	}
}

func (s *windowsTrayService) applyIcon(config TrayConfig) error {
	hicon, err := iconFromARGB(config.Icon)
	if err != nil {
		return err
	}
	s.mu.Lock()
	hwnd := s.hwnd
	previous := s.hicon
	visible := s.visible
	s.hicon = hicon
	s.mu.Unlock()
	nid := notifyIconData{
		hWnd:             hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayNotify,
		hIcon:            hicon,
	}
	nid.cbSize = uint32(unsafe.Offsetof(nid.szTip) + unsafe.Sizeof(nid.szTip))
	if err := writeTooltip(&nid, config.Tooltip); err != nil {
		procDestroyIcon.Call(hicon)
		return err
	}
	cmd := uintptr(nimAdd)
	if visible || previous != 0 {
		cmd = nimModify
	}
	if ok, _, callErr := procShellNotifyIconW.Call(cmd, uintptr(unsafe.Pointer(&nid))); ok == 0 {
		procDestroyIcon.Call(hicon)
		s.mu.Lock()
		s.hicon = previous
		s.mu.Unlock()
		return fmt.Errorf("%w: Shell_NotifyIcon: %v", ErrTrayUnavailable, callErr)
	}
	if previous != 0 {
		procDestroyIcon.Call(previous)
	}
	return nil
}

func (s *windowsTrayService) deleteIcon() {
	s.mu.Lock()
	hwnd := s.hwnd
	hicon := s.hicon
	visible := s.visible
	s.hicon = 0
	s.visible = false
	s.mu.Unlock()
	if hwnd != 0 && (visible || hicon != 0) {
		nid := notifyIconData{hWnd: hwnd, uID: 1}
		nid.cbSize = uint32(unsafe.Offsetof(nid.szTip) + unsafe.Sizeof(nid.szTip))
		procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	}
	if hicon != 0 {
		procDestroyIcon.Call(hicon)
	}
}

func writeTooltip(nid *notifyIconData, tip string) error {
	if tip == "" {
		return nil
	}
	encoded, err := windows.UTF16FromString(tip)
	if err != nil {
		return fmt.Errorf("%w: tooltip: %v", errInvalidTrayIcon, err)
	}
	n := len(encoded)
	if n > 0 && encoded[n-1] == 0 {
		n--
	}
	if n > len(nid.szTip)-1 {
		n = len(nid.szTip) - 1
	}
	copy(nid.szTip[:], encoded[:n])
	return nil
}

func iconFromARGB(icon Icon) (uintptr, error) {
	pixels := bgraFromIcon(icon)
	header := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       icon.Width,
		biHeight:      -icon.Height,
		biPlanes:      1,
		biBitCount:    32,
		biCompression: biRGB,
	}
	var bits uintptr
	color, _, callErr := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == 0 {
		return 0, fmt.Errorf("%w: CreateDIBSection: %v", errInvalidTrayIcon, callErr)
	}
	dst := unsafe.Slice((*byte)(uintptrPtr(bits)), len(pixels))
	copy(dst, pixels)
	mask, _, _ := procCreateBitmap.Call(uintptr(icon.Width), uintptr(icon.Height), 1, 1, 0)
	info := iconInfo{fIcon: 1, hbmMask: mask, hbmColor: color}
	hicon, _, iconErr := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	if mask != 0 {
		procDeleteObject.Call(mask)
	}
	procDeleteObject.Call(color)
	if hicon == 0 {
		return 0, fmt.Errorf("%w: CreateIconIndirect: %v", errInvalidTrayIcon, iconErr)
	}
	return hicon, nil
}

// bgraFromIcon converts the tray's ARGB byte order into a top-down premultiplied
// BGRA buffer, which is what a 32-bit Windows icon bitmap stores.
func bgraFromIcon(icon Icon) []byte {
	out := make([]byte, len(icon.Pixels))
	for i := 0; i+3 < len(icon.Pixels); i += 4 {
		a := uint32(icon.Pixels[i+0])
		r := uint32(icon.Pixels[i+1]) * a / 255
		g := uint32(icon.Pixels[i+2]) * a / 255
		b := uint32(icon.Pixels[i+3]) * a / 255
		out[i+0] = byte(b)
		out[i+1] = byte(g)
		out[i+2] = byte(r)
		out[i+3] = byte(a)
	}
	return out
}

func trayWindowProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	trayWndMu.Lock()
	service := trayByHWND[hwnd]
	trayWndMu.Unlock()
	if service == nil {
		ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
		return ret
	}
	switch uint32(msg) {
	case wmTrayWork:
		service.drainJobs()
		return 0
	case wmTrayNotify:
		switch uint32(lparam) {
		case wmLButtonUp, wmRButtonUp, wmContextMenu, ninSelect, ninKeySelect:
			service.showMenu()
		}
		return 0
	case wmCopyData:
		if service.activateCopyData(lparam) {
			return 1
		}
		return 0
	case wmDestroy:
		return 0
	default:
		ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
		return ret
	}
}

func (s *windowsTrayService) showMenu() {
	s.mu.Lock()
	menu := s.menu
	hwnd := s.hwnd
	s.mu.Unlock()
	if hwnd == 0 {
		return
	}
	var pt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	root, created := buildPopup(menu.Items)
	if root == 0 {
		return
	}
	cmd, _, _ := procTrackPopupMenu.Call(
		root,
		tpmRightAlign|tpmBottomAlign|tpmNoNotify|tpmReturnCmd,
		uintptr(pt.x),
		uintptr(pt.y),
		0,
		hwnd,
		0,
	)
	for i := len(created) - 1; i >= 0; i-- {
		procDestroyMenu.Call(created[i])
	}
	procPostMessageW.Call(hwnd, wmNull, 0, 0)
	if cmd != 0 {
		next := uint32(1)
		if fn := menuAction(menu.Items, uint32(cmd), &next); fn != nil {
			fn()
		}
	}
}

func buildPopup(items []TrayMenuItem) (uintptr, []uintptr) {
	root, _, _ := procCreatePopupMenu.Call()
	if root == 0 {
		return 0, nil
	}
	created := []uintptr{root}
	var next uint32 = 1
	appendPopupItems(root, items, &next, &created)
	return root, created
}

func appendPopupItems(menu uintptr, items []TrayMenuItem, next *uint32, created *[]uintptr) {
	for _, item := range items {
		switch item.Kind {
		case TrayMenuSeparator:
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
		case TrayMenuSubmenu:
			child, _, _ := procCreatePopupMenu.Call()
			if child == 0 {
				continue
			}
			*created = append(*created, child)
			appendPopupItems(child, item.Children, next, created)
			text, _ := menuTextPtr(item)
			flags := uintptr(mfPopup)
			if !item.Enabled {
				flags |= mfGrayed
			}
			procAppendMenuW.Call(menu, flags, child, uintptr(unsafe.Pointer(text)))
			runtime.KeepAlive(text)
		default:
			id := uintptr(*next)
			*next++
			text, _ := menuTextPtr(item)
			flags := uintptr(0)
			if !item.Enabled {
				flags |= mfGrayed
			}
			if item.Checked {
				flags |= mfChecked
			}
			procAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(text)))
			runtime.KeepAlive(text)
		}
	}
}

func menuTextPtr(item TrayMenuItem) (*uint16, error) {
	text := item.Text
	if item.Shortcut != nil && item.Shortcut.Key != 0 {
		text += "\t" + shortcutLabel(*item.Shortcut)
	}
	if text == "" {
		text = " "
	}
	return windows.UTF16PtrFromString(text)
}

func shortcutLabel(shortcut TrayMenuShortcut) string {
	var parts []string
	if shortcut.Modifiers&TrayMenuModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if shortcut.Modifiers&TrayMenuModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if shortcut.Modifiers&TrayMenuModShift != 0 {
		parts = append(parts, "Shift")
	}
	if shortcut.Modifiers&TrayMenuModSuper != 0 {
		parts = append(parts, "Win")
	}
	parts = append(parts, string(shortcut.Key))
	out := parts[0]
	for _, part := range parts[1:] {
		out += "+" + part
	}
	return out
}

// menuAction walks items in the same order buildPopup assigns command IDs.
func menuAction(items []TrayMenuItem, want uint32, next *uint32) func() {
	for _, item := range items {
		switch item.Kind {
		case TrayMenuSeparator:
		case TrayMenuSubmenu:
			if fn := menuAction(item.Children, want, next); fn != nil {
				return fn
			}
		default:
			id := *next
			*next++
			if id == want && item.Enabled {
				return item.Activate
			}
		}
	}
	return nil
}

func (s *windowsTrayService) activateCopyData(lparam uintptr) bool {
	if lparam == 0 {
		return false
	}
	data := (*copyData)(uintptrPtr(lparam))
	label := copyDataString(data)
	s.mu.Lock()
	fn := findTrayAction(s.menu.Items, label)
	s.mu.Unlock()
	if fn == nil {
		return false
	}
	fn()
	return true
}

func findTrayAction(items []TrayMenuItem, label string) func() {
	for _, item := range items {
		if item.Kind == TrayMenuSubmenu {
			if fn := findTrayAction(item.Children, label); fn != nil {
				return fn
			}
			continue
		}
		if item.Kind == TrayMenuSeparator || !item.Enabled {
			continue
		}
		if item.Text == label {
			return item.Activate
		}
	}
	return nil
}

func copyDataString(data *copyData) string {
	if data == nil || data.lpData == 0 || data.cbData < 2 {
		return ""
	}
	n := int(data.cbData / 2)
	raw := unsafe.Slice((*uint16)(uintptrPtr(data.lpData)), n)
	return windows.UTF16ToString(raw)
}

// ActivateWindowsTrayItem asks a visible tray to run the menu action with
// this label, on the tray thread, as a stand-in for the user choosing it.
// meltemi-tray --smoke uses it because Windows has no dbusmenu.
func ActivateWindowsTrayItem(label string) error {
	class, err := windows.UTF16PtrFromString(windowsTrayClass)
	if err != nil {
		return err
	}
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), 0)
	if hwnd == 0 {
		return fmt.Errorf("%w: tray window not found", ErrTrayUnavailable)
	}
	encoded, err := windows.UTF16FromString(label)
	if err != nil {
		return err
	}
	if len(encoded) == 0 {
		return errors.New("desktop: empty tray item label")
	}
	payload := copyData{
		cbData: uint32(len(encoded) * 2),
		lpData: uintptr(unsafe.Pointer(&encoded[0])),
	}
	ret, _, _ := procSendMessageW.Call(hwnd, wmCopyData, 0, uintptr(unsafe.Pointer(&payload)))
	runtime.KeepAlive(encoded)
	if ret == 0 {
		return fmt.Errorf("desktop: tray item %q was not activated", label)
	}
	return nil
}
