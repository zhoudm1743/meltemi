package ui

import (
	internalapp "github.com/zhoudm1743/meltemi/internal/app"
	"github.com/zhoudm1743/meltemi/internal/driver/glfw"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/render/gl"
)

func newDefaultCore() *internalapp.App {
	return internalapp.New(glfw.New())
}

func newGLRenderer(target render.Target) (renderer, error) {
	return gl.New(target)
}
