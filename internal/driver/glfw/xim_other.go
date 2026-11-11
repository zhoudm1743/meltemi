//go:build !linux

package glfw

import glfw3 "github.com/go-gl/glfw/v3.3/glfw"

// newDBusXIMClient reports that this platform has no Fcitx/XIM backend.
// A nil client leaves composition disabled; GLFW still delivers committed
// Unicode, including characters produced by a system IME, through the char callback.
func newDBusXIMClient() (ximClient, error) {
	return nil, nil
}

// x11KeySym returns a stable non-zero value for a real scancode so an attached
// composition client still observes the key. It is not an X11 KeySym.
func x11KeySym(scancode int, _ glfw3.ModifierKey) uint32 {
	if scancode < 0 {
		return 0
	}
	return uint32(scancode) + 1
}
