package style

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Sheet {
	t.Helper()
	sheet, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse(%q) returned error: %v", src, err)
	}
	if sheet == nil {
		t.Fatalf("Parse(%q) returned nil sheet", src)
	}
	return sheet
}

func TestMenuStyleSelectors(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   Selector
	}{
		{"MenuBar { color: red; opacity: 0.5 }", Selector{Kind: TypeSelector, Name: "MenuBar"}},
		{"MenuItem:hover { color: red }", Selector{Kind: TypeSelector, Name: "MenuItem", State: StateHover}},
		{"MenuItem:pressed { color: red }", Selector{Kind: TypeSelector, Name: "MenuItem", State: StatePressed}},
		{"MenuItem:disabled { color: red }", Selector{Kind: TypeSelector, Name: "MenuItem", State: StateDisabled}},
		{"MenuItem:checked { color: red }", Selector{Kind: TypeSelector, Name: "MenuItem", State: StateChecked}},
	} {
		sheet := mustParse(t, tc.source)
		if got := sheet.Rules[0].Selector; got != tc.want {
			t.Errorf("selector %q = %+v, want %+v", tc.source, got, tc.want)
		}
	}
}

func TestParseSelectorsAndRules(t *testing.T) {
	t.Run("order and locations", func(t *testing.T) {
		sheet := mustParse(t, "Button { color: red; background-color: #fff }\n"+
			"#go { width: 10px; }\n"+
			".item { padding: 1px 2px; }\n")

		want := []Rule{
			{
				Selector: Selector{Kind: TypeSelector, Name: "Button"},
				Line:     1,
				Properties: []Property{
					{Name: "color", Value: "red", Line: 1, Column: 10},
					{Name: "background-color", Value: "#fff", Line: 1, Column: 22},
				},
			},
			{
				Selector: Selector{Kind: IDSelector, Name: "go"},
				Line:     2,
				Properties: []Property{
					{Name: "width", Value: "10px", Line: 2, Column: 7},
				},
			},
			{
				Selector: Selector{Kind: ClassSelector, Name: "item"},
				Line:     3,
				Properties: []Property{
					{Name: "padding", Value: "1px 2px", Line: 3, Column: 9},
				},
			},
		}
		if !reflect.DeepEqual(sheet.Rules, want) {
			t.Errorf("rules mismatch\n got: %#v\nwant: %#v", sheet.Rules, want)
		}
	})

	t.Run("component types", func(t *testing.T) {
		for _, name := range []string{"Column", "Row", "Stack", "Scroll", "Spacer", "Label", "Button", "Input", "Password", "TextArea", "Checkbox", "Image"} {
			sheet := mustParse(t, name+" { color: red }")
			got := sheet.Rules[0].Selector
			if want := (Selector{Kind: TypeSelector, Name: name}); got != want {
				t.Errorf("selector for %q = %+v, want %+v", name, got, want)
			}
		}
	})

	t.Run("states", func(t *testing.T) {
		cases := []struct {
			sel  string
			want Selector
		}{
			{"Button:hover", Selector{Kind: TypeSelector, Name: "Button", State: StateHover}},
			{"Button:focus", Selector{Kind: TypeSelector, Name: "Button", State: StateFocus}},
			{"Button:disabled", Selector{Kind: TypeSelector, Name: "Button", State: StateDisabled}},
			{"TextArea:checked", Selector{Kind: TypeSelector, Name: "TextArea", State: StateChecked}},
			{"Button:pressed", Selector{Kind: TypeSelector, Name: "Button", State: StatePressed}},
			{".field:hover", Selector{Kind: ClassSelector, Name: "field", State: StateHover}},
			{"#login:focus", Selector{Kind: IDSelector, Name: "login", State: StateFocus}},
			{".primary-button2", Selector{Kind: ClassSelector, Name: "primary-button2"}},
			{"#user_name", Selector{Kind: IDSelector, Name: "user_name"}},
			{".-lead", Selector{Kind: ClassSelector, Name: "-lead"}},
			{"#_private", Selector{Kind: IDSelector, Name: "_private"}},
			{"#a1", Selector{Kind: IDSelector, Name: "a1"}},
			{".lead-", Selector{Kind: ClassSelector, Name: "lead-"}},
		}
		for _, tc := range cases {
			sheet := mustParse(t, tc.sel+" { color: red }")
			if got := sheet.Rules[0].Selector; got != tc.want {
				t.Errorf("selector %q = %+v, want %+v", tc.sel, got, tc.want)
			}
		}
	})

	t.Run("comments", func(t *testing.T) {
		src := "/* leading */\n" +
			"Button /* in selector */ {\n" +
			"  /* before */ color: /* in value */ red; /* after */\n" +
			"  background-color: blue;\n" +
			"}\n" +
			"/* trailing */"
		sheet := mustParse(t, src)
		if len(sheet.Rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(sheet.Rules))
		}
		props := sheet.Rules[0].Properties
		if len(props) != 2 || props[0].Value != "red" || props[1].Value != "blue" {
			t.Errorf("unexpected properties: %#v", props)
		}
	})

	t.Run("whitespace", func(t *testing.T) {
		sheet := mustParse(t, "\t\n  Button\n{\n\tcolor:\tred\n}\n")
		props := sheet.Rules[0].Properties
		if len(props) != 1 || props[0].Name != "color" || props[0].Value != "red" {
			t.Errorf("unexpected properties: %#v", props)
		}
	})
}

