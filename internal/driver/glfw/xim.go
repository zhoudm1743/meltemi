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
	"fmt"
	"log"
	"os"
	"time"
	"unsafe"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/xim"
)

type ximClient interface {
	SetEventHandler(func(driver.Event))
	SetWakeHandler(func())
	Preedit() xim.Preedit
	SetFocus(bool) error
	SetSpot(x, y, width, height int) error
	ProcessKeyEvent(keyval, keycode, state uint32, isRelease bool, time uint32) (bool, error)
	Close() error
}

func newDBusXIMClient() (ximClient, error) {
	return xim.NewClient(context.Background(), "meltemi", os.Getenv("DISPLAY"))
}

func (b *Backend) reportXIMError(operation string, err error) {
	if err == nil {
		return
	}
	err = fmt.Errorf("glfw: IME %s: %w", operation, err)
	if b.ximErrorHandler != nil {
		b.ximErrorHandler(err)
		return
	}
	log.Printf("%v", err)
}

func (b *Backend) createXIMClient() (ximClient, error) {
	factory := b.ximFactory
	if factory == nil {
		factory = newDBusXIMClient
	}
	client, err := factory()
	if err == nil && client != nil {
		client.SetWakeHandler(b.Wake)
	}
	return client, err
}

func (b *Backend) initXIM() error {
	b.imeEnabled = os.Getenv("MELTEMI_IME") == "1" || os.Getenv("WIND_IME") == "1"
	if !b.imeEnabled {
		return nil
	}
	client, err := b.createXIMClient()
	if err != nil {
		b.imeEnabled = false
		return err
	}
	b.initialXIM = client
	return nil
}

func (b *Backend) refreshCompositions() {
	for w := range b.windows {
		w.refreshComposition()
	}
}

func (b *Backend) closeAllXIM() {
	if b.initialXIM != nil {
		client := b.initialXIM
		b.initialXIM = nil
		client.SetEventHandler(nil)
		b.reportXIMError("close initial input context", client.Close())
	}
	for w := range b.windows {
		w.closeXIM()
	}
}

func ximEventTime() uint32 {
	return uint32(time.Now().UnixMilli())
}

func ximState(mods glfw3.ModifierKey) uint32 {
	var state uint32
	if mods&glfw3.ModShift != 0 {
		state |= 1 << 0
	}
	if mods&glfw3.ModCapsLock != 0 {
		state |= 1 << 1
	}
	if mods&glfw3.ModControl != 0 {
		state |= 1 << 2
	}
	if mods&glfw3.ModAlt != 0 {
		state |= 1 << 3
	}
	if mods&glfw3.ModNumLock != 0 {
		state |= 1 << 4
	}
	if mods&glfw3.ModSuper != 0 {
		state |= 1 << 6
	}
	return state
}

func x11KeySym(scancode int, mods glfw3.ModifierKey) uint32 {
	return uint32(C.windX11KeySym(
		unsafe.Pointer(glfw3.GetX11Display()),
		C.int(scancode),
		C.uint(ximState(mods)),
	))
}

func (w *Window) keySymForEvent(scancode int, action glfw3.Action, mods glfw3.ModifierKey) uint32 {
	if action == glfw3.Release {
		keySym, ok := w.pressedKeySyms[scancode]
		delete(w.pressedKeySyms, scancode)
		if ok {
			return keySym
		}
	} else if action == glfw3.Repeat {
		if keySym, ok := w.pressedKeySyms[scancode]; ok {
			return keySym
		}
	}
	keySym := x11KeySym(scancode, mods)
	if action == glfw3.Press {
		if w.pressedKeySyms == nil {
			w.pressedKeySyms = make(map[int]uint32)
		}
		w.pressedKeySyms[scancode] = keySym
	}
	return keySym
}

func (w *Window) attachXIM(client ximClient) {
	w.xim = client
	client.SetEventHandler(w.handleXIMEvent)
}

func (w *Window) ensureXIM() {
	if w.destroyed || w.destroying || w.hidden || w.hiding || !w.backend.imeEnabled || !w.compositionEnabled || !w.focused || w.xim != nil {
		return
	}
	client, err := w.backend.createXIMClient()
	if err != nil {
		w.backend.reportXIMError("recreate input context", err)
		return
	}
	w.attachXIM(client)
	if err := client.SetFocus(true); err != nil {
		client.SetEventHandler(nil)
		w.xim = nil
		w.backend.reportXIMError("focus recreated input context", err)
		w.backend.reportXIMError("close input context after focus failure", client.Close())
	}
}

func (w *Window) closeXIM() {
	client := w.xim
	w.xim = nil
	w.ximSuppressChar = false
	if client != nil {
		client.SetEventHandler(nil)
		w.backend.reportXIMError("close input context", client.Close())
	}
	w.setComposition(xim.Preedit{})
}

func (w *Window) handleXIMFocus(focused bool) {
	w.focused = focused
	if !focused {
		w.closeXIM()
		return
	}
	if w.xim == nil {
		w.ensureXIM()
		return
	}
	if !w.hidden && w.compositionEnabled {
		w.backend.reportXIMError("focus input context", w.xim.SetFocus(true))
	}
}

// SetCompositionEnabled controls whether this window may own an input context.
// Disabling cancels the composition and closes the context immediately.
func (w *Window) SetCompositionEnabled(enabled bool) {
	if w.destroyed {
		return
	}
	w.compositionEnabled = enabled
	if !enabled {
		w.closeXIM()
		return
	}
	w.ensureXIM()
}

// Preedit returns the current uncommitted text and rune caret on the UI thread.
func (w *Window) Preedit() (text string, caret int, active bool) {
	w.refreshComposition()
	preedit := w.preedit
	return preedit.Text, preedit.Caret, preedit.Active
}

// SetSpot places the input-method candidate rectangle in client coordinates.
func (w *Window) SetSpot(x, y int) {
	if w.destroyed || !w.compositionEnabled {
		return
	}
	w.spot = [4]int{x, y, 1, 1}
	w.spotValid = true
	w.resendSpot()
}

func (w *Window) resendSpot() {
	if w.destroyed || w.xim == nil || !w.compositionEnabled || !w.spotValid {
		return
	}
	err := w.xim.SetSpot(w.spot[0], w.spot[1], w.spot[2], w.spot[3])
	w.backend.reportXIMError("set candidate rectangle", err)
	w.refreshComposition()
}

func (w *Window) refreshComposition() {
	if w.destroyed || w.xim == nil {
		w.setComposition(xim.Preedit{})
		return
	}
	w.setComposition(w.xim.Preedit())
}

func (w *Window) setComposition(next xim.Preedit) {
	previous := w.preedit
	if previous == next {
		return
	}
	w.preedit = next
	switch {
	case !previous.Active && next.Active:
		w.emitComposition(driver.EventCompositionStart)
	case previous.Active && next.Active:
		w.emitComposition(driver.EventCompositionUpdate)
	case previous.Active && !next.Active:
		w.emitComposition(driver.EventCompositionEnd)
	}
}

// emitComposition also delivers the final End after Destroy has blocked
// ordinary events, allowing observers to finish their composition cleanup.
func (w *Window) emitComposition(kind driver.EventKind) {
	if handler := w.handler; handler != nil {
		handler(driver.Event{Kind: kind, Window: w})
	}
}

func (w *Window) handleXIMEvent(event driver.Event) {
	if w.destroyed {
		return
	}
	w.refreshComposition()
	w.emit(event)
}
