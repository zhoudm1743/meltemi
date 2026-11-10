package ui

import (
	"errors"
	"math"
	"testing"

	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

func TestTextAreaDrawLinesSelectionCaretAndClip(t *testing.T) {
	area := TextArea(nil)
	area.SetValue("ab\ncd")
	setTreeFace(area, testFace(t))
	area.arrange(rect(10, 20, 160, 80))
	area.focused = true
	area.buffer.StartSelection(1)
	area.buffer.ExtendSelection(4)
	area.updateScroll()

	painter := &fakePainter{}
	ctx := &drawContext{face: area.face, painter: painter}
	area.draw(ctx)
	if err := ctx.Err(); err != nil {
		t.Fatalf("draw error = %v", err)
	}
	content := area.contentRect()
	lineHeight := area.lineHeight()
	firstOffset := inputTestCaretOffset(t, goregular.TTF, "ab", 1)
	firstEnd := inputTestCaretOffset(t, goregular.TTF, "ab", 2)
	secondOffset := inputTestCaretOffset(t, goregular.TTF, "cd", 1)
	wantSelections := []render.Rect{
		{X: content.X + firstOffset, Y: content.Y, Width: firstEnd - firstOffset, Height: lineHeight},
		{X: content.X, Y: content.Y + lineHeight, Width: secondOffset, Height: lineHeight},
	}
	wantCaret := render.Rect{X: content.X + secondOffset, Y: content.Y + lineHeight, Width: 1, Height: lineHeight}
	selectionsFound := make([]bool, len(wantSelections))
	caretFound := false
	clipIndex, clearIndex := -1, -1
	textLines := []string{}
	for index, op := range painter.ops {
		switch op.kind {
		case "clip":
			if op.rect != content {
				t.Errorf("clip = %+v, want content %+v", op.rect, content)
			}
			clipIndex = index
		case "clearClip":
			clearIndex = index
		case "text":
			textLines = append(textLines, op.text)
		case "rect":
			for i, want := range wantSelections {
				if op.rect == want && op.color == inputSelectionColor {
					selectionsFound[i] = true
				}
			}
			if op.rect == wantCaret && op.color == inputCaretColor {
				caretFound = true
			}
		}
	}
	if len(textLines) != 2 || textLines[0] != "ab" || textLines[1] != "cd" {
		t.Errorf("painted lines = %q, want [ab cd]", textLines)
	}
	for i, found := range selectionsFound {
		if !found {
			t.Errorf("selection line %d not painted at %+v", i, wantSelections[i])
		}
	}
	if !caretFound {
		t.Errorf("caret not painted at %+v", wantCaret)
	}
	if clipIndex < 0 || clearIndex <= clipIndex {
		t.Fatalf("clip operations = clip %d, clear %d; want ordered pair", clipIndex, clearIndex)
	}
}

func TestTextAreaScrollingSharesTextAndCaretOrigin(t *testing.T) {
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
		t.Fatalf("test font is not discriminating: OriginX = %d", bitmap.OriginX)
	}
	area := TextArea(nil)
	setTreeFace(area, face)
	area.arrange(rect(10, 20, 30, 54))
	area.SetValue(value)
	area.focused = true
	area.buffer.SelectAll()
	area.updateScroll()
	content := area.contentRect()
	advance := inputTestCaretOffset(t, goitalic.TTF, value, 4)
	wantScroll := max(0.0, advance-content.Width+1)
	if wantScroll <= 0 || area.scrollX != wantScroll {
		t.Fatalf("scrollX = %v, want advance-based %v", area.scrollX, wantScroll)
	}

	painter := &fakePainter{}
	area.draw(&drawContext{face: face, painter: painter})
	wantTextAt := driver.Point{X: content.X - wantScroll - float64(bitmap.OriginX), Y: content.Y}
	wantSelection := render.Rect{X: content.X - wantScroll, Y: content.Y, Width: advance, Height: area.lineHeight()}
	wantCaret := render.Rect{X: content.X + advance - wantScroll, Y: content.Y, Width: 1, Height: area.lineHeight()}
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
	if !textFound || !selectionFound || !caretFound {
		t.Fatalf("origin paint: text=%v selection=%v caret=%v", textFound, selectionFound, caretFound)
	}
}

