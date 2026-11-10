// Command meltemi-ime demonstrates real Fcitx composition through the public UI
// API. Preedit remains inside Input/TextArea until Fcitx commits it; separate
// labels mirror the committed bindings. --smoke is an opt-in X11/Fcitx 5/rime
// validation which injects real keys with xdotool and observes the public D-Bus
// protocol without importing Meltemi's internal driver or XIM packages.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"

	"github.com/zhoudm1743/meltemi/ui"
)

const (
	windowTitle     = "Meltemi IME · Fcitx 真实输入"
	defaultFontPath = "/usr/share/fonts/opentype/source-han-cjk/SourceHanSansSC-Regular.otf"

	fcitxService       = "org.freedesktop.portal.Fcitx"
	fcitxInputMethod   = "org.fcitx.Fcitx.InputMethod1"
	fcitxInputContext  = "org.fcitx.Fcitx.InputContext1"
	smokeStartDelay    = 500 * time.Millisecond
	smokeDeadline      = 45 * time.Second
	smokeStepTimeout   = 6 * time.Second
	smokeCommandTimout = 3 * time.Second
)

type demo struct {
	app *ui.App
	win *ui.Window

	inputText string
	areaText  string
	input     *ui.InputWidget
	area      *ui.TextAreaWidget
	inputLog  *ui.LabelWidget
	areaLog   *ui.LabelWidget
	status    *ui.LabelWidget

	mu        sync.Mutex
	firstErr  error
	runReturn bool
}

func newDemo(app *ui.App) *demo {
	return &demo{app: app}
}

func (d *demo) init() {
	d.input = ui.Input(&d.inputText).Placeholder("单行输入：预编辑显示在这里")
	d.area = ui.TextArea(&d.areaText).Placeholder("多行输入：预编辑只进入当前行")
	d.inputLog = ui.Label("Input 已提交：（空）")
	d.areaLog = ui.Label("TextArea 已提交：（空）")
	d.status = ui.Label("状态：等待 Fcitx 输入")
	d.win = d.app.Window(windowTitle).Size(760, 480).Content(
		ui.Column(
			ui.Label("Fcitx 5 / rime：点击编辑器，输入 ni，空格选择候选，Esc 取消"),
			ui.Label("下划线文本是预编辑；只有提交后的中文会进入下方结果标签。"),
			d.input,
			d.inputLog,
			d.area,
			d.areaLog,
			d.status,
		).Gap(12).Padding(24),
	)
	d.win.Show()
}

func committedText(value string) string {
	if value == "" {
		return "（空）"
	}
	return value
}

func (d *demo) refreshLabels() {
	d.inputLog.SetText("Input 已提交：" + committedText(d.inputText))
	d.areaLog.SetText("TextArea 已提交：" + committedText(d.areaText))
}

func (d *demo) refreshUntil(done <-chan struct{}) {
	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := d.app.Post(d.refreshLabels); err != nil {
				return
			}
		}
	}
}

func (d *demo) noteErr(err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	if d.firstErr == nil {
		d.firstErr = err
	}
	d.mu.Unlock()
}

func (d *demo) err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.firstErr
}

func (d *demo) markRunReturned() {
	d.mu.Lock()
	d.runReturn = true
	d.mu.Unlock()
}

func (d *demo) hasRunReturned() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runReturn
}

type editorValues struct {
	input        string
	area         string
	inputBinding string
	areaBinding  string
}

func (d *demo) values() (editorValues, error) {
	result := make(chan editorValues, 1)
	if err := d.app.Post(func() {
		result <- editorValues{
			input: d.input.Value(), area: d.area.Value(),
			inputBinding: d.inputText, areaBinding: d.areaText,
		}
	}); err != nil {
		return editorValues{}, err
	}
	select {
	case values := <-result:
		if values.input != values.inputBinding || values.area != values.areaBinding {
			return editorValues{}, fmt.Errorf("public Value and bindings differ: %q/%q versus %q/%q", values.input, values.area, values.inputBinding, values.areaBinding)
		}
		return values, nil
	case <-time.After(smokeCommandTimout):
		return editorValues{}, errors.New("timed out reading public editor values")
	}
}

