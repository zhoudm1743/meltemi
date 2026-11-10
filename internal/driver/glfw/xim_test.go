package glfw

import (
	"errors"
	"testing"
	"time"

	glfw3 "github.com/go-gl/glfw/v3.3/glfw"

	"github.com/zhoudm1743/meltemi/internal/driver"
	"github.com/zhoudm1743/meltemi/internal/xim"
)

var _ driver.CompositionWindow = (*Window)(nil)

type fakeKeyCall struct {
	keyval    uint32
	keycode   uint32
	state     uint32
	isRelease bool
	time      uint32
}

type fakeXIMClient struct {
	preedit       xim.Preedit
	handled       []bool
	processErr    error
	focusErr      error
	spotErr       error
	closeErr      error
	focusOutDelay time.Duration
	onProcess     func(*fakeXIMClient)
	keyCalls      []fakeKeyCall
	focusCalls    []bool
	spotCalls     [][4]int
	closeCalls    int
	eventHandler  func(driver.Event)
	wake          func()
	asyncCommits  chan string
}

func (c *fakeXIMClient) SetEventHandler(handler func(driver.Event)) {
	c.eventHandler = handler
}

func (c *fakeXIMClient) SetWakeHandler(handler func()) {
	c.wake = handler
}

func (c *fakeXIMClient) Preedit() xim.Preedit {
	for {
		select {
		case text := <-c.asyncCommits:
			c.commit(text)
		default:
			return c.preedit
		}
	}
}

func (c *fakeXIMClient) SetFocus(focused bool) error {
	c.focusCalls = append(c.focusCalls, focused)
	if !focused {
		c.preedit = xim.Preedit{}
		time.Sleep(c.focusOutDelay)
	}
	return c.focusErr
}

func (c *fakeXIMClient) SetSpot(x, y, width, height int) error {
	c.spotCalls = append(c.spotCalls, [4]int{x, y, width, height})
	return c.spotErr
}

func (c *fakeXIMClient) ProcessKeyEvent(keyval, keycode, state uint32, isRelease bool, time uint32) (bool, error) {
	c.keyCalls = append(c.keyCalls, fakeKeyCall{
		keyval: keyval, keycode: keycode, state: state, isRelease: isRelease, time: time,
	})
	if c.onProcess != nil {
		c.onProcess(c)
	}
	if c.processErr != nil {
		return false, c.processErr
	}
	if len(c.handled) == 0 {
		return false, nil
	}
	handled := c.handled[0]
	c.handled = c.handled[1:]
	return handled, nil
}

func (c *fakeXIMClient) Close() error {
	c.closeCalls++
	c.preedit = xim.Preedit{}
	c.eventHandler = nil
	return c.closeErr
}

func (c *fakeXIMClient) commit(text string) {
	c.preedit = xim.Preedit{}
	for _, r := range text {
		if c.eventHandler != nil {
			c.eventHandler(driver.Event{Kind: driver.EventRune, Rune: r})
		}
	}
}

type ximTestFixture struct {
	backend    *Backend
	window     *Window
	client     *fakeXIMClient
	clients    []*fakeXIMClient
	terminated bool
}

func newXIMTestWindow(t *testing.T) *ximTestFixture {
	t.Helper()
	lockTestThread(t)
	t.Setenv("WIND_IME", "1")
	fixture := &ximTestFixture{backend: New()}
	fixture.backend.ximFactory = func() (ximClient, error) {
		client := &fakeXIMClient{asyncCommits: make(chan string, 1)}
		fixture.clients = append(fixture.clients, client)
		return client, nil
	}
	if err := fixture.backend.Init(); err != nil {
		t.Skipf("glfw init failed: %v", err)
	}
	t.Cleanup(func() {
		if fixture.terminated {
			return
		}
		if fixture.window != nil {
			fixture.window.Destroy()
		}
		fixture.backend.Terminate()
	})
	win, err := fixture.backend.NewWindow(driver.DefaultConfig())
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	fixture.window = win.(*Window)
	if len(fixture.clients) != 1 {
		t.Fatalf("clients created during Init/NewWindow = %d, want 1", len(fixture.clients))
	}
	fixture.client = fixture.clients[0]
	return fixture
}

