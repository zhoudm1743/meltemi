package style

import (
	"fmt"
	"io"
	"strings"
)

var componentTypes = map[string]bool{
	"Column":   true,
	"Row":      true,
	"Stack":    true,
	"Scroll":   true,
	"Spacer":   true,
	"Label":    true,
	"Button":   true,
	"Input":    true,
	"Password": true,
	"TextArea": true,
	"Checkbox": true,
	"Image":    true,
	"MenuBar":  true,
	"MenuItem": true,
}

var statesByName = map[string]State{
	"hover":    StateHover,
	"focus":    StateFocus,
	"disabled": StateDisabled,
	"checked":  StateChecked,
	"pressed":  StatePressed,
}

// Parse reads the supported CSS subset from r and returns the parsed
// sheet. Rules and declarations keep their source order. The first
// syntax or value error aborts the parse and is reported with its 1-based
// line and column, plus the property name when one was being read.
func Parse(r io.Reader) (*Sheet, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	p := &parser{src: []rune(string(data)), line: 1, col: 1}
	sheet := &Sheet{}
	for {
		if err := p.skipSpaceComments(); err != nil {
			return nil, err
		}
		if _, ok := p.peek(); !ok {
			return sheet, nil
		}
		rule, err := p.parseRule()
		if err != nil {
			return nil, err
		}
		sheet.Rules = append(sheet.Rules, rule)
	}
}

type parser struct {
	src  []rune
	pos  int
	line int
	col  int
}

func (p *parser) errorf(line, col int, format string, args ...any) *Error {
	return &Error{Line: line, Column: col, Message: fmt.Sprintf(format, args...)}
}

func (p *parser) peek() (rune, bool) {
	if p.pos >= len(p.src) {
		return 0, false
	}
	return p.src[p.pos], true
}

func (p *parser) peekAt(off int) (rune, bool) {
	if p.pos+off >= len(p.src) {
		return 0, false
	}
	return p.src[p.pos+off], true
}

func (p *parser) advance() (rune, bool) {
	r, ok := p.peek()
	if !ok {
		return 0, false
	}
	p.pos++
	if r == '\n' {
		p.line++
		p.col = 1
	} else {
		p.col++
	}
	return r, true
}

// skipSpaceComments consumes whitespace and /* */ comments, which act as
// whitespace everywhere in the subset.
func (p *parser) skipSpaceComments() error {
	for {
		r, ok := p.peek()
		if !ok {
			return nil
		}
		if isSpace(r) {
			p.advance()
			continue
		}
		if r == '/' {
			if n, ok := p.peekAt(1); ok && n == '*' {
				if err := p.skipComment(); err != nil {
					return err
				}
				continue
			}
		}
		return nil
	}
}

// skipComment consumes a comment after the opening "/*" has been seen.
// The caller must have verified the opening delimiter.
func (p *parser) skipComment() error {
	startLine, startCol := p.line, p.col
	p.advance()
	p.advance()
	for {
		r, ok := p.peek()
		if !ok {
			return p.errorf(startLine, startCol, "unterminated comment")
		}
		if r == '*' {
			if n, ok := p.peekAt(1); ok && n == '/' {
				p.advance()
				p.advance()
				return nil
			}
		}
		p.advance()
	}
}

func (p *parser) parseRule() (Rule, error) {
	startLine := p.line
	sel, err := p.parseSelector()
	if err != nil {
		return Rule{}, err
	}
	if err := p.skipSpaceComments(); err != nil {
		return Rule{}, err
	}
	if r, ok := p.peek(); !ok || r != '{' {
		return Rule{}, p.errorf(p.line, p.col, "expected '{' after selector")
	}
	p.advance()

	rule := Rule{Selector: sel, Line: startLine}
	for {
		if err := p.skipSpaceComments(); err != nil {
			return Rule{}, err
		}
		r, ok := p.peek()
		if !ok {
			return Rule{}, p.errorf(p.line, p.col, "unexpected end of input inside rule, expected '}'")
		}
		if r == '}' {
			p.advance()
			return rule, nil
		}
		if r == ';' {
			p.advance()
			continue
		}
		prop, err := p.parseDeclaration()
		if err != nil {
			return Rule{}, err
		}
		rule.Properties = append(rule.Properties, prop)
	}
}

