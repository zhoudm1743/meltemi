package ui

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"

	internalapp "github.com/zhoudm1743/meltemi/internal/app"
	"github.com/zhoudm1743/meltemi/internal/driver"
	driverfake "github.com/zhoudm1743/meltemi/internal/driver/fake"
	"github.com/zhoudm1743/meltemi/internal/render"
	"github.com/zhoudm1743/meltemi/internal/text"
)

type uiTestLog struct {
	events []string
}

func (l *uiTestLog) add(event string) {
	l.events = append(l.events, event)
}

type uiTestTarget struct {
	*driverfake.Window
	name         string
	log          *uiTestLog
	makeErr      error
	makeFailed   chan struct{}
	makeFailOnce sync.Once
	handler      driver.Handler
	shown        chan struct{}
	showOnce     sync.Once
	hides        int
	logicalSize  driver.Size
}

func (w *uiTestTarget) Show() {
	w.log.add("show:" + w.name)
	w.Window.Show()
	w.showOnce.Do(func() { close(w.shown) })
}

func (w *uiTestTarget) Hide() {
	w.hides++
	w.log.add("hide:" + w.name)
	w.Window.Hide()
}

func (w *uiTestTarget) Destroy() {
	w.log.add("destroy:" + w.name)
	w.Window.Destroy()
}

func (w *uiTestTarget) SetHandler(handler driver.Handler) {
	w.handler = handler
	w.Window.SetHandler(handler)
}

func (w *uiTestTarget) MakeCurrent() error {
	w.log.add("makecurrent:" + w.name)
	if w.makeErr != nil && w.makeFailed != nil {
		w.makeFailOnce.Do(func() { close(w.makeFailed) })
	}
	return w.makeErr
}

func (w *uiTestTarget) FramebufferSize() driver.Size { return w.Size() }

func (w *uiTestTarget) Present() { w.log.add("present:" + w.name) }

type uiTestBackend struct {
	*driverfake.Backend
	log            *uiTestLog
	targets        []*uiTestTarget
	configs        []driver.Config
	created        chan *uiTestTarget
	newWindowCalls int
	newWindowErrs  map[int]error
}

func newUITestBackend() *uiTestBackend {
	return &uiTestBackend{
		Backend:       driverfake.New(nil),
		created:       make(chan *uiTestTarget, 8),
		newWindowErrs: make(map[int]error),
	}
}

func (b *uiTestBackend) setLog(log *uiTestLog) {
	b.log = log
	for _, target := range b.targets {
		target.log = log
	}
}

func (b *uiTestBackend) NewWindow(config driver.Config) (driver.Window, error) {
	b.newWindowCalls++
	if err := b.newWindowErrs[b.newWindowCalls]; err != nil {
		return nil, err
	}
	native, err := b.Backend.NewWindow(config)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("window-%d", len(b.targets)+1)
	target := &uiTestTarget{
		Window: native.(*driverfake.Window),
		name:   name,
		log:    b.log,
		shown:  make(chan struct{}),
	}
	b.targets = append(b.targets, target)
	b.configs = append(b.configs, config)
	b.created <- target
	return target, nil
}

type uiTestRenderer struct {
	name      string
	log       *uiTestLog
	target    *uiTestTarget
	beginErr  error
	onBegin   func()
	drawErr   error
	endErr    error
	begins    int
	ends      int
	closed    atomic.Bool
	discarded bool
	texts     []string
	faces     []*text.Face
	frames    chan struct{}
}

func (r *uiTestRenderer) Begin(render.Color) error {
	r.begins++
	r.log.add("begin:" + r.name)
	if err := r.target.MakeCurrent(); err != nil {
		return err
	}
	if r.onBegin != nil {
		r.onBegin()
	}
	return r.beginErr
}

func (r *uiTestRenderer) DrawRect(render.Rect, render.Color) {}