func TestParseValuesAndBorder(t *testing.T) {
	decls := []struct {
		decl  string
		name  string
		value string
	}{
		{"color: #abc", "color", "#abc"},
		{"color: #aabbcc", "color", "#aabbcc"},
		{"color: #aabbccdd", "color", "#aabbccdd"},
		{"color: white", "color", "white"},
		{"color: black", "color", "black"},
		{"color: transparent", "color", "transparent"},
		{"color: red", "color", "red"},
		{"color: green", "color", "green"},
		{"color: blue", "color", "blue"},
		{"color: gray", "color", "gray"},
		{"background: #123456", "background", "#123456"},
		{"background-color: #654321", "background-color", "#654321"},
		{"border-color: blue", "border-color", "blue"},
		{"border-width: 2px", "border-width", "2px"},
		{"border-radius: 4px", "border-radius", "4px"},
		{"width: 200px", "width", "200px"},
		{"height: 0", "height", "0"},
		{"gap: 8px", "gap", "8px"},
		{"font-size: 14px", "font-size", "14px"},
		{"font-family: SourceHan", "font-family", "SourceHan"},
		{`font-family: "Source Han"`, "font-family", "Source Han"},
		{"font-family: 'Hei'", "font-family", "Hei"},
		{`font-family: "A!"`, "font-family", "A!"},
		{"font-family: 'B!'", "font-family", "B!"},
		{`font-family: "A\"B"`, "font-family", `A"B`},
		{`font-family: "A\"!B"`, "font-family", `A"!B`},
		{`font-family: 'A\'B'`, "font-family", "A'B"},
		{`font-family: 'A\\B'`, "font-family", `A\B`},
		{"border: 1px solid red", "border", "1px solid red"},
		{"border: 2px solid #aabbccdd", "border", "2px solid #aabbccdd"},
		{"padding: 4px", "padding", "4px"},
		{"padding: 4px 8px", "padding", "4px 8px"},
		{"padding: 4px 8px 2px", "padding", "4px 8px 2px"},
		{"padding: 1px 2px 3px 4px", "padding", "1px 2px 3px 4px"},
		{"padding: 0", "padding", "0"},
	}
	for _, tc := range decls {
		sheet := mustParse(t, "Button { "+tc.decl+" }")
		props := sheet.Rules[0].Properties
		if len(props) != 1 {
			t.Fatalf("declaration %q produced %d properties, want 1", tc.decl, len(props))
		}
		if props[0].Name != tc.name || props[0].Value != tc.value {
			t.Errorf("declaration %q stored as %q=%q, want %q=%q", tc.decl, props[0].Name, props[0].Value, tc.name, tc.value)
		}
	}
}

