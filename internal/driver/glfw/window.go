package glfw

import (
	"errors"
	"fmt"
	"image"
	"image/draw"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/xim"
)

// errDestroyed is returned by operations that need a live window.
var errDestroyed = errors.New("glfw: window already destroyed")

func cloneIcon(source image.Image) *image.NRGBA {
	if source == nil {
		return nil
	}
	bounds := source.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil
	}
	cloned := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(cloned, cloned.Bounds(), source, bounds.Min, draw.Src)
	return cloned
}

func applyWindowConfig(win *glfw3.Window, config driver.Config) {
	if config.MinSize.Width > 0 || config.MinSize.Height > 0 {
		minWidth, minHeight := glfw3.DontCare, glfw3.DontCare
		if config.MinSize.Width > 0 {
			minWidth = config.MinSize.Width
		}
		if config.MinSize.Height > 0 {
			minHeight = config.MinSize.Height
		}
		win.SetSizeLimits(minWidth, minHeight, glfw3.DontCare, glfw3.DontCare)
	}
	if icon := cloneIcon(config.Icon); icon != nil {
		win.SetIcon([]image.Image{icon})
	}
}

// Window is a GLFW-backed top-level window. Every method must be called on
// the UI thread, matching the driver contract. After Destroy all methods are
// safe no-ops so a stray call cannot touch freed native state.
type Window struct {
	backend *Backend
	win     *glfw3.Window
	handler driver.Handler

	// destroyed is the logical state set by the first Destroy call; every
	// method guards on it.
	destroyed bool

	// nativeDestroyCount guards and records the actual glfwDestroyWindow
	// call, which the backend performs exactly once and only outside the
	// GLFW callback stack.
	nativeDestroyCount int

	// callbackClears guards and records the GLFW callback clearing, which
	// runs immediately at destroy request time — possibly inside the
	// callback stack, which GLFW permits for callback setters — and never
	// repeats.
	callbackClears int

	xim                ximClient
	preedit            xim.Preedit
	spot               [4]int
	spotValid          bool
	compositionEnabled bool
	focused            bool
	hidden             bool
	hiding             bool
	destroying         bool
	ximSuppressChar    bool
	pressedKeySyms     map[int]uint32
}

// Show makes the window visible.
func (w *Window) Show() {
	if w.destroyed || w.hiding {
		return
	}
	w.hidden = false
	w.win.Show()
	w.ensureXIM()
}

// Hide makes the window invisible.
func (w *Window) Hide() {
	if w.destroyed || w.hiding {
		return
	}
	w.hiding = true
	w.hidden = true
	w.closeXIM()
	if !w.destroyed {
		w.win.Hide()
	}
	w.hiding = false
}

// Destroy logically destroys the window: it clears every GLFW callback so no
// further native events can arrive, closes composition, and queues the native
// destroy for the backend to run once the GLFW callback stack unwinds — GLFW
// forbids destroying a window from inside its own callbacks. It is idempotent.
func (w *Window) Destroy() {
	if w.destroyed {
		return
	}
	w.destroying = true
	w.destroyed = true
	w.clearCallbacks()
	w.closeXIM()
	delete(w.backend.windows, w)
	w.backend.enqueueDestroy(w)
}

// destroyNative frees the native window and context. The backend only calls
// it from flush points that run outside the GLFW callback stack.
func (w *Window) destroyNative() {
	if w.nativeDestroyCount > 0 {
		return
	}
	w.nativeDestroyCount++
	w.clearCallbacks()
	w.win.Destroy()
}

// Size returns the logical (screen-coordinate) size of the window.
func (w *Window) Size() driver.Size {
	if w.destroyed {
		return driver.Size{}
	}
	width, height := w.win.GetSize()
	return driver.Size{Width: width, Height: height}
}

