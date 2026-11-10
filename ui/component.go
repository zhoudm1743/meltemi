// Package ui provides Meltemi's public component API: a sealed Component
// handle that applications compose through constructors like Column, plus
// the internal contract the built-in components implement.
package ui

import (
	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// Component is the public handle of a built-in UI component. The sealed
// isUIComponent method keeps the drawable set closed: applications can
// hold, return and compose built-in components, but cannot plug in their
// own arbitrary drawing implementations in this stage.
type Component interface {
	isUIComponent()
}

// component is the internal contract every built-in component implements.
// measure runs top-down with parent constraints and returns the clamped
// size; arrange receives the rect chosen by the parent; draw paints with
// the internal context; handleEvent reports whether the event was handled
// and whether a redraw is needed; walk visits the component and its
// descendants.
type component interface {
	Component
	measure(Constraints) Size
	arrange(render.Rect)
	draw(*drawContext)
	handleEvent(driver.Event) (handled, redraw bool)
	walk(func(component))
}

// childContainer is the internal contract of components that own child
// components — the box layouts today, Scroll in the next stage. Style
// resolution and the recursive hit tester traverse the tree through this
// interface, so the public Component stays sealed and a new container
// joins both walks by implementing it, without touching them.
type childContainer interface {
	component
	childComponents() []component
}

// boundsReporter is implemented by every built-in component to expose the
// rect recorded during arrange. The hit tester gates traversal on it: a
// point outside a container's arranged bounds never reaches its children,
// which is the clip boundary later containers such as Scroll build on.
type boundsReporter interface {
	arrangedBounds() render.Rect
}

func layoutComponent(root component, bounds render.Rect) {
	root.measure(Constraints{Max: Size{Width: bounds.Width, Height: bounds.Height}})
	root.arrange(bounds)
}

// painter is the package-internal paint sink one frame draws into. The
// window task supplies the frame's painter — the existing *gl.Renderer
// satisfies this contract directly, so no adapter and no second
// rasterization stand between the components and the GL backend. It stays
// unexported: applications compose components, they never paint directly,
// and no arbitrary canvas leaks through the sealed component set.
type painter interface {
	DrawRect(rect render.Rect, color render.Color)
	DrawRoundedRect(rect render.Rect, color render.Color, radius float64)
	DrawRoundedBorder(rect render.Rect, color render.Color, radius, width float64)
	DrawText(value string, at driver.Point, color render.Color, face *text.Face) error
	DrawImage(source *render.Image, destination render.Rect, opacity float64) error
	SetClip(render.Rect)
	ClearClip()
}

// drawContext carries the internal rendering, font and final-alpha state a
// component needs while drawing a frame. The window loop injects the app-owned
// face, the frame's painter and an opacity of 1; err collects the first paint
// error of the frame so the loop can return it. A zero-value context also has
// effective opacity 1 for layout-only and direct-draw tests; opacitySet keeps
// that default distinct from an explicitly composed opacity of 0.
type drawContext struct {
	face       *text.Face
	painter    painter
	opacity    float64
	opacitySet bool
	err        error
	// clips is the stack of active clip rectangles. SetClip intersects the
	// new rect with the current one and pushes the result, so nested
	// containers such as Scroll inside Scroll clip to the overlap; ClearClip
	// pops and re-applies the parent rect, restoring it in a defer.
	clips []render.Rect
}

func (ctx *drawContext) effectiveOpacity() float64 {
	if !ctx.opacitySet && ctx.opacity == 0 {
		return 1
	}
	return ctx.opacity
}

// pushOpacity enters one component's nested composition scope and returns the
// restoration function. The caller defers that function so siblings and parents
// regain their previous alpha even when painting records an error or returns.
func (ctx *drawContext) pushOpacity(opacity float64) func() {
	previous, previousSet := ctx.opacity, ctx.opacitySet
	ctx.opacity = ctx.effectiveOpacity() * opacity
	ctx.opacitySet = true
	return func() {
		ctx.opacity, ctx.opacitySet = previous, previousSet
	}
}

func (ctx *drawContext) withOpacity(color render.Color) render.Color {
	color.A *= float32(ctx.effectiveOpacity())
	return color
}

// DrawRect records one filled rectangle with the composed final alpha.
func (ctx *drawContext) DrawRect(rect render.Rect, color render.Color) {
	if ctx.painter == nil {
		return
	}
	ctx.painter.DrawRect(rect, ctx.withOpacity(color))
}

// DrawRoundedRect records a rounded rectangle, retaining the original paint
// stream for zero radii and applying composition alpha exactly once.
func (ctx *drawContext) DrawRoundedRect(rect render.Rect, color render.Color, radius float64) {
	if radius <= 0 {
		ctx.DrawRect(rect, color)
		return
	}
	if ctx.painter != nil {
		ctx.painter.DrawRoundedRect(rect, ctx.withOpacity(color), radius)
	}
}

func (ctx *drawContext) DrawRoundedBorder(rect render.Rect, color render.Color, radius, width float64) {
	if ctx.painter != nil && width > 0 {
		ctx.painter.DrawRoundedBorder(rect, ctx.withOpacity(color), radius, width)
	}
}

// DrawText records one line of text with its top-left corner at at. The
// string and the app-owned face travel together to the painter, which
// rasterizes exactly once — the components' cached bitmaps only size and
// center the text. The first DrawText error, for example a failed shader
// or texture upload in the GL renderer, is kept for the window loop;
// painting the rest of the frame continues.
func (ctx *drawContext) DrawText(value string, at driver.Point, color render.Color, face *text.Face) {
	if ctx.painter == nil {
		return
	}
	if err := ctx.painter.DrawText(value, at, ctx.withOpacity(color), face); err != nil {
		ctx.setErr(err)
	}
}

// DrawImage records one image stretched to destination. opacity is already
// the frame's final alpha; neither the component nor the painter applies a
// second component-opacity value. Empty sources, degenerate destinations and
// fully transparent draws create no renderer work.
func (ctx *drawContext) DrawImage(source *render.Image, destination render.Rect) {
	opacity := ctx.effectiveOpacity()
	if ctx.painter == nil || source == nil || destination.Width <= 0 || destination.Height <= 0 || opacity <= 0 {
		return
	}
	if err := ctx.painter.DrawImage(source, destination, opacity); err != nil {
		ctx.setErr(err)
	}
}

// SetClip pushes a clip rectangle, intersected with the currently active
// one so nested containers clip to the overlap, and applies it to the
// painter. With no active clip it applies the rect unchanged, matching the
// painter-level contract for single-level users.
func (ctx *drawContext) SetClip(bounds render.Rect) {
	if len(ctx.clips) > 0 {
		bounds = intersectRects(bounds, ctx.clips[len(ctx.clips)-1])
	}
	ctx.clips = append(ctx.clips, bounds)
	if ctx.painter != nil {
		ctx.painter.SetClip(bounds)
	}
}

// ClearClip pops the most recent clip and restores the parent one, or
// removes clipping entirely when the stack empties. It runs in a defer
// after the matching SetClip, so an early return cannot leak a clip into
// the siblings drawn next.
func (ctx *drawContext) ClearClip() {
	if len(ctx.clips) == 0 {
		return
	}
	ctx.clips = ctx.clips[:len(ctx.clips)-1]
	if ctx.painter == nil {
		return
	}
	if len(ctx.clips) == 0 {
		ctx.painter.ClearClip()
	} else {
		ctx.painter.SetClip(ctx.clips[len(ctx.clips)-1])
	}
}

// Err returns the first paint error collected while drawing the frame.
func (ctx *drawContext) Err() error { return ctx.err }

func (ctx *drawContext) setErr(err error) {
	if err != nil && ctx.err == nil {
		ctx.err = err
	}
}

func resolvedComponentOpacity(c component) float64 {
	var resolved internalstyle.Resolved
	switch c := c.(type) {
	case *ColumnLayout:
		resolved = c.resolved
	case *RowLayout:
		resolved = c.resolved
	case *StackLayout:
		resolved = c.resolved
	case *ScrollLayout:
		resolved = c.resolved
	case *SpacerWidget:
		resolved = c.resolved
	case *LabelWidget:
		resolved = c.resolved
	case *ButtonWidget:
		resolved = c.resolved
	case *CheckboxWidget:
		resolved = c.resolved
	case *ImageWidget:
		resolved = c.resolved
	case *InputWidget:
		resolved = c.resolved
	case *TextAreaWidget:
		resolved = c.resolved
	case *menuBarWidget:
		resolved = c.resolved
	case *menuItemWidget:
		resolved = c.resolved
	}
	return defaultComponentStyle().merge(resolved).opacity
}

// drawResolvedComponent paints one component inside its own opacity scope.
// Leaf components use their styled path; containers recurse through the same
// helper, multiplying parent and child alpha without ordinary CSS inheritance.
func drawResolvedComponent(ctx *drawContext, c component) {
	restore := ctx.pushOpacity(resolvedComponentOpacity(c))
	defer restore()
	switch c := c.(type) {
	case *LabelWidget:
		c.drawStyled(ctx, c.resolved)
	case *ButtonWidget:
		c.drawStyled(ctx, c.resolved)
	case *CheckboxWidget:
		c.drawStyled(ctx, c.resolved)
	case *ImageWidget:
		c.drawStyled(ctx, c.resolved)
	case *InputWidget:
		c.drawStyled(ctx, c.resolved)
	case *TextAreaWidget:
		c.drawStyled(ctx, c.resolved)
	default:
		c.draw(ctx)
	}
}

// faceReceiver is implemented by components that rasterize text. The App
// owns, closes and rebuilds the face; a fresh face reaches every text
// component through setTreeFace.
type faceReceiver interface {
	setFace(*text.Face)
}

// setTreeFace installs the app-owned face into every text component of
// the tree, dropping their cached rasterizations so the new face's
// metrics take effect. The App re-runs this walk after every rebuild,
// before the next layout pass; text that is measured before the first
// injection (or after the face is closed) measures and draws as empty
// instead of failing a frame.
func setTreeFace(root component, face *text.Face) {
	root.walk(func(c component) {
		if f, ok := c.(faceReceiver); ok {
			f.setFace(face)
		}
	})
}

type clipboardReceiver interface {
	setClipboard(textClipboard)
}

func setTreeClipboard(root component, clipboard textClipboard) {
	root.walk(func(c component) {
		if receiver, ok := c.(clipboardReceiver); ok {
			receiver.setClipboard(clipboard)
		}
	})
}

// asComponent narrows a public Component to the internal contract. The
// sealed interface guarantees every non-nil Component is implemented by
// this package.
func asComponent(c Component) component {
	if c == nil {
		panic("ui: nil component")
	}
	internal, ok := c.(component)
	if !ok {
		panic("ui: component not implemented by this package")
	}
	return internal
}
