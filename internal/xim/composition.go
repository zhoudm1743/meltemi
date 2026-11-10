package xim

import "unicode/utf8"

// Preedit is the current uncommitted input-method text. Caret is a rune index.
type Preedit struct {
	Text   string
	Caret  int
	Active bool
}

type composition struct {
	preedit Preedit
}

func (c *composition) start() {
	c.preedit = Preedit{Active: true}
}

func (c *composition) draw(text string, caret int) {
	c.preedit.Text = text
	c.preedit.Active = true
	c.moveCaret(caret)
}

func (c *composition) done() {
	c.preedit = Preedit{}
}

func (c *composition) commit() {
	c.preedit = Preedit{}
}

func (c *composition) cancel() {
	c.preedit = Preedit{}
}

func (c *composition) moveCaret(caret int) {
	length := utf8.RuneCountInString(c.preedit.Text)
	if caret < 0 {
		caret = 0
	}
	if caret > length {
		caret = length
	}
	c.preedit.Caret = caret
}

func (c *composition) snapshot() Preedit {
	return c.preedit
}
