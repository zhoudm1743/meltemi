package gl

import (
	"testing"

	"github.com/zhoudm1743/meltemi/internal/render"
)

func TestRoundedRectGeometry(t *testing.T) {
	bounds := render.Rect{X: 10, Y: 20, Width: 40, Height: 20}
	for _, tc := range []struct {
		name   string
		radius float64
		want   float64
	}{
		{name: "negative clamps to zero", radius: -3, want: 0},
		{name: "zero", radius: 0, want: 0},
		{name: "ordinary", radius: 4, want: 4},
		{name: "large clamps to half the short side", radius: 100, want: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampRoundedRadius(bounds, tc.radius); got != tc.want {
				t.Fatalf("clampRoundedRadius(%v) = %v, want %v", tc.radius, got, tc.want)
			}
		})
	}

	color := render.Color{R: 0.1, G: 0.2, B: 0.3, A: 0.4}
	if vertices := roundedRectVertices(bounds, color, 0); vertices != nil {
		t.Fatalf("zero-radius mesh has %d floats, want nil so DrawRoundedRect uses DrawRect", len(vertices))
	}

	vertices := roundedRectVertices(bounds, color, 100)
	wantVertices := 4 * (roundedCornerSegments + 1) * 3
	if got := len(vertices) / 6; got != wantVertices {
		t.Fatalf("mesh vertex count = %d, want %d", got, wantVertices)
	}
	for vertex := 0; vertex < len(vertices); vertex += 6 {
		x, y := float64(vertices[vertex]), float64(vertices[vertex+1])
		if x < bounds.X || x > bounds.X+bounds.Width || y < bounds.Y || y > bounds.Y+bounds.Height {
			t.Errorf("vertex %d at (%v,%v) lies outside %+v", vertex/6, x, y, bounds)
		}
		gotColor := render.Color{R: vertices[vertex+2], G: vertices[vertex+3], B: vertices[vertex+4], A: vertices[vertex+5]}
		if gotColor != color {
			t.Errorf("vertex %d color = %+v, want %+v", vertex/6, gotColor, color)
		}
	}

	wantStart := []float32{30, 30, 40, 20}
	positions := []int{0, 1, 6, 7}
	for index, want := range wantStart {
		position := positions[index]
		if vertices[position] != want {
			t.Errorf("triangle start coordinate %d = %v, want %v", position, vertices[position], want)
		}
	}
}

func TestRoundedBorderRingGeometry(t *testing.T) {
	bounds := render.Rect{X: 0, Y: 0, Width: 20, Height: 10}
	color := render.Color{R: 0.2, G: 0.4, B: 0.6, A: 0.5}
	if vertices := roundedBorderVertices(bounds, color, 0, 0); vertices != nil {
		t.Fatalf("zero-width border has %d floats, want nil", len(vertices))
	}
	for _, radius := range []float64{0, 4} {
		vertices := roundedBorderVertices(bounds, color, radius, 2)
		if len(vertices) == 0 || len(vertices)%18 != 0 {
			t.Fatalf("radius %v border mesh length = %d, want complete triangles", radius, len(vertices))
		}
		for vertex := 0; vertex < len(vertices); vertex += 6 {
			x, y := float64(vertices[vertex]), float64(vertices[vertex+1])
			if x < 0 || x > 20 || y < 0 || y > 10 {
				t.Errorf("radius %v vertex %d outside border bounds: (%v,%v)", radius, vertex/6, x, y)
			}
			if x == 10 && y == 5 {
				t.Errorf("radius %v border mesh fills the center instead of forming a ring", radius)
			}
			if got := (render.Color{R: vertices[vertex+2], G: vertices[vertex+3], B: vertices[vertex+4], A: vertices[vertex+5]}); got != color {
				t.Errorf("radius %v vertex %d color = %+v, want %+v", radius, vertex/6, got, color)
			}
		}
	}
}
