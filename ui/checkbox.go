package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

// Default checkbox metrics from the built-in theme.
const (
	checkboxBoxSize     = 18.0
	checkboxTextGap     = 8.0
	checkboxBorderWidth = 1.5
	checkboxCheckInset  = 4.0
)

// CheckboxWidget is a labeled check box: a small box with a check mark when
// on, plus a text label. It is bound to an optional external *bool that both
// seeds the initial value and receives every change made through the
// widget. A click toggles it, and so do Enter and Space while it holds the
// focus. The constructor is Checkbox, mirroring the Column/ColumnLayout
// split.
type CheckboxWidget struct {
	textBlock
	bounds   render.Rect
	id       string
	class    string
	resolved internalstyle.Resolved

	value    bool
	binding  *bool
	enabled  bool
	onChange func(bool)

	hover   bool
	pressed bool
	focused bool
}

// Checkbox creates an enabled checkbox showing the given label, bound to
// value. A nil value creates an unbound checkbox that starts unchecked.
// Direct writes through the pointer do not notify the component; use
// SetValue to synchronize both explicitly.
func Checkbox(text string, value *bool) *CheckboxWidget {
	cb := &CheckboxWidget{textBlock: textBlock{text: text}, binding: value, enabled: true}
	if value != nil {
		cb.value = *value
	}
	return cb
}

// ID sets the stylesheet ID selector metadata and returns the checkbox.
func (c *CheckboxWidget) ID(value string) *CheckboxWidget {
	c.id = value
	return c
}

// Class sets the stylesheet class selector metadata and returns the
// checkbox.
func (c *CheckboxWidget) Class(value string) *CheckboxWidget {
	c.class = value
	return c
}

func (c *CheckboxWidget) styleMeta() internalstyle.Meta {
	return internalstyle.Meta{
		Type: "Checkbox", ID: c.id, Class: c.class,
		Hover: c.hover, Focus: c.focused, Disabled: !c.enabled,
		Checked: c.value, Pressed: c.pressed,
	}
}

func (c *CheckboxWidget) setResolved(resolved internalstyle.Resolved) {
	c.resolved = resolved
}

// Text returns the label shown next to the box.
func (c *CheckboxWidget) Text() string {
	return c.text
}

// SetText replaces the label and drops the cached rasterization so the
// next measure and draw pick it up.
func (c *CheckboxWidget) SetText(text string) {
	c.setText(text)
}

// Value reports whether the checkbox is checked.
func (c *CheckboxWidget) Value() bool {
	return c.value
}

// SetValue sets the checkbox, synchronizes a non-nil binding pointer and
// runs the change handler — but only when the value actually changes, so
// repeated calls with the same value are quiet.
func (c *CheckboxWidget) SetValue(value bool) {
	if c.value == value {
		return
	}
	c.value = value
	if c.binding != nil {
		*c.binding = value
	}
	if c.onChange != nil {
		c.onChange(value)
	}
}

// Enabled reports whether the checkbox reacts to input.
func (c *CheckboxWidget) Enabled() bool {
	return c.enabled
}

// SetEnabled toggles input. Disabling drops the transient hover, pressed
// and focused state, so an in-flight press cannot toggle later and a
// disabled checkbox never keeps drawing its focus state; the event router
// re-validates its focus on the next key event.
func (c *CheckboxWidget) SetEnabled(enabled bool) {
	if c.enabled == enabled {
		return
	}
	c.enabled = enabled
	if !enabled {
		c.hover = false
		c.pressed = false
		c.focused = false
	}
}

// OnChange sets the handler SetValue and the input paths run when the value
// actually changes, and returns the checkbox for chaining. A nil handler
// makes changes quiet.
func (c *CheckboxWidget) OnChange(fn func(bool)) *CheckboxWidget {
	c.onChange = fn
	return c
}

// toggle flips the checkbox through SetValue, so binding and handler stay
// in sync exactly as for programmatic changes.
func (c *CheckboxWidget) toggle() {
	c.SetValue(!c.value)
}

// measure returns the resolved natural size — box, gap and label — clamped
// to the constraints. Before the App installs a face the label measures as
// empty and the checkbox keeps its box-and-gap width.
func (c *CheckboxWidget) measure(constraints Constraints) Size {
	return c.measureStyled(constraints, c.resolved)
}

func (c *CheckboxWidget) arrange(bounds render.Rect) {
	c.bounds = bounds
}

// draw paints the checkbox with its resolved style.
func (c *CheckboxWidget) draw(ctx *drawContext) {
	c.drawStyled(ctx, c.resolved)
}

