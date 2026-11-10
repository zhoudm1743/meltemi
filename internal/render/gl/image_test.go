package gl

import (
	"testing"

	gogl "github.com/go-gl/gl/v3.3-core/gl"

	"github.com/zhoudm1743/meltemi/internal/render"
)

func imageTestSource() *render.Image {
	return render.NewImage(1, 1, []byte{255, 0, 0, 255})
}

func readImagePixel(t *testing.T, r *Renderer, x, y float64) [4]uint8 {
	t.Helper()
	fb := r.target.FramebufferSize()
	logical := r.target.Size()
	physicalX := int32(x * float64(fb.Width) / float64(logical.Width))
	physicalY := int32(y * float64(fb.Height) / float64(logical.Height))
	var pixel [4]uint8
	gogl.ReadPixels(physicalX, int32(fb.Height)-1-physicalY, 1, 1, gogl.RGBA, gogl.UNSIGNED_BYTE, gogl.Ptr(&pixel[0]))
	return pixel
}

func TestImageRendererLifecycle(t *testing.T) {
	r, win := newGLRenderer(t)
	source := imageTestSource()
	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	destination := render.Rect{X: 4, Y: 4, Width: 8, Height: 8}
	if err := r.DrawImage(source, destination, 1); err != nil {
		t.Fatalf("DrawImage: %v", err)
	}
	state := imageStates[r]
	if state == nil {
		t.Fatal("DrawImage did not create per-renderer image state")
	}
	texture := state.textures[source]
	if texture == 0 || !gogl.IsTexture(texture) {
		t.Fatalf("cached image texture = %d, want a live GL texture", texture)
	}
	if err := r.DrawImage(source, destination, 1); err != nil {
		t.Fatalf("second DrawImage: %v", err)
	}
	if len(state.textures) != 1 || state.textures[source] != texture {
		t.Errorf("same *render.Image did not reuse its texture: %+v", state.textures)
	}

	second := imageTestSource()
	if err := r.DrawImage(second, destination, 1); err != nil {
		t.Fatalf("DrawImage with equal pixels at another pointer: %v", err)
	}
	secondTexture := state.textures[second]
	if len(state.textures) != 2 || secondTexture == 0 {
		t.Errorf("distinct image pointers did not get distinct cache entries: %+v", state.textures)
	}
	if err := r.End(); err != nil {
		t.Fatalf("End: %v", err)
	}

	if err := win.MakeCurrent(); err != nil {
		t.Fatalf("MakeCurrent before Close: %v", err)
	}
	r.Close()
	if _, ok := imageStates[r]; ok {
		t.Error("imageStates entry survived Close")
	}
	if gogl.IsTexture(texture) || gogl.IsTexture(secondTexture) {
		t.Error("Close left an image texture alive")
	}
	if gogl.IsProgram(state.program) || gogl.IsVertexArray(state.vao) || gogl.IsBuffer(state.vbo) {
		t.Error("Close left image shader/buffer state alive")
	}
	if err := gogl.GetError(); err != gogl.NO_ERROR {
		t.Errorf("gl.GetError after image Close = %v, want NO_ERROR", err)
	}
}

func TestImageSameSourceAcrossRenderers(t *testing.T) {
	first, firstWin, second, secondWin := newGLRendererPair(t)
	source := imageTestSource()
	destination := render.Rect{X: 4, Y: 4, Width: 8, Height: 8}

	if err := first.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	if err := first.DrawImage(source, destination, 1); err != nil {
		t.Fatalf("first DrawImage: %v", err)
	}
	firstState := imageStates[first]
	if firstState == nil || len(firstState.textures) != 1 {
		t.Fatalf("first renderer image cache = %+v, want one source entry", firstState)
	}
	firstTexture := firstState.textures[source]
	if firstTexture == 0 || !gogl.IsTexture(firstTexture) {
		t.Fatalf("first source texture = %d, want a live texture in the first context", firstTexture)
	}
	if err := first.End(); err != nil {
		t.Fatalf("first End: %v", err)
	}

	if err := second.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	if err := second.DrawImage(source, destination, 1); err != nil {
		t.Fatalf("second DrawImage: %v", err)
	}
	secondState := imageStates[second]
	if secondState == nil || len(secondState.textures) != 1 {
		t.Fatalf("second renderer image cache = %+v, want one source entry", secondState)
	}
	if firstState == secondState {
		t.Fatal("two renderers share image state for the same source")
	}
	secondTexture := secondState.textures[source]
	if secondTexture == 0 || !gogl.IsTexture(secondTexture) {
		t.Fatalf("second source texture = %d, want a live texture in the second context", secondTexture)
	}
	if err := second.End(); err != nil {
		t.Fatalf("second End: %v", err)
	}

	if err := firstWin.MakeCurrent(); err != nil {
		t.Fatalf("MakeCurrent before first Close: %v", err)
	}
	first.Close()
	if _, ok := imageStates[first]; ok {
		t.Error("first image cache survived first Close")
	}
	if gogl.IsTexture(firstTexture) {
		t.Error("first source texture survived first Close")
	}
	if err := secondWin.MakeCurrent(); err != nil {
		t.Fatalf("MakeCurrent after first Close: %v", err)
	}
	if got := imageStates[second]; got != secondState || got.textures[source] != secondTexture {
		t.Errorf("second cache after first Close = %+v, want its original source entry", got)
	}
	if !gogl.IsTexture(secondTexture) {
		t.Fatal("first Close invalidated the second context's source texture")
	}

	if err := second.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin after other renderer Close: %v", err)
	}
	if err := second.DrawImage(source, destination, 1); err != nil {
		t.Fatalf("DrawImage after other renderer Close: %v", err)
	}
	gogl.Finish()
	if err := gogl.GetError(); err != gogl.NO_ERROR {
		t.Fatalf("GL error after other renderer Close = %v, want NO_ERROR", err)
	}
	if pixel := readImagePixel(t, second, 8, 8); pixel != [4]uint8{255, 0, 0, 255} {
		t.Errorf("remaining renderer pixel = %v, want opaque red", pixel)
	}
	if err := second.End(); err != nil {
		t.Fatalf("second End after drawing: %v", err)
	}
}

