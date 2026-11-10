package main

import (
	"image/color"
	"strings"
	"testing"

	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
)

func TestDemoConfiguration(t *testing.T) {
	d := newDemo(nil)
	if d.root == nil || d.row == nil || d.stack == nil || d.scroll == nil || d.spacer == nil {
		t.Fatal("demo did not construct Column, Row, Stack, Scroll and Spacer")
	}
	if d.enabled == nil || !d.enabled.Value() || !d.enabled.Enabled() {
		t.Errorf("enabled checkbox = %+v, want checked and enabled", d.enabled)
	}
	if d.disabled == nil || !d.disabled.Value() || d.disabled.Enabled() {
		t.Errorf("disabled checkbox = %+v, want checked and disabled", d.disabled)
	}
	if d.image == nil {
		t.Fatal("demo did not construct its Image")
	}

	sheet, err := internalstyle.Parse(strings.NewReader(demoStyles))
	if err != nil {
		t.Fatalf("demo stylesheet Parse: %v", err)
	}
	for _, tc := range []struct {
		name string
		meta internalstyle.Meta
		want string
	}{
		{name: "nested parent", meta: internalstyle.Meta{Type: "Column", ID: "content"}, want: "0.8"},
		{name: "disabled state", meta: internalstyle.Meta{Type: "Checkbox", Disabled: true}, want: "0.35"},
		{name: "image", meta: internalstyle.Meta{Type: "Image", ID: "art"}, want: "0.75"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sheet.Resolve(tc.meta, internalstyle.Resolved{})
			if got.Values["opacity"] != tc.want {
				t.Errorf("resolved opacity = %q, want %q (all values: %v)", got.Values["opacity"], tc.want, got.Values)
			}
		})
	}
}

func TestDemoImageDeterministic(t *testing.T) {
	img := demoImage()
	if got, want := img.Bounds().Dx(), 4; got != want {
		t.Fatalf("demo image width = %d, want %d", got, want)
	}
	if got, want := img.Bounds().Dy(), 4; got != want {
		t.Fatalf("demo image height = %d, want %d", got, want)
	}
	want := []color.NRGBA{
		{R: 255, A: 255},
		{G: 255, A: 255},
		{B: 255, A: 255},
		{R: 255, G: 255, B: 255, A: 128},
	}
	for x, expected := range want {
		if got := img.NRGBAAt(x, 0); got != expected {
			t.Errorf("top row pixel %d = %+v, want %+v", x, got, expected)
		}
	}
}
