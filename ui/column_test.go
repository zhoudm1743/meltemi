package ui

import (
	"reflect"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/render"
)

func size(width, height float64) Size {
	return Size{Width: width, Height: height}
}

func rect(x, y, width, height float64) render.Rect {
	return render.Rect{X: x, Y: y, Width: width, Height: height}
}

// fakeComponent is a leaf with a fixed natural size that records how the
// layout pass treated it: the constraints each measure received, the
// measure count and clamped result, the arranged bounds, the events it
// received and its draw calls.
type fakeComponent struct {
	name          string
	natural       Size
	size          Size
	bounds        render.Rect
	log           *[]drawRecord
	onEvent       func(driver.Event) (handled, redraw bool)
	events        []driver.Event
	measureCalls  int
	constraintLog []Constraints
}

type drawRecord struct {
	name string
	rect render.Rect
}

func fake(name string, width, height float64) *fakeComponent {
	return &fakeComponent{name: name, natural: size(width, height)}
}

func (f *fakeComponent) measure(constraints Constraints) Size {
	f.measureCalls++
	f.constraintLog = append(f.constraintLog, constraints)
	f.size = clampSize(f.natural, constraints)
	return f.size
}

func (f *fakeComponent) arrange(bounds render.Rect) {
	f.bounds = bounds
}

func (f *fakeComponent) draw(ctx *drawContext) {
	if f.log != nil {
		*f.log = append(*f.log, drawRecord{name: f.name, rect: f.bounds})
	}
}

func (f *fakeComponent) handleEvent(e driver.Event) (handled, redraw bool) {
	f.events = append(f.events, e)
	if f.onEvent != nil {
		return f.onEvent(e)
	}
	return false, false
}

func (f *fakeComponent) walk(visit func(component)) {
	visit(f)
}

func (f *fakeComponent) isUIComponent() {}

func TestColumnMeasureAndArrange(t *testing.T) {
	first := fake("first", 100, 24)
	second := fake("second", 80, 36)
	col := Column(first, second).Gap(12).Padding(20)

	// Content width is the widest child, content height is the child
	// heights plus the gap, and padding is added on every side.
	if got, want := col.measure(Constraints{}), size(140, 112); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}

	col.arrange(rect(0, 0, 140, 112))
	if got, want := first.bounds, rect(20, 20, 100, 24); got != want {
		t.Errorf("first bounds = %v, want %v", got, want)
	}
	if got, want := second.bounds, rect(20, 56, 100, 36); got != want {
		t.Errorf("second bounds = %v, want %v", got, want)
	}

	// A wider arrange rect stretches children horizontally to the content
	// width while heights keep their natural sizes.
	col.arrange(rect(0, 0, 220, 160))
	if got, want := first.bounds, rect(20, 20, 180, 24); got != want {
		t.Errorf("stretched first bounds = %v, want %v", got, want)
	}
	if got, want := second.bounds, rect(20, 56, 180, 36); got != want {
		t.Errorf("stretched second bounds = %v, want %v", got, want)
	}
}

func TestColumnEmptyAndSingleChild(t *testing.T) {
	empty := Column().Gap(12).Padding(20)
	if got, want := empty.measure(Constraints{}), size(40, 40); got != want {
		t.Errorf("empty measure = %v, want %v", got, want)
	}
	empty.arrange(rect(0, 0, 40, 40))

	only := fake("only", 30, 24)
	single := Column(only).Gap(12).Padding(20)
	if got, want := single.measure(Constraints{}), size(70, 64); got != want {
		t.Errorf("single measure = %v, want %v", got, want)
	}
	single.arrange(rect(0, 0, 70, 64))
	// One child means no gap and padding on every side.
	if got, want := only.bounds, rect(20, 20, 30, 24); got != want {
		t.Errorf("single bounds = %v, want %v", got, want)
	}
}

func TestColumnNested(t *testing.T) {
	innerFirst := fake("inner-first", 50, 10)
	innerSecond := fake("inner-second", 30, 20)
	var log []drawRecord
	innerFirst.log = &log
	innerSecond.log = &log
	inner := Column(innerFirst, innerSecond).Gap(4)
	if got, want := inner.measure(Constraints{}), size(50, 34); got != want {
		t.Errorf("inner measure = %v, want %v", got, want)
	}

	outerLast := fake("outer-last", 80, 40)
	outerLast.log = &log
	outer := Column(inner, outerLast).Gap(8).Padding(6)
	if got, want := outer.measure(Constraints{}), size(92, 94); got != want {
		t.Errorf("outer measure = %v, want %v", got, want)
	}

	outer.arrange(rect(10, 20, 92, 94))
	bounds := []struct {
		name      string
		got, want render.Rect
	}{
		{"inner column", inner.bounds, rect(16, 26, 80, 34)},
		{"inner first", innerFirst.bounds, rect(16, 26, 80, 10)},
		{"inner second", innerSecond.bounds, rect(16, 40, 80, 20)},
		{"outer last", outerLast.bounds, rect(16, 68, 80, 40)},
	}
	for _, check := range bounds {
		if check.got != check.want {
			t.Errorf("%s bounds = %v, want %v", check.name, check.got, check.want)
		}
	}

	log = nil
	outer.draw(&drawContext{})
	wantLog := []drawRecord{
		{"inner-first", rect(16, 26, 80, 10)},
		{"inner-second", rect(16, 40, 80, 20)},
		{"outer-last", rect(16, 68, 80, 40)},
	}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("draw order = %v, want %v", log, wantLog)
	}
}

