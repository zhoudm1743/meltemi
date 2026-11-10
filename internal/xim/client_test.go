package xim

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"
	"github.com/godbus/dbus/v5"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

const (
	testInputMethodPath  = dbus.ObjectPath("/org/freedesktop/portal/inputmethod")
	testInputContextPath = dbus.ObjectPath("/org/freedesktop/portal/inputcontext/1")
	testFcitxSender      = ":1.42"
)

type testPreeditPart struct {
	Text   string
	Format int32
}

type recordedCall struct {
	method string
	args   []interface{}
}

const fakeContextWatchdog = 750 * time.Millisecond

type fakeBus struct {
	inputMethod      *fakeBusObject
	inputContext     *fakeBusObject
	busObject        *fakeBusObject
	operations       []string
	signal           chan<- *dbus.Signal
	matches          int
	matchOptions     [][]dbus.MatchOption
	createErr        error
	matchErr         error
	removeMatchErr   error
	closeErr         error
	blockAddMatch    bool
	blockRemoveMatch bool
	closed           bool
}

func newFakeBus() *fakeBus {
	bus := &fakeBus{}
	bus.inputMethod = &fakeBusObject{
		bus:  bus,
		path: testInputMethodPath,
	}
	bus.inputContext = &fakeBusObject{
		bus:        bus,
		path:       testInputContextPath,
		keyHandled: true,
	}
	bus.busObject = &fakeBusObject{
		bus:  bus,
		path: dbus.ObjectPath("/org/freedesktop/DBus"),
	}
	return bus
}

func (b *fakeBus) Object(_ string, path dbus.ObjectPath) dbus.BusObject {
	if path == testInputMethodPath {
		return b.inputMethod
	}
	return b.inputContext
}

func (b *fakeBus) BusObject() dbus.BusObject {
	return b.busObject
}

func (b *fakeBus) AddMatchSignal(options ...dbus.MatchOption) error {
	if b.matchErr != nil {
		return b.matchErr
	}
	b.matches++
	b.matchOptions = append(b.matchOptions, append([]dbus.MatchOption(nil), options...))
	b.operations = append(b.operations, "add-match")
	return nil
}

func (b *fakeBus) AddMatchSignalContext(ctx context.Context, options ...dbus.MatchOption) error {
	if b.blockAddMatch {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(fakeContextWatchdog):
			return errors.New("fake AddMatchSignal context was not canceled")
		}
	}
	return b.AddMatchSignal(options...)
}

func (b *fakeBus) RemoveMatchSignal(options ...dbus.MatchOption) error {
	return b.RemoveMatchSignalContext(context.Background(), options...)
}

func (b *fakeBus) RemoveMatchSignalContext(ctx context.Context, _ ...dbus.MatchOption) error {
	b.operations = append(b.operations, "remove-match")
	if b.blockRemoveMatch {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(fakeContextWatchdog):
			return errors.New("fake RemoveMatchSignal context was not canceled")
		}
	}
	return b.removeMatchErr
}

func (b *fakeBus) Signal(ch chan<- *dbus.Signal) {
	b.signal = ch
	b.operations = append(b.operations, "signal")
}

func (b *fakeBus) RemoveSignal(ch chan<- *dbus.Signal) {
	if b.signal == ch {
		b.signal = nil
	}
	b.operations = append(b.operations, "remove-signal")
}

func (b *fakeBus) Close() error {
	b.closed = true
	b.operations = append(b.operations, "close")
	return b.closeErr
}

type fakeBusObject struct {
	bus         *fakeBus
	path        dbus.ObjectPath
	calls       []recordedCall
	keyHandled  bool
	callErr     map[string]error
	blockMethod string
}

func (o *fakeBusObject) Call(method string, flags dbus.Flags, args ...interface{}) *dbus.Call {
	return o.callWithContext(context.Background(), method, args...)
}

func (o *fakeBusObject) CallWithContext(ctx context.Context, method string, _ dbus.Flags, args ...interface{}) *dbus.Call {
	return o.callWithContext(ctx, method, args...)
}

