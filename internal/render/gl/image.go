package gl

import (
	gogl "github.com/go-gl/gl/v3.3-core/gl"

	"github.com/zhoudm1743/meltemi/internal/render"
)

const imageVertexShader = `#version 330 core
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

const imageFragmentShader = `#version 330 core
in vec2 vUV;

uniform sampler2D uTex;
uniform float uOpacity;

out vec4 fragColor;

void main() {
	vec4 texel = texture(uTex, vUV);
	fragColor = vec4(texel.rgb, texel.a * uOpacity);
}
`

type imageState struct {
	program  uint32
	vao      uint32
	vbo      uint32
	uProj    int32
	uOpacity int32
	uTex     int32
	textures map[*render.Image]uint32
}

// imageStates belongs to one Renderer and therefore one native GL context.
// Texture names are never placed in another renderer's cache.
var imageStates = map[*Renderer]*imageState{}

func imageStateFor(r *Renderer) (*imageState, error) {
	if state, ok := imageStates[r]; ok {
		return state, nil
	}
	program, err := compileProgram(imageVertexShader, imageFragmentShader)
	if err != nil {
		return nil, err
	}
	state := &imageState{program: program, textures: map[*render.Image]uint32{}}
	state.uProj = gogl.GetUniformLocation(program, gogl.Str("uProj\x00"))
	state.uOpacity = gogl.GetUniformLocation(program, gogl.Str("uOpacity\x00"))
	state.uTex = gogl.GetUniformLocation(program, gogl.Str("uTex\x00"))

	gogl.GenVertexArrays(1, &state.vao)
	gogl.BindVertexArray(state.vao)
	gogl.GenBuffers(1, &state.vbo)
	gogl.BindBuffer(gogl.ARRAY_BUFFER, state.vbo)
	stride := int32(4 * 4)
	gogl.VertexAttribPointer(0, 2, gogl.FLOAT, false, stride, gogl.PtrOffset(0))
	gogl.EnableVertexAttribArray(0)
	gogl.VertexAttribPointer(1, 2, gogl.FLOAT, false, stride, gogl.PtrOffset(2*4))
	gogl.EnableVertexAttribArray(1)

	imageStates[r] = state
	return state, nil
}

func imageTextureFor(state *imageState, source *render.Image) uint32 {
	if texture, ok := state.textures[source]; ok {
		return texture
	}
	width, height := source.Size()
	pixels := source.Pixels()
	if width <= 0 || height <= 0 || len(pixels) != width*height*4 {
		return 0
	}
	var texture uint32
	gogl.GenTextures(1, &texture)
	gogl.ActiveTexture(gogl.TEXTURE0)
	gogl.BindTexture(gogl.TEXTURE_2D, texture)
	gogl.PixelStorei(gogl.UNPACK_ALIGNMENT, 1)
	gogl.TexParameteri(gogl.TEXTURE_2D, gogl.TEXTURE_MIN_FILTER, gogl.LINEAR)
	gogl.TexParameteri(gogl.TEXTURE_2D, gogl.TEXTURE_MAG_FILTER, gogl.LINEAR)
	gogl.TexParameteri(gogl.TEXTURE_2D, gogl.TEXTURE_WRAP_S, gogl.CLAMP_TO_EDGE)
	gogl.TexParameteri(gogl.TEXTURE_2D, gogl.TEXTURE_WRAP_T, gogl.CLAMP_TO_EDGE)
	gogl.TexImage2D(gogl.TEXTURE_2D, 0, gogl.RGBA8, int32(width), int32(height), 0, gogl.RGBA, gogl.UNSIGNED_BYTE, gogl.Ptr(pixels))
	state.textures[source] = texture
	return texture
}

// DrawImage stretches source into destination in logical coordinates. Begin's
// physical viewport and the logical projection perform framebuffer scaling;
// the active scissor clips this draw just like rectangles and text. opacity is
// the final alpha computed by the UI draw context and is applied only here to
// the source alpha, not multiplied by any component opacity a second time.
func (r *Renderer) DrawImage(source *render.Image, destination render.Rect, opacity float64) error {
	if r.skip || source == nil || destination.Width <= 0 || destination.Height <= 0 || opacity <= 0 {
		return nil
	}
	width, height := source.Size()
	if width <= 0 || height <= 0 {
		return nil
	}
	state, err := imageStateFor(r)
	if err != nil {
		return err
	}
	texture := imageTextureFor(state, source)
	if texture == 0 {
		return nil
	}

	x0 := float32(destination.X)
	y0 := float32(destination.Y)
	x1 := float32(destination.X + destination.Width)
	y1 := float32(destination.Y + destination.Height)
	vertices := []float32{
		x0, y0, 0, 0,
		x1, y0, 1, 0,
		x1, y1, 1, 1,
		x0, y0, 0, 0,
		x1, y1, 1, 1,
		x0, y1, 0, 1,
	}

	gogl.UseProgram(state.program)
	projection := newProjection(r.target.Size()).matrix()
	gogl.UniformMatrix3fv(state.uProj, 1, false, &projection[0])
	gogl.Uniform1f(state.uOpacity, float32(opacity))
	gogl.Uniform1i(state.uTex, 0)
	gogl.ActiveTexture(gogl.TEXTURE0)
	gogl.BindTexture(gogl.TEXTURE_2D, texture)
	gogl.BindVertexArray(state.vao)
	gogl.BindBuffer(gogl.ARRAY_BUFFER, state.vbo)
	gogl.BufferData(gogl.ARRAY_BUFFER, len(vertices)*4, gogl.Ptr(vertices), gogl.DYNAMIC_DRAW)
	gogl.BlendFuncSeparate(gogl.SRC_ALPHA, gogl.ONE_MINUS_SRC_ALPHA, gogl.ONE, gogl.ONE_MINUS_SRC_ALPHA)
	gogl.DrawArrays(gogl.TRIANGLES, 0, 6)
	gogl.BlendFuncSeparate(gogl.SRC_ALPHA, gogl.ONE_MINUS_SRC_ALPHA, gogl.ONE, gogl.ONE_MINUS_SRC_ALPHA)
	gogl.UseProgram(r.program)
	return nil
}

// discardImageState drops only Go references. Native context destruction
// reclaims the GL names when no correct context can be made current.
func discardImageState(r *Renderer) {
	delete(imageStates, r)
}

// releaseImageState deletes all GL resources owned by r's image cache. The
// caller is Renderer.Close with r's own context current.
func releaseImageState(r *Renderer) {
	state, ok := imageStates[r]
	if !ok {
		return
	}
	delete(imageStates, r)
	for _, texture := range state.textures {
		if texture != 0 {
			gogl.DeleteTextures(1, &texture)
		}
	}
	if state.program != 0 {
		gogl.DeleteProgram(state.program)
	}
	if state.vbo != 0 {
		gogl.DeleteBuffers(1, &state.vbo)
	}
	if state.vao != 0 {
		gogl.DeleteVertexArrays(1, &state.vao)
	}
}
