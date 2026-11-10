package gl

import (
	"errors"
	"fmt"

	gogl "github.com/go-gl/gl/v3.3-core/gl"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// textVertexShader maps logical coordinates to NDC with uProj and forwards
// the texture coordinate. It lives here rather than in shaders.go so the
// rectangle path stays untouched.
const textVertexShader = `#version 330 core
layout(location = 0) in vec2 aPos;
layout(location = 1) in vec2 aUV;

uniform mat3 uProj;

out vec2 vUV;

void main() {
	vec3 ndc = uProj * vec3(aPos, 1.0);
	gl_Position = vec4(ndc.xy, 0.0, 1.0);
	vUV = aUV;
}
`

// textFragmentShader turns the red channel of the alpha texture into text
// coverage modulated by the uniform text color.
const textFragmentShader = `#version 330 core
in vec2 vUV;

uniform sampler2D uTex;
uniform vec4 uColor;

out vec4 fragColor;

void main() {
	float coverage = texture(uTex, vUV).r;
	fragColor = vec4(uColor.rgb, uColor.a * coverage);
}
`

// textState is the GL state DrawText needs beyond the rectangle renderer: a
// program sampling an alpha texture and a small vertex buffer for one
// textured quad.
type textState struct {
	program uint32
	vao     uint32
	vbo     uint32
	uProj   int32
	uColor  int32
	uTex    int32
}

// textStates caches one textState per Renderer. Like every Renderer method,
// DrawText runs on the UI thread, so the map needs no synchronization.
var textStates = map[*Renderer]*textState{}

// textStateFor returns the cached text state for r, compiling the program
// and allocating the vertex buffer on first use.
func textStateFor(r *Renderer) (*textState, error) {
	if s, ok := textStates[r]; ok {
		return s, nil
	}

	program, err := compileProgram(textVertexShader, textFragmentShader)
	if err != nil {
		return nil, err
	}

	s := &textState{program: program}
	s.uProj = gogl.GetUniformLocation(program, gogl.Str("uProj\x00"))
	s.uColor = gogl.GetUniformLocation(program, gogl.Str("uColor\x00"))
	s.uTex = gogl.GetUniformLocation(program, gogl.Str("uTex\x00"))

	gogl.GenVertexArrays(1, &s.vao)
	gogl.BindVertexArray(s.vao)
	gogl.GenBuffers(1, &s.vbo)
	gogl.BindBuffer(gogl.ARRAY_BUFFER, s.vbo)
	stride := int32(4 * 4)
	gogl.VertexAttribPointer(0, 2, gogl.FLOAT, false, stride, gogl.PtrOffset(0))
	gogl.EnableVertexAttribArray(0)
	gogl.VertexAttribPointer(1, 2, gogl.FLOAT, false, stride, gogl.PtrOffset(2*4))
	gogl.EnableVertexAttribArray(1)

	textStates[r] = s
	return s, nil
}

// DrawText rasterizes value with face at the target's content scale and
// draws it as an alpha-textured quad whose top-left corner sits at the
// logical position at, tinted with color. The alpha mask is uploaded at
// physical resolution (twice the pixels at content scale 2) while the quad
// spans the bitmap's logical size, so text stays crisp on high-DPI targets
// without affecting logical layout. The coverage texture is cached per
// renderer in a bounded LRU keyed by face, text and physical scale — color
// stays out of the key and is applied per draw through the program uniform —
// so repeated draws of the same text skip rasterization and upload. Empty
// text is a no-op that creates no GL resources, and a failed rasterization
// caches nothing. Like every Renderer method it must be called on the UI
// thread with the target's context current, between Begin and End.
func (r *Renderer) DrawText(value string, at driver.Point, color render.Color, face *text.Face) error {
	if r.skip {
		return nil
	}
	if face == nil {
		return errors.New("gl: draw text with nil face")
	}
	if value == "" {
		return nil
	}

	cache := textCacheFor(r)
	key := textCacheKey{face: face, value: value, scaleX: r.scaleX, scaleY: r.scaleY}
	entry, ok := cache.lookup(key)
	var texture uint32
	var logicalWidth, logicalHeight float64
	if ok {
		texture = entry.tex
		logicalWidth = entry.logicalWidth
		logicalHeight = entry.logicalHeight
	} else {
		bmp, err := face.RasterizeScaled(value, r.scaleX, r.scaleY)
		if err != nil {
			return fmt.Errorf("gl: rasterize text: %w", err)
		}
		if bmp.Width <= 0 || bmp.Height <= 0 || len(bmp.Alpha) == 0 {
			return nil
		}
		texture = cache.upload(key, bmp)
		logicalWidth = bmp.LogicalWidth
		logicalHeight = bmp.LogicalHeight
	}

	state, err := textStateFor(r)
	if err != nil {
		return err
	}

	x0 := float32(at.X)
	y0 := float32(at.Y)
	x1 := float32(at.X + logicalWidth)
	y1 := float32(at.Y + logicalHeight)
	vertices := []float32{
		x0, y0, 0, 0,
		x1, y0, 1, 0,
		x1, y1, 1, 1,
		x0, y0, 0, 0,
		x1, y1, 1, 1,
		x0, y1, 0, 1,
	}

	gogl.UseProgram(state.program)
	proj := newProjection(r.target.Size()).matrix()
	gogl.UniformMatrix3fv(state.uProj, 1, false, &proj[0])
	gogl.Uniform4f(state.uColor, color.R, color.G, color.B, color.A)
	gogl.Uniform1i(state.uTex, 0)
	gogl.ActiveTexture(gogl.TEXTURE0)
	gogl.BindTexture(gogl.TEXTURE_2D, texture)
	gogl.BindVertexArray(state.vao)
	gogl.BindBuffer(gogl.ARRAY_BUFFER, state.vbo)
	gogl.BufferData(gogl.ARRAY_BUFFER, len(vertices)*4, gogl.Ptr(vertices), gogl.DYNAMIC_DRAW)
	gogl.DrawArrays(gogl.TRIANGLES, 0, 6)
	// Leave the frame the way rectangle drawing expects: Begin binds
	// r.program once per frame and DrawRect must not depend on whatever
	// program a preceding DrawText left current.
	gogl.UseProgram(r.program)
	return nil
}

// Discard drops cached text state, text coverage texture and image state
// references without making GL calls. It is safe without a current context;
// native window destruction reclaims the GL objects whose names were stored
// in the discarded state.
func (r *Renderer) Discard() {
	delete(textStates, r)
	discardTextCache(r)
	discardImageState(r)
}

// releaseTextState frees the text program and buffers cached for r and drops
// the cache entry so the Renderer and its target can be collected. It is
// idempotent and is called from Renderer.Close, which runs on the UI thread
// with the target's context still current.
func releaseTextState(r *Renderer) {
	s, ok := textStates[r]
	if !ok {
		return
	}
	delete(textStates, r)
	if s.program != 0 {
		gogl.DeleteProgram(s.program)
	}
	if s.vbo != 0 {
		gogl.DeleteBuffers(1, &s.vbo)
	}
	if s.vao != 0 {
		gogl.DeleteVertexArrays(1, &s.vao)
	}
}
