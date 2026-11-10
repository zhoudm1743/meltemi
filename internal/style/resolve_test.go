package style

import (
	"testing"
)

func TestStylePriorityAndState(t *testing.T) {
	sheet := mustParse(t, `
Button { color: #111111; font-size: 10px; }
Button:hover { color: #222222; }
.primary { color: #333333; }
.primary:hover { color: #444444; }
#ok { color: #555555; }
#ok:hover { color: #666666; }
`)

	inherited := Resolved{Values: map[string]string{
		"color":     "#ff0000",
		"font-size": "9px",
	}}

	cases := []struct {
		name string
		meta Meta
		want map[string]string
	}{
		{
			name: "default inherited beats nothing",
			meta: Meta{Type: "Label"},
			want: map[string]string{"color": "#ff0000", "font-size": "9px"},
		},
		{
			name: "type beats default",
			meta: Meta{Type: "Button"},
			want: map[string]string{"color": "#111111", "font-size": "10px"},
		},
		{
			name: "type+state beats type",
			meta: Meta{Type: "Button", Hover: true},
			// Only the highest matching level contributes properties,
			// so the type rule's font-size does not apply; the
			// inherited value stays.
			want: map[string]string{"color": "#222222", "font-size": "9px"},
		},
		{
			name: "class beats type",
			meta: Meta{Type: "Button", Class: "primary"},
			want: map[string]string{"color": "#333333", "font-size": "9px"},
		},
		{
			name: "class+state beats class",
			meta: Meta{Type: "Button", Class: "primary", Hover: true},
			want: map[string]string{"color": "#444444", "font-size": "9px"},
		},
		{
			name: "id beats class",
			meta: Meta{Type: "Button", Class: "primary", ID: "ok"},
			want: map[string]string{"color": "#555555", "font-size": "9px"},
		},
		{
			name: "id+state beats id",
			meta: Meta{Type: "Button", Class: "primary", ID: "ok", Hover: true},
			want: map[string]string{"color": "#666666", "font-size": "9px"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sheet.Resolve(tc.meta, inherited)
			assertValues(t, got, tc.want)
		})
	}

	t.Run("class beats type+state", func(t *testing.T) {
		// Minimal sheet: with Hover active both rules match, so a
		// broken type+state > class ranking would wrongly win here.
		sheet := mustParse(t, `
Button:hover { color: #111111; }
.primary { color: #222222; }
`)
		got := sheet.Resolve(Meta{Type: "Button", Class: "primary", Hover: true}, Resolved{})
		if v := got.Values["color"]; v != "#222222" {
			t.Errorf("color = %q, want %q (class beats type+state)", v, "#222222")
		}
	})

	t.Run("id beats class+state", func(t *testing.T) {
		// Minimal sheet: with Hover active both rules match, so a
		// broken class+state > id ranking would wrongly win here.
		sheet := mustParse(t, `
.primary:hover { color: #111111; }
#ok { color: #222222; }
`)
		got := sheet.Resolve(Meta{Type: "Button", Class: "primary", ID: "ok", Hover: true}, Resolved{})
		if v := got.Values["color"]; v != "#222222" {
			t.Errorf("color = %q, want %q (id beats class+state)", v, "#222222")
		}
	})
}

func TestStyleStateMatching(t *testing.T) {
	sheet := mustParse(t, `
Button:hover { color: #111111; }
Button:focus { color: #222222; }
Button:disabled { color: #333333; }
Button:checked { color: #444444; }
Button:pressed { color: #555555; }
`)

	cases := []struct {
		name string
		meta Meta
		want string
	}{
		{"hover", Meta{Type: "Button", Hover: true}, "#111111"},
		{"focus", Meta{Type: "Button", Focus: true}, "#222222"},
		{"disabled", Meta{Type: "Button", Disabled: true}, "#333333"},
		{"checked", Meta{Type: "Button", Checked: true}, "#444444"},
		{"pressed", Meta{Type: "Button", Pressed: true}, "#555555"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sheet.Resolve(tc.meta, Resolved{})
			if v := got.Values["color"]; v != tc.want {
				t.Errorf("color = %q, want %q", v, tc.want)
			}
		})
	}

	t.Run("no active state", func(t *testing.T) {
		got := sheet.Resolve(Meta{Type: "Button"}, Resolved{})
		if v, ok := got.Values["color"]; ok {
			t.Errorf("color = %q, want no value without an active state", v)
		}
	})

	t.Run("each state matches only its own flag", func(t *testing.T) {
		got := sheet.Resolve(Meta{Type: "Button", Hover: true}, Resolved{})
		if v := got.Values["color"]; v != "#111111" {
			t.Errorf("color = %q, want %q; hover must not match focus/disabled/checked/pressed rules", v, "#111111")
		}
	})
}

