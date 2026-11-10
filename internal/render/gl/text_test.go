package gl

import (
	"math"
	"runtime"
	"testing"
	"time"

	gogl "github.com/go-gl/gl/v3.3-core/gl"
	glfw3 "github.com/go-gl/glfw/v3.3/glfw"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/driver/glfw"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// newGLTestBackend locks the calling test goroutine to its OS thread before
// initializing GLFW. Later renderer cleanups run before Terminate, and the
// thread is unlocked last.
func newGLTestBackend(t *testing.T) *glfw.Backend {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	backend := glfw.New()
	if err := backend.Init(); err != nil {
		t.Skipf("glfw init failed: %v", err)
	}
	t.Cleanup(backend.Terminate)
	return backend
}

// newGLRenderer creates a real OpenGL context on a GLFW window and a Renderer
// for it. Show and an initial presented frame keep the default framebuffer
// mapped and readable; a fresh or unmapped framebuffer can return zero pixels
// even with a current context. Only GLFW init and window creation may skip:
// they are the points where a headless machine has no environment to offer.
// Once the window exists its context is real, so a renderer build failure
// (binding load, shader compile, buffer allocation) is a regression in this
// package and fails the test instead of skipping it.
func newGLRenderer(t *testing.T) (*Renderer, *glfw.Window) {
	t.Helper()
	return newGLRendererWithBackend(t, newGLTestBackend(t))
}

func newGLRendererPair(t *testing.T) (*Renderer, *glfw.Window, *Renderer, *glfw.Window) {
	t.Helper()
	backend := newGLTestBackend(t)
	first, firstWin := newGLRendererWithBackend(t, backend)
	second, secondWin := newGLRendererWithBackend(t, backend)
	return first, firstWin, second, secondWin
}

func newGLRendererWithBackend(t *testing.T, backend *glfw.Backend) (*Renderer, *glfw.Window) {
	t.Helper()
	drvWin, err := backend.NewWindow(driver.Config{Title: "gl-text-test", Size: driver.Size{Width: 64, Height: 64}})
	if err != nil {
		t.Skipf("window creation failed: %v", err)
	}
	win := drvWin.(*glfw.Window)
	win.Show()

	r, err := New(win)
	if err != nil {
		t.Fatalf("renderer creation failed with a live context: %v", err)
	}
	t.Cleanup(func() {
		if err := win.MakeCurrent(); err != nil {
			t.Errorf("MakeCurrent in cleanup: %v", err)
		}
		r.Close() // also covers idempotent second Close via the hook
		r.Close()
		win.Destroy()
		backend.PollEvents()
	})
	if err := r.Begin(render.Color{}); err != nil {
		t.Fatalf("initial Begin: %v", err)
	}
	if err := r.End(); err != nil {
		t.Fatalf("initial End: %v", err)
	}
	gogl.Finish()
	return r, win
}

func currentProgram(t *testing.T) uint32 {
	t.Helper()
	var current int32
	gogl.GetIntegerv(gogl.CURRENT_PROGRAM, &current)
	return uint32(current)
}

// TestDrawTextRestoresRectProgram pins that a rectangle drawn in the same
// frame after DrawText uses the rectangle program and renders correctly:
// before the fix DrawText left the text program current and DrawRect never
// re-bound its own, so the rect was drawn with the text shader and the
// already-deleted text texture.
func TestDrawTextRestoresRectProgram(t *testing.T) {
	r, _ := newGLRenderer(t)

	face, err := text.NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()

	if err := r.Begin(render.Color{R: 0.2, G: 0.2, B: 0.2, A: 1}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := r.DrawText("Wind", driver.Point{X: 16, Y: 8}, render.Color{R: 1, G: 1, B: 1, A: 1}, face); err != nil {
		t.Fatalf("DrawText: %v", err)
	}

	if got := currentProgram(t); got != r.program {
		t.Errorf("after DrawText, CURRENT_PROGRAM = %d, want rect program %d", got, r.program)
	}

	// Same frame, after the text: the rectangle must render with the rect
	// program. Read a pixel back so a wrong shader cannot pass unnoticed.
	r.DrawRect(render.Rect{X: 1, Y: 1, Width: 10, Height: 10}, render.Color{R: 1, A: 1})
	if err := gogl.GetError(); err != gogl.NO_ERROR {
		t.Errorf("gl.GetError after same-frame DrawRect = %v, want NO_ERROR", err)
	}
	if got := currentProgram(t); got != r.program {
		t.Errorf("after DrawRect, CURRENT_PROGRAM = %d, want rect program %d", got, r.program)
	}

	gogl.Finish()
	var px [4]uint8
	// Framebuffer origin is bottom-left: logical (6, 6) in 64x64 maps to
	// GL pixel (6, 64-6-1). The rect is opaque red over a grey clear.
	gogl.ReadPixels(6, 57, 1, 1, gogl.RGBA, gogl.UNSIGNED_BYTE, gogl.Ptr(&px[0]))
	if px != [4]uint8{255, 0, 0, 255} {
		t.Errorf("rect pixel = %v, want opaque red (255,0,0,255)", px)
	}

	if err := r.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
}

// TestCloseReleasesTextState pins that Renderer.Close deletes the cached
// text program/VAO/VBO and drops the cache entry, so neither GL objects nor
// the renderer (and its window) leak.
func TestCloseReleasesTextState(t *testing.T) {
	r, win := newGLRenderer(t)

	if _, err := textStateFor(r); err != nil {
		t.Fatalf("textStateFor: %v", err)
	}
	s, ok := textStates[r]
	if !ok {
		t.Fatal("textStateFor did not cache the state")
	}
	program, vao, vbo := s.program, s.vao, s.vbo

	// Normalize bindings so the deletes take effect immediately instead of
	// being deferred while the objects are still in use.
	gogl.BindVertexArray(0)
	gogl.BindBuffer(gogl.ARRAY_BUFFER, 0)
	if err := win.MakeCurrent(); err != nil {
		t.Fatalf("MakeCurrent: %v", err)
	}
	r.Close()

	if _, ok := textStates[r]; ok {
		t.Error("textStates entry survived Close")
	}
	// A deleted program name is no longer queryable (GetProgramiv would just
	// raise INVALID_VALUE), so IsProgram is the deterministic check.
	if gogl.IsProgram(program) {
		t.Error("text program still live after Close")
	}
	if gogl.IsVertexArray(vao) {
		t.Error("text VAO not deleted by Close")
	}
	if gogl.IsBuffer(vbo) {
		t.Error("text VBO not deleted by Close")
	}
	if err := gogl.GetError(); err != gogl.NO_ERROR {
		t.Errorf("gl.GetError after Close = %v, want NO_ERROR", err)
	}
}

func TestDiscardAfterDrawTextWithoutContext(t *testing.T) {
	r, _ := newGLRenderer(t)

	face, err := text.NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()
	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := r.DrawText("Wind", driver.Point{}, render.Color{A: 1}, face); err != nil {
		t.Fatalf("DrawText: %v", err)
	}
	if _, ok := textStates[r]; !ok {
		t.Fatal("DrawText did not cache text state")
	}

	glfw3.DetachCurrentContext()
	r.Discard()

	if _, ok := textStates[r]; ok {
		t.Error("textStates entry survived Discard without a current context")
	}
}

// TestDrawTextEmptyCreatesNoGLState pins the empty-text fast path on a real
// context: no texture, no cached text state.
func TestDrawTextEmptyCreatesNoGLState(t *testing.T) {
	r, _ := newGLRenderer(t)

	face, err := text.NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()

	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := r.DrawText("", driver.Point{}, render.Color{A: 1}, face); err != nil {
		t.Fatalf("DrawText with empty string: %v", err)
	}
	if _, ok := textStates[r]; ok {
		t.Error("empty DrawText cached text state")
	}
	if err := r.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
}

// TestCloseWithoutAssets runs the Close hook paths without a GL context:
// they must be plain no-ops so headless callers never touch GL.
func TestCloseWithoutAssets(t *testing.T) {
	r := &Renderer{}
	r.Close()
	r.Close()
	releaseTextState(r)
	if len(textStates) != 0 {
		t.Errorf("textStates = %d entries, want 0", len(textStates))
	}
}

// scaledTarget wraps a real window-backed target and overrides the logical and
// framebuffer sizes, so a test can drive the renderer at content scales the
// test display does not offer while rendering into the same real context.
type scaledTarget struct {
	render.Target
	logical driver.Size
	fb      driver.Size
}

func (t *scaledTarget) Size() driver.Size            { return t.logical }
func (t *scaledTarget) FramebufferSize() driver.Size { return t.fb }

// newGLRendererScaled creates a real OpenGL context on a GLFW window plus a
// Renderer bound to a scaledTarget, like newGLRendererWithBackend but with
// overridable sizes. Show and an initial presented frame keep the default
// framebuffer mapped and readable.
func newGLRendererScaled(t *testing.T, backend *glfw.Backend, logical, fb driver.Size) (*Renderer, *scaledTarget) {
	t.Helper()
	drvWin, err := backend.NewWindow(driver.Config{Title: "gl-text-scale-test", Size: driver.Size{Width: 64, Height: 64}})
	if err != nil {
		t.Skipf("window creation failed: %v", err)
	}
	win := drvWin.(*glfw.Window)
	win.Show()
	target := &scaledTarget{Target: win, logical: logical, fb: fb}
	r, err := New(target)
	if err != nil {
		t.Fatalf("renderer creation failed with a live context: %v", err)
	}
	t.Cleanup(func() {
		if err := win.MakeCurrent(); err != nil {
			t.Errorf("MakeCurrent in cleanup: %v", err)
		}
		r.Close()
		win.Destroy()
		backend.PollEvents()
	})
	waitForReadableFramebuffer(t, r, backend)
	return r, target
}

// waitForReadableFramebuffer presents sentinel frames until one reads back
// the clear color. A freshly mapped window can discard frames and return
// zero pixels while the compositor has not attached a surface yet (the same
// condition newGLRendererWithBackend documents); this test reads real text
// pixels from the first content frame, so it waits for the framebuffer to
// become backed instead of hoping the first frame lands.
func waitForReadableFramebuffer(t *testing.T, r *Renderer, backend *glfw.Backend) {
	t.Helper()
	const maxTries = 100
	for i := 0; i < maxTries; i++ {
		if err := r.Begin(render.Color{B: 1, A: 1}); err != nil {
			t.Fatalf("warmup Begin: %v", err)
		}
		gogl.Finish()
		var px [4]uint8
		gogl.ReadPixels(0, 0, 1, 1, gogl.RGBA, gogl.UNSIGNED_BYTE, gogl.Ptr(&px[0]))
		if err := r.End(); err != nil {
			t.Fatalf("warmup End: %v", err)
		}
		backend.PollEvents()
		if px == [4]uint8{0, 0, 255, 255} {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("window framebuffer never became readable")
}

// sampleBilinear samples the tightly packed alpha mask at texel coordinates
// (fu, fv) with bilinear filtering and clamp-to-edge, replicating what the
// GL text quad's LINEAR sampler must produce for a pixel at normalized
// texture coordinates (u, v) = (fu+0.5)/w, (fv+0.5)/h.
func sampleBilinear(alpha []byte, w, h, stride int, fu, fv float64) float64 {
	clamp := func(v, hi float64) float64 {
		if v < 0 {
			return 0
		}
		if v > hi {
			return hi
		}
		return v
	}
	tu := clamp(fu, float64(w-1))
	tv := clamp(fv, float64(h-1))
	x0 := int(math.Floor(tu))
	y0 := int(math.Floor(tv))
	x1 := min(x0+1, w-1)
	y1 := min(y0+1, h-1)
	dx := tu - float64(x0)
	dy := tv - float64(y0)
	a00 := float64(alpha[y0*stride+x0])
	a01 := float64(alpha[y0*stride+x1])
	a10 := float64(alpha[y1*stride+x0])
	a11 := float64(alpha[y1*stride+x1])
	top := a00*(1-dx) + a01*dx
	bottom := a10*(1-dx) + a11*dx
	return top*(1-dy) + bottom*dy
}

// drawAndCheck draws value once at the wrapper's current content scale and
// verifies the framebuffer against the oracle bitmap: every pixel center
// inside the logical target quad must match the oracle alpha mask sampled
// with bilinear filtering (physical upload, logical quad), every pixel
// outside must stay clear, and the mask must contain solid ink. A renderer
// that stretched a logical-size bitmap, or reused a stale one after a
// resize, fails the per-pixel comparison.
func drawAndCheck(t *testing.T, r *Renderer, target *scaledTarget, face *text.Face, value string, at driver.Point, oracle text.Bitmap, tolerance float64) {
	t.Helper()
	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := r.DrawText(value, at, render.Color{G: 1, A: 1}, face); err != nil {
		t.Fatalf("DrawText: %v", err)
	}
	// Read the back buffer before End: Present swaps it away and reading
	// after the swap is undefined.
	gogl.Finish()
	fb := target.fb
	logical := target.logical
	pix := make([]byte, fb.Width*fb.Height*4)
	gogl.ReadPixels(0, 0, int32(fb.Width), int32(fb.Height), gogl.RGBA, gogl.UNSIGNED_BYTE, gogl.Ptr(pix))
	if err := r.End(); err != nil {
		t.Fatalf("End: %v", err)
	}

	logW, logH := oracle.LogicalWidth, oracle.LogicalHeight
	checked, mismatches, outsideInk := 0, 0, 0
	maxG := 0
	for j := 0; j < fb.Height; j++ {
		for i := 0; i < fb.Width; i++ {
			g := int(pix[(j*fb.Width+i)*4+1])
			if g > maxG {
				maxG = g
			}
			// Logical center of this physical pixel; GL row j counts from
			// the framebuffer bottom, logical Y from the top.
			lx := (float64(i) + 0.5) * float64(logical.Width) / float64(fb.Width)
			ly := (float64(fb.Height) - float64(j) - 0.5) * float64(logical.Height) / float64(fb.Height)
			inside := lx > at.X && lx < at.X+logW && ly > at.Y && ly < at.Y+logH
			if !inside {
				if g != 0 {
					outsideInk++
				}
				continue
			}
			checked++
			u := (lx - at.X) / logW
			v := (ly - at.Y) / logH
			want := sampleBilinear(oracle.Alpha, oracle.Width, oracle.Height, oracle.Stride,
				u*float64(oracle.Width)-0.5, v*float64(oracle.Height)-0.5)
			if math.Abs(float64(g)-want) > tolerance {
				mismatches++
			}
		}
	}
	scale := float64(fb.Width) / float64(logical.Width)
	if checked == 0 {
		t.Fatal("no framebuffer pixels fell inside the text quad")
	}
	if mismatches > 0 {
		t.Errorf("scale %v: %d/%d in-quad pixels mismatch the physical oracle alpha (tolerance %v)", scale, mismatches, checked, tolerance)
	}
	if outsideInk > 0 {
		t.Errorf("scale %v: %d pixels outside the logical quad carry ink (quad must be logical-sized)", scale, outsideInk)
	}
	if maxG < 200 {
		t.Errorf("scale %v: brightest text pixel = %d, want >= 200 (solid hinted core present)", scale, maxG)
	}
}

// TestTextPhysicalRasterizationAndResize pins that DrawText rasterizes at the
// target's content scale and draws a logical-sized quad: 2x, then resize to
// 1.5x and 1x, checking framebuffer pixels against independently computed
// oracle bitmaps, and finally a zero framebuffer, which must safely no-op.
func TestTextPhysicalRasterizationAndResize(t *testing.T) {
	backend := newGLTestBackend(t)
	logical := driver.Size{Width: 32, Height: 32}
	r, target := newGLRendererScaled(t, backend, logical, driver.Size{Width: 64, Height: 64})

	face, err := text.NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()

	const value = "Wj"
	at := driver.Point{X: 4, Y: 3}

	// 2x content scale: the physical bitmap is twice the logical size.
	oracle2, err := face.RasterizeScaled(value, 2, 2)
	if err != nil {
		t.Fatalf("RasterizeScaled 2x: %v", err)
	}
	drawAndCheck(t, r, target, face, value, at, oracle2, 2)

	// Resize the framebuffer: the next draw must regenerate the physical
	// bitmap at the new scale, not reuse the 2x one.
	target.fb = driver.Size{Width: 48, Height: 48}
	oracle15, err := face.RasterizeScaled(value, 1.5, 1.5)
	if err != nil {
		t.Fatalf("RasterizeScaled 1.5x: %v", err)
	}
	drawAndCheck(t, r, target, face, value, at, oracle15, 2)

	// Back to 1x: at this scale the sampling hits texel centers, so the
	// framebuffer must match the alpha mask almost exactly.
	target.fb = driver.Size{Width: 32, Height: 32}
	oracle1, err := face.RasterizeScaled(value, 1, 1)
	if err != nil {
		t.Fatalf("RasterizeScaled 1x: %v", err)
	}
	drawAndCheck(t, r, target, face, value, at, oracle1, 1)

	// Zero framebuffer: Begin skips the frame and DrawText safely no-ops.
	target.fb = driver.Size{}
	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin with zero framebuffer: %v", err)
	}
	if err := r.DrawText(value, at, render.Color{G: 1, A: 1}, face); err != nil {
		t.Fatalf("DrawText with zero framebuffer: %v", err)
	}
	if err := r.End(); err != nil {
		t.Fatalf("End with zero framebuffer: %v", err)
	}
}