func (r *uiTestRenderer) DrawText(value string, _ driver.Point, _ render.Color, face *text.Face) error {
	r.texts = append(r.texts, value)
	r.faces = append(r.faces, face)
	r.log.add("drawtext:" + r.name)
	return r.drawErr
}

func (r *uiTestRenderer) DrawImage(*render.Image, render.Rect, float64) error {
	return r.drawErr
}

func (r *uiTestRenderer) End() error {
	r.ends++
	r.log.add("end:" + r.name)
	r.frames <- struct{}{}
	return r.endErr
}

func (r *uiTestRenderer) Close() {
	r.closed.Store(true)
	r.log.add("close-renderer:" + r.name)
}

func (r *uiTestRenderer) Discard() {
	r.discarded = true
	r.log.add("discard-renderer:" + r.name)
}

type uiTestRendererFactory struct {
	log        *uiTestLog
	renderers  map[string]*uiTestRenderer
	inject     map[string]func(*uiTestRenderer)
	createErrs map[string]error
	created    chan *uiTestRenderer
}

func (f *uiTestRendererFactory) new(target render.Target) (renderer, error) {
	target.(*uiTestTarget).log = f.log
	if err := target.MakeCurrent(); err != nil {
		return nil, err
	}
	name := target.(*uiTestTarget).name
	if err := f.createErrs[name]; err != nil {
		return nil, err
	}
	r := &uiTestRenderer{
		name:   name,
		log:    f.log,
		target: target.(*uiTestTarget),
		frames: make(chan struct{}, 32),
	}
	if inject := f.inject[name]; inject != nil {
		inject(r)
	}
	f.renderers[name] = r
	f.created <- r
	return r, nil
}

func newUITestApp(backend driver.Backend, log *uiTestLog) (*App, *uiTestRendererFactory) {
	a := New()
	a.core = internalapp.New(backend)
	if backend, ok := backend.(interface{ setLog(*uiTestLog) }); ok {
		backend.setLog(log)
	}
	factory := &uiTestRendererFactory{
		log:        log,
		renderers:  make(map[string]*uiTestRenderer),
		inject:     make(map[string]func(*uiTestRenderer)),
		createErrs: make(map[string]error),
		created:    make(chan *uiTestRenderer, 8),
	}
	a.newRenderer = factory.new
	return a, factory
}

func receiveUI[T any](t *testing.T, ch <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func runUIAsync(a *App) <-chan error {
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	return done
}

func showUI(t *testing.T, w *Window, backend *uiTestBackend, factory *uiTestRendererFactory) (*uiTestTarget, *uiTestRenderer) {
	t.Helper()
	w.Show()
	target := receiveUI(t, backend.created, "native window creation")
	r := receiveUI(t, factory.created, "renderer creation")
	receiveUI(t, target.shown, "native Show")
	receiveUI(t, r.frames, "first frame")
	settled := make(chan struct{})
	if err := w.app.core.Post(func() { close(settled) }); err != nil {
		t.Fatalf("Show completion barrier: %v", err)
	}
	receiveUI(t, settled, "Show completion barrier")
	for {
		select {
		case <-r.frames:
		default:
			return target, r
		}
	}
}

func waitUIReturn(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("App.Run did not return within 2 seconds")
		return nil
	}
}