func (o *fakeBusObject) callWithContext(ctx context.Context, method string, args ...interface{}) *dbus.Call {
	o.calls = append(o.calls, recordedCall{method: method, args: args})
	if method == "org.fcitx.Fcitx.InputContext1.DestroyIC" {
		o.bus.operations = append(o.bus.operations, "destroy")
	}
	if o.blockMethod == method {
		select {
		case <-ctx.Done():
			return &dbus.Call{Err: ctx.Err()}
		case <-time.After(fakeContextWatchdog):
			return &dbus.Call{Err: errors.New("fake " + method + " context was not canceled")}
		}
	}
	if err := o.callErr[method]; err != nil {
		return &dbus.Call{Err: err}
	}
	switch method {
	case "org.fcitx.Fcitx.InputMethod1.CreateInputContext":
		o.bus.operations = append(o.bus.operations, "create")
		if o.bus.createErr != nil {
			return &dbus.Call{Err: o.bus.createErr}
		}
		return &dbus.Call{Body: []interface{}{testInputContextPath, []byte{1, 2, 3}}}
	case "org.freedesktop.DBus.GetNameOwner":
		return &dbus.Call{Body: []interface{}{testFcitxSender}}
	case "org.fcitx.Fcitx.InputContext1.ProcessKeyEvent":
		o.bus.operations = append(o.bus.operations, "key")
		return &dbus.Call{Body: []interface{}{o.keyHandled}}
	}
	return &dbus.Call{}
}

func (o *fakeBusObject) Go(method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
	return o.Call(method, flags, args...)
}

func (o *fakeBusObject) GoWithContext(ctx context.Context, method string, flags dbus.Flags, ch chan *dbus.Call, args ...interface{}) *dbus.Call {
	return o.CallWithContext(ctx, method, flags, args...)
}

func (o *fakeBusObject) AddMatchSignal(_, _ string, _ ...dbus.MatchOption) *dbus.Call {
	return &dbus.Call{}
}

func (o *fakeBusObject) RemoveMatchSignal(_, _ string, _ ...dbus.MatchOption) *dbus.Call {
	return &dbus.Call{}
}

func (o *fakeBusObject) GetProperty(string) (dbus.Variant, error) {
	return dbus.Variant{}, nil
}

func (o *fakeBusObject) StoreProperty(string, interface{}) error {
	return nil
}

func (o *fakeBusObject) SetProperty(string, interface{}) error {
	return nil
}

func (o *fakeBusObject) Destination() string {
	return "org.freedesktop.portal.Fcitx"
}

func (o *fakeBusObject) Path() dbus.ObjectPath {
	return o.path
}

func newTestClient(t *testing.T) (*Client, *fakeBus) {
	t.Helper()
	bus := newFakeBus()
	client, err := newClientWithConn(context.Background(), bus, "meltemi-test", ":0")
	if err != nil {
		t.Fatalf("newClientWithConn: %v", err)
	}
	return client, bus
}

func focusTestClient(t *testing.T, client *Client) {
	t.Helper()
	if err := client.SetFocus(true); err != nil {
		t.Fatalf("SetFocus(true): %v", err)
	}
}

func sendTestSignal(client *Client, name string, body ...interface{}) {
	client.signals <- &dbus.Signal{
		Sender: testFcitxSender,
		Name:   name,
		Path:   testInputContextPath,
		Body:   body,
	}
}

