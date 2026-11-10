//go:build linux

package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalDestination      = "org.freedesktop.portal.Desktop"
	portalDesktopPath      = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	fileChooserInterface   = "org.freedesktop.portal.FileChooser"
	portalRequestInterface = "org.freedesktop.portal.Request"
	portalResponseMember   = "Response"
	dialogOperationTimeout = 2 * time.Second
)

var dialogTokenSequence atomic.Uint64

// dialogWireFilterPattern and dialogWireFilter encode the FileChooser
// filters option, wire type a(sa(us)).
type dialogWireFilterPattern struct {
	Type    uint32
	Pattern string
}

type dialogWireFilter struct {
	Name     string
	Patterns []dialogWireFilterPattern
}

type linuxDialogService struct {
	opMu sync.Mutex
	mu   sync.Mutex

	conn      dbusConnection
	connect   func(context.Context) (dbusConnection, error)
	closed    bool
	matchSet  bool
	unique    string
	owner     string
	channel   chan *dbus.Signal
	watchStop chan struct{}

	requests map[dbus.ObjectPath]*dialogRequest
}

func newPlatformDialogService() DialogService {
	return &linuxDialogService{connect: connectSessionBus}
}

func newDialogServiceWithConn(conn dbusConnection) DialogService {
	return &linuxDialogService{conn: conn, requests: make(map[dbus.ObjectPath]*dialogRequest)}
}

func nextDialogToken() string {
	return strconv.FormatUint(dialogTokenSequence.Add(1), 10)
}

// dialogRequestPath predicts the portal request object path for a sender
// unique name and handle token: the sender drops its leading ':' and has dots
// replaced so the result is a valid object path element.
func dialogRequestPath(sender, token string) dbus.ObjectPath {
	sender = strings.TrimPrefix(sender, ":")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + strings.ReplaceAll(sender, ".", "_") + "/" + token)
}

func (s *linuxDialogService) Capabilities() Capabilities {
	if s == nil {
		return Capabilities{}
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return Capabilities{}
	}
	if _, err := s.connectionLocked(); err != nil {
		return Capabilities{}
	}
	if _, err := s.portalOwnerLocked(); err != nil {
		return Capabilities{}
	}
	if err := s.fileChooserVersionLocked(); err != nil {
		return Capabilities{}
	}
	return Capabilities{FileDialog: true}
}

func (s *linuxDialogService) Open(options DialogOptions, callback func(DialogResult, error)) (DialogRequest, error) {
	return s.start(fileChooserInterface+".OpenFile", options, true, callback)
}

func (s *linuxDialogService) Save(options DialogOptions, callback func(DialogResult, error)) (DialogRequest, error) {
	return s.start(fileChooserInterface+".SaveFile", options, false, callback)
}

func (s *linuxDialogService) start(method string, options DialogOptions, open bool, callback func(DialogResult, error)) (DialogRequest, error) {
	if callback == nil {
		return nil, errDialogNilCallback
	}
	if s == nil {
		return nil, errDialogClosed
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return nil, errDialogClosed
	}
	conn, err := s.connectionLocked()
	if err != nil {
		return nil, err
	}
	owner, err := s.portalOwnerLocked()
	if err != nil {
		return nil, err
	}
	wire, err := buildDialogOptions(options, open)
	if err != nil {
		return nil, err
	}
	token := nextDialogToken()
	path := dialogRequestPath(s.unique, token)
	wire["handle_token"] = dbus.MakeVariant(token)
	request := &dialogRequest{service: s, path: path, callback: callback}
	s.registerRequestLocked(request)

	callCtx, cancel := context.WithTimeout(context.Background(), dialogOperationTimeout)
	call := conn.Object(portalDestination, portalDesktopPath).CallWithContext(callCtx, method, 0, options.ParentWindow, options.Title, wire)
	cancel()
	if call.Err != nil {
		s.takeRequestLocked(path)
		return nil, fmt.Errorf("desktop: portal %s: %w", method, call.Err)
	}
	if len(call.Body) != 1 {
		s.takeRequestLocked(path)
		return nil, fmt.Errorf("desktop: portal %s returned %d values, want 1 handle", method, len(call.Body))
	}
	handle, ok := call.Body[0].(dbus.ObjectPath)
	if !ok || handle == "" {
		s.takeRequestLocked(path)
		return nil, fmt.Errorf("desktop: portal %s returned an invalid handle %#v", method, call.Body[0])
	}
	if handle != path {
		s.takeRequestLocked(path)
		return nil, fmt.Errorf("desktop: portal %s returned handle %s, want the predicted request path %s", method, handle, path)
	}
	if owner == "" {
		// unreachable: portalOwnerLocked already rejects empty owners
		s.takeRequestLocked(path)
		return nil, ErrDialogUnavailable
	}
	return request, nil
}

