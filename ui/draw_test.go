package ui

import (
	"errors"
	"reflect"
	"testing"

	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/render/gl"
	"github.com/zhoudm1743/meltemi/internal/text"
)

// The frame painter of the window task is the existing GL renderer; the
// contract is shaped so it satisfies the package-internal painter
// interface directly — no adapter, and the text is rasterized exactly
// once, by the renderer itself. This assertion fails to compile if either
// side of the contract drifts. (The import is test-only: production ui
// files never reach the OpenGL backend.)
var _ painter = (*gl.Renderer)(nil)

// testFace loads the GoRegular 18px face a test stands in for the app
// owning: installed into the tree with setTreeFace and injected per frame
// into the drawContext, and closed when the test ends.
func testFace(t *testing.T) *text.Face {
	t.Helper()
	face, err := text.NewFace(goregular.TTF, 18)
	if err != nil {
		t.Fatalf("text.NewFace(GoRegular, 18) error = %v", err)
	}
	t.Cleanup(func() { face.Close() })
	return face
}

// paintOp is one recorded paint instruction.
type paintOp struct {
	kind    string // "rect", "text" or "image"
	rect    render.Rect
	at      driver.Point
	text    string
	color   render.Color
	face    *text.Face
	image   *render.Image
	opacity float64
}

// fakePainter records every instruction and replays queued errors, one
// per DrawText call, so tests can fix the exact paint stream and the
// first-error collection.
type fakePainter struct {
	ops      []paintOp
	textErrs []error
}

func (p *fakePainter) DrawRect(r render.Rect, c render.Color) {
	p.ops = append(p.ops, paintOp{kind: "rect", rect: r, color: c})
}

func (p *fakePainter) DrawText(value string, at driver.Point, c render.Color, face *text.Face) error {
	p.ops = append(p.ops, paintOp{kind: "text", at: at, text: value, color: c, face: face})
	if len(p.textErrs) == 0 {
		return nil
	}
	err := p.textErrs[0]
	if len(p.textErrs) > 1 {
		p.textErrs = p.textErrs[1:]
	}
	return err
}

func (p *fakePainter) DrawImage(source *render.Image, destination render.Rect, opacity float64) error {
	p.ops = append(p.ops, paintOp{kind: "image", rect: destination, image: source, opacity: opacity})
	return nil
}

func TestLabelDraw(t *testing.T) {
	face := testFace(t)
	label := Label("Hi")
	setTreeFace(label, face)
	laid := label.measure(Constraints{})
	label.arrange(rect(10, 20, laid.Width, laid.Height))

	p := &fakePainter{}
	ctx := &drawContext{face: face, painter: p}
	label.draw(ctx)

	// The text op carries the original string, the top-left point of the
	// arranged bounds, the theme label color and the component's face.
	want := []paintOp{{
		kind:  "text",
		at:    driver.Point{X: 10, Y: 20},
		text:  "Hi",
		color: defaultTheme.labelText,
		face:  face,
	}}
	if !reflect.DeepEqual(p.ops, want) {
		t.Errorf("paint ops = %v, want %v", p.ops, want)
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}

	// Empty text paints nothing, and a nil painter is tolerated so bare
	// drawContext values stay safe in layout-only tests.
	empty := Label("")
	setTreeFace(empty, face)
	empty.arrange(rect(0, 0, 0, 0))
	q := &fakePainter{}
	empty.draw(&drawContext{face: face, painter: q})
	if len(q.ops) != 0 {
		t.Errorf("empty label painted %d ops, want 0", len(q.ops))
	}
	label.draw(&drawContext{})
}

