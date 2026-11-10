package ui

import (
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

type edgePadding struct {
	top, right, bottom, left float64
}

func uniformPadding(value float64) edgePadding {
	return edgePadding{top: value, right: value, bottom: value, left: value}
}

func (p edgePadding) horizontal() float64 { return p.left + p.right }
func (p edgePadding) vertical() float64   { return p.top + p.bottom }

type componentStyle struct {
	background        render.Color
	text              render.Color
	borderColor       render.Color
	selection         render.Color
	borderWidth       float64
	borderRadius      float64
	padding           edgePadding
	width             float64
	height            float64
	gap               float64
	fontFamily        string
	fontSize          float64
	opacity           float64
	borderStyled      bool
	borderColorStyled bool
	borderWidthStyled bool
}

func defaultComponentStyle() componentStyle {
	return componentStyle{fontFamily: DefaultFont, fontSize: defaultFontSize, opacity: 1}
}

func (s componentStyle) merge(resolved internalstyle.Resolved) componentStyle {
	values := resolved.Values
	if opacity, err := internalstyle.ParseOpacity(values["opacity"]); err == nil && values["opacity"] != "" {
		s.opacity = opacity
	}
	for _, name := range []string{"background", "background-color"} {
		if color, ok := styleColor(values, name); ok {
			s.background = color
		}
	}
	if color, ok := styleColor(values, "color"); ok {
		s.text = color
	}
	if border, err := internalstyle.ParseBorder(values["border"]); err == nil && values["border"] != "" {
		s.borderWidth = border.Width.Value
		s.borderColor = renderColor(border.Color)
		s.borderStyled = true
		s.borderColorStyled = true
		s.borderWidthStyled = true
	}
	if color, ok := styleColor(values, "border-color"); ok {
		s.borderColor = color
		s.borderStyled = true
		s.borderColorStyled = true
	}
	if width, ok := styleDimension(values, "border-width"); ok {
		s.borderWidth = width
		s.borderStyled = true
		s.borderWidthStyled = true
	}
	if radius, ok := styleDimension(values, "border-radius"); ok {
		s.borderRadius = radius
	}
	if padding, err := internalstyle.ParsePadding(values["padding"]); err == nil && values["padding"] != "" {
		s.padding = expandPadding(padding)
	}
	if width, ok := styleDimension(values, "width"); ok {
		s.width = width
	}
	if height, ok := styleDimension(values, "height"); ok {
		s.height = height
	}
	if family := values["font-family"]; family != "" {
		s.fontFamily = family
	}
	if size, ok := styleDimension(values, "font-size"); ok {
		s.fontSize = size
	}
	return s
}

func styleColor(values map[string]string, name string) (render.Color, bool) {
	value, ok := values[name]
	if !ok {
		return render.Color{}, false
	}
	parsed, err := internalstyle.ParseColor(value)
	if err != nil {
		return render.Color{}, false
	}
	return renderColor(parsed), true
}

func renderColor(color internalstyle.Color) render.Color {
	return render.Color{
		R: float32(color.R) / 255,
		G: float32(color.G) / 255,
		B: float32(color.B) / 255,
		A: float32(color.A) / 255,
	}
}

func styleDimension(values map[string]string, name string) (float64, bool) {
	value, ok := values[name]
	if !ok {
		return 0, false
	}
	parsed, err := internalstyle.ParseDimension(value)
	if err != nil {
		return 0, false
	}
	return parsed.Value, true
}

func expandPadding(values []internalstyle.Dimension) edgePadding {
	switch len(values) {
	case 1:
		return uniformPadding(values[0].Value)
	case 2:
		return edgePadding{top: values[0].Value, right: values[1].Value, bottom: values[0].Value, left: values[1].Value}
	case 3:
		return edgePadding{top: values[0].Value, right: values[1].Value, bottom: values[2].Value, left: values[1].Value}
	case 4:
		return edgePadding{top: values[0].Value, right: values[1].Value, bottom: values[2].Value, left: values[3].Value}
	default:
		return edgePadding{}
	}
}

func labelComponentStyle(label *LabelWidget, resolved internalstyle.Resolved) componentStyle {
	bitmap := label.rasterize()
	s := defaultComponentStyle()
	s.text = defaultTheme.labelText
	s.borderColor = defaultTheme.labelText
	s.width = float64(bitmap.Width)
	s.height = float64(bitmap.Height)
	s = s.merge(resolved)
	if _, ok := styleDimension(resolved.Values, "width"); !ok {
		s.width = float64(bitmap.Width) + s.padding.horizontal()
	}
	if _, ok := styleDimension(resolved.Values, "height"); !ok {
		s.height = float64(bitmap.Height) + s.padding.vertical()
	}
	return s
}

func buttonComponentStyle(button *ButtonWidget, resolved internalstyle.Resolved) componentStyle {
	bitmap := button.rasterize()
	s := defaultComponentStyle()
	s.background = button.background(defaultTheme)
	s.text = button.foreground(defaultTheme)
	s.borderColor = defaultTheme.focusBorder
	s.padding = edgePadding{left: buttonPaddingX, right: buttonPaddingX}
	s = s.merge(resolved)
	if _, ok := styleDimension(resolved.Values, "width"); !ok {
		s.width = float64(bitmap.Width) + s.padding.horizontal()
	}
	if _, ok := styleDimension(resolved.Values, "height"); !ok {
		s.height = max(buttonHeight, float64(bitmap.Height)+s.padding.vertical())
	}
	return s
}

// checkboxComponentStyle resolves the box and label of a checkbox. The
// stylesheet background fills the box — the theme's accent by default when
// the checkbox is checked — and border values style the box ring; width,
// height and padding grow the natural box-plus-label size the way Button
// does.
func checkboxComponentStyle(checkbox *CheckboxWidget, resolved internalstyle.Resolved) componentStyle {
	bitmap := checkbox.rasterize()
	s := defaultComponentStyle()
	s.background = checkbox.boxBackground(defaultTheme)
	s.text = checkbox.foreground(defaultTheme)
	s.borderColor = defaultTheme.checkboxBorder
	s.borderWidth = checkboxBorderWidth
	labelWidth := 0.0
	labelHeight := 0.0
	if checkbox.text != "" {
		labelWidth = checkboxTextGap + float64(bitmap.Width)
		labelHeight = float64(bitmap.Height)
	}
	s.width = checkboxBoxSize + labelWidth
	s.height = max(checkboxBoxSize, labelHeight)
	s = s.merge(resolved)
	if _, ok := styleDimension(resolved.Values, "width"); !ok {
		s.width = checkboxBoxSize + labelWidth + s.padding.horizontal()
	}
	if _, ok := styleDimension(resolved.Values, "height"); !ok {
		s.height = max(checkboxBoxSize, labelHeight+s.padding.vertical())
	}
	return s
}

func inputComponentStyle(input *InputWidget, resolved internalstyle.Resolved) componentStyle {
	s := defaultComponentStyle()
	s.background = defaultTheme.inputBackground
	s.text = defaultTheme.buttonText
	if !input.enabled {
		s.background = defaultTheme.inputDisabledBackground
		s.text = defaultTheme.buttonTextDisabled
	}
	s.borderColor = defaultTheme.inputBorder
	s.borderWidth = inputBorderWidth
	s.selection = defaultTheme.inputSelection
	s.padding = edgePadding{left: inputPaddingX, right: inputPaddingX}
	s.width = inputNaturalWidth
	s.height = inputHeight
	s = s.merge(resolved)
	if resolved.Values["padding"] != "" {
		metrics := input.textMetrics()
		if _, ok := styleDimension(resolved.Values, "width"); !ok {
			s.width = max(inputNaturalWidth, float64(metrics.bitmap.Width)+s.padding.horizontal())
		}
		if _, ok := styleDimension(resolved.Values, "height"); !ok {
			s.height = max(inputHeight, float64(metrics.bitmap.Height)+s.padding.vertical())
		}
	}
	return s
}

func textAreaComponentStyle(area *TextAreaWidget, resolved internalstyle.Resolved) componentStyle {
	s := defaultComponentStyle()
	s.background = defaultTheme.inputBackground
	s.text = defaultTheme.buttonText
	if !area.enabled {
		s.background = defaultTheme.inputDisabledBackground
		s.text = defaultTheme.buttonTextDisabled
	}
	s.borderColor = defaultTheme.inputBorder
	s.borderWidth = inputBorderWidth
	s.selection = defaultTheme.inputSelection
	s.padding = edgePadding{top: textAreaPaddingY, right: inputPaddingX, bottom: textAreaPaddingY, left: inputPaddingX}
	s.width = textAreaNaturalWidth
	s.height = textAreaNaturalHeight
	s = s.merge(resolved)
	if resolved.Values["padding"] != "" {
		metrics := area.textMetrics()
		contentWidth := 0.0
		for _, line := range metrics.lines {
			contentWidth = max(contentWidth, float64(line.bitmap.Width))
		}
		contentHeight := float64(len(metrics.lines)) * metrics.lineHeight
		if _, ok := styleDimension(resolved.Values, "width"); !ok {
			s.width = max(textAreaNaturalWidth, contentWidth+s.padding.horizontal())
		}
		if _, ok := styleDimension(resolved.Values, "height"); !ok {
			s.height = max(textAreaNaturalHeight, contentHeight+s.padding.vertical())
		}
	}
	return s
}

func imageComponentStyle(image *ImageWidget, resolved internalstyle.Resolved) componentStyle {
	width, height := image.source.Size()
	s := defaultComponentStyle()
	s.width = float64(width)
	s.height = float64(height)
	s = s.merge(resolved)
	if _, ok := styleDimension(resolved.Values, "width"); !ok {
		s.width = float64(width) + s.padding.horizontal()
	}
	if _, ok := styleDimension(resolved.Values, "height"); !ok {
		s.height = float64(height) + s.padding.vertical()
	}
	return s
}

func (image *ImageWidget) measureStyled(constraints Constraints, resolved internalstyle.Resolved) Size {
	s := imageComponentStyle(image, resolved)
	return clampSize(Size{Width: s.width, Height: s.height}, constraints)
}

func (image *ImageWidget) drawStyled(ctx *drawContext, resolved internalstyle.Resolved) {
	image.drawWithStyle(ctx, imageComponentStyle(image, resolved))
}

// containerComponentStyle resolves the gap and padding of the box
// layouts. An explicit Go value takes priority over the stylesheet value.
// Column, Row and Stack share it; Stack passes no gap.
func containerComponentStyle(gap, padding float64, gapExplicit, paddingExplicit bool, resolved internalstyle.Resolved) componentStyle {
	s := defaultComponentStyle()
	s.gap = gap
	s.padding = uniformPadding(padding)
	if !gapExplicit {
		if value, ok := styleDimension(resolved.Values, "gap"); ok {
			s.gap = value
		}
	}
	if !paddingExplicit {
		if values, err := internalstyle.ParsePadding(resolved.Values["padding"]); err == nil && resolved.Values["padding"] != "" {
			s.padding = expandPadding(values)
		}
	}
	return s
}

// stackComponentStyle resolves the stack padding; a stack has no gap, and
// a stylesheet gap must not invent one.
func stackComponentStyle(padding float64, paddingExplicit bool, resolved internalstyle.Resolved) componentStyle {
	return containerComponentStyle(0, padding, true, paddingExplicit, resolved)
}

// scrollComponentStyle resolves the scroll padding; a scroll holds a single
// content child, so a stylesheet gap has nothing to space and is ignored.
func scrollComponentStyle(padding float64, paddingExplicit bool, resolved internalstyle.Resolved) componentStyle {
	return containerComponentStyle(0, padding, true, paddingExplicit, resolved)
}

func menuBarComponentStyle(bar *menuBarWidget, resolved internalstyle.Resolved) componentStyle {
	s := defaultComponentStyle()
	s.background = color(0.94, 0.94, 0.94)
	s.borderColor = defaultTheme.inputBorder
	s.padding = uniformPadding(2)
	if bar.popup {
		s.background = color(1, 1, 1)
		s.borderWidth = 1
		s.borderRadius = 4
		s.padding = uniformPadding(4)
	}
	return s.merge(resolved)
}

func menuItemComponentStyle(item *menuItemWidget, resolved internalstyle.Resolved) componentStyle {
	s := defaultComponentStyle()
	s.text = defaultTheme.buttonText
	s.borderColor = defaultTheme.inputBorder
	s.padding = edgePadding{top: 3, right: 8, bottom: 3, left: 8}
	if item.menu != nil {
		s.padding = edgePadding{top: 2, right: 12, bottom: 2, left: 12}
	}
	if item.model != nil && !item.model.enabled {
		s.text = defaultTheme.buttonTextDisabled
	}
	switch {
	case item.pressed || item.open:
		s.background = defaultTheme.buttonBackgroundActive
	case item.hover:
		s.background = defaultTheme.buttonBackgroundHover
	}
	s = s.merge(resolved)

	bitmap := item.rasterize()
	naturalWidth := float64(bitmap.Width) + s.padding.horizontal()
	naturalHeight := max(menuItemDefaultHeight, float64(bitmap.Height)+s.padding.vertical())
	if item.menu == nil {
		naturalWidth += menuCheckWidth
		if shortcut := item.shortcutBlock.rasterize(); shortcut.Width != 0 {
			naturalWidth += float64(shortcut.Width) + 12
		}
		if item.arrowBlock.text != "" {
			naturalWidth += menuArrowWidth
		}
		naturalWidth = max(menuPopupMinWidth, naturalWidth)
	}
	if item.model != nil && item.model.separator {
		naturalWidth = menuPopupMinWidth
		naturalHeight = menuSeparatorHeight
	}
	if _, ok := styleDimension(resolved.Values, "width"); !ok {
		s.width = naturalWidth
	}
	if _, ok := styleDimension(resolved.Values, "height"); !ok {
		s.height = naturalHeight
	}
	return s
}

func unresolvedStyle() internalstyle.Resolved { return internalstyle.Resolved{} }

func (label *LabelWidget) measureStyled(constraints Constraints, resolved internalstyle.Resolved) Size {
	s := labelComponentStyle(label, resolved)
	return clampSize(Size{Width: s.width, Height: s.height}, constraints)
}

func (label *LabelWidget) drawStyled(ctx *drawContext, resolved internalstyle.Resolved) {
	label.drawWithStyle(ctx, labelComponentStyle(label, resolved))
}

func (button *ButtonWidget) measureStyled(constraints Constraints, resolved internalstyle.Resolved) Size {
	s := buttonComponentStyle(button, resolved)
	return clampSize(Size{Width: s.width, Height: s.height}, constraints)
}

func (button *ButtonWidget) drawStyled(ctx *drawContext, resolved internalstyle.Resolved) {
	button.drawWithStyle(ctx, buttonComponentStyle(button, resolved))
}

func (checkbox *CheckboxWidget) measureStyled(constraints Constraints, resolved internalstyle.Resolved) Size {
	s := checkboxComponentStyle(checkbox, resolved)
	return clampSize(Size{Width: s.width, Height: s.height}, constraints)
}

func (checkbox *CheckboxWidget) drawStyled(ctx *drawContext, resolved internalstyle.Resolved) {
	checkbox.drawWithStyle(ctx, checkboxComponentStyle(checkbox, resolved))
}

func (input *InputWidget) measureStyled(constraints Constraints, resolved internalstyle.Resolved) Size {
	input.resolved = resolved
	return input.measure(constraints)
}

func (input *InputWidget) drawStyled(ctx *drawContext, resolved internalstyle.Resolved) {
	input.resolved = resolved
	input.drawWithStyle(ctx, inputComponentStyle(input, resolved))
}

func (area *TextAreaWidget) measureStyled(constraints Constraints, resolved internalstyle.Resolved) Size {
	area.resolved = resolved
	return area.measure(constraints)
}

func (area *TextAreaWidget) drawStyled(ctx *drawContext, resolved internalstyle.Resolved) {
	area.resolved = resolved
	area.drawWithStyle(ctx, textAreaComponentStyle(area, resolved))
}

func insetStyleRect(bounds render.Rect, inset float64) render.Rect {
	inset = max(inset, 0)
	return intersectRects(render.Rect{
		X:      bounds.X + inset,
		Y:      bounds.Y + inset,
		Width:  bounds.Width - 2*inset,
		Height: bounds.Height - 2*inset,
	}, bounds)
}

func styleContentRect(bounds render.Rect, style componentStyle) render.Rect {
	padded := intersectRects(render.Rect{
		X:      bounds.X + style.padding.left,
		Y:      bounds.Y + style.padding.top,
		Width:  bounds.Width - style.padding.horizontal(),
		Height: bounds.Height - style.padding.vertical(),
	}, bounds)
	return intersectRects(padded, insetStyleRect(bounds, style.borderWidth))
}

func drawComponentBox(ctx *drawContext, bounds render.Rect, style componentStyle) {
	if style.background.A != 0 {
		ctx.DrawRoundedRect(bounds, style.background, style.borderRadius)
	}
	if style.borderWidth > 0 {
		ctx.DrawRoundedBorder(bounds, style.borderColor, style.borderRadius, style.borderWidth)
	}
}
