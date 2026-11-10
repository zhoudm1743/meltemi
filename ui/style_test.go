package ui

import (
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
	"github.com/zhoudm1743/meltemi/internal/text"
)

type stylePaintOp struct {
	kind    string
	rect    render.Rect
	radius  float64
	width   float64
	at      driver.Point
	text    string
	color   render.Color
	face    *text.Face
	image   *render.Image
	opacity float64
}

type stylePainter struct {
	ops      []stylePaintOp
	textErr  error
	imageErr error
}

func (p *stylePainter) DrawRect(rect render.Rect, color render.Color) {
	p.ops = append(p.ops, stylePaintOp{kind: "rect", rect: rect, color: color})
}

func (p *stylePainter) DrawRoundedRect(rect render.Rect, color render.Color, radius float64) {
	p.ops = append(p.ops, stylePaintOp{kind: "rounded", rect: rect, radius: radius, color: color})
}

func (p *stylePainter) DrawRoundedBorder(rect render.Rect, color render.Color, radius, width float64) {
	p.ops = append(p.ops, stylePaintOp{kind: "border", rect: rect, radius: radius, width: width, color: color})
}

func (p *stylePainter) DrawText(value string, at driver.Point, color render.Color, face *text.Face) error {
	p.ops = append(p.ops, stylePaintOp{kind: "text", at: at, text: value, color: color, face: face})
	return p.textErr
}

func (p *stylePainter) DrawImage(source *render.Image, destination render.Rect, opacity float64) error {
	p.ops = append(p.ops, stylePaintOp{kind: "image", rect: destination, image: source, opacity: opacity})
	return p.imageErr
}

func (p *stylePainter) SetClip(rect render.Rect) {
	p.ops = append(p.ops, stylePaintOp{kind: "clip", rect: rect})
}

func (p *stylePainter) ClearClip() {
	p.ops = append(p.ops, stylePaintOp{kind: "clearClip"})
}

func (p *fakePainter) DrawRoundedRect(rect render.Rect, color render.Color, radius float64) {
	if radius <= 0 {
		p.DrawRect(rect, color)
		return
	}
	p.ops = append(p.ops, paintOp{kind: "roundedRect", rect: rect, color: color})
}

func (p *fakePainter) DrawRoundedBorder(rect render.Rect, color render.Color, radius, width float64) {
	p.ops = append(p.ops, paintOp{kind: "border", rect: rect, color: color})
}

func (r *uiTestRenderer) DrawRoundedRect(render.Rect, render.Color, float64) {}

func (r *uiTestRenderer) DrawRoundedBorder(render.Rect, render.Color, float64, float64) {}

func resolved(values map[string]string) internalstyle.Resolved {
	return internalstyle.Resolved{Values: values}
}

func TestComponentStyleBorderRingTransparency(t *testing.T) {
	bounds := rect(10, 20, 80, 40)
	borderColor := render.Color{R: 1, A: 0.7}
	for _, backgroundAlpha := range []float32{0, 0.4} {
		for _, radius := range []float64{0, 5} {
			painter := &stylePainter{}
			background := render.Color{G: 1, A: backgroundAlpha}
			drawComponentBox(&drawContext{painter: painter}, bounds, componentStyle{
				background: background, borderColor: borderColor, borderWidth: 2, borderRadius: radius,
			})
			wantOps := 1
			if backgroundAlpha != 0 {
				wantOps = 2
			}
			if len(painter.ops) != wantOps {
				t.Errorf("alpha %v radius %v produced %d ops, want %d: %+v", backgroundAlpha, radius, len(painter.ops), wantOps, painter.ops)
				continue
			}
			if backgroundAlpha != 0 {
				fill := painter.ops[0]
				wantKind := "rect"
				if radius > 0 {
					wantKind = "rounded"
				}
				if fill.kind != wantKind || fill.rect != bounds || fill.color != background {
					t.Errorf("alpha %v radius %v background = %+v, want independent %+v %v", backgroundAlpha, radius, fill, wantKind, background)
				}
			}
			border := painter.ops[len(painter.ops)-1]
			if border.kind != "border" || border.rect != bounds || border.color != borderColor || border.radius != radius || border.width != 2 {
				t.Errorf("alpha %v radius %v border = %+v, want independent ring", backgroundAlpha, radius, border)
			}
		}
	}
}

