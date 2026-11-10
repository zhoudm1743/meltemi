package ui

import (
	"reflect"
	"testing"
)

func TestStackMeasureArrangeAndStyles(t *testing.T) {
	first := fake("first", 100, 24)
	second := fake("second", 80, 36)
	stack := Stack(first, second).Padding(10)

	// The content box is the largest child in each direction, plus
	// padding on every side.
	if got, want := stack.measure(Constraints{}), size(120, 56); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}

	// Every child receives the same content rectangle; they overlap by
	// design and paint in declaration order, so the later child is on
	// top.
	stack.arrange(rect(0, 0, 120, 56))
	if got, want := first.bounds, rect(10, 10, 100, 36); got != want {
		t.Errorf("first bounds = %v, want %v", got, want)
	}
	if got, want := second.bounds, rect(10, 10, 100, 36); got != want {
		t.Errorf("second bounds = %v, want %v", got, want)
	}

	var log []drawRecord
	first.log = &log
	second.log = &log
	stack.draw(&drawContext{})
	wantLog := []drawRecord{
		{"first", rect(10, 10, 100, 36)},
		{"second", rect(10, 10, 100, 36)},
	}
	if !reflect.DeepEqual(log, wantLog) {
		t.Errorf("draw order = %v, want %v", log, wantLog)
	}
}

func TestStackEmptyAndSingleChild(t *testing.T) {
	empty := Stack().Padding(10)
	if got, want := empty.measure(Constraints{}), size(20, 20); got != want {
		t.Errorf("empty measure = %v, want %v", got, want)
	}
	empty.arrange(rect(0, 0, 20, 20))

	only := fake("only", 30, 24)
	single := Stack(only).Padding(10)
	if got, want := single.measure(Constraints{}), size(50, 44); got != want {
		t.Errorf("single measure = %v, want %v", got, want)
	}
	single.arrange(rect(0, 0, 50, 44))
	if got, want := only.bounds, rect(10, 10, 30, 24); got != want {
		t.Errorf("single bounds = %v, want %v", got, want)
	}
}

func TestStackStylesAndExplicitPriority(t *testing.T) {
	// Stylesheet padding applies when the Go value is not set.
	a := fake("a", 10, 10)
	b := fake("b", 40, 20)
	styled := Stack(a, b)
	styled.setResolved(resolved(map[string]string{"padding": "6px"}))
	if got, want := styled.measure(Constraints{}), size(52, 32); got != want {
		t.Errorf("styled measure = %v, want %v", got, want)
	}

	// An explicit Go value takes priority over the stylesheet.
	c := fake("c", 10, 10)
	explicit := Stack(c).Padding(2)
	explicit.setResolved(resolved(map[string]string{"padding": "6px"}))
	if got, want := explicit.measure(Constraints{}), size(14, 14); got != want {
		t.Errorf("explicit measure = %v, want %v", got, want)
	}
}

func TestStackPaddingExceedsAvailable(t *testing.T) {
	child := fake("child", 100, 24)
	stack := Stack(child).Padding(20)

	// Max 30 leaves no content space (30 < 2*20): the child collapses
	// without being measured and the total clamps to Max.
	if got, want := stack.measure(Constraints{Max: size(30, 30)}), size(30, 30); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	if child.measureCalls != 0 {
		t.Errorf("child measure calls = %d, want 0 (no room, must not measure unbounded)", child.measureCalls)
	}
	stack.arrange(rect(0, 0, 30, 30))
	if got, want := child.bounds, rect(20, 20, 0, 0); got != want {
		t.Errorf("child bounds = %v, want %v", got, want)
	}
}

func TestStackZeroBoundsArrange(t *testing.T) {
	child := fake("child", 10, 10)
	stack := Stack(child)
	if got, want := stack.measure(Constraints{}), size(10, 10); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}

	// Degenerate bounds collapse the child to a zero-size point instead
	// of a rect that would draw outside the stack.
	stack.arrange(rect(0, 0, 0, 0))
	if got, want := child.bounds, rect(0, 0, 0, 0); got != want {
		t.Errorf("child bounds = %v, want %v", got, want)
	}
}