func assertUIRunning(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("App.Run returned early with %v", err)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestWindowShowContentAndPost(t *testing.T) {
	backend := newUITestBackend()
	log := &uiTestLog{}
	a, factory := newUITestApp(backend, log)
	for _, target := range backend.targets {
		target.log = log
	}

	leaf := fake("leaf", 40, 20)
	label := Label("before")
	other := Label("other")
	a.Window("main").Size(320, 200).Content(Column(leaf, label)).Show()
	a.Window("other").Size(200, 100).Content(other).Show()
	if err := a.Post(func() { label.SetText("after") }); err != nil {
		t.Fatalf("Post update: %v", err)
	}
	copyIsolated := false
	if err := a.Post(func() {
		windows := a.Windows()
		copyIsolated = len(windows) == 2
		windows[0] = nil
		copyIsolated = copyIsolated && len(a.Windows()) == 2 && a.Windows()[0] != nil
		a.Quit()
	}); err != nil {
		t.Fatalf("Post shutdown: %v", err)
	}
	if err := a.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !copyIsolated {
		t.Error("Windows did not return two live windows in an isolated copy")
	}
	if leaf.measureCalls < 2 {
		t.Fatalf("measure calls = %d, want an initial frame and a Post redraw", leaf.measureCalls)
	}
	if got, want := leaf.bounds, rect(0, 0, 320, 20); got != want {
		t.Errorf("arranged leaf bounds = %v, want %v", got, want)
	}
	first := factory.renderers["window-1"]
	second := factory.renderers["window-2"]
	if first == nil || second == nil {
		t.Fatalf("renderers = %v, want one independent renderer per window", factory.renderers)
	}
	if first.begins < 2 || second.begins < 2 {
		t.Errorf("Begin calls = %d and %d, want Post to redraw every existing window", first.begins, second.begins)
	}
	if len(first.texts) == 0 || first.texts[len(first.texts)-1] != "after" {
		t.Errorf("first window text draws = %v, want the Post update to draw %q", first.texts, "after")
	}
}

func TestFirstFrameFailureCleansUp(t *testing.T) {
	beginErr := errors.New("fake: Begin failed")
	drawErr := errors.New("fake: DrawText failed")
	endErr := errors.New("fake: End failed")
	cleanupErr := errors.New("fake: Face cleanup failed")
	tests := []struct {
		name      string
		inject    func(*uiTestRenderer)
		want      error
		wantDraw  bool
		wantEnd   bool
		wantError error
	}{
		{
			name: "Begin",
			inject: func(r *uiTestRenderer) {
				r.beginErr = beginErr
				r.drawErr = drawErr
				r.endErr = endErr
			},
			want:      beginErr,
			wantError: beginErr,
		},
		{
			name: "DrawText",
			inject: func(r *uiTestRenderer) {
				r.drawErr = drawErr
				r.endErr = endErr
			},
			want:      drawErr,
			wantDraw:  true,
			wantEnd:   true,
			wantError: drawErr,
		},
		{
			name: "End",
			inject: func(r *uiTestRenderer) {
				r.endErr = endErr
			},
			want:      endErr,
			wantDraw:  true,
			wantEnd:   true,
			wantError: endErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newUITestBackend()
			log := &uiTestLog{}
			a, factory := newUITestApp(backend, log)
			factory.inject["window-2"] = tt.inject
			var face *text.Face
			newFace := a.newFace
			a.newFace = func() (*text.Face, error) {
				created, err := newFace()
				face = created
				return created, err
			}
			a.closeFace = func(face *text.Face) error {
				log.add("close-face")
				if err := face.Close(); err != nil {
					return err
				}
				return cleanupErr
			}

			a.Window("first").Size(200, 100).Content(Label("first")).Show()
			a.Window("failing").Size(200, 100).Content(Label("bad")).Show()
			err := a.Run()
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Run error = %v, want first frame error %v (not cleanup error %v)", err, tt.wantError, cleanupErr)
			}
			first := factory.renderers["window-1"]
			second := factory.renderers["window-2"]
			if first == nil || second == nil || !first.closed.Load() || !second.closed.Load() {
				t.Errorf("both established renderers must close: first=%v second=%v", first, second)
			}
			if got := backend.Destroyed(); got != 2 {
				t.Errorf("native Destroy calls = %d, want 2", got)
			}
			if got := len(a.Windows()); got != 0 {
				t.Errorf("live windows after cleanup = %d, want 0", got)
			}
			if a.face != nil {
				t.Error("App still owns a Face after cleanup")
			}
			if face == nil {
				t.Fatal("test did not capture the app-owned Face")
			}
			if _, rasterErr := face.Rasterize("closed"); rasterErr == nil {
				t.Error("Face remains usable after cleanup")
			}
			if tt.wantDraw && len(second.texts) == 0 {
				t.Error("DrawText was not attempted")
			}
			if !tt.wantDraw && len(second.texts) != 0 {
				t.Errorf("DrawText ran after Begin failure: %v", second.texts)
			}
			if tt.wantEnd && second.ends == 0 {
				t.Error("End was not attempted")
			}
			if !tt.wantEnd && second.ends != 0 {
				t.Errorf("End ran after Begin failure %d times", second.ends)
			}
			wantSuffix := []string{
				"makecurrent:window-2", "close-renderer:window-2", "destroy:window-2",
				"makecurrent:window-1", "close-renderer:window-1", "destroy:window-1",
				"close-face",
			}
			if len(log.events) < len(wantSuffix) || !reflect.DeepEqual(log.events[len(log.events)-len(wantSuffix):], wantSuffix) {
				t.Errorf("cleanup events = %v, want reverse-order suffix %v", log.events, wantSuffix)
			}
			joined := strings.Join(log.events, ",")
			if strings.Count(joined, "close-face") != 1 {
				t.Errorf("Face close count = %d, want 1", strings.Count(joined, "close-face"))
			}
		})
	}
}