func TestComponentStyleResolvedValues(t *testing.T) {
	got := defaultComponentStyle().merge(resolved(map[string]string{
		"background":    "#102030",
		"color":         "#405060",
		"border":        "2px solid #708090",
		"border-radius": "6px",
		"padding":       "1px 2px 3px 4px",
		"width":         "120px",
		"height":        "50px",
		"font-family":   "Custom",
		"font-size":     "22px",
	}))

	if want := (render.Color{R: 0x10p0 / 255, G: 0x20p0 / 255, B: 0x30p0 / 255, A: 1}); got.background != want {
		t.Errorf("background = %+v, want %+v", got.background, want)
	}
	if want := (render.Color{R: 0x40p0 / 255, G: 0x50p0 / 255, B: 0x60p0 / 255, A: 1}); got.text != want {
		t.Errorf("text = %+v, want %+v", got.text, want)
	}
	if want := (render.Color{R: 0x70p0 / 255, G: 0x80p0 / 255, B: 0x90p0 / 255, A: 1}); got.borderColor != want {
		t.Errorf("borderColor = %+v, want %+v", got.borderColor, want)
	}
	if got.borderWidth != 2 || got.borderRadius != 6 {
		t.Errorf("border width/radius = %v/%v, want 2/6", got.borderWidth, got.borderRadius)
	}
	if want := (edgePadding{top: 1, right: 2, bottom: 3, left: 4}); got.padding != want {
		t.Errorf("padding = %+v, want %+v", got.padding, want)
	}
	if got.width != 120 || got.height != 50 {
		t.Errorf("natural size = %vx%v, want 120x50", got.width, got.height)
	}
	if got.fontFamily != "Custom" || got.fontSize != 22 {
		t.Errorf("font = %q %v, want Custom 22", got.fontFamily, got.fontSize)
	}
}

func TestComponentStyleInvalidValuesPreserveDefaults(t *testing.T) {
	base := defaultComponentStyle()
	got := base.merge(resolved(map[string]string{
		"background":    "not-a-color",
		"border-width":  "-2px",
		"border-radius": "50%",
		"padding":       "1px 2px 3px 4px 5px",
		"width":         "wide",
		"height":        "-1px",
		"font-size":     "large",
	}))
	if got.background != base.background || got.borderWidth != base.borderWidth || got.borderRadius != base.borderRadius || got.padding != base.padding || got.width != base.width || got.height != base.height || got.fontSize != base.fontSize {
		t.Errorf("invalid resolved values changed defaults: got %+v, want %+v", got, base)
	}
}

func TestComponentStyleStatePriority(t *testing.T) {
	button := Button("Go")
	button.hover = true
	button.pressed = true
	button.focused = true
	button.enabled = false
	if got := buttonComponentStyle(button, internalstyle.Resolved{}); got.background != defaultTheme.buttonBackgroundDisabled || got.text != defaultTheme.buttonTextDisabled {
		t.Errorf("disabled did not beat pressed/hover: %+v", got)
	}
	button.enabled = true
	if got := buttonComponentStyle(button, internalstyle.Resolved{}); got.background != defaultTheme.buttonBackgroundActive {
		t.Errorf("pressed did not beat hover: background = %+v", got.background)
	}
	button.pressed = false
	if got := buttonComponentStyle(button, internalstyle.Resolved{}); got.background != defaultTheme.buttonBackgroundHover {
		t.Errorf("hover background = %+v, want %+v", got.background, defaultTheme.buttonBackgroundHover)
	}
}

func TestComponentStylePaddingAndNaturalSize(t *testing.T) {
	face := testFace(t)
	button := Button("OK")
	setTreeFace(button, face)
	bitmap := *button.rasterize()
	padding := resolved(map[string]string{"padding": "4px 8px 6px 10px"})
	wantWidth := float64(bitmap.Width) + 18
	wantHeight := max(buttonHeight, float64(bitmap.Height)+10)
	if got, want := button.measureStyled(Constraints{}, padding), (Size{Width: wantWidth, Height: wantHeight}); got != want {
		t.Errorf("Button measureStyled = %+v, want %+v", got, want)
	}
	explicitSize := resolved(map[string]string{"padding": "4px 8px 6px 10px", "width": "140px", "height": "48px"})
	if got, want := button.measureStyled(Constraints{}, explicitSize), (Size{Width: 140, Height: 48}); got != want {
		t.Errorf("Button explicit natural size = %+v, want %+v", got, want)
	}

	input := Input(nil)
	if got, want := input.measureStyled(Constraints{}, internalstyle.Resolved{}), (Size{Width: 200, Height: 36}); got != want {
		t.Errorf("Input default size = %+v, want %+v", got, want)
	}
	if got, want := input.measureStyled(Constraints{}, resolved(map[string]string{"height": "48px"})), (Size{Width: 200, Height: 48}); got != want {
		t.Errorf("Input CSS height = %+v, want %+v", got, want)
	}
	area := TextArea(nil)
	if got, want := area.measureStyled(Constraints{}, resolved(map[string]string{"width": "280px", "height": "90px"})), (Size{Width: 280, Height: 90}); got != want {
		t.Errorf("TextArea CSS size = %+v, want %+v", got, want)
	}

	label := Label("Hi")
	setTreeFace(label, face)
	labelBitmap := *label.rasterize()
	if got, want := label.measureStyled(Constraints{}, padding), (Size{Width: float64(labelBitmap.Width) + 18, Height: float64(labelBitmap.Height) + 10}); got != want {
		t.Errorf("Label padded size = %+v, want %+v", got, want)
	}
}

