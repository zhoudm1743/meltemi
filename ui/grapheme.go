package ui

import (
	"math"
	"sort"
	"unicode"

	"github.com/zhoudm1743/meltemi/internal/text"
)

// This file is the deterministic grapheme-cluster boundary logic the shared
// text buffer edits by. It is a documented subset of UAX#29 tailored to what
// a UI text field must never split — not a claim of full segmentation
// conformance. The supported rules are exactly:
//
//   - CRLF is one cluster (a lone CR or LF, or any other control, splits).
//   - Combining marks (Mn/Mc/Me), variation selectors (U+FE00–FE0F,
//     U+E0100–E01EF), emoji modifiers (U+1F3FB–U+1F3FF), the combining
//     enclosing keycap (U+20E3) and ZWNJ join the preceding base.
//   - ZWJ joins the surrounding runes when the rune before it is
//     emoji-like (approximated as U+1F000+, U+2300–U+23FF, U+2600–U+27BF,
//     U+2B00–U+2BFF, U+2190–U+21FF — a stand-in for the Extended_
//     Pictographic property), so common emoji ZWJ sequences stay atomic.
//     A ZWJ after ordinary text does not glue.
//   - Regional indicators pair up: a flag (two indicators) is one cluster;
//     a third indicator starts a new cluster.
//   - Anything else breaks between base runes, so plain text, CJK and
//     unpaired emoji behave as one caret step per rune.
//
// Outside the subset (documented, not silently wrong): Indic conjoining
// (virama-joined consonants), Hangul jamo stacking, Arabic-number prepend
// runes and full Extended_Pictographic coverage. Those runes keep one
// boundary each (or per the rules above), which is always safe for editing.

const (
	zwjRune  = rune(0x200D)
	zwnjRune = rune(0x200C)
)

// isExtendRune reports whether r attaches to the preceding base rune:
// combining marks, variation selectors, emoji modifiers and the keycap.
func isExtendRune(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r) ||
		(r >= 0x1F3FB && r <= 0x1F3FF) || // emoji skin-tone modifiers
		(r >= 0xE0100 && r <= 0xE01EF) // variation selectors supplement
}

// isGraphemeControl reports whether r forces a boundary on both sides.
// ZWJ and ZWNJ are format chars but glue instead of splitting, so they are
// excluded here and handled by the join rules.
func isGraphemeControl(r rune) bool {
	if r == zwjRune || r == zwnjRune {
		return false
	}
	return r < 0x20 || (r >= 0x7F && r <= 0xA0) || unicode.Is(unicode.Cf, r)
}

