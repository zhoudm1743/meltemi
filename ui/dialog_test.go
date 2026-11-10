package ui

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zhoudm1743/meltemi/internal/desktop"
)

// fakeDialogRequest simulates one in-flight portal request. respond invokes
// the service-level callback directly and is deliberately NOT once-guarded:
// tests drive user cancel, success, failure and late or duplicate responses
// through it so the public layer must enforce exactly-once delivery itself.
type fakeDialogRequest struct {
	service  *fakeDialogService
	callback func(desktop.DialogResult, error)
	closed   bool
}

func (r *fakeDialogRequest) Close() error {
	r.service.mu.Lock()
	if r.closed {
		r.service.mu.Unlock()
		return nil
	}
	r.closed = true
	r.service.mu.Unlock()
	r.callback(desktop.DialogResult{}, desktop.ErrDialogCanceled)
	return nil
}

func (r *fakeDialogRequest) respond(result desktop.DialogResult, err error) {
	r.callback(result, err)
}

// fakeDialogService implements desktop.DialogService without any D-Bus. It
// records the options snapshot it received so tests can verify the public
// conversion, and fails Open/Save with openErr/saveErr when set.
type fakeDialogService struct {
	mu           sync.Mutex
	capabilities desktop.Capabilities
	openErr      error
	saveErr      error
	opens        []desktop.DialogOptions
	saves        []desktop.DialogOptions
	openRequests []*fakeDialogRequest
	saveRequests []*fakeDialogRequest
	closed       bool
}

func (s *fakeDialogService) Capabilities() desktop.Capabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capabilities
}

func (s *fakeDialogService) Open(options desktop.DialogOptions, callback func(desktop.DialogResult, error)) (desktop.DialogRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openErr != nil {
		return nil, s.openErr
	}
	s.opens = append(s.opens, options)
	request := &fakeDialogRequest{service: s, callback: callback}
	s.openRequests = append(s.openRequests, request)
	return request, nil
}

func (s *fakeDialogService) Save(options desktop.DialogOptions, callback func(desktop.DialogResult, error)) (desktop.DialogRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return nil, s.saveErr
	}
	s.saves = append(s.saves, options)
	request := &fakeDialogRequest{service: s, callback: callback}
	s.saveRequests = append(s.saveRequests, request)
	return request, nil
}

func (s *fakeDialogService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeDialogService) lastOpenRequest(t *testing.T) *fakeDialogRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.openRequests) == 0 {
		t.Fatal("fake dialog service recorded no OpenFile request")
	}
	return s.openRequests[len(s.openRequests)-1]
}

