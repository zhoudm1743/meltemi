package ui

import (
	"reflect"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

func scrollWheelEvent(p driver.Point, dx, dy float64) driver.Event {
	return driver.Event{Kind: driver.EventScroll, Position: p, ScrollX: dx, ScrollY: dy}
}

func TestScrollMeasuresContentAndClampsOffset(t *testing.T) {
	content := fake("content", 100, 240)
	scroll := Scroll(content)

	// Unbounded constraints: the viewport is the content's natural size, so
	// there is nothing to scroll yet.
	if got, want := scroll.measure(Constraints{}), size(100, 240); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	if got, want := content.constraintLog[0], (Constraints{}); got != want {
		t.Errorf("content constraints = %v, want %v (content measures at its natural size)", got, want)
	}

	// Bounded constraints decide the viewport; the content keeps its
	// natural size instead of being squeezed into the viewport.
	if got, want := scroll.measure(Constraints{Max: size(80, 100)}), size(80, 100); got != want {
		t.Errorf("bounded measure = %v, want %v", got, want)
	}
	if got, want := content.size, size(100, 240); got != want {
		t.Errorf("content size = %v, want %v (natural size kept)", got, want)
	}

	scroll.arrange(rect(0, 0, 80, 100))
	if got, want := content.bounds, rect(0, 0, 100, 240); got != want {
		t.Errorf("content bounds = %v, want %v", got, want)
	}

	// Negative offsets clamp to zero. The offset range per axis is
	// [0, contentSize-viewportSize] = [0,20] x [0,140] here.
	scroll.SetOffset(-10, -20)
	if x, y := scroll.Offset(); x != 0 || y != 0 {
		t.Errorf("Offset after negative SetOffset = (%v, %v), want (0, 0)", x, y)
	}

	// arrange applies the offset by translating the content rect in the
	// opposite direction.
	scroll.SetOffset(9, 139)
	scroll.arrange(rect(0, 0, 80, 100))
	if got, want := content.bounds, rect(-9, -139, 100, 240); got != want {
		t.Errorf("translated content bounds = %v, want %v", got, want)
	}

	// Beyond the range clamps to the far edge.
	scroll.SetOffset(1000, 1000)
	if x, y := scroll.Offset(); x != 20 || y != 140 {
		t.Errorf("Offset after huge SetOffset = (%v, %v), want (20, 140)", x, y)
	}

	// Growing the viewport re-clamps a stale offset: the vertical range
	// shrinks from 140 to 240-200 = 40.
	scroll.measure(Constraints{Max: size(80, 200)})
	if x, y := scroll.Offset(); x != 20 || y != 40 {
		t.Errorf("Offset after viewport grew = (%v, %v), want (20, 40)", x, y)
	}

	// A viewport at least as large as the content leaves nothing to scroll.
	scroll.measure(Constraints{Max: size(120, 300)})
	if got, want := scroll.measure(Constraints{}), size(100, 240); got != want {
		t.Errorf("re-measured natural size = %v, want %v", got, want)
	}
	if x, y := scroll.Offset(); x != 0 || y != 0 {
		t.Errorf("Offset after viewport covered the content = (%v, %v), want (0, 0)", x, y)
	}
}

func TestScrollMeasureIncludesPaddingInNaturalOuterSize(t *testing.T) {
	for _, test := range []struct {
		name        string
		constraints Constraints
	}{
		{name: "unbounded", constraints: Constraints{}},
		{name: "ample bounds", constraints: Constraints{Max: size(200, 300)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := fake("content", 100, 200)
			scroll := Scroll(content).Padding(10)

			measured := scroll.measure(test.constraints)
			if want := size(120, 220); measured != want {
				t.Errorf("measure = %v, want natural content plus padding %v", measured, want)
			}
			scroll.arrange(rect(3, 4, measured.Width, measured.Height))
			if got, want := content.bounds, rect(13, 14, 100, 200); got != want {
				t.Errorf("content bounds = %v, want padded origin %v", got, want)
			}
			scroll.SetOffset(1000, 1000)
			if x, y := scroll.Offset(); x != 0 || y != 0 {
				t.Errorf("maximum offset = (%v, %v), want (0, 0) when padded content fits", x, y)
			}
		})
	}
}

func TestScrollMeasureClampsPaddedOuterSize(t *testing.T) {
	content := fake("content", 100, 200)
	scroll := Scroll(content).Padding(10)

	measured := scroll.measure(Constraints{Max: size(110, 180)})
	if want := size(110, 180); measured != want {
		t.Errorf("measure = %v, want padded outer size clamped to %v", measured, want)
	}
	scroll.arrange(rect(3, 4, measured.Width, measured.Height))
	if got, want := content.bounds, rect(13, 14, 100, 200); got != want {
		t.Errorf("content bounds = %v, want padded origin %v", got, want)
	}
	scroll.SetOffset(1000, 1000)
	if x, y := scroll.Offset(); x != 10 || y != 40 {
		t.Errorf("maximum offset = (%v, %v), want (10, 40) from natural content minus 90x160 viewport", x, y)
	}
}

func TestScrollClipsAndHitTestsViewport(t *testing.T) {
	// The draw pass restricts the children to the viewport with a clip that
	// is restored afterwards, and the hit test keeps content outside the
	// viewport from receiving pointer events.
	first := fake("first", 60, 40)
	second := fake("second", 60, 40)
	content := Column(first, second).Gap(10)
	scroll := Scroll(content)
	scroll.measure(Constraints{Max: size(60, 50)})
	scroll.arrange(rect(0, 0, 60, 50))

	// Both children keep their content-sized rects; the clip, not the
	// arrange, hides the part below the fold.
	if got, want := second.bounds, rect(0, 50, 60, 40); got != want {
		t.Errorf("second bounds = %v, want %v", got, want)
	}

	var log []drawRecord
	first.log = &log
	second.log = &log
	p := &fakePainter{}
	scroll.draw(&drawContext{painter: p})

	wantOps := []paintOp{
		{kind: "clip", rect: rect(0, 0, 60, 50)},
		{kind: "clearClip"},
	}
	if !reflect.DeepEqual(p.ops, wantOps) {
		t.Errorf("paint ops = %v, want %v", p.ops, wantOps)
	}
	wantLog := []drawRecord{
		{"first", rect(0, 0, 60, 40)},
		{"second", rect(0, 50, 60, 40)},
	}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("draw log = %v, want %v", log, wantLog)
	}

	// Hit testing: a button below the fold is inside its own arranged
	// bounds but outside the viewport, so the scroll gate must keep it from
	// receiving pointer events.
	visibleClicks, hiddenClicks := 0, 0
	visible := Button("visible").OnClick(func() { visibleClicks++ })
	hidden := Button("hidden").OnClick(func() { hiddenClicks++ })
	buttons := Column(visible, hidden).Gap(10)
	setTreeFace(buttons, testFace(t))
	natural := buttons.measure(Constraints{})
	hitScroll := Scroll(buttons)
	hitScroll.measure(Constraints{Max: size(natural.Width, natural.Height/2)})
	hitScroll.arrange(rect(0, 0, natural.Width, natural.Height/2))
	router := newEventRouter(hitScroll)

	// The hidden button sits below the viewport: its center is by
	// construction lower than the viewport height.
	pHidden := center(hidden.bounds)
	if pHidden.Y < natural.Height/2 {
		t.Fatalf("test setup: hidden button center %v should lie below the %v viewport", pHidden, natural.Height/2)
	}
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: pHidden})
	router.Handle(leftPress(pHidden))
	router.Handle(leftRelease(pHidden))
	if hiddenClicks != 0 || hidden.hover || hidden.pressed {
		t.Errorf("hidden button received events: clicks=%d hover=%v pressed=%v, want all zero", hiddenClicks, hidden.hover, hidden.pressed)
	}

	pVisible := center(visible.bounds)
	router.Handle(driver.Event{Kind: driver.EventMouseMove, Position: pVisible})
	router.Handle(leftPress(pVisible))
	router.Handle(leftRelease(pVisible))
	if visibleClicks != 1 {
		t.Errorf("visible clicks = %d, want 1 (inside the viewport the button still receives events)", visibleClicks)
	}
}

