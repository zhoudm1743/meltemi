package ui

import (
	"fmt"
	"image"
	stdcolor "image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

// ImageWidget draws an immutable snapshot of a standard-library image. Its
// natural size is the source's pixel size; stylesheet width and height can
// override it and stretch the image to the arranged content rectangle.
type ImageWidget struct {
	source   *render.Image
	bounds   render.Rect
	id       string
	class    string
	resolved internalstyle.Resolved
}

// Image creates an image component from source. A nil or empty source has
// zero natural size and paints nothing.
func Image(source image.Image) *ImageWidget {
	widget := &ImageWidget{}
	widget.SetImage(source)
	return widget
}

// LoadImage decodes a PNG, JPEG or GIF file and wraps the first image in an
// ImageWidget. Open and decode errors include the source path.
func LoadImage(path string) (*ImageWidget, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ui: load image %s: %w", path, err)
	}
	defer file.Close()
	source, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("ui: load image %s: %w", path, err)
	}
	return Image(source), nil
}

// SetImage replaces the source with a fresh immutable RGBA snapshot. The new
// internal pointer makes each renderer upload a matching new texture instead
// of reusing pixels cached for the previous source.
func (i *ImageWidget) SetImage(source image.Image) {
	i.source = imagePixels(source)
}

// ID sets the stylesheet ID selector metadata and returns the image.
func (i *ImageWidget) ID(value string) *ImageWidget {
	i.id = value
	return i
}

// Class sets the stylesheet class selector metadata and returns the image.
func (i *ImageWidget) Class(value string) *ImageWidget {
	i.class = value
	return i
}

func (i *ImageWidget) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{Type: "Image", ID: i.id, Class: i.class}
}

func (i *ImageWidget) setResolved(resolved internalstyle.Resolved) {
	i.resolved = resolved
}

func imagePixels(source image.Image) *render.Image {
	if source == nil {
		return nil
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || width > int(^uint(0)>>1)/4/height {
		return nil
	}
	pixels := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixel := stdcolor.NRGBAModel.Convert(source.At(bounds.Min.X+x, bounds.Min.Y+y)).(stdcolor.NRGBA)
			offset := (y*width + x) * 4
			pixels[offset] = pixel.R
			pixels[offset+1] = pixel.G
			pixels[offset+2] = pixel.B
			pixels[offset+3] = pixel.A
		}
	}
	return render.NewImage(width, height, pixels)
}

func (i *ImageWidget) measure(constraints Constraints) Size {
	return i.measureStyled(constraints, i.resolved)
}

func (i *ImageWidget) arrange(bounds render.Rect) {
	i.bounds = bounds
}

func (i *ImageWidget) draw(ctx *drawContext) {
	i.drawStyled(ctx, i.resolved)
}

func (i *ImageWidget) drawWithStyle(ctx *drawContext, style componentStyle) {
	drawComponentBox(ctx, i.bounds, style)
	if i.source == nil {
		return
	}
	content := styleContentRect(i.bounds, style)
	if content.Width <= 0 || content.Height <= 0 {
		return
	}
	ctx.DrawImage(i.source, content)
}

func (i *ImageWidget) handleEvent(driver.Event) (handled, redraw bool) {
	return false, false
}

func (i *ImageWidget) walk(visit func(component)) {
	visit(i)
}

func (i *ImageWidget) isUIComponent() {}

func (i *ImageWidget) arrangedBounds() render.Rect { return i.bounds }