func keyCallback(w *Window) glfw3.KeyCallback {
	callback := w.win.SetKeyCallback(nil)
	w.win.SetKeyCallback(callback)
	return callback
}

func charCallback(w *Window) glfw3.CharCallback {
	callback := w.win.SetCharCallback(nil)
	w.win.SetCharCallback(callback)
	return callback
}

func focusCallback(w *Window) glfw3.FocusCallback {
	callback := w.win.SetFocusCallback(nil)
	w.win.SetFocusCallback(callback)
	return callback
}

func posCallback(w *Window) glfw3.PosCallback {
	callback := w.win.SetPosCallback(nil)
	w.win.SetPosCallback(callback)
	return callback
}

func eventKinds(events []driver.Event) []driver.EventKind {
	kinds := make([]driver.EventKind, len(events))
	for i, event := range events {
		kinds[i] = event.Kind
	}
	return kinds
}

func requireEventKinds(t *testing.T, events []driver.Event, want ...driver.EventKind) {
	t.Helper()
	got := eventKinds(events)
	if len(got) != len(want) {
		t.Fatalf("event kinds = %v, want %v; events = %#v", got, want, events)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event kinds = %v, want %v", got, want)
		}
	}
}

func TestXIMAsyncCommitWakesWaitEvents(t *testing.T) {
	fixture := newXIMTestWindow(t)
	w := fixture.window
	focusCallback(w)(w.win, true)
	for i := 0; i < 5; i++ {
		fixture.backend.PollEvents()
		time.Sleep(10 * time.Millisecond)
	}
	var runes []rune
	w.SetHandler(func(event driver.Event) {
		if event.Kind == driver.EventRune {
			runes = append(runes, event.Rune)
		}
	})
	if fixture.client.wake == nil {
		t.Fatal("asynchronous D-Bus signal has no Backend.Wake handler")
	}

	// Phase 1: condition barrier. Wait until the producer goroutine has
	// enqueued the commit and returned from wake(), tolerating producer
	// scheduling delay instead of misreading it as a lost wake. The patched
	// GLFW self-pipe is level-triggered, so once wake() returns the wake is
	// posted and persists.
	wakePosted := make(chan struct{})
	go func() {
		fixture.client.asyncCommits <- "你"
		fixture.client.wake()
		close(wakePosted)
	}()
	watchdog := time.AfterFunc(1500*time.Millisecond, func() {
		glfw3.PostEmptyEvent()
	})
	defer watchdog.Stop()
	select {
	case <-wakePosted:
	case <-time.After(2 * time.Second):
		t.Fatal("producer goroutine did not enqueue the commit and return from wake() within 2s: producer scheduling stall, wake delivery never tested")
	}

	// Phase 2: the wake is provably posted, so a bounded WaitEvents/refresh
	// cycle must deliver the commit. The watchdog only unwinds a genuinely
	// hung WaitEvents; reaching the deadline here means a delivery failure.
	started := time.Now()
	for len(runes) == 0 && time.Since(started) < 500*time.Millisecond {
		fixture.backend.WaitEvents()
	}
	if len(runes) == 0 {
		t.Fatalf("wake() returned but the commit was not delivered within %v: asynchronous D-Bus signal did not wake WaitEvents", time.Since(started))
	}
	if len(runes) != 1 || runes[0] != '你' {
		t.Fatalf("asynchronous commit runes = %q, want exactly one 你", runes)
	}
}