func TestClientCreatesPersistentInputContext(t *testing.T) {
	client, bus := newTestClient(t)
	defer client.Close()

	if bus.signal == nil {
		t.Fatal("no D-Bus signal channel registered")
	}
	if bus.matches != 1 {
		t.Fatalf("signal matches = %d, want 1", bus.matches)
	}
	calls := bus.inputMethod.calls
	if len(calls) != 1 {
		t.Fatalf("InputMethod calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.method != "org.fcitx.Fcitx.InputMethod1.CreateInputContext" {
		t.Fatalf("method = %q, want CreateInputContext", call.method)
	}
	wantArgs := []interface{}{[]struct {
		Name  string
		Value string
	}{{"program", "meltemi-test"}, {"display", "x11::0"}}}
	if got := dbus.SignatureOf(call.args...).String(); got != "a(ss)" {
		t.Fatalf("CreateInputContext wire signature = %q, want %q", got, "a(ss)")
	}
	if !reflect.DeepEqual(call.args, wantArgs) {
		t.Fatalf("CreateInputContext args = %#v, want %#v", call.args, wantArgs)
	}
	if client.inputContext == nil || client.inputContext.Path() != testInputContextPath {
		t.Fatalf("input context = %v, want path %q", client.inputContext, testInputContextPath)
	}
	if len(bus.inputContext.calls) != 1 {
		t.Fatalf("input context setup calls = %#v, want one SetCapability", bus.inputContext.calls)
	}
	setup := bus.inputContext.calls[0]
	if setup.method != "org.fcitx.Fcitx.InputContext1.SetCapability" || !reflect.DeepEqual(setup.args, []interface{}{uint64(18)}) {
		t.Fatalf("input context setup = %#v, want SetCapability(Preedit|FormattedPreedit)", setup)
	}
	if bus.closed {
		t.Fatal("NewClient closed the persistent connection")
	}
}

func TestClientMatchLimitsFcitxSender(t *testing.T) {
	client, bus := newTestClient(t)
	defer client.Close()

	if len(bus.matchOptions) != 1 {
		t.Fatalf("recorded match rules = %d, want 1", len(bus.matchOptions))
	}
	foundSender := false
	for _, option := range bus.matchOptions[0] {
		value := reflect.ValueOf(option)
		key := value.FieldByName("key").String()
		item := value.FieldByName("value").String()
		if key == "sender" && item == fcitxPortalService {
			foundSender = true
		}
	}
	if !foundSender {
		t.Fatalf("input-context match has no Fcitx sender restriction: %#v", bus.matchOptions[0])
	}
}

func TestClientCreateFailureClosesConnection(t *testing.T) {
	bus := newFakeBus()
	bus.createErr = errors.New("create failed")
	client, err := newClientWithConn(context.Background(), bus, "meltemi-test", ":0")
	if err == nil || client != nil {
		t.Fatalf("newClientWithConn = (%v, %v), want (nil, error)", client, err)
	}
	if !bus.closed {
		t.Fatal("connection left open after CreateInputContext failure")
	}
	if len(bus.inputContext.calls) != 0 {
		t.Fatalf("input context calls after failed creation = %#v", bus.inputContext.calls)
	}
}

func TestClientMatchFailureDestroysInputContextBeforeClose(t *testing.T) {
	bus := newFakeBus()
	bus.matchErr = errors.New("match failed")
	client, err := newClientWithConn(context.Background(), bus, "meltemi-test", ":0")
	if err == nil || client != nil {
		t.Fatalf("newClientWithConn = (%v, %v), want (nil, error)", client, err)
	}
	if !bus.closed {
		t.Fatal("connection left open after signal subscription failure")
	}
	assertOperationOrder(t, bus.operations, "destroy", "close")
}

func TestClientInitializationFailureJoinsCleanupErrors(t *testing.T) {
	for _, test := range []struct {
		name         string
		errorContext string
	}{
		{name: "CreateInputContext", errorContext: "xim: create input context:"},
		{name: "SetCapability", errorContext: "xim: set input context capabilities:"},
		{name: "AddMatch", errorContext: "xim: subscribe input context:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bus := newFakeBus()
			primaryErr := errors.New(test.name + " primary error")
			unsubscribeErr := errors.New("unsubscribe cleanup error")
			destroyErr := errors.New("destroy cleanup error")
			closeErr := errors.New("close cleanup error")
			bus.closeErr = closeErr
			wantErrors := []error{primaryErr, closeErr}

			switch test.name {
			case "CreateInputContext":
				bus.createErr = primaryErr
			case "SetCapability":
				bus.inputContext.callErr = map[string]error{
					"org.fcitx.Fcitx.InputContext1.SetCapability": primaryErr,
					"org.fcitx.Fcitx.InputContext1.DestroyIC":     destroyErr,
				}
				bus.removeMatchErr = unsubscribeErr
				wantErrors = []error{primaryErr, unsubscribeErr, destroyErr, closeErr}
			case "AddMatch":
				bus.matchErr = primaryErr
				bus.inputContext.callErr = map[string]error{
					"org.fcitx.Fcitx.InputContext1.DestroyIC": destroyErr,
				}
				bus.removeMatchErr = unsubscribeErr
				wantErrors = []error{primaryErr, unsubscribeErr, destroyErr, closeErr}
			}

			client, err := newClientWithConn(context.Background(), bus, "meltemi-test", ":0")
			if err == nil || client != nil {
				t.Fatalf("newClientWithConn = (%v, %v), want (nil, error)", client, err)
			}
			if !strings.HasPrefix(err.Error(), test.errorContext) {
				t.Fatalf("error = %q, want initial context %q first", err, test.errorContext)
			}
			for _, wantErr := range wantErrors {
				if !errors.Is(err, wantErr) {
					t.Errorf("error = %v, want errors.Join to retain %v", err, wantErr)
				}
			}
			if bus.signal != nil {
				t.Error("initialization failure left the signal channel registered")
			}
			if !bus.closed {
				t.Error("initialization failure left the connection open")
			}
		})
	}
}

func TestClientUpdateFormattedPreeditConcatenatesTextAndClampsRuneCaret(t *testing.T) {
	client, _ := newTestClient(t)
	defer client.Close()
	focusTestClient(t, client)

	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
		[]testPreeditPart{{Text: "你", Format: 0}, {Text: "a🙂", Format: 1}}, int32(99))
	want := Preedit{Text: "你a🙂", Caret: 3, Active: true}
	if got := client.Preedit(); got != want {
		t.Fatalf("Preedit() = %+v, want %+v", got, want)
	}

	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
		[]testPreeditPart{{Text: "你a🙂", Format: 0}}, int32(4))
	if got := client.Preedit().Caret; got != 2 {
		t.Fatalf("byte caret 4 in %q became rune caret %d, want 2", "你a🙂", got)
	}

	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
		[]testPreeditPart{{Text: "你a🙂", Format: 0}}, int32(-4))
	if got := client.Preedit().Caret; got != 0 {
		t.Fatalf("negative caret = %d, want 0", got)
	}

	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
		[]testPreeditPart{}, int32(-1))
	if got := client.Preedit(); got != (Preedit{}) {
		t.Fatalf("empty preedit update = %+v, want canceled state", got)
	}
}