func TestScrollNestedClipsIntersectAndRestore(t *testing.T) {
	// An inner scroll whose viewport extends past the outer one clips to
	// the intersection, and the outer clip is restored when the inner draw
	// finishes — siblings drawn afterwards stay inside the outer viewport.
	innerContent := fake("inner-content", 80, 200)
	inner := Scroll(innerContent)
	outer := Scroll(inner)
	outer.measure(Constraints{Max: size(100, 100)})
	outer.SetOffset(0, 40)
	outer.arrange(rect(0, 0, 100, 100))

	if got, want := innerContent.bounds, rect(0, -40, 80, 200); got != want {
		t.Fatalf("inner content bounds = %v, want %v", got, want)
	}

	var log []drawRecord
	innerContent.log = &log
	p := &fakePainter{}
	outer.draw(&drawContext{painter: p})

	// The inner viewport (0,-40,80,200) is intersected with the outer clip
	// (0,0,100,100); the inner ClearClip restores the outer clip instead of
	// dropping clipping entirely.
	wantOps := []paintOp{
		{kind: "clip", rect: rect(0, 0, 100, 100)},
		{kind: "clip", rect: rect(0, 0, 80, 100)},
		{kind: "clip", rect: rect(0, 0, 100, 100)},
		{kind: "clearClip"},
	}
	if !reflect.DeepEqual(p.ops, wantOps) {
		t.Errorf("paint ops = %v, want %v", p.ops, wantOps)
	}
	wantLog := []drawRecord{{"inner-content", rect(0, -40, 80, 200)}}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("draw log = %v, want %v", log, wantLog)
	}
}

