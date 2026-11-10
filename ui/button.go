package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

// Default button metrics from the built-in theme.
const (
	buttonHeight     = 36.0
	buttonPaddingX   = 16.0
	focusBorderWidth = 1.5
)

// ButtonWidget is a labeled, focusable control. It arms on a left press
// inside and fires when the matching release also lands inside; hover
// tracks the cursor, and the theme draws the focus ring while focused.
// The constructor is Button, mirroring the Column/ColumnLayout split.
type ButtonWidget struct {
	textBlock
	bounds   render.Rect
	id       string
	class    string
	resolved internalstyle.Resolved

	enabled bool
	onClick func()

	hover   bool
	pressed bool
	focused bool
}

// Button creates an enabled button showing the given text.
func Button(text string) *ButtonWidget {
	return &ButtonWidget{textBlock: textBlock{text: text}, enabled: true}
}

// ID sets the stylesheet ID selector metadata and returns the button.
func (b *ButtonWidget) ID(value string) *ButtonWidget {
	b.id = value
	return b
}

// Class sets the stylesheet class selector metadata and returns the button.
func (b *ButtonWidget) Class(value string) *ButtonWidget {
	b.class = value
	return b
}

func (b *ButtonWidget) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{
		Type: "Button", ID: b.id, Class: b.class,
		Hover: b.hover, Focus: b.focused, Disabled: !b.enabled, Pressed: b.pressed,
	}
}

func (b *ButtonWidget) setResolved(resolved internalstyle.Resolved) {
	b.resolved = resolved
}

// Text returns the button label.
func (b *ButtonWidget) Text() string {
	return b.text
}

// SetText replaces the label and drops the cached rasterization so the
// next measure and draw pick it up.
func (b *ButtonWidget) SetText(text string) {
	b.setText(text)
}

// Enabled reports whether the button reacts to input.
func (b *ButtonWidget) Enabled() bool {
	return b.enabled
}

// SetEnabled toggles input. Disabling drops the transient hover, pressed
// and focused state, so an in-flight press cannot fire later and a
// disabled button never keeps drawing its focus ring; the event router
// re-validates its focus on the next key event.
func (b *ButtonWidget) SetEnabled(enabled bool) {
	if b.enabled == enabled {
		return
	}
	b.enabled = enabled
	if !enabled {
		b.hover = false
		b.pressed = false
		b.focused = false
	}
}

// OnClick sets the handler Click runs and returns the button for
// chaining. A nil handler makes Click a no-op.
func (b *ButtonWidget) OnClick(fn func()) *ButtonWidget {
	b.onClick = fn
	return b
}

// Click runs the click handler when the button is enabled and a handler
// is installed. It is the imperative half of activation: an inside
// release and Enter both land here.
func (b *ButtonWidget) Click() {
	if !b.enabled || b.onClick == nil {
		return
	}
	b.onClick()
}

// measure returns the resolved label size including padding, clamped to the
// constraints. Before the App installs a face the label measures as empty
// and the button keeps its padding-only width.
func (b *ButtonWidget) measure(constraints Constraints) Size {
	return b.measureStyled(constraints, b.resolved)
}

func (b *ButtonWidget) arrange(bounds render.Rect) {
	b.bounds = bounds
}

// draw paints the button with its resolved style. Zero-radius focus retains
// the original four-strip ring; a resolved border or rounded focus uses the
// same outer-border/inner-background path as the rest of the box.
func (b *ButtonWidget) draw(ctx *drawContext) {
	b.drawStyled(ctx, b.resolved)
}

func (b *ButtonWidget) drawWithStyle(ctx *drawContext, style componentStyle) {
	drawStyle := style
	focusColor := defaultTheme.focusBorder
	if style.borderColorStyled {
		focusColor = style.borderColor
	}
	focusRing := b.focused && b.enabled && (!style.borderStyled || style.borderColorStyled && !style.borderWidthStyled && style.borderWidth == 0)
	if focusRing && style.borderRadius > 0 {
		drawStyle.borderColor = focusColor
		drawStyle.borderWidth = focusBorderWidth
	}
	drawComponentBox(ctx, b.bounds, drawStyle)
	bitmap := b.rasterize()
	if bitmap.Width != 0 || bitmap.Height != 0 {
		dst := centeredBitmapRect(styleContentRect(b.bounds, drawStyle), *bitmap)
		ctx.DrawText(b.text, driver.Point{X: dst.X, Y: dst.Y}, style.text, b.face)
	}
	if focusRing && style.borderRadius <= 0 {
		for _, strip := range focusStrips(b.bounds, focusBorderWidth) {
			ctx.DrawRect(strip, focusColor)
		}
	}
}