func TestComponentStyleInputPaddingNaturalSize(t *testing.T) {
	face := testFace(t)
	probe := Input(nil)
	probe.SetValue("A")
	setTreeFace(probe, face)
	bitmap := probe.textMetrics().bitmap
	tests := []struct {
		name         string
		values       map[string]string
		want         Size
		padding      edgePadding
		checkContent bool
	}{
		{name: "default", want: Size{Width: 200, Height: 36}},
		{name: "small padding keeps default lower bound", values: map[string]string{"padding": "4px"}, want: Size{Width: 200, Height: 36}},
		{
			name:         "large padding",
			values:       map[string]string{"padding": "120px"},
			want:         Size{Width: max(200, float64(bitmap.Width)+240), Height: max(36, float64(bitmap.Height)+240)},
			padding:      uniformPadding(120),
			checkContent: true,
		},
		{
			name:         "asymmetric padding",
			values:       map[string]string{"padding": "40px 80px 30px 120px"},
			want:         Size{Width: max(200, float64(bitmap.Width)+200), Height: max(36, float64(bitmap.Height)+70)},
			padding:      edgePadding{top: 40, right: 80, bottom: 30, left: 120},
			checkContent: true,
		},
		{
			name:   "explicit CSS size wins",
			values: map[string]string{"padding": "120px", "width": "90px", "height": "50px"},
			want:   Size{Width: 90, Height: 50},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := Input(nil)
			input.SetValue("A")
			setTreeFace(input, face)
			resolved := resolved(tt.values)
			got := input.measureStyled(Constraints{}, resolved)
			if got != tt.want {
				t.Errorf("Input natural size = %+v, want %+v", got, tt.want)
			}
			if !tt.checkContent {
				return
			}
			input.arrange(rect(0, 0, got.Width, got.Height))
			wantContent := rect(
				tt.padding.left,
				tt.padding.top,
				tt.want.Width-tt.padding.horizontal(),
				tt.want.Height-tt.padding.vertical(),
			)
			if content := input.contentRect(); content != wantContent {
				t.Errorf("Input content = %+v, want %+v", content, wantContent)
			}
		})
	}
}

func TestComponentStyleTextAreaPaddingNaturalSize(t *testing.T) {
	face := testFace(t)
	probe := TextArea(nil)
	probe.SetValue("first\nsecond")
	setTreeFace(probe, face)
	metrics := probe.textMetrics()
	contentWidth := 0.0
	for _, line := range metrics.lines {
		contentWidth = max(contentWidth, float64(line.bitmap.Width))
	}
	contentHeight := float64(len(metrics.lines)) * metrics.lineHeight
	tests := []struct {
		name         string
		values       map[string]string
		want         Size
		padding      edgePadding
		checkContent bool
	}{
		{name: "default", want: Size{Width: 320, Height: 160}},
		{name: "small padding keeps default lower bound", values: map[string]string{"padding": "4px"}, want: Size{Width: 320, Height: 160}},
		{
			name:         "large padding",
			values:       map[string]string{"padding": "180px"},
			want:         Size{Width: max(320, contentWidth+360), Height: max(160, contentHeight+360)},
			padding:      uniformPadding(180),
			checkContent: true,
		},
		{
			name:         "asymmetric padding",
			values:       map[string]string{"padding": "70px 140px 90px 180px"},
			want:         Size{Width: max(320, contentWidth+320), Height: max(160, contentHeight+160)},
			padding:      edgePadding{top: 70, right: 140, bottom: 90, left: 180},
			checkContent: true,
		},
		{
			name:   "explicit CSS size wins",
			values: map[string]string{"padding": "180px", "width": "90px", "height": "50px"},
			want:   Size{Width: 90, Height: 50},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			area := TextArea(nil)
			area.SetValue("first\nsecond")
			setTreeFace(area, face)
			resolved := resolved(tt.values)
			got := area.measureStyled(Constraints{}, resolved)
			if got != tt.want {
				t.Errorf("TextArea natural size = %+v, want %+v", got, tt.want)
			}
			if !tt.checkContent {
				return
			}
			area.arrange(rect(0, 0, got.Width, got.Height))
			wantContent := rect(
				tt.padding.left,
				tt.padding.top,
				tt.want.Width-tt.padding.horizontal(),
				tt.want.Height-tt.padding.vertical(),
			)
			if content := area.contentRect(); content != wantContent {
				t.Errorf("TextArea content = %+v, want %+v", content, wantContent)
			}
		})
	}
}

func TestComponentStyleLabelAndFocusDrawing(t *testing.T) {
	face := testFace(t)
	label := Label("Hi")
	setTreeFace(label, face)
	label.arrange(rect(10, 20, 80, 30))
	labelPainter := &stylePainter{}
	label.drawStyled(&drawContext{face: face, painter: labelPainter}, resolved(map[string]string{
		"background": "#ff0000", "color": "#00ff00", "border": "2px solid #0000ff", "border-radius": "6px", "padding": "3px",
	}))
	if len(labelPainter.ops) != 3 {
		t.Fatalf("Label paint ops = %d, want background, border and text: %+v", len(labelPainter.ops), labelPainter.ops)
	}
	if op := labelPainter.ops[0]; op.kind != "rounded" || op.rect != label.bounds || op.radius != 6 || op.color != (render.Color{R: 1, A: 1}) {
		t.Errorf("Label background = %+v", op)
	}
	if op := labelPainter.ops[1]; op.kind != "border" || op.rect != label.bounds || op.radius != 6 || op.width != 2 || op.color != (render.Color{B: 1, A: 1}) {
		t.Errorf("Label border ring = %+v", op)
	}
	if op := labelPainter.ops[2]; op.kind != "text" || op.at != (driver.Point{X: 13, Y: 23}) || op.color != (render.Color{G: 1, A: 1}) {
		t.Errorf("Label text = %+v", op)
	}

	button := Button("Go")
	setTreeFace(button, face)
	button.arrange(rect(0, 0, 100, 40))
	button.focused = true
	buttonPainter := &stylePainter{}
	button.drawStyled(&drawContext{face: face, painter: buttonPainter}, resolved(map[string]string{
		"border": "2px solid #ff0000", "border-radius": "5px",
	}))
	if len(buttonPainter.ops) < 2 || buttonPainter.ops[1].kind != "border" || buttonPainter.ops[1].color != (render.Color{R: 1, A: 1}) || buttonPainter.ops[1].radius != 5 || buttonPainter.ops[1].width != 2 {
		t.Fatalf("focused Button did not use its resolved border ring: %+v", buttonPainter.ops)
	}
	for _, op := range buttonPainter.ops {
		if op.color == defaultTheme.focusBorder {
			t.Errorf("resolved focus border was overpainted by the default focus ring: %+v", buttonPainter.ops)
		}
	}
}