// uiGoroutineID identifies the calling goroutine so tests can prove the
// dialog callback was posted back to the UI thread instead of running on the
// portal response goroutine.
func uiGoroutineID() uint64 {
	var stack [64]byte
	n := runtime.Stack(stack[:], false)
	fields := strings.Fields(string(stack[:n]))
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func newDialogTestApp(service desktop.DialogService) (*App, *fakeDialogService) {
	a := New()
	fake, ok := service.(*fakeDialogService)
	if !ok {
		fake = nil
	}
	a.newDialogService = func() desktop.DialogService { return service }
	return a, fake
}

func TestPublicOpenSaveDialogLifecycle(t *testing.T) {
	t.Run("open success posts callback to the UI thread", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, fake := newDialogTestApp(service)
		done := runUIAsync(a)

		var uiID, callbackID uint64
		var gotPaths []string
		var gotErr error
		callbacks := 0
		filters := []FileFilter{{
			Name: "Text",
			Patterns: []FileFilterPattern{
				{Kind: FileFilterPatternGlob, Pattern: "*.txt"},
				{Kind: FileFilterPatternMIMEType, Pattern: "text/plain"},
			},
		}}
		current := &FileFilter{Name: "Current", Patterns: []FileFilterPattern{{Kind: FileFilterPatternGlob, Pattern: "*.md"}}}
		parent := a.Window("dialog-parent")

		if err := a.Post(func() {
			uiID = uiGoroutineID()
			dialog, err := a.OpenFile(OpenFileOptions{
				Title:         "Pick files",
				AcceptLabel:   "Choose",
				Modal:         true,
				Multiple:      true,
				Filters:       filters,
				CurrentFilter: current,
				CurrentFolder: "/tmp",
				Parent:        parent,
			}, func(paths []string, err error) {
				callbacks++
				callbackID = uiGoroutineID()
				gotPaths, gotErr = paths, err
			})
			if err != nil {
				t.Errorf("OpenFile: %v", err)
				a.Quit()
				return
			}
			if dialog == nil {
				t.Error("OpenFile returned a nil dialog")
				a.Quit()
				return
			}
			// Mutating the caller's filters after the call must not leak into
			// the request: the public layer snapshots options.
			filters[0].Patterns[0].Pattern = "MUTATED"
			fake.lastOpenRequest(t).respond(desktop.DialogResult{Paths: []string{"/tmp/a.txt", "/tmp/b.txt"}}, nil)
		}); err != nil {
			t.Fatalf("Post OpenFile step: %v", err)
		}
		if err := a.Post(func() { a.Quit() }); err != nil {
			t.Fatalf("Post quit step: %v", err)
		}
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if gotErr != nil {
			t.Fatalf("callback error = %v, want nil", gotErr)
		}
		if want := []string{"/tmp/a.txt", "/tmp/b.txt"}; strings.Join(gotPaths, ",") != strings.Join(want, ",") {
			t.Errorf("callback paths = %v, want %v", gotPaths, want)
		}
		if callbacks != 1 {
			t.Errorf("callback count = %d, want 1", callbacks)
		}
		if callbackID == 0 || callbackID != uiID {
			t.Errorf("callback ran on goroutine %d, want the UI thread goroutine %d", callbackID, uiID)
		}

		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.opens) != 1 {
			t.Fatalf("service Open calls = %d, want 1", len(fake.opens))
		}
		options := fake.opens[0]
		if options.Title != "Pick files" || options.AcceptLabel != "Choose" || !options.Modal || !options.Multiple {
			t.Errorf("converted options = %+v, want title/accept/modal/multiple preserved", options)
		}
		if options.Directory {
			t.Error("Directory set on an OpenFile without Directory requested")
		}
		if options.CurrentFolder != "/tmp" {
			t.Errorf("CurrentFolder = %q, want /tmp", options.CurrentFolder)
		}
		if options.ParentWindow != "" {
			t.Errorf("ParentWindow = %q, want empty: the current X11 backend cannot declare a portal parent", options.ParentWindow)
		}
		if len(options.Filters) != 1 || options.Filters[0].Name != "Text" {
			t.Fatalf("converted filters = %+v, want one Text filter", options.Filters)
		}
		patterns := options.Filters[0].Patterns
		if len(patterns) != 2 ||
			patterns[0].Kind != desktop.FileFilterPatternGlob || patterns[0].Pattern != "*.txt" ||
			patterns[1].Kind != desktop.FileFilterPatternMIMEType || patterns[1].Pattern != "text/plain" {
			t.Errorf("converted filter patterns = %+v, want glob *.txt and mime text/plain", patterns)
		}
		if options.Filters[0].Patterns[0].Pattern != "*.txt" {
			t.Error("OpenFile did not snapshot the caller's filters")
		}
		if options.CurrentFilter == nil || options.CurrentFilter.Name != "Current" ||
			len(options.CurrentFilter.Patterns) != 1 || options.CurrentFilter.Patterns[0].Pattern != "*.md" {
			t.Errorf("converted current filter = %+v, want the Current snapshot", options.CurrentFilter)
		}
	})

	t.Run("save success maps the single path", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, fake := newDialogTestApp(service)
		done := runUIAsync(a)

		var gotPath string
		var gotErr error
		callbacks := 0
		if err := a.Post(func() {
			_, err := a.SaveFile(SaveFileOptions{
				Title:         "Save as",
				CurrentFolder: "/tmp",
				CurrentName:   "notes.txt",
			}, func(path string, err error) {
				callbacks++
				gotPath, gotErr = path, err
			})
			if err != nil {
				t.Errorf("SaveFile: %v", err)
				a.Quit()
				return
			}
			fake.mu.Lock()
			requests := fake.saveRequests
			fake.mu.Unlock()
			if len(requests) != 1 {
				t.Errorf("service Save calls = %d, want 1", len(requests))
				a.Quit()
				return
			}
			requests[0].respond(desktop.DialogResult{Paths: []string{"/tmp/notes.txt"}}, nil)
		}); err != nil {
			t.Fatalf("Post SaveFile step: %v", err)
		}
		if err := a.Post(func() { a.Quit() }); err != nil {
			t.Fatalf("Post quit step: %v", err)
		}
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if gotErr != nil {
			t.Fatalf("callback error = %v, want nil", gotErr)
		}
		if gotPath != "/tmp/notes.txt" {
			t.Errorf("callback path = %q, want /tmp/notes.txt", gotPath)
		}
		if callbacks != 1 {
			t.Errorf("callback count = %d, want 1", callbacks)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.saves) != 1 {
			t.Fatalf("service Save calls = %d, want 1", len(fake.saves))
		}
		options := fake.saves[0]
		if options.Title != "Save as" || options.CurrentFolder != "/tmp" || options.CurrentName != "notes.txt" {
			t.Errorf("converted save options = %+v, want title/folder/name preserved", options)
		}
		if options.Multiple || options.Directory {
			t.Errorf("save options carry open-only fields: %+v", options)
		}
	})

	t.Run("cancel and service failure map to public sentinels", func(t *testing.T) {
		t.Run("cancel", func(t *testing.T) {
			service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
			a, fake := newDialogTestApp(service)
			done := runUIAsync(a)

			var gotErr error
			if err := a.Post(func() {
				if _, err := a.OpenFile(OpenFileOptions{Title: "cancel"}, func(_ []string, err error) {
					gotErr = err
				}); err != nil {
					t.Errorf("OpenFile: %v", err)
				}
				fake.lastOpenRequest(t).respond(desktop.DialogResult{}, desktop.ErrDialogCanceled)
			}); err != nil {
				t.Fatalf("Post step: %v", err)
			}
			if err := a.Post(func() { a.Quit() }); err != nil {
				t.Fatalf("Post quit step: %v", err)
			}
			if err := waitUIReturn(t, done); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !errors.Is(gotErr, ErrDialogCanceled) {
				t.Errorf("callback error = %v, want errors.Is ErrDialogCanceled", gotErr)
			}
		})

		t.Run("unavailable", func(t *testing.T) {
			service := &fakeDialogService{
				capabilities: desktop.Capabilities{FileDialog: true},
				openErr:      fmt.Errorf("portal: %w", desktop.ErrDialogUnavailable),
			}
			a, fake := newDialogTestApp(service)
			done := runUIAsync(a)

			callbackRan := false
			if err := a.Post(func() {
				dialog, err := a.OpenFile(OpenFileOptions{Title: "unavailable"}, func(_ []string, _ error) {
					callbackRan = true
				})
				if err == nil {
					t.Error("OpenFile with an unavailable service: nil error")
				}
				if !errors.Is(err, ErrDialogUnavailable) {
					t.Errorf("OpenFile error = %v, want errors.Is ErrDialogUnavailable", err)
				}
				if dialog != nil {
					t.Errorf("OpenFile dialog = %v, want nil on failure", dialog)
				}
			}); err != nil {
				t.Fatalf("Post step: %v", err)
			}
			if err := a.Post(func() { a.Quit() }); err != nil {
				t.Fatalf("Post quit step: %v", err)
			}
			if err := waitUIReturn(t, done); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if callbackRan {
				t.Error("user callback ran although the request never started")
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.opens) != 0 {
				t.Errorf("service recorded %d opens, want 0 on failure", len(fake.opens))
			}
		})
	})

	t.Run("close cancels once and late responses are dropped", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, fake := newDialogTestApp(service)
		done := runUIAsync(a)

		var dialog *FileDialog
		var gotErr error
		callbacks := 0
		if err := a.Post(func() {
			var err error
			dialog, err = a.OpenFile(OpenFileOptions{Title: "close"}, func(_ []string, err error) {
				callbacks++
				gotErr = err
			})
			if err != nil {
				t.Errorf("OpenFile: %v", err)
				a.Quit()
				return
			}
			if err := dialog.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
			// Late portal responses after Close must not reach the callback.
			fake.lastOpenRequest(t).respond(desktop.DialogResult{Paths: []string{"/late.txt"}}, nil)
			fake.lastOpenRequest(t).respond(desktop.DialogResult{}, desktop.ErrDialogCanceled)
			if err := dialog.Close(); err != nil {
				t.Errorf("second Close: %v", err)
			}
		}); err != nil {
			t.Fatalf("Post step: %v", err)
		}
		if err := a.Post(func() { a.Quit() }); err != nil {
			t.Fatalf("Post quit step: %v", err)
		}
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if callbacks != 1 {
			t.Errorf("callback count = %d, want 1", callbacks)
		}
		if !errors.Is(gotErr, ErrDialogCanceled) {
			t.Errorf("callback error = %v, want errors.Is ErrDialogCanceled", gotErr)
		}
	})

	t.Run("duplicate service responses invoke the callback once", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, fake := newDialogTestApp(service)
		done := runUIAsync(a)

		callbacks := 0
		var gotPaths []string
		if err := a.Post(func() {
			if _, err := a.OpenFile(OpenFileOptions{Title: "dup"}, func(paths []string, _ error) {
				callbacks++
				gotPaths = paths
			}); err != nil {
				t.Errorf("OpenFile: %v", err)
				a.Quit()
				return
			}
			fake.lastOpenRequest(t).respond(desktop.DialogResult{Paths: []string{"/first.txt"}}, nil)
			fake.lastOpenRequest(t).respond(desktop.DialogResult{Paths: []string{"/second.txt"}}, nil)
		}); err != nil {
			t.Fatalf("Post step: %v", err)
		}
		if err := a.Post(func() { a.Quit() }); err != nil {
			t.Fatalf("Post quit step: %v", err)
		}
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if callbacks != 1 {
			t.Errorf("callback count = %d, want 1", callbacks)
		}
		if len(gotPaths) != 1 || gotPaths[0] != "/first.txt" {
			t.Errorf("callback paths = %v, want the first response only", gotPaths)
		}
	})

	t.Run("quit invalidates pending dialogs without invoking callbacks", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, fake := newDialogTestApp(service)

		callbacks := 0
		var dialogs []*FileDialog
		if err := a.Post(func() {
			for i := 0; i < 2; i++ {
				dialog, err := a.OpenFile(OpenFileOptions{Title: "pending"}, func(_ []string, _ error) {
					callbacks++
				})
				if err != nil {
					t.Errorf("OpenFile %d: %v", i, err)
					continue
				}
				dialogs = append(dialogs, dialog)
			}
			a.Quit()
		}); err != nil {
			t.Fatalf("Post step: %v", err)
		}
		done := runUIAsync(a)
		if err := waitUIReturn(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if callbacks != 0 {
			t.Errorf("callback count after Quit = %d, want 0: Quit drops pending dialogs without queueing callbacks", callbacks)
		}
		fake.mu.Lock()
		closed := fake.closed
		fake.mu.Unlock()
		if !closed {
			t.Error("dialog service was not closed on Quit")
		}
		for i, dialog := range dialogs {
			if err := dialog.Close(); err != nil {
				t.Errorf("Close on invalidated dialog %d: %v", i, err)
			}
		}
		if _, err := a.OpenFile(OpenFileOptions{}, func(_ []string, _ error) {}); !errors.Is(err, errAppQuitting) {
			t.Errorf("OpenFile after Quit = %v, want errors.Is errAppQuitting", err)
		}
	})
}