func TestColumnMeasureClampedByConstraints(t *testing.T) {
	child := fake("child", 100, 24)
	col := Column(child).Padding(20)

	// A zero Max dimension leaves that direction unbounded.
	if got, want := col.measure(Constraints{Max: size(120, 0)}), size(120, 64); got != want {
		t.Errorf("max-clamped measure = %v, want %v", got, want)
	}
	if got, want := col.measure(Constraints{Max: size(0, 50)}), size(140, 50); got != want {
		t.Errorf("max-clamped measure = %v, want %v", got, want)
	}
	if got, want := col.measure(Constraints{Min: size(200, 150)}), size(200, 150); got != want {
		t.Errorf("min-clamped measure = %v, want %v", got, want)
	}

	// Leaves obey the same Min/Max contract.
	if got, want := child.measure(Constraints{Max: size(50, 10)}), size(50, 10); got != want {
		t.Errorf("leaf clamped measure = %v, want %v", got, want)
	}
}

func TestColumnNegativeGapAndPaddingClamp(t *testing.T) {
	first := fake("first", 30, 10)
	second := fake("second", 30, 10)
	col := Column(first, second).Gap(-8).Padding(-4)

	if got, want := col.measure(Constraints{}), size(30, 20); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	col.arrange(rect(0, 0, 30, 20))
	if got, want := second.bounds, rect(0, 10, 30, 10); got != want {
		t.Errorf("second bounds = %v, want %v", got, want)
	}
}

func TestColumnWalkOrder(t *testing.T) {
	innerFirst := fake("inner-first", 10, 10)
	innerSecond := fake("inner-second", 10, 10)
	inner := Column(innerFirst, innerSecond)
	outerLast := fake("outer-last", 10, 10)
	outer := Column(inner, outerLast)

	var order []component
	outer.walk(func(c component) {
		order = append(order, c)
	})
	want := []component{outer, inner, innerFirst, innerSecond, outerLast}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("walk order = %v, want %v", order, want)
	}
}

func TestColumnHandleEventAggregation(t *testing.T) {
	event := driver.Event{Kind: driver.EventMouseButton}

	passive := fake("passive", 10, 10)
	passive.onEvent = func(driver.Event) (bool, bool) { return false, true }
	greedy := fake("greedy", 10, 10)
	greedy.onEvent = func(driver.Event) (bool, bool) { return true, true }
	col := Column(passive, greedy)

	handled, redraw := col.handleEvent(event)
	if !handled || !redraw {
		t.Errorf("handleEvent = (%v, %v), want (true, true)", handled, redraw)
	}
	if len(passive.events) != 1 || len(greedy.events) != 1 {
		t.Fatalf("children events = (%d, %d), want (1, 1)", len(passive.events), len(greedy.events))
	}
	if passive.events[0] != event || greedy.events[0] != event {
		t.Error("children did not receive the forwarded event")
	}

	quiet := Column(fake("a", 1, 1), fake("b", 1, 1))
	if handled, redraw := quiet.handleEvent(event); handled || redraw {
		t.Errorf("quiet handleEvent = (%v, %v), want (false, false)", handled, redraw)
	}
}

func TestColumnMinHeightAppliesOnce(t *testing.T) {
	first := fake("first", 100, 24)
	second := fake("second", 80, 36)
	col := Column(first, second).Gap(12)

	// The vertical minimum clamps the total only; it must not inflate
	// every child. Natural 72 clamps to 100, not 100+12+100=212.
	if got, want := col.measure(Constraints{Min: size(0, 100)}), size(100, 100); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	if got := first.constraintLog[0].Min.Height; got != 0 {
		t.Errorf("first Min.Height = %v, want 0 (the parent minimum clamps the total only)", got)
	}
	col.arrange(rect(0, 0, 100, 100))
	if got, want := first.bounds, rect(0, 0, 100, 24); got != want {
		t.Errorf("first bounds = %v, want %v", got, want)
	}
	if got, want := second.bounds, rect(0, 36, 100, 36); got != want {
		t.Errorf("second bounds = %v, want %v", got, want)
	}
}