func TestRoundedBorderAndInputClip(t *testing.T) {
	face := testFace(t)
	input := Input(nil)
	input.SetValue("A")
	setTreeFace(input, face)
	input.arrange(rect(10, 20, 120, 48))
	input.focused = true
	input.buffer.SelectAll()
	style := resolved(map[string]string{
		"background": "#ffffff", "color": "#102030", "border": "3px solid #405060", "border-radius": "6px", "padding": "10px",
	})
	painter := &stylePainter{}
	ctx := &drawContext{face: face, painter: painter}
	input.drawStyled(ctx, style)
	if err := ctx.Err(); err != nil {
		t.Fatalf("drawStyled error = %v", err)
	}

	content := rect(20, 30, 100, 28)
	if len(painter.ops) != 7 {
		t.Fatalf("Input paint ops = %d, want 7: %+v", len(painter.ops), painter.ops)
	}
	if op := painter.ops[0]; op.kind != "rounded" || op.rect != input.bounds || op.radius != 6 || op.color != (render.Color{R: 1, G: 1, B: 1, A: 1}) {
		t.Errorf("Input background = %+v", op)
	}
	if op := painter.ops[1]; op.kind != "border" || op.rect != input.bounds || op.radius != 6 || op.width != 3 {
		t.Errorf("Input border ring = %+v", op)
	}
	if op := painter.ops[2]; op.kind != "clip" || op.rect != content {
		t.Errorf("content clip = %+v, want %+v", op, content)
	}
	selection, textOp, caret := painter.ops[3], painter.ops[4], painter.ops[5]
	if selection.kind != "rect" || !rectInside(selection.rect, content) {
		t.Errorf("selection = %+v, want a rectangle inside %+v", selection, content)
	}
	if textOp.kind != "text" || textOp.color != (render.Color{R: 0x10p0 / 255, G: 0x20p0 / 255, B: 0x30p0 / 255, A: 1}) {
		t.Errorf("styled Input text = %+v", textOp)
	}
	if caret.kind != "rect" || !rectInside(caret.rect, content) || caret.color != textOp.color {
		t.Errorf("caret = %+v, want text-colored rectangle inside content", caret)
	}
	if painter.ops[6].kind != "clearClip" {
		t.Errorf("last op = %+v, want ClearClip", painter.ops[6])
	}

	errorPainter := &stylePainter{textErr: errors.New("styled text failure")}
	errorCtx := &drawContext{face: face, painter: errorPainter}
	input.drawStyled(errorCtx, style)
	if errorCtx.Err() == nil || errorCtx.Err().Error() != "styled text failure" {
		t.Errorf("drawStyled error = %v, want styled text failure", errorCtx.Err())
	}
	if len(errorPainter.ops) == 0 || errorPainter.ops[len(errorPainter.ops)-1].kind != "clearClip" {
		t.Errorf("clip was not restored after text error: %+v", errorPainter.ops)
	}
}

func TestComponentStyleTextAreaUsesResolvedStyle(t *testing.T) {
	face := testFace(t)
	area := TextArea(nil)
	area.SetValue("ab")
	setTreeFace(area, face)
	area.arrange(rect(0, 0, 120, 80))
	area.focused = true
	area.buffer.SelectAll()
	painter := &stylePainter{}
	textColor := render.Color{R: 0x20p0 / 255, G: 0x30p0 / 255, B: 0x40p0 / 255, A: 1}
	area.drawStyled(&drawContext{face: face, painter: painter}, resolved(map[string]string{
		"color": "#203040", "border": "2px solid #ff0000", "border-radius": "4px", "padding": "9px",
	}))
	content := rect(9, 9, 102, 62)
	clipFound, selectionFound, textFound, caretFound := false, false, false, false
	for _, op := range painter.ops {
		switch op.kind {
		case "clip":
			clipFound = op.rect == content
		case "text":
			textFound = op.color == textColor
		case "rect":
			if op.color == textColor && rectInside(op.rect, content) {
				caretFound = true
			}
			if op.color == inputSelectionColor && rectInside(op.rect, content) {
				selectionFound = true
			}
		}
	}
	if !clipFound || !selectionFound || !textFound || !caretFound {
		t.Errorf("TextArea styled drawing: clip=%v selection=%v text=%v caret=%v; ops=%+v", clipFound, selectionFound, textFound, caretFound, painter.ops)
	}
	if len(painter.ops) == 0 || painter.ops[len(painter.ops)-1].kind != "clearClip" {
		t.Errorf("TextArea did not restore clip: %+v", painter.ops)
	}
}

