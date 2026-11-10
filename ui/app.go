package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"golang.org/x/image/font/gofont/goregular"

	internalapp "github.com/zhoudm1743/meltemi/internal/app"
	"github.com/zhoudm1743/meltemi/internal/desktop"
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// DefaultFont is the registry name of the font data the default text face is
// built from. The built-in data behind this name is GoRegular at
// defaultFontSize; registering different data under DefaultFont before the
// first window is shown replaces it, so applications can render scripts the
// built-in font lacks, such as CJK.
const DefaultFont = "GoRegular"

// defaultFontSize is the fixed logical size of the app-owned default face.
const defaultFontSize = 18.0

// LastWindowAction controls what Run does after the last visible window has
// been hidden or destroyed.
type LastWindowAction uint8

const (
	// LastWindowQuit is the default: losing the last visible window quits Run.
	LastWindowQuit LastWindowAction = iota
	// LastWindowKeepRunning leaves Run active so a window can be shown again.
	LastWindowKeepRunning
)

// App owns the UI event loop, all live windows and their default and styled
// text Faces. Window and component state is confined to the UI thread; Post is
// safe from any goroutine.
type App struct {
	core        *internalapp.App
	newRenderer func(render.Target) (renderer, error)
	newFace     func() (*text.Face, error)
	closeFace   func(*text.Face) error

	windowsMu sync.RWMutex
	windows   []*Window

	newTrayService func() desktop.TrayService
	traysMu        sync.Mutex
	trays          []*Tray
	traysClosing   bool

	newDialogService func() desktop.DialogService
	dialogsMu        sync.Mutex
	dialogService    desktop.DialogService
	dialogs          []*FileDialog
	dialogsClosing   bool

	lastWindowAction     atomic.Uint32
	lastWindowCallbackMu sync.RWMutex
	lastWindowCallback   func() LastWindowAction
	quitOnce             sync.Once
	imeMu                sync.Mutex
	runStarted           bool

	errMu    sync.Mutex
	firstErr error

	fontMu      sync.Mutex
	fonts       map[string][]byte
	faceCreated bool

	face      *text.Face
	faceCache map[styleFaceKey]*text.Face
	sheet     *internalstyle.Sheet
	stopping  bool
}

type styleFaceKey struct {
	family string
	size   float64
}

// LoadStyles reads and validates a stylesheet before atomically replacing the
// sheet used by existing and future windows. The UI-thread application redraws
// every existing window once through postResult.
func (a *App) LoadStyles(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ui: load styles %q: %w", path, err)
	}
	sheet, err := internalstyle.Parse(bytes.NewReader(data))
	if err != nil {
		var cssErr *internalstyle.Error
		if errors.As(err, &cssErr) {
			return cssErr.WithFile(path)
		}
		return fmt.Errorf("ui: load styles %q: %w", path, err)
	}
	if err := a.validateStyleFonts(sheet, path); err != nil {
		return err
	}
	return a.postResult(func() error {
		transaction := &styleTransaction{app: a}
		for _, window := range a.Windows() {
			if err := window.prepareStyles(transaction, sheet); err != nil {
				return fmt.Errorf("ui: apply styles %q: %w", path, transaction.fail(err))
			}
		}
		transaction.commit()
		a.sheet = sheet
		return nil
	})
}

func (a *App) validateStyleFonts(sheet *internalstyle.Sheet, path string) error {
	a.fontMu.Lock()
	defer a.fontMu.Unlock()
	for _, rule := range sheet.Rules {
		for _, property := range rule.Properties {
			if property.Name != "font-family" {
				continue
			}
			family := property.Value
			if family == DefaultFont {
				continue
			}
			if _, ok := a.fonts[family]; !ok {
				return &internalstyle.Error{
					File: path, Line: property.Line, Column: property.Column,
					Property: property.Name, Message: fmt.Sprintf("unregistered font family %q", family),
				}
			}
		}
	}
	return nil
}

type resolvedStyleReceiver interface {
	styleMeta() internalstyle.Meta
	setResolved(internalstyle.Resolved)
}

type styleAssignment struct {
	receiver     resolvedStyleReceiver
	textReceiver faceReceiver
	resolved     internalstyle.Resolved
	face         *text.Face
}

type styleTransaction struct {
	app         *App
	assignments []styleAssignment
	defaultFace *text.Face
	faces       map[styleFaceKey]*text.Face
}

func (a *App) fontData(family string) ([]byte, bool) {
	a.fontMu.Lock()
	defer a.fontMu.Unlock()
	data, ok := a.fonts[family]
	if family == DefaultFont && !ok {
		data = goregular.TTF
		ok = true
	}
	return data, ok
}