// SetSize resizes the window. It returns an error for a non-positive size
// and errDestroyed once the window has been destroyed. The native resize is
// negotiated with the window manager, so Size need not reflect the new value
// until the manager applies it.
func (w *Window) SetSize(size driver.Size) error {
	if w.destroyed {
		return errDestroyed
	}
	if size.Width <= 0 || size.Height <= 0 {
		return fmt.Errorf("glfw: invalid window size %dx%d", size.Width, size.Height)
	}
	w.win.SetSize(size.Width, size.Height)
	return nil
}

// Position returns the window's position in integer screen coordinates.
func (w *Window) Position() driver.Point {
	if w.destroyed {
		return driver.Point{}
	}
	x, y := w.win.GetPos()
	return driver.Point{X: float64(x), Y: float64(y)}
}

// SetPosition moves the window to pos in integer screen coordinates.
func (w *Window) SetPosition(pos driver.Point) {
	if w.destroyed {
		return
	}
	w.win.SetPos(int(pos.X), int(pos.Y))
}

// Iconify minimizes the window to the task bar.
func (w *Window) Iconify() {
	if w.destroyed {
		return
	}
	w.win.Iconify()
}

// IsIconified reports whether the window is currently iconified (minimized).
// A destroyed window reports false.
func (w *Window) IsIconified() bool {
	if w.destroyed {
		return false
	}
	return w.win.GetAttrib(glfw3.Iconified) == glfw3.True
}

// Maximize enlarges the window to fill the work area.
func (w *Window) Maximize() {
	if w.destroyed {
		return
	}
	w.win.Maximize()
}

// Restore returns an iconified or maximized window to its normal state.
// Iconified windows are restored through the EWMH _NET_ACTIVE_WINDOW
// request (which GLFW's Focus sends on X11) rather than glfwRestoreWindow's
// raw XMapWindow: window managers like KWin deliberately ignore map requests
// for iconified top-level windows, and GLFW's internal wait for the map to
// take effect can spin without bound when that happens. A maximized window
// keeps the regular path, which asks the WM to drop the maximized state.
func (w *Window) Restore() {
	if w.destroyed {
		return
	}
	if w.win.GetAttrib(glfw3.Iconified) == glfw3.True {
		w.win.Focus()
		return
	}
	w.win.Restore()
}

// IsMaximized reports whether the window is currently maximized.
func (w *Window) IsMaximized() bool {
	if w.destroyed {
		return false
	}
	return w.win.GetAttrib(glfw3.Maximized) == glfw3.True
}

// SetHandler installs the handler receiving this window's events. It must be
// called on the UI thread, like every other method.
func (w *Window) SetHandler(h driver.Handler) {
	w.handler = h
}

// SetTitle updates the native window title. It is a no-op after Destroy.
func (w *Window) SetTitle(title string) {
	if w.destroyed {
		return
	}
	w.win.SetTitle(title)
}

// MakeCurrent binds the window's OpenGL context to the calling thread.
func (w *Window) MakeCurrent() error {
	if w.destroyed {
		return errDestroyed
	}
	w.win.MakeContextCurrent()
	return nil
}

// FramebufferSize returns the size of the window's framebuffer in pixels,
// which differs from the logical size on high-resolution displays.
func (w *Window) FramebufferSize() driver.Size {
	if w.destroyed {
		return driver.Size{}
	}
	width, height := w.win.GetFramebufferSize()
	return driver.Size{Width: width, Height: height}
}

// Present swaps the front and back buffers of the window's context.
func (w *Window) Present() {
	if w.destroyed {
		return
	}
	w.win.SwapBuffers()
}

// Text returns the current system clipboard text. A destroyed window reports
// errDestroyed; an empty clipboard yields an empty string with a nil error,
// matching GLFW's clipboard contract.
func (w *Window) Text() (string, error) {
	if w.destroyed {
		return "", errDestroyed
	}
	return w.win.GetClipboardString(), nil
}

// SetText replaces the system clipboard text. A destroyed window reports
// errDestroyed without touching the native clipboard, whose resources are
// already freed.
func (w *Window) SetText(s string) error {
	if w.destroyed {
		return errDestroyed
	}
	w.win.SetClipboardString(s)
	return nil
}