func TestXIMFilteredKeyDoesNotReachGLFWTwice(t *testing.T) {
	fixture := newXIMTestWindow(t)
	w := fixture.window
	client := fixture.client
	var events []driver.Event
	w.SetHandler(func(event driver.Event) { events = append(events, event) })
	key := keyCallback(w)
	char := charCallback(w)

	nScanCode := glfw3.GetKeyScancode(glfw3.KeyN)
	if nScanCode < 0 {
		t.Skip("KeyN has no X11 scancode")
	}
	client.handled = []bool{true, false}
	key(w.win, glfw3.KeyN, nScanCode, glfw3.Press, glfw3.ModControl)
	requireEventKinds(t, events)
	char(w.win, 'n')
	requireEventKinds(t, events)

	if len(client.keyCalls) != 1 {
		t.Fatalf("ProcessKeyEvent calls = %d, want 1", len(client.keyCalls))
	}
	filtered := client.keyCalls[0]
	if filtered.keyval != 0x006e || filtered.keycode != uint32(nScanCode) || filtered.state != 4 || filtered.isRelease || filtered.time == 0 {
		t.Fatalf("filtered D-Bus key = %+v, want keyval 0x6e, keycode %d, Control state 4, press, nonzero time", filtered, nScanCode)
	}

	aScanCode := glfw3.GetKeyScancode(glfw3.KeyA)
	if aScanCode < 0 {
		t.Skip("KeyA has no X11 scancode")
	}
	key(w.win, glfw3.KeyA, aScanCode, glfw3.Press, 0)
	requireEventKinds(t, events, driver.EventKey)
	if events[0].Key != driver.KeyA || events[0].Action != driver.ActionPress || events[0].ScanCode != aScanCode {
		t.Fatalf("unfiltered EventKey = %+v, want KeyA press with scancode %d", events[0], aScanCode)
	}
	if len(client.keyCalls) != 2 || events[0].KeySym != client.keyCalls[1].keyval {
		t.Fatalf("EventKey KeySym = %#x, D-Bus calls = %+v, want the same original KeySym", events[0].KeySym, client.keyCalls)
	}
	char(w.win, 'a')
	requireEventKinds(t, events, driver.EventKey, driver.EventRune)
	if events[1].Rune != 'a' {
		t.Fatalf("unfiltered rune = %q, want 'a'", events[1].Rune)
	}
}

func TestXIMKeySymUsesFullXKBStateAndMatchesRelease(t *testing.T) {
	tests := []struct {
		name      string
		key       glfw3.Key
		mods      glfw3.ModifierKey
		wantState uint32
		wantSym   uint32
	}{
		{name: "Shift", key: glfw3.KeyN, mods: glfw3.ModShift, wantState: 1, wantSym: 0x004e},
		{name: "CapsLock", key: glfw3.KeyN, mods: glfw3.ModCapsLock, wantState: 2, wantSym: 0x004e},
		{name: "NumLock", key: glfw3.KeyKP1, mods: glfw3.ModNumLock, wantState: 16, wantSym: 0xffb1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newXIMTestWindow(t)
			w := fixture.window
			client := fixture.client
			client.handled = []bool{false, false}
			var events []driver.Event
			w.SetHandler(func(event driver.Event) { events = append(events, event) })
			scancode := glfw3.GetKeyScancode(test.key)
			if scancode < 0 {
				t.Skipf("%v has no X11 scancode", test.key)
			}
			key := keyCallback(w)
			key(w.win, test.key, scancode, glfw3.Press, test.mods)
			key(w.win, test.key, scancode, glfw3.Release, 0)

			requireEventKinds(t, events, driver.EventKey, driver.EventKey)
			if events[0].KeySym != test.wantSym {
				t.Fatalf("press KeySym = %#x, want %#x", events[0].KeySym, test.wantSym)
			}
			if events[1].Action != driver.ActionRelease || events[1].KeySym != events[0].KeySym {
				t.Fatalf("press/release KeySyms = %#x/%#x, action = %v; want identical symbols and release", events[0].KeySym, events[1].KeySym, events[1].Action)
			}
			if len(client.keyCalls) != 2 {
				t.Fatalf("ProcessKeyEvent calls = %d, want 2", len(client.keyCalls))
			}
			press, release := client.keyCalls[0], client.keyCalls[1]
			if press.keyval != test.wantSym || press.state != test.wantState || press.isRelease {
				t.Fatalf("press D-Bus key = %+v, want sym %#x state %d press", press, test.wantSym, test.wantState)
			}
			if release.keyval != press.keyval || !release.isRelease {
				t.Fatalf("release D-Bus key = %+v, want sym %#x and release", release, press.keyval)
			}
		})
	}
}