func TestStyleLaterRuleWinsAtSamePriority(t *testing.T) {
	sheet := mustParse(t, `
Button { color: #111111; }
Label { color: #999999; }
Button { color: #222222; }
Button:hover { color: #333333; }
Button:hover { color: #444444; }
`)

	cases := []struct {
		name string
		meta Meta
		want string
	}{
		{"duplicate type rules", Meta{Type: "Button"}, "#222222"},
		{"duplicate state rules", Meta{Type: "Button", Hover: true}, "#444444"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sheet.Resolve(tc.meta, Resolved{})
			if v := got.Values["color"]; v != tc.want {
				t.Errorf("color = %q, want %q", v, tc.want)
			}
		})
	}
}

func TestStyleShorthandDeclarationOrder(t *testing.T) {
	tests := []struct {
		name string
		css  string
		want map[string]string
	}{
		{
			name: "same rule shorthand clears earlier longhands",
			css: `Button {
				background-color: #ff0000;
				background: #0000ff;
				border-color: #111111;
				border-width: 9px;
				border: 2px solid #222222;
			}`,
			want: map[string]string{
				"background": "#0000ff",
				"border":     "2px solid #222222",
			},
		},
		{
			name: "same rule longhands override earlier shorthand",
			css: `Button {
				background: #0000ff;
				background-color: #ff0000;
				border: 2px solid #222222;
				border-color: #111111;
				border-width: 9px;
			}`,
			want: map[string]string{
				"background":       "#0000ff",
				"background-color": "#ff0000",
				"border":           "2px solid #222222",
				"border-color":     "#111111",
				"border-width":     "9px",
			},
		},
		{
			name: "later rule shorthand clears earlier longhands",
			css: `Button { background-color: #ff0000; border-color: #111111; border-width: 9px; }
			Button { background: #0000ff; border: 2px solid #222222; }`,
			want: map[string]string{
				"background": "#0000ff",
				"border":     "2px solid #222222",
			},
		},
		{
			name: "later rule longhands override earlier shorthand",
			css: `Button { background: #0000ff; border: 2px solid #222222; }
			Button { background-color: #ff0000; border-color: #111111; border-width: 9px; }`,
			want: map[string]string{
				"background":       "#0000ff",
				"background-color": "#ff0000",
				"border":           "2px solid #222222",
				"border-color":     "#111111",
				"border-width":     "9px",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustParse(t, tt.css).Resolve(Meta{Type: "Button"}, Resolved{})
			assertValues(t, got, tt.want)
		})
	}
}

func TestStyleInheritance(t *testing.T) {
	sheet := mustParse(t, `
Label { background: #222222; }
Button { font-size: 18px; }
`)

	parent := Resolved{Values: map[string]string{
		"font-family": "Inter",
		"font-size":   "14px",
		"color":       "#111111",
		"background":  "#333333",
		"padding":     "4px",
		"border":      "1px solid #000000",
	}}

	t.Run("only font and color inherit", func(t *testing.T) {
		got := sheet.Resolve(Meta{Type: "Column"}, parent)
		want := map[string]string{
			"font-family": "Inter",
			"font-size":   "14px",
			"color":       "#111111",
		}
		assertValues(t, got, want)
	})

	t.Run("own rule overrides inherited", func(t *testing.T) {
		got := sheet.Resolve(Meta{Type: "Button"}, parent)
		if v := got.Values["font-size"]; v != "18px" {
			t.Errorf("font-size = %q, want %q", v, "18px")
		}
	})

	t.Run("no cross-contamination from other type rules", func(t *testing.T) {
		got := sheet.Resolve(Meta{Type: "Column"}, parent)
		for _, k := range []string{"background", "padding", "border"} {
			if v, ok := got.Values[k]; ok {
				t.Errorf("%s = %q leaked into Column, want no value", k, v)
			}
		}
	})

	t.Run("result is a copy", func(t *testing.T) {
		got := sheet.Resolve(Meta{Type: "Column"}, parent)
		got.Values["color"] = "#ffffff"
		got.Values["background"] = "#ffffff"
		if parent.Values["color"] != "#111111" {
			t.Errorf("mutating result changed inherited color to %q", parent.Values["color"])
		}
		if _, ok := parent.Values["background"]; !ok {
			t.Error("mutating result added background to the inherited map")
		}
	})
}

func assertValues(t *testing.T, got Resolved, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if gv, ok := got.Values[k]; !ok {
			t.Errorf("missing %s = %q", k, v)
		} else if gv != v {
			t.Errorf("%s = %q, want %q", k, gv, v)
		}
	}
	for k, gv := range got.Values {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected %s = %q", k, gv)
		}
	}
}
