package windowframe

import (
	"testing"
	"time"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

func TestDefaultStyle(t *testing.T) {
	s := DefaultStyle()
	if s.Height != 36 {
		t.Errorf("Height = %v, want 36", s.Height)
	}
	if s.ButtonWidth != 46 {
		t.Errorf("ButtonWidth = %v, want 46", s.ButtonWidth)
	}
	if s.ResizeMargin != 5 {
		t.Errorf("ResizeMargin = %v, want 5", s.ResizeMargin)
	}
	if s.MinSize != (driver.Size{Width: 240, Height: 120}) {
		t.Errorf("MinSize = %v, want 240x120", s.MinSize)
	}
	colors := []struct {
		name string
		c    render.Color
	}{
		{"Background", s.Background},
		{"TitleColor", s.TitleColor},
		{"IconColor", s.IconColor},
		{"ButtonColor", s.ButtonColor},
		{"ButtonHover", s.ButtonHover},
		{"ButtonActive", s.ButtonActive},
		{"CloseColor", s.CloseColor},
		{"CloseHover", s.CloseHover},
		{"CloseActive", s.CloseActive},
	}
	for _, col := range colors {
		if col.c == (render.Color{}) || col.c.A != 1 {
			t.Errorf("%s not fully specified: %+v", col.name, col.c)
		}
	}
	if s.ButtonHover == s.ButtonColor || s.ButtonActive == s.ButtonColor {
		t.Error("button hover/active colors must differ from idle color")
	}
	if s.CloseHover == s.CloseColor || s.CloseActive == s.CloseColor {
		t.Error("close hover/active colors must differ from idle color")
	}
	if s.CloseHover == s.ButtonHover {
		t.Error("close hover should be distinct from other buttons' hover")
	}
}

func TestLayoutGeometry(t *testing.T) {
	s := DefaultStyle()
	for _, size := range []driver.Size{{Width: 400, Height: 300}, {Width: 240, Height: 120}} {
		l := s.Layout(size)
		wantBar := render.Rect{X: 0, Y: 0, Width: float64(size.Width), Height: s.Height}
		if l.TitleBar != wantBar {
			t.Errorf("size %v: TitleBar = %v, want %v", size, l.TitleBar, wantBar)
		}
		for name, b := range map[string]render.Rect{
			"Minimize": l.Minimize,
			"Maximize": l.Maximize,
			"Close":    l.Close,
		} {
			if b.Width != s.ButtonWidth || b.Height != s.Height || b.Y != 0 {
				t.Errorf("size %v: %s = %v, want width %v height %v at y=0", size, name, b, s.ButtonWidth, s.Height)
			}
			if b.X < 0 || b.X+b.Width > float64(size.Width) {
				t.Errorf("size %v: %s escapes the window: %v", size, name, b)
			}
		}
		if l.Minimize.X >= l.Maximize.X || l.Maximize.X >= l.Close.X {
			t.Errorf("size %v: buttons out of order: min %v max %v close %v", size, l.Minimize.X, l.Maximize.X, l.Close.X)
		}
		if l.Minimize.X+l.Minimize.Width > l.Maximize.X || l.Maximize.X+l.Maximize.Width > l.Close.X {
			t.Errorf("size %v: buttons overlap: %v %v %v", size, l.Minimize, l.Maximize, l.Close)
		}
		if l.Close.X+l.Close.Width != float64(size.Width) {
			t.Errorf("size %v: close button not flush with the right edge: %v", size, l.Close)
		}
		wantIcon := render.Rect{X: 8, Y: (s.Height - 16) / 2, Width: 16, Height: 16}
		if l.Icon != wantIcon {
			t.Errorf("size %v: Icon = %v, want %v", size, l.Icon, wantIcon)
		}
		if l.Icon.Y < 0 || l.Icon.Y+l.Icon.Height > l.TitleBar.Height {
			t.Errorf("size %v: icon escapes the title bar: %v", size, l.Icon)
		}
		if l.Icon.X+l.Icon.Width >= l.Minimize.X {
			t.Errorf("size %v: icon enters the button area: %v", size, l.Icon)
		}
		wantTitleX := l.Icon.X + l.Icon.Width + 8
		if l.Title.X != wantTitleX {
			t.Errorf("size %v: Title.X = %v, want %v (right of the icon)", size, l.Title.X, wantTitleX)
		}
		if l.Title.X+l.Title.Width > l.Minimize.X {
			t.Errorf("size %v: title enters the button area: %v", size, l.Title)
		}
		if l.Title.Width <= 0 {
			t.Errorf("size %v: title has no room: %v", size, l.Title)
		}
	}
}

func TestButtonHitPriority(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	l := s.Layout(size)

	for _, tc := range []struct {
		p    driver.Point
		want Button
	}{
		{mid(l.Minimize), ButtonMinimize},
		{mid(l.Maximize), ButtonMaximize},
		{mid(l.Close), ButtonClose},
		{p: driver.Point{X: l.Title.X + 2, Y: l.TitleBar.Height / 2}, want: ButtonNone},
	} {
		if got := l.ButtonAt(tc.p); got != tc.want {
			t.Errorf("ButtonAt(%v) = %v, want %v", tc.p, got, tc.want)
		}
	}

	// The top-right corner is inside both the resize margins and the close
	// button: the button must win over the Top/Right resize edges.
	corner := driver.Point{X: float64(size.Width) - 2, Y: 2}
	if edges := s.ResizeEdges(size, corner); edges != EdgeTop|EdgeRight {
		t.Fatalf("ResizeEdges(%v) = %v, want EdgeTop|EdgeRight", corner, edges)
	}
	if got := l.ButtonAt(corner); got != ButtonClose {
		t.Fatalf("ButtonAt(%v) = %v, want ButtonClose (button wins over edges)", corner, got)
	}

	w := &fakeFrameWindow{size: size, pos: driver.Point{X: 100, Y: 50}}
	c := NewController(s)
	now := time.Now()
	r := c.HandleEvent(pressAt(corner), w, add(w.pos, corner), now)
	if !r.Redraw || c.Active() != ButtonClose {
		t.Fatalf("press in corner: result %v active %v, want redraw with close active", r, c.Active())
	}
	// Moving away and releasing must cancel the button, not resize the window.
	far := driver.Point{X: 200, Y: 150}
	c.HandleEvent(moveAt(far), w, add(w.pos, far), now.Add(10*time.Millisecond))
	r = c.HandleEvent(releaseAt(far), w, add(w.pos, far), now.Add(20*time.Millisecond))
	if r.Close {
		t.Fatal("release outside the button must not close the window")
	}
	if got := w.Size(); got != size {
		t.Fatalf("window size changed to %v, want %v (corner press is not a resize)", got, size)
	}
}

func TestResizeEdgesAndCorners(t *testing.T) {
	s := DefaultStyle()
	size := driver.Size{Width: 400, Height: 300}
	for _, tc := range []struct {
		p    driver.Point
		want Edges
	}{
		{p: driver.Point{X: 2, Y: 150}, want: EdgeLeft},
		{p: driver.Point{X: 397, Y: 150}, want: EdgeRight},
		{p: driver.Point{X: 200, Y: 2}, want: EdgeTop},
		{p: driver.Point{X: 200, Y: 297}, want: EdgeBottom},
		{p: driver.Point{X: 2, Y: 2}, want: EdgeLeft | EdgeTop},
		{p: driver.Point{X: 397, Y: 2}, want: EdgeRight | EdgeTop},
		{p: driver.Point{X: 2, Y: 297}, want: EdgeLeft | EdgeBottom},
		{p: driver.Point{X: 397, Y: 297}, want: EdgeRight | EdgeBottom},
		{p: driver.Point{X: 200, Y: 150}, want: 0},
		{p: driver.Point{X: 5, Y: 150}, want: 0},
		{p: driver.Point{X: 395, Y: 150}, want: EdgeRight},
		{p: driver.Point{X: 4, Y: 150}, want: EdgeLeft},
	} {
		if got := s.ResizeEdges(size, tc.p); got != tc.want {
			t.Errorf("ResizeEdges(%v) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

func mid(r render.Rect) driver.Point {
	return driver.Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}
}