func TestXIMProcessErrorFallsBackToGLFW(t *testing.T) {
	fixture := newXIMTestWindow(t)
	w := fixture.window
	fixture.client.processErr = errors.New("D-Bus unavailable")
	var events []driver.Event
	w.SetHandler(func(event driver.Event) { events = append(events, event) })
	scancode := glfw3.GetKeyScancode(glfw3.KeyA)
	if scancode < 0 {
		t.Skip("KeyA has no X11 scancode")
	}
	keyCallback(w)(w.win, glfw3.KeyA, scancode, glfw3.Press, 0)
	requireEventKinds(t, events, driver.EventKey)
	charCallback(w)(w.win, 'a')
	requireEventKinds(t, events, driver.EventKey, driver.EventRune)
}

func TestCompositionCommitAndCancel(t *testing.T) {
	fixture := newXIMTestWindow(t)
	w := fixture.window
	client := fixture.client
	composition, ok := interface{}(w).(driver.CompositionWindow)
	if !ok {
		t.Fatalf("window %T does not implement driver.CompositionWindow", w)
	}
	var events []driver.Event
	w.SetHandler(func(event driver.Event) { events = append(events, event) })

	client.preedit = xim.Preedit{Text: "ni", Caret: 2, Active: true}
	text, caret, active := composition.Preedit()
	if text != "ni" || caret != 2 || !active {
		t.Fatalf("Preedit() = %q, %d, %v; want ni, 2, true", text, caret, active)
	}
	requireEventKinds(t, events, driver.EventCompositionStart)

	client.preedit = xim.Preedit{Text: "nih", Caret: 3, Active: true}
	composition.Preedit()
	requireEventKinds(t, events, driver.EventCompositionStart, driver.EventCompositionUpdate)
	composition.SetSpot(24, 96)
	if len(client.spotCalls) != 1 || client.spotCalls[0] != [4]int{24, 96, 1, 1} {
		t.Fatalf("SetSpot calls = %v, want [(24,96,1,1)]", client.spotCalls)
	}

	client.handled = []bool{true}
	client.onProcess = func(client *fakeXIMClient) { client.commit("你") }
	scancode := glfw3.GetKeyScancode(glfw3.KeySpace)
	if scancode < 0 {
		t.Skip("KeySpace has no X11 scancode")
	}
	keyCallback(w)(w.win, glfw3.KeySpace, scancode, glfw3.Press, 0)
	charCallback(w)(w.win, ' ')
	requireEventKinds(t, events,
		driver.EventCompositionStart,
		driver.EventCompositionUpdate,
		driver.EventCompositionEnd,
		driver.EventRune,
	)
	if events[3].Rune != '你' || events[3].Window != w {
		t.Fatalf("commit event = %+v, want one EventRune for 你 on this window", events[3])
	}
	if text, caret, active = composition.Preedit(); text != "" || caret != 0 || active {
		t.Fatalf("preedit after commit = %q, %d, %v; want empty", text, caret, active)
	}

	events = nil
	client.preedit = xim.Preedit{Text: "ni", Caret: 2, Active: true}
	composition.Preedit()
	client.commit("你好")
	requireEventKinds(t, events,
		driver.EventCompositionStart,
		driver.EventCompositionEnd,
		driver.EventRune,
		driver.EventRune,
	)
	if events[2].Rune != '你' || events[3].Rune != '好' {
		t.Fatalf("multi-rune commit = %q, %q; want 你, 好 exactly once each", events[2].Rune, events[3].Rune)
	}

	events = nil
	client.preedit = xim.Preedit{Text: "ma", Caret: 2, Active: true}
	composition.Preedit()
	client.preedit = xim.Preedit{}
	composition.Preedit()
	requireEventKinds(t, events, driver.EventCompositionStart, driver.EventCompositionEnd)
	if text, caret, active = composition.Preedit(); text != "" || caret != 0 || active {
		t.Fatalf("preedit after cancel = %q, %d, %v; want empty", text, caret, active)
	}
}