// background selects the theme background by state, most restrictive
// first: disabled, then armed, then hovered, then idle.
func (b *ButtonWidget) background(theme *theme) render.Color {
	switch {
	case !b.enabled:
		return theme.buttonBackgroundDisabled
	case b.pressed:
		return theme.buttonBackgroundActive
	case b.hover:
		return theme.buttonBackgroundHover
	default:
		return theme.buttonBackground
	}
}

// foreground selects the theme text color by state: disabled first.
func (b *ButtonWidget) foreground(theme *theme) render.Color {
	if !b.enabled {
		return theme.buttonTextDisabled
	}
	return theme.buttonText
}

// focusStrips splits the focus ring into four filled strips inside the
// bounds. A thickness beyond half the bounds collapses so degenerate
// bounds paint nothing outside themselves.
func focusStrips(bounds render.Rect, thickness float64) []render.Rect {
	t := min(thickness, bounds.Width/2, bounds.Height/2)
	if t <= 0 {
		return nil
	}
	return []render.Rect{
		{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: t},
		{X: bounds.X, Y: bounds.Y + bounds.Height - t, Width: bounds.Width, Height: t},
		{X: bounds.X, Y: bounds.Y + t, Width: t, Height: bounds.Height - 2*t},
		{X: bounds.X + bounds.Width - t, Y: bounds.Y + t, Width: t, Height: bounds.Height - 2*t},
	}
}

// handleEvent implements the pointer half of button behavior for events
// aimed at the button by the event router: hover follows moves, a left
// press inside arms the button, and the matching release fires only when
// it lands inside. Cursor leave drops hover but never the armed press,
// so the later release can still cancel cleanly.
func (b *ButtonWidget) handleEvent(e driver.Event) (handled, redraw bool) {
	if !b.enabled {
		return false, false
	}
	switch e.Kind {
	case driver.EventMouseMove:
		inside := b.contains(e.Position)
		changed := b.hover != inside
		b.hover = inside
		return true, changed
	case driver.EventMouseButton:
		if e.Button != driver.MouseButtonLeft {
			return false, false
		}
		switch e.Action {
		case driver.ActionPress:
			if !b.contains(e.Position) {
				return false, false
			}
			changed := !b.pressed
			b.pressed = true
			return true, changed
		case driver.ActionRelease:
			if !b.pressed {
				return false, false
			}
			b.pressed = false
			if b.contains(e.Position) {
				b.Click()
			}
			return true, true
		}
	case driver.EventCursorLeave:
		if !b.hover {
			return false, false
		}
		b.hover = false
		return true, true
	}
	return false, false
}

func (b *ButtonWidget) walk(visit func(component)) {
	visit(b)
}

func (b *ButtonWidget) isUIComponent() {}

// arrangedBounds returns the rect recorded by the last arrange.
func (b *ButtonWidget) arrangedBounds() render.Rect { return b.bounds }

func (b *ButtonWidget) acceptsPointer() bool { return true }

// contains reports whether p lies inside the arranged bounds. Zero-size
// bounds contain nothing, so a collapsed button never receives input.
func (b *ButtonWidget) contains(p driver.Point) bool {
	return boundsContain(b.bounds, p)
}

// focusable reports whether the button may take focus: never while
// disabled.
func (b *ButtonWidget) focusable() bool {
	return b.enabled
}

// isFocused reports whether the button currently holds its focus flag.
// The router checks it alongside focusable: a button that was disabled
// and re-enabled before the next key event is focusable again but must
// not resurrect the old focus.
func (b *ButtonWidget) isFocused() bool {
	return b.focused
}

func (b *ButtonWidget) setFocused(focused bool) {
	b.focused = focused
}

// activate is the keyboard half of button activation, wired to Enter by
// the event router.
func (b *ButtonWidget) activate() {
	b.Click()
}

// clearHover drops the hover state when the router moves the cursor
// elsewhere; it reports whether hover was set so the caller knows whether
// a repaint is due.
func (b *ButtonWidget) clearHover() (wasHovered bool) {
	wasHovered = b.hover
	b.hover = false
	return wasHovered
}
