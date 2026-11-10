//go:build linux

package desktop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	testPortalDestination = "org.freedesktop.portal.Desktop"
	testPortalDesktopPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	testPortalOwner       = ":1.77"
)

// wantDialogRequestPath mirrors the XDG portal rule for request object paths:
// /org/freedesktop/portal/desktop/request/<sender>/<token>, where the sender
// keeps only characters valid in object paths.
func wantDialogRequestPath(sender, token string) dbus.ObjectPath {
	sender = strings.TrimPrefix(sender, ":")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + strings.ReplaceAll(sender, ".", "_") + "/" + token)
}

type dialogBusCall struct {
	destination string
	path        dbus.ObjectPath
	method      string
	args        []interface{}
	hasDeadline bool
}

type fakeDialogBusObject struct {
	dbus.BusObject
	bus         *fakeDialogBus
	destination string
	path        dbus.ObjectPath
}

func (o *fakeDialogBusObject) CallWithContext(ctx context.Context, method string, _ dbus.Flags, args ...interface{}) *dbus.Call {
	_, hasDeadline := ctx.Deadline()
	o.bus.mu.Lock()
	o.bus.events = append(o.bus.events, "call:"+method)
	o.bus.calls = append(o.bus.calls, dialogBusCall{
		destination: o.destination,
		path:        o.path,
		method:      method,
		args:        append([]interface{}(nil), args...),
		hasDeadline: hasDeadline,
	})
	portalPresent := o.bus.portalPresent
	blockOpen := o.bus.blockOpen
	openErr := o.bus.openErr
	closeErr := o.bus.closeErr
	handleOverride := o.bus.handleOverride
	onOpen := o.bus.onOpen
	o.bus.mu.Unlock()

	switch method {
	case "org.freedesktop.DBus.GetNameOwner":
		name, _ := args[0].(string)
		if !portalPresent || name != testPortalDestination {
			return &dbus.Call{Err: errors.New("org.freedesktop.DBus.Error.NameHasNoOwner")}
		}
		return &dbus.Call{Body: []interface{}{o.bus.portalOwner()}}
	case "org.freedesktop.DBus.Properties.Get":
		if !portalPresent {
			return &dbus.Call{Err: errors.New("org.freedesktop.DBus.Error.ServiceUnknown")}
		}
		iface, _ := args[0].(string)
		name, _ := args[1].(string)
		if iface == "org.freedesktop.portal.FileChooser" && name == "version" {
			return &dbus.Call{Body: []interface{}{dbus.MakeVariant(uint32(4))}}
		}
		return &dbus.Call{Err: fmt.Errorf("unknown property %s.%s", iface, name)}
	case "org.freedesktop.portal.FileChooser.OpenFile",
		"org.freedesktop.portal.FileChooser.SaveFile":
		if !portalPresent {
			return &dbus.Call{Err: errors.New("org.freedesktop.DBus.Error.ServiceUnknown")}
		}
		if blockOpen {
			<-ctx.Done()
			return &dbus.Call{Err: ctx.Err()}
		}
		if openErr != nil {
			return &dbus.Call{Err: openErr}
		}
		token := ""
		if len(args) == 3 {
			if options, ok := args[2].(map[string]dbus.Variant); ok {
				if variant, ok := options["handle_token"]; ok {
					token, _ = variant.Value().(string)
				}
			}
		}
		handle := wantDialogRequestPath(o.bus.uniqueName, token)
		if handleOverride != "" {
			handle = handleOverride
		}
		if onOpen != nil {
			onOpen()
		}
		return &dbus.Call{Body: []interface{}{handle}}
	case "org.freedesktop.portal.Request.Close":
		o.bus.mu.Lock()
		o.bus.closedPaths = append(o.bus.closedPaths, o.path)
		o.bus.mu.Unlock()
		if closeErr != nil {
			return &dbus.Call{Err: closeErr}
		}
		return &dbus.Call{}
	default:
		return &dbus.Call{Err: fmt.Errorf("unexpected D-Bus method %s", method)}
	}
}

type fakeDialogBus struct {
	mu sync.Mutex

	uniqueName     string
	portalPresent  bool
	portalOwnerID  string
	blockOpen      bool
	openErr        error
	closeErr       error
	handleOverride dbus.ObjectPath
	onOpen         func()

	events        []string
	calls         []dialogBusCall
	addMatchCalls int
	removeMatch   int
	closeCalls    int
	signalChannel chan<- *dbus.Signal
	closedPaths   []dbus.ObjectPath
}

func newFakeDialogBus() *fakeDialogBus {
	return &fakeDialogBus{
		uniqueName:    ":1.42",
		portalPresent: true,
		portalOwnerID: testPortalOwner,
	}
}

func (b *fakeDialogBus) portalOwner() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.portalOwnerID
}

func (b *fakeDialogBus) Object(destination string, path dbus.ObjectPath) dbus.BusObject {
	return &fakeDialogBusObject{bus: b, destination: destination, path: path}
}

func (b *fakeDialogBus) BusObject() dbus.BusObject {
	return &fakeDialogBusObject{bus: b, destination: "org.freedesktop.DBus", path: "/org/freedesktop/DBus"}
}

func (b *fakeDialogBus) RequestName(string, dbus.RequestNameFlags) (dbus.RequestNameReply, error) {
	return 0, errors.New("unexpected RequestName call")
}

func (b *fakeDialogBus) ReleaseName(string) (dbus.ReleaseNameReply, error) {
	return 0, errors.New("unexpected ReleaseName call")
}

func (b *fakeDialogBus) Export(interface{}, dbus.ObjectPath, string) error { return nil }

func (b *fakeDialogBus) Emit(dbus.ObjectPath, string, ...interface{}) error { return nil }

func (b *fakeDialogBus) AddMatchSignalContext(context.Context, ...dbus.MatchOption) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, "addmatch")
	b.addMatchCalls++
	return nil
}

func (b *fakeDialogBus) RemoveMatchSignalContext(context.Context, ...dbus.MatchOption) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, "removematch")
	b.removeMatch++
	return nil
}

func (b *fakeDialogBus) Signal(channel chan<- *dbus.Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.signalChannel = channel
}