func TestScrollWheelAtLimits(t *testing.T) {
	content := fake("content", 300, 240)
	scroll := Scroll(content)
	scroll.measure(Constraints{Max: size(100, 80)})
	scroll.arrange(rect(0, 0, 100, 80))
	router := newEventRouter(scroll)
	inside := driver.Point{X: 50, Y: 40}
	wheel := func(p driver.Point, dx, dy float64) (bool, bool) {
		return router.Handle(scrollWheelEvent(p, dx, dy))
	}

	// Wheel up (positive delta) at the top edge: clamped, nothing changed,
	// and the event is left unhandled so an enclosing scroll could take it.
	if handled, redraw := wheel(inside, 0, 1); handled || redraw {
		t.Errorf("wheel up at top = (%v, %v), want (false, false)", handled, redraw)
	}

	// Vertical: one wheel line is the fixed logical step of 40. A negative
	// delta (wheel down) reveals content below; a positive delta moves back.
	if handled, redraw := wheel(inside, 0, -1); !handled || !redraw {
		t.Errorf("wheel down = (%v, %v), want (true, true)", handled, redraw)
	}
	if x, y := scroll.Offset(); x != 0 || y != 40 {
		t.Errorf("Offset after wheel down = (%v, %v), want (0, 40)", x, y)
	}
	if handled, _ := wheel(inside, 0, 1); !handled {
		t.Error("wheel up after wheel down must be handled")
	}
	if x, y := scroll.Offset(); x != 0 || y != 0 {
		t.Errorf("Offset after wheel up = (%v, %v), want (0, 0)", x, y)
	}

	// Horizontal: a positive delta reveals content on the right, a negative
	// delta moves back.
	if handled, _ := wheel(inside, 1, 0); !handled {
		t.Error("wheel right must be handled")
	}
	if x, y := scroll.Offset(); x != 40 || y != 0 {
		t.Errorf("Offset after wheel right = (%v, %v), want (40, 0)", x, y)
	}
	if handled, _ := wheel(inside, -1, 0); !handled {
		t.Error("wheel left must be handled")
	}
	if x, y := scroll.Offset(); x != 0 || y != 0 {
		t.Errorf("Offset after wheel left = (%v, %v), want (0, 0)", x, y)
	}

	// A large delta clamps to the far edge (160 = 240-80) and still
	// reports the change; another wheel down at the edge is unhandled.
	if handled, _ := wheel(inside, 0, -10); !handled {
		t.Error("wheel down into the far edge must be handled")
	}
	if x, y := scroll.Offset(); x != 0 || y != 160 {
		t.Errorf("Offset at far edge = (%v, %v), want (0, 160)", x, y)
	}
	if handled, redraw := wheel(inside, 0, -1); handled || redraw {
		t.Errorf("wheel down at far edge = (%v, %v), want (false, false)", handled, redraw)
	}
	if x, y := scroll.Offset(); x != 0 || y != 160 {
		t.Errorf("Offset after clamped wheel = (%v, %v), want (0, 160)", x, y)
	}

	// A wheel over empty space outside the viewport consumes nothing.
	if handled, _ := wheel(driver.Point{X: 500, Y: 500}, 0, -1); handled {
		t.Error("wheel outside the viewport must not be handled")
	}
}