func TestImageDrawOpacityAndClip(t *testing.T) {
	r, _ := newGLRenderer(t)
	source := imageTestSource()
	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := r.DrawImage(source, render.Rect{X: 4, Y: 4, Width: 8, Height: 8}, 0.5); err != nil {
		t.Fatalf("DrawImage with opacity: %v", err)
	}
	var blendSourceAlpha, blendDestinationAlpha int32
	gogl.GetIntegerv(gogl.BLEND_SRC_ALPHA, &blendSourceAlpha)
	gogl.GetIntegerv(gogl.BLEND_DST_ALPHA, &blendDestinationAlpha)
	if blendSourceAlpha != gogl.ONE || blendDestinationAlpha != gogl.ONE_MINUS_SRC_ALPHA {
		t.Errorf("DrawImage left alpha blend factors = %v/%v, want restored frame factors ONE/ONE_MINUS_SRC_ALPHA", blendSourceAlpha, blendDestinationAlpha)
	}
	gogl.Finish()
	if pixel := readImagePixel(t, r, 8, 8); pixel[0] < 127 || pixel[0] > 128 || pixel[1] != 0 || pixel[2] != 0 || pixel[3] != 255 {
		t.Errorf("half-opacity red pixel = %v, want red 127..128, zero green/blue and alpha 255", pixel)
	}
	if err := r.End(); err != nil {
		t.Fatalf("End: %v", err)
	}

	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	r.SetClip(render.Rect{X: 16, Y: 16, Width: 4, Height: 4})
	if err := r.DrawImage(source, render.Rect{X: 16, Y: 16, Width: 8, Height: 8}, 1); err != nil {
		t.Fatalf("clipped DrawImage: %v", err)
	}
	gogl.Finish()
	if pixel := readImagePixel(t, r, 18, 18); pixel != [4]uint8{255, 0, 0, 255} {
		t.Errorf("pixel inside image clip = %v, want opaque red", pixel)
	}
	if pixel := readImagePixel(t, r, 22, 22); pixel != [4]uint8{0, 0, 0, 255} {
		t.Errorf("pixel outside image clip = %v, want opaque black", pixel)
	}
	r.ClearClip()
	if err := r.End(); err != nil {
		t.Fatalf("second End: %v", err)
	}

	translucent := render.NewImage(1, 1, []byte{255, 0, 0, 128})
	if err := r.Begin(render.Color{}); err != nil {
		t.Fatalf("translucent Begin: %v", err)
	}
	if err := r.DrawImage(translucent, render.Rect{X: 32, Y: 32, Width: 8, Height: 8}, 0.5); err != nil {
		t.Fatalf("translucent DrawImage: %v", err)
	}
	gogl.Finish()
	if pixel := readImagePixel(t, r, 36, 36); pixel != [4]uint8{64, 0, 0, 64} {
		t.Errorf("translucent source pixel = %v, want [64 0 0 64] with source alpha applied once", pixel)
	}
	if err := r.End(); err != nil {
		t.Fatalf("translucent End: %v", err)
	}
}

func TestImageRendererEmptyDraws(t *testing.T) {
	r := &Renderer{}
	source := imageTestSource()
	calls := []struct {
		source      *render.Image
		destination render.Rect
		opacity     float64
	}{
		{source: nil, destination: render.Rect{Width: 10, Height: 10}, opacity: 1},
		{source: source, destination: render.Rect{Width: 0, Height: 10}, opacity: 1},
		{source: source, destination: render.Rect{Width: 10, Height: -1}, opacity: 1},
		{source: source, destination: render.Rect{Width: 10, Height: 10}, opacity: 0},
	}
	for _, call := range calls {
		if err := r.DrawImage(call.source, call.destination, call.opacity); err != nil {
			t.Errorf("empty DrawImage(%+v) error = %v", call, err)
		}
	}
	if _, ok := imageStates[r]; ok {
		t.Error("empty DrawImage created GL state without a context")
	}
}

func TestImageDiscardWithoutContext(t *testing.T) {
	r := &Renderer{}
	imageStates[r] = &imageState{textures: map[*render.Image]uint32{imageTestSource(): 7}}
	r.Discard()
	if _, ok := imageStates[r]; ok {
		t.Error("imageStates entry survived Discard without a current context")
	}
}
