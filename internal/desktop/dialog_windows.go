//go:build windows

package desktop

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	clsctxInprocServer = 1
	fosOverwritePrompt = 0x00000002
	fosNoChangeDir     = 0x00000008
	fosPickFolders     = 0x00000020
	fosForceFileSystem = 0x00000040
	fosAllowMulti      = 0x00000200
	fosPathMustExist   = 0x00000800
	fosFileMustExist   = 0x00001000
	sigdnFileSysPath   = 0x80058000
	hresultCanceled    = 0x800704C7
	hresultNoInterface = 0x80004002
	hresultPointer     = 0x80004003
	wmClose            = 0x0010
	wmCommand          = 0x0111
	idCancel           = 2
)

var (
	ole32   = windows.NewLazySystemDLL("ole32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")

	procOleInitialize               = ole32.NewProc("OleInitialize")
	procOleUninitialize             = ole32.NewProc("OleUninitialize")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	procPostMessageW                = user32.NewProc("PostMessageW")

	clsidFileOpen   = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	clsidFileSave   = windows.GUID{Data1: 0xC0B4E2F3, Data2: 0xBA21, Data3: 0x4773, Data4: [8]byte{0x8D, 0xBA, 0x33, 0x5E, 0xC9, 0x46, 0xEB, 0x8B}}
	iidFileOpen     = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidFileSave     = windows.GUID{Data1: 0x84BCCD23, Data2: 0x5FDE, Data3: 0x4CDB, Data4: [8]byte{0xAE, 0xA4, 0xAF, 0x64, 0xB8, 0x3D, 0x78, 0xAB}}
	iidShellItem    = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
	iidOleWindow    = windows.GUID{Data1: 0x00000114, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidUnknown      = windows.GUID{Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidDialogEvents = windows.GUID{Data1: 0x973510DB, Data2: 0x7D7F, Data3: 0x452B, Data4: [8]byte{0x89, 0x75, 0x74, 0xA8, 0x58, 0x28, 0xD3, 0x54}}
)

// winFilter is one Windows common-dialog filter. Spec is a semicolon-separated
// list of glob patterns; MIME patterns are dropped because the Win32 dialog
// matches file names, not media types.
type winFilter struct {
	Name string
	Spec string
}

type windowsDialogService struct {
	mu       sync.Mutex
	closed   bool
	requests []*winDialogRequest
}

type winDialogRequest struct {
	service  *windowsDialogService
	callback func(DialogResult, error)
	once     sync.Once
	hwnd     atomic.Uintptr
	canceled atomic.Bool
	done     atomic.Bool
}

type dialogEvents struct {
	vtbl    *dialogEventsVtbl
	refs    int32
	request *winDialogRequest
	dialog  uintptr
}

type dialogEventsVtbl struct {
	queryInterface    uintptr
	addRef            uintptr
	release           uintptr
	onFileOk          uintptr
	onFolderChanging  uintptr
	onFolderChange    uintptr
	onSelectionChange uintptr
	onShareViolation  uintptr
	onTypeChange      uintptr
	onOverwrite       uintptr
}

var (
	dialogEventsOnce sync.Once
	dialogEventsV    dialogEventsVtbl
)

func newPlatformDialogService() DialogService { return &windowsDialogService{} }

func (s *windowsDialogService) Capabilities() Capabilities {
	if s == nil {
		return Capabilities{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Capabilities{}
	}
	return Capabilities{FileDialog: true}
}

func (s *windowsDialogService) Open(options DialogOptions, callback func(DialogResult, error)) (DialogRequest, error) {
	return s.start(true, options, callback)
}

func (s *windowsDialogService) Save(options DialogOptions, callback func(DialogResult, error)) (DialogRequest, error) {
	return s.start(false, options, callback)
}

func (s *windowsDialogService) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	requests := s.requests
	s.requests = nil
	s.mu.Unlock()
	for _, request := range requests {
		_ = request.Close()
	}
	return nil
}

func (s *windowsDialogService) start(open bool, options DialogOptions, callback func(DialogResult, error)) (DialogRequest, error) {
	if callback == nil {
		return nil, errDialogNilCallback
	}
	if s == nil {
		return nil, errDialogClosed
	}
	if err := validateWindowsDialog(options, open); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errDialogClosed
	}
	request := &winDialogRequest{service: s, callback: callback}
	s.requests = append(s.requests, request)
	s.mu.Unlock()

	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		s.runDialog(request, options, open, ready)
	}()
	if err := <-ready; err != nil {
		s.dropRequest(request)
		return nil, err
	}
	return request, nil
}

