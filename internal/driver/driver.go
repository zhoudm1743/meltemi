package driver

import "fmt"

// Handler receives window and input events.
type Handler func(Event)

// Window is a native top-level window driven by a Backend.
type Window interface {
	Show()
	Hide()
	Destroy()
	Size() Size
	SetHandler(Handler)
}

// TitleWindow is a Window whose native title can be updated after creation.
type TitleWindow interface {
	Window
	SetTitle(string)
}

// CompositionWindow exposes input-method composition state for a native window.
// All methods follow the Window UI-thread contract; SetSpot uses client coordinates.
type CompositionWindow interface {
	Window
	Preedit() (text string, caret int, active bool)
	SetSpot(x, y int)
}

// Clipboard is a text clipboard, bound to a window's lifetime. Like Window
// methods it must be used on the UI thread; once the window is destroyed
// every method reports an error and never touches native state. A window
// exposes it through a type assertion, mirroring how FrameWindow stays an
// optional extension of Window.
type Clipboard interface {
	// Text returns the current clipboard text. An empty string with a nil
	// error means the clipboard holds no text.
	Text() (string, error)
	// SetText replaces the clipboard contents with s.
	SetText(s string) error
}

// FrameWindow is a Window with native frame controls — size, position,
// iconification and maximization state. Custom-drawn title bars use it to
// drive a window created with Config.Frameless. All methods follow the same
// UI-thread contract as Window.
type FrameWindow interface {
	Window
	SetSize(Size) error
	Position() Point
	SetPosition(Point)
	Iconify()
	IsIconified() bool
	Maximize()
	Restore()
	IsMaximized() bool
}

// Backend creates and drives native windows and their event loop.
type Backend interface {
	Init() error
	Terminate()
	NewWindow(Config) (Window, error)
	PollEvents()
	WaitEvents()
	Wake()
}

// errInvalidSize reports a non-positive window size.
func errInvalidSize(size Size) error {
	return fmt.Errorf("driver: invalid window size %dx%d", size.Width, size.Height)
}