func TestComponentStyleInputVerticalAlignment(t *testing.T) {
	face := testFace(t)
	input := Input(nil)
	setTreeFace(input, face)
	input.SetValue("A")
	style := resolved(map[string]string{"padding": "10px 20px 30px 40px"})
	input.measureStyled(Constraints{}, style)
	input.arrange(rect(0, 0, 200, 80))
	input.focused = true
	painter := &stylePainter{}
	input.drawStyled(&drawContext{face: face, painter: painter}, style)

	content := input.contentRect()
	if want := rect(40, 10, 140, 40); content != want {
		t.Fatalf("Input asymmetric content = %+v, want %+v", content, want)
	}
	bitmap := input.textMetrics().bitmap
	wantAt := driver.Point{
		X: content.X - float64(bitmap.OriginX),
		Y: content.Y + (content.Height-float64(bitmap.Height))/2,
	}
	for _, op := range painter.ops {
		if op.kind == "text" {
			if op.at != wantAt {
				t.Errorf("Input text position = %+v, want content-centered %+v", op.at, wantAt)
			}
			return
		}
	}
	t.Fatalf("Input painted no text: %+v", painter.ops)
}

func TestComponentStyleInputScrollAndHitTest(t *testing.T) {
	face := testFace(t)
	input := Input(nil)
	setTreeFace(input, face)
	input.SetValue("abcdefghijklmnop")
	style := resolved(map[string]string{"padding": "0 40px"})
	input.measureStyled(Constraints{}, style)
	input.arrange(rect(0, 0, 200, 36))
	input.focused = true

	content := input.contentRect()
	if want := rect(40, 1, 120, 34); content != want {
		t.Fatalf("styled Input content = %+v, want %+v", content, want)
	}
	metrics := input.textMetrics()
	last := len(metrics.offsets) - 1
	wantScroll := max(0, metrics.offsets[last]-content.Width+1)
	if input.scrollX != wantScroll {
		t.Errorf("styled Input scrollX = %v, want %v", input.scrollX, wantScroll)
	}
	input.buffer.SelectAll()
	input.updateScroll()
	selection, ok := input.selectionRect()
	if !ok {
		t.Fatal("Input SelectAll produced no selection")
	}
	wantSelection := render.Rect{X: content.X - wantScroll, Y: content.Y, Width: metrics.offsets[last], Height: content.Height}
	if selection != wantSelection {
		t.Errorf("styled Input selection = %+v, want %+v", selection, wantSelection)
	}
	point := driver.Point{
		X: content.X + (metrics.offsets[last-1]+metrics.offsets[last])/2 - 0.25 - wantScroll,
		Y: content.Y + content.Height/2,
	}
	if got := input.runeIndexAt(point); got != last-1 {
		t.Errorf("styled Input hit test = %d, want rune %d", got, last-1)
	}
	input.drawStyled(&drawContext{face: face, painter: &stylePainter{}}, style)
	if input.scrollX != wantScroll {
		t.Errorf("drawStyled changed Input scrollX to %v, want %v", input.scrollX, wantScroll)
	}
}