func dialogWireFilterFor(filter FileFilter) (dialogWireFilter, error) {
	patterns := make([]dialogWireFilterPattern, len(filter.Patterns))
	for j, pattern := range filter.Patterns {
		switch pattern.Kind {
		case FileFilterPatternGlob:
			patterns[j] = dialogWireFilterPattern{Type: 0, Pattern: pattern.Pattern}
		case FileFilterPatternMIMEType:
			patterns[j] = dialogWireFilterPattern{Type: 1, Pattern: pattern.Pattern}
		default:
			return dialogWireFilter{}, fmt.Errorf("%w: %d", errDialogFilterPattern, pattern.Kind)
		}
	}
	return dialogWireFilter{Name: filter.Name, Patterns: patterns}, nil
}

func buildDialogOptions(options DialogOptions, open bool) (map[string]dbus.Variant, error) {
	wire := make(map[string]dbus.Variant)
	if options.AcceptLabel != "" {
		wire["accept_label"] = dbus.MakeVariant(options.AcceptLabel)
	}
	if options.Modal {
		wire["modal"] = dbus.MakeVariant(true)
	}
	if open {
		if options.Multiple {
			wire["multiple"] = dbus.MakeVariant(true)
		}
		if options.Directory {
			wire["directory"] = dbus.MakeVariant(true)
		}
	}
	if len(options.Filters) > 0 {
		filters := make([]dialogWireFilter, len(options.Filters))
		for i, filter := range options.Filters {
			wireFilter, err := dialogWireFilterFor(filter)
			if err != nil {
				return nil, err
			}
			filters[i] = wireFilter
		}
		wire["filters"] = dbus.MakeVariant(filters)
	}
	if options.CurrentFilter != nil {
		wireFilter, err := dialogWireFilterFor(*options.CurrentFilter)
		if err != nil {
			return nil, err
		}
		wire["current_filter"] = dbus.MakeVariant(wireFilter)
	}
	if options.CurrentFolder != "" {
		if !filepath.IsAbs(options.CurrentFolder) {
			return nil, fmt.Errorf("%w: %q", errDialogFolderNotAbs, options.CurrentFolder)
		}
		wire["current_folder"] = dbus.MakeVariant(append([]byte(options.CurrentFolder), 0))
	}
	if !open {
		if options.CurrentName != "" {
			wire["current_name"] = dbus.MakeVariant(options.CurrentName)
		}
		if options.CurrentFile != "" {
			if !filepath.IsAbs(options.CurrentFile) {
				return nil, fmt.Errorf("%w: %q", errDialogFileNotAbs, options.CurrentFile)
			}
			wire["current_file"] = dbus.MakeVariant(append([]byte(options.CurrentFile), 0))
		}
	}
	return wire, nil
}

func (s *linuxDialogService) connectionLocked() (dbusConnection, error) {
	if s.closed {
		return nil, errDialogClosed
	}
	if s.matchSet {
		return s.conn, nil
	}
	conn := s.conn
	if conn == nil {
		if s.connect == nil {
			return nil, fmt.Errorf("%w: no session bus connector", ErrDialogUnavailable)
		}
		ctx, cancel := context.WithTimeout(context.Background(), dialogOperationTimeout)
		defer cancel()
		dialed, err := s.connect(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: connect session bus: %v", ErrDialogUnavailable, err)
		}
		if dialed == nil {
			return nil, fmt.Errorf("%w: connect session bus returned nil", ErrDialogUnavailable)
		}
		conn = dialed
	}
	names := conn.Names()
	if len(names) == 0 || names[0] == "" {
		return nil, fmt.Errorf("%w: session bus returned an empty unique name", ErrDialogUnavailable)
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialogOperationTimeout)
	defer cancel()
	if err := conn.AddMatchSignalContext(ctx, dialogResponseMatchOptions()...); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: subscribe to portal responses: %v", ErrDialogUnavailable, err)
	}
	if err := conn.AddMatchSignalContext(ctx, dialogOwnerMatchOptions()...); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: subscribe to portal owner changes: %v", ErrDialogUnavailable, err)
	}
	channel := make(chan *dbus.Signal, 16)
	conn.Signal(channel)
	s.conn = conn
	s.unique = names[0]
	s.owner = ""
	s.channel = channel
	s.watchStop = make(chan struct{})
	s.requests = make(map[dbus.ObjectPath]*dialogRequest)
	s.matchSet = true
	go s.watchSignals(channel, s.watchStop)
	return conn, nil
}

