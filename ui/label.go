package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// textBlock is the shared text state of the text-bearing components. The
// face is app-owned and arrives through setFace; the bitmap caches the
// rasterization so measure and draw share one copy.
type textBlock struct {
	text   string
	face   *text.Face
	bitmap *text.Bitmap
}

func (t *textBlock) setText(value string) {
	if t.text == value {
		return
	}
	t.text = value
	t.bitmap = nil
}

// setFace installs the app-owned face and drops the cached rasterization only
// when the face changes: a new face may measure the same text differently.
func (t *textBlock) setFace(face *text.Face) {
	if t.face == face {
		return
	}
	t.face = face
	t.bitmap = nil
}

// rasterize returns the cached bitmap, rasterizing through the installed
// face. Before the App injects a face — or after it closes one — the
// block measures and draws as empty text instead of failing a frame.
func (t *textBlock) rasterize() *text.Bitmap {
	if t.bitmap == nil {
		bitmap := text.Bitmap{}
		if t.face != nil {
			if rasterized, err := t.face.Rasterize(t.text); err == nil {
				bitmap = rasterized
			}
		}
		t.bitmap = &bitmap
	}
	return t.bitmap
}

// centeredBitmapRect places one bitmap in the middle of bounds. A bitmap
// wider or taller than the bounds overflows symmetrically; degenerate
// bounds never produce a negative rect.
func centeredBitmapRect(bounds render.Rect, bitmap text.Bitmap) render.Rect {
	return render.Rect{
		X:      bounds.X + (bounds.Width-float64(bitmap.Width))/2,
		Y:      bounds.Y + (bounds.Height-float64(bitmap.Height))/2,
		Width:  float64(bitmap.Width),
		Height: float64(bitmap.Height),
	}
}

// LabelWidget is a non-interactive line of text. Its natural size is
// exactly the rasterized text size, so the measured size follows the
// installed face. The constructor is Label: the Go package namespace
// cannot hold a type and a function with the same name, so the
// constructor keeps the brief's name and the type carries the Widget
// suffix, matching the Column/ColumnLayout split from task one.
type LabelWidget struct {
	textBlock
	bounds   render.Rect
	id       string
	class    string
	resolved internalstyle.Resolved
}

// Label creates a label showing the given text.
func Label(text string) *LabelWidget {
	return &LabelWidget{textBlock: textBlock{text: text}}
}

// ID sets the stylesheet ID selector metadata and returns the label.
func (l *LabelWidget) ID(value string) *LabelWidget {
	l.id = value
	return l
}

// Class sets the stylesheet class selector metadata and returns the label.
func (l *LabelWidget) Class(value string) *LabelWidget {
	l.class = value
	return l
}

func (l *LabelWidget) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{Type: "Label", ID: l.id, Class: l.class}
}

func (l *LabelWidget) setResolved(resolved internalstyle.Resolved) {
	l.resolved = resolved
}

// Text returns the line the label shows.
func (l *LabelWidget) Text() string {
	return l.text
}

// SetText replaces the line. The cached rasterization is dropped so the
// next measure and draw pick the new text up; the repaint itself is the
// window loop's job.
func (l *LabelWidget) SetText(text string) {
	l.setText(text)
}

// measure returns the resolved natural size clamped to the constraints.
// Before the App installs a face the text measures as empty.
func (l *LabelWidget) measure(constraints Constraints) Size {
	return l.measureStyled(constraints, l.resolved)
}

func (l *LabelWidget) arrange(bounds render.Rect) {
	l.bounds = bounds
}

// draw paints the label with its resolved style. The cached bitmap decides
// whether there is text to draw; the string and face travel to the painter,
// which rasterizes exactly once.
func (l *LabelWidget) draw(ctx *drawContext) {
	l.drawStyled(ctx, l.resolved)
}

func (l *LabelWidget) drawWithStyle(ctx *drawContext, style componentStyle) {
	drawComponentBox(ctx, l.bounds, style)
	bitmap := l.rasterize()
	if bitmap.Width == 0 && bitmap.Height == 0 {
		return
	}
	content := styleContentRect(l.bounds, style)
	ctx.DrawText(l.text, driver.Point{X: content.X, Y: content.Y}, style.text, l.face)
}

// handleEvent: a label never interacts.
func (l *LabelWidget) handleEvent(driver.Event) (bool, bool) {
	return false, false
}

func (l *LabelWidget) walk(visit func(component)) {
	visit(l)
}

func (l *LabelWidget) isUIComponent() {}

// arrangedBounds returns the rect recorded by the last arrange.
func (l *LabelWidget) arrangedBounds() render.Rect { return l.bounds }
