package ui

import (
	"strings"
	"testing"

	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

func TestRowMeasureArrangeAndStyles(t *testing.T) {
	first := fake("first", 100, 24)
	second := fake("second", 80, 36)
	row := Row(first, second).Gap(12).Padding(20)

	// Content width is the child widths plus the gaps, content height is
	// the tallest child, and padding is added on every side. Children
	// keep their measured widths and stretch vertically to the content
	// height.
	if got, want := row.measure(Constraints{}), size(232, 76); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}

	row.arrange(rect(0, 0, 232, 76))
	if got, want := first.bounds, rect(20, 20, 100, 36); got != want {
		t.Errorf("first bounds = %v, want %v", got, want)
	}
	if got, want := second.bounds, rect(132, 20, 80, 36); got != want {
		t.Errorf("second bounds = %v, want %v", got, want)
	}

	// A taller arrange rect stretches children vertically to the content
	// height while widths keep their measured sizes.
	row.arrange(rect(0, 0, 300, 100))
	if got, want := first.bounds, rect(20, 20, 100, 60); got != want {
		t.Errorf("stretched first bounds = %v, want %v", got, want)
	}
	if got, want := second.bounds, rect(132, 20, 80, 60); got != want {
		t.Errorf("stretched second bounds = %v, want %v", got, want)
	}
}

func TestRowEmptyAndSingleChild(t *testing.T) {
	empty := Row().Gap(12).Padding(20)
	if got, want := empty.measure(Constraints{}), size(40, 40); got != want {
		t.Errorf("empty measure = %v, want %v", got, want)
	}
	empty.arrange(rect(0, 0, 40, 40))

	only := fake("only", 30, 24)
	single := Row(only).Gap(12).Padding(20)
	if got, want := single.measure(Constraints{}), size(70, 64); got != want {
		t.Errorf("single measure = %v, want %v", got, want)
	}
	single.arrange(rect(0, 0, 70, 64))
	if got, want := only.bounds, rect(20, 20, 30, 24); got != want {
		t.Errorf("single bounds = %v, want %v", got, want)
	}
}

func TestRowStylesAndExplicitPriority(t *testing.T) {
	// Stylesheet gap and padding apply when the Go values are not set.
	a := fake("a", 10, 10)
	b := fake("b", 10, 10)
	styled := Row(a, b)
	styled.setResolved(resolved(map[string]string{"gap": "10px", "padding": "4px"}))
	if got, want := styled.measure(Constraints{}), size(38, 18); got != want {
		t.Errorf("styled measure = %v, want %v", got, want)
	}

	// An explicit Go value takes priority over the stylesheet.
	c := fake("c", 10, 10)
	d := fake("d", 10, 10)
	explicit := Row(c, d).Gap(2).Padding(1)
	explicit.setResolved(resolved(map[string]string{"gap": "10px", "padding": "4px"}))
	if got, want := explicit.measure(Constraints{}), size(24, 12); got != want {
		t.Errorf("explicit measure = %v, want %v", got, want)
	}
}

func TestRowStylesFromParsedSheet(t *testing.T) {
	// The parser accepts the Row type name and a sheet resolves against
	// the row's metadata, closing the loop from CSS text to layout.
	sheet, err := internalstyle.Parse(strings.NewReader("Row { gap: 10px; padding: 4px }\nStack { padding: 6px }"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	row := Row(fake("a", 10, 10), fake("b", 10, 10)).ID("row-id")
	row.setResolved(sheet.Resolve(row.styleMeta(), internalstyle.Resolved{}))
	if got, want := row.measure(Constraints{}), size(38, 18); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}

	stack := Stack(fake("c", 10, 10))
	stack.setResolved(sheet.Resolve(stack.styleMeta(), internalstyle.Resolved{}))
	// The Row rule's gap must not leak into the stack: padding only.
	if got, want := stack.measure(Constraints{}), size(22, 22); got != want {
		t.Errorf("stack measure = %v, want %v", got, want)
	}
}

func TestRowNegativeGapAndPaddingClamp(t *testing.T) {
	first := fake("first", 30, 10)
	second := fake("second", 30, 10)
	row := Row(first, second).Gap(-8).Padding(-4)

	if got, want := row.measure(Constraints{}), size(60, 10); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	row.arrange(rect(0, 0, 60, 10))
	if got, want := second.bounds, rect(30, 0, 30, 10); got != want {
		t.Errorf("second bounds = %v, want %v", got, want)
	}
}

func TestRowMaxExhaustedByPadding(t *testing.T) {
	child := fake("child", 100, 24)
	row := Row(child).Padding(20)

	// Max.Width 30 leaves no content space (30 < 2*20): the child
	// collapses without being measured and the total clamps to Max.
	if got, want := row.measure(Constraints{Max: size(30, 0)}), size(30, 40); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	if child.measureCalls != 0 {
		t.Errorf("child measure calls = %d, want 0 (no room, must not measure unbounded)", child.measureCalls)
	}
	row.arrange(rect(0, 0, 30, 64))
	if got, want := child.bounds, rect(20, 20, 0, 0); got != want {
		t.Errorf("child bounds = %v, want %v", got, want)
	}

	// Same exhaustion on the vertical axis.
	if got, want := row.measure(Constraints{Max: size(0, 30)}), size(40, 30); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}
	row.arrange(rect(0, 0, 140, 30))
	if got, want := child.bounds, rect(20, 20, 0, 0); got != want {
		t.Errorf("child bounds = %v, want %v", got, want)
	}
}