func (b *fakeDialogBus) RemoveSignal(channel chan<- *dbus.Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.signalChannel == channel {
		b.signalChannel = nil
	}
}

func (b *fakeDialogBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeCalls++
	return nil
}

func (b *fakeDialogBus) Names() []string { return []string{b.uniqueName} }

func (b *fakeDialogBus) emitResponse(path dbus.ObjectPath, code uint32, results map[string]dbus.Variant) {
	b.emitResponseFrom(path, code, results, b.portalOwner())
}

func (b *fakeDialogBus) emitResponseFrom(path dbus.ObjectPath, code uint32, results map[string]dbus.Variant, sender string) {
	b.mu.Lock()
	channel := b.signalChannel
	b.mu.Unlock()
	if channel == nil {
		return
	}
	channel <- &dbus.Signal{
		Sender: sender,
		Path:   path,
		Name:   "org.freedesktop.portal.Request.Response",
		Body:   []interface{}{code, results},
	}
}

func (b *fakeDialogBus) emitPortalOwnerChanged(newOwner string) {
	b.mu.Lock()
	channel := b.signalChannel
	owner := b.portalOwnerID
	b.mu.Unlock()
	if channel == nil {
		return
	}
	channel <- &dbus.Signal{
		Sender: "org.freedesktop.DBus",
		Path:   "/org/freedesktop/DBus",
		Name:   "org.freedesktop.DBus.NameOwnerChanged",
		Body:   []interface{}{testPortalDestination, owner, newOwner},
	}
}

func (b *fakeDialogBus) findCall(method string) (dialogBusCall, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, call := range b.calls {
		if call.method == method {
			return call, true
		}
	}
	return dialogBusCall{}, false
}

func (b *fakeDialogBus) callCount(method string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, call := range b.calls {
		if call.method == method {
			count++
		}
	}
	return count
}

func (b *fakeDialogBus) subscribedBeforeFirstCall(t *testing.T, method string) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	firstMatch, firstCall := -1, -1
	for i, event := range b.events {
		if event == "addmatch" && firstMatch < 0 {
			firstMatch = i
		}
		if event == "call:"+method && firstCall < 0 {
			firstCall = i
		}
	}
	if firstMatch < 0 || firstCall < 0 || firstMatch > firstCall {
		t.Errorf("subscription ordering: first signal match at event %d, first %s call at event %d, want match first",
			firstMatch, method, firstCall)
	}
}

type recordedDialogCallback struct {
	result DialogResult
	err    error
}

type dialogCallbackRecorder struct {
	mu    sync.Mutex
	calls []recordedDialogCallback
}

func (r *dialogCallbackRecorder) callback(result DialogResult, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedDialogCallback{result: result, err: err})
}

func (r *dialogCallbackRecorder) snapshot() []recordedDialogCallback {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedDialogCallback(nil), r.calls...)
}