func TestComponentStyleTextAreaScrollAndHitTest(t *testing.T) {
	face := testFace(t)
	area := TextArea(nil)
	setTreeFace(area, face)
	area.SetValue("zero\none\ntwo\nthree\nfour\nabcdefghijklmnop")
	style := resolved(map[string]string{
		"width": "180px", "height": "90px", "padding": "20px 30px 25px 40px", "border": "2px solid #405060",
	})
	area.measureStyled(Constraints{}, style)
	area.arrange(rect(0, 0, 180, 90))
	area.focused = true

	content := area.contentRect()
	if want := rect(40, 20, 110, 45); content != want {
		t.Fatalf("styled TextArea content = %+v, want %+v", content, want)
	}
	metrics := area.textMetrics()
	lastLine := len(metrics.lines) - 1
	visibleLines := max(1, int(content.Height/metrics.lineHeight))
	wantScrollY := float64(lastLine - visibleLines + 1)
	if area.scrollY != wantScrollY {
		t.Errorf("styled TextArea scrollY = %v, want %v", area.scrollY, wantScrollY)
	}
	lineMetrics := metrics.lines[lastLine]
	lastColumn := len(lineMetrics.offsets) - 1
	wantScrollX := max(0, lineMetrics.offsets[lastColumn]-content.Width+1)
	if area.scrollX != wantScrollX {
		t.Errorf("styled TextArea scrollX = %v, want %v", area.scrollX, wantScrollX)
	}
	wantCaret := render.Rect{
		X:     content.X + lineMetrics.offsets[lastColumn] - wantScrollX,
		Y:     content.Y + (float64(lastLine)-wantScrollY)*metrics.lineHeight,
		Width: 1, Height: metrics.lineHeight,
	}
	if got := area.caretRect(); got != wantCaret || !rectInside(got, content) {
		t.Errorf("styled TextArea caret = %+v, want %+v inside content", got, wantCaret)
	}
	point := driver.Point{
		X: content.X + (lineMetrics.offsets[lastColumn-1]+lineMetrics.offsets[lastColumn])/2 - 0.25 - wantScrollX,
		Y: wantCaret.Y + metrics.lineHeight/2,
	}
	wantRune := len([]rune(area.Value())) - 1
	if got := area.runeIndexAt(point); got != wantRune {
		t.Errorf("styled TextArea hit test = %d, want rune %d", got, wantRune)
	}
	lineStart := area.buffer.LineStart(lastLine)
	area.buffer.StartSelection(lineStart)
	area.buffer.ExtendSelection(len([]rune(area.Value())))
	area.updateScroll()
	selections := area.selectionRects()
	wantSelection := []render.Rect{{
		X: content.X - wantScrollX, Y: wantCaret.Y,
		Width: lineMetrics.offsets[lastColumn], Height: metrics.lineHeight,
	}}
	if len(selections) != 1 || selections[0] != wantSelection[0] {
		t.Errorf("styled TextArea selections = %+v, want %+v", selections, wantSelection)
	}
	painter := &stylePainter{}
	area.drawStyled(&drawContext{face: face, painter: painter}, style)
	if area.scrollX != wantScrollX || area.scrollY != wantScrollY {
		t.Errorf("drawStyled changed TextArea scroll to (%v,%v), want (%v,%v)", area.scrollX, area.scrollY, wantScrollX, wantScrollY)
	}
}

func TestComponentStyleColumnExplicitLayout(t *testing.T) {
	css := resolved(map[string]string{"gap": "12px", "padding": "1px 2px 3px 4px"})
	got := containerComponentStyle(6, 8, false, false, css)
	if got.gap != 12 || got.padding != (edgePadding{top: 1, right: 2, bottom: 3, left: 4}) {
		t.Errorf("Column CSS layout = gap %v padding %+v", got.gap, got.padding)
	}
	got = containerComponentStyle(6, 8, true, true, css)
	if got.gap != 6 || got.padding != (edgePadding{top: 8, right: 8, bottom: 8, left: 8}) {
		t.Errorf("explicit Column layout was overridden = gap %v padding %+v", got.gap, got.padding)
	}
}

func TestComponentStyleFocusBorderColorOnly(t *testing.T) {
	button := Button("")
	button.arrange(rect(0, 0, 100, 40))
	button.focused = true
	painter := &stylePainter{}
	button.drawStyled(&drawContext{painter: painter}, resolved(map[string]string{"border-color": "#ff0000"}))
	if len(painter.ops) != 5 {
		t.Fatalf("border-color-only focus produced %d ops, want background plus four ring strips: %+v", len(painter.ops), painter.ops)
	}
	for _, op := range painter.ops[1:] {
		if op.kind != "rect" || op.color != (render.Color{R: 1, A: 1}) {
			t.Errorf("focus ring op = %+v, want a red rectangle", op)
		}
	}
}

func TestComponentStyleRoundedFocusClipUsesDrawnBorder(t *testing.T) {
	input := Input(nil)
	input.SetValue("A")
	setTreeFace(input, testFace(t))
	input.arrange(rect(0, 0, 120, 36))
	input.focused = true
	painter := &stylePainter{}
	input.drawStyled(&drawContext{face: input.face, painter: painter}, resolved(map[string]string{"border-radius": "6px"}))
	wantClip := rect(8, 1.5, 104, 33)
	for _, op := range painter.ops {
		if op.kind == "clip" {
			if op.rect != wantClip {
				t.Errorf("rounded focus clip = %+v, want %+v", op.rect, wantClip)
			}
			return
		}
	}
	t.Fatalf("rounded focus Input emitted no clip: %+v", painter.ops)
}

func rectInside(inner, outer render.Rect) bool {
	return inner.X >= outer.X && inner.Y >= outer.Y && inner.X+inner.Width <= outer.X+outer.Width && inner.Y+inner.Height <= outer.Y+outer.Height
}

func resolveOpacityTestTree(root component, sheet *internalstyle.Sheet, inherited internalstyle.Resolved) {
	receiver := root.(resolvedStyleReceiver)
	current := sheet.Resolve(receiver.styleMeta(), inherited)
	receiver.setResolved(current)
	if container, ok := root.(childContainer); ok {
		for _, child := range container.childComponents() {
			resolveOpacityTestTree(child, sheet, current)
		}
	}
}