func TestCompositionWindowMoveResendsCachedSpotAndDestroyClearsCallback(t *testing.T) {
	fixture := newXIMTestWindow(t)
	w := fixture.window
	w.SetSpot(24, 96)
	callback := posCallback(w)
	if callback == nil {
		t.Fatal("window has no GLFW position callback")
	}

	callback(w.win, 200, 300)
	want := [4]int{24, 96, 1, 1}
	if len(fixture.client.spotCalls) != 2 || fixture.client.spotCalls[0] != want || fixture.client.spotCalls[1] != want {
		t.Fatalf("SetSpot calls after window move = %v, want two identical client rectangles %v", fixture.client.spotCalls, want)
	}

	w.Destroy()
	if previous := w.win.SetPosCallback(nil); previous != nil {
		t.Fatal("Destroy did not clear the GLFW position callback")
	}
}

func TestCompositionCloseDoesNotWaitForFocusOut(t *testing.T) {
	const (
		focusOutDelay = 600 * time.Millisecond
		closeLimit    = 250 * time.Millisecond
	)
	for _, test := range []struct {
		name string
		stop func(*ximTestFixture)
	}{
		{name: "Hide", stop: func(f *ximTestFixture) { f.window.Hide() }},
		{name: "Destroy", stop: func(f *ximTestFixture) { f.window.Destroy() }},
		{name: "Terminate", stop: func(f *ximTestFixture) {
			f.backend.Terminate()
			f.terminated = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newXIMTestWindow(t)
			fixture.client.focusOutDelay = focusOutDelay

			started := time.Now()
			test.stop(fixture)
			elapsed := time.Since(started)
			if fixture.client.closeCalls != 1 {
				t.Fatalf("Close calls = %d, want exactly 1", fixture.client.closeCalls)
			}
			if elapsed >= closeLimit {
				t.Fatalf("%s reached Close only after %v with blocking FocusOut; limit %v", test.name, elapsed, closeLimit)
			}
		})
	}
}

func TestCompositionLifecycleCleanup(t *testing.T) {
	tests := []struct {
		name    string
		stop    func(*ximTestFixture)
		restart func(*ximTestFixture)
	}{
		{
			name: "focus loss",
			stop: func(f *ximTestFixture) { focusCallback(f.window)(f.window.win, false) },
			restart: func(f *ximTestFixture) {
				focusCallback(f.window)(f.window.win, true)
			},
		},
		{
			name: "component disabled",
			stop: func(f *ximTestFixture) { f.window.SetCompositionEnabled(false) },
			restart: func(f *ximTestFixture) {
				f.window.SetCompositionEnabled(true)
			},
		},
		{
			name: "hide",
			stop: func(f *ximTestFixture) { f.window.Hide() },
			restart: func(f *ximTestFixture) {
				f.window.Show()
			},
		},
		{
			name: "destroy",
			stop: func(f *ximTestFixture) { f.window.Destroy() },
		},
		{
			name: "terminate",
			stop: func(f *ximTestFixture) {
				f.backend.Terminate()
				f.terminated = true
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newXIMTestWindow(t)
			w := fixture.window
			client := fixture.client
			focusCallback(w)(w.win, true)
			var events []driver.Event
			w.SetHandler(func(event driver.Event) { events = append(events, event) })
			client.preedit = xim.Preedit{Text: "ni", Caret: 2, Active: true}
			w.Preedit()
			requireEventKinds(t, events, driver.EventCompositionStart)

			test.stop(fixture)
			if client.closeCalls != 1 {
				t.Fatalf("Close calls = %d, want exactly 1", client.closeCalls)
			}
			for _, focused := range client.focusCalls {
				if !focused {
					t.Fatalf("focus calls = %v, want Close without a separate FocusOut", client.focusCalls)
				}
			}
			wantEvents := []driver.EventKind{driver.EventCompositionStart, driver.EventCompositionEnd}
			if test.name == "focus loss" {
				wantEvents = append(wantEvents, driver.EventFocus)
			}
			requireEventKinds(t, events, wantEvents...)
			if text, caret, active := w.Preedit(); text != "" || caret != 0 || active {
				t.Fatalf("preedit after %s = %q, %d, %v; want empty", test.name, text, caret, active)
			}
			if test.name == "destroy" && w.nativeDestroyCount != 0 {
				t.Fatal("Destroy synchronously destroyed the native window")
			}

			if test.restart != nil {
				test.restart(fixture)
				if len(fixture.clients) != 2 {
					t.Fatalf("clients after restart = %d, want a fresh client", len(fixture.clients))
				}
				restarted := fixture.clients[1]
				if restarted.closeCalls != 0 || len(restarted.focusCalls) != 1 || !restarted.focusCalls[0] {
					t.Fatalf("restarted client state: close=%d focus=%v, want open and focused", restarted.closeCalls, restarted.focusCalls)
				}
			}
		})
	}
}

