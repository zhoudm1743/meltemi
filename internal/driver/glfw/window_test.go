package glfw

import (
	"image"
	"image/color"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

func TestWindowConfigurationAndNativeEvents(t *testing.T) {
	b := newTestBackend(t)
	win := newTestWindow(t, b, driver.DefaultConfig())
	native := win.(*Window)
	titled, ok := win.(driver.TitleWindow)
	if !ok {
		t.Fatalf("window %T does not support native title updates", win)
	}
	titled.SetTitle("updated")

	var events []driver.Event
	win.SetHandler(func(event driver.Event) { events = append(events, event) })
	sizeCallback := native.win.SetSizeCallback(nil)
	if sizeCallback == nil {
		t.Fatal("window has no GLFW size callback")
	}
	native.win.SetSizeCallback(sizeCallback)
	focusCallback := native.win.SetFocusCallback(nil)
	if focusCallback == nil {
		t.Fatal("window has no GLFW focus callback")
	}
	native.win.SetFocusCallback(focusCallback)

	win.Hide()
	if !native.hidden {
		t.Fatal("Hide did not mark the native window hidden")
	}
	win.Show()
	if native.hidden {
		t.Fatal("Show did not mark the native window visible")
	}
	sizeCallback(native.win, 321, 234)
	focusCallback(native.win, true)
	focusCallback(native.win, false)
	if len(events) != 3 {
		t.Fatalf("native events = %v, want resize and two focus events", events)
	}
	if events[0].Kind != driver.EventResize || events[0].Size != (driver.Size{Width: 321, Height: 234}) {
		t.Errorf("resize event = %+v, want 321x234 EventResize", events[0])
	}
	if events[1].Kind != driver.EventFocus || !events[1].Focused || events[1].Window != win {
		t.Errorf("focus-gained event = %+v, want focused EventFocus for this window", events[1])
	}
	if events[2].Kind != driver.EventFocus || events[2].Focused {
		t.Errorf("focus-lost event = %+v, want unfocused EventFocus", events[2])
	}

	win.Destroy()
	titled.SetTitle("destroyed")
	win.Show()
	win.Hide()
	sizeCallback(native.win, 1, 1)
	focusCallback(native.win, true)
	if len(events) != 3 {
		t.Fatalf("destroyed window emitted stale events: %v", events[3:])
	}
	if previous := native.win.SetSizeCallback(nil); previous != nil {
		t.Error("Destroy did not clear the GLFW size callback")
	}
	if previous := native.win.SetFocusCallback(nil); previous != nil {
		t.Error("Destroy did not clear the GLFW focus callback")
	}
}

func TestWindowIconAndMinSize(t *testing.T) {
	source := image.NewRGBA(image.Rect(10, 20, 12, 21))
	source.Set(10, 20, color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff})
	source.Set(11, 20, color.RGBA{R: 0x65, G: 0x43, B: 0x21, A: 0xff})
	cloned := cloneIcon(source)
	if cloned == nil {
		t.Fatal("cloneIcon returned nil for a non-nil source")
	}
	if cloned.Bounds() != image.Rect(0, 0, 2, 1) {
		t.Fatalf("cloneIcon bounds = %v, want (0,0)-(2,1)", cloned.Bounds())
	}
	if got := cloned.NRGBAAt(0, 0); got != (color.NRGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Errorf("first cloned pixel = %#v", got)
	}
	if got := cloned.NRGBAAt(1, 0); got != (color.NRGBA{R: 0x65, G: 0x43, B: 0x21, A: 0xff}) {
		t.Errorf("second cloned pixel = %#v", got)
	}
	source.Set(10, 20, color.RGBA{})
	if got := cloned.NRGBAAt(0, 0); got != (color.NRGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Errorf("cloned icon changed with its source: %#v", got)
	}
	if cloneIcon(nil) != nil {
		t.Error("cloneIcon(nil) returned an image")
	}

	b := newTestBackend(t)
	cfg := driver.DefaultConfig()
	cfg.Size = driver.Size{Width: 640, Height: 480}
	cfg.MinSize = driver.Size{Width: 320, Height: 200}
	cfg.Icon = source
	win := newTestWindow(t, b, cfg)
	frame := win.(driver.FrameWindow)
	initial := win.Size()
	if err := frame.SetSize(driver.Size{Width: 100, Height: 80}); err != nil {
		t.Fatalf("SetSize below minimum: %v", err)
	}
	waitFor(t, b, "native resize below minimum", func() bool { return win.Size() != initial })
	if got := win.Size(); got != cfg.MinSize {
		t.Errorf("size after requesting 100x80 = %v, want minimum 320x200", got)
	}

	win.Destroy()
	if err := frame.SetSize(driver.Size{Width: 800, Height: 600}); err != errDestroyed {
		t.Errorf("SetSize after Destroy = %v, want %v", err, errDestroyed)
	}
}
