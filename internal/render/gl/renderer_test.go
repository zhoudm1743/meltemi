package gl

import (
	"slices"
	"testing"

	gogl "github.com/go-gl/gl/v3.3-core/gl"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// recordingFrameState records the frameState calls in order instead of
// issuing GL calls.
type recordingFrameState struct {
	calls []string
}

func (rec *recordingFrameState) viewport(driver.Size) {
	rec.calls = append(rec.calls, "viewport")
}

func (rec *recordingFrameState) disableScissor() {
	rec.calls = append(rec.calls, "disableScissor")
}

func (rec *recordingFrameState) clearWith(render.Color) {
	rec.calls = append(rec.calls, "clearWith")
}

func (rec *recordingFrameState) blend() {
	rec.calls = append(rec.calls, "blend")
}

func TestSetupFrameOrder(t *testing.T) {
	// gl.Clear is affected by the scissor test: the scissor left over from
	// the previous frame must be disabled before the clear, or the new
	// frame only clears the old clip region.
	rec := &recordingFrameState{}
	setupFrame(rec, driver.Size{Width: 800, Height: 600}, render.Color{R: 1, A: 1})

	want := []string{"viewport", "disableScissor", "clearWith", "blend"}
	if !slices.Equal(rec.calls, want) {
		t.Errorf("setupFrame order = %v, want %v", rec.calls, want)
	}
}

func TestDiscardWithoutContext(t *testing.T) {
	r := &Renderer{}
	textStates[r] = &textState{program: 1, vao: 2, vbo: 3}
	t.Cleanup(func() { delete(textStates, r) })

	r.Discard()
	r.Discard()

	if _, ok := textStates[r]; ok {
		t.Error("textStates entry survived Discard")
	}
}

func assertSourceOverBlend(t *testing.T) {
	t.Helper()
	var sourceRGB, destinationRGB, sourceAlpha, destinationAlpha int32
	gogl.GetIntegerv(gogl.BLEND_SRC_RGB, &sourceRGB)
	gogl.GetIntegerv(gogl.BLEND_DST_RGB, &destinationRGB)
	gogl.GetIntegerv(gogl.BLEND_SRC_ALPHA, &sourceAlpha)
	gogl.GetIntegerv(gogl.BLEND_DST_ALPHA, &destinationAlpha)
	if sourceRGB != gogl.SRC_ALPHA || destinationRGB != gogl.ONE_MINUS_SRC_ALPHA || sourceAlpha != gogl.ONE || destinationAlpha != gogl.ONE_MINUS_SRC_ALPHA {
		t.Errorf("blend factors RGB=%v/%v alpha=%v/%v, want SRC_ALPHA/ONE_MINUS_SRC_ALPHA and ONE/ONE_MINUS_SRC_ALPHA", sourceRGB, destinationRGB, sourceAlpha, destinationAlpha)
	}
}

func TestSourceOverAlphaForRectTextAndImage(t *testing.T) {
	color := render.Color{R: 1, A: 128.0 / 255.0}
	destination := render.Rect{X: 4, Y: 4, Width: 8, Height: 8}
	wantPixel := [4]uint8{128, 0, 0, 128}

	t.Run("rect", func(t *testing.T) {
		r, _ := newGLRenderer(t)
		if err := r.Begin(render.Color{}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		r.DrawRect(destination, color)
		assertSourceOverBlend(t)
		gogl.Finish()
		if pixel := readImagePixel(t, r, 8, 8); pixel != wantPixel {
			t.Errorf("translucent rect pixel = %v, want exact source-over %v", pixel, wantPixel)
		}
		if err := r.End(); err != nil {
			t.Fatalf("End: %v", err)
		}
	})

	t.Run("text", func(t *testing.T) {
		r, _ := newGLRenderer(t)
		face, err := text.NewFace(goregular.TTF, 24)
		if err != nil {
			t.Fatalf("text.NewFace: %v", err)
		}
		defer face.Close()
		bitmap, err := face.Rasterize("I")
		if err != nil {
			t.Fatalf("Rasterize: %v", err)
		}
		sample := driver.Point{}
		found := false
		for y := 0; y < bitmap.Height && !found; y++ {
			for x := 0; x < bitmap.Width; x++ {
				if bitmap.Alpha[y*bitmap.Stride+x] == 255 {
					sample = driver.Point{X: float64(x), Y: float64(y)}
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatal("test glyph has no fully covered pixel")
		}
		if err := r.Begin(render.Color{}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := r.DrawText("I", driver.Point{}, color, face); err != nil {
			t.Fatalf("DrawText: %v", err)
		}
		assertSourceOverBlend(t)
		gogl.Finish()
		if pixel := readImagePixel(t, r, sample.X, sample.Y); pixel != wantPixel {
			t.Errorf("fully covered translucent text pixel = %v, want exact source-over %v", pixel, wantPixel)
		}
		if err := r.End(); err != nil {
			t.Fatalf("End: %v", err)
		}
	})

	t.Run("image and restored state", func(t *testing.T) {
		r, _ := newGLRenderer(t)
		source := render.NewImage(1, 1, []byte{255, 0, 0, 128})
		if err := r.Begin(render.Color{}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := r.DrawImage(source, destination, 1); err != nil {
			t.Fatalf("DrawImage: %v", err)
		}
		assertSourceOverBlend(t)
		gogl.Finish()
		if pixel := readImagePixel(t, r, 8, 8); pixel != wantPixel {
			t.Errorf("translucent image pixel = %v, want exact source-over %v", pixel, wantPixel)
		}
		if err := r.End(); err != nil {
			t.Fatalf("End: %v", err)
		}
	})
}
