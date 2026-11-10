package ui

import (
	"reflect"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

func TestSpacerContract(t *testing.T) {
	spacer := Spacer()

	// A spacer has no natural size of its own.
	if got, want := spacer.measure(Constraints{}), (Size{}); got != want {
		t.Errorf("measure = %v, want %v", got, want)
	}

	// Arranging and drawing a spacer never fails or paints; it never
	// claims pointer events either.
	spacer.arrange(rect(5, 5, 100, 20))
	spacer.draw(&drawContext{})
	if handled, redraw := spacer.handleEvent(driver.Event{Kind: driver.EventMouseMove}); handled || redraw {
		t.Errorf("handleEvent = (%v, %v), want (false, false)", handled, redraw)
	}

	var visited []component
	spacer.walk(func(c component) { visited = append(visited, c) })
	if want := []component{spacer}; !reflect.DeepEqual(visited, want) {
		t.Errorf("walk = %v, want %v", visited, want)
	}

	if got := spacer.styleMeta(); got.Type != "Spacer" {
		t.Errorf("styleMeta type = %q, want Spacer", got.Type)
	}
}

func TestRowAndColumnDistributeSpacers(t *testing.T) {
	t.Run("row spreads leftover width", func(t *testing.T) {
		left := fake("left", 50, 20)
		spring := Spacer()
		right := fake("right", 30, 20)
		row := Row(left, spring, right)

		if got, want := row.measure(Constraints{Max: size(200, 0)}), size(200, 20); got != want {
			t.Fatalf("measure = %v, want %v", got, want)
		}
		row.arrange(rect(0, 0, 200, 20))
		if got, want := left.bounds, rect(0, 0, 50, 20); got != want {
			t.Errorf("left bounds = %v, want %v", got, want)
		}
		if got, want := spring.bounds, rect(50, 0, 120, 20); got != want {
			t.Errorf("spacer bounds = %v, want %v", got, want)
		}
		if got, want := right.bounds, rect(170, 0, 30, 20); got != want {
			t.Errorf("right bounds = %v, want %v", got, want)
		}
	})

	t.Run("column spreads leftover height", func(t *testing.T) {
		top := fake("top", 20, 30)
		spring := Spacer()
		bottom := fake("bottom", 20, 40)
		column := Column(top, spring, bottom)

		if got, want := column.measure(Constraints{Max: size(0, 200)}), size(20, 200); got != want {
			t.Fatalf("measure = %v, want %v", got, want)
		}
		column.arrange(rect(0, 0, 20, 200))
		if got, want := top.bounds, rect(0, 0, 20, 30); got != want {
			t.Errorf("top bounds = %v, want %v", got, want)
		}
		if got, want := spring.bounds, rect(0, 30, 20, 130); got != want {
			t.Errorf("spacer bounds = %v, want %v", got, want)
		}
		if got, want := bottom.bounds, rect(0, 160, 20, 40); got != want {
			t.Errorf("bottom bounds = %v, want %v", got, want)
		}
	})

	t.Run("multiple spacers share equally", func(t *testing.T) {
		before := Spacer()
		middle := fake("middle", 40, 20)
		after := Spacer()
		row := Row(before, middle, after)

		if got, want := row.measure(Constraints{Max: size(200, 0)}), size(200, 20); got != want {
			t.Fatalf("measure = %v, want %v", got, want)
		}
		row.arrange(rect(0, 0, 200, 20))
		if got, want := before.bounds, rect(0, 0, 80, 20); got != want {
			t.Errorf("first spacer bounds = %v, want %v", got, want)
		}
		if got, want := middle.bounds, rect(80, 0, 40, 20); got != want {
			t.Errorf("middle bounds = %v, want %v", got, want)
		}
		if got, want := after.bounds, rect(120, 0, 80, 20); got != want {
			t.Errorf("second spacer bounds = %v, want %v", got, want)
		}
	})

	t.Run("insufficient space collapses spacers", func(t *testing.T) {
		// The fixed children consume the whole budget: the spacer gets
		// zero and the children clamp instead of producing negative
		// sizes or drawing outside the container.
		left := fake("left", 80, 20)
		spring := Spacer()
		right := fake("right", 90, 20)
		row := Row(left, spring, right)

		if got, want := row.measure(Constraints{Max: size(100, 0)}), size(100, 20); got != want {
			t.Fatalf("row measure = %v, want %v", got, want)
		}
		row.arrange(rect(0, 0, 100, 20))
		if got, want := left.bounds, rect(0, 0, 80, 20); got != want {
			t.Errorf("left bounds = %v, want %v", got, want)
		}
		if got, want := spring.bounds, rect(80, 0, 0, 20); got != want {
			t.Errorf("row spacer bounds = %v, want zero width", got)
		}
		if got, want := right.bounds, rect(80, 0, 20, 20); got != want {
			t.Errorf("right bounds = %v, want %v", got, want)
		}

		top := fake("top", 20, 60)
		springColumn := Spacer()
		bottom := fake("bottom", 20, 60)
		column := Column(top, springColumn, bottom)

		if got, want := column.measure(Constraints{Max: size(0, 100)}), size(20, 100); got != want {
			t.Fatalf("column measure = %v, want %v", got, want)
		}
		column.arrange(rect(0, 0, 20, 100))
		if got, want := top.bounds, rect(0, 0, 20, 60); got != want {
			t.Errorf("top bounds = %v, want %v", got, want)
		}
		if got, want := springColumn.bounds, rect(0, 60, 20, 0); got != want {
			t.Errorf("column spacer bounds = %v, want zero height", got)
		}
		if got, want := bottom.bounds, rect(0, 60, 20, 40); got != want {
			t.Errorf("bottom bounds = %v, want %v", got, want)
		}
	})

	t.Run("unbounded leaves spacers empty", func(t *testing.T) {
		left := fake("left", 50, 20)
		spring := Spacer()
		row := Row(left, spring)

		if got, want := row.measure(Constraints{}), size(50, 20); got != want {
			t.Fatalf("row measure = %v, want %v", got, want)
		}
		row.arrange(rect(0, 0, 50, 20))
		if got, want := spring.bounds, rect(50, 0, 0, 20); got != want {
			t.Errorf("row spacer bounds = %v, want zero width", got)
		}

		top := fake("top", 20, 30)
		springColumn := Spacer()
		column := Column(top, springColumn)

		if got, want := column.measure(Constraints{}), size(20, 30); got != want {
			t.Fatalf("column measure = %v, want %v", got, want)
		}
		column.arrange(rect(0, 0, 20, 30))
		if got, want := springColumn.bounds, rect(0, 30, 20, 0); got != want {
			t.Errorf("column spacer bounds = %v, want zero height", got)
		}
	})

	t.Run("all spacers split the whole space", func(t *testing.T) {
		first := Spacer()
		second := Spacer()
		row := Row(first, second)

		if got, want := row.measure(Constraints{Max: size(100, 0)}), size(100, 0); got != want {
			t.Fatalf("measure = %v, want %v", got, want)
		}
		row.arrange(rect(0, 0, 100, 20))
		if got, want := first.bounds, rect(0, 0, 50, 20); got != want {
			t.Errorf("first spacer bounds = %v, want %v", got, want)
		}
		if got, want := second.bounds, rect(50, 0, 50, 20); got != want {
			t.Errorf("second spacer bounds = %v, want %v", got, want)
		}
	})
}