// isRegionalIndicator reports whether r is an emoji flag indicator.
func isRegionalIndicator(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// isEmojiLike approximates the Extended_Pictographic property with codepoint
// ranges; it only decides whether a ZWJ glues, so a coarse superset is safe.
func isEmojiLike(r rune) bool {
	return r >= 0x1F000 ||
		(r >= 0x2300 && r <= 0x23FF) ||
		(r >= 0x2600 && r <= 0x27BF) ||
		(r >= 0x2B00 && r <= 0x2BFF) ||
		(r >= 0x2190 && r <= 0x21FF)
}

// emojiSequenceBase reports whether the ZWJ at zwjIndex continues an emoji
// sequence: the rune before it, skipping attached extenders, is emoji-like.
func emojiSequenceBase(runes []rune, zwjIndex int) bool {
	for j := zwjIndex - 1; j >= 0; j-- {
		r := runes[j]
		if r == zwjRune {
			return false
		}
		if isExtendRune(r) {
			continue
		}
		return isEmojiLike(r)
	}
	return false
}

// graphemeBreakBefore reports whether a caret boundary exists between
// runes[i-1] and runes[i]. Index i must be inside runes.
func graphemeBreakBefore(runes []rune, i int) bool {
	prev, cur := runes[i-1], runes[i]
	if prev == '\r' && cur == '\n' {
		return false
	}
	if isGraphemeControl(prev) || isGraphemeControl(cur) {
		return true
	}
	if isExtendRune(cur) || cur == zwjRune || cur == zwnjRune {
		return false
	}
	if prev == zwjRune && emojiSequenceBase(runes, i-1) {
		return false
	}
	if isRegionalIndicator(prev) && isRegionalIndicator(cur) {
		// Count the consecutive indicators ending at prev: a break exists
		// only between pairs, so an odd run length means prev opens a
		// fresh pair and no boundary belongs between the two.
		count := 0
		for j := i - 1; j >= 0 && isRegionalIndicator(runes[j]); j-- {
			count++
		}
		if count%2 == 1 {
			return false
		}
	}
	return true
}

// graphemeBoundaries returns the sorted caret boundaries of runes under the
// documented subset rules, always including 0 and len(runes).
func graphemeBoundaries(runes []rune) []int {
	if len(runes) == 0 {
		return []int{0}
	}
	boundaries := []int{0}
	for i := 1; i < len(runes); i++ {
		if graphemeBreakBefore(runes, i) {
			boundaries = append(boundaries, i)
		}
	}
	return append(boundaries, len(runes))
}

// shapedTextBoundaries refines grapheme boundaries with the shaped clusters
// of the same text: a grapheme boundary that falls strictly inside a shaped
// cluster (a ligature such as lam-alef, where the caret has no visual
// position of its own) is dropped, so editing never splits what shaping
// merged. Boundaries that are cluster edges — including whole graphemes
// spanning several shaped clusters, like a ZWJ sequence in a font without
// the joined glyph — are kept.
func shapedTextBoundaries(runes []rune, clusters []text.Cluster) []int {
	graphemes := graphemeBoundaries(runes)
	if len(clusters) == 0 {
		return graphemes
	}
	allowed := make(map[int]bool, 2*len(clusters)+2)
	allowed[0] = true
	allowed[len(runes)] = true
	for _, cluster := range clusters {
		if cluster.StartRune >= 0 && cluster.StartRune <= len(runes) {
			allowed[cluster.StartRune] = true
		}
		if cluster.EndRune >= 0 && cluster.EndRune <= len(runes) {
			allowed[cluster.EndRune] = true
		}
	}
	kept := make([]int, 0, len(graphemes))
	for _, boundary := range graphemes {
		if allowed[boundary] {
			kept = append(kept, boundary)
		}
	}
	return kept
}

// snapToTextBoundary returns the boundary nearest to index; an exact
// boundary stays put and a tie breaks toward the lower boundary.
func snapToTextBoundary(boundaries []int, index int) int {
	if len(boundaries) == 0 {
		return index
	}
	position := sort.SearchInts(boundaries, index)
	if position < len(boundaries) && boundaries[position] == index {
		return index
	}
	lower := boundaries[0]
	if position > 0 {
		lower = boundaries[position-1]
	}
	if position >= len(boundaries) {
		return lower
	}
	upper := boundaries[position]
	if index-lower <= upper-index {
		return lower
	}
	return upper
}

// boundaryAtOrBefore returns the greatest boundary <= index, clamped to the
// first boundary. It is the deletion start for Backspace-style edits.
func boundaryAtOrBefore(boundaries []int, index int) int {
	if len(boundaries) == 0 {
		return index
	}
	if index <= boundaries[0] {
		return boundaries[0]
	}
	position := sort.SearchInts(boundaries, index)
	if position < len(boundaries) && boundaries[position] == index {
		return index
	}
	return boundaries[position-1]
}

// boundaryAtOrAfter returns the smallest boundary >= index, clamped to the
// last boundary. It is the deletion end for Delete-style edits and the
// forward caret step.
func boundaryAtOrAfter(boundaries []int, index int) int {
	if len(boundaries) == 0 {
		return index
	}
	if index >= boundaries[len(boundaries)-1] {
		return boundaries[len(boundaries)-1]
	}
	return boundaries[sort.SearchInts(boundaries, index)]
}

// nextTextBoundary returns the smallest boundary strictly greater than
// index, or len(runes) when index is at or past the end.
func nextTextBoundary(boundaries []int, index int) int {
	if len(boundaries) == 0 {
		return index + 1
	}
	return boundaryAtOrAfter(boundaries, index+1)
}

// prevTextBoundary returns the greatest boundary strictly less than index,
// or 0 when index is at or before the start.
func prevTextBoundary(boundaries []int, index int) int {
	if len(boundaries) == 0 {
		return max(0, index-1)
	}
	return boundaryAtOrBefore(boundaries, max(0, index-1))
}

// nearestTextBoundary maps a content-relative x to the caret boundary whose
// visual offset is closest; a tie keeps the lower boundary. Restricting the
// search to editable boundaries makes hit-testing cluster-safe by
// construction and direction-agnostic, because the offsets already encode
// the visual direction of every boundary.
func nearestTextBoundary(offsets []float64, boundaries []int, x float64) int {
	if len(boundaries) == 0 {
		return 0
	}
	best := boundaries[0]
	bestDistance := math.Abs(offsets[best] - x)
	for _, boundary := range boundaries[1:] {
		if distance := math.Abs(offsets[boundary] - x); distance < bestDistance {
			best, bestDistance = boundary, distance
		}
	}
	return best
}
