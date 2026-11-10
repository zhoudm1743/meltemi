package ui

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

func (p *fakePainter) SetClip(bounds render.Rect) {
	p.ops = append(p.ops, paintOp{kind: "clip", rect: bounds})
}

func (p *fakePainter) ClearClip() {
	p.ops = append(p.ops, paintOp{kind: "clearClip"})
}

func (r *uiTestRenderer) SetClip(render.Rect) {}

func (r *uiTestRenderer) ClearClip() {}

// inputTestCaretOffset measures a caret boundary with an independent
// reference face built from the same data through the text engine. The
// component under test uses the same engine, so the offsets agree exactly;
// the x/image primitives are no longer an oracle because shaping (including
// system font fallback for CJK and emoji) now lives in HarfBuzz/FreeType.
func inputTestCaretOffset(t *testing.T, fontData []byte, value string, index int) float64 {
	t.Helper()
	reference, err := text.NewFace(fontData, 18)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer reference.Close()
	runes := []rune(value)
	if index < 0 || index > len(runes) {
		t.Fatalf("caret index %d outside [0,%d]", index, len(runes))
	}
	offsets, err := reference.CaretOffsets(value)
	if err != nil {
		t.Fatalf("reference CaretOffsets: %v", err)
	}
	return float64(offsets[index]) / 64
}

func TestInputDrawSelectionCaretAndFocus(t *testing.T) {
	input := Input(nil)
	input.SetValue("A中🙂")
	setTreeFace(input, testFace(t))
	input.arrange(rect(10, 20, 120, 36))
	input.focused = true
	input.buffer.StartSelection(1)
	input.buffer.ExtendSelection(3)
	input.updateScroll()

	painter := &fakePainter{}
	ctx := &drawContext{face: input.face, painter: painter}
	input.draw(ctx)
	if err := ctx.Err(); err != nil {
		t.Fatalf("draw error = %v", err)
	}

	content := input.contentRect()
	selectionStart := inputTestCaretOffset(t, goregular.TTF, "A中🙂", 1)
	selectionEnd := inputTestCaretOffset(t, goregular.TTF, "A中🙂", 3)
	wantSelection := render.Rect{
		X:      content.X + selectionStart,
		Y:      content.Y,
		Width:  selectionEnd - selectionStart,
		Height: content.Height,
	}
	wantCaret := render.Rect{
		X:      content.X + selectionEnd,
		Y:      content.Y,
		Width:  1,
		Height: content.Height,
	}
	clipIndex, clearIndex := -1, -1
	selectionFound, caretFound, textFound := false, false, false
	focusRectsAfterClear := 0
	for index, op := range painter.ops {
		switch op.kind {
		case "clip":
			if op.rect != content {
				t.Errorf("clip rect = %+v, want content %+v", op.rect, content)
			}
			clipIndex = index
		case "clearClip":
			clearIndex = index
		case "text":
			if op.text == "A中🙂" {
				textFound = true
				if op.face != input.face {
					t.Error("Input text did not use the injected face")
				}
			}
		case "rect":
			if op.color == defaultTheme.focusBorder && clearIndex >= 0 {
				focusRectsAfterClear++
			}
			if op.rect == wantSelection {
				selectionFound = true
			}
			if op.rect == wantCaret {
				caretFound = true
			}
		}
	}
	if clipIndex < 0 || clearIndex <= clipIndex {
		t.Fatalf("clip operations = clip %d, clear %d; want one ordered pair", clipIndex, clearIndex)
	}
	if !selectionFound {
		t.Errorf("no selection rectangle at independent advance+kern coordinates %+v", wantSelection)
	}
	if !caretFound {
		t.Errorf("no 1px caret at independent advance+kern coordinate %+v", wantCaret)
	}
	if !textFound {
		t.Error("the real Input text was not painted")
	}
	if focusRectsAfterClear != 4 {
		t.Errorf("focus border strips after ClearClip = %d, want 4", focusRectsAfterClear)
	}
}