func (d *demo) setStatus(value string) {
	_ = d.app.Post(func() { d.status.SetText("状态：" + value) })
}

type cursorRect struct {
	x      int32
	y      int32
	width  int32
	height int32
}

type preeditState struct {
	text   string
	caret  int32
	active bool
}

type contextKey struct {
	owner  string
	serial uint32
}

type busMonitor struct {
	conn     *dbus.Conn
	pidConn  *dbus.Conn
	messages chan *dbus.Message
	notify   chan struct{}
	done     chan struct{}
	loopDone chan struct{}

	mu          sync.Mutex
	pending     map[contextKey]struct{}
	created     []dbus.ObjectPath
	spots       map[dbus.ObjectPath][]cursorRect
	preedits    map[dbus.ObjectPath][]preeditState
	commitTexts map[dbus.ObjectPath][]string
	destroyed   map[dbus.ObjectPath]int
	parseErr    error
}

func startBusMonitor(pidConn *dbus.Conn) (*busMonitor, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect monitor session bus: %w", err)
	}
	rules := []string{
		"type='method_call',interface='" + fcitxInputMethod + "',member='CreateInputContext'",
		"type='method_call',interface='" + fcitxInputContext + "'",
		"type='method_return'",
		"type='error'",
		"type='signal',interface='" + fcitxInputContext + "'",
	}
	if err := conn.BusObject().Call("org.freedesktop.DBus.Monitoring.BecomeMonitor", 0, rules, uint32(0)).Err; err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("become D-Bus monitor: %w", err)
	}
	monitor := &busMonitor{
		conn:        conn,
		pidConn:     pidConn,
		messages:    make(chan *dbus.Message, 1024),
		notify:      make(chan struct{}, 1),
		done:        make(chan struct{}),
		loopDone:    make(chan struct{}),
		pending:     make(map[contextKey]struct{}),
		spots:       make(map[dbus.ObjectPath][]cursorRect),
		preedits:    make(map[dbus.ObjectPath][]preeditState),
		commitTexts: make(map[dbus.ObjectPath][]string),
		destroyed:   make(map[dbus.ObjectPath]int),
	}
	conn.Eavesdrop(monitor.messages)
	go monitor.loop()
	return monitor, nil
}

func headerString(message *dbus.Message, field dbus.HeaderField) string {
	if message == nil || message.Headers == nil {
		return ""
	}
	value, ok := message.Headers[field]
	if !ok {
		return ""
	}
	text, _ := value.Value().(string)
	return text
}

func messagePath(message *dbus.Message) dbus.ObjectPath {
	if message == nil || message.Headers == nil {
		return ""
	}
	value, ok := message.Headers[dbus.FieldPath]
	if !ok {
		return ""
	}
	path, _ := value.Value().(dbus.ObjectPath)
	return path
}

func messageMember(message *dbus.Message) string {
	return headerString(message, dbus.FieldMember)
}

func messageInterface(message *dbus.Message) string {
	return headerString(message, dbus.FieldInterface)
}

func (m *busMonitor) changed() {
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

func (m *busMonitor) fail(err error) {
	m.mu.Lock()
	if m.parseErr == nil {
		m.parseErr = err
	}
	m.mu.Unlock()
	m.changed()
}

func (m *busMonitor) loop() {
	defer close(m.loopDone)
	for {
		select {
		case <-m.done:
			return
		case message := <-m.messages:
			if message != nil {
				m.handle(message)
			}
		}
	}
}

func (m *busMonitor) handle(message *dbus.Message) {
	switch message.Type {
	case dbus.TypeMethodCall:
		m.handleCall(message)
	case dbus.TypeMethodReply, dbus.TypeError:
		m.handleReply(message)
	case dbus.TypeSignal:
		m.handleSignal(message)
	}
}

func (m *busMonitor) createdByCurrentProcess(message *dbus.Message) bool {
	sender := headerString(message, dbus.FieldSender)
	if sender == "" || m.pidConn == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), smokeCommandTimout)
	defer cancel()
	var pid uint32
	if err := m.pidConn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, sender).Store(&pid); err != nil {
		return false
	}
	return int64(pid) == int64(os.Getpid())
}

