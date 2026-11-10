package gl

import (
	"fmt"
	"runtime"
	"testing"

	gogl "github.com/go-gl/gl/v3.3-core/gl"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/driver/glfw"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// newGLBenchmarkBackend mirrors newGLTestBackend for benchmarks: the calling
// goroutine is locked to its OS thread before GLFW initializes, and the
// backend terminates on cleanup.
func newGLBenchmarkBackend(b *testing.B) *glfw.Backend {
	b.Helper()
	runtime.LockOSThread()
	b.Cleanup(runtime.UnlockOSThread)
	backend := glfw.New()
	if err := backend.Init(); err != nil {
		b.Skipf("glfw init failed: %v", err)
	}
	b.Cleanup(backend.Terminate)
	return backend
}

// newGLBenchmarkRenderer opens a shown 256x256 window and a Renderer for it,
// warmed up with one presented frame like the test helper. End/Present are
// deliberately left out of the measured loop: the swap would throttle the
// benchmark to the display's vertical sync and hide the text-path cost these
// benchmarks exist to measure.
func newGLBenchmarkRenderer(b *testing.B, backend *glfw.Backend) (*Renderer, *glfw.Window) {
	b.Helper()
	drvWin, err := backend.NewWindow(driver.Config{Title: "gl-text-bench", Size: driver.Size{Width: 256, Height: 256}})
	if err != nil {
		b.Skipf("window creation failed: %v", err)
	}
	win := drvWin.(*glfw.Window)
	win.Show()

	r, err := New(win)
	if err != nil {
		b.Fatalf("renderer creation failed with a live context: %v", err)
	}
	b.Cleanup(func() {
		if err := win.MakeCurrent(); err != nil {
			b.Errorf("MakeCurrent in cleanup: %v", err)
		}
		r.Close()
		win.Destroy()
		backend.PollEvents()
	})
	if err := r.Begin(render.Color{A: 1}); err != nil {
		b.Fatalf("initial Begin: %v", err)
	}
	if err := r.End(); err != nil {
		b.Fatalf("initial End: %v", err)
	}
	gogl.Finish()
	return r, win
}

// reportTextCacheMetrics exposes the cache counters as benchmark metrics so
// the cold/warm difference is attributable to rasterize+upload vs cache hits.
func reportTextCacheMetrics(b *testing.B, r *Renderer) {
	b.Helper()
	cache := textCaches[r]
	if cache == nil {
		b.ReportMetric(0, "hits/op")
		b.ReportMetric(0, "uploads/op")
		return
	}
	b.ReportMetric(float64(cache.hits)/float64(b.N), "hits/op")
	b.ReportMetric(float64(cache.uploads)/float64(b.N), "uploads/op")
}

// BenchmarkTextCacheCold measures the uncached path: every iteration draws a
// distinct coverage key (a 512-string pool, larger than the 256-texture cap,
// so steady state also includes eviction and upload). Expected hits/op: 0,
// uploads/op: 1.
func BenchmarkTextCacheCold(b *testing.B) {
	backend := newGLBenchmarkBackend(b)
	r, _ := newGLBenchmarkRenderer(b, backend)

	face, err := text.NewFace(goregular.TTF, 24)
	if err != nil {
		b.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()

	pool := make([]string, 512)
	for i := range pool {
		pool[i] = fmt.Sprintf("label-%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.Begin(render.Color{A: 1}); err != nil {
			b.Fatalf("Begin: %v", err)
		}
		if err := r.DrawText(pool[i%len(pool)], driver.Point{X: 8, Y: 8}, render.Color{A: 1}, face); err != nil {
			b.Fatalf("DrawText: %v", err)
		}
	}
	b.StopTimer()
	reportTextCacheMetrics(b, r)
}

// BenchmarkTextCacheWarm measures the cached path: every iteration draws the
// same coverage key, so after the first call the texture comes from the LRU
// cache and no rasterization or upload runs. Expected hits/op: ~1,
// uploads/op: ~0.
func BenchmarkTextCacheWarm(b *testing.B) {
	backend := newGLBenchmarkBackend(b)
	r, _ := newGLBenchmarkRenderer(b, backend)

	face, err := text.NewFace(goregular.TTF, 24)
	if err != nil {
		b.Fatalf("text.NewFace: %v", err)
	}
	defer face.Close()

	const value = "The quick brown fox"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.Begin(render.Color{A: 1}); err != nil {
			b.Fatalf("Begin: %v", err)
		}
		if err := r.DrawText(value, driver.Point{X: 8, Y: 8}, render.Color{A: 1}, face); err != nil {
			b.Fatalf("DrawText: %v", err)
		}
	}
	b.StopTimer()
	reportTextCacheMetrics(b, r)
}
