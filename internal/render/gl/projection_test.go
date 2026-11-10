package gl

import (
	"math"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

func TestProjectLogicalCorners(t *testing.T) {
	p := newProjection(driver.Size{Width: 800, Height: 600})

	cases := []struct {
		name         string
		x, y         float64
		wantX, wantY float64
	}{
		{"top-left", 0, 0, -1, 1},
		{"top-right", 800, 0, 1, 1},
		{"bottom-left", 0, 600, -1, -1},
		{"bottom-right", 800, 600, 1, -1},
		{"center", 400, 300, 0, 0},
	}
	for _, c := range cases {
		gotX, gotY := p.project(c.x, c.y)
		if gotX != c.wantX || gotY != c.wantY {
			t.Errorf("%s: project(%v, %v) = (%v, %v), want (%v, %v)",
				c.name, c.x, c.y, gotX, gotY, c.wantX, c.wantY)
		}
	}
}

func TestClipPixelsHighDPI(t *testing.T) {
	// Logical 400x300 with an 800x600 framebuffer is a 1:2 high-DPI display.
	// Scissor boxes use physical pixels with a bottom-left origin.
	x, y, w, h, ok := clipRect(
		driver.Size{Width: 800, Height: 600},
		driver.Size{Width: 400, Height: 300},
		render.Rect{X: 10, Y: 20, Width: 30, Height: 40},
	)
	if !ok {
		t.Fatal("clipRect: ok = false, want true")
	}
	if x != 20 || y != 480 || w != 60 || h != 80 {
		t.Errorf("clipRect = (%d, %d, %d, %d), want (20, 480, 60, 80)", x, y, w, h)
	}
}

func TestClipPixelsOrigin(t *testing.T) {
	// At 1:1 the physical box keeps its size; only the Y axis flips to the
	// bottom-left origin OpenGL expects.
	x, y, w, h, ok := clipRect(
		driver.Size{Width: 800, Height: 600},
		driver.Size{Width: 800, Height: 600},
		render.Rect{X: 10, Y: 20, Width: 30, Height: 40},
	)
	if !ok {
		t.Fatal("clipRect: ok = false, want true")
	}
	if x != 10 || y != 540 || w != 30 || h != 40 {
		t.Errorf("clipRect = (%d, %d, %d, %d), want (10, 540, 30, 40)", x, y, w, h)
	}
}

func TestClipZeroFramebuffer(t *testing.T) {
	rect := render.Rect{X: 10, Y: 20, Width: 30, Height: 40}
	for _, fb := range []driver.Size{{Width: 0, Height: 0}, {Width: 0, Height: 600}, {Width: 800, Height: 0}} {
		x, y, w, h, ok := clipRect(fb, driver.Size{Width: 400, Height: 300}, rect)
		if ok {
			t.Errorf("clipRect(fb=%v): ok = true, want false", fb)
		}
		if x != 0 || y != 0 || w != 0 || h != 0 {
			t.Errorf("clipRect(fb=%v) = (%d, %d, %d, %d), want all zero", fb, x, y, w, h)
		}
	}
}

func TestClipDegenerateRect(t *testing.T) {
	// An empty clip region must map to an empty scissor box, not a NaN or a
	// negative size leaking into gl.Scissor.
	for _, rect := range []render.Rect{
		{X: 10, Y: 20, Width: 0, Height: 40},
		{X: 10, Y: 20, Width: 30, Height: 0},
		{X: 10, Y: 20, Width: -5, Height: 40},
	} {
		x, y, w, h, ok := clipRect(
			driver.Size{Width: 800, Height: 600},
			driver.Size{Width: 400, Height: 300},
			rect,
		)
		if !ok {
			t.Errorf("clipRect(%v): ok = false, want true", rect)
		}
		if w < 0 || h < 0 {
			t.Errorf("clipRect(%v) = (%d, %d, %d, %d), negative size", rect, x, y, w, h)
		}
		if w != 0 && h != 0 {
			t.Errorf("clipRect(%v) = (%d, %d, %d, %d), want an empty box", rect, x, y, w, h)
		}
	}
}

func TestProjectZeroLogicalSize(t *testing.T) {
	// A zero logical size must not divide by zero or produce NaN; the
	// mapping falls back to the identity.
	for _, size := range []driver.Size{{Width: 0, Height: 0}, {Width: 0, Height: 600}, {Width: 800, Height: 0}} {
		p := newProjection(size)
		gotX, gotY := p.project(10, 20)
		if math.IsNaN(gotX) || math.IsNaN(gotY) {
			t.Errorf("project with logical size %v produced NaN (%v, %v)", size, gotX, gotY)
		}
		if gotX != 10 || gotY != 20 {
			t.Errorf("project with logical size %v = (%v, %v), want identity (10, 20)", size, gotX, gotY)
		}
	}
}

// applyMatrix multiplies the column-major 3x3 matrix by (x, y, 1), the same
// product the vertex shader computes.
func applyMatrix(m [9]float32, x, y float64) (float64, float64) {
	ndcX := float64(m[0])*x + float64(m[3])*y + float64(m[6])
	ndcY := float64(m[1])*x + float64(m[4])*y + float64(m[7])
	return ndcX, ndcY
}

func TestMatrixMapsLogicalCorners(t *testing.T) {
	// The renderer uploads matrix() to the GPU; verify the actual GPU path
	// by multiplying it the way the vertex shader does.
	p := newProjection(driver.Size{Width: 800, Height: 600})
	m := p.matrix()

	cases := []struct {
		name         string
		x, y         float64
		wantX, wantY float64
	}{
		{"top-left", 0, 0, -1, 1},
		{"top-right", 800, 0, 1, 1},
		{"bottom-left", 0, 600, -1, -1},
		{"bottom-right", 800, 600, 1, -1},
		{"center", 400, 300, 0, 0},
	}
	const eps = 1e-5 // the shader computes in float32
	for _, c := range cases {
		gotX, gotY := applyMatrix(m, c.x, c.y)
		if math.Abs(gotX-c.wantX) > eps || math.Abs(gotY-c.wantY) > eps {
			t.Errorf("%s: matrix * (%v, %v, 1) = (%v, %v), want (%v, %v)",
				c.name, c.x, c.y, gotX, gotY, c.wantX, c.wantY)
		}
	}
}

func TestMatrixZeroLogicalSize(t *testing.T) {
	// Like project, the zero-size matrix must not produce NaN; it falls
	// back to the identity.
	for _, size := range []driver.Size{{Width: 0, Height: 0}, {Width: 0, Height: 600}, {Width: 800, Height: 0}} {
		m := newProjection(size).matrix()
		gotX, gotY := applyMatrix(m, 10, 20)
		if math.IsNaN(gotX) || math.IsNaN(gotY) {
			t.Errorf("matrix with logical size %v produced NaN (%v, %v)", size, gotX, gotY)
		}
		if gotX != 10 || gotY != 20 {
			t.Errorf("matrix with logical size %v maps (10, 20) to (%v, %v), want identity", size, gotX, gotY)
		}
	}
}