func TestFirstFrameCleanupMakeCurrentFailureFinalizesState(t *testing.T) {
	backend := newUITestBackend()
	log := &uiTestLog{}
	a, factory := newUITestApp(backend, log)
	beginErr := errors.New("fake: first frame Begin failed")
	makeErr := errors.New("fake: cleanup MakeCurrent failed")
	factory.inject["window-2"] = func(r *uiTestRenderer) {
		r.beginErr = beginErr
		r.onBegin = func() { r.target.makeErr = makeErr }
	}
	var face *text.Face
	newFace := a.newFace
	a.newFace = func() (*text.Face, error) {
		created, err := newFace()
		face = created
		return created, err
	}
	a.Window("first").Size(200, 100).Content(Label("first")).Show()
	failing := a.Window("failing").Size(200, 100).Content(Label("bad"))
	failing.Show()
	if err := a.Run(); !errors.Is(err, beginErr) {
		t.Fatalf("Run error = %v, want original first frame error %v", err, beginErr)
	}
	first := factory.renderers["window-1"]
	second := factory.renderers["window-2"]
	if first == nil || second == nil {
		t.Fatal("both renderers were not created")
	}
	if !first.closed.Load() || second.closed.Load() {
		t.Errorf("cleanup renderer state = first closed %v, second closed %v; want only the first explicitly closed", first.closed.Load(), second.closed.Load())
	}
	if second.begins != 1 {
		t.Errorf("failing window Begin calls = %d, want no redraw after the first failure", second.begins)
	}
	if got := backend.Destroyed(); got != 2 {
		t.Errorf("native Destroy calls = %d, want exactly one for each of 2 windows", got)
	}
	if got := len(a.Windows()); got != 0 || failing.target != nil || failing.renderer != nil || failing.router != nil {
		t.Errorf("final cleanup left public state alive: windows=%d target=%v renderer=%v router=%v", got, failing.target, failing.renderer, failing.router)
	}
	if face == nil {
		t.Fatal("Face factory did not create the expected Face")
	}
	if a.face != nil {
		t.Error("App retained a Face after every public/native window was finalized")
	}
	if _, err := face.Rasterize("closed"); err == nil {
		t.Error("Face remains usable after final cleanup")
	}
}

