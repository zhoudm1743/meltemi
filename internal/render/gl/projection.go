package gl

import (
	"math"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

// projection maps logical coordinates to OpenGL normalized device
// coordinates. Logical coordinates have a top-left origin with Y pointing
// down; NDC have a bottom-left origin with Y pointing up.
type projection struct {
	width  float64
	height float64
}

func newProjection(size driver.Size) projection {
	return projection{width: float64(size.Width), height: float64(size.Height)}
}

// mapping returns the affine coefficients shared by project and matrix, so
// the tested coordinate path and the GPU path cannot drift apart:
// ndcX = sx*x + ox, ndcY = sy*y + oy. A zero logical size yields the
// identity mapping and ok == false, so no division by zero or NaN can escape;
// the renderer skips drawing while in that state.
func (p projection) mapping() (sx, ox, sy, oy float64, ok bool) {
	if p.width <= 0 || p.height <= 0 {
		return 1, 0, 1, 0, false
	}
	return 2 / p.width, -1, -2 / p.height, 1, true
}

// project converts a logical point to NDC in the range [-1, 1]. The Y axis is
// flipped: logical Y grows downward, NDC Y grows upward.
func (p projection) project(x, y float64) (float64, float64) {
	sx, ox, sy, oy, _ := p.mapping()
	return sx*x + ox, sy*y + oy
}

// matrix returns the projection as a 3x3 column-major matrix for the
// uProj uniform: ndc = matrix * vec3(x, y, 1).
func (p projection) matrix() [9]float32 {
	sx, ox, sy, oy, _ := p.mapping()
	return [9]float32{
		float32(sx), 0, 0,
		0, float32(sy), 0,
		float32(ox), float32(oy), 1,
	}
}

// clipRect converts a logical rectangle into physical framebuffer pixels for
// gl.Scissor, whose box has a bottom-left origin. ok is false when either the
// framebuffer or the logical size is zero, in which case the returned values
// are all zero. A degenerate rectangle maps to an empty box (zero width or
// height) rather than a negative size.
func clipRect(fb, logical driver.Size, rect render.Rect) (x, y, width, height int, ok bool) {
	if fb.Width <= 0 || fb.Height <= 0 || logical.Width <= 0 || logical.Height <= 0 {
		return 0, 0, 0, 0, false
	}
	scaleX := float64(fb.Width) / float64(logical.Width)
	scaleY := float64(fb.Height) / float64(logical.Height)

	left := int(math.Round(rect.X * scaleX))
	right := int(math.Round((rect.X + rect.Width) * scaleX))
	top := int(math.Round(rect.Y * scaleY))
	bottom := int(math.Round((rect.Y + rect.Height) * scaleY))

	w := max(right-left, 0)
	h := max(bottom-top, 0)
	// The scissor Y is measured from the framebuffer bottom.
	return left, fb.Height - bottom, w, h, true
}