func TestColumnChildConstraints(t *testing.T) {
	first := fake("first", 100, 24)
	second := fake("second", 80, 36)
	col := Column(first, second).Gap(12).Padding(20)

	col.measure(Constraints{Min: size(0, 100), Max: size(200, 90)})

	// Every child gets its own inner constraints: the vertical minimum
	// is cleared and the Max is reduced by the padding.
	if got, want := first.constraintLog[0], (Constraints{Max: size(160, 50)}); got != want {
		t.Errorf("first constraints = %v, want %v", got, want)
	}
	// The remaining vertical budget after the first child and the gap is
	// written into the next child's Max.Height: 50-24-12=14.
	if got, want := second.constraintLog[0], (Constraints{Max: size(160, 14)}); got != want {
		t.Errorf("second constraints = %v, want %v", got, want)
	}
	if first.measureCalls != 1 || second.measureCalls != 1 {
		t.Errorf("measure calls = (%d, %d), want (1, 1)", first.measureCalls, second.measureCalls)
	}
}

func TestColumnMaxExhaustedByPadding(t *testing.T) {
	child := fake("child", 100, 24)
	col := Column(child).Padding(20)

	// Max.Height 30 leaves no content space (30 < 2*20): the child
	// collapses without being measured and the total clamps to Max.
	if got, want := col.measure(Constraints{Max: size(0, 30)}), size(40, 30); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	if child.measureCalls != 0 {
		t.Errorf("child measure calls = %d, want 0 (no room, must not measure unbounded)", child.measureCalls)
	}
	col.arrange(rect(0, 0, 140, 30))
	if got, want := child.bounds, rect(20, 20, 0, 0); got != want {
		t.Errorf("child bounds = %v, want %v", got, want)
	}

	// Same exhaustion on the horizontal axis.
	if got, want := col.measure(Constraints{Max: size(30, 0)}), size(30, 40); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	col.arrange(rect(0, 0, 30, 64))
	if got, want := child.bounds, rect(20, 20, 0, 0); got != want {
		t.Errorf("child bounds = %v, want %v", got, want)
	}
}

func TestColumnNestedMaxExhaustedByPadding(t *testing.T) {
	innerFirst := fake("inner-first", 50, 10)
	innerSecond := fake("inner-second", 30, 20)
	inner := Column(innerFirst, innerSecond).Gap(4)
	outer := Column(inner).Padding(10)

	// The outer Max leaves 5px of content space; the inner column passes
	// it down, measures its first child into it, and the second child
	// collapses without measuring.
	if got, want := outer.measure(Constraints{Max: size(0, 25)}), size(70, 25); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	if got, want := innerFirst.constraintLog[0], (Constraints{Max: size(0, 5)}); got != want {
		t.Errorf("inner first constraints = %v, want %v", got, want)
	}
	if innerSecond.measureCalls != 0 {
		t.Errorf("inner second measure calls = %d, want 0 (budget exhausted)", innerSecond.measureCalls)
	}
	outer.arrange(rect(0, 0, 70, 25))
	if got, want := inner.bounds, rect(10, 10, 50, 5); got != want {
		t.Errorf("inner bounds = %v, want %v", got, want)
	}
	if got, want := innerFirst.bounds, rect(10, 10, 50, 5); got != want {
		t.Errorf("inner first bounds = %v, want %v", got, want)
	}
	if got, want := innerSecond.bounds, rect(10, 15, 0, 0); got != want {
		t.Errorf("inner second bounds = %v, want %v", got, want)
	}
}

func TestColumnArrangeSmallerThanPadding(t *testing.T) {
	first := fake("first", 100, 24)
	second := fake("second", 100, 36)
	col := Column(first, second).Gap(12).Padding(20)
	col.measure(Constraints{})
	col.arrange(rect(5, 5, 10, 10))

	// The content box is empty; every child degenerates to a zero-size
	// point that stays inside the parent bounds.
	for _, child := range []*fakeComponent{first, second} {
		b := child.bounds
		if b.Width != 0 || b.Height != 0 {
			t.Errorf("child bounds = %v, want zero size", b)
		}
		if b.X < 5 || b.X > 15 || b.Y < 5 || b.Y > 15 {
			t.Errorf("child bounds = %v, want a point inside (5,5)-(15,15)", b)
		}
	}
	if first.bounds != second.bounds {
		t.Errorf("children bounds = %v and %v, want the same point", first.bounds, second.bounds)
	}
}
