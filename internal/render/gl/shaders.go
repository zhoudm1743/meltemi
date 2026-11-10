package gl

import (
	"fmt"
	"strings"

	gogl "github.com/go-gl/gl/v3.3-core/gl"
)

// vertexShaderSource maps logical coordinates to normalized device
// coordinates with uProj and forwards the per-vertex color.
const vertexShaderSource = `#version 330 core
layout(location = 0) in vec2 aPos;
layout(location = 1) in vec4 aColor;

uniform mat3 uProj;

out vec4 vColor;

void main() {
	vec3 ndc = uProj * vec3(aPos, 1.0);
	gl_Position = vec4(ndc.xy, 0.0, 1.0);
	vColor = aColor;
}
`

// fragmentShaderSource outputs the interpolated vertex color unchanged.
const fragmentShaderSource = `#version 330 core
in vec4 vColor;

out vec4 fragColor;

void main() {
	fragColor = vColor;
}
`

// compileProgram links a vertex and fragment shader into a program,
// deleting the shaders and the half-built program when anything fails.
func compileProgram(vertexSrc, fragmentSrc string) (uint32, error) {
	vertex, err := compileShader(gogl.VERTEX_SHADER, vertexSrc)
	if err != nil {
		return 0, err
	}
	defer gogl.DeleteShader(vertex)

	fragment, err := compileShader(gogl.FRAGMENT_SHADER, fragmentSrc)
	if err != nil {
		return 0, err
	}
	defer gogl.DeleteShader(fragment)

	program := gogl.CreateProgram()
	gogl.AttachShader(program, vertex)
	gogl.AttachShader(program, fragment)
	gogl.LinkProgram(program)

	var status int32
	gogl.GetProgramiv(program, gogl.LINK_STATUS, &status)
	if status == gogl.FALSE {
		log := programInfoLog(program)
		gogl.DeleteProgram(program)
		return 0, fmt.Errorf("gl: link program: %s", log)
	}
	return program, nil
}

// compileShader compiles one shader source, deleting the shader when it does
// not compile. The source is copied into C memory: passing a Go pointer to a
// Go string through cgo panics under Go 1.26's cgocheck.
func compileShader(shaderType uint32, source string) (uint32, error) {
	shader := gogl.CreateShader(shaderType)
	csources, free := gogl.Strs(source + "\x00")
	defer free()
	gogl.ShaderSource(shader, 1, csources, nil)
	gogl.CompileShader(shader)

	var status int32
	gogl.GetShaderiv(shader, gogl.COMPILE_STATUS, &status)
	if status == gogl.FALSE {
		log := shaderInfoLog(shader)
		gogl.DeleteShader(shader)
		return 0, fmt.Errorf("gl: compile shader: %s", log)
	}
	return shader, nil
}

func shaderInfoLog(shader uint32) string {
	var length int32
	gogl.GetShaderiv(shader, gogl.INFO_LOG_LENGTH, &length)
	if length <= 1 {
		return ""
	}
	log := strings.Repeat("\x00", int(length))
	gogl.GetShaderInfoLog(shader, length, nil, gogl.Str(log))
	return strings.TrimSpace(log)
}

func programInfoLog(program uint32) string {
	var length int32
	gogl.GetProgramiv(program, gogl.INFO_LOG_LENGTH, &length)
	if length <= 1 {
		return ""
	}
	log := strings.Repeat("\x00", int(length))
	gogl.GetProgramInfoLog(program, length, nil, gogl.Str(log))
	return strings.TrimSpace(log)
}