func TestClientCommitStringEmitsEventRunesOnceAndClearsPreedit(t *testing.T) {
	client, _ := newTestClient(t)
	defer client.Close()
	focusTestClient(t, client)
	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
		[]testPreeditPart{{Text: "ni", Format: 0}}, int32(2))
	if got := client.Preedit(); !got.Active {
		t.Fatalf("preedit before commit = %+v, want active", got)
	}

	var events []driver.Event
	client.SetEventHandler(func(event driver.Event) {
		if state := client.Preedit(); state != (Preedit{}) {
			t.Errorf("preedit during commit = %+v, want empty", state)
		}
		events = append(events, event)
	})
	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.CommitString", "中🙂")
	client.Preedit()

	want := []driver.Event{
		{Kind: driver.EventRune, Rune: '中'},
		{Kind: driver.EventRune, Rune: '🙂'},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("committed events = %#v, want %#v", events, want)
	}
	if got := client.Preedit(); got != (Preedit{}) {
		t.Fatalf("preedit after commit = %+v, want empty", got)
	}

	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.CommitString", "")
	client.Preedit()
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("empty CommitString generated events = %#v", events)
	}
}

func TestClientAsyncSignalWakesWithoutDispatchingOffUIThread(t *testing.T) {
	client, bus := newTestClient(t)
	defer client.Close()
	focusTestClient(t, client)
	emitted := make(chan driver.Event, 1)
	client.SetEventHandler(func(event driver.Event) { emitted <- event })
	wakes := make(chan struct{}, 1)
	client.SetWakeHandler(func() {
		select {
		case wakes <- struct{}{}:
		default:
		}
	})

	go func() {
		bus.signal <- &dbus.Signal{
			Sender: testFcitxSender,
			Name:   "org.fcitx.Fcitx.InputContext1.CommitString",
			Path:   testInputContextPath,
			Body:   []interface{}{"你"},
		}
	}()
	select {
	case <-wakes:
	case <-time.After(time.Second):
		t.Fatal("asynchronous D-Bus signal did not invoke the wake handler")
	}
	select {
	case event := <-emitted:
		t.Fatalf("D-Bus goroutine emitted UI event before UI dispatch: %+v", event)
	default:
	}

	client.Preedit()
	select {
	case event := <-emitted:
		if event.Kind != driver.EventRune || event.Rune != '你' {
			t.Fatalf("UI-dispatched event = %+v, want EventRune 你", event)
		}
	case <-time.After(time.Second):
		t.Fatal("UI dispatch did not deliver the asynchronous CommitString")
	}
}

func TestClientProcessKeyEventSendsOneDBusCall(t *testing.T) {
	client, bus := newTestClient(t)
	defer client.Close()
	focusTestClient(t, client)

	handled, err := client.ProcessKeyEvent(110, 57, 0, false, 1234)
	if err != nil {
		t.Fatalf("ProcessKeyEvent: %v", err)
	}
	if !handled {
		t.Fatal("ProcessKeyEvent handled = false, want true")
	}
	var calls []recordedCall
	for _, call := range bus.inputContext.calls {
		if call.method == "org.fcitx.Fcitx.InputContext1.ProcessKeyEvent" {
			calls = append(calls, call)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("ProcessKeyEvent D-Bus calls = %d, want 1", len(calls))
	}
	wantArgs := []interface{}{uint32(110), uint32(57), uint32(0), false, uint32(1234)}
	if !reflect.DeepEqual(calls[0].args, wantArgs) {
		t.Fatalf("ProcessKeyEvent args = %#v, want %#v", calls[0].args, wantArgs)
	}
}

func TestClientSetFocusAndSpot(t *testing.T) {
	client, bus := newTestClient(t)
	defer client.Close()
	focusTestClient(t, client)
	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
		[]testPreeditPart{{Text: "拼", Format: 0}}, int32(1))
	if got := client.Preedit(); !got.Active {
		t.Fatalf("preedit before focus loss = %+v", got)
	}

	if err := client.SetSpot(24, 96, 3, 20); err != nil {
		t.Fatalf("SetSpot: %v", err)
	}
	if err := client.SetFocus(false); err != nil {
		t.Fatalf("SetFocus(false): %v", err)
	}
	if got := client.Preedit(); got != (Preedit{}) {
		t.Fatalf("preedit after focus loss = %+v, want empty", got)
	}

	var methods []string
	for _, call := range bus.inputContext.calls {
		methods = append(methods, call.method)
		if call.method == "org.fcitx.Fcitx.InputContext1.SetCursorRect" {
			wantArgs := []interface{}{int32(24), int32(96), int32(3), int32(20)}
			if !reflect.DeepEqual(call.args, wantArgs) {
				t.Fatalf("SetCursorRect args = %#v, want %#v", call.args, wantArgs)
			}
		}
	}
	for _, method := range []string{
		"org.fcitx.Fcitx.InputContext1.FocusIn",
		"org.fcitx.Fcitx.InputContext1.FocusOut",
		"org.fcitx.Fcitx.InputContext1.SetCursorRect",
	} {
		if !containsString(methods, method) {
			t.Errorf("missing D-Bus call %s; calls = %v", method, methods)
		}
	}
}