func (s *windowsDialogService) dropRequest(request *winDialogRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, candidate := range s.requests {
		if candidate == request {
			s.requests = append(s.requests[:i], s.requests[i+1:]...)
			return
		}
	}
}

func (r *winDialogRequest) Close() error {
	if r == nil {
		return nil
	}
	r.canceled.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if hwnd := r.hwnd.Load(); hwnd != 0 {
			_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
			_, _, _ = procPostMessageW.Call(hwnd, wmCommand, idCancel, 0)
			return nil
		}
		if r.done.Load() || time.Now().After(deadline) {
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func (r *winDialogRequest) finish(result DialogResult, err error) {
	r.done.Store(true)
	r.once.Do(func() {
		if r.service != nil {
			r.service.dropRequest(r)
		}
		if r.callback != nil {
			r.callback(result, err)
		}
	})
}

func (s *windowsDialogService) runDialog(request *winDialogRequest, options DialogOptions, open bool, ready chan<- error) {
	hr, _, _ := procOleInitialize.Call(0)
	if failed(hr) {
		ready <- fmt.Errorf("%w: OleInitialize: %s", ErrDialogUnavailable, windows.Errno(hr).Error())
		return
	}
	defer procOleUninitialize.Call()

	clsid, iid := clsidFileOpen, iidFileOpen
	if !open {
		clsid, iid = clsidFileSave, iidFileSave
	}
	var dialog uintptr
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsid)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iid)),
		uintptr(unsafe.Pointer(&dialog)),
	)
	if failed(hr) || dialog == 0 {
		ready <- fmt.Errorf("%w: CoCreateInstance: %s", ErrDialogUnavailable, windows.Errno(hr).Error())
		return
	}
	defer comRelease(dialog)

	events := newDialogEvents(request, dialog)
	defer runtime.KeepAlive(events)
	var cookie uint32
	if hr := comCall(dialog, 7, uintptr(unsafe.Pointer(events)), uintptr(unsafe.Pointer(&cookie))); failed(hr) {
		ready <- fmt.Errorf("%w: dialog advise: %s", ErrDialogUnavailable, windows.Errno(hr).Error())
		return
	}
	defer comCall(dialog, 8, uintptr(cookie))
	runtime.KeepAlive(events)

	if err := configureDialog(dialog, options, open); err != nil {
		ready <- err
		return
	}
	ready <- nil
	if request.canceled.Load() {
		request.finish(DialogResult{}, ErrDialogCanceled)
		return
	}

	hr = comCall(dialog, 3, 0)
	runtime.KeepAlive(events)
	if request.canceled.Load() || uint32(hr) == hresultCanceled {
		request.finish(DialogResult{}, ErrDialogCanceled)
		return
	}
	if failed(hr) {
		request.finish(DialogResult{}, fmt.Errorf("desktop: dialog show: %s", windows.Errno(hr).Error()))
		return
	}
	paths, err := dialogPaths(dialog, open && options.Multiple)
	if err != nil {
		request.finish(DialogResult{}, err)
		return
	}
	if len(paths) == 0 {
		request.finish(DialogResult{}, ErrDialogCanceled)
		return
	}
	request.finish(DialogResult{Paths: paths}, nil)
}