func TestDialogCallbackReentrancyAndQuit(t *testing.T) {
	service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
	a, fake := newDialogTestApp(service)

	var first *FileDialog
	calls := 0
	var reentrantErr error
	record := func(err error) {
		if err != nil && reentrantErr == nil {
			reentrantErr = err
		}
	}

	// The whole reentrant chain runs on the UI thread: the first dialog's
	// callback closes itself, opens a second dialog, cancels it, opens a third
	// that stays pending, and quits. None of these may deadlock, and the
	// pending third dialog must be dropped by Quit.
	if err := a.Post(func() {
		var err error
		first, err = a.OpenFile(OpenFileOptions{Title: "first"}, func(_ []string, err error) {
			if err != nil {
				record(fmt.Errorf("first callback error: %w", err))
				a.Quit()
				return
			}
			record(first.Close())
			second, err := a.OpenFile(OpenFileOptions{Title: "second"}, func(_ []string, err error) {
				calls++
				if err != nil {
					record(fmt.Errorf("second callback error: %w", err))
				}
			})
			if err != nil {
				record(fmt.Errorf("reentrant OpenFile: %w", err))
				a.Quit()
				return
			}
			fake.mu.Lock()
			requests := fake.openRequests
			fake.mu.Unlock()
			if len(requests) != 2 {
				record(fmt.Errorf("service open requests = %d, want 2", len(requests)))
				a.Quit()
				return
			}
			requests[1].respond(desktop.DialogResult{Paths: []string{"/second.txt"}}, nil)
			record(second.Close())
			if _, err := a.OpenFile(OpenFileOptions{Title: "third"}, func(_ []string, _ error) {
				calls++
			}); err != nil {
				record(fmt.Errorf("third OpenFile: %w", err))
			}
			a.Quit()
		})
		if err != nil {
			record(fmt.Errorf("initial OpenFile: %w", err))
			a.Quit()
			return
		}
		fake.mu.Lock()
		requests := fake.openRequests
		fake.mu.Unlock()
		if len(requests) != 1 {
			record(fmt.Errorf("service open requests = %d, want 1", len(requests)))
			a.Quit()
			return
		}
		requests[0].respond(desktop.DialogResult{Paths: []string{"/first.txt"}}, nil)
	}); err != nil {
		t.Fatalf("Post reentrancy step: %v", err)
	}

	done := runUIAsync(a)
	if err := waitUIReturn(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reentrantErr != nil {
		t.Fatalf("reentrant chain: %v", reentrantErr)
	}
	if calls != 1 {
		t.Errorf("second dialog callbacks = %d, want 1: the pending third dialog must be dropped by Quit", calls)
	}
}

func TestDialogCapabilityAndValidation(t *testing.T) {
	t.Run("capability mapping", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, _ := newDialogTestApp(service)
		if capabilities := a.Capabilities(); !capabilities.FileDialog {
			t.Error("Capabilities().FileDialog = false, want true from the service")
		}

		unsupported := &fakeDialogService{}
		b, _ := newDialogTestApp(unsupported)
		if capabilities := b.Capabilities(); capabilities.FileDialog {
			t.Error("Capabilities().FileDialog = true, want false from an unsupported service")
		}

		plain := New()
		plain.newTrayService = nil
		plain.newDialogService = nil
		plainCapabilities := plain.Capabilities()
		if plainCapabilities.FileDialog {
			t.Error("Capabilities().FileDialog = true without a dialog service factory")
		}
		if !plainCapabilities.MenuBar {
			t.Error("Capabilities().MenuBar = false, want the static true")
		}
	})

	t.Run("nil callback", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, fake := newDialogTestApp(service)
		if dialog, err := a.OpenFile(OpenFileOptions{}, nil); err == nil || dialog != nil {
			t.Errorf("OpenFile with nil callback = (%v, %v), want (nil, error)", dialog, err)
		}
		if dialog, err := a.SaveFile(SaveFileOptions{}, nil); err == nil || dialog != nil {
			t.Errorf("SaveFile with nil callback = (%v, %v), want (nil, error)", dialog, err)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.opens) != 0 || len(fake.saves) != 0 {
			t.Errorf("service calls = %d opens/%d saves, want 0/0", len(fake.opens), len(fake.saves))
		}
	})

	t.Run("option validation", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, fake := newDialogTestApp(service)

		if _, err := a.OpenFile(OpenFileOptions{
			Filters: []FileFilter{{Name: "Bad", Patterns: []FileFilterPattern{{Kind: FileFilterPatternKind(42), Pattern: "*"}}}},
		}, func(_ []string, _ error) {}); err == nil {
			t.Error("OpenFile with an invalid filter pattern kind: nil error")
		}
		if _, err := a.OpenFile(OpenFileOptions{CurrentFolder: "relative/dir"}, func(_ []string, _ error) {}); err == nil {
			t.Error("OpenFile with a relative CurrentFolder: nil error")
		}
		if _, err := a.SaveFile(SaveFileOptions{CurrentFile: "relative.txt"}, func(_ string, _ error) {}); err == nil {
			t.Error("SaveFile with a relative CurrentFile: nil error")
		}
		if _, err := a.SaveFile(SaveFileOptions{
			CurrentFolder: "/tmp",
			CurrentFile:   "/tmp/absolute.txt",
			Filters:       []FileFilter{{Name: "Any", Patterns: []FileFilterPattern{{Kind: FileFilterPatternGlob, Pattern: "*"}}}},
		}, func(_ string, _ error) {}); err != nil {
			t.Errorf("SaveFile with valid absolute options: %v", err)
		}

		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.opens) != 0 {
			t.Errorf("service recorded %d opens, want 0: invalid options must fail before the service call", len(fake.opens))
		}
		if len(fake.saves) != 1 {
			t.Errorf("service recorded %d saves, want 1 for the valid SaveFile", len(fake.saves))
		}
	})

	t.Run("open after quit", func(t *testing.T) {
		service := &fakeDialogService{capabilities: desktop.Capabilities{FileDialog: true}}
		a, fake := newDialogTestApp(service)
		a.Quit()
		if _, err := a.OpenFile(OpenFileOptions{}, func(_ []string, _ error) {}); !errors.Is(err, errAppQuitting) {
			t.Errorf("OpenFile after Quit = %v, want errors.Is errAppQuitting", err)
		}
		if _, err := a.SaveFile(SaveFileOptions{}, func(_ string, _ error) {}); !errors.Is(err, errAppQuitting) {
			t.Errorf("SaveFile after Quit = %v, want errors.Is errAppQuitting", err)
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.opens) != 0 || len(fake.saves) != 0 {
			t.Errorf("service calls after Quit = %d/%d, want 0/0", len(fake.opens), len(fake.saves))
		}
	})
}