func TestParseRejectsUnsupportedSyntax(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"descendant", "Button Label { color: red }", "single"},
		{"compound", "Button.primary { color: red }", "single"},
		{"child combinator", "Button > Label { color: red }", "combinator"},
		{"group", "Button, Label { color: red }", "group"},
		{"universal", "* { color: red }", "universal"},
		{"pseudo element", "Button::before { color: red }", "pseudo-elements"},
		{"two states", "Button:hover:focus { color: red }", "one state"},
		{"unknown state", "Button:drag { color: red }", "drag"},
		{"unknown type", "Div { color: red }", "Div"},
		{"unknown property", "Button { nonsense: red }", "nonsense"},
		{"class starts with digit", ".123 { color: red }", "class name"},
		{"id lone hyphen", "#- { color: red }", "id name"},
		{"id hyphen digit", "#-2 { color: red }", "id name"},
		{"two font strings", `Button { font-family: "A" "B" }`, "single"},
		{"font string trailing ident", "Button { font-family: 'Hei' sans }", "single"},
		{"escaped font string trailing ident", `Button { font-family: 'A\'B' junk }`, "single"},
		{"unterminated escaped font string", `Button { font-family: "A\" }`, "unterminated"},
		{"margin", "Button { margin: 4px }", "margin"},
		{"margin-left", "Button { margin-left: 4px }", "margin"},
		{"media query", "@media (min-width: 100px) { Button { color: red } }", "@media"},
		{"important", "Button { color: red !important }", "important"},
		{"bad border style", "Button { border: 1px dashed red }", "solid"},
		{"bad color", "Button { color: banana }", "banana"},
		{"bad padding count", "Button { padding: 1px 2px 3px 4px 5px }", "padding"},
		{"empty value", "Button { color: }", "value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tc.src))
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want error containing %q", tc.src, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestParseErrorIncludesLocationAndProperty(t *testing.T) {
	_, err := Parse(strings.NewReader("Button {\n  color: banana;\n}\n"))
	if err == nil {
		t.Fatal("expected error for invalid color")
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("error is not *Error: %T (%v)", err, err)
	}
	if se.Line != 2 || se.Column != 3 {
		t.Errorf("location = %d:%d, want 2:3", se.Line, se.Column)
	}
	if se.Property != "color" {
		t.Errorf("property = %q, want %q", se.Property, "color")
	}
	for _, part := range []string{"2:3", "color", "banana"} {
		if !strings.Contains(se.Error(), part) {
			t.Errorf("error message %q does not contain %q", se.Error(), part)
		}
	}

	withFile := se.WithFile("theme.css")
	if !strings.Contains(withFile.Error(), "theme.css:2:3") {
		t.Errorf("error with file = %q, want it to contain %q", withFile.Error(), "theme.css:2:3")
	}
	if se.File != "" {
		t.Errorf("WithFile mutated the original error: %q", se.File)
	}

	_, err = Parse(strings.NewReader("Label { color: red }\n@media screen { }\n"))
	if err == nil {
		t.Fatal("expected error for media query")
	}
	if !errors.As(err, &se) {
		t.Fatalf("error is not *Error: %T (%v)", err, err)
	}
	if se.Line != 2 || se.Column != 1 {
		t.Errorf("media query location = %d:%d, want 2:1", se.Line, se.Column)
	}

	_, err = Parse(strings.NewReader(".a {\n  font-family: \"unterminated;\n}\n"))
	if err == nil {
		t.Fatal("expected error for unterminated string")
	}
	if !errors.As(err, &se) {
		t.Fatalf("error is not *Error: %T (%v)", err, err)
	}
	if se.Property != "font-family" {
		t.Errorf("property = %q, want %q", se.Property, "font-family")
	}
	if se.Line != 2 {
		t.Errorf("line = %d, want 2", se.Line)
	}
}

func TestParseDeclarationErrorsCarryProperty(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"missing colon", "Button { color red; }"},
		{"unterminated comment after name", "Button { color /* open"},
		{"unterminated value string", "Button { color: \"open }"},
	} {
		_, err := Parse(strings.NewReader(tc.src))
		if err == nil {
			t.Fatalf("%s: Parse succeeded, want error", tc.name)
		}
		var se *Error
		if !errors.As(err, &se) {
			t.Fatalf("%s: error is not *Error: %T (%v)", tc.name, err, err)
		}
		if se.Property != "color" {
			t.Errorf("%s: property = %q, want %q (error: %v)", tc.name, se.Property, "color", err)
		}
	}

	sheet := mustParse(t, "Button { color: red; }")
	got := sheet.Rules[0].Properties
	if len(got) != 1 || got[0].Name != "color" || got[0].Value != "red" {
		t.Errorf("valid declaration misparsed: %#v", got)
	}
}

func TestOpacityAcceptsCSSNumbers(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  float64
	}{
		{value: "0", want: 0},
		{value: "1", want: 1},
		{value: ".5", want: 0.5},
		{value: "+0.5", want: 0.5},
		{value: "-0", want: 0},
		{value: "1e-1", want: 0.1},
		{value: "1E-1", want: 0.1},
		{value: ".5e+0", want: 0.5},
		{value: "10e-1", want: 1},
		{value: "+1.0E+0", want: 1},
	} {
		t.Run(tc.value, func(t *testing.T) {
			sheet := mustParse(t, "Button { opacity: "+tc.value+" }")
			if got := sheet.Rules[0].Properties[0].Value; got != tc.value {
				t.Errorf("stored opacity = %q, want original %q", got, tc.value)
			}
			got, err := ParseOpacity(tc.value)
			if err != nil {
				t.Fatalf("ParseOpacity(%q): %v", tc.value, err)
			}
			if got != tc.want {
				t.Errorf("ParseOpacity(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestOpacityRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"-0.01", "1.01", "2", "1e1", "NaN", "Inf", "+Inf", "-Inf", "50%", "0.5px", "solid", "1e-1x", "1e", "1e+", "++0.5", "--0.5", "0x1", "1_0", ".", "1."} {
		t.Run(value, func(t *testing.T) {
			_, err := Parse(strings.NewReader("Button { opacity: " + value + " }"))
			if err == nil {
				t.Fatalf("Parse opacity %q succeeded, want error", value)
			}
			var cssErr *Error
			if !errors.As(err, &cssErr) {
				t.Fatalf("error type = %T, want *Error: %v", err, err)
			}
			if cssErr.Property != "opacity" || !strings.Contains(err.Error(), value) {
				t.Errorf("opacity %q error = %q, want property and rejected value", value, err)
			}
			if strings.Contains(err.Error(), "unsupported property") {
				t.Errorf("opacity %q is still an unsupported property: %v", value, err)
			}
		})
	}
}