func TestInitializationFailureCleansUp(t *testing.T) {
	stageErr := errors.New("fake: initialization stage failed")
	cleanupErr := errors.New("fake: Face cleanup failed")
	tests := []struct {
		name               string
		establishedWindow  bool
		prepare            func(*App, *uiTestRendererFactory, *uiTestBackend)
		wantDestroys       int
		wantEstablished    bool
		wantFaceCreated    bool
		wantFaceCloseCount int
		wantSuffix         []string
	}{
		{
			name:              "native NewWindow after an established window",
			establishedWindow: true,
			prepare: func(_ *App, _ *uiTestRendererFactory, backend *uiTestBackend) {
				backend.newWindowErrs[2] = stageErr
			},
			wantDestroys:       1,
			wantEstablished:    true,
			wantFaceCreated:    true,
			wantFaceCloseCount: 1,
			wantSuffix: []string{
				"makecurrent:window-1", "close-renderer:window-1", "destroy:window-1", "close-face",
			},
		},
		{
			name: "Face creation",
			prepare: func(a *App, _ *uiTestRendererFactory, _ *uiTestBackend) {
				a.newFace = func() (*text.Face, error) { return nil, stageErr }
			},
			wantDestroys:       1,
			wantFaceCreated:    false,
			wantFaceCloseCount: 0,
			wantSuffix:         []string{"destroy:window-1"},
		},
		{
			name:              "renderer creation after an established window",
			establishedWindow: true,
			prepare: func(_ *App, factory *uiTestRendererFactory, _ *uiTestBackend) {
				factory.createErrs["window-2"] = stageErr
			},
			wantDestroys:       2,
			wantEstablished:    true,
			wantFaceCreated:    true,
			wantFaceCloseCount: 1,
			wantSuffix: []string{
				"destroy:window-2", "makecurrent:window-1", "close-renderer:window-1", "destroy:window-1", "close-face",
			},
		},
		{
			name: "renderer creation with Face cleanup failure",
			prepare: func(_ *App, factory *uiTestRendererFactory, _ *uiTestBackend) {
				factory.createErrs["window-1"] = stageErr
			},
			wantDestroys:       1,
			wantFaceCreated:    true,
			wantFaceCloseCount: 1,
			wantSuffix:         []string{"destroy:window-1", "close-face"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newUITestBackend()
			log := &uiTestLog{}
			a, factory := newUITestApp(backend, log)
			var face *text.Face
			newFace := a.newFace
			a.newFace = func() (*text.Face, error) {
				created, err := newFace()
				face = created
				return created, err
			}
			tt.prepare(a, factory, backend)
			faceCloses := 0
			a.closeFace = func(face *text.Face) error {
				faceCloses++
				log.add("close-face")
				if err := face.Close(); err != nil {
					return err
				}
				return cleanupErr
			}
			if tt.establishedWindow {
				a.Window("established").Size(200, 100).Content(Label("first")).Show()
			}
			a.Window("failing").Size(200, 100).Content(Label("bad")).Show()
			if err := a.Run(); !errors.Is(err, stageErr) {
				t.Fatalf("Run error = %v, want original initialization error %v, not cleanup error %v", err, stageErr, cleanupErr)
			}
			if got := backend.Destroyed(); got != tt.wantDestroys {
				t.Errorf("native Destroy calls = %d, want %d", got, tt.wantDestroys)
			}
			if got := len(a.Windows()); got != 0 {
				t.Errorf("public windows after cleanup = %d, want 0", got)
			}
			if a.face != nil {
				t.Error("App retained a Face after complete initialization cleanup")
			}
			if tt.wantEstablished {
				first := factory.renderers["window-1"]
				if first == nil || !first.closed.Load() {
					t.Error("established window renderer was not closed")
				}
			}
			if tt.wantFaceCreated {
				if face == nil {
					t.Fatal("Face factory did not create the expected Face")
				}
				if _, err := face.Rasterize("closed"); err == nil {
					t.Error("Face remains usable after complete cleanup")
				}
			}
			if faceCloses != tt.wantFaceCloseCount {
				t.Errorf("Face Close calls = %d, want %d", faceCloses, tt.wantFaceCloseCount)
			}
			if len(log.events) < len(tt.wantSuffix) || !reflect.DeepEqual(log.events[len(log.events)-len(tt.wantSuffix):], tt.wantSuffix) {
				t.Errorf("cleanup events = %v, want suffix %v", log.events, tt.wantSuffix)
			}
		})
	}
}