func (p *parser) parseSelector() (Selector, error) {
	startLine, startCol := p.line, p.col
	r, ok := p.peek()
	if !ok {
		return Selector{}, p.errorf(startLine, startCol, "expected selector")
	}
	var sel Selector
	switch {
	case r == '@':
		p.advance()
		name, _ := p.readIdent()
		return Selector{}, p.errorf(startLine, startCol, "at-rules are not supported (found \"@%s\")", name)
	case r == '*':
		return Selector{}, p.errorf(startLine, startCol, "the universal selector '*' is not supported")
	case r == '.':
		p.advance()
		name, ok := p.readName()
		if !ok {
			return Selector{}, p.errorf(startLine, startCol, "expected class name after '.'")
		}
		sel = Selector{Kind: ClassSelector, Name: name}
	case r == '#':
		p.advance()
		name, ok := p.readName()
		if !ok {
			return Selector{}, p.errorf(startLine, startCol, "expected id name after '#'")
		}
		sel = Selector{Kind: IDSelector, Name: name}
	case isIdentStart(r):
		name, _ := p.readIdent()
		if !componentTypes[name] {
			return Selector{}, p.errorf(startLine, startCol, "unknown component type %q (supported: Column, Row, Stack, Scroll, Spacer, Label, Button, Input, Password, TextArea, Checkbox, Image, MenuBar, MenuItem)", name)
		}
		sel = Selector{Kind: TypeSelector, Name: name}
	default:
		return Selector{}, p.errorf(startLine, startCol, "expected a type, class or id selector")
	}

	if r, ok := p.peek(); ok && r == ':' {
		p.advance()
		stateLine, stateCol := p.line, p.col
		if n, ok := p.peek(); ok && n == ':' {
			return Selector{}, p.errorf(stateLine, stateCol, "pseudo-elements are not supported")
		}
		name, ok := p.readIdent()
		if !ok {
			return Selector{}, p.errorf(stateLine, stateCol, "expected state name after ':'")
		}
		state, ok := statesByName[strings.ToLower(name)]
		if !ok {
			return Selector{}, p.errorf(stateLine, stateCol, "unknown state %q (supported: hover, focus, disabled, checked, pressed)", name)
		}
		sel.State = state
	}

	if err := p.skipSpaceComments(); err != nil {
		return Selector{}, err
	}
	r, ok = p.peek()
	if !ok || r == '{' {
		return sel, nil
	}
	switch {
	case r == ',':
		return Selector{}, p.errorf(p.line, p.col, "grouped selectors (',') are not supported")
	case r == '>' || r == '+' || r == '~':
		return Selector{}, p.errorf(p.line, p.col, "combinators are not supported")
	case r == ':':
		return Selector{}, p.errorf(p.line, p.col, "only one state may be specified")
	default:
		return Selector{}, p.errorf(p.line, p.col, "only a single type, class or id selector is supported")
	}
}

func (p *parser) parseDeclaration() (Property, error) {
	nameLine, nameCol := p.line, p.col
	if r, ok := p.peek(); ok && r == '@' {
		p.advance()
		name, _ := p.readIdent()
		return Property{}, p.errorf(nameLine, nameCol, "at-rules are not supported (found \"@%s\")", name)
	}
	name, ok := p.readIdent()
	if !ok {
		return Property{}, p.errorf(nameLine, nameCol, "expected property name")
	}
	// From here on every failure belongs to this declaration, so attach
	// the property name to each error.
	propErr := func(line, col int, format string, args ...any) *Error {
		e := p.errorf(line, col, format, args...)
		e.Property = name
		return e
	}
	if err := p.skipSpaceComments(); err != nil {
		return Property{}, attachProperty(err, name)
	}
	if r, ok := p.peek(); !ok || r != ':' {
		return Property{}, propErr(nameLine, nameCol, "expected ':' after property name %q", name)
	}
	p.advance()

	value, err := p.readValue()
	if err != nil {
		return Property{}, attachProperty(err, name)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return Property{}, propErr(nameLine, nameCol, "missing value")
	}
	if hasBangOutsideQuotes(value) {
		return Property{}, propErr(nameLine, nameCol, "!important is not supported")
	}
	value, err = validateValue(name, value)
	if err != nil {
		return Property{}, propErr(nameLine, nameCol, "%s", err.Error())
	}
	return Property{Name: name, Value: value, Line: nameLine, Column: nameCol}, nil
}

// attachProperty adds a property name to a parse error that does not
// carry one yet, so declaration errors always name their property.
func attachProperty(err error, name string) error {
	if e, ok := err.(*Error); ok && e.Property == "" {
		e.Property = name
	}
	return err
}

