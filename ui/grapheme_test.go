package ui

import (
	"reflect"
	"testing"
)

// graphemeTestEnv pins the documented boundary rules to concrete strings.
// These tests are font-independent: they exercise the deterministic
// grapheme subset only, not any shaping engine.
func TestGraphemeBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []int
	}{
		{"empty", "", []int{0}},
		{"ascii", "abc", []int{0, 1, 2, 3}},
		{"combining mark joins base", "e\u0301", []int{0, 2}},
		{"mark chain joins base", "a\u0301\u0302b", []int{0, 3, 4}},
		{"crlf stays together", "a\r\nb", []int{0, 1, 3, 4}},
		{"lone controls split", "a\nb", []int{0, 1, 2, 3}},
		{"emoji modifier joins", "\U0001F44D\U0001F3FD", []int{0, 2}},
		{"zwj sequence joins", "\U0001F468\u200D\U0001F469\u200D\U0001F467", []int{0, 5}},
		{"zwj glues forward without pictograph", "a\u200Db", []int{0, 2, 3}},
		{"flag pairs", "\U0001F1E8\U0001F1F3\U0001F1E9\U0001F1EA", []int{0, 2, 4}},
		{"lone trailing regional indicator", "\U0001F1E8\U0001F1F3\U0001F1E9", []int{0, 2, 3}},
		{"regional indicators inside text", "x\U0001F1E8\U0001F1F3 y", []int{0, 1, 3, 4, 5}},
		{"variation selector joins", "❤\uFE0F", []int{0, 2}},
		{"keycap joins", "1\uFE0F\u20E3", []int{0, 3}},
		// Indic conjoining (virama-joined consonants) and Hangul jamo
		// stacking are outside the documented subset: the virama is a
		// combining mark and attaches to its base, but a following
		// consonant starts a new boundary. That is the honest capability
		// boundary, not a claim of full UAX#29 conformance.
		{"indic conjunct chain not merged (documented subset boundary)", "क\u094Dष", []int{0, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graphemeBoundaries([]rune(tt.value))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("graphemeBoundaries(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestGraphemeBoundarySnapping(t *testing.T) {
	boundaries := []int{0, 2, 4, 7}

	if got := snapToTextBoundary(boundaries, 4); got != 4 {
		t.Errorf("snapToTextBoundary(boundaries, 4) = %d, want 4 (already a boundary)", got)
	}
	if got := snapToTextBoundary(boundaries, 3); got != 2 {
		t.Errorf("snapToTextBoundary(boundaries, 3) = %d, want 2 (nearer, tie breaks lower)", got)
	}
	if got := snapToTextBoundary(boundaries, 5); got != 4 {
		t.Errorf("snapToTextBoundary(boundaries, 5) = %d, want 4 (nearer)", got)
	}
	if got := snapToTextBoundary(boundaries, 6); got != 7 {
		t.Errorf("snapToTextBoundary(boundaries, 6) = %d, want 7 (nearer)", got)
	}
	if got := snapToTextBoundary(boundaries, -3); got != 0 {
		t.Errorf("snapToTextBoundary(boundaries, -3) = %d, want 0 (clamped)", got)
	}
	if got := snapToTextBoundary(boundaries, 99); got != 7 {
		t.Errorf("snapToTextBoundary(boundaries, 99) = %d, want 7 (clamped)", got)
	}

	if got := boundaryAtOrBefore(boundaries, 5); got != 4 {
		t.Errorf("boundaryAtOrBefore(boundaries, 5) = %d, want 4", got)
	}
	if got := boundaryAtOrBefore(boundaries, 4); got != 4 {
		t.Errorf("boundaryAtOrBefore(boundaries, 4) = %d, want 4 (inclusive)", got)
	}
	if got := boundaryAtOrAfter(boundaries, 5); got != 7 {
		t.Errorf("boundaryAtOrAfter(boundaries, 5) = %d, want 7", got)
	}
	if got := boundaryAtOrAfter(boundaries, 4); got != 4 {
		t.Errorf("boundaryAtOrAfter(boundaries, 4) = %d, want 4 (inclusive)", got)
	}
}
