package ui

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/zhoudm1743/meltemi/internal/render"
	internalstyle "github.com/zhoudm1743/meltemi/internal/style"
	"github.com/zhoudm1743/meltemi/internal/text"
)

func writeStyleSheet(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "styles.css")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write stylesheet: %v", err)
	}
	return path
}

func syncStyleUI(t *testing.T, a *App) {
	t.Helper()
	done := make(chan struct{})
	if err := a.core.Post(func() { close(done) }); err != nil {
		t.Fatalf("style UI barrier: %v", err)
	}
	receiveUI(t, done, "style UI barrier")
}

func drainStyleFrames(r *uiTestRenderer) {
	for {
		select {
		case <-r.frames:
		default:
			return
		}
	}
}

func TestComponentIDAndClass(t *testing.T) {
	column := Column().ID("column-id").Class("column-class")
	label := Label("label").ID("label-id").Class("label-class")
	button := Button("button").ID("button-id").Class("button-class")
	input := Input(nil).ID("input-id").Class("input-class")
	password := Password(nil).ID("password-id").Class("password-class")
	area := TextArea(nil).ID("area-id").Class("area-class")

	if got := column.ID("column-id").Class("column-class"); got != column {
		t.Error("Column ID/Class did not return its own pointer")
	}
	if got := label.ID("label-id").Class("label-class"); got != label {
		t.Error("Label ID/Class did not return its own pointer")
	}
	if got := button.ID("button-id").Class("button-class"); got != button {
		t.Error("Button ID/Class did not return its own pointer")
	}
	if got := input.ID("input-id").Class("input-class"); got != input {
		t.Error("Input ID/Class did not return its own pointer")
	}
	if got := password.ID("password-id").Class("password-class"); got != password {
		t.Error("Password ID/Class did not return its own pointer")
	}
	if got := area.ID("area-id").Class("area-class"); got != area {
		t.Error("TextArea ID/Class did not return its own pointer")
	}

	tests := []struct {
		name      string
		component component
		wantType  string
		wantID    string
		wantClass string
	}{
		{"Column", column, "Column", "column-id", "column-class"},
		{"Label", label, "Label", "label-id", "label-class"},
		{"Button", button, "Button", "button-id", "button-class"},
		{"Input", input, "Input", "input-id", "input-class"},
		{"Password", password, "Password", "password-id", "password-class"},
		{"TextArea", area, "TextArea", "area-id", "area-class"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, ok := tt.component.(interface{ styleMeta() internalstyle.Meta })
			if !ok {
				t.Fatal("component does not expose style metadata internally")
			}
			got := provider.styleMeta()
			if got.Type != tt.wantType || got.ID != tt.wantID || got.Class != tt.wantClass {
				t.Errorf("style metadata = %+v, want type %q ID %q class %q", got, tt.wantType, tt.wantID, tt.wantClass)
			}
		})
	}
}