func dialogResponseMatchOptions() []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchInterface(portalRequestInterface),
		dbus.WithMatchMember(portalResponseMember),
	}
}

func dialogOwnerMatchOptions() []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, portalDestination),
	}
}

func (s *linuxDialogService) portalOwnerLocked() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialogOperationTimeout)
	defer cancel()
	call := s.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, portalDestination)
	if call.Err != nil {
		return "", fmt.Errorf("%w: portal owner: %v", ErrDialogUnavailable, call.Err)
	}
	if len(call.Body) != 1 {
		return "", fmt.Errorf("%w: portal owner reply has %d values", ErrDialogUnavailable, len(call.Body))
	}
	owner, ok := call.Body[0].(string)
	if !ok || owner == "" {
		return "", fmt.Errorf("%w: portal owner reply is %#v", ErrDialogUnavailable, call.Body[0])
	}
	s.mu.Lock()
	s.owner = owner
	s.mu.Unlock()
	return owner, nil
}

func (s *linuxDialogService) fileChooserVersionLocked() error {
	ctx, cancel := context.WithTimeout(context.Background(), dialogOperationTimeout)
	defer cancel()
	call := s.conn.Object(portalDestination, portalDesktopPath).CallWithContext(
		ctx, dbusPropertiesInterface+".Get", 0, fileChooserInterface, "version")
	if call.Err != nil {
		return fmt.Errorf("%w: portal FileChooser version: %v", ErrDialogUnavailable, call.Err)
	}
	if len(call.Body) != 1 {
		return fmt.Errorf("%w: portal FileChooser version reply has %d values", ErrDialogUnavailable, len(call.Body))
	}
	variant, ok := call.Body[0].(dbus.Variant)
	if !ok {
		return fmt.Errorf("%w: portal FileChooser version reply is %#v", ErrDialogUnavailable, call.Body[0])
	}
	if _, ok := variant.Value().(uint32); !ok {
		return fmt.Errorf("%w: portal FileChooser version is %T", ErrDialogUnavailable, variant.Value())
	}
	return nil
}

func (s *linuxDialogService) registerRequestLocked(request *dialogRequest) {
	s.mu.Lock()
	s.requests[request.path] = request
	s.mu.Unlock()
}

// takeRequestLocked removes the request for exactly one winner; the winner is
// responsible for delivering the single allowed callback.
func (s *linuxDialogService) takeRequestLocked(path dbus.ObjectPath) *dialogRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := s.requests[path]
	if request != nil {
		delete(s.requests, path)
	}
	return request
}

func (s *linuxDialogService) takeAllRequestsLocked() []*dialogRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]*dialogRequest, 0, len(s.requests))
	for path, request := range s.requests {
		requests = append(requests, request)
		delete(s.requests, path)
	}
	return requests
}

func (s *linuxDialogService) currentOwner() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.owner
}

func (s *linuxDialogService) watchSignals(channel <-chan *dbus.Signal, stop <-chan struct{}) {
	for {
		select {
		case signal, ok := <-channel:
			if !ok {
				return
			}
			if signal == nil {
				continue
			}
			switch signal.Name {
			case portalRequestInterface + "." + portalResponseMember:
				s.handleResponse(signal)
			case "org.freedesktop.DBus.NameOwnerChanged":
				s.handleOwnerChanged(signal)
			}
		case <-stop:
			return
		}
	}
}

func (s *linuxDialogService) handleResponse(signal *dbus.Signal) {
	if len(signal.Body) < 1 {
		return
	}
	if sender := signal.Sender; sender == "" || sender != s.currentOwner() {
		return
	}
	request := s.takeRequestLocked(signal.Path)
	if request == nil {
		return
	}
	code, ok := dialogResponseCode(signal.Body[0])
	if !ok {
		request.deliver(DialogResult{}, fmt.Errorf("desktop: malformed portal response code %#v", signal.Body[0]))
		return
	}
	switch code {
	case 0:
		var results map[string]dbus.Variant
		if len(signal.Body) >= 2 {
			results, _ = signal.Body[1].(map[string]dbus.Variant)
		}
		result, err := parseDialogURIs(results)
		if err != nil {
			request.deliver(DialogResult{}, err)
			return
		}
		request.deliver(result, nil)
	case 1:
		request.deliver(DialogResult{}, ErrDialogCanceled)
	default:
		request.deliver(DialogResult{}, fmt.Errorf("desktop: portal dialog failed with response code %d", code))
	}
}

func dialogResponseCode(value interface{}) (uint32, bool) {
	switch code := value.(type) {
	case uint32:
		return code, true
	case int32:
		if code >= 0 {
			return uint32(code), true
		}
	}
	return 0, false
}