func (r *dialogCallbackRecorder) wait(t *testing.T, count int) []recordedDialogCallback {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		calls := r.snapshot()
		if len(calls) >= count {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d dialog callbacks, got %d: %+v", count, len(calls), calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *dialogCallbackRecorder) requireNoMore(t *testing.T, count int, hold time.Duration) {
	t.Helper()
	deadline := time.Now().Add(hold)
	for time.Now().Before(deadline) {
		if calls := r.snapshot(); len(calls) != count {
			t.Fatalf("dialog callbacks = %d after settle, want exactly %d: %+v", len(calls), count, calls)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func dialogCallOptions(t *testing.T, call dialogBusCall) map[string]dbus.Variant {
	t.Helper()
	if len(call.args) != 3 {
		t.Fatalf("%s args = %d, want 3 (parent_window, title, options)", call.method, len(call.args))
	}
	options, ok := call.args[2].(map[string]dbus.Variant)
	if !ok {
		t.Fatalf("%s options arg = %T, want map[string]dbus.Variant", call.method, call.args[2])
	}
	return options
}

func dialogCallToken(t *testing.T, options map[string]dbus.Variant) string {
	t.Helper()
	variant, ok := options["handle_token"]
	if !ok {
		t.Fatal("options lack handle_token")
	}
	token, ok := variant.Value().(string)
	if !ok || token == "" {
		t.Fatalf("handle_token = %#v, want non-empty string", variant.Value())
	}
	return token
}

func successPortalResults(uris ...string) map[string]dbus.Variant {
	return map[string]dbus.Variant{"uris": dbus.MakeVariant(uris)}
}

func TestFileChooserOptionsAndURIResults(t *testing.T) {
	t.Run("open_file_options_and_decoded_uris", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		if !service.Capabilities().FileDialog {
			t.Fatal("Capabilities().FileDialog = false with portal present")
		}
		recorder := &dialogCallbackRecorder{}
		request, err := service.Open(DialogOptions{
			Title:       "Open documents",
			AcceptLabel: "Open",
			Modal:       true,
			Multiple:    true,
			Filters: []FileFilter{{
				Name: "Text and data",
				Patterns: []FileFilterPattern{
					{Kind: FileFilterPatternGlob, Pattern: "*.txt"},
					{Kind: FileFilterPatternMIMEType, Pattern: "text/plain"},
				},
			}},
			CurrentFolder: "/home/user/docs",
		}, recorder.callback)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if request == nil {
			t.Fatal("Open returned a nil request")
		}
		bus.subscribedBeforeFirstCall(t, "org.freedesktop.portal.FileChooser.OpenFile")

		call, ok := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
		if !ok {
			t.Fatal("no OpenFile call recorded")
		}
		if call.destination != testPortalDestination || call.path != testPortalDesktopPath {
			t.Errorf("OpenFile destination/path = %s %s, want %s %s",
				call.destination, call.path, testPortalDestination, testPortalDesktopPath)
		}
		if !call.hasDeadline {
			t.Error("OpenFile was called without a context deadline")
		}
		if call.args[0] != "" {
			t.Errorf("parent_window = %q, want empty identifier on the current X11 backend", call.args[0])
		}
		if call.args[1] != "Open documents" {
			t.Errorf("title = %q, want %q", call.args[1], "Open documents")
		}
		options := dialogCallOptions(t, call)
		token := dialogCallToken(t, options)
		if got := options["accept_label"].Value(); got != "Open" {
			t.Errorf("accept_label = %#v, want %q", got, "Open")
		}
		if got := options["modal"].Value(); got != true {
			t.Errorf("modal = %#v, want true", got)
		}
		if got := options["multiple"].Value(); got != true {
			t.Errorf("multiple = %#v, want true", got)
		}
		filtersVariant, ok := options["filters"]
		if !ok {
			t.Fatal("options lack filters")
		}
		if sig := filtersVariant.Signature().String(); sig != "a(sa(us))" {
			t.Errorf("filters signature = %q, want a(sa(us))", sig)
		}
		filters, ok := filtersVariant.Value().([]dialogWireFilter)
		if !ok {
			t.Fatalf("filters value = %T, want []dialogWireFilter", filtersVariant.Value())
		}
		if len(filters) != 1 || filters[0].Name != "Text and data" {
			t.Fatalf("filters = %+v, want one Text and data entry", filters)
		}
		wantPatterns := []dialogWireFilterPattern{{Type: 0, Pattern: "*.txt"}, {Type: 1, Pattern: "text/plain"}}
		if !reflect.DeepEqual(filters[0].Patterns, wantPatterns) {
			t.Errorf("filter patterns = %+v, want %+v", filters[0].Patterns, wantPatterns)
		}
		folder, ok := options["current_folder"]
		if !ok {
			t.Fatal("options lack current_folder")
		}
		if sig := folder.Signature().String(); sig != "ay" {
			t.Errorf("current_folder signature = %q, want ay", sig)
		}
		if got := folder.Value().([]byte); string(got) != "/home/user/docs\x00" {
			t.Errorf("current_folder = %q, want NUL-terminated /home/user/docs", got)
		}

		wantPath := wantDialogRequestPath(bus.uniqueName, token)
		bus.emitResponse(wantPath, 0, successPortalResults("file:///home/user/docs/a.txt", "file:///home/user/docs/b%20c.txt"))
		calls := recorder.wait(t, 1)
		if calls[0].err != nil {
			t.Fatalf("callback error = %v, want success", calls[0].err)
		}
		wantPaths := []string{"/home/user/docs/a.txt", "/home/user/docs/b c.txt"}
		if !reflect.DeepEqual(calls[0].result.Paths, wantPaths) {
			t.Errorf("decoded paths = %v, want %v", calls[0].result.Paths, wantPaths)
		}
		if err := request.Close(); err != nil {
			t.Errorf("Close after completion: %v", err)
		}
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("save_file_options_and_single_uri", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		request, err := service.Save(DialogOptions{
			Title:         "Save report",
			AcceptLabel:   "Save",
			CurrentFolder: "/home/user/out",
			CurrentName:   "report.txt",
			CurrentFile:   "/home/user/out/report.txt",
		}, recorder.callback)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if request == nil {
			t.Fatal("Save returned a nil request")
		}
		call, ok := bus.findCall("org.freedesktop.portal.FileChooser.SaveFile")
		if !ok {
			t.Fatal("no SaveFile call recorded")
		}
		if call.destination != testPortalDestination || call.path != testPortalDesktopPath {
			t.Errorf("SaveFile destination/path = %s %s, want %s %s",
				call.destination, call.path, testPortalDestination, testPortalDesktopPath)
		}
		if !call.hasDeadline {
			t.Error("SaveFile was called without a context deadline")
		}
		options := dialogCallOptions(t, call)
		token := dialogCallToken(t, options)
		for _, forbidden := range []string{"multiple", "directory"} {
			if _, ok := options[forbidden]; ok {
				t.Errorf("SaveFile sent %s option, want it omitted", forbidden)
			}
		}
		if _, ok := options["modal"]; ok {
			t.Error("SaveFile sent modal option, want it omitted when false")
		}
		if got := options["current_name"].Value(); got != "report.txt" {
			t.Errorf("current_name = %#v, want report.txt", got)
		}
		file, ok := options["current_file"]
		if !ok {
			t.Fatal("options lack current_file")
		}
		if got := file.Value().([]byte); string(got) != "/home/user/out/report.txt\x00" {
			t.Errorf("current_file = %q, want NUL-terminated absolute path", got)
		}
		folder, ok := options["current_folder"]
		if !ok || string(folder.Value().([]byte)) != "/home/user/out\x00" {
			t.Errorf("current_folder = %v, want NUL-terminated /home/user/out", folder.Value())
		}

		wantPath := wantDialogRequestPath(bus.uniqueName, token)
		bus.emitResponse(wantPath, 0, successPortalResults("file:///home/user/out/report.txt"))
		calls := recorder.wait(t, 1)
		if calls[0].err != nil {
			t.Fatalf("callback error = %v, want success", calls[0].err)
		}
		if want := []string{"/home/user/out/report.txt"}; !reflect.DeepEqual(calls[0].result.Paths, want) {
			t.Errorf("decoded paths = %v, want %v", calls[0].result.Paths, want)
		}
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("directory_open_sets_directory_option", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		_, err := service.Open(DialogOptions{
			Title:         "Pick folder",
			Directory:     true,
			CurrentFolder: "/home/user",
		}, recorder.callback)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		call, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
		options := dialogCallOptions(t, call)
		if got := options["directory"].Value(); got != true {
			t.Errorf("directory = %#v, want true", got)
		}
		if _, ok := options["multiple"]; ok {
			t.Error("directory open sent multiple option, want it omitted")
		}
		token := dialogCallToken(t, options)
		bus.emitResponse(wantDialogRequestPath(bus.uniqueName, token), 1, nil)
		calls := recorder.wait(t, 1)
		if !errors.Is(calls[0].err, ErrDialogCanceled) {
			t.Errorf("directory-open cancel error = %v, want ErrDialogCanceled", calls[0].err)
		}
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("response_codes_and_malformed_results", func(t *testing.T) {
		t.Run("user_cancel", func(t *testing.T) {
			bus := newFakeDialogBus()
			service := newDialogServiceWithConn(bus)
			recorder := &dialogCallbackRecorder{}
			if _, err := service.Open(DialogOptions{Title: "cancel"}, recorder.callback); err != nil {
				t.Fatalf("Open: %v", err)
			}
			call, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
			token := dialogCallToken(t, dialogCallOptions(t, call))
			wantPath := wantDialogRequestPath(bus.uniqueName, token)
			bus.emitResponse(wantPath, 1, nil)
			calls := recorder.wait(t, 1)
			if !errors.Is(calls[0].err, ErrDialogCanceled) {
				t.Errorf("response 1 error = %v, want ErrDialogCanceled", calls[0].err)
			}
			if len(calls[0].result.Paths) != 0 {
				t.Errorf("cancelled result paths = %v, want empty", calls[0].result.Paths)
			}
			bus.emitResponse(wantPath, 0, successPortalResults("file:///late.txt"))
			recorder.requireNoMore(t, 1, 200*time.Millisecond)
			if err := service.Close(); err != nil {
				t.Fatalf("service Close: %v", err)
			}
		})

		t.Run("other_failure", func(t *testing.T) {
			bus := newFakeDialogBus()
			service := newDialogServiceWithConn(bus)
			recorder := &dialogCallbackRecorder{}
			if _, err := service.Open(DialogOptions{Title: "fail"}, recorder.callback); err != nil {
				t.Fatalf("Open: %v", err)
			}
			call, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
			token := dialogCallToken(t, dialogCallOptions(t, call))
			bus.emitResponse(wantDialogRequestPath(bus.uniqueName, token), 2, nil)
			calls := recorder.wait(t, 1)
			if calls[0].err == nil {
				t.Fatal("response 2 delivered a nil error")
			}
			if errors.Is(calls[0].err, ErrDialogCanceled) {
				t.Errorf("response 2 error = %v, must not be ErrDialogCanceled", calls[0].err)
			}
			if err := service.Close(); err != nil {
				t.Fatalf("service Close: %v", err)
			}
		})

		uriCases := []struct {
			name  string
			value interface{}
		}{
			{name: "non_file_scheme", value: []string{"https://example.com/doc.txt"}},
			{name: "non_local_host", value: []string{"file://otherhost/tmp/doc.txt"}},
			{name: "bad_escape", value: []string{"file:///tmp/%zz.txt"}},
			{name: "empty_uri", value: []string{""}},
			{name: "missing_uris", value: nil},
			{name: "empty_uri_list", value: []string{}},
			{name: "uris_not_string_list", value: []interface{}{"file:///tmp/a.txt"}},
		}
		for _, uriCase := range uriCases {
			t.Run(uriCase.name, func(t *testing.T) {
				bus := newFakeDialogBus()
				service := newDialogServiceWithConn(bus)
				recorder := &dialogCallbackRecorder{}
				if _, err := service.Open(DialogOptions{Title: uriCase.name}, recorder.callback); err != nil {
					t.Fatalf("Open: %v", err)
				}
				call, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
				token := dialogCallToken(t, dialogCallOptions(t, call))
				var results map[string]dbus.Variant
				if uriCase.value != nil {
					results = map[string]dbus.Variant{"uris": dbus.MakeVariant(uriCase.value)}
				}
				bus.emitResponse(wantDialogRequestPath(bus.uniqueName, token), 0, results)
				calls := recorder.wait(t, 1)
				if calls[0].err == nil {
					t.Fatalf("malformed URI result %v delivered no error", uriCase.value)
				}
				if len(calls[0].result.Paths) != 0 {
					t.Errorf("malformed result paths = %v, want empty", calls[0].result.Paths)
				}
				if err := service.Close(); err != nil {
					t.Fatalf("service Close: %v", err)
				}
			})
		}

		t.Run("mismatched_handle_rejected", func(t *testing.T) {
			bus := newFakeDialogBus()
			bus.handleOverride = "/org/freedesktop/portal/desktop/request/1_42/forged"
			service := newDialogServiceWithConn(bus)
			recorder := &dialogCallbackRecorder{}
			request, err := service.Open(DialogOptions{Title: "mismatch"}, recorder.callback)
			if err == nil {
				t.Fatal("Open succeeded with a mismatched portal handle")
			}
			if request != nil {
				t.Errorf("Open request = %v, want nil on handle mismatch", request)
			}
			bus.emitResponse("/org/freedesktop/portal/desktop/request/1_42/forged", 0, successPortalResults("file:///x.txt"))
			recorder.requireNoMore(t, 0, 200*time.Millisecond)
			if err := service.Close(); err != nil {
				t.Fatalf("service Close: %v", err)
			}
		})
	})
}

func TestFileChooserSaveFileFiltersAndCurrentFilter(t *testing.T) {
	t.Run("save_file_sends_filters_and_current_filter", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		textFilter := FileFilter{
			Name:     "Text",
			Patterns: []FileFilterPattern{{Kind: FileFilterPatternGlob, Pattern: "*.txt"}},
		}
		allFilter := FileFilter{
			Name:     "All",
			Patterns: []FileFilterPattern{{Kind: FileFilterPatternMIMEType, Pattern: "*/*"}},
		}
		_, err := service.Save(DialogOptions{
			Title:         "Save as",
			Filters:       []FileFilter{textFilter, allFilter},
			CurrentFilter: &textFilter,
		}, recorder.callback)
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		call, ok := bus.findCall("org.freedesktop.portal.FileChooser.SaveFile")
		if !ok {
			t.Fatal("no SaveFile call recorded")
		}
		options := dialogCallOptions(t, call)

		filtersVariant, ok := options["filters"]
		if !ok {
			t.Fatal("SaveFile lacks the filters option")
		}
		if sig := filtersVariant.Signature().String(); sig != "a(sa(us))" {
			t.Errorf("SaveFile filters signature = %q, want a(sa(us))", sig)
		}
		if got := filtersVariant.Value().([]dialogWireFilter); len(got) != 2 {
			t.Errorf("SaveFile filters = %+v, want both entries on the wire", got)
		}

		currentVariant, ok := options["current_filter"]
		if !ok {
			t.Fatal("SaveFile lacks the current_filter option")
		}
		if sig := currentVariant.Signature().String(); sig != "(sa(us))" {
			t.Errorf("SaveFile current_filter signature = %q, want (sa(us))", sig)
		}
		wantCurrent := dialogWireFilter{
			Name:     "Text",
			Patterns: []dialogWireFilterPattern{{Type: 0, Pattern: "*.txt"}},
		}
		if got, ok := currentVariant.Value().(dialogWireFilter); !ok || !reflect.DeepEqual(got, wantCurrent) {
			t.Errorf("SaveFile current_filter = %+v (ok=%v), want %+v", currentVariant.Value(), ok, wantCurrent)
		}

		token := dialogCallToken(t, options)
		bus.emitResponse(wantDialogRequestPath(bus.uniqueName, token), 1, nil)
		recorder.wait(t, 1)
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("current_filter_omitted_when_nil", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		if _, err := service.Save(DialogOptions{Title: "plain"}, func(DialogResult, error) {}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		call, ok := bus.findCall("org.freedesktop.portal.FileChooser.SaveFile")
		if !ok {
			t.Fatal("no SaveFile call recorded")
		}
		if _, ok := dialogCallOptions(t, call)["current_filter"]; ok {
			t.Error("SaveFile sent current_filter, want it omitted when nil")
		}
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("current_filter_pattern_kind_validated_before_wire", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		bad := DialogOptions{
			Title:         "bad",
			CurrentFilter: &FileFilter{Name: "bad", Patterns: []FileFilterPattern{{Kind: FileFilterPatternKind(9), Pattern: "*.x"}}},
		}
		if _, err := service.Save(bad, func(DialogResult, error) {}); err == nil {
			t.Fatal("Save with an invalid current_filter pattern kind succeeded")
		}
		if got := bus.callCount("org.freedesktop.portal.FileChooser.SaveFile"); got != 0 {
			t.Errorf("SaveFile calls with invalid current_filter = %d, want 0", got)
		}
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})
}

func TestPortalResponseRaceAndCancellation(t *testing.T) {
	t.Run("fast_response_before_method_return_is_not_lost", func(t *testing.T) {
		bus := newFakeDialogBus()
		bus.onOpen = func() {
			call := bus.calls[len(bus.calls)-1]
			token := ""
			if options, ok := call.args[2].(map[string]dbus.Variant); ok {
				token, _ = options["handle_token"].Value().(string)
			}
			bus.emitResponse(wantDialogRequestPath(bus.uniqueName, token), 0, successPortalResults("file:///fast.txt"))
		}
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		request, err := service.Open(DialogOptions{Title: "fast"}, recorder.callback)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if request == nil {
			t.Fatal("Open returned a nil request")
		}
		calls := recorder.wait(t, 1)
		if calls[0].err != nil {
			t.Fatalf("fast callback error = %v, want success", calls[0].err)
		}
		if want := []string{"/fast.txt"}; !reflect.DeepEqual(calls[0].result.Paths, want) {
			t.Errorf("fast paths = %v, want %v", calls[0].result.Paths, want)
		}
		bus.subscribedBeforeFirstCall(t, "org.freedesktop.portal.FileChooser.OpenFile")
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("request_close_cancels_and_ignores_late_response", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		request, err := service.Open(DialogOptions{Title: "cancel"}, recorder.callback)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		call, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
		token := dialogCallToken(t, dialogCallOptions(t, call))
		wantPath := wantDialogRequestPath(bus.uniqueName, token)

		if err := request.Close(); err != nil {
			t.Fatalf("request Close: %v", err)
		}
		if err := request.Close(); err != nil {
			t.Fatalf("repeated request Close: %v", err)
		}
		calls := recorder.wait(t, 1)
		if !errors.Is(calls[0].err, ErrDialogCanceled) {
			t.Errorf("request Close callback error = %v, want ErrDialogCanceled", calls[0].err)
		}
		bus.mu.Lock()
		closedPaths := append([]dbus.ObjectPath(nil), bus.closedPaths...)
		bus.mu.Unlock()
		if len(closedPaths) != 1 || closedPaths[0] != wantPath {
			t.Errorf("portal Request.Close paths = %v, want exactly [%s]", closedPaths, wantPath)
		}
		bus.emitResponse(wantPath, 0, successPortalResults("file:///late.txt"))
		recorder.requireNoMore(t, 1, 200*time.Millisecond)
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("service_close_cancels_all_inflight_and_is_idempotent", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		if _, err := service.Open(DialogOptions{Title: "a"}, recorder.callback); err != nil {
			t.Fatalf("first Open: %v", err)
		}
		if _, err := service.Save(DialogOptions{Title: "b"}, recorder.callback); err != nil {
			t.Fatalf("Save: %v", err)
		}
		openCall, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
		saveCall, _ := bus.findCall("org.freedesktop.portal.FileChooser.SaveFile")
		openPath := wantDialogRequestPath(bus.uniqueName, dialogCallToken(t, dialogCallOptions(t, openCall)))
		savePath := wantDialogRequestPath(bus.uniqueName, dialogCallToken(t, dialogCallOptions(t, saveCall)))

		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
		if err := service.Close(); err != nil {
			t.Fatalf("repeated service Close: %v", err)
		}
		if bus.closeCalls != 1 {
			t.Errorf("connection Close calls = %d, want 1", bus.closeCalls)
		}
		calls := recorder.wait(t, 2)
		for i, call := range calls {
			if !errors.Is(call.err, ErrDialogCanceled) {
				t.Errorf("in-flight callback %d error = %v, want ErrDialogCanceled", i, call.err)
			}
		}
		bus.emitResponse(openPath, 0, successPortalResults("file:///late-a.txt"))
		bus.emitResponse(savePath, 0, successPortalResults("file:///late-b.txt"))
		recorder.requireNoMore(t, 2, 200*time.Millisecond)

		if _, err := service.Open(DialogOptions{Title: "after close"}, recorder.callback); !errors.Is(err, errDialogClosed) {
			t.Errorf("Open after service Close error = %v, want errDialogClosed", err)
		}
		if got := service.Capabilities(); got.FileDialog {
			t.Errorf("Capabilities() after Close = %+v, want FileDialog false", got)
		}
	})

	t.Run("independent_requests_close_one_keeps_other", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		firstRecorder := &dialogCallbackRecorder{}
		secondRecorder := &dialogCallbackRecorder{}
		first, err := service.Open(DialogOptions{Title: "first"}, firstRecorder.callback)
		if err != nil {
			t.Fatalf("first Open: %v", err)
		}
		if _, err := service.Open(DialogOptions{Title: "second"}, secondRecorder.callback); err != nil {
			t.Fatalf("second Open: %v", err)
		}
		openCalls := 0
		bus.mu.Lock()
		for _, call := range bus.calls {
			if call.method == "org.freedesktop.portal.FileChooser.OpenFile" {
				openCalls++
			}
		}
		bus.mu.Unlock()
		if openCalls != 2 {
			t.Fatalf("OpenFile calls = %d, want 2", openCalls)
		}
		// Distinct tokens keep the two requests independent.
		firstCall, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
		secondToken := ""
		bus.mu.Lock()
		for i := len(bus.calls) - 1; i >= 0; i-- {
			if bus.calls[i].method == "org.freedesktop.portal.FileChooser.OpenFile" {
				if options, ok := bus.calls[i].args[2].(map[string]dbus.Variant); ok {
					secondToken, _ = options["handle_token"].Value().(string)
				}
				break
			}
		}
		bus.mu.Unlock()
		firstToken := dialogCallToken(t, dialogCallOptions(t, firstCall))
		if firstToken == secondToken {
			t.Fatalf("both requests share handle_token %q", firstToken)
		}
		secondPath := wantDialogRequestPath(bus.uniqueName, secondToken)

		if err := first.Close(); err != nil {
			t.Fatalf("first request Close: %v", err)
		}
		firstCalls := firstRecorder.wait(t, 1)
		if !errors.Is(firstCalls[0].err, ErrDialogCanceled) {
			t.Errorf("first callback error = %v, want ErrDialogCanceled", firstCalls[0].err)
		}
		bus.emitResponse(secondPath, 0, successPortalResults("file:///second.txt"))
		secondCalls := secondRecorder.wait(t, 1)
		if secondCalls[0].err != nil {
			t.Fatalf("second callback error = %v, want success", secondCalls[0].err)
		}
		if want := []string{"/second.txt"}; !reflect.DeepEqual(secondCalls[0].result.Paths, want) {
			t.Errorf("second paths = %v, want %v", secondCalls[0].result.Paths, want)
		}
		firstRecorder.requireNoMore(t, 1, 200*time.Millisecond)
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("ignores_response_from_wrong_sender_or_path", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		if _, err := service.Open(DialogOptions{Title: "validate"}, recorder.callback); err != nil {
			t.Fatalf("Open: %v", err)
		}
		call, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
		token := dialogCallToken(t, dialogCallOptions(t, call))
		wantPath := wantDialogRequestPath(bus.uniqueName, token)

		bus.emitResponseFrom(wantPath, 0, successPortalResults("file:///spoof.txt"), ":1.99")
		bus.emitResponse("/org/freedesktop/portal/desktop/request/1_42/forged", 0, successPortalResults("file:///forged.txt"))
		recorder.requireNoMore(t, 0, 200*time.Millisecond)

		bus.emitResponse(wantPath, 0, successPortalResults("file:///real.txt"))
		calls := recorder.wait(t, 1)
		if calls[0].err != nil {
			t.Fatalf("callback error = %v, want success", calls[0].err)
		}
		if want := []string{"/real.txt"}; !reflect.DeepEqual(calls[0].result.Paths, want) {
			t.Errorf("paths = %v, want %v", calls[0].result.Paths, want)
		}
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("portal_owner_lost_fails_inflight", func(t *testing.T) {
		bus := newFakeDialogBus()
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		if _, err := service.Open(DialogOptions{Title: "owner lost"}, recorder.callback); err != nil {
			t.Fatalf("Open: %v", err)
		}
		call, _ := bus.findCall("org.freedesktop.portal.FileChooser.OpenFile")
		token := dialogCallToken(t, dialogCallOptions(t, call))
		wantPath := wantDialogRequestPath(bus.uniqueName, token)

		bus.emitPortalOwnerChanged("")
		calls := recorder.wait(t, 1)
		if !errors.Is(calls[0].err, ErrDialogUnavailable) {
			t.Errorf("owner-lost callback error = %v, want ErrDialogUnavailable", calls[0].err)
		}
		bus.emitResponse(wantPath, 0, successPortalResults("file:///stale.txt"))
		recorder.requireNoMore(t, 1, 200*time.Millisecond)
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})
}

func TestDialogUnavailableAndTimeout(t *testing.T) {
	t.Run("unavailable_without_portal", func(t *testing.T) {
		bus := newFakeDialogBus()
		bus.portalPresent = false
		service := newDialogServiceWithConn(bus)
		if service.Capabilities().FileDialog {
			t.Error("Capabilities().FileDialog = true without a portal owner")
		}
		if _, err := service.Open(DialogOptions{Title: "x"}, func(DialogResult, error) {}); !errors.Is(err, ErrDialogUnavailable) {
			t.Errorf("Open error = %v, want ErrDialogUnavailable", err)
		}
		if _, err := service.Save(DialogOptions{Title: "y"}, func(DialogResult, error) {}); !errors.Is(err, ErrDialogUnavailable) {
			t.Errorf("Save error = %v, want ErrDialogUnavailable", err)
		}
		if got := bus.callCount("org.freedesktop.portal.FileChooser.OpenFile"); got != 0 {
			t.Errorf("OpenFile calls without portal = %d, want 0", got)
		}
		if got := bus.callCount("org.freedesktop.portal.FileChooser.SaveFile"); got != 0 {
			t.Errorf("SaveFile calls without portal = %d, want 0", got)
		}
		bus.portalPresent = true
		if !service.Capabilities().FileDialog {
			t.Error("Capabilities().FileDialog = false after the portal appeared")
		}
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})

	t.Run("open_call_timeout_is_bounded", func(t *testing.T) {
		bus := newFakeDialogBus()
		bus.blockOpen = true
		service := newDialogServiceWithConn(bus)
		recorder := &dialogCallbackRecorder{}
		start := time.Now()
		_, err := service.Open(DialogOptions{Title: "slow portal"}, recorder.callback)
		if err == nil {
			t.Fatal("Open succeeded against a portal that never answers")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Open error = %v, want context deadline exceeded", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("Open took %v, want a bounded call", elapsed)
		}
		foundDeadline := false
		bus.mu.Lock()
		for _, call := range bus.calls {
			if call.method == "org.freedesktop.portal.FileChooser.OpenFile" && call.hasDeadline {
				foundDeadline = true
			}
		}
		bus.mu.Unlock()
		if !foundDeadline {
			t.Error("OpenFile was not issued through CallWithContext with a deadline")
		}
		recorder.requireNoMore(t, 0, 100*time.Millisecond)
		if err := service.Close(); err != nil {
			t.Fatalf("service Close: %v", err)
		}
	})
}

// fakePortalFileChooser is a real D-Bus exported object standing in for
// org.freedesktop.portal.Desktop on a private bus spawned by the test.
type fakePortalFileChooser struct {
	conn *dbus.Conn

	mu          sync.Mutex
	lastOptions map[string]dbus.Variant
	lastHandle  dbus.ObjectPath
	titles      []string
	closePaths  []dbus.ObjectPath
	autoRespond []string
}

// wireOptions returns the options map from the raw message body: godbus v5.1.0
// re-makes variants while storing into exported method parameters (rewriting
// the a(sa(us)) filters signature into aav), so the raw message keeps the only
// wire-faithful view.
func wireOptions(msg dbus.Message, fallback map[string]dbus.Variant) map[string]dbus.Variant {
	if len(msg.Body) == 3 {
		if options, ok := msg.Body[2].(map[string]dbus.Variant); ok {
			return options
		}
	}
	return fallback
}

func (p *fakePortalFileChooser) OpenFile(msg dbus.Message, parentWindow, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.handle("OpenFile", parentWindow, title, wireOptions(msg, options))
}

func (p *fakePortalFileChooser) SaveFile(msg dbus.Message, parentWindow, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	return p.handle("SaveFile", parentWindow, title, wireOptions(msg, options))
}

func (p *fakePortalFileChooser) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface == "org.freedesktop.portal.FileChooser" && name == "version" {
		return dbus.MakeVariant(uint32(4)), nil
	}
	return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs",
		[]interface{}{fmt.Sprintf("unknown property %s.%s", iface, name)})
}

func (p *fakePortalFileChooser) handle(kind, parentWindow, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	if parentWindow != "" {
		return "", dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs",
			[]interface{}{fmt.Sprintf("parent_window = %q, want empty", parentWindow)})
	}
	p.mu.Lock()
	p.lastOptions = options
	p.titles = append(p.titles, kind+":"+title)
	auto := p.autoRespond
	p.mu.Unlock()

	token, _ := options["handle_token"].Value().(string)
	if token == "" {
		return "", dbus.NewError("org.freedesktop.portal.Error.InvalidToken", []interface{}{"missing handle_token"})
	}
	sender := p.peerUniqueName()
	if sender == "" {
		return "", dbus.NewError("org.freedesktop.DBus.Error.Failed", []interface{}{"no peer on the private bus"})
	}
	handle := wantDialogRequestPath(sender, token)
	if err := p.conn.Export(&fakePortalRequest{portal: p, path: handle}, handle, "org.freedesktop.portal.Request"); err != nil {
		return "", dbus.NewError("org.freedesktop.DBus.Error.Failed", []interface{}{err.Error()})
	}
	p.mu.Lock()
	p.lastHandle = handle
	p.mu.Unlock()
	if len(auto) > 0 {
		results := map[string]dbus.Variant{"uris": dbus.MakeVariant(append([]string(nil), auto...))}
		if err := p.conn.Emit(handle, "org.freedesktop.portal.Request.Response", uint32(0), results); err != nil {
			return "", dbus.NewError("org.freedesktop.DBus.Error.Failed", []interface{}{err.Error()})
		}
	}
	return handle, nil
}

// peerUniqueName finds the only other client on the private bus, which is the
// Meltemi dialog service; the portal request path embeds that sender name.
func (p *fakePortalFileChooser) peerUniqueName() string {
	own := map[string]bool{"org.freedesktop.DBus": true}
	for _, name := range p.conn.Names() {
		own[name] = true
	}
	call := p.conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0)
	var names []string
	if err := call.Store(&names); err != nil {
		return ""
	}
	for _, name := range names {
		if !own[name] && strings.HasPrefix(name, ":") {
			return name
		}
	}
	return ""
}

func (p *fakePortalFileChooser) emitResponse(t *testing.T, handle dbus.ObjectPath, code uint32, uris []string) {
	t.Helper()
	results := map[string]dbus.Variant{"uris": dbus.MakeVariant(uris)}
	if err := p.conn.Emit(handle, "org.freedesktop.portal.Request.Response", code, results); err != nil {
		t.Fatalf("emit portal Response: %v", err)
	}
}

type fakePortalRequest struct {
	portal *fakePortalFileChooser
	path   dbus.ObjectPath
}

func (r *fakePortalRequest) Close() *dbus.Error {
	r.portal.mu.Lock()
	defer r.portal.mu.Unlock()
	r.portal.closePaths = append(r.portal.closePaths, r.path)
	return nil
}

func TestFileChooserPortalPrivateBus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping private-bus portal integration test in -short mode")
	}
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skipf("dbus-daemon not installed: %v", err)
	}
	cmd := exec.Command(daemon, "--session", "--print-address=1", "--nofork", "--nopidfile")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read private bus address: %v", err)
	}
	address = strings.TrimSpace(address)
	if address == "" {
		t.Fatal("dbus-daemon printed an empty address")
	}

	portalConn, err := dbus.Dial(address)
	if err != nil {
		t.Fatalf("dial private bus: %v", err)
	}
	defer portalConn.Close()
	if err := portalConn.Auth(nil); err != nil {
		t.Fatalf("authenticate private bus: %v", err)
	}
	if err := portalConn.Hello(); err != nil {
		t.Fatalf("hello private bus: %v", err)
	}

	portal := &fakePortalFileChooser{conn: portalConn}
	reply, err := portalConn.RequestName(testPortalDestination, dbus.NameFlagDoNotQueue)
	if err != nil {
		t.Fatalf("request portal name: %v", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request portal name reply = %d, want primary owner", reply)
	}
	if err := portalConn.Export(portal, testPortalDesktopPath, "org.freedesktop.portal.FileChooser"); err != nil {
		t.Fatalf("export fake FileChooser: %v", err)
	}
	if err := portalConn.Export(portal, testPortalDesktopPath, "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatalf("export fake Properties: %v", err)
	}

	serviceConn, err := connectSessionBusAddress(context.Background(), address, (&net.Dialer{}).DialContext)
	if err != nil {
		t.Fatalf("connect dialog service to private bus: %v", err)
	}
	service := newDialogServiceWithConn(serviceConn)
	defer service.Close()

	if !service.Capabilities().FileDialog {
		t.Fatal("Capabilities().FileDialog = false with a fake portal on a private bus")
	}

	recorder := &dialogCallbackRecorder{}
	request, err := service.Open(DialogOptions{
		Title:         "Integration open",
		AcceptLabel:   "Pick",
		Modal:         true,
		CurrentFolder: "/tmp",
		Filters: []FileFilter{{
			Name:     "Go files",
			Patterns: []FileFilterPattern{{Kind: FileFilterPatternGlob, Pattern: "*.go"}},
		}},
	}, recorder.callback)
	if err != nil {
		t.Fatalf("Open over private bus: %v", err)
	}
	if request == nil {
		t.Fatal("Open returned a nil request over the private bus")
	}

	portal.mu.Lock()
	options := portal.lastOptions
	handle := portal.lastHandle
	portal.mu.Unlock()
	if options == nil {
		t.Fatal("fake portal recorded no options")
	}
	if token, _ := options["handle_token"].Value().(string); token == "" {
		t.Error("handle_token missing on the wire")
	}
	filtersVariant, ok := options["filters"]
	if !ok {
		t.Fatal("filters missing on the wire")
	}
	if got := filtersVariant.Signature().String(); got != "a(sa(us))" {
		t.Errorf("filters wire signature = %q, want a(sa(us))", got)
	}
	filters, ok := filtersVariant.Value().([][]interface{})
	if !ok || len(filters) != 1 {
		t.Fatalf("wire filters = %#v, want one filter", filtersVariant.Value())
	}
	filter := filters[0]
	if len(filter) != 2 || filter[0] != "Go files" {
		t.Fatalf("wire filter entry = %#v, want name + patterns", filter)
	}
	patterns, ok := filter[1].([][]interface{})
	if !ok || len(patterns) != 1 {
		t.Fatalf("wire filter patterns = %#v, want one pattern", filter[1])
	}
	pattern := patterns[0]
	if len(pattern) != 2 || pattern[0] != uint32(0) || pattern[1] != "*.go" {
		t.Errorf("wire filter pattern = %#v, want [0 *.go]", pattern)
	}
	if got, ok := options["current_folder"].Value().([]byte); !ok || string(got) != "/tmp\x00" {
		t.Errorf("current_folder on the wire = %v, want NUL-terminated /tmp", options["current_folder"].Value())
	}

	portal.emitResponse(t, handle, 0, []string{"file:///tmp/wind integration.txt", "file:///tmp/second.go"})
	calls := recorder.wait(t, 1)
	if calls[0].err != nil {
		t.Fatalf("integration callback error = %v", calls[0].err)
	}
	wantPaths := []string{"/tmp/wind integration.txt", "/tmp/second.go"}
	if !reflect.DeepEqual(calls[0].result.Paths, wantPaths) {
		t.Errorf("integration paths = %v, want %v", calls[0].result.Paths, wantPaths)
	}

	// A response emitted while the OpenFile reply is still in flight must not
	// be lost either.
	portal.mu.Lock()
	portal.autoRespond = []string{"file:///instant.txt"}
	portal.mu.Unlock()
	fastRecorder := &dialogCallbackRecorder{}
	fastRequest, err := service.Open(DialogOptions{Title: "fast"}, fastRecorder.callback)
	if err != nil {
		t.Fatalf("fast Open over private bus: %v", err)
	}
	if fastRequest == nil {
		t.Fatal("fast Open returned a nil request")
	}
	fastCalls := fastRecorder.wait(t, 1)
	if fastCalls[0].err != nil {
		t.Fatalf("fast integration callback error = %v", fastCalls[0].err)
	}
	if want := []string{"/instant.txt"}; !reflect.DeepEqual(fastCalls[0].result.Paths, want) {
		t.Errorf("fast integration paths = %v, want %v", fastCalls[0].result.Paths, want)
	}
	portal.mu.Lock()
	portal.autoRespond = nil
	portal.mu.Unlock()

	// Request.Close reaches the portal over the real bus.
	cancelRecorder := &dialogCallbackRecorder{}
	cancelRequest, err := service.Save(DialogOptions{Title: "cancel", CurrentName: "out.txt"}, cancelRecorder.callback)
	if err != nil {
		t.Fatalf("Save over private bus: %v", err)
	}
	portal.mu.Lock()
	cancelHandle := portal.lastHandle
	portal.mu.Unlock()
	if err := cancelRequest.Close(); err != nil {
		t.Fatalf("request Close over private bus: %v", err)
	}
	cancelCalls := cancelRecorder.wait(t, 1)
	if !errors.Is(cancelCalls[0].err, ErrDialogCanceled) {
		t.Errorf("private-bus cancel error = %v, want ErrDialogCanceled", cancelCalls[0].err)
	}
	portal.mu.Lock()
	closePaths := append([]dbus.ObjectPath(nil), portal.closePaths...)
	portal.mu.Unlock()
	if len(closePaths) != 1 || closePaths[0] != cancelHandle {
		t.Errorf("portal Request.Close paths = %v, want exactly [%s]", closePaths, cancelHandle)
	}
}
