//go:build linux

package glfw

/*
#cgo LDFLAGS: -lX11
#include <X11/Xlib.h>
#include <X11/XKBlib.h>

static unsigned long windX11KeySym(void *display, int scancode, unsigned int state) {
	Display *dpy = (Display *)display;
	if (dpy == NULL || scancode < 0 || scancode > 255) {
		return NoSymbol;
	}
	XkbStateRec keyboard;
	int group = 0;
	if (XkbGetState(dpy, XkbUseCoreKbd, &keyboard) == Success) {
		group = keyboard.group;
	}
	unsigned int consumed = 0;
	KeySym keysym = NoSymbol;
	unsigned int coreState = XkbBuildCoreState(state, group);
	if (!XkbLookupKeySym(dpy, (KeyCode)scancode, coreState, &consumed, &keysym)) {
		return NoSymbol;
	}
	return keysym;
}
*/
import "C"

import (
	"context"
	"os"
	"unsafe"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/xim"
)

func newDBusXIMClient() (ximClient, error) {
	return xim.NewClient(context.Background(), "meltemi", os.Getenv("DISPLAY"))
}

func x11KeySym(scancode int, mods glfw3.ModifierKey) uint32 {
	return uint32(C.windX11KeySym(
		unsafe.Pointer(glfw3.GetX11Display()),
		C.int(scancode),
		C.uint(ximState(mods)),
	))
}