func TestInputCaretIgnoresBitmapOverhang(t *testing.T) {
	face, err := text.NewFace(goitalic.TTF, 18)
	if err != nil {
		t.Fatalf("text.NewFace(GoItalic): %v", err)
	}
	t.Cleanup(func() { face.Close() })
	bitmap, err := face.Rasterize("f")
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	advance := inputTestCaretOffset(t, goitalic.TTF, "f", 1)
	if float64(bitmap.Width) == advance {
		t.Fatalf("test font is not discriminating: bitmap width and advance are both %v", advance)
	}

	clickInput := Input(nil)
	clickInput.SetValue("ff")
	setTreeFace(clickInput, face)
	clickInput.arrange(rect(0, 0, 120, 36))
	router := newEventRouter(clickInput)
	point := driver.Point{X: clickInput.contentRect().X + 3.5, Y: center(clickInput.bounds).Y}
	router.Handle(leftPress(point))
	router.Handle(leftRelease(point))
	if got := clickInput.buffer.Cursor(); got != 1 {
		t.Errorf("click between bitmap/advance midpoints put cursor at %d, want rune 1", got)
	}

	narrow := Input(nil)
	setTreeFace(narrow, face)
	narrow.arrange(rect(0, 0, 22, 36))
	narrow.SetValue("f")
	if narrow.scrollX != 0 {
		t.Errorf("advance-fitting text scrollX = %v, want 0 despite bitmap overhang", narrow.scrollX)
	}

	input := Input(nil)
	input.SetValue("f")
	setTreeFace(input, face)
	input.arrange(rect(0, 0, 120, 36))
	input.focused = true
	input.buffer.SelectAll()
	painter := &fakePainter{}
	input.draw(&drawContext{face: face, painter: painter})
	content := input.contentRect()
	wantCaret := render.Rect{X: content.X + advance, Y: content.Y, Width: 1, Height: content.Height}
	wantSelection := render.Rect{X: content.X, Y: content.Y, Width: advance, Height: content.Height}
	caretFound, selectionFound := false, false
	for _, op := range painter.ops {
		if op.kind != "rect" {
			continue
		}
		if op.rect == wantCaret && op.color == inputCaretColor {
			caretFound = true
		}
		if op.rect == wantSelection && op.color == inputSelectionColor {
			selectionFound = true
		}
	}
	if !caretFound {
		t.Errorf("caret not painted at advance %v (bitmap width %d)", advance, bitmap.Width)
	}
	if !selectionFound {
		t.Errorf("selection not painted at advance width %v (bitmap width %d)", advance, bitmap.Width)
	}
}

