// Package render defines the backend-independent primitives the UI draws
// with: colors, rectangles, immutable images and the target rendering happens on.
package render

import "github.com/zhoudm1743/meltemi/internal/driver"

// Color is an RGBA color with each component in the range [0, 1].
type Color struct {
	R, G, B, A float32
}

// Rect is a rectangle in logical coordinates with a top-left origin: X grows
// rightward, Y grows downward.
type Rect struct {
	X, Y, Width, Height float64
}

// Image is an immutable, tightly packed, non-premultiplied RGBA bitmap.
// Its pointer identity is also the texture-cache key used by a renderer.
type Image struct {
	width, height int
	pixels        []byte
}

// NewImage copies width*height*4 bytes into an immutable image. Invalid or
// empty dimensions and malformed pixel buffers produce nil.
func NewImage(width, height int, pixels []byte) *Image {
	if width <= 0 || height <= 0 || width > int(^uint(0)>>1)/4/height {
		return nil
	}
	if len(pixels) != width*height*4 {
		return nil
	}
	return &Image{
		width: width, height: height,
		pixels: append([]byte(nil), pixels...),
	}
}

// Size returns the image dimensions in pixels.
func (i *Image) Size() (width, height int) {
	if i == nil {
		return 0, 0
	}
	return i.width, i.height
}

// Pixels returns a copy of the packed RGBA bytes so the cached image stays
// immutable even while a renderer uploads them.
func (i *Image) Pixels() []byte {
	if i == nil {
		return nil
	}
	return append([]byte(nil), i.pixels...)
}

// Target is a top-level window that OpenGL rendering can happen on. The
// embedded driver.Window supplies the logical size; the remaining methods
// bind the GL context, report the physical framebuffer size and swap buffers.
type Target interface {
	driver.Window
	MakeCurrent() error
	FramebufferSize() driver.Size
	Present()
}
