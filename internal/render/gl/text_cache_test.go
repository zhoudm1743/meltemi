package gl

import (
	"fmt"
	"testing"

	gogl "github.com/go-gl/gl/v3.3-core/gl"
	glfw3 "github.com/go-gl/glfw/v3.3/glfw"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// solidPixel returns the logical position of one fully covered pixel of value
// rasterized at scale 1, so tests can read back a pixel that must equal the
// text color exactly.
func solidPixel(t *testing.T, face *text.Face, value string) driver.Point {
	t.Helper()
	bitmap, err := face.Rasterize(value)
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	for y := 0; y < bitmap.Height; y++ {
		for x := 0; x < bitmap.Width; x++ {
			if bitmap.Alpha[y*bitmap.Stride+x] == 255 {
				return driver.Point{X: float64(x), Y: float64(y)}
			}
		}
	}
	t.Fatalf("text %q has no fully covered pixel", value)
	return driver.Point{}
}

// TestTextCacheReusesCoverageNotColor pins that the coverage texture is keyed
// without color: drawing the same text again with a different color is a cache
// hit (one rasterization, one upload) while the framebuffer shows the new
// color, proving the color uniform is applied per frame on the shared
// coverage texture.
func TestTextCacheReusesCoverageNotColor(t *testing.T) {
	r, _ := newGLRenderer(t)

	face, err := text.NewFace(goregular.TTF, 24)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()

	sample := solidPixel(t, face, "I")

	// Frame 1: red text on an opaque black clear.
	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin frame 1: %v", err)
	}
	if err := r.DrawText("I", driver.Point{}, render.Color{R: 1, A: 1}, face); err != nil {
		t.Fatalf("DrawText frame 1: %v", err)
	}
	gogl.Finish()
	if got := readImagePixel(t, r, sample.X, sample.Y); got != [4]uint8{255, 0, 0, 255} {
		t.Fatalf("frame 1 solid pixel = %v, want opaque red (255,0,0,255)", got)
	}
	if err := r.End(); err != nil {
		t.Fatalf("End frame 1: %v", err)
	}

	// Frame 2: same coverage, green color. A color-keyed cache would upload a
	// second texture; the coverage cache must hit and tint the same texture.
	if err := r.Begin(render.Color{A: 1}); err != nil {
		t.Fatalf("Begin frame 2: %v", err)
	}
	if err := r.DrawText("I", driver.Point{}, render.Color{G: 1, A: 1}, face); err != nil {
		t.Fatalf("DrawText frame 2: %v", err)
	}
	gogl.Finish()
	if got := readImagePixel(t, r, sample.X, sample.Y); got != [4]uint8{0, 255, 0, 255} {
		t.Fatalf("frame 2 solid pixel = %v, want opaque green (0,255,0,255) with reused coverage", got)
	}
	if err := r.End(); err != nil {
		t.Fatalf("End frame 2: %v", err)
	}

	cache := textCaches[r]
	if cache == nil {
		t.Fatal("DrawText did not create a text cache")
	}
	if cache.uploads != 1 {
		t.Errorf("uploads = %d, want 1 (same coverage drawn twice)", cache.uploads)
	}
	if cache.hits != 1 {
		t.Errorf("hits = %d, want 1 (second draw is a cache hit)", cache.hits)
	}
	if cache.misses != 1 {
		t.Errorf("misses = %d, want 1", cache.misses)
	}
	if len(cache.entries) != 1 {
		t.Errorf("cache entries = %d, want 1", len(cache.entries))
	}
}

