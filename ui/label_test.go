package ui

import (
	"testing"

	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/text"
)

// rasterTextSize measures one line of text with a freshly loaded GoRegular
// 18px face. It is the oracle that keeps the component measurement tests
// honest: it sees no component cache, so a stale cache fails the equality
// check no matter which text it kept.
func rasterTextSize(t *testing.T, value string) Size {
	t.Helper()
	face, err := text.NewFace(goregular.TTF, 18)
	if err != nil {
		t.Fatalf("text.NewFace(GoRegular, 18) error = %v", err)
	}
	defer face.Close()
	bitmap, err := face.Rasterize(value)
	if err != nil {
		t.Fatalf("Rasterize(%q) error = %v", value, err)
	}
	return Size{Width: float64(bitmap.Width), Height: float64(bitmap.Height)}
}

func TestLabelSetTextInvalidatesMeasurement(t *testing.T) {
	label := Label("Hi")
	setTreeFace(label, testFace(t))
	if got := label.Text(); got != "Hi" {
		t.Fatalf("Text() = %q, want %q", got, "Hi")
	}

	before := rasterTextSize(t, "Hi")
	if got := label.measure(Constraints{}); got != before {
		t.Errorf("measure = %v, want %v", got, before)
	}

	// SetText must drop the cached measurement: the next measure has to
	// match a fresh rasterization of the new text, not the old size.
	label.SetText("Hello, Meltemi!")
	if got := label.Text(); got != "Hello, Meltemi!" {
		t.Fatalf("Text() after SetText = %q, want %q", got, "Hello, Meltemi!")
	}
	after := rasterTextSize(t, "Hello, Meltemi!")
	got := label.measure(Constraints{})
	if got != after {
		t.Errorf("measure after SetText = %v, want a fresh measurement %v", got, after)
	}
	if got == before {
		t.Error("measure after SetText returned the stale size")
	}

	// Until the next SetText the measurement is stable.
	if again := label.measure(Constraints{}); again != got {
		t.Errorf("repeated measure = %v, want %v", again, got)
	}

	// Constraints still clamp the fresh measurement.
	if clamped := label.measure(Constraints{Max: size(4, 4)}); clamped != size(4, 4) {
		t.Errorf("clamped measure = %v, want %v", clamped, size(4, 4))
	}

	label.SetText("")
	if got, want := label.measure(Constraints{}), (Size{}); got != want {
		t.Errorf("empty measure = %v, want %v", got, want)
	}
}