func configureDialog(dialog uintptr, options DialogOptions, open bool) error {
	var current uint32
	if hr := comCall(dialog, 10, uintptr(unsafe.Pointer(&current))); failed(hr) {
		return fmt.Errorf("%w: GetOptions: %s", ErrDialogUnavailable, windows.Errno(hr).Error())
	}
	flags := current | fosForceFileSystem | fosPathMustExist | fosNoChangeDir
	if open {
		flags |= fosFileMustExist
		if options.Multiple {
			flags |= fosAllowMulti
		}
		if options.Directory {
			flags &^= fosFileMustExist
			flags |= fosPickFolders
		}
	} else {
		flags |= fosOverwritePrompt
	}
	if hr := comCall(dialog, 9, uintptr(flags)); failed(hr) {
		return fmt.Errorf("%w: SetOptions: %s", ErrDialogUnavailable, windows.Errno(hr).Error())
	}
	filters, err := windowsFilterSpecs(options.Filters)
	if err != nil {
		return err
	}
	specs, names := filterSpecMemory(filters)
	if len(specs) > 0 {
		if hr := comCall(dialog, 4, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0]))); failed(hr) {
			return fmt.Errorf("desktop: set file types: %s", windows.Errno(hr).Error())
		}
		if index := windowsFilterIndex(filters, options.CurrentFilter); index > 0 {
			_ = comCall(dialog, 5, uintptr(index))
		}
	}
	runtime.KeepAlive(names)
	if options.Title != "" {
		title, err := windows.UTF16PtrFromString(options.Title)
		if err != nil {
			return err
		}
		if hr := comCall(dialog, 17, uintptr(unsafe.Pointer(title))); failed(hr) {
			return fmt.Errorf("desktop: set dialog title: %s", windows.Errno(hr).Error())
		}
	}
	if options.AcceptLabel != "" {
		label, err := windows.UTF16PtrFromString(options.AcceptLabel)
		if err != nil {
			return err
		}
		_ = comCall(dialog, 18, uintptr(unsafe.Pointer(label)))
	}
	folder := options.CurrentFolder
	name := options.CurrentName
	if !open && options.CurrentFile != "" {
		folder = filepath.Dir(options.CurrentFile)
		if name == "" {
			name = filepath.Base(options.CurrentFile)
		}
	}
	if folder != "" {
		if err := setDialogFolder(dialog, folder); err != nil {
			return err
		}
	}
	if name != "" {
		filename, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		if hr := comCall(dialog, 15, uintptr(unsafe.Pointer(filename))); failed(hr) {
			return fmt.Errorf("desktop: set file name: %s", windows.Errno(hr).Error())
		}
	}
	return nil
}

func setDialogFolder(dialog uintptr, folder string) error {
	path, err := windows.UTF16PtrFromString(folder)
	if err != nil {
		return err
	}
	var item uintptr
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(path)),
		0,
		uintptr(unsafe.Pointer(&iidShellItem)),
		uintptr(unsafe.Pointer(&item)),
	)
	if failed(hr) || item == 0 {
		return fmt.Errorf("desktop: current folder: %s", windows.Errno(hr).Error())
	}
	defer comRelease(item)
	if hr := comCall(dialog, 12, item); failed(hr) {
		return fmt.Errorf("desktop: set folder: %s", windows.Errno(hr).Error())
	}
	return nil
}