func (m *busMonitor) handleCall(message *dbus.Message) {
	if messageInterface(message) != fcitxInputContext {
		if messageInterface(message) == fcitxInputMethod && messageMember(message) == "CreateInputContext" && m.createdByCurrentProcess(message) {
			key := contextKey{owner: headerString(message, dbus.FieldSender), serial: message.Serial()}
			m.mu.Lock()
			m.pending[key] = struct{}{}
			m.mu.Unlock()
			m.changed()
		}
		return
	}
	path := messagePath(message)
	m.mu.Lock()
	known := m.knownLocked(path)
	m.mu.Unlock()
	if !known {
		return
	}
	switch messageMember(message) {
	case "SetCursorRect":
		var rect cursorRect
		if err := dbus.Store(message.Body, &rect.x, &rect.y, &rect.width, &rect.height); err != nil {
			m.fail(fmt.Errorf("decode SetCursorRect on %s: %w", path, err))
			return
		}
		m.mu.Lock()
		m.spots[path] = append(m.spots[path], rect)
		m.mu.Unlock()
		m.changed()
	case "DestroyIC":
		m.mu.Lock()
		m.destroyed[path]++
		m.mu.Unlock()
		m.changed()
	}
}

func (m *busMonitor) handleReply(message *dbus.Message) {
	key := contextKey{owner: headerString(message, dbus.FieldDestination), serial: 0}
	reply, ok := message.Headers[dbus.FieldReplySerial]
	if !ok {
		return
	}
	serial, ok := reply.Value().(uint32)
	if !ok {
		return
	}
	key.serial = serial
	m.mu.Lock()
	_, pending := m.pending[key]
	if pending {
		delete(m.pending, key)
	}
	m.mu.Unlock()
	if !pending {
		return
	}
	if message.Type == dbus.TypeError {
		m.fail(fmt.Errorf("CreateInputContext from %s failed: %s %v", key.owner, headerString(message, dbus.FieldErrorName), message.Body))
		return
	}
	var path dbus.ObjectPath
	var token []byte
	if err := dbus.Store(message.Body, &path, &token); err != nil || path == "" {
		m.fail(fmt.Errorf("decode CreateInputContext reply from %s: %v, body=%v", key.owner, err, message.Body))
		return
	}
	m.mu.Lock()
	m.created = append(m.created, path)
	m.mu.Unlock()
	m.changed()
}

func (m *busMonitor) handleSignal(message *dbus.Message) {
	if messageInterface(message) != fcitxInputContext {
		return
	}
	path := messagePath(message)
	m.mu.Lock()
	known := m.knownLocked(path)
	m.mu.Unlock()
	if !known {
		return
	}
	switch messageMember(message) {
	case "UpdateFormattedPreedit":
		var parts []struct {
			Text   string
			Format int32
		}
		var caret int32
		if err := dbus.Store(message.Body, &parts, &caret); err != nil {
			m.fail(fmt.Errorf("decode preedit on %s: %w", path, err))
			return
		}
		var text strings.Builder
		for _, part := range parts {
			text.WriteString(part.Text)
		}
		state := preeditState{text: text.String(), caret: caret, active: text.Len() != 0}
		m.mu.Lock()
		m.preedits[path] = append(m.preedits[path], state)
		m.mu.Unlock()
		m.changed()
	case "CommitString":
		var text string
		if err := dbus.Store(message.Body, &text); err != nil {
			m.fail(fmt.Errorf("decode commit on %s: %w", path, err))
			return
		}
		m.mu.Lock()
		m.commitTexts[path] = append(m.commitTexts[path], text)
		m.mu.Unlock()
		m.changed()
	}
}