func (t *styleTransaction) faceFor(resolved internalstyle.Resolved) (*text.Face, error) {
	style := defaultComponentStyle().merge(resolved)
	family, size := style.fontFamily, style.fontSize
	if family == DefaultFont && size == defaultFontSize {
		if t.app.face != nil {
			return t.app.face, nil
		}
		if t.defaultFace != nil {
			return t.defaultFace, nil
		}
		face, err := t.app.newFace()
		if err != nil {
			return nil, err
		}
		t.defaultFace = face
		return face, nil
	}
	key := styleFaceKey{family: family, size: size}
	if face := t.faces[key]; face != nil {
		return face, nil
	}
	if face := t.app.faceCache[key]; face != nil {
		return face, nil
	}
	data, ok := t.app.fontData(family)
	if !ok {
		return nil, fmt.Errorf("ui: font family %q is not registered", family)
	}
	face, err := text.NewFace(data, size)
	if err != nil {
		return nil, fmt.Errorf("ui: create font face %q at %vpx: %w", family, size, err)
	}
	if t.faces == nil {
		t.faces = make(map[styleFaceKey]*text.Face)
	}
	t.faces[key] = face
	return face, nil
}

func (t *styleTransaction) prepare(root component, sheet *internalstyle.Sheet) error {
	if root == nil {
		return nil
	}
	_, err := t.prepareComponent(root, sheet, unresolvedStyle())
	return err
}

func (t *styleTransaction) prepareComponent(root component, sheet *internalstyle.Sheet, inherited internalstyle.Resolved) (internalstyle.Resolved, error) {
	receiver, ok := root.(resolvedStyleReceiver)
	if !ok {
		return inherited, nil
	}
	resolved := unresolvedStyle()
	if sheet != nil {
		resolved = sheet.Resolve(receiver.styleMeta(), inherited)
	}
	assignment := styleAssignment{receiver: receiver, resolved: resolved}
	if textReceiver, ok := root.(faceReceiver); ok {
		face, err := t.faceFor(resolved)
		if err != nil {
			return resolved, err
		}
		assignment.textReceiver = textReceiver
		assignment.face = face
	}
	t.assignments = append(t.assignments, assignment)
	if container, ok := root.(childContainer); ok {
		for _, child := range container.childComponents() {
			if _, err := t.prepareComponent(child, sheet, resolved); err != nil {
				return resolved, err
			}
		}
	}
	return resolved, nil
}

func (t *styleTransaction) commit() {
	if t.defaultFace != nil {
		t.app.face = t.defaultFace
		t.app.markFaceCreated()
		t.defaultFace = nil
	}
	if len(t.faces) != 0 {
		if t.app.faceCache == nil {
			t.app.faceCache = make(map[styleFaceKey]*text.Face)
		}
		for key, face := range t.faces {
			t.app.faceCache[key] = face
		}
		t.faces = nil
	}
	for _, assignment := range t.assignments {
		assignment.receiver.setResolved(assignment.resolved)
		if assignment.textReceiver != nil {
			assignment.textReceiver.setFace(assignment.face)
		}
	}
}

func (t *styleTransaction) fail(cause error) error {
	var first error
	if t.defaultFace != nil {
		if err := t.app.closeFace(t.defaultFace); err != nil {
			first = err
		}
		t.defaultFace = nil
	}
	for key, face := range t.faces {
		if err := t.app.closeFace(face); err != nil && first == nil {
			first = err
		}
		delete(t.faces, key)
	}
	if first != nil {
		return fmt.Errorf("%w (style face cleanup: %v)", cause, first)
	}
	return cause
}

func (a *App) applyStyleTree(root component, sheet *internalstyle.Sheet) error {
	transaction := &styleTransaction{app: a}
	if err := transaction.prepare(root, sheet); err != nil {
		return transaction.fail(err)
	}
	transaction.commit()
	return nil
}

func (a *App) applyWindowStyles(window *Window, sheet *internalstyle.Sheet) error {
	transaction := &styleTransaction{app: a}
	if err := window.prepareStyles(transaction, sheet); err != nil {
		return transaction.fail(err)
	}
	transaction.commit()
	return nil
}

// New returns an application backed by GLFW and OpenGL. The backend and its
// resources are not initialized until Run.
func New() *App {
	a := &App{
		core:             newDefaultCore(),
		newRenderer:      newGLRenderer,
		newTrayService:   desktop.NewTrayService,
		newDialogService: desktop.NewDialogService,
		closeFace:        func(face *text.Face) error { return face.Close() },
	}
	a.newFace = a.createDefaultFace
	return a
}

