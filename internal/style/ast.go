// Package style implements a small CSS subset used to theme Meltemi widgets.
// A sheet is a list of rules; each rule has a single type, class or id
// selector with one optional state and a fixed set of declarations.
// Anything outside the subset is rejected with a source location.
package style

import (
	"fmt"
	"strings"
)

// SelectorKind identifies which part of a component a selector matches.
type SelectorKind uint8

const (
	TypeSelector SelectorKind = iota
	IDSelector
	ClassSelector
)

// State is the interactive state a selector targets.
type State uint8

const (
	StateNone State = iota
	StateHover
	StateFocus
	StateDisabled
	StateChecked
	StatePressed
)

// Selector is one simple selector: a single type, class or id plus an
// optional state. Compound, descendant and grouped selectors are not
// supported.
type Selector struct {
	Kind  SelectorKind
	Name  string
	State State
}

// Property is a single declaration with its source location. Line and
// Column are 1-based and point at the property name.
type Property struct {
	Name   string
	Value  string
	Line   int
	Column int
}

// Rule is one selector block. Properties keep declaration order.
type Rule struct {
	Selector   Selector
	Properties []Property
	Line       int
}

// Sheet is a parsed style sheet. Rules keep document order so that later
// rules override earlier ones at the same priority.
type Sheet struct {
	Rules []Rule
}

// Error describes a parse failure at a source location. Property is set
// when the failure belongs to a specific declaration.
type Error struct {
	File     string
	Line     int
	Column   int
	Property string
	Message  string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("css: ")
	if e.File != "" {
		b.WriteString(e.File)
		b.WriteByte(':')
	}
	fmt.Fprintf(&b, "%d:%d", e.Line, e.Column)
	if e.Property != "" {
		fmt.Fprintf(&b, " (property %q)", e.Property)
	}
	b.WriteString(": ")
	b.WriteString(e.Message)
	return b.String()
}

// WithFile returns a copy of e annotated with the source file name, so
// callers that read from a named file can attach it to parse errors.
func (e *Error) WithFile(name string) *Error {
	c := *e
	c.File = name
	return &c
}