func (m *busMonitor) knownLocked(path dbus.ObjectPath) bool {
	for _, candidate := range m.created {
		if candidate == path {
			return true
		}
	}
	return false
}

func (m *busMonitor) close() {
	select {
	case <-m.done:
		return
	default:
	}
	close(m.done)
	_ = m.conn.Close()
	<-m.loopDone
}

func (m *busMonitor) wait(timeout time.Duration, condition func() bool) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		m.mu.Lock()
		err := m.parseErr
		ok := err == nil && condition()
		m.mu.Unlock()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-m.notify:
		case <-deadline.C:
			return errors.New("timed out waiting for Fcitx D-Bus state")
		}
	}
}

func (m *busMonitor) createdCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.created)
}

func (m *busMonitor) context(index int) (dbus.ObjectPath, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.created) {
		return "", fmt.Errorf("input context index %d unavailable; created=%d", index, len(m.created))
	}
	return m.created[index], nil
}

func (m *busMonitor) lastPreedit(path dbus.ObjectPath) (preeditState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	history := m.preedits[path]
	if len(history) == 0 {
		return preeditState{}, false
	}
	return history[len(history)-1], true
}

func (m *busMonitor) committedStrings(path dbus.ObjectPath) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.commitTexts[path]...)
}

func commitDelta(baseline, current []string) ([]string, error) {
	if len(current) < len(baseline) {
		return nil, fmt.Errorf("commit history shrank from %d to %d", len(baseline), len(current))
	}
	for index, text := range baseline {
		if current[index] != text {
			return nil, fmt.Errorf("commit history prefix changed at %d: %q became %q", index, text, current[index])
		}
	}
	return append([]string(nil), current[len(baseline):]...), nil
}

func (m *busMonitor) distinctSpots(path dbus.ObjectPath) []cursorRect {
	m.mu.Lock()
	defer m.mu.Unlock()
	var unique []cursorRect
	for _, rect := range m.spots[path] {
		if len(unique) == 0 || unique[len(unique)-1] != rect {
			unique = append(unique, rect)
		}
	}
	return unique
}

func (m *busMonitor) cursorRects(path dbus.ObjectPath) []cursorRect {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]cursorRect(nil), m.spots[path]...)
}

type smokeRunner struct {
	demo    *demo
	monitor *busMonitor
	probe   *dbus.Conn
	window  string
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func command(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), smokeCommandTimout)
	defer cancel()
	return runCommand(ctx, name, args...)
}

func restoreFcitx(state string) error {
	arg := "-c"
	if state == "2" {
		arg = "-o"
	}
	_, err := command("fcitx5-remote", arg)
	return err
}

func checkDesktop() (string, error) {
	if os.Getenv("MELTEMI_GLFW_DESKTOP_TESTS") != "1" && os.Getenv("WIND_GLFW_DESKTOP_TESTS") != "1" {
		return "", errors.New("--smoke requires MELTEMI_GLFW_DESKTOP_TESTS=1 and injects real desktop input")
	}
	if os.Getenv("DISPLAY") == "" {
		return "", errors.New("--smoke requires an X11 DISPLAY")
	}
	for _, tool := range []string{"xdotool", "fcitx5-remote"} {
		if _, err := exec.LookPath(tool); err != nil {
			return "", fmt.Errorf("required desktop tool %s: %w", tool, err)
		}
	}
	if _, err := command("fcitx5-remote", "--check"); err != nil {
		return "", err
	}
	inputMethod, err := command("fcitx5-remote", "-n")
	if err != nil {
		return "", err
	}
	if inputMethod != "rime" {
		return "", fmt.Errorf("current Fcitx input method is %q, want rime; no input-method configuration was changed", inputMethod)
	}
	state, err := command("fcitx5-remote")
	if err != nil {
		return "", err
	}
	if _, err := command("fcitx5-remote", "-o"); err != nil {
		return "", fmt.Errorf("activate Fcitx temporarily: %w", err)
	}
	return state, nil
}