func TestTextAreaDrawClearsClipAfterTextError(t *testing.T) {
	area := TextArea(nil)
	area.SetValue("text")
	setTreeFace(area, testFace(t))
	area.arrange(rect(0, 0, 120, 60))
	paintErr := errors.New("fake textarea text failure")
	painter := &fakePainter{textErrs: []error{paintErr}}
	ctx := &drawContext{face: area.face, painter: painter}
	area.draw(ctx)
	if !errors.Is(ctx.Err(), paintErr) {
		t.Fatalf("draw error = %v, want %v", ctx.Err(), paintErr)
	}
	if len(painter.ops) == 0 || painter.ops[len(painter.ops)-1].kind != "clearClip" {
		t.Fatalf("last paint op = %+v, want ClearClip even after DrawText error", painter.ops)
	}
}

// findPaintOp reports whether ops contain a text op with the exact string,
// and returns it for further field assertions.
func findPaintOp(ops []paintOp, kind, value string) (paintOp, bool) {
	for _, op := range ops {
		if op.kind == kind && op.text == value {
			return op, true
		}
	}
	return paintOp{}, false
}

func TestComplexLabelAndTextAreaDrawing(t *testing.T) {
	const complexLabel = "中文 e\u0301 \u0645\u0631\u062D\u0628\u0627 🙂"

	t.Run("label draws complex text through the shared face", func(t *testing.T) {
		face := testFace(t)
		label := Label(complexLabel)
		setTreeFace(label, face)
		laid := label.measure(Constraints{})
		label.arrange(rect(10, 20, laid.Width, laid.Height))

		// The label's measured and rasterized geometry is exactly the
		// shared Layout bitmap geometry: one shaping stack for every text
		// component, no per-component measurement.
		reference := inputTestLayout(t, goregular.TTF, complexLabel)
		bitmap := label.rasterize()
		if bitmap.LogicalWidth != reference.Bitmap.LogicalWidth || bitmap.LogicalHeight != reference.Bitmap.LogicalHeight {
			t.Errorf("label bitmap geometry = %vx%v, want Layout logical %vx%v",
				bitmap.LogicalWidth, bitmap.LogicalHeight, reference.Bitmap.LogicalWidth, reference.Bitmap.LogicalHeight)
		}
		if bitmap.Width == 0 || bitmap.Height == 0 {
			t.Fatal("complex label rasterized to an empty bitmap")
		}

		painter := &fakePainter{}
		label.draw(&drawContext{face: face, painter: painter})
		op, found := findPaintOp(painter.ops, "text", complexLabel)
		if !found {
			t.Fatalf("complex label was not painted; ops=%v", painter.ops)
		}
		if op.face != face || op.color != defaultTheme.labelText {
			t.Errorf("label op face/color = %v/%v, want injected face and label color", op.face, op.color)
		}
	})

	t.Run("button and checkbox draw complex labels", func(t *testing.T) {
		face := testFace(t)

		button := Button("确定 🙂")
		setTreeFace(button, face)
		button.arrange(rect(0, 0, 200, 36))
		painter := &fakePainter{}
		button.draw(&drawContext{face: face, painter: painter})
		if _, found := findPaintOp(painter.ops, "text", "确定 🙂"); !found {
			t.Errorf("complex button label was not painted; ops=%v", painter.ops)
		}

		checkbox := Checkbox("\u0645\u0631\u062D\u0628\u0627", nil)
		setTreeFace(checkbox, face)
		checkbox.arrange(rect(0, 0, 200, 36))
		painter = &fakePainter{}
		checkbox.draw(&drawContext{face: face, painter: painter})
		if _, found := findPaintOp(painter.ops, "text", "\u0645\u0631\u062D\u0628\u0627"); !found {
			t.Errorf("complex checkbox label was not painted; ops=%v", painter.ops)
		}
	})

	t.Run("textarea paints every line and rtl selection spans correctly", func(t *testing.T) {
		face := testFace(t)
		area := TextArea(nil)
		area.SetValue("abc\n\u0645\u0631\u062D\u0628\u0627\nend")
		setTreeFace(area, face)
		area.arrange(rect(10, 20, 300, 120))
		area.focused = true

		reference := inputTestLayout(t, goregular.TTF, "\u0645\u0631\u062D\u0628\u0627")
		offsets := layoutOffsets(reference)
		// The Arabic letters shape into an RTL run whose caret offsets
		// decrease (plus the final alef in its own fallback run, per the
		// documented simplified bidi); the assertions stay inside the run.
		for boundary := 0; boundary < 3; boundary++ {
			if !(offsets[boundary] > offsets[boundary+1]) {
				t.Fatalf("reference RTL offsets decrease: offsets[%d] = %v, offsets[%d] = %v",
					boundary, offsets[boundary], boundary+1, offsets[boundary+1])
			}
		}

		area.buffer.StartSelection(4)
		area.buffer.ExtendSelection(8)
		area.updateScroll()

		painter := &fakePainter{}
		area.draw(&drawContext{face: face, painter: painter})

		content := area.contentRect()
		lineHeight := area.lineHeight()
		for line, value := range []string{"abc", "\u0645\u0631\u062D\u0628\u0627", "end"} {
			op, found := findPaintOp(painter.ops, "text", value)
			if !found {
				t.Errorf("line %d %q was not painted; ops=%v", line, value, painter.ops)
				continue
			}
			if wantY := content.Y + float64(line)*lineHeight; op.at.Y != wantY {
				t.Errorf("line %d painted at Y %v, want %v", line, op.at.Y, wantY)
			}
			if op.face != face {
				t.Errorf("line %d did not use the injected face", line)
			}
		}

		// Selecting columns [0, 4) of the Arabic line draws one rectangle
		// at the smaller visual offset with the full positive width — the
		// same min/absolute geometry the Input uses for RTL selections.
		startX, endX := offsets[0], offsets[4]
		wantSelection := render.Rect{
			X:      content.X + math.Min(startX, endX),
			Y:      content.Y + lineHeight,
			Width:  math.Abs(endX - startX),
			Height: lineHeight,
		}
		if wantSelection.Width <= 0 {
			t.Fatalf("test is not discriminating: RTL selection width %v", wantSelection.Width)
		}
		found := false
		for _, op := range painter.ops {
			if op.kind == "rect" && op.rect == wantSelection && op.color == inputSelectionColor {
				found = true
			}
		}
		if !found {
			t.Errorf("no RTL line selection rectangle %+v; ops=%v", wantSelection, painter.ops)
		}

		// The caret on the RTL line sits at the reference offset of its
		// column: the cursor is at rune 9, column 5 of line 1.
		painter = &fakePainter{}
		area.SetSelection(9, 9)
		area.draw(&drawContext{face: face, painter: painter})
		wantCaret := render.Rect{X: content.X + offsets[5], Y: content.Y + lineHeight, Width: 1, Height: lineHeight}
		found = false
		for _, op := range painter.ops {
			if op.kind == "rect" && op.rect == wantCaret && op.color == inputCaretColor {
				found = true
			}
		}
		if !found {
			t.Errorf("no RTL caret rectangle %+v; ops=%v", wantCaret, painter.ops)
		}
	})

	t.Run("textarea cluster selection and undo", func(t *testing.T) {
		area := TextArea(nil)
		area.SetValue("12e\u0301\n🙂ab")
		// The combining mark merges into one shaped cluster with e, so the
		// selection [2, 4) covers exactly that cluster; both ends snap to
		// boundaries.
		area.SetSelection(2, 4)
		if start, end, ok := area.Selection(); start != 2 || end != 4 || !ok {
			t.Fatalf("Selection() = (%d, %d, %v), want (2, 4, true)", start, end, ok)
		}
		area.Backspace()
		if got, want := area.Value(), "12\n🙂ab"; got != want {
			t.Fatalf("Value() after cluster Backspace = %q, want %q", got, want)
		}
		area.Undo()
		if got, want := area.Value(), "12e\u0301\n🙂ab"; got != want {
			t.Errorf("Value() after Undo = %q, want %q", got, want)
		}
		if start, end, ok := area.Selection(); start != 2 || end != 4 || !ok {
			t.Errorf("Selection() after Undo = (%d, %d, %v), want restored (2, 4, true)", start, end, ok)
		}
		if got := area.Cursor(); got != 4 {
			t.Errorf("Cursor() after Undo = %d, want 4", got)
		}
	})

	t.Run("placeholder uses the same text stack", func(t *testing.T) {
		face := testFace(t)
		area := TextArea(nil).Placeholder("请输入文本 🙂")
		setTreeFace(area, face)
		area.arrange(rect(0, 0, 300, 100))
		area.focused = true

		painter := &fakePainter{}
		area.draw(&drawContext{face: face, painter: painter})
		op, found := findPaintOp(painter.ops, "text", "请输入文本 🙂")
		if !found {
			t.Fatalf("placeholder was not painted; ops=%v", painter.ops)
		}
		if op.color != defaultTheme.inputPlaceholder || op.face != face {
			t.Errorf("placeholder op color/face = %v/%v, want placeholder color and injected face", op.color, op.face)
		}
	})
}