func TestRegisterFontValidation(t *testing.T) {
	a := New()
	if err := a.RegisterFont("", goregular.TTF); err == nil {
		t.Error("RegisterFont with empty name: nil error")
	}
	if err := a.RegisterFont("Empty", nil); err == nil {
		t.Error("RegisterFont with nil data: nil error")
	}
	if err := a.RegisterFont("Junk", []byte("not a font")); err == nil {
		t.Error("RegisterFont with unusable data: nil error")
	}
	if err := a.RegisterFont(DefaultFont, goregular.TTF); err != nil {
		t.Errorf("RegisterFont with usable data: %v", err)
	}
}

func TestRegisterFontOverridesDefaultFaceData(t *testing.T) {
	a := New()
	if !bytes.Equal(a.defaultFontData(), goregular.TTF) {
		t.Error("default font data without registration is not the built-in GoRegular")
	}
	data := bytes.Clone(goregular.TTF)
	if err := a.RegisterFont(DefaultFont, data); err != nil {
		t.Fatalf("RegisterFont: %v", err)
	}
	data[0] = 0 // the registry must hold its own copy, not the caller's buffer
	if got := a.defaultFontData(); !bytes.Equal(got, goregular.TTF) {
		t.Error("default font data changed when the caller's buffer was mutated")
	}

	// The face the app creates must actually come from the registered data.
	mono, err := a.newFace()
	if err != nil {
		t.Fatalf("newFace after registration: %v", err)
	}
	defer mono.Close()
	reference, err := text.NewFace(goregular.TTF, defaultFontSize)
	if err != nil {
		t.Fatalf("reference face: %v", err)
	}
	defer reference.Close()
	got, err := mono.CaretOffsets("account")
	if err != nil {
		t.Fatalf("CaretOffsets: %v", err)
	}
	want, err := reference.CaretOffsets("account")
	if err != nil {
		t.Fatalf("reference CaretOffsets: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Error("face created after registering GoRegular data does not match a face from that data")
	}
}

func TestRegisterFontChangesCreatedFace(t *testing.T) {
	a := New()
	if err := a.RegisterFont(DefaultFont, gomono.TTF); err != nil {
		t.Fatalf("RegisterFont: %v", err)
	}
	face, err := a.newFace()
	if err != nil {
		t.Fatalf("newFace: %v", err)
	}
	defer face.Close()
	mono, err := text.NewFace(gomono.TTF, defaultFontSize)
	if err != nil {
		t.Fatalf("mono reference face: %v", err)
	}
	defer mono.Close()
	regular, err := text.NewFace(goregular.TTF, defaultFontSize)
	if err != nil {
		t.Fatalf("regular reference face: %v", err)
	}
	defer regular.Close()

	got, err := face.CaretOffsets("account")
	if err != nil {
		t.Fatalf("CaretOffsets: %v", err)
	}
	monoOffsets, _ := mono.CaretOffsets("account")
	regularOffsets, _ := regular.CaretOffsets("account")
	if !reflect.DeepEqual(got, monoOffsets) {
		t.Error("created face does not use the registered data")
	}
	if reflect.DeepEqual(got, regularOffsets) {
		t.Error("created face ignored the registration and used the built-in data")
	}
}

func TestRegisterFontRejectedAfterFaceCreated(t *testing.T) {
	backend := newUITestBackend()
	log := &uiTestLog{}
	a, _ := newUITestApp(backend, log)
	a.Window("main").Size(200, 100).Content(Label("x")).Show()
	if err := a.Post(func() {
		if regErr := a.RegisterFont(DefaultFont, goregular.TTF); regErr == nil {
			t.Error("RegisterFont after the app face was created: nil error")
		}
		a.Quit()
	}); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if err := a.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