func TestInputNegativeLeftBearingSharesPenOrigin(t *testing.T) {
	face, err := text.NewFace(goitalic.TTF, 18)
	if err != nil {
		t.Fatalf("text.NewFace(GoItalic): %v", err)
	}
	t.Cleanup(func() { face.Close() })
	const value = "jjjj"
	bitmap, err := face.Rasterize(value)
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	if bitmap.OriginX <= 0 {
		t.Fatalf("test font is not discriminating: OriginX = %d, want positive left-overhang translation", bitmap.OriginX)
	}

	input := Input(nil)
	setTreeFace(input, face)
	input.arrange(rect(10, 20, 30, 36))
	input.SetValue(value)
	input.focused = true
	input.buffer.SelectAll()
	input.updateScroll()
	content := input.contentRect()
	advance := inputTestCaretOffset(t, goitalic.TTF, value, 4)
	wantScroll := max(0.0, advance-content.Width+1)
	if wantScroll <= 0 {
		t.Fatalf("test input is not discriminating: advance %v fits content width %v", advance, content.Width)
	}
	if input.scrollX != wantScroll {
		t.Errorf("scrollX = %v, want advance-based %v", input.scrollX, wantScroll)
	}

	painter := &fakePainter{}
	input.draw(&drawContext{face: face, painter: painter})
	wantTextAt := driver.Point{
		X: content.X - wantScroll - float64(bitmap.OriginX),
		Y: input.bounds.Y + (input.bounds.Height-float64(bitmap.Height))/2,
	}
	wantSelection := render.Rect{
		X: content.X - wantScroll, Y: content.Y,
		Width: advance, Height: content.Height,
	}
	wantCaret := render.Rect{
		X: content.X + advance - wantScroll, Y: content.Y,
		Width: 1, Height: content.Height,
	}
	textFound, selectionFound, caretFound := false, false, false
	for _, op := range painter.ops {
		switch op.kind {
		case "text":
			if op.text == value {
				textFound = true
				if op.at != wantTextAt {
					t.Errorf("text top-left = %+v, want pen origin minus OriginX: %+v", op.at, wantTextAt)
				}
			}
		case "rect":
			if op.rect == wantSelection && op.color == inputSelectionColor {
				selectionFound = true
			}
			if op.rect == wantCaret && op.color == inputCaretColor {
				caretFound = true
			}
		}
	}
	if !textFound {
		t.Error("negative-left-bearing text was not painted")
	}
	if !selectionFound {
		t.Errorf("selection not painted at pen-origin coordinates %+v", wantSelection)
	}
	if !caretFound {
		t.Errorf("caret not painted at pen-origin coordinates %+v", wantCaret)
	}

	third := inputTestCaretOffset(t, goitalic.TTF, value, 3)
	midpoint := (third + advance) / 2
	point := driver.Point{X: content.X + midpoint - wantScroll + 0.25, Y: center(input.bounds).Y}
	if point.X < content.X || point.X >= content.X+content.Width {
		t.Fatalf("test click X = %v outside visible content [%v,%v)", point.X, content.X, content.X+content.Width)
	}
	router := newEventRouter(input)
	router.Handle(leftPress(point))
	router.Handle(leftRelease(point))
	if got := input.buffer.Cursor(); got != 4 {
		t.Errorf("click just after the last advance midpoint put cursor at %d, want 4", got)
	}
}

func TestInputDrawDisabledOmitsCaretAndFocus(t *testing.T) {
	input := Input(nil)
	input.SetValue("disabled")
	setTreeFace(input, testFace(t))
	input.arrange(rect(0, 0, 120, 36))
	input.focused = true
	input.SetEnabled(false)

	painter := &fakePainter{}
	input.draw(&drawContext{face: input.face, painter: painter})
	for _, op := range painter.ops {
		if op.kind == "rect" && op.color == defaultTheme.focusBorder {
			t.Error("disabled Input painted a focus border")
		}
		if op.kind == "rect" && op.rect.Width == 1 && op.rect.Height == input.contentRect().Height && op.color == inputCaretColor {
			t.Error("disabled Input painted a caret")
		}
	}
}

func TestInputDrawClearsClipAfterTextError(t *testing.T) {
	input := Input(nil)
	input.SetValue("text")
	setTreeFace(input, testFace(t))
	input.arrange(rect(0, 0, 120, 36))
	paintErr := errors.New("fake text failure")
	painter := &fakePainter{textErrs: []error{paintErr}}
	ctx := &drawContext{face: input.face, painter: painter}
	input.draw(ctx)
	if !errors.Is(ctx.Err(), paintErr) {
		t.Fatalf("draw error = %v, want %v", ctx.Err(), paintErr)
	}
	if len(painter.ops) == 0 || painter.ops[len(painter.ops)-1].kind != "clearClip" {
		t.Fatalf("last paint op = %+v, want ClearClip even after DrawText error", painter.ops)
	}
}

func TestInputCaretPointStartsAtContent(t *testing.T) {
	input := Input(nil)
	setTreeFace(input, testFace(t))
	input.arrange(rect(10, 20, 120, 36))
	input.focused = true
	painter := &fakePainter{}
	input.draw(&drawContext{face: input.face, painter: painter})
	want := render.Rect{X: input.contentRect().X, Y: input.contentRect().Y, Width: 1, Height: input.contentRect().Height}
	for _, op := range painter.ops {
		if op.kind == "rect" && op.rect == want && op.color == inputCaretColor {
			return
		}
	}
	t.Fatalf("empty focused Input has no caret at %+v; ops=%v", want, painter.ops)
}

