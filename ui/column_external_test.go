package ui_test

import (
	"testing"

	"github.com/zhoudm1743/meltemi/ui"
)

// newColumn is the helper-signature shape the exported ColumnLayout type
// supports: helper functions can name the return type.
func newColumn() *ui.ColumnLayout {
	return ui.Column().Gap(4)
}

func TestColumnLayoutExternalUse(t *testing.T) {
	// Explicit type declaration in a variable.
	var empty *ui.ColumnLayout = ui.Column()
	if empty == nil {
		t.Fatal("Column() returned nil")
	}

	// Chained calls return the same exported type and keep composing.
	col := ui.Column(empty, newColumn()).Gap(12).Padding(20)
	if col == nil {
		t.Fatal("chained Column is nil")
	}

	// A ColumnLayout is a Component and can be nested into other layouts.
	var asComponent ui.Component = col
	if got := ui.Column(asComponent).Gap(1); got == nil {
		t.Fatal("Column(Component) returned nil")
	}
}