func TestCompositionDestroyClearsCallbacksBeforeEnd(t *testing.T) {
	fixture := newXIMTestWindow(t)
	w := fixture.window
	fixture.client.preedit = xim.Preedit{Text: "ni", Caret: 2, Active: true}
	w.Preedit()
	endCalled := false
	endDestroyed := false
	endCallbackClears := 0
	endNativeDestroys := 0
	w.SetHandler(func(event driver.Event) {
		if event.Kind == driver.EventCompositionEnd {
			endCalled = true
			endDestroyed = w.destroyed
			endCallbackClears = w.callbackClears
			endNativeDestroys = w.nativeDestroyCount
		}
	})

	w.Destroy()
	if !endCalled {
		t.Fatal("Destroy did not emit CompositionEnd")
	}
	if !endDestroyed || endCallbackClears != 1 || endNativeDestroys != 0 {
		t.Fatalf("CompositionEnd observed destroyed=%v callbackClears=%d nativeDestroys=%d; want true/1/0", endDestroyed, endCallbackClears, endNativeDestroys)
	}
	if fixture.client.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", fixture.client.closeCalls)
	}
}

func TestCompositionHideShowReentryLeavesHiddenWithoutClient(t *testing.T) {
	fixture := newXIMTestWindow(t)
	w := fixture.window
	focusCallback(w)(w.win, true)
	fixture.client.preedit = xim.Preedit{Text: "ni", Caret: 2, Active: true}
	w.Preedit()
	reentered := false
	w.SetHandler(func(event driver.Event) {
		if event.Kind == driver.EventCompositionEnd && !reentered {
			reentered = true
			w.Show()
		}
	})

	w.Hide()
	if !reentered {
		t.Fatal("Hide did not emit CompositionEnd")
	}
	if len(fixture.clients) != 1 || w.xim != nil {
		t.Fatalf("CompositionEnd -> Show recreated a client during Hide: clients=%d live=%v", len(fixture.clients), w.xim != nil)
	}
	if !w.hidden {
		t.Fatal("CompositionEnd -> Show reentry cleared the final hidden state")
	}
	if fixture.client.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", fixture.client.closeCalls)
	}

	w.SetHandler(nil)
	w.Show()
	if w.hidden || len(fixture.clients) != 2 || w.xim != fixture.clients[1] {
		t.Fatalf("subsequent Show state: hidden=%v clients=%d, want visible with a fresh client", w.hidden, len(fixture.clients))
	}
	if focusCalls := fixture.clients[1].focusCalls; len(focusCalls) != 1 || !focusCalls[0] {
		t.Fatalf("subsequent Show focus calls = %v, want one FocusIn", focusCalls)
	}
}

func TestCompositionCleanupDoesNotRecreateClient(t *testing.T) {
	tests := []struct {
		name string
		stop func(*ximTestFixture)
	}{
		{name: "destroy", stop: func(f *ximTestFixture) { f.window.Destroy() }},
		{name: "terminate", stop: func(f *ximTestFixture) {
			f.backend.Terminate()
			f.terminated = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newXIMTestWindow(t)
			w := fixture.window
			focusCallback(w)(w.win, true)
			w.SetHandler(func(event driver.Event) {
				if event.Kind == driver.EventCompositionEnd {
					w.SetCompositionEnabled(true)
				}
			})
			fixture.client.preedit = xim.Preedit{Text: "ni", Caret: 2, Active: true}
			w.Preedit()
			test.stop(fixture)
			if len(fixture.clients) != 1 {
				t.Fatalf("clients after %s = %d, want no client recreated by CompositionEnd", test.name, len(fixture.clients))
			}
			if fixture.client.closeCalls != 1 {
				t.Fatalf("Close calls = %d, want 1", fixture.client.closeCalls)
			}
		})
	}
}

