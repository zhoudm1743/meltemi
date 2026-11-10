package main

import (
	"testing"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

func TestTitleBarClipsTitleToLayoutAtMinimumWidth(t *testing.T) {
	rec := &recorder{}
	tw := &fakeFrameTarget{rec: rec, name: "win", size: driver.Size{Width: 240, Height: 120}}
	d, rend := newFakeDemo(rec, tw)
	d.init()
	if d.err != nil {
		t.Fatalf("init: %v", d.err)
	}
	layout := d.style.Layout(tw.Size())
	bmp, err := d.face.Rasterize(windowTitle)
	if err != nil {
		t.Fatalf("Rasterize(windowTitle): %v", err)
	}
	if float64(bmp.Width) <= layout.Title.Width {
		t.Fatalf("test fixture title width = %d, layout width = %v; want overflowing text", bmp.Width, layout.Title.Width)
	}
	rend.ops = nil

	if err := drawTitleBar(rend, d.style, layout, d.ctl, d.face); err != nil {
		t.Fatalf("drawTitleBar: %v", err)
	}

	titleIndex := -1
	for i, op := range rend.ops {
		if op.kind == "text" && op.text == windowTitle {
			titleIndex = i
			break
		}
	}
	if titleIndex < 0 {
		t.Fatal("title text was not drawn")
	}
	if titleIndex == 0 {
		t.Fatal("title text was drawn before any clipping operation")
	}
	if got, want := rend.ops[titleIndex-1], (renderOperation{kind: "clip", rect: layout.Title}); got != want {
		t.Errorf("operation before title = %+v, want %+v so text cannot enter the button area", got, want)
	}
	if titleIndex+1 >= len(rend.ops) || rend.ops[titleIndex+1].kind != "clear-clip" {
		t.Errorf("operation after title = %+v, want clear-clip before the buttons", rend.ops[titleIndex+1:])
	}
	minimizeIndex := -1
	for i, op := range rend.ops {
		if op.kind == "rect" && op.rect == layout.Minimize {
			minimizeIndex = i
			break
		}
	}
	if minimizeIndex <= titleIndex+1 {
		t.Errorf("minimize button operation index = %d, want after title clip restore at %d", minimizeIndex, titleIndex+1)
	}
}