func TestOpacityParsingAndNestedDrawing(t *testing.T) {
	sheet, err := internalstyle.Parse(strings.NewReader(`
#zero { opacity: 0; }
#one { opacity: 1; }
#fraction { opacity: 0.25; }
#outer { opacity: .5; }
#button { opacity: .5; }
#picture { opacity: .5; }
`))
	if err != nil {
		t.Fatalf("Parse valid opacity values: %v", err)
	}
	for i, want := range []string{"0", "1", "0.25"} {
		if got := sheet.Rules[i].Properties[0].Value; got != want {
			t.Errorf("valid opacity %d = %q, want %q", i, got, want)
		}
	}

	face := testFace(t)
	button := Button("Go").ID("button")
	picture := Image(image.NewNRGBA(image.Rect(0, 0, 2, 2))).ID("picture")
	after := Label("after")
	root := Column(Row(button, picture), after).ID("outer")
	setTreeFace(root, face)
	resolveOpacityTestTree(root, sheet, unresolvedStyle())
	if _, inherited := after.resolved.Values["opacity"]; inherited {
		t.Error("opacity was inherited as an ordinary CSS property")
	}
	root.measure(Constraints{})
	root.arrange(rect(0, 0, 200, 100))

	textErr := errors.New("opacity text failure")
	painter := &stylePainter{textErr: textErr}
	ctx := &drawContext{face: face, painter: painter, opacity: 1}
	drawResolvedComponent(ctx, root)
	if ctx.Err() != textErr {
		t.Errorf("draw error = %v, want %v", ctx.Err(), textErr)
	}
	if ctx.opacity != 1 {
		t.Errorf("context opacity after root draw = %v, want restored 1", ctx.opacity)
	}
	if len(painter.ops) != 4 {
		t.Fatalf("paint ops = %d, want button rect/text, image and following label: %+v", len(painter.ops), painter.ops)
	}

	wantBackground := defaultTheme.buttonBackground
	wantBackground.A = 0.25
	if op := painter.ops[0]; op.kind != "rect" || op.color != wantBackground {
		t.Errorf("nested Button rect = %+v, want alpha 0.25 and color %+v", op, wantBackground)
	}
	wantButtonText := defaultTheme.buttonText
	wantButtonText.A = 0.25
	if op := painter.ops[1]; op.kind != "text" || op.color != wantButtonText {
		t.Errorf("nested Button text = %+v, want alpha 0.25 and color %+v", op, wantButtonText)
	}
	if op := painter.ops[2]; op.kind != "image" || op.opacity != 0.25 {
		t.Errorf("nested Image = %+v, want final opacity 0.25 applied once", op)
	}
	wantAfterText := defaultTheme.labelText
	wantAfterText.A = 0.5
	if op := painter.ops[3]; op.kind != "text" || op.color != wantAfterText {
		t.Errorf("following Label = %+v, want restored parent alpha 0.5 after a paint error", op)
	}
}

func assertComponentPaintOpacity(t *testing.T, c component, face *text.Face) {
	t.Helper()
	setTreeFace(c, face)
	size := c.measure(Constraints{})
	size.Width = max(size.Width, 20)
	size.Height = max(size.Height, 20)
	c.arrange(rect(0, 0, size.Width, size.Height))
	painter := &stylePainter{}
	ctx := &drawContext{face: face, painter: painter, opacity: 1}
	drawResolvedComponent(ctx, c)

	if _, ok := c.(*ImageWidget); ok {
		images := 0
		for _, op := range painter.ops {
			if op.kind == "image" {
				images++
				if op.opacity != 0.5 {
					t.Errorf("Image final opacity = %v, want 0.5", op.opacity)
				}
			}
		}
		if images != 1 {
			t.Errorf("Image paint calls = %d, want 1: %+v", images, painter.ops)
		}
		return
	}

	painted := false
	for _, op := range painter.ops {
		switch op.kind {
		case "rect", "rounded", "border", "text":
			painted = true
			if op.color.A != 0.5 {
				t.Errorf("%T %s alpha = %v, want 0.5: %+v", c, op.kind, op.color.A, op)
			}
		}
	}
	if !painted {
		t.Errorf("%T produced no paint output: %+v", c, painter.ops)
	}
}

func assertContainerPaintOpacity(t *testing.T, c component, face *text.Face) {
	t.Helper()
	firstFound := false
	c.walk(func(child component) {
		if label, ok := child.(*LabelWidget); ok && label.text == "first" {
			label.setResolved(resolved(map[string]string{"opacity": ".5"}))
			firstFound = true
		}
	})
	if !firstFound {
		t.Fatalf("%T has no first painting child", c)
	}
	setTreeFace(c, face)
	size := c.measure(Constraints{})
	size.Width = max(size.Width, 20)
	size.Height = max(size.Height, 20)
	c.arrange(rect(0, 0, size.Width, size.Height))
	painter := &stylePainter{}
	ctx := &drawContext{face: face, painter: painter, opacity: 1}
	drawResolvedComponent(ctx, c)

	wantAlpha := map[string]float32{"first": 0.25, "second": 0.5}
	for _, op := range painter.ops {
		want, ok := wantAlpha[op.text]
		if op.kind != "text" || !ok {
			continue
		}
		if op.color.A != want {
			t.Errorf("%T child %q alpha = %v, want %v: %+v", c, op.text, op.color.A, want, op)
		}
		delete(wantAlpha, op.text)
	}
	if len(wantAlpha) != 0 {
		t.Errorf("%T missing painting children %v: %+v", c, wantAlpha, painter.ops)
	}
	if ctx.opacity != 1 {
		t.Errorf("%T context opacity after draw = %v, want restored 1", c, ctx.opacity)
	}
}