func TestClientRejectsSignalsFromDifferentSender(t *testing.T) {
	t.Run("CommitString", func(t *testing.T) {
		client, _ := newTestClient(t)
		defer client.Close()
		focusTestClient(t, client)
		var events []driver.Event
		client.SetEventHandler(func(event driver.Event) { events = append(events, event) })

		client.signals <- &dbus.Signal{
			Sender: ":1.attacker",
			Name:   "org.fcitx.Fcitx.InputContext1.CommitString",
			Path:   testInputContextPath,
			Body:   []interface{}{"伪"},
		}
		client.Preedit()
		if len(events) != 0 {
			t.Fatalf("same path/interface from a different sender committed events = %#v", events)
		}
	})

	t.Run("UpdateFormattedPreedit", func(t *testing.T) {
		client, _ := newTestClient(t)
		defer client.Close()
		focusTestClient(t, client)

		client.signals <- &dbus.Signal{
			Sender: ":1.attacker",
			Name:   "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
			Path:   testInputContextPath,
			Body:   []interface{}{[]testPreeditPart{{Text: "伪造", Format: 0}}, int32(4)},
		}
		if got := client.Preedit(); got != (Preedit{}) {
			t.Fatalf("same path/interface from a different sender left preedit = %+v", got)
		}
	})
}

func TestClientOperationsTimeOut(t *testing.T) {
	tests := []struct {
		name   string
		method string
		run    func(*Client) error
	}{
		{
			name:   "FocusIn",
			method: "org.fcitx.Fcitx.InputContext1.FocusIn",
			run:    func(client *Client) error { return client.SetFocus(true) },
		},
		{
			name:   "FocusOut",
			method: "org.fcitx.Fcitx.InputContext1.FocusOut",
			run:    func(client *Client) error { return client.SetFocus(false) },
		},
		{
			name:   "SetSpot",
			method: "org.fcitx.Fcitx.InputContext1.SetCursorRect",
			run:    func(client *Client) error { return client.SetSpot(24, 96, 1, 1) },
		},
		{
			name:   "ProcessKeyEvent",
			method: "org.fcitx.Fcitx.InputContext1.ProcessKeyEvent",
			run: func(client *Client) error {
				_, err := client.ProcessKeyEvent(110, 57, 0, false, 1234)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, bus := newTestClient(t)
			defer client.Close()
			bus.inputContext.blockMethod = test.method

			started := time.Now()
			err := test.run(client)
			elapsed := time.Since(started)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s error = %v, want context deadline exceeded", test.name, err)
			}
			if elapsed >= fakeContextWatchdog {
				t.Fatalf("%s waited %v, want a bounded context before %v", test.name, elapsed, fakeContextWatchdog)
			}
		})
	}
}

func TestClientInitializationCallsTimeOut(t *testing.T) {
	for _, test := range []struct {
		name  string
		block func(*fakeBus)
	}{
		{
			name: "CreateInputContext",
			block: func(bus *fakeBus) {
				bus.inputMethod.blockMethod = "org.fcitx.Fcitx.InputMethod1.CreateInputContext"
			},
		},
		{
			name: "GetNameOwner",
			block: func(bus *fakeBus) {
				bus.busObject.blockMethod = "org.freedesktop.DBus.GetNameOwner"
			},
		},
		{
			name: "SetCapability",
			block: func(bus *fakeBus) {
				bus.inputContext.blockMethod = "org.fcitx.Fcitx.InputContext1.SetCapability"
			},
		},
		{
			name: "AddMatch",
			block: func(bus *fakeBus) {
				bus.blockAddMatch = true
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bus := newFakeBus()
			test.block(bus)
			started := time.Now()
			client, err := newClientWithConn(context.Background(), bus, "meltemi-test", ":0")
			elapsed := time.Since(started)
			if client != nil {
				t.Fatalf("blocked %s returned a live client", test.name)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s error = %v, want context deadline exceeded", test.name, err)
			}
			if elapsed >= fakeContextWatchdog {
				t.Fatalf("%s waited %v, want a bounded context before %v", test.name, elapsed, fakeContextWatchdog)
			}
			if !bus.closed {
				t.Fatalf("blocked %s left the connection open", test.name)
			}
		})
	}
}