func requireReportedXIMError(t *testing.T, reported []error, want error) {
	t.Helper()
	for _, err := range reported {
		if errors.Is(err, want) {
			return
		}
	}
	t.Fatalf("reported XIM errors = %v, want an error wrapping %v", reported, want)
}

func TestXIMReportsFocusAndSpotErrors(t *testing.T) {
	t.Run("FocusIn", func(t *testing.T) {
		fixture := newXIMTestWindow(t)
		focusErr := errors.New("focus failed")
		fixture.client.focusErr = focusErr
		var reported []error
		fixture.backend.ximErrorHandler = func(err error) { reported = append(reported, err) }

		focusCallback(fixture.window)(fixture.window.win, true)
		requireReportedXIMError(t, reported, focusErr)
	})

	t.Run("SetSpot", func(t *testing.T) {
		fixture := newXIMTestWindow(t)
		spotErr := errors.New("spot failed")
		fixture.client.spotErr = spotErr
		var reported []error
		fixture.backend.ximErrorHandler = func(err error) { reported = append(reported, err) }

		fixture.window.SetSpot(24, 96)
		requireReportedXIMError(t, reported, spotErr)
	})
}

func TestXIMReportsClientRecreationFailure(t *testing.T) {
	fixture := newXIMTestWindow(t)
	focusCallback(fixture.window)(fixture.window.win, true)
	fixture.window.Hide()
	createErr := errors.New("recreate failed")
	fixture.backend.ximFactory = func() (ximClient, error) { return nil, createErr }
	var reported []error
	fixture.backend.ximErrorHandler = func(err error) { reported = append(reported, err) }

	fixture.window.Show()
	requireReportedXIMError(t, reported, createErr)
	if fixture.window.xim != nil {
		t.Fatal("failed client recreation left a live XIM client")
	}
}

func TestXIMReportsClientCloseErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(*ximTestFixture)
	}{
		{name: "Hide", stop: func(f *ximTestFixture) { f.window.Hide() }},
		{name: "Destroy", stop: func(f *ximTestFixture) { f.window.Destroy() }},
		{name: "Terminate", stop: func(f *ximTestFixture) {
			f.backend.Terminate()
			f.terminated = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newXIMTestWindow(t)
			closeErr := errors.New("close failed")
			fixture.client.closeErr = closeErr
			var reported []error
			fixture.backend.ximErrorHandler = func(err error) { reported = append(reported, err) }

			test.stop(fixture)
			requireReportedXIMError(t, reported, closeErr)
			if fixture.client.closeCalls != 1 {
				t.Fatalf("Close calls = %d, want 1", fixture.client.closeCalls)
			}
		})
	}
}

func TestXIMDisabledByDefaultUsesOriginalInputPath(t *testing.T) {
	lockTestThread(t)
	t.Setenv("WIND_IME", "")
	b := New()
	factoryCalls := 0
	b.ximFactory = func() (ximClient, error) {
		factoryCalls++
		return &fakeXIMClient{}, nil
	}
	if err := b.Init(); err != nil {
		t.Skipf("glfw init failed: %v", err)
	}
	defer b.Terminate()
	win, err := b.NewWindow(driver.DefaultConfig())
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	defer win.Destroy()
	w := win.(*Window)
	var events []driver.Event
	w.SetHandler(func(event driver.Event) { events = append(events, event) })
	scancode := glfw3.GetKeyScancode(glfw3.KeyA)
	if scancode < 0 {
		t.Skip("KeyA has no X11 scancode")
	}
	keyCallback(w)(w.win, glfw3.KeyA, scancode, glfw3.Press, 0)
	charCallback(w)(w.win, 'a')
	requireEventKinds(t, events, driver.EventKey, driver.EventRune)
	if factoryCalls != 0 {
		t.Fatalf("XIM factory calls with MELTEMI_IME/WIND_IME unset = %d, want 0", factoryCalls)
	}
}
