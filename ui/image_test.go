package ui

import (
	"bytes"
	"errors"
	"image"
	stdcolor "image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

type imagePaintOp struct {
	source      *render.Image
	destination render.Rect
	opacity     float64
}

type imagePainter struct {
	stylePainter
	draws   []imagePaintOp
	drawErr error
}

func (p *imagePainter) DrawImage(source *render.Image, destination render.Rect, opacity float64) error {
	p.draws = append(p.draws, imagePaintOp{source: source, destination: destination, opacity: opacity})
	return p.drawErr
}

func TestImageNaturalAndStyledSize(t *testing.T) {
	source := image.NewNRGBA(image.Rect(8, 9, 15, 14))
	widget := Image(source)
	if got := widget.ID("hero").Class("artwork"); got != widget {
		t.Fatal("Image ID/Class did not return its own pointer")
	}
	if got, want := widget.styleMeta(), (internalstyle.Meta{Type: "Image", ID: "hero", Class: "artwork"}); got != want {
		t.Errorf("Image style metadata = %+v, want %+v", got, want)
	}
	if got, want := widget.measure(Constraints{}), (Size{Width: 7, Height: 5}); got != want {
		t.Errorf("Image natural size = %+v, want %+v", got, want)
	}

	styled := resolved(map[string]string{"width": "30px", "height": "20px"})
	if got, want := widget.measureStyled(Constraints{}, styled), (Size{Width: 30, Height: 20}); got != want {
		t.Errorf("Image styled size = %+v, want %+v", got, want)
	}
	if got, want := widget.measureStyled(Constraints{Max: Size{Width: 25}}, styled), (Size{Width: 25, Height: 20}); got != want {
		t.Errorf("Image constrained styled size = %+v, want %+v", got, want)
	}

	widget.SetImage(image.NewNRGBA(image.Rect(0, 0, 4, 9)))
	if got, want := widget.measure(Constraints{}), (Size{Width: 4, Height: 9}); got != want {
		t.Errorf("Image size after SetImage = %+v, want %+v", got, want)
	}
	widget.SetImage(nil)
	if got := widget.measure(Constraints{}); got != (Size{}) {
		t.Errorf("nil Image size = %+v, want zero", got)
	}
	widget.SetImage(image.NewNRGBA(image.Rect(4, 4, 4, 4)))
	if got := widget.measure(Constraints{}); got != (Size{}) {
		t.Errorf("empty Image size = %+v, want zero", got)
	}
}

func TestLoadImagePNGAndInvalidData(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	source.SetNRGBA(0, 0, stdcolor.NRGBA{R: 200, G: 100, B: 50, A: 255})
	tests := []struct {
		name   string
		ext    string
		encode func(io.Writer, image.Image) error
	}{
		{name: "PNG", ext: ".png", encode: func(w io.Writer, img image.Image) error { return png.Encode(w, img) }},
		{name: "JPEG", ext: ".jpg", encode: func(w io.Writer, img image.Image) error { return jpeg.Encode(w, img, nil) }},
		{name: "GIF", ext: ".gif", encode: func(w io.Writer, img image.Image) error { return gif.Encode(w, img, nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var encoded bytes.Buffer
			if err := tt.encode(&encoded, source); err != nil {
				t.Fatalf("encode %s: %v", tt.name, err)
			}
			path := filepath.Join(t.TempDir(), "source"+tt.ext)
			if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
				t.Fatalf("write encoded image: %v", err)
			}
			widget, err := LoadImage(path)
			if err != nil {
				t.Fatalf("LoadImage(%s): %v", tt.name, err)
			}
			if got, want := widget.measure(Constraints{}), (Size{Width: 3, Height: 2}); got != want {
				t.Errorf("loaded %s size = %+v, want %+v", tt.name, got, want)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "invalid.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatalf("write invalid image: %v", err)
	}
	widget, err := LoadImage(path)
	if err == nil || widget != nil {
		t.Fatalf("LoadImage(invalid) = (%v, %v), want nil widget and error", widget, err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("invalid-data error %q does not contain path %q", err, path)
	}
	missing := filepath.Join(t.TempDir(), "missing.gif")
	if _, err := LoadImage(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("LoadImage(missing) error = %v, want path %q", err, missing)
	}
}

func TestImageDrawDestination(t *testing.T) {
	source := image.NewNRGBA(image.Rect(5, 6, 7, 7))
	source.SetNRGBA(5, 6, stdcolor.NRGBA{R: 200, G: 100, B: 50, A: 128})
	source.SetNRGBA(6, 6, stdcolor.NRGBA{R: 1, G: 2, B: 3, A: 255})
	widget := Image(source)
	internal := widget.source
	source.SetNRGBA(5, 6, stdcolor.NRGBA{})

	pixels := internal.Pixels()
	if want := []byte{200, 100, 50, 128, 1, 2, 3, 255}; !bytes.Equal(pixels, want) {
		t.Errorf("Image RGBA pixels = %v, want %v", pixels, want)
	}
	pixels[0] = 0
	if got := internal.Pixels()[0]; got != 200 {
		t.Errorf("Image exposed mutable pixels: first channel = %d, want 200", got)
	}

	destination := render.Rect{X: -12, Y: 20, Width: 40, Height: 18}
	widget.arrange(destination)
	painter := &imagePainter{}
	ctx := &drawContext{painter: painter, opacity: 0.375}
	drawResolvedComponent(ctx, widget)
	if len(painter.draws) != 1 {
		t.Fatalf("Image paint calls = %d, want 1", len(painter.draws))
	}
	if got, want := painter.draws[0], (imagePaintOp{source: internal, destination: destination, opacity: 0.375}); got != want {
		t.Errorf("Image paint = %+v, want %+v", got, want)
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("Image draw error = %v, want nil", err)
	}

	drawErr := errors.New("image upload failed")
	painter.drawErr = drawErr
	ctx = &drawContext{painter: painter, opacity: 1}
	widget.draw(ctx)
	if ctx.Err() != drawErr {
		t.Errorf("Image drawContext error = %v, want %v", ctx.Err(), drawErr)
	}

	painter.drawErr = nil
	painter.draws = nil
	empty := Image(nil)
	empty.arrange(render.Rect{Width: 10, Height: 10})
	empty.draw(ctx)
	widget.arrange(render.Rect{X: 1, Y: 2, Width: 0, Height: 10})
	widget.draw(ctx)
	widget.arrange(render.Rect{X: 1, Y: 2, Width: 10, Height: -1})
	widget.draw(ctx)
	if len(painter.draws) != 0 {
		t.Errorf("nil or degenerate Image painted %d calls, want 0", len(painter.draws))
	}
}