func TestClientIgnoresUnfocusedForeignAndLateSignals(t *testing.T) {
	client, _ := newTestClient(t)
	focusTestClient(t, client)
	var events []driver.Event
	client.SetEventHandler(func(event driver.Event) {
		events = append(events, event)
	})

	client.signals <- &dbus.Signal{
		Sender: testFcitxSender,
		Name:   "org.fcitx.Fcitx.InputContext1.CommitString",
		Path:   dbus.ObjectPath("/org/freedesktop/portal/inputcontext/another"),
		Body:   []interface{}{"外"},
	}
	client.Preedit()
	if len(events) != 0 {
		t.Fatalf("foreign input context committed = %#v", events)
	}

	if err := client.SetFocus(false); err != nil {
		t.Fatalf("SetFocus(false): %v", err)
	}
	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.CommitString", "晚")
	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
		[]testPreeditPart{{Text: "残留", Format: 0}}, int32(2))
	if got := client.Preedit(); got != (Preedit{}) {
		t.Fatalf("unfocused signals left preedit = %+v", got)
	}
	if len(events) != 0 {
		t.Fatalf("unfocused CommitString generated events = %#v", events)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.CommitString", "后")
	client.Preedit()
	if len(events) != 0 {
		t.Fatalf("late CommitString after Close generated events = %#v", events)
	}
}