func (s *smokeRunner) findWindow() error {
	deadline := time.Now().Add(smokeStepTimeout)
	for time.Now().Before(deadline) {
		output, err := command("xdotool", "search", "--onlyvisible", "--name", windowTitle)
		if err == nil {
			for _, id := range strings.Fields(output) {
				pid, pidErr := command("xdotool", "getwindowpid", id)
				if pidErr == nil && pid == strconv.Itoa(os.Getpid()) {
					s.window = id
					return nil
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("cannot find this process's visible meltemi-ime X11 window")
}

func (s *smokeRunner) windowPosition() (int, int, error) {
	output, err := command("xdotool", "getwindowgeometry", "--shell", s.window)
	if err != nil {
		return 0, 0, err
	}
	var x, y int
	var haveX, haveY bool
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "X":
			x, err = strconv.Atoi(parts[1])
			haveX = err == nil
		case "Y":
			y, err = strconv.Atoi(parts[1])
			haveY = err == nil
		}
		if err != nil {
			return 0, 0, fmt.Errorf("parse xdotool window geometry %q: %w", line, err)
		}
	}
	if !haveX || !haveY {
		return 0, 0, fmt.Errorf("xdotool window geometry has no X/Y position: %q", output)
	}
	return x, y, nil
}

func (s *smokeRunner) key(keys ...string) error {
	if s.window == "" {
		return errors.New("X11 window is not selected")
	}
	args := []string{"key", "--clearmodifiers", "--delay", "90"}
	args = append(args, keys...)
	_, err := command("xdotool", args...)
	return err
}

func (s *smokeRunner) waitPreedit(path dbus.ObjectPath, count int, active bool) error {
	if err := s.monitor.wait(smokeStepTimeout, func() bool {
		history := s.monitor.preedits[path]
		return len(history) >= count && history[len(history)-1].active == active
	}); err != nil {
		state, _ := s.monitor.lastPreedit(path)
		return fmt.Errorf("preedit update %d on %s: %w; last=%+v", count, path, err, state)
	}
	return nil
}

func (s *smokeRunner) compose(path dbus.ObjectPath, startCount int) (int, error) {
	if err := s.key("n"); err != nil {
		return startCount, err
	}
	if err := s.waitPreedit(path, startCount+1, true); err != nil {
		return startCount, err
	}
	if err := s.key("i"); err != nil {
		return startCount, err
	}
	if err := s.waitPreedit(path, startCount+2, true); err != nil {
		return startCount, err
	}
	return startCount + 2, nil
}

func (s *smokeRunner) requireInput(want string) error {
	deadline := time.Now().Add(smokeStepTimeout)
	for time.Now().Before(deadline) {
		values, err := s.demo.values()
		if err == nil && values.input == want && values.area == "" {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	values, err := s.demo.values()
	if err != nil {
		return err
	}
	return fmt.Errorf("Input/TextArea committed values = %q/%q, want %q/empty", values.input, values.area, want)
}

func (s *smokeRunner) requireNoExtraCommit(path dbus.ObjectPath, wantValue string, wantCommits int) error {
	time.Sleep(250 * time.Millisecond)
	commitTexts := s.monitor.committedStrings(path)
	if len(commitTexts) != wantCommits {
		return fmt.Errorf("CommitString count = %d (%q), want %d", len(commitTexts), commitTexts, wantCommits)
	}
	return s.requireInput(wantValue)
}

func (s *smokeRunner) requireGone(path dbus.ObjectPath) error {
	deadline := time.Now().Add(smokeStepTimeout)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		var introspection string
		err := s.probe.Object(fcitxService, path).CallWithContext(ctx, "org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&introspection)
		cancel()
		if err != nil {
			var dbusErr dbus.Error
			if errors.As(err, &dbusErr) && dbusErr.Name == "org.freedesktop.DBus.Error.UnknownObject" {
				return nil
			}
			last = err
		} else {
			last = errors.New("input context still responds to Introspect")
		}
		time.Sleep(40 * time.Millisecond)
	}
	return fmt.Errorf("input context %s remains after DestroyIC: %v", path, last)
}

func (s *smokeRunner) run() error {
	if err := s.monitor.wait(smokeStepTimeout, func() bool { return len(s.monitor.created) >= 1 }); err != nil {
		return fmt.Errorf("initial CreateInputContext: %w", err)
	}
	if got := s.monitor.createdCount(); got != 1 {
		return fmt.Errorf("initial process-owned input contexts = %d, want exactly 1", got)
	}
	first, err := s.monitor.context(0)
	if err != nil {
		return err
	}
	if err := s.findWindow(); err != nil {
		return err
	}
	if _, err := command("xdotool", "windowactivate", "--sync", s.window); err != nil {
		return err
	}
	if err := s.key("Tab"); err != nil {
		return fmt.Errorf("focus public Input through native Tab: %w", err)
	}

	preeditCount, err := s.compose(first, 0)
	if err != nil {
		return fmt.Errorf("composition start/update: %w", err)
	}
	firstPreedit, _ := s.monitor.lastPreedit(first)
	fmt.Printf("smoke: real preedit signal/update path on %s: %d updates, latest=%q caret=%d\n", first, preeditCount, firstPreedit.text, firstPreedit.caret)
	s.demo.setStatus("预编辑已更新")

	if err := s.key("space"); err != nil {
		return err
	}
	if err := s.monitor.wait(smokeStepTimeout, func() bool { return len(s.monitor.commitTexts[first]) == 1 }); err != nil {
		return fmt.Errorf("select first rime candidate: %w", err)
	}
	commitTexts := s.monitor.committedStrings(first)
	committed := commitTexts[0]
	if utf8.RuneCountInString(committed) != 1 || !unicode.Is(unicode.Han, []rune(committed)[0]) {
		return fmt.Errorf("CommitString = %q, want exactly one Han rune", committed)
	}
	if err := s.requireInput(committed); err != nil {
		return err
	}
	if err := s.waitPreedit(first, preeditCount+1, false); err != nil {
		return fmt.Errorf("composition end after commit: %w", err)
	}
	if err := s.requireNoExtraCommit(first, committed, 1); err != nil {
		return fmt.Errorf("commit deduplication: %w", err)
	}
	fmt.Printf("smoke: committed %q exactly once; Input Value and binding match; preedit empty\n", committed)
	s.demo.setStatus("中文已提交一次，无重复、无组合残留")

	preeditCount, err = s.compose(first, preeditCount+1)
	if err != nil {
		return fmt.Errorf("second composition: %w", err)
	}
	spots := s.monitor.distinctSpots(first)
	if len(spots) < 2 || spots[0] == spots[len(spots)-1] {
		return fmt.Errorf("candidate cursor rectangles did not move after committed caret advanced: %v", spots)
	}
	fmt.Printf("smoke: real SetCursorRect moved from (%d,%d) to (%d,%d) after the committed caret advanced\n", spots[0].x, spots[0].y, spots[len(spots)-1].x, spots[len(spots)-1].y)
	s.demo.setStatus("候选坐标已随已提交 caret 移动")

	time.Sleep(250 * time.Millisecond)
	beforeMove := s.monitor.cursorRects(first)
	if len(beforeMove) == 0 {
		return errors.New("window move has no baseline SetCursorRect")
	}
	oldX, oldY, err := s.windowPosition()
	if err != nil {
		return err
	}
	wantX, wantY := oldX+40, oldY+30
	if _, err := command("xdotool", "windowmove", s.window, strconv.Itoa(wantX), strconv.Itoa(wantY)); err != nil {
		return fmt.Errorf("real xdotool windowmove: %w", err)
	}
	if err := s.monitor.wait(smokeStepTimeout, func() bool {
		return len(s.monitor.spots[first]) > len(beforeMove)
	}); err != nil {
		return fmt.Errorf("window move did not resend SetCursorRect: %w", err)
	}
	afterMove := s.monitor.cursorRects(first)
	wantRect := beforeMove[len(beforeMove)-1]
	if got := afterMove[len(afterMove)-1]; got != wantRect {
		return fmt.Errorf("window move changed the client rectangle from %+v to %+v", wantRect, got)
	}
	gotX, gotY, err := s.windowPosition()
	if err != nil {
		return err
	}
	if gotX != wantX || gotY != wantY {
		return fmt.Errorf("xdotool windowmove position = (%d,%d), want (%d,%d)", gotX, gotY, wantX, wantY)
	}
	fmt.Printf("smoke: real xdotool windowmove (%d,%d)->(%d,%d) resent the same client SetCursorRect (%d,%d,%d,%d)\n", oldX, oldY, gotX, gotY, wantRect.x, wantRect.y, wantRect.width, wantRect.height)
	s.demo.setStatus("窗口移动已同步候选坐标")

	if err := s.key("Escape"); err != nil {
		return err
	}
	if err := s.waitPreedit(first, preeditCount+1, false); err != nil {
		return fmt.Errorf("Escape cancellation: %w", err)
	}
	if err := s.requireNoExtraCommit(first, committed, 1); err != nil {
		return fmt.Errorf("Escape committed or duplicated text: %w", err)
	}
	fmt.Println("smoke: second composition cancelled by Escape without commit or residual preedit")

	preeditCount, err = s.compose(first, preeditCount+1)
	if err != nil {
		return fmt.Errorf("composition before Hide: %w", err)
	}
	hideBaseline := s.monitor.committedStrings(first)
	if len(hideBaseline) != 1 || hideBaseline[0] != committed {
		return fmt.Errorf("Hide commit baseline = %q, want exactly %q", hideBaseline, committed)
	}
	s.demo.win.Hide()
	if err := s.monitor.wait(smokeStepTimeout, func() bool { return s.monitor.destroyed[first] >= 1 }); err != nil {
		return fmt.Errorf("Hide DestroyIC: %w", err)
	}
	if err := s.requireGone(first); err != nil {
		return fmt.Errorf("Hide resource cleanup: %w", err)
	}
	time.Sleep(250 * time.Millisecond)
	hideDelta, err := commitDelta(hideBaseline, s.monitor.committedStrings(first))
	if err != nil {
		return fmt.Errorf("Hide commit delta: %w", err)
	}
	for _, text := range hideDelta {
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				return fmt.Errorf("Hide cleanup produced extra Han commit %q in stage delta %q", text, hideDelta)
			}
		}
	}
	if err := s.requireInput(committed); err != nil {
		return fmt.Errorf("Hide let a late cleanup commit reach Input: %w", err)
	}
	fmt.Printf("smoke: Hide cancelled active composition; %s is UnknownObject; late commit delta=%q contains no Han; Input remains %q\n", first, hideDelta, committed)

	s.demo.win.Show()
	if err := s.monitor.wait(smokeStepTimeout, func() bool { return len(s.monitor.created) >= 2 }); err != nil {
		return fmt.Errorf("Show fresh CreateInputContext: %w", err)
	}
	if got := s.monitor.createdCount(); got != 2 {
		return fmt.Errorf("process-owned input contexts after Show = %d, want exactly 2", got)
	}
	second, err := s.monitor.context(1)
	if err != nil {
		return err
	}
	if second == first {
		return fmt.Errorf("Show reused destroyed input context %s", second)
	}
	if _, err := command("xdotool", "windowactivate", "--sync", s.window); err != nil {
		return err
	}
	preeditCount, err = s.compose(second, 0)
	if err != nil {
		return fmt.Errorf("composition before Close: %w", err)
	}
	closeBaseline := s.monitor.committedStrings(second)
	s.demo.setStatus("隐藏已释放旧输入上下文；关闭正在验证新上下文释放")
	if err := s.demo.win.Close(); err != nil {
		return err
	}
	if err := s.monitor.wait(smokeStepTimeout, func() bool { return s.monitor.destroyed[second] >= 1 }); err != nil {
		return fmt.Errorf("Close DestroyIC: %w", err)
	}
	if err := s.requireGone(second); err != nil {
		return fmt.Errorf("Close resource cleanup: %w", err)
	}
	time.Sleep(250 * time.Millisecond)
	closeDelta, err := commitDelta(closeBaseline, s.monitor.committedStrings(second))
	if err != nil {
		return fmt.Errorf("Close commit delta: %w", err)
	}
	for _, text := range closeDelta {
		for _, r := range text {
			if unicode.Is(unicode.Han, r) {
				return fmt.Errorf("Close produced an extra Han commit %q in stage delta %q", text, closeDelta)
			}
		}
	}
	if got := s.monitor.createdCount(); got != 2 {
		return fmt.Errorf("process-owned input contexts at exit = %d, want exactly 2", got)
	}
	fmt.Printf("smoke: Close destroyed the window; %s is UnknownObject; late commit delta=%q contains no Han\n", second, closeDelta)
	fmt.Println("SMOKE PASS: preedit signal/update path, caret and window-move candidate spot synchronization, one commit, retype/cancel, Hide and Close cleanup")
	return nil
}

func run() (result error) {
	smoke := flag.Bool("smoke", false, "opt-in real X11/Fcitx 5/rime input and resource-lifecycle validation")
	font := flag.String("font", defaultFontPath, "TTF/OTF font registered for Chinese text")
	flag.Parse()

	fontData, err := os.ReadFile(*font)
	if err != nil {
		return fmt.Errorf("read Chinese font %s: %w", *font, err)
	}
	app := ui.New()
	if err := app.RegisterFont(ui.DefaultFont, fontData); err != nil {
		return err
	}
	if err := app.EnableIME(); err != nil {
		return err
	}
	d := newDemo(app)
	if err := app.Post(d.init); err != nil {
		return err
	}

	var monitor *busMonitor
	var probe *dbus.Conn
	var fcitxState string
	if *smoke {
		fcitxState, err = checkDesktop()
		if err != nil {
			return err
		}
		defer func() {
			if err := restoreFcitx(fcitxState); err != nil {
				result = errors.Join(result, fmt.Errorf("restore Fcitx activation state: %w", err))
			}
		}()
		probe, err = dbus.ConnectSessionBus()
		if err != nil {
			return fmt.Errorf("connect input-context probe: %w", err)
		}
		defer probe.Close()
		monitor, err = startBusMonitor(probe)
		if err != nil {
			return err
		}
		defer monitor.close()
	}

	refreshDone := make(chan struct{})
	go d.refreshUntil(refreshDone)
	defer close(refreshDone)

	var smokeDone chan error
	var watchdog *time.Timer
	if *smoke {
		// The smoke deliberately hides and re-shows the only window; the default
		// last-visible-window policy would end Run mid-sequence. Keep Running and
		// quit explicitly when the smoke finishes. Normal demo runs keep the
		// default policy so closing the last window exits.
		app.SetLastWindowAction(ui.LastWindowKeepRunning)
		smokeDone = make(chan error, 1)
		runner := &smokeRunner{demo: d, monitor: monitor, probe: probe}
		time.AfterFunc(smokeStartDelay, func() {
			err := runner.run()
			if err != nil {
				d.noteErr(err)
			}
			app.Quit()
			smokeDone <- err
		})
		watchdog = time.AfterFunc(smokeDeadline, func() {
			if !d.hasRunReturned() {
				d.noteErr(errors.New("meltemi-ime: smoke deadline exceeded"))
				app.Quit()
			}
		})
	}

	runErr := app.Run()
	d.markRunReturned()
	if watchdog != nil {
		watchdog.Stop()
	}
	if *smoke {
		select {
		case err := <-smokeDone:
			d.noteErr(err)
		case <-time.After(smokeStepTimeout):
			d.noteErr(errors.New("meltemi-ime: Run returned before smoke validation completed"))
		}
	}
	if err := d.err(); err != nil {
		if runErr != nil {
			return errors.Join(err, fmt.Errorf("additional Run error: %w", runErr))
		}
		return err
	}
	return runErr
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "meltemi-ime:", err)
		os.Exit(1)
	}
}