func TestScrollWheelFallsThroughToEnclosingScroll(t *testing.T) {
	// A wheel over a child that cannot scroll is offered to the enclosing
	// scroll: hovering a plain button inside a scroll view still scrolls
	// the view.
	clickCount := 0
	button := Button("inside").OnClick(func() { clickCount++ })
	filler := fake("filler", 60, 300)
	content := Column(button, filler)
	setTreeFace(content, testFace(t))
	outer := Scroll(content)
	outer.measure(Constraints{Max: size(60, 100)})
	outer.arrange(rect(0, 0, 60, 100))
	router := newEventRouter(outer)

	p := center(button.bounds)
	handled, redraw := router.Handle(scrollWheelEvent(p, 0, -1))
	if !handled || !redraw {
		t.Fatalf("wheel over the button = (%v, %v), want (true, true)", handled, redraw)
	}
	if x, y := outer.Offset(); x != 0 || y != 40 {
		t.Errorf("outer offset after wheel over the button = (%v, %v), want (0, 40)", x, y)
	}
	if clickCount != 0 {
		t.Errorf("clicks = %d, want 0 (a wheel never presses a button)", clickCount)
	}
}

func TestScrollWheelPrefersInnerScroll(t *testing.T) {
	// With an (at-limit) inner scroll under the cursor, the wheel is
	// offered to the innermost consumer first and falls to the outer scroll
	// only when the inner one cannot move.
	innerContent := fake("inner-content", 60, 60)
	inner := Scroll(innerContent)
	filler := fake("filler", 60, 300)
	outer := Scroll(Column(inner, filler))
	outer.measure(Constraints{Max: size(60, 100)})
	outer.arrange(rect(0, 0, 60, 100))
	router := newEventRouter(outer)

	// The inner scroll's viewport equals its content size, so it can never
	// move and every wheel over it must reach the outer scroll.
	if x, y := inner.Offset(); x != 0 || y != 0 {
		t.Fatalf("inner offset = (%v, %v), want (0, 0) before any wheel", x, y)
	}
	p := center(inner.bounds)
	handled, redraw := router.Handle(scrollWheelEvent(p, 0, -1))
	if !handled || !redraw {
		t.Fatalf("wheel over the inner scroll = (%v, %v), want (true, true)", handled, redraw)
	}
	if x, y := inner.Offset(); x != 0 || y != 0 {
		t.Errorf("inner offset changed to (%v, %v), want (0, 0) (its content fits)", x, y)
	}
	if x, y := outer.Offset(); x != 0 || y != 40 {
		t.Errorf("outer offset = (%v, %v), want (0, 40) (the wheel fell through)", x, y)
	}
}

