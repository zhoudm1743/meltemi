// Package gl implements the render abstraction on OpenGL 3.3 Core. Logical
// drawing coordinates are converted to physical pixels by projection, so
// rendering stays crisp on high-DPI displays.
package gl

import (
	"errors"
	"fmt"

	gogl "github.com/go-gl/gl/v3.3-core/gl"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// Renderer draws rectangles into a render.Target using OpenGL. Drawing
// methods and Close must be called on the UI thread while the target's
// context is current; New leaves the context current on the calling thread.
// Discard only drops Go-side cache references and needs no current context.
type Renderer struct {
	target render.Target

	program uint32
	vao     uint32
	vbo     uint32
	uProj   int32

	// skip is set by Begin when the framebuffer or logical size is zero
	// (for example a minimized window): DrawRect, DrawText, DrawImage and
	// SetClip become no-ops, and End does not swap buffers.
	skip bool

	// scaleX and scaleY are the framebuffer-to-logical ratios Begin read
	// from the target; DrawText rasterizes physical text at exactly these
	// scales. They are valid for the current frame only.
	scaleX float64
	scaleY float64
}

// New creates a Renderer for target: it makes the target's context current,
// loads the OpenGL bindings, compiles the shader program and allocates the
// vertex buffer. It returns an error when the context cannot be bound or the
// shaders fail to build.
func New(target render.Target) (*Renderer, error) {
	if target == nil {
		return nil, errors.New("gl: nil render target")
	}
	if err := target.MakeCurrent(); err != nil {
		return nil, fmt.Errorf("gl: make context current: %w", err)
	}
	if err := gogl.Init(); err != nil {
		return nil, fmt.Errorf("gl: initialize OpenGL bindings: %w", err)
	}

	program, err := compileProgram(vertexShaderSource, fragmentShaderSource)
	if err != nil {
		return nil, err
	}

	r := &Renderer{target: target, program: program}
	r.uProj = gogl.GetUniformLocation(program, gogl.Str("uProj\x00"))

	// One interleaved vertex buffer: position (2 floats) then color
	// (4 floats), 6 vertices per rectangle (two triangles).
	gogl.GenVertexArrays(1, &r.vao)
	gogl.BindVertexArray(r.vao)
	gogl.GenBuffers(1, &r.vbo)
	gogl.BindBuffer(gogl.ARRAY_BUFFER, r.vbo)
	stride := int32(6 * 4)
	gogl.VertexAttribPointer(0, 2, gogl.FLOAT, false, stride, gogl.PtrOffset(0))
	gogl.EnableVertexAttribArray(0)
	gogl.VertexAttribPointer(1, 4, gogl.FLOAT, false, stride, gogl.PtrOffset(2*4))
	gogl.EnableVertexAttribArray(1)

	return r, nil
}

// Begin starts a frame: it binds the target's context, reads the current
// framebuffer and logical sizes, resets the per-frame GL state, and clears
// the color buffer. When either size is zero it skips all frame work
// (including the buffer swap in End) and returns nil.
func (r *Renderer) Begin(clear render.Color) error {
	if err := r.target.MakeCurrent(); err != nil {
		return fmt.Errorf("gl: make context current: %w", err)
	}
	fb := r.target.FramebufferSize()
	logical := r.target.Size()
	r.skip = fb.Width <= 0 || fb.Height <= 0 || logical.Width <= 0 || logical.Height <= 0
	if r.skip {
		return nil
	}

	setupFrame(glFrameState{}, fb, clear)

	r.scaleX = float64(fb.Width) / float64(logical.Width)
	r.scaleY = float64(fb.Height) / float64(logical.Height)

	gogl.UseProgram(r.program)
	proj := newProjection(logical).matrix()
	gogl.UniformMatrix3fv(r.uProj, 1, false, &proj[0])
	return nil
}

// frameState is the slice of OpenGL state Begin resets every frame. Begin
// drives it through this interface so tests can record the exact call order
// without a GPU.
type frameState interface {
	viewport(fbSize driver.Size)
	disableScissor()
	clearWith(c render.Color)
	blend()
}

// glFrameState forwards frameState to OpenGL.
type glFrameState struct{}

func (glFrameState) viewport(fbSize driver.Size) {
	gogl.Viewport(0, 0, int32(fbSize.Width), int32(fbSize.Height))
}

func (glFrameState) disableScissor() {
	gogl.Disable(gogl.SCISSOR_TEST)
}

func (glFrameState) clearWith(c render.Color) {
	gogl.ClearColor(c.R, c.G, c.B, c.A)
	gogl.Clear(gogl.COLOR_BUFFER_BIT)
}

// blend configures conventional non-premultiplied source-over for both color
// and alpha. Using SRC_ALPHA for the source alpha factor would square source
// alpha over a transparent destination.
func (glFrameState) blend() {
	gogl.Enable(gogl.BLEND)
	gogl.BlendFuncSeparate(gogl.SRC_ALPHA, gogl.ONE_MINUS_SRC_ALPHA, gogl.ONE, gogl.ONE_MINUS_SRC_ALPHA)
}

// setupFrame establishes the GL state a frame starts from. The order is
// fixed and covered by TestSetupFrameOrder: the viewport is set first, the
// scissor test is disabled before the clear (gl.Clear is affected by
// scissor, and a clip left over from the previous frame would otherwise
// restrict the clear), and blending is configured last.
func setupFrame(state frameState, fbSize driver.Size, clear render.Color) {
	state.viewport(fbSize)
	state.disableScissor()
	state.clearWith(clear)
	state.blend()
}

// DrawRect draws a filled rectangle in logical coordinates. It binds the
// rectangle program explicitly so a preceding DrawText (which switches to
// the text program) cannot leak the wrong shader into this draw.
func (r *Renderer) DrawRect(rect render.Rect, color render.Color) {
	if r.skip {
		return
	}
	x0 := float32(rect.X)
	y0 := float32(rect.Y)
	x1 := float32(rect.X + rect.Width)
	y1 := float32(rect.Y + rect.Height)
	r0, g0, b0, a0 := color.R, color.G, color.B, color.A
	vertices := []float32{
		x0, y0, r0, g0, b0, a0,
		x1, y0, r0, g0, b0, a0,
		x1, y1, r0, g0, b0, a0,
		x0, y0, r0, g0, b0, a0,
		x1, y1, r0, g0, b0, a0,
		x0, y1, r0, g0, b0, a0,
	}
	gogl.UseProgram(r.program)
	gogl.BindVertexArray(r.vao)
	gogl.BindBuffer(gogl.ARRAY_BUFFER, r.vbo)
	gogl.BufferData(gogl.ARRAY_BUFFER, len(vertices)*4, gogl.Ptr(vertices), gogl.DYNAMIC_DRAW)
	gogl.DrawArrays(gogl.TRIANGLES, 0, 6)
}

// SetClip restricts all later drawing in the frame to the logical rectangle,
// converted to physical pixels for the scissor test.
func (r *Renderer) SetClip(rect render.Rect) {
	if r.skip {
		return
	}
	x, y, w, h, ok := clipRect(r.target.FramebufferSize(), r.target.Size(), rect)
	if !ok {
		gogl.Disable(gogl.SCISSOR_TEST)
		return
	}
	gogl.Enable(gogl.SCISSOR_TEST)
	gogl.Scissor(int32(x), int32(y), int32(w), int32(h))
}

// ClearClip removes the clipping set by SetClip.
func (r *Renderer) ClearClip() {
	if r.skip {
		return
	}
	gogl.Disable(gogl.SCISSOR_TEST)
}

// End presents the frame. The buffer swap happens here, not in DrawRect.
func (r *Renderer) End() error {
	if r.skip {
		return nil
	}
	r.target.Present()
	return nil
}

// Close deletes the OpenGL objects owned by the renderer. It is idempotent
// and must be called with the target's context still current, before the
// window is destroyed. Text state, cached text coverage textures, image
// textures and their shared image program/buffers are released before the
// no-assets early return.
func (r *Renderer) Close() {
	releaseTextState(r)
	releaseTextCache(r)
	releaseImageState(r)
	if r.program == 0 && r.vao == 0 && r.vbo == 0 {
		return
	}
	gogl.DeleteProgram(r.program)
	gogl.DeleteBuffers(1, &r.vbo)
	gogl.DeleteVertexArrays(1, &r.vao)
	r.program, r.vao, r.vbo = 0, 0, 0
}