func TestClientCloseCancelsDestroysBeforeClosingAndIsIdempotent(t *testing.T) {
	client, bus := newTestClient(t)
	focusTestClient(t, client)
	sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
		[]testPreeditPart{{Text: "未提交", Format: 0}}, int32(3))
	if got := client.Preedit(); !got.Active {
		t.Fatalf("preedit before Close = %+v", got)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := client.Preedit(); got != (Preedit{}) {
		t.Fatalf("preedit after Close = %+v, want empty", got)
	}
	if !bus.closed {
		t.Fatal("Close did not close the private D-Bus connection")
	}
	assertOperationOrder(t, bus.operations, "destroy", "close")
	if bus.signal != nil {
		t.Fatal("Close left the signal channel registered")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	destroyCalls := 0
	for _, call := range bus.inputContext.calls {
		if call.method == "org.fcitx.Fcitx.InputContext1.DestroyIC" {
			destroyCalls++
		}
	}
	if destroyCalls != 1 {
		t.Fatalf("DestroyIC calls = %d, want 1", destroyCalls)
	}
	if _, err := client.ProcessKeyEvent(110, 57, 0, false, 0); !errors.Is(err, errClientClosed) {
		t.Fatalf("ProcessKeyEvent after Close error = %v, want errClientClosed", err)
	}
	if err := client.SetFocus(true); !errors.Is(err, errClientClosed) {
		t.Fatalf("SetFocus after Close error = %v, want errClientClosed", err)
	}
	if err := client.SetSpot(0, 0, 0, 0); !errors.Is(err, errClientClosed) {
		t.Fatalf("SetSpot after Close error = %v, want errClientClosed", err)
	}
}

func TestClientCloseTimeoutStillReleasesResources(t *testing.T) {
	for _, test := range []struct {
		name  string
		block func(*fakeBus)
	}{
		{
			name: "unsubscribe",
			block: func(bus *fakeBus) {
				bus.blockRemoveMatch = true
			},
		},
		{
			name: "DestroyIC",
			block: func(bus *fakeBus) {
				bus.inputContext.blockMethod = "org.fcitx.Fcitx.InputContext1.DestroyIC"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, bus := newTestClient(t)
			focusTestClient(t, client)
			sendTestSignal(client, "org.fcitx.Fcitx.InputContext1.UpdateFormattedPreedit",
				[]testPreeditPart{{Text: "关闭前", Format: 0}}, int32(6))
			if got := client.Preedit(); !got.Active {
				t.Fatalf("preedit before Close = %+v, want active", got)
			}
			callerCtx, cancelCaller := context.WithCancel(context.Background())
			cancelCaller()
			client.ctx = callerCtx
			test.block(bus)

			started := time.Now()
			err := client.Close()
			elapsed := time.Since(started)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Close error = %v, want context deadline exceeded", err)
			}
			if elapsed >= fakeContextWatchdog {
				t.Fatalf("Close waited %v, want an independent timeout before %v", elapsed, fakeContextWatchdog)
			}
			if got := client.Preedit(); got != (Preedit{}) {
				t.Fatalf("preedit after timed-out Close = %+v, want empty", got)
			}
			if bus.signal != nil {
				t.Fatal("timed-out Close left the signal channel registered")
			}
			if !bus.closed {
				t.Fatal("timed-out Close did not close the connection")
			}
			assertOperationOrder(t, bus.operations, "remove-signal", "close")
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertOperationOrder(t *testing.T, operations []string, first, second string) {
	t.Helper()
	firstIndex, secondIndex := -1, -1
	for i, operation := range operations {
		if operation == first && firstIndex == -1 {
			firstIndex = i
		}
		if operation == second && secondIndex == -1 {
			secondIndex = i
		}
	}
	if firstIndex == -1 || secondIndex == -1 || firstIndex >= secondIndex {
		t.Fatalf("operation order = %v, want %s before %s", operations, first, second)
	}
}

func TestXIMPreeditAndCommitOnce(t *testing.T) {
	if os.Getenv("MELTEMI_GLFW_DESKTOP_TESTS") != "1" && os.Getenv("WIND_GLFW_DESKTOP_TESTS") != "1" {
		t.Skip("set MELTEMI_GLFW_DESKTOP_TESTS=1 to run the real X11/Fcitx validation")
	}
	if os.Getenv("DISPLAY") == "" {
		t.Skip("DISPLAY is not set")
	}
	if _, err := exec.LookPath("fcitx5-remote"); err != nil {
		t.Skip("fcitx5-remote is unavailable")
	}
	if err := exec.Command("fcitx5-remote", "--check").Run(); err != nil {
		t.Skip("Fcitx 5 is not running")
	}
	inputMethod, err := exec.Command("fcitx5-remote", "-n").Output()
	if err != nil {
		t.Skipf("cannot read the current Fcitx input method: %v", err)
	}
	if strings.TrimSpace(string(inputMethod)) != "rime" {
		t.Skipf("current input method is %q, want rime for Chinese validation", strings.TrimSpace(string(inputMethod)))
	}

	stateOutput, err := exec.Command("fcitx5-remote").Output()
	if err != nil {
		t.Skipf("cannot read Fcitx state: %v", err)
	}
	state := strings.TrimSpace(string(stateOutput))
	defer func() {
		arg := "-c"
		if state == "2" {
			arg = "-o"
		}
		if output, err := exec.Command("fcitx5-remote", arg).CombinedOutput(); err != nil {
			t.Errorf("restore Fcitx state: %v: %s", err, output)
		}
	}()
	if err := exec.Command("fcitx5-remote", "-o").Run(); err != nil {
		t.Skip("cannot activate Fcitx")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := glfw3.Init(); err != nil {
		t.Skipf("GLFW cannot initialize X11: %v", err)
	}
	defer glfw3.Terminate()
	window, err := glfw3.CreateWindow(480, 120, "Meltemi Fcitx D-Bus validation", nil, nil)
	if err != nil {
		t.Skipf("cannot create the real X11 window: %v", err)
	}
	defer window.Destroy()
	window.Focus()

	client, err := NewClient(context.Background(), "meltemi-fcitx-validation", os.Getenv("DISPLAY"))
	if err != nil {
		if isFcitxUnavailable(err) {
			t.Skipf("Fcitx InputMethod1/InputContext1 is unavailable: %v", err)
		}
		t.Fatalf("NewClient: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	var committed []driver.Event
	client.SetEventHandler(func(event driver.Event) {
		committed = append(committed, event)
	})
	if err := client.SetFocus(true); err != nil {
		t.Fatalf("SetFocus(true): %v", err)
	}
	pumpDesktopEvents(client, 100*time.Millisecond)

	if err := client.SetSpot(24, 96, 2, 20); err != nil {
		t.Fatalf("SetSpot(24, 96, 2, 20): %v", err)
	}
	sendFcitxKey(t, client, glfw3.KeyN, 0x006e)
	sendFcitxKey(t, client, glfw3.KeyI, 0x0069)
	if !waitDesktop(client, func() bool {
		p := client.Preedit()
		return p.Active && p.Text != ""
	}) {
		t.Fatalf("no real preedit update; last state = %+v", client.Preedit())
	}
	t.Logf("real Fcitx preedit = %+v", client.Preedit())
	if err := client.SetSpot(160, 96, 2, 20); err != nil {
		t.Fatalf("SetSpot(160, 96, 2, 20): %v", err)
	}
	t.Log("real SetCursorRect calls succeeded at (24,96,2,20) and (160,96,2,20)")

	sendFcitxKey(t, client, glfw3.KeySpace, 0x0020)
	if !waitDesktop(client, func() bool { return len(committed) > 0 }) {
		t.Fatalf("no Chinese commit; preedit = %+v", client.Preedit())
	}
	pumpDesktopEvents(client, 200*time.Millisecond)
	if len(committed) != 1 || committed[0].Kind != driver.EventRune || !unicode.Is(unicode.Han, committed[0].Rune) {
		t.Fatalf("committed events = %#v, want exactly one Han EventRune", committed)
	}
	if got := client.Preedit(); got != (Preedit{}) {
		t.Fatalf("preedit after commit = %+v, want empty", got)
	}
	t.Logf("real Fcitx D-Bus commit = %q (one EventRune); native GLFW dedup is deferred to Task 2", committed[0].Rune)

	sendFcitxKey(t, client, glfw3.KeyN, 0x006e)
	sendFcitxKey(t, client, glfw3.KeyI, 0x0069)
	if !waitDesktop(client, func() bool { return client.Preedit().Active }) {
		t.Fatal("cannot start preedit for Escape cancellation")
	}
	sendFcitxKey(t, client, glfw3.KeyEscape, 0xff1b)
	if !waitDesktop(client, func() bool { return !client.Preedit().Active }) {
		t.Fatalf("Escape left preedit = %+v", client.Preedit())
	}
	pumpDesktopEvents(client, 100*time.Millisecond)
	if len(committed) != 1 {
		t.Fatalf("Escape committed extra text: %#v", committed)
	}
	t.Log("real Escape cancellation cleared preedit without a commit")

	sendFcitxKey(t, client, glfw3.KeyN, 0x006e)
	if !waitDesktop(client, func() bool { return client.Preedit().Active }) {
		t.Fatal("cannot start preedit for focus loss")
	}
	if err := client.SetFocus(false); err != nil {
		t.Fatalf("SetFocus(false): %v", err)
	}
	if got := client.Preedit(); got != (Preedit{}) {
		t.Fatalf("focus loss left preedit = %+v", got)
	}
	pumpDesktopEvents(client, 100*time.Millisecond)
	if len(committed) != 1 {
		t.Fatalf("FocusOut committed extra text: %#v", committed)
	}
	if err := client.SetFocus(true); err != nil {
		t.Fatalf("SetFocus(true) after focus loss: %v", err)
	}

	sendFcitxKey(t, client, glfw3.KeyN, 0x006e)
	if !waitDesktop(client, func() bool { return client.Preedit().Active }) {
		t.Fatal("cannot start preedit for Close")
	}
	inputContextPath := client.inputContext.Path()
	if err := client.Close(); err != nil {
		t.Fatalf("Close with active preedit: %v", err)
	}
	if got := client.Preedit(); got != (Preedit{}) {
		t.Fatalf("Close left preedit = %+v", got)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	pumpDesktopEvents(client, 100*time.Millisecond)
	if len(committed) != 1 {
		t.Fatalf("late D-Bus events after Close: %#v", committed)
	}

	probe, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatalf("connect probe session bus: %v", err)
	}
	defer probe.Close()
	var introspection string
	err = probe.Object("org.freedesktop.portal.Fcitx", inputContextPath).Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&introspection)
	if err == nil {
		t.Fatalf("input context %s still responds after DestroyIC/Close", inputContextPath)
	}
	var dbusErr dbus.Error
	if !errors.As(err, &dbusErr) || dbusErr.Name != "org.freedesktop.DBus.Error.UnknownObject" {
		t.Fatalf("input context probe error = %T %v, want org.freedesktop.DBus.Error.UnknownObject", err, err)
	}
	t.Logf("input context %s is gone after Close: %s", inputContextPath, dbusErr.Name)
}

func isFcitxUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, fragment := range []string{
		"org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.DBus.Error.UnknownMethod",
		"org.freedesktop.DBus.Error.UnknownInterface",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func sendFcitxKey(t *testing.T, client *Client, key glfw3.Key, keyval uint32) {
	t.Helper()
	keycode := glfw3.GetKeyScancode(key)
	if keycode < 0 {
		t.Fatalf("no X11 keycode for key %v", key)
	}
	for _, release := range []bool{false, true} {
		handled, err := client.ProcessKeyEvent(keyval, uint32(keycode), 0, release, uint32(time.Now().UnixMilli()))
		if err != nil {
			t.Fatalf("ProcessKeyEvent(key=%v, release=%t): %v", key, release, err)
		}
		if !release && !handled {
			t.Fatalf("Fcitx did not handle key %v during composition", key)
		}
		pumpDesktopEvents(client, 40*time.Millisecond)
	}
}

func waitDesktop(client *Client, condition func() bool) bool {
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		glfw3.PollEvents()
		client.Preedit()
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func pumpDesktopEvents(client *Client, duration time.Duration) {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		glfw3.PollEvents()
		client.Preedit()
		time.Sleep(5 * time.Millisecond)
	}
	glfw3.PollEvents()
	client.Preedit()
}