func TestScrollWheelDoesNotFallThroughToCoveredStackSibling(t *testing.T) {
	bottom := Scroll(fake("bottom-content", 60, 300))
	top := Scroll(fake("top-content", 60, 60))
	stack := Stack(bottom, top)
	stack.measure(Constraints{Max: size(60, 100)})
	stack.arrange(rect(0, 0, 60, 100))
	router := newEventRouter(stack)

	handled, redraw := router.Handle(scrollWheelEvent(center(top.bounds), 0, -1))
	if handled || redraw {
		t.Errorf("wheel over at-limit top Scroll = (%v, %v), want (false, false)", handled, redraw)
	}
	if x, y := top.Offset(); x != 0 || y != 0 {
		t.Errorf("top offset = (%v, %v), want unchanged (0, 0)", x, y)
	}
	if x, y := bottom.Offset(); x != 0 || y != 0 {
		t.Errorf("covered bottom offset = (%v, %v), want unchanged (0, 0)", x, y)
	}
}

func TestScrollWheelFallsThroughStackBranchToActualAncestor(t *testing.T) {
	top := Scroll(fake("top-content", 60, 60))
	content := Column(Stack(top), fake("filler", 60, 240))
	outer := Scroll(content)
	outer.measure(Constraints{Max: size(60, 100)})
	outer.arrange(rect(0, 0, 60, 100))
	router := newEventRouter(outer)

	handled, redraw := router.Handle(scrollWheelEvent(center(top.bounds), 0, -1))
	if !handled || !redraw {
		t.Fatalf("wheel over at-limit Stack branch = (%v, %v), want enclosing Scroll (true, true)", handled, redraw)
	}
	if x, y := top.Offset(); x != 0 || y != 0 {
		t.Errorf("top offset = (%v, %v), want unchanged (0, 0)", x, y)
	}
	if x, y := outer.Offset(); x != 0 || y != 40 {
		t.Errorf("outer offset = (%v, %v), want (0, 40) through actual ancestor", x, y)
	}
}

func TestScrollStyles(t *testing.T) {
	// The stylesheet sees the Scroll type name and applies padding when the
	// Go value is not set.
	styled := Scroll(fake("styled", 100, 200)).Class("feed")
	if got, want := styled.styleMeta(), (internalstyle.Meta{Type: "Scroll", Class: "feed"}); got != want {
		t.Errorf("styleMeta = %v, want %v", got, want)
	}
	styled.setResolved(resolved(map[string]string{"padding": "10px"}))
	styled.measure(Constraints{Max: size(100, 100)})
	styled.arrange(rect(0, 0, 100, 100))

	// The padding ring is carved out of the viewport: the content is
	// translated by the padded origin, and the vertical range shrinks to
	// 200-(100-2*10) = 120.
	styledChild := styled.content
	if got, want := styledChild.(*fakeComponent).bounds, rect(10, 10, 100, 200); got != want {
		t.Errorf("styled content bounds = %v, want %v", got, want)
	}
	styled.SetOffset(0, 1000)
	if x, y := styled.Offset(); x != 0 || y != 120 {
		t.Errorf("styled offset = (%v, %v), want (0, 120)", x, y)
	}

	// An explicit Go padding beats the stylesheet.
	otherContent := fake("other", 100, 200)
	explicit := Scroll(otherContent).Padding(4)
	explicit.setResolved(resolved(map[string]string{"padding": "10px"}))
	explicit.measure(Constraints{Max: size(100, 100)})
	explicit.arrange(rect(0, 0, 100, 100))
	if got, want := otherContent.bounds, rect(4, 4, 100, 200); got != want {
		t.Errorf("explicit content bounds = %v, want %v", got, want)
	}

	// Negative padding clamps to zero.
	plain := Scroll(fake("plain", 50, 50)).Padding(-3)
	if got, want := plain.measure(Constraints{}), size(50, 50); got != want {
		t.Errorf("negative padding measure = %v, want %v", got, want)
	}
}