// drawWithStyle paints the box vertically centered on the left of the
// content rect and the label to its right, also vertically centered. The
// box fill is the stylesheet background (the theme's checked color by
// default when on); the check mark is a small inset rect, so it needs no
// font support. Degenerate bounds collapse the box and label to nothing
// instead of painting outside themselves.
func (c *CheckboxWidget) drawWithStyle(ctx *drawContext, style componentStyle) {
	// The resolved border draws the box ring, not a frame around the whole
	// control, so only the padding subtracts from the content rect.
	content := intersectRects(render.Rect{
		X:      c.bounds.X + style.padding.left,
		Y:      c.bounds.Y + style.padding.top,
		Width:  c.bounds.Width - style.padding.horizontal(),
		Height: c.bounds.Height - style.padding.vertical(),
	}, c.bounds)
	box := intersectRects(render.Rect{
		X:      content.X,
		Y:      content.Y + (content.Height-checkboxBoxSize)/2,
		Width:  checkboxBoxSize,
		Height: checkboxBoxSize,
	}, content)
	if box.Width > 0 && box.Height > 0 {
		ctx.DrawRoundedRect(box, style.background, style.borderRadius)
		if c.value {
			mark := insetStyleRect(box, checkboxCheckInset)
			if mark.Width > 0 && mark.Height > 0 {
				ctx.DrawRect(mark, c.markColor(defaultTheme))
			}
		}
		borderColor := style.borderColor
		if c.focused && c.enabled && !style.borderColorStyled {
			borderColor = defaultTheme.focusBorder
		}
		ctx.DrawRoundedBorder(box, borderColor, style.borderRadius, style.borderWidth)
	}
	if c.text == "" {
		return
	}
	bitmap := c.rasterize()
	if bitmap.Width == 0 && bitmap.Height == 0 {
		return
	}
	label := intersectRects(render.Rect{
		X:      content.X + checkboxBoxSize + checkboxTextGap,
		Y:      content.Y,
		Width:  content.Width - checkboxBoxSize - checkboxTextGap,
		Height: content.Height,
	}, content)
	if label.Width <= 0 || label.Height <= 0 {
		return
	}
	dst := centeredBitmapRect(label, *bitmap)
	ctx.DrawText(c.text, driver.Point{X: dst.X, Y: dst.Y}, style.text, c.face)
}

// boxBackground selects the theme box fill by state: disabled first, then
// checked, then idle white.
func (c *CheckboxWidget) boxBackground(theme *theme) render.Color {
	switch {
	case !c.enabled:
		return theme.checkboxBackgroundDisabled
	case c.value:
		return theme.checkboxChecked
	default:
		return theme.checkboxBackground
	}
}

// foreground selects the theme label color by state: disabled first.
func (c *CheckboxWidget) foreground(theme *theme) render.Color {
	if !c.enabled {
		return theme.buttonTextDisabled
	}
	return theme.buttonText
}

// markColor selects the check mark color: white on the accent fill, and the
// disabled text color on the grayed disabled fill where white would vanish.
func (c *CheckboxWidget) markColor(theme *theme) render.Color {
	if !c.enabled {
		return theme.buttonTextDisabled
	}
	return theme.checkboxCheckMark
}

// handleEvent implements the input half of checkbox behavior for events
// aimed at it by the event router: hover follows moves, a left press inside
// arms the checkbox, the matching release inside toggles and one outside
// only cancels, and Space while focused toggles. Enter reaches activate
// through the router. A disabled checkbox is fully inert.
func (c *CheckboxWidget) handleEvent(e driver.Event) (handled, redraw bool) {
	if !c.enabled {
		return false, false
	}
	switch e.Kind {
	case driver.EventMouseMove:
		inside := c.contains(e.Position)
		changed := c.hover != inside
		c.hover = inside
		return true, changed
	case driver.EventMouseButton:
		if e.Button != driver.MouseButtonLeft {
			return false, false
		}
		switch e.Action {
		case driver.ActionPress:
			if !c.contains(e.Position) {
				return false, false
			}
			changed := !c.pressed
			c.pressed = true
			return true, changed
		case driver.ActionRelease:
			if !c.pressed {
				return false, false
			}
			c.pressed = false
			if c.contains(e.Position) {
				c.toggle()
			}
			return true, true
		}
	case driver.EventCursorLeave:
		if !c.hover {
			return false, false
		}
		c.hover = false
		return true, true
	case driver.EventKey:
		if !c.focused || e.Key != driver.KeySpace || e.Action != driver.ActionPress {
			return false, false
		}
		c.toggle()
		return true, true
	}
	return false, false
}

func (c *CheckboxWidget) walk(visit func(component)) {
	visit(c)
}

func (c *CheckboxWidget) isUIComponent() {}

// arrangedBounds returns the rect recorded by the last arrange.
func (c *CheckboxWidget) arrangedBounds() render.Rect { return c.bounds }

func (c *CheckboxWidget) acceptsPointer() bool { return true }

// contains reports whether p lies inside the arranged bounds. Zero-size
// bounds contain nothing, so a collapsed checkbox never receives input.
func (c *CheckboxWidget) contains(p driver.Point) bool {
	return boundsContain(c.bounds, p)
}

// focusable reports whether the checkbox may take focus: never while
// disabled.
func (c *CheckboxWidget) focusable() bool {
	return c.enabled
}

// isFocused reports whether the checkbox currently holds its focus flag.
// The router checks it alongside focusable: a checkbox that was disabled
// and re-enabled before the next key event is focusable again but must not
// resurrect the old focus.
func (c *CheckboxWidget) isFocused() bool {
	return c.focused
}

func (c *CheckboxWidget) setFocused(focused bool) {
	c.focused = focused
}

// activate is the keyboard half of checkbox activation, wired to Enter by
// the event router; Space is handled directly in handleEvent.
func (c *CheckboxWidget) activate() {
	if !c.enabled {
		return
	}
	c.toggle()
}

// clearHover drops the hover state when the router moves the cursor
// elsewhere; it reports whether hover was set so the caller knows whether
// a repaint is due.
func (c *CheckboxWidget) clearHover() (wasHovered bool) {
	wasHovered = c.hover
	c.hover = false
	return wasHovered
}