// TestTextCacheIsolationAndEviction pins that caches are per renderer and
// bounded: two renderers never consult each other's cache, closing one
// renderer does not delete the other's texture, and inserting past the 256
// entry cap evicts least-recently-used entries deterministically, deleting
// their GL textures with the owning context current.
func TestTextCacheIsolationAndEviction(t *testing.T) {
	face, err := text.NewFace(goregular.TTF, 16)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()

	t.Run("isolation", func(t *testing.T) {
		first, firstWin, second, secondWin := newGLRendererPair(t)

		// The same text drawn on both renderers uploads once per renderer:
		// texture names are never taken from another renderer's cache.
		for i, r := range []*Renderer{first, second} {
			if err := r.Begin(render.Color{A: 1}); err != nil {
				t.Fatalf("Begin renderer %d: %v", i, err)
			}
			if err := r.DrawText("Wind", driver.Point{}, render.Color{A: 1}, face); err != nil {
				t.Fatalf("DrawText renderer %d: %v", i, err)
			}
			if err := r.End(); err != nil {
				t.Fatalf("End renderer %d: %v", i, err)
			}
		}
		if got := textCaches[first].uploads; got != 1 {
			t.Errorf("first renderer uploads = %d, want 1", got)
		}
		if got := textCaches[second].uploads; got != 1 {
			t.Errorf("second renderer uploads = %d, want 1", got)
		}

		// A second draw on the second renderer is a hit there and must not
		// touch the first renderer's cache.
		if err := second.Begin(render.Color{A: 1}); err != nil {
			t.Fatalf("Begin second: %v", err)
		}
		if err := second.DrawText("Wind", driver.Point{}, render.Color{A: 1}, face); err != nil {
			t.Fatalf("DrawText second: %v", err)
		}
		if err := second.End(); err != nil {
			t.Fatalf("End second: %v", err)
		}
		if got := textCaches[second].hits; got != 1 {
			t.Errorf("second renderer hits = %d, want 1", got)
		}
		if got := textCaches[first].hits; got != 0 {
			t.Errorf("first renderer hits = %d, want 0 (no cross-renderer reuse)", got)
		}

		// Closing the first renderer frees its own texture and cache entry
		// only; the second renderer's cached texture survives.
		secondTexture := textCaches[second].entries[textCacheKey{face: face, value: "Wind", scaleX: 1, scaleY: 1}].tex
		if err := firstWin.MakeCurrent(); err != nil {
			t.Fatalf("MakeCurrent first: %v", err)
		}
		first.Close()
		if _, ok := textCaches[first]; ok {
			t.Error("first renderer cache entry survived Close")
		}
		if err := secondWin.MakeCurrent(); err != nil {
			t.Fatalf("MakeCurrent second: %v", err)
		}
		if !gogl.IsTexture(secondTexture) {
			t.Error("second renderer's cached texture deleted by first renderer's Close")
		}
	})

	t.Run("eviction", func(t *testing.T) {
		r, _ := newGLRenderer(t)

		const total = 260 // 4 past the 256-texture cap
		key := func(i int) textCacheKey {
			return textCacheKey{face: face, value: fmt.Sprintf("v%03d", i), scaleX: 1, scaleY: 1}
		}

		if err := r.Begin(render.Color{A: 1}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		for i := 0; i < total; i++ {
			if err := r.DrawText(fmt.Sprintf("v%03d", i), driver.Point{}, render.Color{A: 1}, face); err != nil {
				t.Fatalf("DrawText %d: %v", i, err)
			}
		}
		if err := r.End(); err != nil {
			t.Fatalf("End: %v", err)
		}
		if err := gogl.GetError(); err != gogl.NO_ERROR {
			t.Fatalf("gl.GetError after %d draws = %v, want NO_ERROR", total, err)
		}

		cache := textCaches[r]
		if got := len(cache.entries); got != 256 {
			t.Errorf("cache entries = %d, want 256 (bounded)", got)
		}
		if got := cache.evictions; got != total-256 {
			t.Errorf("evictions = %d, want %d", got, total-256)
		}
		if got := cache.uploads; got != total {
			t.Errorf("uploads = %d, want %d", got, total)
		}
		if got := cache.hits; got != 0 {
			t.Errorf("hits = %d, want 0 (every draw is a distinct coverage)", got)
		}

		// LRU order: the four oldest entries are gone, the survivors' textures
		// are all live, and the newest entry is present. (The evicted GL names
		// are not checked: GenTextures may immediately reuse a name freed by
		// the same eviction cycle, so IsTexture on an evicted name is not
		// deterministic. The eviction counter plus NO_ERROR pins that the
		// deletes ran as valid GL calls.)
		for i := 0; i < total-256; i++ {
			if _, ok := cache.entries[key(i)]; ok {
				t.Errorf("entry %d survived; LRU order violated", i)
			}
		}
		for k, entry := range cache.entries {
			if !gogl.IsTexture(entry.tex) {
				t.Errorf("survivor %q texture %d not live", k.value, entry.tex)
			}
		}
		if _, ok := cache.entries[key(total-1)]; !ok {
			t.Error("newest entry evicted; LRU order violated")
		}
	})
}

// TestTextCacheCloseAndDiscard pins the two teardown paths: Close deletes every
// cached texture with the owning context current and drops the cache entry;
// Discard only drops Go references and must make no GL calls, so it stays safe
// after the context is detached.
func TestTextCacheCloseAndDiscard(t *testing.T) {
	t.Run("close deletes textures", func(t *testing.T) {
		r, _ := newGLRenderer(t)

		face, err := text.NewFace(goregular.TTF, 16)
		if err != nil {
			t.Fatalf("text.NewFace: %v", err)
		}
		defer face.Close()
		if err := r.Begin(render.Color{A: 1}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := r.DrawText("Wind", driver.Point{}, render.Color{A: 1}, face); err != nil {
			t.Fatalf("DrawText: %v", err)
		}
		if err := r.End(); err != nil {
			t.Fatalf("End: %v", err)
		}

		texture := textCaches[r].entries[textCacheKey{face: face, value: "Wind", scaleX: 1, scaleY: 1}].tex
		r.Close()
		r.Close() // idempotent

		if _, ok := textCaches[r]; ok {
			t.Error("text cache entry survived Close")
		}
		if gogl.IsTexture(texture) {
			t.Error("cached texture not deleted by Close")
		}
		if err := gogl.GetError(); err != gogl.NO_ERROR {
			t.Errorf("gl.GetError after Close = %v, want NO_ERROR", err)
		}
	})

	t.Run("discard drops references only", func(t *testing.T) {
		r, win := newGLRenderer(t)

		face, err := text.NewFace(goregular.TTF, 16)
		if err != nil {
			t.Fatalf("text.NewFace: %v", err)
		}
		defer face.Close()
		if err := r.Begin(render.Color{A: 1}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := r.DrawText("Wind", driver.Point{}, render.Color{A: 1}, face); err != nil {
			t.Fatalf("DrawText: %v", err)
		}
		if err := r.End(); err != nil {
			t.Fatalf("End: %v", err)
		}
		if _, ok := textCaches[r]; !ok {
			t.Fatal("DrawText did not create a text cache")
		}

		glfw3.DetachCurrentContext()
		r.Discard()

		if _, ok := textCaches[r]; ok {
			t.Error("text cache entry survived Discard")
		}
		if _, ok := textStates[r]; ok {
			t.Error("textStates entry survived Discard")
		}
		if err := win.MakeCurrent(); err != nil {
			t.Fatalf("MakeCurrent after Discard: %v", err)
		}
	})
}

// TestTextCacheScaleAndFaceInvalidation pins the cache key: the physical
// scale is part of it, so a resize re-rasterizes instead of reusing a stale
// texture, and a replaced (previously closed) face never reads the old face's
// coverage. Drawing with a closed face fails and caches nothing.
func TestTextCacheScaleAndFaceInvalidation(t *testing.T) {
	t.Run("scale", func(t *testing.T) {
		backend := newGLTestBackend(t)
		logical := driver.Size{Width: 32, Height: 32}
		r, target := newGLRendererScaled(t, backend, logical, driver.Size{Width: 64, Height: 64})

		face, err := text.NewFace(goregular.TTF, 16)
		if err != nil {
			t.Fatalf("text.NewFace: %v", err)
		}
		defer face.Close()

		const value = "Wj"
		at := driver.Point{X: 4, Y: 3}

		oracle2, err := face.RasterizeScaled(value, 2, 2)
		if err != nil {
			t.Fatalf("RasterizeScaled 2x: %v", err)
		}
		drawAndCheck(t, r, target, face, value, at, oracle2, 2)

		// Resize to 1x: the cached 2x coverage must not be reused.
		target.fb = driver.Size{Width: 32, Height: 32}
		oracle1, err := face.RasterizeScaled(value, 1, 1)
		if err != nil {
			t.Fatalf("RasterizeScaled 1x: %v", err)
		}
		drawAndCheck(t, r, target, face, value, at, oracle1, 1)

		cache := textCaches[r]
		if got := cache.uploads; got != 2 {
			t.Errorf("uploads = %d, want 2 (one per physical scale)", got)
		}
		if got := len(cache.entries); got != 2 {
			t.Errorf("cache entries = %d, want 2 (2x and 1x keys)", got)
		}
	})

	t.Run("face replacement", func(t *testing.T) {
		backend := newGLTestBackend(t)
		r, target := newGLRendererScaled(t, backend,
			driver.Size{Width: 64, Height: 64}, driver.Size{Width: 64, Height: 64})

		firstFace, err := text.NewFace(goregular.TTF, 16)
		if err != nil {
			t.Fatalf("text.NewFace regular: %v", err)
		}
		const value = "Wind"
		at := driver.Point{X: 4, Y: 20}
		if err := r.Begin(render.Color{A: 1}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := r.DrawText(value, at, render.Color{G: 1, A: 1}, firstFace); err != nil {
			t.Fatalf("DrawText first face: %v", err)
		}
		if err := r.End(); err != nil {
			t.Fatalf("End: %v", err)
		}

		// Replace the face after closing it, then demand the new face's
		// coverage pixel by pixel: a stale regular-face texture would fail
		// the oracle comparison.
		if err := firstFace.Close(); err != nil {
			t.Fatalf("first face Close: %v", err)
		}
		secondFace, err := text.NewFace(gobold.TTF, 16)
		if err != nil {
			t.Fatalf("text.NewFace bold: %v", err)
		}
		defer secondFace.Close()

		oracle, err := secondFace.RasterizeScaled(value, 1, 1)
		if err != nil {
			t.Fatalf("RasterizeScaled bold: %v", err)
		}
		drawAndCheck(t, r, target, secondFace, value, at, oracle, 1)

		cache := textCaches[r]
		if got := len(cache.entries); got != 2 {
			t.Errorf("cache entries = %d, want 2 (one per face)", got)
		}
		if got := cache.uploads; got != 2 {
			t.Errorf("uploads = %d, want 2 (replacement face re-rasterizes)", got)
		}
	})

	t.Run("closed face draws error and caches nothing", func(t *testing.T) {
		r, _ := newGLRenderer(t)

		face, err := text.NewFace(goregular.TTF, 16)
		if err != nil {
			t.Fatalf("text.NewFace: %v", err)
		}
		if err := face.Close(); err != nil {
			t.Fatalf("face Close: %v", err)
		}

		if err := r.Begin(render.Color{A: 1}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		err = r.DrawText("Wind", driver.Point{}, render.Color{A: 1}, face)
		if err == nil {
			t.Fatal("DrawText with a closed face succeeded, want error")
		}
		if err := r.End(); err != nil {
			t.Fatalf("End: %v", err)
		}

		cache := textCaches[r]
		if cache == nil {
			t.Fatal("no text cache recorded the failed draw")
		}
		if got := len(cache.entries); got != 0 {
			t.Errorf("cache entries = %d, want 0 (errors are not cached)", got)
		}
		if got := cache.uploads; got != 0 {
			t.Errorf("uploads = %d, want 0 (no texture for a failed draw)", got)
		}
	})
}
