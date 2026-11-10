package driver

import "image"

// Size is the integer width and height of a window or framebuffer.
type Size struct {
	Width  int
	Height int
}

// Point is a floating-point position, typically in screen or window
// coordinates.
type Point struct {
	X float64
	Y float64
}

// Config holds the parameters used to create a new window.
type Config struct {
	Title   string
	Size    Size
	MinSize Size
	Icon    image.Image
	// Frameless requests a window without native decorations — no title bar
	// or border — for apps that draw their own. The default config stays
	// decorated.
	Frameless bool
}

// DefaultConfig returns the standard window configuration used by Meltemi.
func DefaultConfig() Config {
	return Config{
		Title: "Meltemi",
		Size:  Size{Width: 800, Height: 600},
	}
}

// Validate reports whether the configuration is usable to create a window.
func (c Config) Validate() error {
	if c.Size.Width <= 0 || c.Size.Height <= 0 {
		return errInvalidSize(c.Size)
	}
	return nil
}

// EventKind identifies the type of an Event.
type EventKind uint8

const (
	EventCloseRequested EventKind = iota + 1
	EventResize
	EventFramebufferResize
	EventPaint
	EventMouseMove
	EventMouseButton
	EventKey
	EventRune
	EventCursorLeave
	EventCompositionStart
	EventCompositionUpdate
	EventCompositionEnd
	// EventScroll is a wheel or trackpad scroll. ScrollX and ScrollY carry
	// the delta in wheel lines (1 is one wheel line; trackpads emit smooth
	// fractions), with negative values for the opposite direction.
	EventScroll
	// EventFocus reports a native focus change; Focused carries the new state.
	EventFocus
)

// MouseButton identifies a mouse button.
type MouseButton int

const (
	MouseButtonLeft MouseButton = iota
	MouseButtonMiddle
	MouseButtonRight
)

// Action identifies a button or key action.
type Action int

const (
	ActionPress Action = iota
	ActionRelease
	ActionRepeat
)

// Key identifies a keyboard key.
type Key int

const (
	KeyUnknown Key = iota
	KeyA
	KeyEscape
	KeyEnter
	KeyTab
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyC
	KeyV
	KeyX
	KeyZ
	KeyY
	// KeySpace is the only key beyond editing shortcuts the controls need;
	// Checkbox activates on it while focused.
	KeySpace
	// ASCII letter and digit keys for menu shortcuts. The letters the editing
	// controls need (A, C, V, X, Z, Y) keep their values above; the remaining
	// letters and the digits are appended so existing key values never change.
	KeyB
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyI
	KeyJ
	KeyK
	KeyL
	KeyM
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyU
	KeyW
	Key0
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9
	// KeyPageUp and KeyPageDown drive TextArea page navigation. They are
	// appended after the existing keys so earlier values never change.
	KeyPageUp
	KeyPageDown
)

// Modifiers is a bitmask of active modifier keys.
type Modifiers uint8

const (
	ModShift Modifiers = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
)

// Shortcut is the backend-independent description of a keyboard shortcut:
// the exact modifier mask held at press time plus the normalized rune of an
// ASCII letter or digit key. Letters are normalized to lowercase, so Shift
// stays expressed only through Mods and never through the rune case.
type Shortcut struct {
	Mods Modifiers
	Key  rune
}

// Shortcut returns the shortcut data carried by a key event: the exact
// modifier mask and the normalized rune of the key. The second result is
// false for non-key events and for keys outside the ASCII letters and
// digits, so shortcut matching can never involve anything else. Action
// filtering (press and repeat only) is the caller's job.
func (e Event) Shortcut() (Shortcut, bool) {
	if e.Kind != EventKey {
		return Shortcut{}, false
	}
	key, ok := shortcutRune(e.Key)
	if !ok {
		return Shortcut{}, false
	}
	return Shortcut{Mods: e.Mods, Key: key}, true
}

// shortcutRune maps an ASCII letter or digit key to its normalized shortcut
// rune: lowercase for letters, the digit itself for digits. Every other key
// reports false.
func shortcutRune(key Key) (rune, bool) {
	switch key {
	case KeyA:
		return 'a', true
	case KeyB:
		return 'b', true
	case KeyC:
		return 'c', true
	case KeyD:
		return 'd', true
	case KeyE:
		return 'e', true
	case KeyF:
		return 'f', true
	case KeyG:
		return 'g', true
	case KeyH:
		return 'h', true
	case KeyI:
		return 'i', true
	case KeyJ:
		return 'j', true
	case KeyK:
		return 'k', true
	case KeyL:
		return 'l', true
	case KeyM:
		return 'm', true
	case KeyN:
		return 'n', true
	case KeyO:
		return 'o', true
	case KeyP:
		return 'p', true
	case KeyQ:
		return 'q', true
	case KeyR:
		return 'r', true
	case KeyS:
		return 's', true
	case KeyT:
		return 't', true
	case KeyU:
		return 'u', true
	case KeyV:
		return 'v', true
	case KeyW:
		return 'w', true
	case KeyX:
		return 'x', true
	case KeyY:
		return 'y', true
	case KeyZ:
		return 'z', true
	case Key0:
		return '0', true
	case Key1:
		return '1', true
	case Key2:
		return '2', true
	case Key3:
		return '3', true
	case Key4:
		return '4', true
	case Key5:
		return '5', true
	case Key6:
		return '6', true
	case Key7:
		return '7', true
	case Key8:
		return '8', true
	case Key9:
		return '9', true
	}
	return 0, false
}

// Event is a single window or input event delivered to a Handler.
type Event struct {
	Kind     EventKind
	Window   Window
	Size     Size
	Focused  bool
	Position Point
	Button   MouseButton
	Action   Action
	Key      Key
	ScanCode int
	KeySym   uint32
	Mods     Modifiers
	Rune     rune
	ScrollX  float64
	ScrollY  float64
}