// hasBangOutsideQuotes reports whether value contains '!' outside string
// literals. Inside a quoted font-family name any '!' is legal, including
// escaped quotes and escaped backslashes, which never change the quote
// state; a real bang only counts when it appears outside quotes.
func hasBangOutsideQuotes(value string) bool {
	rs := []rune(value)
	var quote rune
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if quote != 0 {
			if r == '\\' && i+1 < len(rs) {
				i++ // skip the escaped rune entirely
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '!' {
			return true
		}
	}
	return false
}

// readValue consumes a declaration value up to (but not including) the
// next ';' or '}' outside quotes. Comments are replaced by a space. The
// value is kept as raw source: quotes stay and escape backslashes stay, so
// downstream scans can tell an escaped quote from a real closing quote;
// validateValue later resolves escapes for font-family. The error location
// is the start of the value.
func (p *parser) readValue() (string, error) {
	startLine, startCol := p.line, p.col
	var b strings.Builder
	var quote rune
	for {
		r, ok := p.peek()
		if !ok {
			if quote != 0 {
				return "", p.errorf(startLine, startCol, "unterminated string literal")
			}
			return b.String(), nil
		}
		if quote != 0 {
			switch {
			case r == '\\':
				// Keep the escape verbatim ("\_" stays two runes) so
				// downstream scans can tell an escaped quote from a real
				// closing quote; either way the quote state must not
				// change here.
				b.WriteRune(r)
				p.advance()
				if n, ok := p.peek(); ok {
					b.WriteRune(n)
					p.advance()
				}
			case r == quote:
				quote = 0
				b.WriteRune(r)
				p.advance()
			default:
				b.WriteRune(r)
				p.advance()
			}
			continue
		}
		switch {
		case r == ';' || r == '}':
			return b.String(), nil
		case r == '\'' || r == '"':
			quote = r
			b.WriteRune(r)
			p.advance()
		case r == '/':
			if n, ok := p.peekAt(1); ok && n == '*' {
				if err := p.skipComment(); err != nil {
					return "", err
				}
				b.WriteByte(' ')
			} else {
				b.WriteRune(r)
				p.advance()
			}
		default:
			b.WriteRune(r)
			p.advance()
		}
	}
}

func (p *parser) readIdent() (string, bool) {
	var b strings.Builder
	for {
		r, ok := p.peek()
		if !ok || !isIdentChar(r) {
			break
		}
		b.WriteRune(r)
		p.advance()
	}
	return b.String(), b.Len() > 0
}

// readName reads a class or id name. The first rune must be a letter, '_'
// or '-'; a leading '-' must be followed by another name character, never
// a digit or the end. Later runes may also be digits. This keeps '.123'
// and '#-' out while allowing '-lead' and 'primary-2'.
func (p *parser) readName() (string, bool) {
	r, ok := p.peek()
	if !ok {
		return "", false
	}
	var b strings.Builder
	switch {
	case isIdentStart(r):
		b.WriteRune(r)
		p.advance()
	case r == '-':
		n, ok := p.peekAt(1)
		if !ok || !isIdentStart(n) && n != '-' {
			return "", false
		}
		b.WriteRune(r)
		p.advance()
	default:
		return "", false
	}
	for {
		r, ok := p.peek()
		if !ok || !isIdentChar(r) {
			break
		}
		b.WriteRune(r)
		p.advance()
	}
	return b.String(), true
}

// validateValue checks a declaration value against the property's value
// grammar. The property name itself is validated here as well. It returns
// the value to store, which differs from the raw text only for
// font-family, where quotes are removed and escapes resolved.
func validateValue(name, value string) (string, error) {
	switch name {
	case "color", "background", "background-color", "border-color":
		_, err := ParseColor(value)
		return value, err
	case "font-size", "border-width", "border-radius", "width", "height", "gap":
		_, err := ParseDimension(value)
		return value, err
	case "opacity":
		_, err := ParseOpacity(value)
		return value, err
	case "padding":
		_, err := ParsePadding(value)
		return value, err
	case "border":
		_, err := ParseBorder(value)
		return value, err
	case "font-family":
		return validateFontFamily(value)
	default:
		return "", fmt.Errorf("unsupported property %q", name)
	}
}

// validateFontFamily accepts a single unquoted identifier or a single
// quoted string in single or double quotes. Font lists are not supported.
// The value is raw source, so escapes are honoured while scanning: only an
// unescaped quote closes the string, and the closing quote must be the
// last rune of the value (anything after it is rejected). On success it
// returns the family name with quotes removed and escapes resolved, which
// is the name a font registry should see.
func validateFontFamily(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("empty font-family")
	}
	rs := []rune(value)
	first := rs[0]
	if first == '\'' || first == '"' {
		var name []rune
		for i := 1; i < len(rs); i++ {
			r := rs[i]
			if r == '\\' && i+1 < len(rs) {
				name = append(name, rs[i+1])
				i++
				continue
			}
			if r == first {
				if i != len(rs)-1 {
					return "", fmt.Errorf("font-family %q must be a single quoted string or identifier", value)
				}
				if len(name) == 0 {
					return "", fmt.Errorf("empty font-family name")
				}
				return string(name), nil
			}
			name = append(name, r)
		}
		return "", fmt.Errorf("unterminated font-family string %q", value)
	}
	if !isIdentStart(first) {
		return "", fmt.Errorf("invalid font-family %q: use an identifier or a quoted string", value)
	}
	for _, r := range rs[1:] {
		if !isIdentChar(r) {
			return "", fmt.Errorf("font-family %q must be a single quoted string or identifier", value)
		}
	}
	return value, nil
}

func isSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

func isIdentStart(r rune) bool {
	return r == '_' || isLetter(r)
}

func isIdentChar(r rune) bool {
	return r == '_' || r == '-' || isLetter(r) || r >= '0' && r <= '9'
}

func isLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 0x7f
}