func TestButtonDrawStates(t *testing.T) {
	face := testFace(t)
	probe := Button("Go")
	setTreeFace(probe, face)
	bitmap := *probe.rasterize()

	bounds := rect(0, 0, 120, 36)
	// The expected text position, computed here by hand rather than
	// through the production helper, so a broken centering cannot agree
	// with itself.
	textAt := driver.Point{
		X: (bounds.Width - float64(bitmap.Width)) / 2,
		Y: (bounds.Height - float64(bitmap.Height)) / 2,
	}
	textOp := paintOp{kind: "text", at: textAt, text: "Go", color: defaultTheme.buttonText, face: face}
	thickness := focusBorderWidth
	focus := []render.Rect{
		{X: 0, Y: 0, Width: bounds.Width, Height: thickness},
		{X: 0, Y: bounds.Height - thickness, Width: bounds.Width, Height: thickness},
		{X: 0, Y: thickness, Width: thickness, Height: bounds.Height - 2*thickness},
		{X: bounds.Width - thickness, Y: thickness, Width: thickness, Height: bounds.Height - 2*thickness},
	}

	tests := []struct {
		name  string
		text  string
		state func(*ButtonWidget)
		want  []paintOp
	}{
		{
			name: "idle",
			want: []paintOp{
				{kind: "rect", rect: bounds, color: defaultTheme.buttonBackground},
				textOp,
			},
		},
		{
			name:  "hover",
			state: func(b *ButtonWidget) { b.hover = true },
			want: []paintOp{
				{kind: "rect", rect: bounds, color: defaultTheme.buttonBackgroundHover},
				textOp,
			},
		},
		{
			name:  "pressed",
			state: func(b *ButtonWidget) { b.pressed = true },
			want: []paintOp{
				{kind: "rect", rect: bounds, color: defaultTheme.buttonBackgroundActive},
				textOp,
			},
		},
		{
			name: "pressed beats hover",
			state: func(b *ButtonWidget) {
				b.hover = true
				b.pressed = true
			},
			want: []paintOp{
				{kind: "rect", rect: bounds, color: defaultTheme.buttonBackgroundActive},
				textOp,
			},
		},
		{
			name:  "disabled",
			state: func(b *ButtonWidget) { b.SetEnabled(false) },
			want: []paintOp{
				{kind: "rect", rect: bounds, color: defaultTheme.buttonBackgroundDisabled},
				{kind: "text", at: textAt, text: "Go", color: defaultTheme.buttonTextDisabled, face: face},
			},
		},
		{
			name: "disabled beats pressed",
			state: func(b *ButtonWidget) {
				b.pressed = true
				b.enabled = false
			},
			want: []paintOp{
				{kind: "rect", rect: bounds, color: defaultTheme.buttonBackgroundDisabled},
				{kind: "text", at: textAt, text: "Go", color: defaultTheme.buttonTextDisabled, face: face},
			},
		},
		{
			name:  "focused draws the border",
			state: func(b *ButtonWidget) { b.focused = true },
			want: []paintOp{
				{kind: "rect", rect: bounds, color: defaultTheme.buttonBackground},
				textOp,
				{kind: "rect", rect: focus[0], color: defaultTheme.focusBorder},
				{kind: "rect", rect: focus[1], color: defaultTheme.focusBorder},
				{kind: "rect", rect: focus[2], color: defaultTheme.focusBorder},
				{kind: "rect", rect: focus[3], color: defaultTheme.focusBorder},
			},
		},
		{
			name: "empty label paints no text",
			text: "",
			want: []paintOp{
				{kind: "rect", rect: bounds, color: defaultTheme.buttonBackground},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := tt.text
			if value == "" && tt.name != "empty label paints no text" {
				value = "Go"
			}
			button := Button(value)
			setTreeFace(button, face)
			button.arrange(bounds)
			if tt.state != nil {
				tt.state(button)
			}

			p := &fakePainter{}
			button.draw(&drawContext{face: face, painter: p})
			if !reflect.DeepEqual(p.ops, tt.want) {
				t.Errorf("paint ops = %v, want %v", p.ops, tt.want)
			}
		})
	}
}

func TestDrawContextCollectsFirstTextError(t *testing.T) {
	face := testFace(t)
	first := errors.New("first text failure")
	second := errors.New("second text failure")
	p := &fakePainter{textErrs: []error{first, second}}

	label := Label("Hi")
	setTreeFace(label, face)
	laid := label.measure(Constraints{})
	label.arrange(rect(0, 0, laid.Width, laid.Height))

	ctx := &drawContext{face: face, painter: p}
	label.draw(ctx)
	label.draw(ctx)

	if err := ctx.Err(); err != first {
		t.Errorf("Err() = %v, want the first error %v", err, first)
	}
	if len(p.ops) != 2 {
		t.Errorf("ops after errors = %d, want 2 (the first error must not stop the frame)", len(p.ops))
	}
}