func TestCodeLayoutPriority(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	path := writeStyleSheet(t, "#layout { gap: 40px; padding: 30px; }")
	if err := a.LoadStyles(path); err != nil {
		t.Fatalf("LoadStyles: %v", err)
	}
	first := fake("first", 60, 20)
	second := fake("second", 80, 30)
	root := Column(first, second).ID("layout").Gap(6).Padding(8)
	window := a.Window("layout").Size(240, 160).Content(root)
	done := runUIAsync(a)
	defer func() {
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	showUI(t, window, backend, factory)
	syncStyleUI(t, a)

	if got, want := first.bounds, rect(8, 8, 224, 20); got != want {
		t.Errorf("first bounds = %v, want explicit Gap/Padding layout %v", got, want)
	}
	if got, want := second.bounds, rect(8, 34, 224, 30); got != want {
		t.Errorf("second bounds = %v, want explicit Gap/Padding layout %v", got, want)
	}
}

func TestLoadStylesFailurePreservesPrevious(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	validPath := writeStyleSheet(t, "#status { width: 144px; height: 31px; }")
	if err := a.LoadStyles(validPath); err != nil {
		t.Fatalf("LoadStyles valid sheet: %v", err)
	}
	label := Label("status").ID("status")
	window := a.Window("main").Size(240, 120).Content(label)
	done := runUIAsync(a)
	defer func() {
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	_, renderer := showUI(t, window, backend, factory)
	syncStyleUI(t, a)
	oldSheet := a.sheet
	oldFace := label.face
	oldSize := label.measure(Constraints{})
	if oldSheet == nil || oldSize != (Size{Width: 144, Height: 31}) {
		t.Fatalf("valid sheet was not applied: sheet=%p natural size=%v", oldSheet, oldSize)
	}

	tests := []struct {
		name         string
		contents     string
		wantProperty string
		wantMessage  string
	}{
		{"parse error", "#status { unknown: 1px; }", "unknown", "unsupported property"},
		{"unregistered font", "#status { width: 190px; font-family: Missing; }", "font-family", "Missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeStyleSheet(t, tt.contents)
			begins := renderer.begins
			err := a.LoadStyles(path)
			if err == nil {
				t.Fatal("LoadStyles error = nil")
			}
			var cssErr *internalstyle.Error
			if !errors.As(err, &cssErr) {
				t.Fatalf("LoadStyles error type = %T, want *style.Error: %v", err, err)
			}
			if cssErr.File != path || cssErr.Line != 1 || cssErr.Column == 0 || cssErr.Property != tt.wantProperty {
				t.Errorf("error location = %+v, want file %q line 1 property %q", cssErr, path, tt.wantProperty)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("LoadStyles error = %q, want it to contain %q", err, tt.wantMessage)
			}
			syncStyleUI(t, a)
			if got := label.measure(Constraints{}); a.sheet != oldSheet || label.face != oldFace || got != oldSize {
				t.Errorf("failed load changed style: sheet preserved=%v face preserved=%v natural size=%v want %v", a.sheet == oldSheet, label.face == oldFace, got, oldSize)
			}
			if renderer.begins != begins {
				t.Errorf("failed load redraw count = %d, want %d", renderer.begins, begins)
			}
		})
	}
}

func TestLoadStylesAppliesToExistingAndNewWindows(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	done := runUIAsync(a)
	defer func() {
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	firstLabel := Label("first").ID("status")
	secondLabel := Label("second").ID("status")
	firstWindow := a.Window("first").Size(240, 120).Content(firstLabel)
	secondWindow := a.Window("second").Size(240, 120).Content(secondLabel)
	_, firstRenderer := showUI(t, firstWindow, backend, factory)
	_, secondRenderer := showUI(t, secondWindow, backend, factory)
	syncStyleUI(t, a)
	drainStyleFrames(firstRenderer)
	drainStyleFrames(secondRenderer)
	firstBegins := firstRenderer.begins
	secondBegins := secondRenderer.begins

	path := writeStyleSheet(t, "#status { width: 144px; height: 31px; }")
	if err := a.LoadStyles(path); err != nil {
		t.Fatalf("LoadStyles: %v", err)
	}
	receiveUI(t, firstRenderer.frames, "first existing-window style redraw")
	receiveUI(t, secondRenderer.frames, "second existing-window style redraw")
	syncStyleUI(t, a)
	if firstRenderer.begins != firstBegins+1 || secondRenderer.begins != secondBegins+1 {
		t.Errorf("style redraw Begin counts = %d and %d, want %d and %d", firstRenderer.begins, secondRenderer.begins, firstBegins+1, secondBegins+1)
	}
	for name, label := range map[string]*LabelWidget{"first": firstLabel, "second": secondLabel} {
		if got, want := label.measure(Constraints{}), (Size{Width: 144, Height: 31}); got != want {
			t.Errorf("%s existing-window label natural size = %v, want %v", name, got, want)
		}
	}

	newLabel := Label("new").ID("status")
	newWindow := a.Window("new").Size(240, 120).Content(newLabel)
	showUI(t, newWindow, backend, factory)
	syncStyleUI(t, a)
	if got, want := newLabel.measure(Constraints{}), (Size{Width: 144, Height: 31}); got != want {
		t.Errorf("new-window label natural size = %v, want %v", got, want)
	}
}

func TestCSSSizeDoesNotOverrideColumnStretch(t *testing.T) {
	var duplicateLog bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&duplicateLog)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	path := writeStyleSheet(t, "#duplicate { width: 40px; height: 12px; }")
	if err := a.LoadStyles(path); err != nil {
		t.Fatalf("LoadStyles: %v", err)
	}
	first := Label("first").ID("duplicate")
	second := Label("second").ID("duplicate")
	root := Column(first, second)
	window := a.Window("stretch").Size(240, 100).Content(root)
	done := runUIAsync(a)
	defer func() {
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	showUI(t, window, backend, factory)
	syncStyleUI(t, a)

	for _, check := range []struct {
		name  string
		label *LabelWidget
		want  render.Rect
	}{
		{"first", first, rect(0, 0, 240, 12)},
		{"second", second, rect(0, 12, 240, 12)},
	} {
		if got := check.label.bounds; got != check.want {
			t.Errorf("%s label bounds = %v, want Column-stretched %v", check.name, got, check.want)
		}
	}
	messages := duplicateLog.String()
	if strings.Count(messages, "\n") != 1 || !strings.Contains(messages, "duplicate") {
		t.Errorf("duplicate ID debug log = %q, want one report naming %q", messages, "duplicate")
	}
}

func checkStyleFace(t *testing.T, face *text.Face, data []byte, size float64) {
	t.Helper()
	if face == nil {
		t.Fatal("styled Face = nil")
	}
	got, err := face.CaretOffsets("account")
	if err != nil {
		t.Fatalf("styled CaretOffsets: %v", err)
	}
	reference, err := text.NewFace(data, size)
	if err != nil {
		t.Fatalf("reference Face: %v", err)
	}
	defer reference.Close()
	want, err := reference.CaretOffsets("account")
	if err != nil {
		t.Fatalf("reference CaretOffsets: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("styled Face offsets = %v, want registered-font offsets %v", got, want)
	}
}

func TestFontRegistryIsolationAndCleanup(t *testing.T) {
	css := ".shared { font-family: Shared; font-size: 22px; }\n#large { font-family: Shared; font-size: 27px; }"
	newFontApp := func(t *testing.T, data []byte) (*App, *uiTestBackend, *uiTestRendererFactory) {
		t.Helper()
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		if err := a.RegisterFont("Shared", data); err != nil {
			t.Fatalf("RegisterFont: %v", err)
		}
		if err := a.LoadStyles(writeStyleSheet(t, css)); err != nil {
			t.Fatalf("LoadStyles: %v", err)
		}
		return a, backend, factory
	}

	first, firstBackend, firstFactory := newFontApp(t, gomono.TTF)
	second, secondBackend, secondFactory := newFontApp(t, goregular.TTF)
	first.SetLastWindowAction(LastWindowKeepRunning)
	var firstClosed []*text.Face
	closeFace := first.closeFace
	first.closeFace = func(face *text.Face) error {
		firstClosed = append(firstClosed, face)
		return closeFace(face)
	}

	firstDone := runUIAsync(first)
	firstFinished := false
	defer func() {
		if !firstFinished {
			first.Quit()
			if err := waitUIReturn(t, firstDone); err != nil {
				t.Errorf("first App Run: %v", err)
			}
		}
	}()
	secondDone := runUIAsync(second)
	secondFinished := false
	defer func() {
		if !secondFinished {
			second.Quit()
			if err := waitUIReturn(t, secondDone); err != nil {
				t.Errorf("second App Run: %v", err)
			}
		}
	}()

	firstSmall := Label("first small").Class("shared")
	firstSame := Label("first same").Class("shared")
	firstLarge := Label("first large").ID("large")
	firstWindow := first.Window("first").Size(320, 180).Content(Column(firstSmall, firstSame, firstLarge))
	showUI(t, firstWindow, firstBackend, firstFactory)
	secondSmall := Label("second small").Class("shared")
	secondWindow := second.Window("second").Size(320, 180).Content(secondSmall)
	showUI(t, secondWindow, secondBackend, secondFactory)
	syncStyleUI(t, first)
	syncStyleUI(t, second)

	checkStyleFace(t, firstSmall.face, gomono.TTF, 22)
	checkStyleFace(t, firstLarge.face, gomono.TTF, 27)
	checkStyleFace(t, secondSmall.face, goregular.TTF, 22)
	if firstSmall.face != firstSame.face {
		t.Error("same App and family+size did not reuse its cached Face")
	}
	if firstSmall.face == firstLarge.face {
		t.Error("different sizes unexpectedly share a Face")
	}
	if firstSmall.face == secondSmall.face {
		t.Error("different Apps share a Face for the same family+size")
	}
	firstDefault := first.face
	oldSmall := firstSmall.face
	oldLarge := firstLarge.face

	if err := firstWindow.Close(); err != nil {
		t.Fatalf("Close first App window: %v", err)
	}
	postBarrier(t, first)
	assertUIRunning(t, firstDone)
	if first.face != nil || len(firstClosed) != 3 {
		t.Errorf("last-window cleanup: default=%p closed Faces=%d, want nil and 3", first.face, len(firstClosed))
	}
	seen := make(map[*text.Face]bool)
	for _, face := range firstClosed {
		if seen[face] {
			t.Errorf("Face %p was closed more than once", face)
		}
		seen[face] = true
		if _, err := face.Rasterize("closed"); err == nil {
			t.Errorf("cached Face %p remains usable after last-window cleanup", face)
		}
	}
	if !seen[firstDefault] || !seen[oldSmall] || !seen[oldLarge] {
		t.Errorf("cleanup did not include default and every styled Face: %v", firstClosed)
	}
	if _, err := secondSmall.face.Rasterize("alive"); err != nil {
		t.Errorf("first App cleanup closed the second App's Face: %v", err)
	}

	rebuiltSmall := Label("rebuilt small").Class("shared")
	rebuiltLarge := Label("rebuilt large").ID("large")
	rebuiltWindow := first.Window("rebuilt").Size(320, 180).Content(Column(rebuiltSmall, rebuiltLarge))
	showUI(t, rebuiltWindow, firstBackend, firstFactory)
	syncStyleUI(t, first)
	if first.face == nil || first.face == firstDefault || rebuiltSmall.face == nil || rebuiltSmall.face == oldSmall || rebuiltLarge.face == oldLarge {
		t.Error("KeepRunning rebuild reused a closed default or styled Face")
	}
	checkStyleFace(t, rebuiltSmall.face, gomono.TTF, 22)
	if len(firstClosed) != 3 {
		t.Errorf("rebuild closed Faces prematurely: %d", len(firstClosed))
	}
	if err := rebuiltWindow.Close(); err != nil {
		t.Fatalf("Close rebuilt window: %v", err)
	}
	postBarrier(t, first)
	assertUIRunning(t, firstDone)
	if len(firstClosed) != 6 {
		t.Errorf("closed Face count after rebuild cleanup = %d, want 6", len(firstClosed))
	}
	if _, err := rebuiltSmall.face.Rasterize("closed"); err == nil {
		t.Error("rebuilt styled Face remains usable after final window close")
	}

	if err := secondWindow.Close(); err != nil {
		t.Fatalf("Close second App window: %v", err)
	}
	if err := waitUIReturn(t, secondDone); err != nil {
		t.Fatalf("second App Run: %v", err)
	}
	secondFinished = true
	if _, err := secondSmall.face.Rasterize("closed"); err == nil {
		t.Error("second App styled Face remains usable after cleanup")
	}

	first.Quit()
	if err := waitUIReturn(t, firstDone); err != nil {
		t.Fatalf("first App Run: %v", err)
	}
	firstFinished = true
}

type labelStyleSnapshot struct {
	values map[string]string
	face   *text.Face
	size   Size
}

func snapshotLabelStyle(label *LabelWidget) labelStyleSnapshot {
	var values map[string]string
	if label.resolved.Values != nil {
		values = make(map[string]string, len(label.resolved.Values))
		for name, value := range label.resolved.Values {
			values[name] = value
		}
	}
	return labelStyleSnapshot{values: values, face: label.face, size: label.measure(Constraints{})}
}

func assertLabelStyleSnapshot(t *testing.T, label *LabelWidget, want labelStyleSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(label.resolved.Values, want.values) {
		t.Errorf("Resolved values = %v, want preserved %v", label.resolved.Values, want.values)
	}
	if label.face != want.face {
		t.Errorf("Face = %p, want preserved %p", label.face, want.face)
	}
	if got := label.measure(Constraints{}); got != want.size {
		t.Errorf("natural size = %v, want preserved %v", got, want.size)
	}
}

func TestStyleApplicationFailureIsAtomic(t *testing.T) {
	t.Run("existing windows", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		oldSheet := "#first { width: 101px; height: 11px; }\n#second { width: 102px; height: 12px; }\n#third { width: 103px; height: 13px; }"
		if err := a.LoadStyles(writeStyleSheet(t, oldSheet)); err != nil {
			t.Fatalf("LoadStyles old sheet: %v", err)
		}
		first := Label("first").ID("first")
		second := Label("second").ID("second")
		third := Label("third").ID("third")
		firstWindow := a.Window("first").Size(240, 120).Content(first)
		secondWindow := a.Window("second").Size(240, 120).Content(Column(second, third))
		done := runUIAsync(a)
		finished := false
		defer func() {
			if !finished {
				a.Quit()
				_ = waitUIReturn(t, done)
			}
		}()
		_, firstRenderer := showUI(t, firstWindow, backend, factory)
		_, secondRenderer := showUI(t, secondWindow, backend, factory)
		syncStyleUI(t, a)
		sheetBefore := a.sheet
		firstBefore := snapshotLabelStyle(first)
		secondBefore := snapshotLabelStyle(second)
		thirdBefore := snapshotLabelStyle(third)
		firstBegins := firstRenderer.begins
		secondBegins := secondRenderer.begins

		newSheet := "#first { width: 151px; height: 41px; font-size: 22px; }\n#second { width: 152px; height: 42px; font-size: 22px; }\n#third { width: 153px; height: 43px; font-size: 0px; }"
		if err := a.LoadStyles(writeStyleSheet(t, newSheet)); err != nil {
			t.Fatalf("LoadStyles Post: %v", err)
		}
		syncStyleUI(t, a)
		if a.err() == nil || !strings.Contains(a.err().Error(), "font size 0") {
			t.Fatalf("application error = %v, want font-size 0 failure", a.err())
		}
		if a.sheet != sheetBefore {
			t.Error("failed application replaced the old sheet")
		}
		assertLabelStyleSnapshot(t, first, firstBefore)
		assertLabelStyleSnapshot(t, second, secondBefore)
		assertLabelStyleSnapshot(t, third, thirdBefore)
		if firstRenderer.begins != firstBegins || secondRenderer.begins != secondBegins {
			t.Errorf("failed application redrew windows: Begin = %d and %d, want %d and %d", firstRenderer.begins, secondRenderer.begins, firstBegins, secondBegins)
		}

		a.Quit()
		runErr := waitUIReturn(t, done)
		finished = true
		if runErr == nil || !strings.Contains(runErr.Error(), "font size 0") {
			t.Errorf("Run error = %v, want original font-size failure", runErr)
		}
	})

	t.Run("new window", func(t *testing.T) {
		backend := newUITestBackend()
		a, factory := newUITestApp(backend, &uiTestLog{})
		sheet := "#first { width: 140px; font-size: 22px; }\n#second { font-size: 0px; }"
		if err := a.LoadStyles(writeStyleSheet(t, sheet)); err != nil {
			t.Fatalf("LoadStyles: %v", err)
		}
		first := Label("first").ID("first")
		second := Label("second").ID("second")
		firstBefore := snapshotLabelStyle(first)
		secondBefore := snapshotLabelStyle(second)
		a.Window("new").Size(240, 120).Content(Column(first, second)).Show()
		if err := a.Run(); err == nil || !strings.Contains(err.Error(), "font size 0") {
			t.Fatalf("Run error = %v, want font-size 0 failure", err)
		}
		assertLabelStyleSnapshot(t, first, firstBefore)
		assertLabelStyleSnapshot(t, second, secondBefore)
		if len(factory.renderers) != 0 {
			t.Errorf("renderer creation occurred before style commit: %v", factory.renderers)
		}
		if backend.Destroyed() != 1 || len(a.Windows()) != 0 || a.face != nil || len(a.faceCache) != 0 {
			t.Errorf("new-window cleanup = destroys %d windows %d default %p cached %d", backend.Destroyed(), len(a.Windows()), a.face, len(a.faceCache))
		}
	})
}

func TestWindowContentStyleFailureIsAtomic(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	if err := a.LoadStyles(writeStyleSheet(t, "#bad { font-size: 0px; }")); err != nil {
		t.Fatalf("LoadStyles: %v", err)
	}
	oldInput := Input(nil).ID("duplicate")
	oldRoot := Column(oldInput, Label("old").ID("duplicate"))
	window := a.Window("content").Size(320, 160).Content(oldRoot)
	done := runUIAsync(a)
	finished := false
	defer func() {
		if !finished {
			a.Quit()
			_ = waitUIReturn(t, done)
		}
	}()
	target, renderer := showUI(t, window, backend, factory)
	syncStyleUI(t, a)
	oldRouter := window.router
	oldDuplicates := map[string]bool{"duplicate": true}
	if window.root != oldRoot || oldRouter == nil || oldRouter.root != oldRoot || oldInput.clipboard != target || !reflect.DeepEqual(window.duplicateIDs, oldDuplicates) {
		t.Fatalf("initial live state is not the expected old tree: root=%p router=%p clipboard=%v duplicates=%v", window.root, oldRouter, oldInput.clipboard, window.duplicateIDs)
	}
	begins := renderer.begins

	newInput := Input(nil).ID("new-duplicate")
	newRoot := Column(
		newInput,
		Label("new").ID("new-duplicate"),
		Label("bad").ID("bad"),
	)
	window.Content(newRoot)
	syncStyleUI(t, a)

	if a.err() == nil || !strings.Contains(a.err().Error(), "font size 0") {
		t.Fatalf("application error = %v, want font-size 0 failure", a.err())
	}
	if window.root != oldRoot {
		t.Errorf("failed Content root = %p, want old root %p", window.root, oldRoot)
	}
	if window.router != oldRouter || window.router.root != oldRoot {
		t.Errorf("failed Content router = %p with root %p, want old router %p with old root", window.router, window.router.root, oldRouter)
	}
	if oldInput.clipboard != target || newInput.clipboard != nil {
		t.Errorf("failed Content clipboards = old %v new %v, want old target and new nil", oldInput.clipboard, newInput.clipboard)
	}
	if !reflect.DeepEqual(window.duplicateIDs, oldDuplicates) {
		t.Errorf("failed Content duplicate IDs = %v, want preserved %v", window.duplicateIDs, oldDuplicates)
	}
	if newInput.face != nil || newInput.resolved.Values != nil {
		t.Errorf("failed Content partially styled the new tree: face=%p resolved=%v", newInput.face, newInput.resolved.Values)
	}
	if renderer.begins != begins {
		t.Errorf("failed Content redraw count = %d, want %d", renderer.begins, begins)
	}

	a.Quit()
	runErr := waitUIReturn(t, done)
	finished = true
	if runErr == nil || !strings.Contains(runErr.Error(), "font size 0") {
		t.Errorf("Run error = %v, want original font-size failure", runErr)
	}
}

func TestSameFaceRedrawPreservesTextCaches(t *testing.T) {
	backend := newUITestBackend()
	a, factory := newUITestApp(backend, &uiTestLog{})
	if err := a.LoadStyles(writeStyleSheet(t, ".control { font-family: GoRegular; font-size: 18px; }")); err != nil {
		t.Fatalf("LoadStyles: %v", err)
	}
	button := Button("hover").Class("control")
	inputValue := "input"
	input := Input(&inputValue).Class("control")
	areaValue := "area"
	area := TextArea(&areaValue).Class("control")
	window := a.Window("caches").Size(320, 300).Content(Column(button, input, area))
	done := runUIAsync(a)
	defer func() {
		a.Quit()
		if err := waitUIReturn(t, done); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()
	_, renderer := showUI(t, window, backend, factory)
	syncStyleUI(t, a)
	buttonFace := button.face
	inputFace := input.face
	areaFace := area.face
	buttonBitmap := button.bitmap
	inputMetrics := input.metrics
	areaMetrics := area.metrics
	if buttonBitmap == nil || inputMetrics == nil || areaMetrics == nil {
		t.Fatal("initial frame did not populate every text cache")
	}

	drainStyleFrames(renderer)
	if err := a.Post(func() {
		button.hover = true
		input.focused = true
		area.focused = true
	}); err != nil {
		t.Fatalf("Post hover/focus redraw: %v", err)
	}
	receiveUI(t, renderer.frames, "hover/focus redraw")
	syncStyleUI(t, a)
	if button.face != buttonFace || input.face != inputFace || area.face != areaFace {
		t.Error("unchanged styles replaced a component Face")
	}
	if button.bitmap != buttonBitmap {
		t.Error("hover redraw invalidated Button bitmap with an unchanged Face")
	}
	if input.metrics != inputMetrics {
		t.Error("focus redraw invalidated Input metrics with an unchanged Face")
	}
	if area.metrics != areaMetrics {
		t.Error("focus redraw invalidated TextArea metrics with an unchanged Face")
	}
}