func assertSpacerPaintFree(t *testing.T, spacer component, face *text.Face) {
	t.Helper()
	setTreeFace(spacer, face)
	spacer.measure(Constraints{})
	spacer.arrange(rect(0, 0, 20, 20))
	painter := &stylePainter{}
	ctx := &drawContext{face: face, painter: painter, opacity: 1}
	drawResolvedComponent(ctx, spacer)
	if len(painter.ops) != 0 {
		t.Errorf("Spacer painted with opacity metadata: %+v", painter.ops)
	}
	if ctx.opacity != 1 {
		t.Errorf("Spacer context opacity after draw = %v, want restored 1", ctx.opacity)
	}
}

func TestEveryComponentAcceptsOpacity(t *testing.T) {
	sheet, err := internalstyle.Parse(strings.NewReader(`
Column { opacity: .5; }
Row { opacity: .5; }
Stack { opacity: .5; }
Scroll { opacity: .5; }
Spacer { opacity: .5; }
Label { opacity: .5; }
Button { opacity: .5; }
Input { opacity: .5; }
Password { opacity: .5; }
TextArea { opacity: .5; }
Checkbox { opacity: .5; }
Image { opacity: .5; }
`))
	if err != nil {
		t.Fatalf("Parse component opacity rules: %v", err)
	}
	face := testFace(t)
	components := []component{
		Column(Label("first"), Label("second")),
		Row(Label("first"), Label("second")),
		Stack(Label("first"), Label("second")),
		Scroll(Column(Label("first"), Label("second"))),
		Spacer(),
		Label("label"), Button("button"), Input(nil), Password(nil), TextArea(nil),
		Checkbox("checkbox", nil), Image(image.NewNRGBA(image.Rect(0, 0, 2, 2))),
	}
	for _, c := range components {
		receiver := c.(resolvedStyleReceiver)
		t.Run(receiver.styleMeta().Type, func(t *testing.T) {
			got := sheet.Resolve(receiver.styleMeta(), resolved(map[string]string{"opacity": ".25"}))
			if got.Values["opacity"] != ".5" {
				t.Errorf("resolved opacity = %q, want own .5 instead of inherited .25", got.Values["opacity"])
			}
			receiver.setResolved(got)
			if opacity := resolvedComponentOpacity(c); opacity != 0.5 {
				t.Errorf("draw-time opacity = %v, want 0.5", opacity)
			}
			child := (&internalstyle.Sheet{}).Resolve(internalstyle.Meta{Type: "Label"}, got)
			if _, ok := child.Values["opacity"]; ok {
				t.Errorf("opacity leaked into child resolution: %v", child.Values)
			}
			switch c.(type) {
			case *ColumnLayout, *RowLayout, *StackLayout, *ScrollLayout:
				assertContainerPaintOpacity(t, c, face)
			case *SpacerWidget:
				assertSpacerPaintFree(t, c, face)
			case *LabelWidget, *ButtonWidget, *InputWidget, *TextAreaWidget, *CheckboxWidget, *ImageWidget:
				assertComponentPaintOpacity(t, c, face)
			}
		})
	}
}

func TestOpacityZeroAndZeroValueContext(t *testing.T) {
	face := testFace(t)
	label := Label("hidden")
	setTreeFace(label, face)
	label.arrange(rect(0, 0, 80, 30))
	picture := Image(image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	picture.arrange(rect(0, 0, 20, 20))

	label.setResolved(resolved(map[string]string{"opacity": "0", "background": "#ff0000"}))
	picture.setResolved(resolved(map[string]string{"opacity": "0"}))
	painter := &stylePainter{}
	ctx := &drawContext{face: face, painter: painter, opacity: 1}
	drawResolvedComponent(ctx, label)
	drawResolvedComponent(ctx, picture)
	if len(painter.ops) != 2 {
		t.Fatalf("zero-opacity paint ops = %d, want rect and text with image suppressed: %+v", len(painter.ops), painter.ops)
	}
	for i, wantKind := range []string{"rect", "text"} {
		op := painter.ops[i]
		if op.kind != wantKind || op.color.A != 0 {
			t.Errorf("zero-opacity op %d = %+v, want %s with alpha 0", i, op, wantKind)
		}
	}

	label.setResolved(resolved(map[string]string{"background": "#ff0000"}))
	picture.setResolved(unresolvedStyle())
	painter = &stylePainter{}
	ctx = &drawContext{face: face, painter: painter}
	drawResolvedComponent(ctx, label)
	drawResolvedComponent(ctx, picture)
	if len(painter.ops) != 3 {
		t.Fatalf("zero-value-context paint ops = %d, want rect, text and image: %+v", len(painter.ops), painter.ops)
	}
	for i, wantKind := range []string{"rect", "text"} {
		op := painter.ops[i]
		if op.kind != wantKind || op.color.A != 1 {
			t.Errorf("zero-value-context op %d = %+v, want %s with alpha 1", i, op, wantKind)
		}
	}
	if op := painter.ops[2]; op.kind != "image" || op.opacity != 1 {
		t.Errorf("zero-value-context image = %+v, want final opacity 1", op)
	}
}
