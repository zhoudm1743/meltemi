package style

import (
	"fmt"
	"strconv"
	"strings"
)

// Color is an RGBA color with 8 bits per channel.
type Color struct {
	R, G, B, A uint8
}

// ParseColor parses a hex color (#RGB, #RRGGBB, #RRGGBBAA) or one of the
// named colors white, black, transparent, red, green, blue and gray.
func ParseColor(s string) (Color, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		return parseHexColor(s[1:])
	}
	switch strings.ToLower(s) {
	case "white":
		return Color{0xff, 0xff, 0xff, 0xff}, nil
	case "black":
		return Color{0, 0, 0, 0xff}, nil
	case "transparent":
		return Color{0, 0, 0, 0}, nil
	case "red":
		return Color{0xff, 0, 0, 0xff}, nil
	case "green":
		return Color{0, 0x80, 0, 0xff}, nil
	case "blue":
		return Color{0, 0, 0xff, 0xff}, nil
	case "gray":
		return Color{0x80, 0x80, 0x80, 0xff}, nil
	}
	return Color{}, fmt.Errorf("unsupported color %q: use #RGB, #RRGGBB, #RRGGBBAA or a named color (white, black, transparent, red, green, blue, gray)", s)
}

func parseHexColor(hex string) (Color, error) {
	invalid := fmt.Errorf("invalid hex color %q: use #RGB, #RRGGBB or #RRGGBBAA", "#"+hex)
	n := len(hex)
	if n != 3 && n != 6 && n != 8 {
		return Color{}, invalid
	}
	digits := make([]byte, 0, 4)
	if n == 3 {
		for i := 0; i < 3; i++ {
			v, ok := hexNibble(hex[i])
			if !ok {
				return Color{}, invalid
			}
			digits = append(digits, v<<4|v)
		}
		digits = append(digits, 0xff)
	} else {
		for i := 0; i < n; i += 2 {
			hi, ok1 := hexNibble(hex[i])
			lo, ok2 := hexNibble(hex[i+1])
			if !ok1 || !ok2 {
				return Color{}, invalid
			}
			digits = append(digits, hi<<4|lo)
		}
		if n == 6 {
			digits = append(digits, 0xff)
		}
	}
	return Color{R: digits[0], G: digits[1], B: digits[2], A: digits[3]}, nil
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// Dimension is a length in logical units. Only px and bare 0 exist.
type Dimension struct {
	Value float64
}

// ParseDimension parses "0" (with any number of zero decimals) or a
// non-negative px length such as "12px". Other units and percentages are
// rejected.
func ParseDimension(s string) (Dimension, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Dimension{}, fmt.Errorf("empty length: use a px value such as \"12px\" or bare 0")
	}
	if strings.HasSuffix(s, "px") {
		v, ok := parseNumber(s[:len(s)-2])
		if !ok {
			return Dimension{}, fmt.Errorf("invalid length %q: px values must be non-negative numbers", s)
		}
		return Dimension{Value: v}, nil
	}
	if v, ok := parseNumber(s); ok && v == 0 {
		return Dimension{Value: 0}, nil
	}
	return Dimension{}, fmt.Errorf("unsupported length %q: only px units and bare 0 are supported", s)
}

// ParseOpacity parses a CSS number in the inclusive range [0, 1].
func ParseOpacity(s string) (float64, error) {
	s = strings.TrimSpace(s)
	value, ok := parseCSSNumber(s)
	if !ok || value < 0 || value > 1 {
		return 0, fmt.Errorf("invalid opacity %q: use a number from 0 to 1", s)
	}
	return value, nil
}

// parseCSSNumber scans an optional sign, decimal significand and optional
// exponent. It is separate from parseNumber so dimensions keep their narrower
// unsigned grammar. A trailing decimal point and non-finite names are rejected.
func parseCSSNumber(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	i := 0
	if s[i] == '+' || s[i] == '-' {
		i++
		if i == len(s) {
			return 0, false
		}
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		fractionStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
		if i == fractionStart {
			return 0, false
		}
	}
	if digits == 0 {
		return 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exponentStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == exponentStart {
			return 0, false
		}
	}
	if i != len(s) {
		return 0, false
	}
	value, err := strconv.ParseFloat(s, 64)
	if err != nil && value != 0 {
		return 0, false
	}
	return value, true
}

// parseNumber scans a non-negative decimal literal: digits with at most
// one dot. It rejects signs, exponents, a trailing dot without fractional
// digits ("1.") and empty input, while ".5" stays valid.
func parseNumber(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	intPart := s
	fracPart := ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
		if fracPart == "" {
			return 0, false
		}
	}
	if intPart == "" && fracPart == "" {
		return 0, false
	}
	if !isDigits(intPart) || !isDigits(fracPart) {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// isDigits reports whether s is non-empty ASCII digits. An empty intPart
// is allowed (".5") because the caller checks that at least one side is
// non-empty and the fractional side is non-empty when a dot exists.
func isDigits(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Border is a parsed border shorthand: a width and a color with a solid
// style.
type Border struct {
	Width Dimension
	Color Color
}

// ParseBorder parses "<width> solid <color>"; the style keyword must be
// exactly one word and only solid is supported.
func ParseBorder(s string) (Border, error) {
	fields := strings.Fields(s)
	if len(fields) != 3 {
		return Border{}, fmt.Errorf("border requires \"<width> solid <color>\", got %q", s)
	}
	if !strings.EqualFold(fields[1], "solid") {
		return Border{}, fmt.Errorf("unsupported border style %q: only \"solid\" is supported", fields[1])
	}
	width, err := ParseDimension(fields[0])
	if err != nil {
		return Border{}, fmt.Errorf("invalid border width: %v", err)
	}
	color, err := ParseColor(fields[2])
	if err != nil {
		return Border{}, fmt.Errorf("invalid border color: %v", err)
	}
	return Border{Width: width, Color: color}, nil
}

// ParsePadding parses one to four whitespace-separated dimensions. The
// CSS shorthand expansion into individual sides is left to the consumer.
func ParsePadding(s string) ([]Dimension, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil, fmt.Errorf("padding requires at least one value")
	}
	if len(fields) > 4 {
		return nil, fmt.Errorf("padding accepts at most four values, got %d", len(fields))
	}
	dims := make([]Dimension, len(fields))
	for i, f := range fields {
		d, err := ParseDimension(f)
		if err != nil {
			return nil, fmt.Errorf("invalid padding value %q: %v", f, err)
		}
		dims[i] = d
	}
	return dims, nil
}