// inputTestLayout shapes value with an independent reference face built from
// the same font data through the public text engine. Complex-text assertions
// derive every expected coordinate from this layout, so the tests pin the
// component to the same shaping stack without hardcoding font metrics.
func inputTestLayout(t *testing.T, fontData []byte, value string) text.LineLayout {
	t.Helper()
	reference, err := text.NewFace(fontData, 18)
	if err != nil {
		t.Fatalf("text.NewFace: %v", err)
	}
	defer reference.Close()
	layout, err := reference.Layout(value)
	if err != nil {
		t.Fatalf("reference Layout(%q): %v", value, err)
	}
	return layout
}

func layoutOffsets(layout text.LineLayout) []float64 {
	offsets := make([]float64, len(layout.CaretOffsets))
	for index, offset := range layout.CaretOffsets {
		offsets[index] = float64(offset) / 64
	}
	return offsets
}

// clickInputPresses asks the input for the rune index at point.X (relative to
// the content origin, already scrolled) through a real event-router click.
func clickInputRuneIndex(input *InputWidget, point driver.Point) int {
	router := newEventRouter(input)
	router.Handle(leftPress(point))
	router.Handle(leftRelease(point))
	return input.buffer.Cursor()
}

func TestComplexClusterEditingAndHitTesting(t *testing.T) {
	t.Run("caret movement steps whole graphemes", func(t *testing.T) {
		buffer := newTextBuffer("e\u0301x\U0001F44D\U0001F3FD") // e, mark, x, emoji, modifier
		if got := buffer.Cursor(); got != 5 {
			t.Fatalf("Cursor() = %d, want 5 (end)", got)
		}
		buffer.Move(-1, false)
		if got := buffer.Cursor(); got != 3 {
			t.Errorf("Move(-1) over emoji+modifier = %d, want 3 (skipped 2 runes)", got)
		}
		buffer.Move(-1, false)
		if got := buffer.Cursor(); got != 2 {
			t.Errorf("Move(-1) over mark = %d, want 2 (skipped combining mark)", got)
		}
		buffer.Move(-1, false)
		if got := buffer.Cursor(); got != 0 {
			t.Errorf("Move(-1) to start = %d, want 0", got)
		}
		buffer.Move(1, false)
		buffer.Move(1, false)
		if got := buffer.Cursor(); got != 3 {
			t.Errorf("Move(+1)x2 = %d, want 3 (cluster-wise forward)", got)
		}
		buffer.Move(100, false)
		if got := buffer.Cursor(); got != 5 {
			t.Errorf("Move(+100) = %d, want 5 (clamped, no runaway)", got)
		}
		buffer.Move(-100, false)
		if got := buffer.Cursor(); got != 0 {
			t.Errorf("Move(-100) = %d, want 0 (clamped, no runaway)", got)
		}
	})

	t.Run("backspace and delete remove whole graphemes", func(t *testing.T) {
		buffer := newTextBuffer("e\u0301\U0001F44D\U0001F3FDx")
		buffer.Backspace()
		if got := buffer.Value(); got != "e\u0301\U0001F44D\U0001F3FD" {
			t.Errorf("Backspace value = %q, want %q (emoji+modifier deleted whole)", got, "e\u0301\U0001F44D\U0001F3FD")
		}
		buffer.Backspace()
		if got := buffer.Value(); got != "e\u0301" {
			t.Errorf("Backspace value = %q, want %q (mark cluster deleted whole)", got, "e\u0301")
		}

		buffer = newTextBuffer("e\u0301\U0001F44D\U0001F3FDx")
		buffer.Home(false)
		buffer.Delete()
		if got := buffer.Value(); got != "\U0001F44D\U0001F3FDx" {
			t.Errorf("Delete value = %q, want %q (e+mark removed whole)", got, "\U0001F44D\U0001F3FDx")
		}
		buffer.Home(false)
		buffer.Delete()
		if got := buffer.Value(); got != "x" {
			t.Errorf("Delete value = %q, want %q (emoji+modifier removed whole)", got, "x")
		}
	})

	t.Run("zwj sequence and flag stay atomic", func(t *testing.T) {
		// "a<family>b": stepping left from the end crosses b first, then
		// the whole five-rune ZWJ family in one step.
		buffer := newTextBuffer("a\U0001F468\u200D\U0001F469\u200D\U0001F467b")
		buffer.Move(-1, false)
		buffer.Move(-1, false)
		if got := buffer.Cursor(); got != 1 {
			t.Errorf("Move(-1)x2 over ZWJ family = %d, want 1 (whole sequence skipped)", got)
		}

		buffer = newTextBuffer("a\U0001F468\u200D\U0001F469\u200D\U0001F467b")
		buffer.Move(-1, false)
		buffer.Backspace()
		if got := buffer.Value(); got != "ab" {
			t.Errorf("Backspace over ZWJ family = %q, want %q (whole sequence removed)", got, "ab")
		}

		buffer = newTextBuffer("x\U0001F1E8\U0001F1F3y")
		buffer.Move(-1, false)
		buffer.Move(-1, false)
		if got := buffer.Cursor(); got != 1 {
			t.Errorf("Move(-1)x2 over flag = %d, want 1 (2 runes skipped)", got)
		}

		buffer = newTextBuffer("x\U0001F1E8\U0001F1F3y")
		buffer.Move(-1, false)
		buffer.Backspace()
		if got := buffer.Value(); got != "xy" {
			t.Errorf("Backspace over flag = %q, want %q (both indicators removed)", got, "xy")
		}
	})

	t.Run("pointer hit testing never lands inside a grapheme", func(t *testing.T) {
		const value = "e\u0301x\U0001F44D\U0001F3FD"
		input := Input(nil)
		input.SetValue(value)
		setTreeFace(input, testFace(t))
		input.arrange(rect(0, 0, 240, 36))

		reference := inputTestLayout(t, goregular.TTF, value)
		offsets := layoutOffsets(reference)
		content := input.contentRect()
		graphemes := graphemeBoundaries([]rune(value))

		// Clicking exactly on a caret boundary (or a quarter-gap toward the
		// next one) lands on that boundary — never on an interior rune index.
		for index, boundary := range graphemes[:len(graphemes)-1] {
			for _, fraction := range []float64{0, 0.25} {
				x := offsets[boundary]
				if index+1 < len(graphemes) {
					x += fraction * (offsets[graphemes[index+1]] - offsets[boundary])
				}
				point := driver.Point{X: content.X + x, Y: center(input.bounds).Y}
				if got := clickInputRuneIndex(input, point); got != boundary {
					t.Errorf("click at boundary %d (+%.2f gap) = index %d, want %d", boundary, fraction, got, boundary)
				}
			}
		}

		// A dense sweep across and beyond the text always reports a grapheme
		// boundary: hit-testing can never park the caret between e and its
		// combining mark or inside the emoji+modifier cluster.
		advance := offsets[len(offsets)-1]
		for x := -8.0; x <= advance+8; x += 0.5 {
			point := driver.Point{X: content.X + x, Y: center(input.bounds).Y}
			got := input.runeIndexAt(point)
			found := false
			for _, boundary := range graphemes {
				if got == boundary {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("runeIndexAt(x=%.1f) = %d, want one of grapheme boundaries %v", x, got, graphemes)
			}
		}
	})

	t.Run("rtl caret follows logical order with visual offsets", func(t *testing.T) {
		const value = "abc \u0645\u0631\u062D\u0628\u0627" // "abc مرحبا"
		input := Input(nil)
		input.SetValue(value)
		setTreeFace(input, testFace(t))
		input.arrange(rect(0, 0, 300, 36))
		input.focused = true

		reference := inputTestLayout(t, goregular.TTF, value)
		offsets := layoutOffsets(reference)
		// The Arabic run is shaped RTL: its caret offsets decrease from
		// the run's right edge to its left. (Across fallback-font runs the
		// line follows Task 1's documented simplified bidi — runs are laid
		// out in logical order — so the assertion stays inside one run.)
		for boundary := 4; boundary < 7; boundary++ {
			if !(offsets[boundary] > offsets[boundary+1]) {
				t.Fatalf("reference offsets decrease through the RTL run: offsets[%d] = %v, offsets[%d] = %v",
					boundary, offsets[boundary], boundary+1, offsets[boundary+1])
			}
		}

		content := input.contentRect()
		wantX := func(cursor int) float64 { return content.X + offsets[cursor] - input.scrollX }
		for cursor := 9; cursor > 0; cursor-- {
			input.handleEvent(driver.Event{Kind: driver.EventKey, Key: driver.KeyLeft, Action: driver.ActionPress})
			if got := input.buffer.Cursor(); got != cursor-1 {
				t.Fatalf("after Left: Cursor() = %d, want %d (logical order)", got, cursor-1)
			}
			if got := input.caretX(); got != wantX(cursor-1) {
				t.Errorf("after Left to %d: caretX = %v, want reference offset %v", cursor-1, got, wantX(cursor-1))
			}
		}
	})

	t.Run("shaped ligature cluster is not split", func(t *testing.T) {
		// "لا" (lam-alef) shapes into a single two-rune cluster with a
		// font that provides the ligature. The GoRegular+fontconfig stack
		// resolves the two letters to different fallback files on some
		// systems, so the subtest loads DejaVu — which covers Arabic — as
		// the primary face and skips when it is unavailable.
		const dejaVuSans = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
		data, err := os.ReadFile(dejaVuSans)
		if err != nil {
			t.Skipf("environment: read %s: %v", dejaVuSans, err)
		}
		reference := inputTestLayout(t, data, "\u0644\u0627")
		merged := len(reference.Clusters) == 1 && reference.Clusters[0].StartRune == 0 && reference.Clusters[0].EndRune == 2
		if !merged {
			t.Skipf("environment: %q shapes into %d clusters with DejaVu, not one merged lam-alef cluster", "لا", len(reference.Clusters))
		}

		face, err := text.NewFace(data, 18)
		if err != nil {
			t.Fatalf("text.NewFace(DejaVuSans): %v", err)
		}
		t.Cleanup(func() { face.Close() })

		input := Input(nil)
		input.SetValue("\u0644\u0627")
		setTreeFace(input, face)
		input.buffer.Move(-1, false)
		if got := input.buffer.Cursor(); got != 0 {
			t.Errorf("Move(-1) over lam-alef = %d, want 0 (shaped cluster is atomic)", got)
		}
		input.SetValue("\u0644\u0627")
		input.buffer.Backspace()
		if got := input.buffer.Value(); got != "" {
			t.Errorf("Backspace over lam-alef = %q, want empty (whole cluster removed)", got)
		}
	})
}

func TestComplexTextSelectionAndUndo(t *testing.T) {
	t.Run("selection extension snaps to cluster boundaries", func(t *testing.T) {
		buffer := newTextBuffer("e\u0301\U0001F44D\U0001F3FDx")
		buffer.StartSelection(0)
		buffer.ExtendSelection(3)
		if start, end, ok := buffer.Selection(); start != 0 || end != 2 || !ok {
			t.Errorf("Selection() after ExtendSelection(3) = (%d, %d, %v), want (0, 2, true) (snapped out of the emoji cluster)", start, end, ok)
		}
		buffer.ExtendSelection(4)
		if start, end, ok := buffer.Selection(); start != 0 || end != 4 || !ok {
			t.Errorf("Selection() after ExtendSelection(4) = (%d, %d, %v), want (0, 4, true)", start, end, ok)
		}
	})

	t.Run("typing through a cluster selection undoes in one step", func(t *testing.T) {
		buffer := newTextBuffer("e\u0301\U0001F44D\U0001F3FDx")
		buffer.StartSelection(0)
		buffer.ExtendSelection(4)
		buffer.Insert('a')
		if got := buffer.Value(); got != "ax" {
			t.Fatalf("Value() after replacing selection = %q, want %q", got, "ax")
		}
		if got := buffer.Cursor(); got != 1 {
			t.Fatalf("Cursor() after replacing selection = %d, want 1", got)
		}
		buffer.Undo()
		if got := buffer.Value(); got != "e\u0301\U0001F44D\U0001F3FDx" {
			t.Errorf("Value() after Undo = %q, want original", got)
		}
		if start, end, ok := buffer.Selection(); start != 0 || end != 4 || !ok {
			t.Errorf("Selection() after Undo = (%d, %d, %v), want the restored (0, 4, true)", start, end, ok)
		}
		buffer.Redo()
		if got := buffer.Value(); got != "ax" {
			t.Errorf("Value() after Redo = %q, want %q", got, "ax")
		}
	})

	t.Run("backspace through clusters needs one undo per cluster", func(t *testing.T) {
		buffer := newTextBuffer("e\u0301x")
		buffer.Backspace()
		if got := buffer.Value(); got != "e\u0301" {
			t.Fatalf("Backspace = %q, want %q", got, "e\u0301")
		}
		buffer.Backspace()
		if got := buffer.Value(); got != "" {
			t.Fatalf("Backspace = %q, want empty", got)
		}
		buffer.Undo()
		if got := buffer.Value(); got != "e\u0301" || buffer.Cursor() != 2 {
			t.Errorf("Undo = %q cursor %d, want %q cursor 2", got, buffer.Cursor(), "e\u0301")
		}
		buffer.Undo()
		if got := buffer.Value(); got != "e\u0301x" || buffer.Cursor() != 3 {
			t.Errorf("Undo = %q cursor %d, want %q cursor 3 (state before the x deletion)", got, buffer.Cursor(), "e\u0301x")
		}
		if buffer.Undo() {
			t.Error("third Undo() = true, want false (history exhausted)")
		}
	})

	t.Run("shift+arrows extend cluster by cluster", func(t *testing.T) {
		input := Input(nil)
		input.SetValue("e\u0301\U0001F44D\U0001F3FDx")
		setTreeFace(input, testFace(t))
		input.arrange(rect(0, 0, 240, 36))
		input.focused = true

		shiftLeft := driver.Event{Kind: driver.EventKey, Key: driver.KeyLeft, Action: driver.ActionPress, Mods: driver.ModShift}
		input.handleEvent(shiftLeft)
		input.handleEvent(shiftLeft)
		if start, end, ok := input.buffer.Selection(); start != 2 || end != 5 || !ok {
			t.Fatalf("Selection() after two Shift+Left = (%d, %d, %v), want (2, 5, true)", start, end, ok)
		}
		input.handleEvent(driver.Event{Kind: driver.EventKey, Key: driver.KeyBackspace, Action: driver.ActionPress})
		if got := input.buffer.Value(); got != "e\u0301" {
			t.Errorf("Value() after deleting selection = %q, want %q", got, "e\u0301")
		}
		if got := input.buffer.Cursor(); got != 2 {
			t.Errorf("Cursor() after deleting selection = %d, want 2", got)
		}
	})

	t.Run("rtl selection rectangle spans visual extents", func(t *testing.T) {
		const value = "abc \u0645\u0631\u062D\u0628\u0627"
		input := Input(nil)
		input.SetValue(value)
		setTreeFace(input, testFace(t))
		input.arrange(rect(0, 0, 300, 36))
		input.focused = true

		reference := inputTestLayout(t, goregular.TTF, value)
		offsets := layoutOffsets(reference)

		painter := &fakePainter{}
		content := input.contentRect()

		// Select inside the Arabic run, [4, 7): the rectangle must span
		// from the run's left edge with the full positive width even though
		// the logical offsets decrease.
		input.buffer.StartSelection(4)
		input.buffer.ExtendSelection(7)
		input.draw(&drawContext{face: input.face, painter: painter})
		rtlWidth := offsets[4] - offsets[7]
		wantRTL := render.Rect{
			X:      content.X + offsets[7],
			Y:      content.Y,
			Width:  rtlWidth,
			Height: content.Height,
		}
		if wantRTL.Width <= 0 {
			t.Fatalf("test is not discriminating: RTL selection width %v", wantRTL.Width)
		}
		found := false
		for _, op := range painter.ops {
			if op.kind == "rect" && op.rect == wantRTL && op.color == inputSelectionColor {
				found = true
			}
		}
		if !found {
			t.Errorf("no RTL selection rectangle %+v (offsets[4]=%v > offsets[9]=%v)", wantRTL, offsets[4], offsets[9])
		}

		painter = &fakePainter{}
		input.buffer.StartSelection(0)
		input.buffer.ExtendSelection(4)
		input.draw(&drawContext{face: input.face, painter: painter})
		wantLTR := render.Rect{X: content.X + offsets[0], Y: content.Y, Width: offsets[4] - offsets[0], Height: content.Height}
		found = false
		for _, op := range painter.ops {
			if op.kind == "rect" && op.rect == wantLTR && op.color == inputSelectionColor {
				found = true
			}
		}
		if !found {
			t.Errorf("no LTR selection rectangle %+v", wantLTR)
		}
	})
}

// TestPureRTLNarrowInputKeepsCaretVisible is the regression for the
// logical-start caret of a pure RTL line in a box narrower than the text:
// the scrollable extent must come from the visual caret extents (the
// maximum offset), not offsets[len-1], which is 0 for a pure RTL line.
func TestPureRTLNarrowInputKeepsCaretVisible(t *testing.T) {
	// "مرح" shapes as a single RTL run here: every rune resolves to one
	// fallback font, so offsets[len-1] is 0 — the exact case where an
	// offsets[len-1]-based scroll extent leaves the logical-start caret
	// clipped. Strings whose runes split across fallback fonts have a
	// non-zero final offset and skip the guard below.
	const value = "\u0645\u0631\u062D" // "مرح"
	input := Input(nil)
	input.SetValue(value)
	setTreeFace(input, testFace(t))
	input.arrange(rect(0, 0, 30, 36))
	input.focused = true

	reference := inputTestLayout(t, goregular.TTF, value)
	offsets := layoutOffsets(reference)
	extent := 0.0
	for _, offset := range offsets {
		extent = max(extent, offset)
	}
	content := input.contentRect()
	if offsets[len(offsets)-1] != 0 {
		t.Skipf("environment: %q final offset = %v, want 0 for a single-run pure RTL line", value, offsets[len(offsets)-1])
	}
	if extent <= content.Width || offsets[0] <= content.Width {
		t.Skipf("environment: RTL extent %v and start caret %v must both exceed content width %v", extent, offsets[0], content.Width)
	}

	input.buffer.Home(false) // logical start: caret sits at the right edge
	input.updateScroll()

	wantScroll := offsets[0] - content.Width + 1
	if input.scrollX != wantScroll {
		t.Errorf("scrollX = %v, want %v (start caret kept visible, extent-based)", input.scrollX, wantScroll)
	}
	if got := input.caretX() - content.X; got != content.Width-1 {
		t.Errorf("caret offset inside content = %v, want right edge %v", got, content.Width-1)
	}
}

func TestPureRTLNarrowTextAreaKeepsCaretVisible(t *testing.T) {
	// See the Input variant: a single-font-run pure RTL line, where the
	// final caret offset is 0.
	const value = "\u0645\u0631\u062D"
	area := TextArea(nil)
	area.SetValue(value)
	setTreeFace(area, testFace(t))
	area.arrange(rect(0, 0, 30, 80))
	area.focused = true

	reference := inputTestLayout(t, goregular.TTF, value)
	offsets := layoutOffsets(reference)
	extent := 0.0
	for _, offset := range offsets {
		extent = max(extent, offset)
	}
	content := area.contentRect()
	if offsets[len(offsets)-1] != 0 {
		t.Skipf("environment: %q final offset = %v, want 0 for a single-run pure RTL line", value, offsets[len(offsets)-1])
	}
	if extent <= content.Width || offsets[0] <= content.Width {
		t.Skipf("environment: RTL extent %v and start caret %v must both exceed content width %v", extent, offsets[0], content.Width)
	}

	area.SetSelection(0, 0) // logical start of the RTL line
	area.updateScroll()

	wantScroll := offsets[0] - content.Width + 1
	if area.scrollX != wantScroll {
		t.Errorf("scrollX = %v, want %v (start caret kept visible, extent-based)", area.scrollX, wantScroll)
	}
	caret := area.caretRect()
	if got := caret.X - content.X; got != content.Width-1 {
		t.Errorf("caret offset inside content = %v, want right edge %v", got, content.Width-1)
	}
}
