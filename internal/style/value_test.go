package style

import (
	"reflect"
	"testing"
)

func TestParseColor(t *testing.T) {
	cases := []struct {
		in   string
		want Color
		ok   bool
	}{
		{"#abc", Color{0xaa, 0xbb, 0xcc, 0xff}, true},
		{"#ABC", Color{0xaa, 0xbb, 0xcc, 0xff}, true},
		{"#aabbcc", Color{0xaa, 0xbb, 0xcc, 0xff}, true},
		{"#AaBbCc", Color{0xaa, 0xbb, 0xcc, 0xff}, true},
		{"#aabbccdd", Color{0xaa, 0xbb, 0xcc, 0xdd}, true},
		{"#01020304", Color{1, 2, 3, 4}, true},
		{"white", Color{0xff, 0xff, 0xff, 0xff}, true},
		{"black", Color{0, 0, 0, 0xff}, true},
		{"transparent", Color{0, 0, 0, 0}, true},
		{"red", Color{0xff, 0, 0, 0xff}, true},
		{"green", Color{0, 0x80, 0, 0xff}, true},
		{"blue", Color{0, 0, 0xff, 0xff}, true},
		{"gray", Color{0x80, 0x80, 0x80, 0xff}, true},
		{"Gray", Color{0x80, 0x80, 0x80, 0xff}, true},
		{"", Color{}, false},
		{"#", Color{}, false},
		{"#ab", Color{}, false},
		{"#abcde", Color{}, false},
		{"#abcdefg", Color{}, false},
		{"#ggg", Color{}, false},
		{"grey", Color{}, false},
		{"orange", Color{}, false},
		{"red blue", Color{}, false},
	}
	for _, tc := range cases {
		got, err := ParseColor(tc.in)
		switch {
		case tc.ok && err != nil:
			t.Errorf("ParseColor(%q) returned error: %v", tc.in, err)
		case !tc.ok && err == nil:
			t.Errorf("ParseColor(%q) = %+v, want error", tc.in, got)
		case tc.ok && got != tc.want:
			t.Errorf("ParseColor(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseDimension(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"0", 0, true},
		{"0px", 0, true},
		{"0.0", 0, true},
		{"12px", 12, true},
		{"12.5px", 12.5, true},
		{".5px", 0.5, true},
		{" 8px ", 8, true},
		{"", 0, false},
		{"px", 0, false},
		{"12", 0, false},
		{"0.5", 0, false},
		{"12em", 0, false},
		{"12%", 0, false},
		{"-4px", 0, false},
		{"+4px", 0, false},
		{"4 px", 0, false},
		{"1e3px", 0, false},
		{"4.5.6px", 0, false},
		{"4.5.6", 0, false},
		{"1.px", 0, false},
		{"0.", 0, false},
		{"1.", 0, false},
	}
	for _, tc := range cases {
		got, err := ParseDimension(tc.in)
		switch {
		case tc.ok && err != nil:
			t.Errorf("ParseDimension(%q) returned error: %v", tc.in, err)
		case !tc.ok && err == nil:
			t.Errorf("ParseDimension(%q) = %+v, want error", tc.in, got)
		case tc.ok && got.Value != tc.want:
			t.Errorf("ParseDimension(%q) = %+v, want value %v", tc.in, got, tc.want)
		}
	}
}

func TestParseBorder(t *testing.T) {
	ok := []struct {
		in   string
		want Border
	}{
		{"1px solid red", Border{Width: Dimension{1}, Color: Color{0xff, 0, 0, 0xff}}},
		{"0 solid #fff", Border{Width: Dimension{0}, Color: Color{0xff, 0xff, 0xff, 0xff}}},
		{"2px SOLID #aabbccdd", Border{Width: Dimension{2}, Color: Color{0xaa, 0xbb, 0xcc, 0xdd}}},
		{"1.5px solid transparent", Border{Width: Dimension{1.5}, Color: Color{0, 0, 0, 0}}},
	}
	for _, tc := range ok {
		got, err := ParseBorder(tc.in)
		if err != nil {
			t.Errorf("ParseBorder(%q) returned error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseBorder(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}

	bad := []string{
		"",
		"solid red",
		"1px dashed red",
		"1px solid",
		"1px solid red blue",
		"thick solid red",
		"1 px solid red",
		"1px solidred",
		"1px solid #ggg",
	}
	for _, in := range bad {
		if got, err := ParseBorder(in); err == nil {
			t.Errorf("ParseBorder(%q) = %+v, want error", in, got)
		}
	}
}

func TestParsePadding(t *testing.T) {
	ok := []struct {
		in   string
		want []Dimension
	}{
		{"4px", []Dimension{{4}}},
		{"4px 8px", []Dimension{{4}, {8}}},
		{"4px 8px 2px", []Dimension{{4}, {8}, {2}}},
		{"1px 2px 3px 4px", []Dimension{{1}, {2}, {3}, {4}}},
		{"0", []Dimension{{0}}},
	}
	for _, tc := range ok {
		got, err := ParsePadding(tc.in)
		if err != nil {
			t.Errorf("ParsePadding(%q) returned error: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParsePadding(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}

	bad := []string{
		"",
		"  ",
		"1px 2px 3px 4px 5px",
		"1px nope",
		"solid",
		"1px solid red",
	}
	for _, in := range bad {
		if got, err := ParsePadding(in); err == nil {
			t.Errorf("ParsePadding(%q) = %+v, want error", in, got)
		}
	}
}