// RegisterFont registers TTF/OTF data under name, replacing any previous
// registration of that name. The app-owned text face is built from the data
// registered under DefaultFont when the first window is shown, so
// RegisterFont(DefaultFont, data) replaces the built-in GoRegular face.
//
// Registration must happen before the first window is shown (typically
// before Run); once the face exists the call fails, since a live face cannot
// swap its data. The data is validated eagerly so an unusable font fails
// here rather than mid-Run, and it is copied, so callers may release their
// buffer after the call.
//
// Registration is only needed to choose a named primary typeface: faces
// automatically fall back to system fonts (via fontconfig on Linux) for
// runes the registered data does not cover, so CJK text no longer requires
// registering a CJK font.
func (a *App) RegisterFont(name string, data []byte) error {
	if name == "" {
		return errors.New("ui: register font: empty name")
	}
	if len(data) == 0 {
		return errors.New("ui: register font: empty font data")
	}
	probe, err := text.NewFace(data, defaultFontSize)
	if err != nil {
		return fmt.Errorf("ui: register font %q: %w", name, err)
	}
	_ = probe.Close()

	a.fontMu.Lock()
	defer a.fontMu.Unlock()
	if a.faceCreated {
		return errors.New("ui: register font: the app text face already exists; register before the first window is shown")
	}
	if a.fonts == nil {
		a.fonts = make(map[string][]byte)
	}
	a.fonts[name] = bytes.Clone(data)
	return nil
}

// createDefaultFace builds the shared text face from the data registered
// under DefaultFont, falling back to the built-in GoRegular. The face
// shapes with HarfBuzz on Linux (see internal/text) and covers missing
// runes from system fonts automatically.
func (a *App) createDefaultFace() (*text.Face, error) {
	return text.NewFace(a.defaultFontData(), defaultFontSize)
}

func (a *App) defaultFontData() []byte {
	a.fontMu.Lock()
	defer a.fontMu.Unlock()
	if data, ok := a.fonts[DefaultFont]; ok {
		return data
	}
	return goregular.TTF
}

// markFaceCreated freezes font registration: the shared face now lives on
// the UI thread and its data can no longer be swapped.
func (a *App) markFaceCreated() {
	a.fontMu.Lock()
	defer a.fontMu.Unlock()
	a.faceCreated = true
}

// EnableIME selects the D-Bus composition backend. It must be called before
// Run so backend initialization observes the process environment.
func (a *App) EnableIME() error {
	a.imeMu.Lock()
	defer a.imeMu.Unlock()
	if a.runStarted {
		return errors.New("ui: enable IME: call before Run")
	}
	if err := os.Setenv("MELTEMI_IME", "1"); err != nil {
		return fmt.Errorf("ui: enable IME: set MELTEMI_IME: %w", err)
	}
	if err := os.Setenv("XMODIFIERS", "@im=none"); err != nil {
		return fmt.Errorf("ui: enable IME: set XMODIFIERS: %w", err)
	}
	return nil
}

// Run locks the calling goroutine to the UI OS thread and processes posted
// work and native events until Quit. It returns the first UI or rendering
// error, preferring that original error over any later cleanup failure.
func (a *App) Run() error {
	a.imeMu.Lock()
	a.runStarted = true
	a.imeMu.Unlock()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	runErr := a.core.Run()
	a.noteErr(runErr)
	a.noteErr(a.closeTrays())
	a.noteErr(a.closeDialogs())
	return a.err()
}

// Quit releases trays and invalidates in-flight file dialogs directly (their
// portal requests are canceled without waiting on the UI queue or invoking
// their callbacks), then asks the UI thread to release all remaining window
// renderers, native windows and the app-owned Face before stopping the
// internal event loop. Calls after the first are no-ops.
func (a *App) Quit() {
	a.quitOnce.Do(func() {
		a.noteErr(a.closeTrays())
		a.noteErr(a.closeDialogs())
		if err := a.post(func() {
			a.stopping = true
			a.cleanup()
			a.core.Quit()
		}); err != nil {
			a.noteErr(err)
		}
	})
}

// Post queues fn on the UI thread. After fn returns, every existing window is
// redrawn once so component setters need no separate invalidation call. No
// background redraw loop is introduced.
func (a *App) Post(fn func()) error {
	return a.post(fn)
}

func (a *App) post(fn func()) error {
	if fn == nil {
		return nil
	}
	return a.core.Post(func() {
		fn()
		if !a.stopping {
			a.redrawAll()
		}
	})
}