func (s *linuxDialogService) handleOwnerChanged(signal *dbus.Signal) {
	if len(signal.Body) != 3 {
		return
	}
	if name, ok := signal.Body[0].(string); !ok || name != portalDestination {
		return
	}
	owner, ok := signal.Body[2].(string)
	if !ok {
		return
	}
	s.mu.Lock()
	s.owner = owner
	s.mu.Unlock()
	// Any ownership change orphans in-flight request handles; fail them once.
	pending := s.takeAllRequestsLocked()
	for _, request := range pending {
		request.deliver(DialogResult{}, fmt.Errorf("%w: portal disappeared", ErrDialogUnavailable))
	}
}

func parseDialogURIs(results map[string]dbus.Variant) (DialogResult, error) {
	variant, ok := results["uris"]
	if !ok {
		return DialogResult{}, errors.New("desktop: portal response lacks uris")
	}
	uris, ok := variant.Value().([]string)
	if !ok {
		return DialogResult{}, fmt.Errorf("desktop: portal uris = %T, want []string", variant.Value())
	}
	if len(uris) == 0 {
		return DialogResult{}, errors.New("desktop: portal response has no uris")
	}
	paths := make([]string, len(uris))
	for i, uri := range uris {
		path, err := dialogURIToPath(uri)
		if err != nil {
			return DialogResult{}, err
		}
		paths[i] = path
	}
	return DialogResult{Paths: paths}, nil
}

// dialogURIToPath converts a file:// URI to a decoded local path and rejects
// any other scheme, non-local authority, or malformed escape.
func dialogURIToPath(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("desktop: invalid portal uri %q: %w", uri, err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("desktop: portal uri %q is not a file scheme", uri)
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", fmt.Errorf("desktop: portal uri %q has a non-local host", uri)
	}
	if parsed.Path == "" {
		return "", fmt.Errorf("desktop: portal uri %q has no path", uri)
	}
	return parsed.Path, nil
}

func (s *linuxDialogService) requestCloseCallLocked(path dbus.ObjectPath) error {
	ctx, cancel := context.WithTimeout(context.Background(), dialogOperationTimeout)
	defer cancel()
	return s.conn.Object(portalDestination, path).CallWithContext(ctx, portalRequestInterface+".Close", 0).Err
}

func (s *linuxDialogService) Close() error {
	if s == nil {
		return nil
	}
	s.opMu.Lock()
	if s.closed {
		s.opMu.Unlock()
		return nil
	}
	s.closed = true
	pending := s.takeAllRequestsLocked()
	var result error
	for _, request := range pending {
		if s.conn != nil {
			if err := s.requestCloseCallLocked(request.path); err != nil {
				result = errors.Join(result, fmt.Errorf("desktop: cancel portal request %s: %v", request.path, err))
			}
		}
	}
	if s.matchSet {
		ctx, cancel := context.WithTimeout(context.Background(), dialogOperationTimeout)
		if err := s.conn.RemoveMatchSignalContext(ctx, dialogResponseMatchOptions()...); err != nil {
			result = errors.Join(result, fmt.Errorf("desktop: remove portal signal match: %v", err))
		}
		if err := s.conn.RemoveMatchSignalContext(ctx, dialogOwnerMatchOptions()...); err != nil {
			result = errors.Join(result, fmt.Errorf("desktop: remove portal owner match: %v", err))
		}
		cancel()
		s.conn.RemoveSignal(s.channel)
		close(s.watchStop)
		s.matchSet = false
	}
	conn := s.conn
	s.conn = nil
	s.connect = nil
	s.opMu.Unlock()

	// Cancellation callbacks run after the locks are released: a callback may
	// touch the service again without deadlocking.
	for _, request := range pending {
		request.deliver(DialogResult{}, ErrDialogCanceled)
	}

	if conn != nil {
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("desktop: close session bus: %v", err))
		}
	}
	return result
}

type dialogRequest struct {
	service     *linuxDialogService
	path        dbus.ObjectPath
	callback    func(DialogResult, error)
	deliverOnce sync.Once
}

func (r *dialogRequest) Close() error {
	s := r.service
	s.opMu.Lock()
	claimed := s.takeRequestLocked(r.path) != nil
	var callErr error
	if claimed && s.conn != nil {
		callErr = s.requestCloseCallLocked(r.path)
	}
	s.opMu.Unlock()
	if !claimed {
		return nil
	}
	r.deliver(DialogResult{}, ErrDialogCanceled)
	return callErr
}

// deliver invokes the callback at most once; it never runs under the service
// mutex or the operation mutex.
func (r *dialogRequest) deliver(result DialogResult, err error) {
	r.deliverOnce.Do(func() {
		r.callback(result, err)
	})
}
