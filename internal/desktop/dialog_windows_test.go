//go:build windows

package desktop

import (
	"errors"
	"testing"
	"time"
)

func TestWindowsFilterSpecs(t *testing.T) {
	filters, err := windowsFilterSpecs([]FileFilter{
		{
			Name: "Text",
			Patterns: []FileFilterPattern{
				{Kind: FileFilterPatternGlob, Pattern: "*.txt"},
				{Kind: FileFilterPatternGlob, Pattern: "*.md"},
				{Kind: FileFilterPatternMIMEType, Pattern: "text/plain"},
			},
		},
		{Name: "All files", Patterns: []FileFilterPattern{{Kind: FileFilterPatternGlob, Pattern: "*"}}},
		{Name: "MIME only", Patterns: []FileFilterPattern{{Kind: FileFilterPatternMIMEType, Pattern: "image/png"}}},
	})
	if err != nil {
		t.Fatalf("windowsFilterSpecs: %v", err)
	}
	if filters[0].Spec != "*.txt;*.md" || filters[1].Spec != "*.*" || filters[2].Spec != "*.*" {
		t.Fatalf("specs = %+v", filters)
	}
	if got := windowsFilterIndex(filters, &FileFilter{
		Name: "Text",
		Patterns: []FileFilterPattern{
			{Kind: FileFilterPatternGlob, Pattern: "*.txt"},
			{Kind: FileFilterPatternMIMEType, Pattern: "text/plain"},
			{Kind: FileFilterPatternGlob, Pattern: "*.md"},
		},
	}); got != 1 {
		t.Fatalf("current filter index = %d, want 1", got)
	}
	if _, err := windowsFilterSpec(FileFilter{Patterns: []FileFilterPattern{{Kind: 9, Pattern: "x"}}}); !errors.Is(err, errDialogFilterPattern) {
		t.Fatalf("invalid pattern error = %v", err)
	}
}

func TestWindowsDialogValidation(t *testing.T) {
	service := NewDialogService()
	if caps := service.Capabilities(); !caps.FileDialog {
		t.Fatalf("Capabilities = %+v, want FileDialog", caps)
	}
	if _, err := service.Open(DialogOptions{}, nil); !errors.Is(err, errDialogNilCallback) {
		t.Fatalf("nil callback error = %v", err)
	}
	_, err := service.Open(DialogOptions{CurrentFolder: "relative"}, func(DialogResult, error) {})
	if !errors.Is(err, errDialogFolderNotAbs) {
		t.Fatalf("relative folder error = %v", err)
	}
	_, err = service.Save(DialogOptions{CurrentFile: "relative.txt"}, func(DialogResult, error) {})
	if !errors.Is(err, errDialogFileNotAbs) {
		t.Fatalf("relative file error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if caps := service.Capabilities(); caps.FileDialog {
		t.Fatalf("Capabilities after Close = %+v, want no file dialog", caps)
	}
	if _, err := service.Open(DialogOptions{}, func(DialogResult, error) {}); !errors.Is(err, errDialogClosed) {
		t.Fatalf("Open after Close = %v", err)
	}
}

func TestWindowsDialogCancel(t *testing.T) {
	service := NewDialogService()
	defer service.Close()
	got := make(chan error, 1)
	request, err := service.Open(DialogOptions{Title: "Meltemi cancel test"}, func(_ DialogResult, err error) {
		got <- err
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Open returns once the dialog is configured and Show is in progress on
	// its own thread. Wait until that native dialog is up, then cancel it.
	time.Sleep(700 * time.Millisecond)
	if err := request.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-got:
		if !errors.Is(err, ErrDialogCanceled) {
			t.Fatalf("callback error = %v, want ErrDialogCanceled", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("dialog callback was not delivered")
	}
}