// postResult queues a fallible window operation and returns only whether it
// could be posted. The operation's own error is retained for Run, and skips
// the operation's following redraw so a preserved window cannot fail again in
// Begin and turn a local close failure into a global shutdown.
func (a *App) postResult(fn func() error) error {
	if fn == nil {
		return nil
	}
	return a.core.Post(func() {
		if a.stopping {
			return
		}
		if err := fn(); err != nil {
			a.noteErr(err)
			return
		}
		if !a.stopping {
			a.redrawAll()
		}
	})
}

// Window returns a detached window configuration. Its native resources are
// created by Show on the UI thread.
func (a *App) Window(title string) *Window {
	config := driver.DefaultConfig()
	config.Title = title
	root := Column()
	return &Window{app: a, config: config, root: root, content: root}
}

// Windows returns a copy of the live, successfully created windows in creation
// order. Mutating the slice does not change the app's window set.
func (a *App) Windows() []*Window {
	a.windowsMu.RLock()
	defer a.windowsMu.RUnlock()
	return append([]*Window(nil), a.windows...)
}

// SetLastWindowAction changes the static policy applied after the last visible
// window is hidden or destroyed. A vetoed close does not trigger it.
func (a *App) SetLastWindowAction(action LastWindowAction) {
	a.lastWindowAction.Store(uint32(action))
}

// SetLastWindowCallback installs a UI-thread policy callback. Its result takes
// precedence over the static action; nil restores static-only behavior.
func (a *App) SetLastWindowCallback(callback func() LastWindowAction) {
	a.lastWindowCallbackMu.Lock()
	defer a.lastWindowCallbackMu.Unlock()
	a.lastWindowCallback = callback
}

func (a *App) applyLastWindowPolicy() {
	action := LastWindowAction(a.lastWindowAction.Load())
	a.lastWindowCallbackMu.RLock()
	callback := a.lastWindowCallback
	a.lastWindowCallbackMu.RUnlock()
	if callback != nil {
		action = callback()
	}
	if action == LastWindowQuit {
		a.Quit()
	}
}

func (a *App) addWindow(w *Window) {
	a.windowsMu.Lock()
	defer a.windowsMu.Unlock()
	a.windows = append(a.windows, w)
}

func (a *App) removeWindow(w *Window) {
	a.windowsMu.Lock()
	defer a.windowsMu.Unlock()
	for i, candidate := range a.windows {
		if candidate == w {
			a.windows = append(a.windows[:i], a.windows[i+1:]...)
			return
		}
	}
}

func (a *App) windowCount() int {
	a.windowsMu.RLock()
	defer a.windowsMu.RUnlock()
	return len(a.windows)
}

func (a *App) visibleWindowCount() int {
	a.windowsMu.RLock()
	defer a.windowsMu.RUnlock()
	count := 0
	for _, window := range a.windows {
		if window.visible {
			count++
		}
	}
	return count
}

func (a *App) redrawAll() {
	for _, w := range a.Windows() {
		if err := w.redraw(); err != nil {
			a.fail(fmt.Errorf("ui: redraw: %w", err))
			return
		}
	}
}

func (a *App) fail(err error) {
	if err == nil {
		return
	}
	a.noteErr(err)
	a.stopping = true
	a.cleanup()
	a.Quit()
}

// cleanup performs final best-effort unwinding in reverse creation order.
// Unlike a runtime Close, finalization invalidates public handles even when
// context binding fails: native destruction then reclaims the GL resources.
// Every native window is unregistered exactly once before all Faces are released.
func (a *App) cleanup() {
	windows := a.Windows()
	for i := len(windows) - 1; i >= 0; i-- {
		a.noteErr(windows[i].finalize())
	}
	if a.windowCount() == 0 {
		a.noteErr(a.releaseFace())
	}
}

func (a *App) releaseFace() error {
	if a.face == nil && len(a.faceCache) == 0 {
		return nil
	}
	faces := make([]*text.Face, 0, len(a.faceCache)+1)
	seen := make(map[*text.Face]bool)
	if a.face != nil {
		faces = append(faces, a.face)
		seen[a.face] = true
	}
	for _, face := range a.faceCache {
		if face != nil && !seen[face] {
			faces = append(faces, face)
			seen[face] = true
		}
	}
	a.face = nil
	a.faceCache = nil

	var first error
	for _, face := range faces {
		if err := a.closeFace(face); err != nil && first == nil {
			first = fmt.Errorf("ui: close face: %w", err)
		}
	}
	return first
}

func (a *App) noteErr(err error) {
	if err == nil {
		return
	}
	a.errMu.Lock()
	defer a.errMu.Unlock()
	if a.firstErr == nil {
		a.firstErr = err
	}
}

func (a *App) err() error {
	a.errMu.Lock()
	defer a.errMu.Unlock()
	return a.firstErr
}
