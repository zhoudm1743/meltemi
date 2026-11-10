package glfw

import (
	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

// mapKey converts a GLFW key to the driver key set. Keys outside that set map
// to driver.KeyUnknown.
func mapKey(key glfw3.Key) driver.Key {
	switch key {
	case glfw3.KeyA:
		return driver.KeyA
	case glfw3.KeyEscape:
		return driver.KeyEscape
	case glfw3.KeyEnter:
		return driver.KeyEnter
	case glfw3.KeyTab:
		return driver.KeyTab
	case glfw3.KeyBackspace:
		return driver.KeyBackspace
	case glfw3.KeyDelete:
		return driver.KeyDelete
	case glfw3.KeyLeft:
		return driver.KeyLeft
	case glfw3.KeyRight:
		return driver.KeyRight
	case glfw3.KeyUp:
		return driver.KeyUp
	case glfw3.KeyDown:
		return driver.KeyDown
	case glfw3.KeyHome:
		return driver.KeyHome
	case glfw3.KeyEnd:
		return driver.KeyEnd
	case glfw3.KeyC:
		return driver.KeyC
	case glfw3.KeyV:
		return driver.KeyV
	case glfw3.KeyX:
		return driver.KeyX
	case glfw3.KeyZ:
		return driver.KeyZ
	case glfw3.KeyY:
		return driver.KeyY
	case glfw3.KeySpace:
		return driver.KeySpace
	case glfw3.KeyB:
		return driver.KeyB
	case glfw3.KeyD:
		return driver.KeyD
	case glfw3.KeyE:
		return driver.KeyE
	case glfw3.KeyF:
		return driver.KeyF
	case glfw3.KeyG:
		return driver.KeyG
	case glfw3.KeyH:
		return driver.KeyH
	case glfw3.KeyI:
		return driver.KeyI
	case glfw3.KeyJ:
		return driver.KeyJ
	case glfw3.KeyK:
		return driver.KeyK
	case glfw3.KeyL:
		return driver.KeyL
	case glfw3.KeyM:
		return driver.KeyM
	case glfw3.KeyN:
		return driver.KeyN
	case glfw3.KeyO:
		return driver.KeyO
	case glfw3.KeyP:
		return driver.KeyP
	case glfw3.KeyQ:
		return driver.KeyQ
	case glfw3.KeyR:
		return driver.KeyR
	case glfw3.KeyS:
		return driver.KeyS
	case glfw3.KeyT:
		return driver.KeyT
	case glfw3.KeyU:
		return driver.KeyU
	case glfw3.KeyW:
		return driver.KeyW
	case glfw3.Key0:
		return driver.Key0
	case glfw3.Key1:
		return driver.Key1
	case glfw3.Key2:
		return driver.Key2
	case glfw3.Key3:
		return driver.Key3
	case glfw3.Key4:
		return driver.Key4
	case glfw3.Key5:
		return driver.Key5
	case glfw3.Key6:
		return driver.Key6
	case glfw3.Key7:
		return driver.Key7
	case glfw3.Key8:
		return driver.Key8
	case glfw3.Key9:
		return driver.Key9
	case glfw3.KeyPageUp:
		return driver.KeyPageUp
	case glfw3.KeyPageDown:
		return driver.KeyPageDown
	default:
		return driver.KeyUnknown
	}
}

// mapMouseButton converts a GLFW mouse button. The second result is false
// for buttons outside the driver contract (side buttons and later), which
// must be dropped rather than misreported as the left button.
func mapMouseButton(button glfw3.MouseButton) (driver.MouseButton, bool) {
	switch button {
	case glfw3.MouseButtonLeft:
		return driver.MouseButtonLeft, true
	case glfw3.MouseButtonMiddle:
		return driver.MouseButtonMiddle, true
	case glfw3.MouseButtonRight:
		return driver.MouseButtonRight, true
	default:
		return 0, false
	}
}

// mapAction converts a GLFW button or key action. GLFW only produces press,
// release and repeat; anything else falls back to press.
func mapAction(action glfw3.Action) driver.Action {
	switch action {
	case glfw3.Release:
		return driver.ActionRelease
	case glfw3.Repeat:
		return driver.ActionRepeat
	default:
		return driver.ActionPress
	}
}

// mapModifiers converts a GLFW modifier mask to the driver bitmask.
func mapModifiers(mods glfw3.ModifierKey) driver.Modifiers {
	var out driver.Modifiers
	if mods&glfw3.ModShift != 0 {
		out |= driver.ModShift
	}
	if mods&glfw3.ModControl != 0 {
		out |= driver.ModCtrl
	}
	if mods&glfw3.ModAlt != 0 {
		out |= driver.ModAlt
	}
	if mods&glfw3.ModSuper != 0 {
		out |= driver.ModSuper
	}
	return out
}

// emit delivers an event to the window handler on the UI thread. GLFW invokes
// callbacks from PollEvents and WaitEvents, which the app only calls on the
// UI thread, so no synchronization is needed here. A logically destroyed
// window receives no further ordinary events; its final CompositionEnd is
// delivered separately while closing composition.
func (w *Window) emit(e driver.Event) {
	if w.destroyed {
		return
	}
	e.Window = w
	if h := w.handler; h != nil {
		h(e)
	}
}

// installCallbacks converts GLFW window and input callbacks into driver
// events. With D-Bus IME enabled, keys offer themselves to Fcitx first and only
// unhandled keys follow the original key/character path.
func (w *Window) installCallbacks() {
	w.win.SetCloseCallback(func(*glfw3.Window) {
		w.emit(driver.Event{Kind: driver.EventCloseRequested})
	})
	w.win.SetSizeCallback(func(_ *glfw3.Window, width, height int) {
		w.emit(driver.Event{
			Kind: driver.EventResize,
			Size: driver.Size{Width: width, Height: height},
		})
	})
	w.win.SetFramebufferSizeCallback(func(_ *glfw3.Window, width, height int) {
		w.emit(driver.Event{
			Kind: driver.EventFramebufferResize,
			Size: driver.Size{Width: width, Height: height},
		})
	})
	w.win.SetRefreshCallback(func(*glfw3.Window) {
		w.emit(driver.Event{Kind: driver.EventPaint})
	})
	w.win.SetFocusCallback(func(_ *glfw3.Window, focused bool) {
		w.handleXIMFocus(focused)
		w.emit(driver.Event{Kind: driver.EventFocus, Focused: focused})
	})
	w.win.SetPosCallback(func(_ *glfw3.Window, _, _ int) {
		w.resendSpot()
	})
	w.win.SetCursorPosCallback(func(_ *glfw3.Window, x, y float64) {
		w.emit(driver.Event{
			Kind:     driver.EventMouseMove,
			Position: driver.Point{X: x, Y: y},
		})
	})
	w.win.SetCursorEnterCallback(func(_ *glfw3.Window, entered bool) {
		if !entered {
			w.emit(driver.Event{Kind: driver.EventCursorLeave})
		}
	})
	w.win.SetMouseButtonCallback(func(_ *glfw3.Window, button glfw3.MouseButton, action glfw3.Action, mods glfw3.ModifierKey) {
		w.handleMouseButton(button, action, mods)
	})
	w.win.SetKeyCallback(func(_ *glfw3.Window, key glfw3.Key, scancode int, action glfw3.Action, mods glfw3.ModifierKey) {
		w.handleKey(key, scancode, action, mods)
	})
	w.win.SetCharCallback(func(_ *glfw3.Window, r rune) {
		if w.ximSuppressChar {
			w.ximSuppressChar = false
			return
		}
		w.emit(driver.Event{Kind: driver.EventRune, Rune: r})
	})
	w.win.SetScrollCallback(func(_ *glfw3.Window, xoff, yoff float64) {
		w.handleScroll(xoff, yoff)
	})
}

func (w *Window) handleKey(key glfw3.Key, scancode int, action glfw3.Action, mods glfw3.ModifierKey) {
	event := driver.Event{
		Kind:     driver.EventKey,
		Key:      mapKey(key),
		Action:   mapAction(action),
		Mods:     mapModifiers(mods),
		ScanCode: scancode,
		KeySym:   w.keySymForEvent(scancode, action, mods),
	}
	if action != glfw3.Release {
		w.ximSuppressChar = false
	}
	if w.xim != nil && w.compositionEnabled && scancode >= 0 && event.KeySym != 0 {
		handled, err := w.xim.ProcessKeyEvent(
			event.KeySym,
			uint32(scancode),
			ximState(mods),
			action == glfw3.Release,
			ximEventTime(),
		)
		w.refreshComposition()
		if action != glfw3.Release {
			w.ximSuppressChar = err == nil && handled
		}
		if err == nil && handled {
			return
		}
	}
	w.emit(event)
}

// handleMouseButton converts a GLFW mouse button event, dropping buttons
// outside the driver contract so they can never surface as a faked
// left-button event. Press and release events carry the current cursor
// position, sampled before the event is emitted.
func (w *Window) handleMouseButton(button glfw3.MouseButton, action glfw3.Action, mods glfw3.ModifierKey) {
	mapped, ok := mapMouseButton(button)
	if !ok {
		return
	}
	if w.destroyed {
		return
	}
	x, y := w.win.GetCursorPos()
	w.emit(driver.Event{
		Kind:     driver.EventMouseButton,
		Position: driver.Point{X: x, Y: y},
		Button:   mapped,
		Action:   mapAction(action),
		Mods:     mapModifiers(mods),
	})
}

// handleScroll converts a GLFW scroll callback into a driver scroll event.
// The delta passes through in wheel lines — GLFW reports 1 per wheel line
// and smooth fractions for trackpads — and the fixed logical line step is
// applied by the consuming component, not here. Like button events, the
// event carries the current cursor position so the UI can route the wheel
// to the viewport under the cursor.
func (w *Window) handleScroll(xoff, yoff float64) {
	if w.destroyed {
		return
	}
	x, y := w.win.GetCursorPos()
	w.emit(driver.Event{
		Kind:     driver.EventScroll,
		Position: driver.Point{X: x, Y: y},
		ScrollX:  xoff,
		ScrollY:  yoff,
	})
}

// clearCallbacks drops every GLFW callback. It runs at destroy request time
// — possibly inside the callback stack, which GLFW permits for callback
// setters — so a logically destroyed window receives no further events, and
// it is idempotent because destroyNative may call it again before freeing
// the native window.
func (w *Window) clearCallbacks() {
	if w.callbackClears > 0 {
		return
	}
	w.callbackClears++
	w.win.SetCloseCallback(nil)
	w.win.SetSizeCallback(nil)
	w.win.SetFramebufferSizeCallback(nil)
	w.win.SetRefreshCallback(nil)
	w.win.SetFocusCallback(nil)
	w.win.SetPosCallback(nil)
	w.win.SetCursorPosCallback(nil)
	w.win.SetCursorEnterCallback(nil)
	w.win.SetMouseButtonCallback(nil)
	w.win.SetKeyCallback(nil)
	w.win.SetCharCallback(nil)
	w.win.SetScrollCallback(nil)
}
