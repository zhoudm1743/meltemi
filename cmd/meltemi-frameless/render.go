package main

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
	"github.com/zhoudm1743/meltemi/internal/windowframe"
)

// renderer is the slice of *gl.Renderer the demo drives; the seam exists so
// error paths can be tested deterministically without a GPU.
type renderer interface {
	Begin(render.Color) error
	DrawRect(render.Rect, render.Color)
	DrawText(string, driver.Point, render.Color, *text.Face) error
	SetClip(render.Rect)
	ClearClip()
	End() error
	Close()
}

// windowState is the single window's rendering state. All fields are only
// touched on the UI thread.
type windowState struct {
	target render.Target
	r      renderer
}

// Window look. The title bar geometry and colors come from the windowframe
// style; these cover the content area, the icon and the button glyphs.
var (
	contentBG     = render.Color{R: 0.16, G: 0.17, B: 0.19, A: 1}
	contentAccent = render.Color{R: 0.20, G: 0.60, B: 0.55, A: 1}
	contentText   = render.Color{R: 0.85, G: 0.86, B: 0.88, A: 1}
	glyphColor    = render.Color{R: 0.92, G: 0.92, B: 0.92, A: 1}
)

// drawFrame paints the whole window — title bar, control buttons and content
// — on demand only. The first call happens during init; a failure there
// fails init exactly like a failed renderer creation.
func (d *demo) drawFrame() error {
	st := d.st
	if st == nil || d.face == nil {
		return nil
	}
	if err := st.r.Begin(contentBG); err != nil {
		return err
	}
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	layout := d.style.Layout(st.target.Size())
	note(drawTitleBar(st.r, d.style, layout, d.ctl, d.face))
	note(drawContent(st.r, d.face))
	note(st.r.End())
	return first
}

// drawTitleBar paints the bar background, the 16x16 icon, the title text
// vertically centered and clipped to its layout rectangle, then restores the
// clip before drawing the three control buttons in their current state.
func drawTitleBar(r renderer, s windowframe.Style, l windowframe.Layout, ctl *windowframe.Controller, face *text.Face) (first error) {
	r.DrawRect(l.TitleBar, s.Background)
	drawIcon(r, l.Icon, s)

	r.SetClip(l.Title)
	if bmp, err := face.Rasterize(windowTitle); err != nil {
		first = err
	} else if bmp.Height > 0 {
		y := l.TitleBar.Y + (s.Height-float64(bmp.Height))/2
		if err := r.DrawText(windowTitle, driver.Point{X: l.Title.X, Y: y}, s.TitleColor, face); err != nil {
			first = err
		}
	}
	r.ClearClip()
	note := func(err error) {
		if first == nil {
			first = err
		}
	}
	note(drawButton(r, l.Minimize, windowframe.ButtonMinimize, s, ctl, face, drawMinimizeGlyph))
	note(drawButton(r, l.Maximize, windowframe.ButtonMaximize, s, ctl, face, drawMaximizeGlyph))
	note(drawButton(r, l.Close, windowframe.ButtonClose, s, ctl, face, drawCloseGlyph))
	return first
}

// drawIcon paints the 16x16 window icon as a framed emblem: the icon color
// filled square with the bar background punched back out and a small icon
// color core, so it reads as a mark rather than a hole.
func drawIcon(r renderer, rect render.Rect, s windowframe.Style) {
	r.DrawRect(rect, s.IconColor)
	r.DrawRect(insetRect(rect, 4), s.Background)
	r.DrawRect(insetRect(rect, 6), s.IconColor)
}

// glyphFunc paints one button symbol inside rect, whose content box is
// glyphBox points square. bg is the button background just drawn, so hollow
// symbols can punch themselves out.
type glyphFunc func(r renderer, rect render.Rect, bg render.Color, face *text.Face) error

// glyphBox is the edge length of the square every button symbol fits in.
const glyphBox = 10

// drawButton paints one control button: background by hover/press state,
// then its symbol centered inside.
func drawButton(r renderer, rect render.Rect, b windowframe.Button, s windowframe.Style, ctl *windowframe.Controller, face *text.Face, glyph glyphFunc) error {
	bg := buttonColor(s, b, ctl)
	r.DrawRect(rect, bg)
	box := render.Rect{
		X:      rect.X + (rect.Width-glyphBox)/2,
		Y:      rect.Y + (rect.Height-glyphBox)/2,
		Width:  glyphBox,
		Height: glyphBox,
	}
	return glyph(r, box, bg, face)
}

// buttonColor picks a button's background: press beats hover, hover beats
// idle, and the close button draws from its own color family.
func buttonColor(s windowframe.Style, b windowframe.Button, ctl *windowframe.Controller) render.Color {
	closeButton := b == windowframe.ButtonClose
	switch {
	case ctl.Active() == b:
		if closeButton {
			return s.CloseActive
		}
		return s.ButtonActive
	case ctl.Hover() == b:
		if closeButton {
			return s.CloseHover
		}
		return s.ButtonHover
	default:
		if closeButton {
			return s.CloseColor
		}
		return s.ButtonColor
	}
}

// drawMinimizeGlyph draws a horizontal bar on the box's bottom edge.
func drawMinimizeGlyph(r renderer, rect render.Rect, bg render.Color, face *text.Face) error {
	r.DrawRect(render.Rect{X: rect.X, Y: rect.Y + rect.Height - 2, Width: rect.Width, Height: 2}, glyphColor)
	return nil
}

// drawMaximizeGlyph draws a hollow square: the box in the glyph color with
// the button background punched back out of its center.
func drawMaximizeGlyph(r renderer, rect render.Rect, bg render.Color, face *text.Face) error {
	r.DrawRect(rect, glyphColor)
	r.DrawRect(insetRect(rect, 2), bg)
	return nil
}

// drawCloseGlyph draws an X as text: it stays crisp at any bar height and
// needs no image assets.
func drawCloseGlyph(r renderer, rect render.Rect, bg render.Color, face *text.Face) error {
	if face == nil {
		return nil
	}
	bmp, err := face.Rasterize("X")
	if err != nil {
		return err
	}
	if bmp.Width <= 0 || bmp.Height <= 0 {
		return nil
	}
	at := driver.Point{
		X: rect.X + (rect.Width-float64(bmp.Width))/2,
		Y: rect.Y + (rect.Height-float64(bmp.Height))/2,
	}
	return r.DrawText("X", at, glyphColor, face)
}

// drawContent paints the body below the title bar: a sample rectangle and
// the usage notes.
func drawContent(r renderer, face *text.Face) (first error) {
	r.DrawRect(render.Rect{X: 24, Y: 60, Width: 160, Height: 90}, contentAccent)
	lines := []string{
		"Meltemi frameless demo",
		"Drag the title bar to move.",
		"Double-click it to maximize.",
		"Drag window edges to resize.",
		"Esc or the X button closes.",
	}
	for i, line := range lines {
		if err := r.DrawText(line, driver.Point{X: 24, Y: 170 + float64(i)*24}, contentText, face); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func insetRect(r render.Rect, inset float64) render.Rect {
	return render.Rect{
		X:      r.X + inset,
		Y:      r.Y + inset,
		Width:  r.Width - 2*inset,
		Height: r.Height - 2*inset,
	}
}
