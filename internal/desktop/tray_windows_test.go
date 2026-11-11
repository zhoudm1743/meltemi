//go:build windows

package desktop

import (
	"errors"
	"testing"
	"time"
)

func TestBGRAFromIconPremultiplies(t *testing.T) {
	got := bgraFromIcon(Icon{
		Width:  1,
		Height: 1,
		Pixels: []byte{128, 255, 0, 0},
	})
	if len(got) != 4 || got[0] != 0 || got[1] != 0 || got[2] != 128 || got[3] != 128 {
		t.Fatalf("bgra = %v, want premultiplied BGRA {0,0,128,128}", got)
	}
}

func TestMenuActionIDsFollowPopupOrder(t *testing.T) {
	var calls []string
	action := func(name string) func() {
		return func() { calls = append(calls, name) }
	}
	items := []TrayMenuItem{
		{Text: "Restore window", Kind: TrayMenuAction, Enabled: true, Activate: action("restore")},
		{Text: "Checked", Kind: TrayMenuAction, Enabled: true, Checked: true, Activate: action("checked")},
		{Kind: TrayMenuSeparator},
		{Text: "Disabled", Kind: TrayMenuAction, Enabled: false, Activate: action("disabled")},
		{Text: "More", Kind: TrayMenuSubmenu, Enabled: true, Children: []TrayMenuItem{
			{Text: "Nested", Kind: TrayMenuAction, Enabled: true, Activate: action("nested")},
		}},
	}
	next := uint32(1)
	if menuAction(items, 1, &next) == nil {
		t.Fatal("id 1 did not resolve")
	}
	next = 1
	if fn := menuAction(items, 4, &next); fn == nil {
		t.Fatal("nested id 4 did not resolve")
	} else {
		fn()
	}
	if len(calls) != 1 || calls[0] != "nested" {
		t.Fatalf("calls = %v, want [nested]", calls)
	}
	next = 1
	if fn := menuAction(items, 3, &next); fn != nil {
		t.Fatal("disabled item returned an action")
	}
}

func TestWindowsTrayShowHide(t *testing.T) {
	service := NewTrayService()
	if caps := service.Capabilities(); !caps.Tray {
		t.Fatalf("Capabilities = %+v, want Tray", caps)
	}
	icon := Icon{Width: 2, Height: 2, Pixels: []byte{
		255, 10, 20, 30, 255, 10, 20, 30,
		255, 10, 20, 30, 255, 10, 20, 30,
	}}
	activated := make(chan struct{}, 1)
	if err := service.SetMenu(TrayMenu{Items: []TrayMenuItem{{
		Text: "Restore window", Kind: TrayMenuAction, Enabled: true,
		Activate: func() { activated <- struct{}{} },
	}}}); err != nil {
		t.Fatalf("SetMenu: %v", err)
	}
	if err := service.Show(TrayConfig{Icon: icon, Tooltip: "meltemi test"}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	if err := ActivateWindowsTrayItem("Restore window"); err != nil {
		t.Fatalf("ActivateWindowsTrayItem: %v", err)
	}
	select {
	case <-activated:
	case <-time.After(2 * time.Second):
		t.Fatal("tray activation was not delivered")
	}
	if err := service.Hide(); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if caps := service.Capabilities(); caps.Tray {
		t.Fatalf("Capabilities after Close = %+v", caps)
	}
	if err := service.Show(TrayConfig{Icon: icon}); !errors.Is(err, errTrayClosed) {
		t.Fatalf("Show after Close = %v", err)
	}
}
