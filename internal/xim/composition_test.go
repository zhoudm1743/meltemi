package xim

import "testing"

func TestCompositionStartDrawAndCommit(t *testing.T) {
	var c composition
	c.start()
	c.draw("ni", 1)

	want := Preedit{Text: "ni", Caret: 1, Active: true}
	if got := c.snapshot(); got != want {
		t.Fatalf("preedit = %+v, want %+v", got, want)
	}

	c.commit()
	if got := c.snapshot(); got != (Preedit{}) {
		t.Fatalf("preedit after commit = %+v, want empty", got)
	}
}

func TestCompositionStartResetsPreviousText(t *testing.T) {
	c := composition{preedit: Preedit{Text: "旧", Caret: 1, Active: true}}
	c.start()

	want := Preedit{Caret: 0, Active: true}
	if got := c.snapshot(); got != want {
		t.Fatalf("preedit = %+v, want %+v", got, want)
	}
}

func TestCompositionDoneAndCancelClearState(t *testing.T) {
	for _, test := range []struct {
		name string
		end  func(*composition)
	}{
		{name: "done", end: (*composition).done},
		{name: "cancel", end: (*composition).cancel},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := composition{preedit: Preedit{Text: "拼音", Caret: 2, Active: true}}
			test.end(&c)
			if got := c.snapshot(); got != (Preedit{}) {
				t.Fatalf("preedit = %+v, want empty", got)
			}
		})
	}
}

func TestCompositionCaretIsAClampedRuneIndex(t *testing.T) {
	var c composition
	c.start()
	c.draw("你a🙂", 99)
	if got := c.snapshot(); got != (Preedit{Text: "你a🙂", Caret: 3, Active: true}) {
		t.Fatalf("clamped draw caret = %+v", got)
	}

	c.moveCaret(-4)
	if got := c.snapshot().Caret; got != 0 {
		t.Fatalf("negative caret = %d, want 0", got)
	}
	c.moveCaret(2)
	if got := c.snapshot().Caret; got != 2 {
		t.Fatalf("caret = %d, want 2", got)
	}
	c.moveCaret(8)
	if got := c.snapshot().Caret; got != 3 {
		t.Fatalf("large caret = %d, want 3", got)
	}
}