func dialogPaths(dialog uintptr, multiple bool) ([]string, error) {
	if multiple {
		var items uintptr
		if hr := comCall(dialog, 27, uintptr(unsafe.Pointer(&items))); failed(hr) || items == 0 {
			return nil, fmt.Errorf("desktop: dialog results: %s", windows.Errno(hr).Error())
		}
		defer comRelease(items)
		var count uint32
		if hr := comCall(items, 7, uintptr(unsafe.Pointer(&count))); failed(hr) {
			return nil, fmt.Errorf("desktop: dialog result count: %s", windows.Errno(hr).Error())
		}
		paths := make([]string, 0, count)
		for i := uint32(0); i < count; i++ {
			var item uintptr
			if hr := comCall(items, 8, uintptr(i), uintptr(unsafe.Pointer(&item))); failed(hr) || item == 0 {
				return nil, fmt.Errorf("desktop: dialog result item: %s", windows.Errno(hr).Error())
			}
			path, err := shellItemPath(item)
			comRelease(item)
			if err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		return paths, nil
	}
	var item uintptr
	if hr := comCall(dialog, 20, uintptr(unsafe.Pointer(&item))); failed(hr) || item == 0 {
		return nil, fmt.Errorf("desktop: dialog result: %s", windows.Errno(hr).Error())
	}
	defer comRelease(item)
	path, err := shellItemPath(item)
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

func shellItemPath(item uintptr) (string, error) {
	var name *uint16
	if hr := comCall(item, 5, sigdnFileSysPath, uintptr(unsafe.Pointer(&name))); failed(hr) || name == nil {
		return "", fmt.Errorf("desktop: dialog path: %s", windows.Errno(hr).Error())
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(name))
	return windows.UTF16PtrToString(name), nil
}

func validateWindowsDialog(options DialogOptions, open bool) error {
	if _, err := windowsFilterSpecs(options.Filters); err != nil {
		return err
	}
	if options.CurrentFilter != nil {
		if _, err := windowsFilterSpec(*options.CurrentFilter); err != nil {
			return err
		}
	}
	if options.CurrentFolder != "" && !filepath.IsAbs(options.CurrentFolder) {
		return fmt.Errorf("%w: %q", errDialogFolderNotAbs, options.CurrentFolder)
	}
	if !open && options.CurrentFile != "" && !filepath.IsAbs(options.CurrentFile) {
		return fmt.Errorf("%w: %q", errDialogFileNotAbs, options.CurrentFile)
	}
	return nil
}

func windowsFilterSpecs(filters []FileFilter) ([]winFilter, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	out := make([]winFilter, len(filters))
	for i, filter := range filters {
		converted, err := windowsFilterSpec(filter)
		if err != nil {
			return nil, err
		}
		out[i] = converted
	}
	return out, nil
}

func windowsFilterSpec(filter FileFilter) (winFilter, error) {
	var globs []string
	for _, pattern := range filter.Patterns {
		switch pattern.Kind {
		case FileFilterPatternGlob:
			glob := pattern.Pattern
			if glob == "" {
				continue
			}
			if glob == "*" || glob == "*.*" {
				glob = "*.*"
			}
			globs = append(globs, glob)
		case FileFilterPatternMIMEType:
		default:
			return winFilter{}, fmt.Errorf("%w: %d", errDialogFilterPattern, pattern.Kind)
		}
	}
	spec := strings.Join(globs, ";")
	if spec == "" {
		spec = "*.*"
	}
	name := filter.Name
	if name == "" {
		name = spec
	}
	return winFilter{Name: name, Spec: spec}, nil
}

func windowsFilterIndex(filters []winFilter, current *FileFilter) uint32 {
	if current == nil || len(filters) == 0 {
		return 0
	}
	want, err := windowsFilterSpec(*current)
	if err != nil {
		return 0
	}
	for i, filter := range filters {
		if filter.Name == want.Name && filter.Spec == want.Spec {
			return uint32(i + 1)
		}
	}
	return 0
}

type comFilterSpec struct {
	name *uint16
	spec *uint16
}

func filterSpecMemory(filters []winFilter) ([]comFilterSpec, []unsafe.Pointer) {
	specs := make([]comFilterSpec, len(filters))
	var pinned []unsafe.Pointer
	for i, filter := range filters {
		name, err := windows.UTF16PtrFromString(filter.Name)
		if err != nil {
			name, _ = windows.UTF16PtrFromString("Files")
		}
		spec, err := windows.UTF16PtrFromString(filter.Spec)
		if err != nil {
			spec, _ = windows.UTF16PtrFromString("*.*")
		}
		specs[i] = comFilterSpec{name: name, spec: spec}
		pinned = append(pinned, unsafe.Pointer(name), unsafe.Pointer(spec))
	}
	return specs, pinned
}

func newDialogEvents(request *winDialogRequest, dialog uintptr) *dialogEvents {
	dialogEventsOnce.Do(func() {
		dialogEventsV = dialogEventsVtbl{
			queryInterface:    windows.NewCallback(eventsQueryInterface),
			addRef:            windows.NewCallback(eventsAddRef),
			release:           windows.NewCallback(eventsRelease),
			onFileOk:          windows.NewCallback(eventsOnFileOk),
			onFolderChanging:  windows.NewCallback(eventsOnFolderChanging),
			onFolderChange:    windows.NewCallback(eventsOnFolderChange),
			onSelectionChange: windows.NewCallback(eventsOnSelectionChange),
			onShareViolation:  windows.NewCallback(eventsOnShareViolation),
			onTypeChange:      windows.NewCallback(eventsOnTypeChange),
			onOverwrite:       windows.NewCallback(eventsOnOverwrite),
		}
	})
	return &dialogEvents{vtbl: &dialogEventsV, refs: 1, request: request, dialog: dialog}
}

func eventsQueryInterface(this, riid, ppv uintptr) uintptr {
	if ppv == 0 {
		return hresultPointer
	}
	*(*uintptr)(uintptrPtr(ppv)) = 0
	if riid == 0 {
		return hresultPointer
	}
	id := *(*windows.GUID)(uintptrPtr(riid))
	if id != iidUnknown && id != iidDialogEvents {
		return hresultNoInterface
	}
	*(*uintptr)(uintptrPtr(ppv)) = this
	eventsAddRef(this)
	return 0
}

func eventsAddRef(this uintptr) uintptr {
	events := (*dialogEvents)(uintptrPtr(this))
	return uintptr(atomic.AddInt32(&events.refs, 1))
}

func eventsRelease(this uintptr) uintptr {
	events := (*dialogEvents)(uintptrPtr(this))
	return uintptr(atomic.AddInt32(&events.refs, -1))
}

func eventsOnFileOk(this, _ uintptr) uintptr {
	eventsNoteWindow((*dialogEvents)(uintptrPtr(this)))
	return 0
}

func eventsOnFolderChanging(this, _, _ uintptr) uintptr {
	eventsNoteWindow((*dialogEvents)(uintptrPtr(this)))
	return 0
}

func eventsOnFolderChange(this, _ uintptr) uintptr {
	eventsNoteWindow((*dialogEvents)(uintptrPtr(this)))
	return 0
}

func eventsOnSelectionChange(this, _ uintptr) uintptr {
	eventsNoteWindow((*dialogEvents)(uintptrPtr(this)))
	return 0
}

func eventsOnShareViolation(this, _, _, _ uintptr) uintptr {
	eventsNoteWindow((*dialogEvents)(uintptrPtr(this)))
	return 0
}

func eventsOnTypeChange(this, _ uintptr) uintptr {
	eventsNoteWindow((*dialogEvents)(uintptrPtr(this)))
	return 0
}

func eventsOnOverwrite(this, _, _, _ uintptr) uintptr {
	eventsNoteWindow((*dialogEvents)(uintptrPtr(this)))
	return 0
}

func eventsNoteWindow(events *dialogEvents) {
	if events == nil || events.request == nil || events.dialog == 0 {
		return
	}
	hwnd := dialogHWND(events.dialog)
	if hwnd == 0 {
		return
	}
	events.request.hwnd.Store(hwnd)
	if events.request.canceled.Load() {
		comCall(events.dialog, 23, hresultCanceled)
		_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
}

func dialogHWND(dialog uintptr) uintptr {
	var ole uintptr
	if hr := comCall(dialog, 0, uintptr(unsafe.Pointer(&iidOleWindow)), uintptr(unsafe.Pointer(&ole))); failed(hr) || ole == 0 {
		return 0
	}
	defer comRelease(ole)
	var hwnd uintptr
	if hr := comCall(ole, 3, uintptr(unsafe.Pointer(&hwnd))); failed(hr) {
		return 0
	}
	return hwnd
}

func comCall(obj uintptr, index int, args ...uintptr) uintptr {
	if obj == 0 {
		return hresultPointer
	}
	call := append([]uintptr{obj}, args...)
	r, _, _ := syscallSyscallN(comVtbl(obj, index), call...)
	return r
}

func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2)
	}
}

func comVtbl(obj uintptr, index int) uintptr {
	table := *(*uintptr)(uintptrPtr(obj))
	return *(*uintptr)(unsafe.Add(uintptrPtr(table), index*int(unsafe.Sizeof(uintptr(0)))))
}

// uintptrPtr reinterprets a Windows callback address. syscall callbacks pass
// pointers as uintptr, and unsafe.Pointer(uintptr) is rejected by go vet.
func uintptrPtr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

// syscallSyscallN is a seam so the call site stays obvious in COM traces.
func syscallSyscallN(trap uintptr, args ...uintptr) (uintptr, uintptr, error) {
	return syscall.SyscallN(trap, args...)
}
