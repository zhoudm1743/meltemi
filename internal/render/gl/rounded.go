package gl

import (
	"math"

	gogl "github.com/go-gl/gl/v3.3-core/gl"

	"github.com/zhoudm1743/meltemi/internal/render"
)

const roundedCornerSegments = 8

// DrawRoundedRect draws a filled rounded rectangle in logical coordinates.
// The existing rectangle program and buffers keep rectangle and text drawing
// isolated from one another; Begin has already made the target context current.
func (r *Renderer) DrawRoundedRect(rect render.Rect, color render.Color, radius float64) {
	if r.skip {
		return
	}
	vertices := roundedRectVertices(rect, color, radius)
	if vertices == nil {
		r.DrawRect(rect, color)
		return
	}
	r.drawVertices(vertices)
}

// DrawRoundedBorder draws only the ring between the outer and inner contours.
// It never fills the background, so border and background alpha compose independently.
func (r *Renderer) DrawRoundedBorder(rect render.Rect, color render.Color, radius, width float64) {
	if r.skip {
		return
	}
	r.drawVertices(roundedBorderVertices(rect, color, radius, width))
}

func (r *Renderer) drawVertices(vertices []float32) {
	if len(vertices) == 0 {
		return
	}
	gogl.UseProgram(r.program)
	gogl.BindVertexArray(r.vao)
	gogl.BindBuffer(gogl.ARRAY_BUFFER, r.vbo)
	gogl.BufferData(gogl.ARRAY_BUFFER, len(vertices)*4, gogl.Ptr(vertices), gogl.DYNAMIC_DRAW)
	gogl.DrawArrays(gogl.TRIANGLES, 0, int32(len(vertices)/6))
}

func clampRoundedRadius(rect render.Rect, radius float64) float64 {
	limit := min(rect.Width, rect.Height) / 2
	if math.IsNaN(radius) || math.IsNaN(limit) || limit <= 0 || radius <= 0 {
		return 0
	}
	return min(radius, limit)
}

func clampRoundedBorderWidth(rect render.Rect, width float64) float64 {
	limit := min(rect.Width, rect.Height) / 2
	if math.IsNaN(width) || math.IsNaN(limit) || limit <= 0 || width <= 0 {
		return 0
	}
	return min(width, limit)
}

func roundedContour(rect render.Rect, radius float64, segmented bool) [][2]float32 {
	radius = clampRoundedRadius(rect, radius)
	x0, y0 := rect.X, rect.Y
	x1, y1 := rect.X+rect.Width, rect.Y+rect.Height
	if radius == 0 && !segmented {
		return [][2]float32{{float32(x1), float32(y0)}, {float32(x1), float32(y1)}, {float32(x0), float32(y1)}, {float32(x0), float32(y0)}}
	}
	corners := []struct {
		x, y, startAngle float64
	}{
		{x: x1 - radius, y: y0 + radius, startAngle: -math.Pi / 2},
		{x: x1 - radius, y: y1 - radius, startAngle: 0},
		{x: x0 + radius, y: y1 - radius, startAngle: math.Pi / 2},
		{x: x0 + radius, y: y0 + radius, startAngle: math.Pi},
	}
	outline := make([][2]float32, 0, 4*(roundedCornerSegments+1))
	for _, corner := range corners {
		for segment := 0; segment <= roundedCornerSegments; segment++ {
			angle := corner.startAngle + float64(segment)*(math.Pi/2)/roundedCornerSegments
			outline = append(outline, [2]float32{
				float32(corner.x + radius*math.Cos(angle)),
				float32(corner.y + radius*math.Sin(angle)),
			})
		}
	}
	return outline
}

func roundedRectVertices(rect render.Rect, color render.Color, radius float64) []float32 {
	radius = clampRoundedRadius(rect, radius)
	if radius == 0 {
		return nil
	}
	outline := roundedContour(rect, radius, true)
	center := [2]float32{float32(rect.X + rect.Width/2), float32(rect.Y + rect.Height/2)}
	vertices := make([]float32, 0, len(outline)*3*6)
	appendVertex := func(point [2]float32) {
		vertices = append(vertices, point[0], point[1], color.R, color.G, color.B, color.A)
	}
	for index, point := range outline {
		next := outline[(index+1)%len(outline)]
		appendVertex(center)
		appendVertex(point)
		appendVertex(next)
	}
	return vertices
}

func roundedBorderVertices(rect render.Rect, color render.Color, radius, width float64) []float32 {
	width = clampRoundedBorderWidth(rect, width)
	if width == 0 {
		return nil
	}
	radius = clampRoundedRadius(rect, radius)
	innerRect := render.Rect{
		X:      rect.X + width,
		Y:      rect.Y + width,
		Width:  max(0, rect.Width-2*width),
		Height: max(0, rect.Height-2*width),
	}
	segmented := radius > 0
	outer := roundedContour(rect, radius, segmented)
	inner := roundedContour(innerRect, max(0, radius-width), segmented)
	vertices := make([]float32, 0, len(outer)*2*3*6)
	appendVertex := func(point [2]float32) {
		vertices = append(vertices, point[0], point[1], color.R, color.G, color.B, color.A)
	}
	for index := range outer {
		next := (index + 1) % len(outer)
		appendVertex(outer[index])
		appendVertex(outer[next])
		appendVertex(inner[next])
		appendVertex(outer[index])
		appendVertex(inner[next])
		appendVertex(inner[index])
	}
	return vertices
}
