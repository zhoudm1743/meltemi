package ui

import "github.com/zhoudm1743/meltemi/internal/render"

// theme is the built-in color palette the components draw with. It holds
// no font state: the App owns the face, installs it into the component
// tree with setTreeFace and injects it per frame into the drawContext.
// The palette stays inside the package: applications compose components,
// never paint directly, so the theme remains an implementation detail
// later tasks can extend or replace.
type theme struct {
	labelText render.Color

	buttonBackground         render.Color
	buttonBackgroundHover    render.Color
	buttonBackgroundActive   render.Color
	buttonBackgroundDisabled render.Color

	buttonText         render.Color
	buttonTextDisabled render.Color

	inputBackground         render.Color
	inputDisabledBackground render.Color
	inputBorder             render.Color
	inputPlaceholder        render.Color
	inputSelection          render.Color
	inputCaret              render.Color

	checkboxBackground         render.Color
	checkboxBackgroundDisabled render.Color
	checkboxBorder             render.Color
	checkboxChecked            render.Color
	checkboxCheckMark          render.Color

	focusBorder render.Color
}

// defaultTheme is the package palette: neutral grays with a blue focus
// ring.
var defaultTheme = &theme{
	labelText: color(0.13, 0.13, 0.13),

	buttonBackground:         color(0.94, 0.94, 0.94),
	buttonBackgroundHover:    color(0.87, 0.87, 0.87),
	buttonBackgroundActive:   color(0.78, 0.78, 0.78),
	buttonBackgroundDisabled: color(0.96, 0.96, 0.96),

	buttonText:         color(0.13, 0.13, 0.13),
	buttonTextDisabled: color(0.60, 0.60, 0.60),

	inputBackground:         color(1, 1, 1),
	inputDisabledBackground: color(0.96, 0.96, 0.96),
	inputBorder:             color(0.70, 0.70, 0.70),
	inputPlaceholder:        color(0.55, 0.55, 0.55),
	inputSelection:          render.Color{R: 0.25, G: 0.47, B: 0.85, A: 0.25},
	inputCaret:              color(0.13, 0.13, 0.13),

	checkboxBackground:         color(1, 1, 1),
	checkboxBackgroundDisabled: color(0.96, 0.96, 0.96),
	checkboxBorder:             color(0.55, 0.55, 0.55),
	checkboxChecked:            color(0.25, 0.47, 0.85),
	checkboxCheckMark:          color(1, 1, 1),

	focusBorder: color(0.25, 0.47, 0.85),
}

func color(r, g, b float32) render.Color {
	return render.Color{R: r, G: g, B: b, A: 1}
}
