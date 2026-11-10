package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/zhoudm1743/meltemi/internal/desktop"
)

// ErrDialogUnavailable reports that native file dialogs cannot be provided on
// the current platform or desktop session (for example no XDG portal on
// Linux). It aliases the internal desktop sentinel, so errors.Is works
// through any wrapping.
var ErrDialogUnavailable = desktop.ErrDialogUnavailable

// ErrDialogCanceled reports that a file dialog finished without a selection:
// the user dismissed it or FileDialog.Close canceled it. App.Quit invalidates
// pending dialogs without invoking their callbacks at all.
var ErrDialogCanceled = desktop.ErrDialogCanceled

// FileFilterPatternKind selects how a FileFilterPattern matches files in the
// native dialog: FileFilterPatternGlob is a glob such as "*.txt" and
// FileFilterPatternMIMEType is a MIME type such as "text/plain".
type FileFilterPatternKind uint8

const (
	FileFilterPatternGlob FileFilterPatternKind = iota
	FileFilterPatternMIMEType
)

// FileFilterPattern is one match clause of a FileFilter.
type FileFilterPattern struct {
	Kind    FileFilterPatternKind
	Pattern string
}

// FileFilter is one named entry of the file-type list the native dialog shows.
type FileFilter struct {
	Name     string
	Patterns []FileFilterPattern
}

// OpenFileOptions configures App.OpenFile. The dialog resolves zero (on
// cancel) or more existing paths; Multiple allows more than one and Directory
// selects folders instead of files.
type OpenFileOptions struct {
	Title       string
	AcceptLabel string
	Modal       bool
	Multiple    bool
	Directory   bool
	Filters     []FileFilter
	// CurrentFilter optionally requests a pre-selected entry of Filters.
	CurrentFilter *FileFilter
	// CurrentFolder optionally sets the dialog's starting folder; it must be
	// absolute when nonempty.
	CurrentFolder string
	// Parent optionally declares the window the dialog belongs to. The
	// current X11 backend cannot expose a native handle to the portal, so the
	// portal parent stays empty and the dialog cannot be declared modal over
	// Parent; the field is kept for a future backend that can.
	Parent *Window
}

// SaveFileOptions configures App.SaveFile. The dialog resolves at most one
// output path; CurrentName and CurrentFile seed the initial name.
type SaveFileOptions struct {
	Title       string
	AcceptLabel string
	Modal       bool
	Filters     []FileFilter
	// CurrentFilter optionally requests a pre-selected entry of Filters.
	CurrentFilter *FileFilter
	// CurrentFolder optionally sets the dialog's starting folder; it must be
	// absolute when nonempty.
	CurrentFolder string
	// CurrentName optionally seeds the initial file name.
	CurrentName string
	// CurrentFile optionally seeds the dialog from an existing absolute path.
	CurrentFile string
	// Parent carries the same portal-parent limitation as
	// OpenFileOptions.Parent.
	Parent *Window
}

// FileDialog tracks one in-flight App.OpenFile or App.SaveFile request.
// Close cancels the native dialog; the request callback then runs once on the
// UI thread with ErrDialogCanceled. Calls after the first Close are no-ops.
// After App.Quit pending dialogs are invalidated and their callbacks are
// dropped instead of queued.
type FileDialog struct {
	app *App

	mu        sync.Mutex
	request   desktop.DialogRequest
	delivered bool
	closed    bool
	dead      bool
}

// Close cancels the native dialog. The request callback still runs once with
// ErrDialogCanceled unless the result was already delivered or App.Quit
// invalidated the dialog. Close is idempotent and safe from any goroutine.
func (d *FileDialog) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	if d.closed || d.dead {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	request := d.request
	d.mu.Unlock()
	if request == nil {
		return nil
	}
	return request.Close()
}

// deliver is the service-level callback: it fires on a portal response
// goroutine, so it only validates the dialog state and posts the user
// callback to the UI thread. Quit sets dead before closing the service, so
// quit-time cancellations never touch the queue; delivered is the public
// exactly-once guard over duplicate or late service deliveries.
func (d *FileDialog) deliver(result desktop.DialogResult, err error, invoke func(desktop.DialogResult, error)) {
	d.mu.Lock()
	if d.dead || d.delivered {
		d.mu.Unlock()
		return
	}
	d.delivered = true
	d.mu.Unlock()
	d.app.untrackDialog(d)
	_ = d.app.post(func() { invoke(result, err) })
}

// OpenFile opens a native open dialog. The callback runs on the UI thread
// exactly once: with the selected paths, or with an error (errors.Is
// ErrDialogCanceled on cancel). The returned FileDialog can cancel the
// request; after App.Quit the callback may never run.
func (a *App) OpenFile(options OpenFileOptions, callback func(paths []string, err error)) (*FileDialog, error) {
	if callback == nil {
		return nil, errors.New("ui: open file: nil callback")
	}
	converted, err := desktopOpenOptions(options)
	if err != nil {
		return nil, err
	}
	return a.startDialog(false, converted, func(result desktop.DialogResult, err error) {
		if err != nil {
			callback(nil, err)
			return
		}
		callback(append([]string(nil), result.Paths...), nil)
	})
}

// SaveFile opens a native save dialog. The callback runs on the UI thread
// exactly once: with the chosen output path, or with an error (errors.Is
// ErrDialogCanceled on cancel). The returned FileDialog can cancel the
// request; after App.Quit the callback may never run.
func (a *App) SaveFile(options SaveFileOptions, callback func(path string, err error)) (*FileDialog, error) {
	if callback == nil {
		return nil, errors.New("ui: save file: nil callback")
	}
	converted, err := desktopSaveOptions(options)
	if err != nil {
		return nil, err
	}
	return a.startDialog(true, converted, func(result desktop.DialogResult, err error) {
		if err != nil {
			callback("", err)
			return
		}
		if len(result.Paths) == 0 {
			callback("", errors.New("ui: save file: portal returned no path"))
			return
		}
		callback(result.Paths[0], nil)
	})
}

// startDialog owns the shared service for the app, tracks the dialog and
// hands the service callback to the dialog's exactly-once delivery. The
// service call is deliberately made without holding dialogsMu: Quit may
// invalidate and close the service concurrently, and both orders resolve.
func (a *App) startDialog(save bool, options desktop.DialogOptions, invoke func(desktop.DialogResult, error)) (*FileDialog, error) {
	a.dialogsMu.Lock()
	if a.dialogsClosing {
		a.dialogsMu.Unlock()
		return nil, errAppQuitting
	}
	service := a.dialogService
	if service == nil {
		if a.newDialogService == nil {
			a.dialogsMu.Unlock()
			return nil, fmt.Errorf("ui: file dialog: %w: no platform service", ErrDialogUnavailable)
		}
		service = a.newDialogService()
		if service == nil {
			a.dialogsMu.Unlock()
			return nil, fmt.Errorf("ui: file dialog: %w: nil platform service", ErrDialogUnavailable)
		}
		a.dialogService = service
	}
	d := &FileDialog{app: a}
	a.dialogs = append(a.dialogs, d)
	a.dialogsMu.Unlock()

	start := service.Open
	if save {
		start = service.Save
	}
	request, err := start(options, func(result desktop.DialogResult, err error) {
		d.deliver(result, err, invoke)
	})
	if err != nil {
		a.untrackDialog(d)
		d.mu.Lock()
		d.dead = true
		d.mu.Unlock()
		return nil, err
	}
	d.mu.Lock()
	dead := d.dead
	d.mu.Unlock()
	if dead {
		// Quit won the race while the portal call was in flight.
		_ = request.Close()
		return nil, errAppQuitting
	}
	d.mu.Lock()
	d.request = request
	d.mu.Unlock()
	return d, nil
}

// untrackDialog drops a finished or canceled dialog from the app registry so
// completed dialogs do not accumulate until Quit.
func (a *App) untrackDialog(d *FileDialog) {
	a.dialogsMu.Lock()
	for i, candidate := range a.dialogs {
		if candidate == d {
			a.dialogs = append(a.dialogs[:i:i], a.dialogs[i+1:]...)
			break
		}
	}
	a.dialogsMu.Unlock()
}

// closeDialogs invalidates every tracked dialog and closes the shared dialog
// service. It is called synchronously from Quit and again from Run after the
// loop stops; already queued UI work is never waited on.
func (a *App) closeDialogs() error {
	a.dialogsMu.Lock()
	if a.dialogsClosing {
		a.dialogsMu.Unlock()
		return nil
	}
	a.dialogsClosing = true
	dialogs := a.dialogs
	a.dialogs = nil
	service := a.dialogService
	a.dialogService = nil
	for _, d := range dialogs {
		d.mu.Lock()
		d.dead = true
		d.mu.Unlock()
	}
	a.dialogsMu.Unlock()
	if service == nil {
		return nil
	}
	return service.Close()
}

// dialogParentIdentifier converts the public parent window into the portal
// parent identifier carried by DialogOptions. The current X11 backend cannot
// expose a native window handle to the portal, so the identifier stays empty
// and the portal cannot declare a modal parent; this limitation is recorded
// here, on the option fields and in the task report until a backend provides
// real handles.
func dialogParentIdentifier(parent *Window) string { return "" }

func desktopOpenOptions(options OpenFileOptions) (desktop.DialogOptions, error) {
	if err := validateDialogFilters("open file", options.Filters, options.CurrentFilter); err != nil {
		return desktop.DialogOptions{}, err
	}
	if options.CurrentFolder != "" && !filepath.IsAbs(options.CurrentFolder) {
		return desktop.DialogOptions{}, fmt.Errorf("ui: open file: CurrentFolder must be an absolute path, got %q", options.CurrentFolder)
	}
	return desktop.DialogOptions{
		Title:         options.Title,
		AcceptLabel:   options.AcceptLabel,
		Modal:         options.Modal,
		Multiple:      options.Multiple,
		Directory:     options.Directory,
		Filters:       desktopDialogFilters(options.Filters),
		CurrentFilter: desktopDialogCurrentFilter(options.CurrentFilter),
		CurrentFolder: options.CurrentFolder,
		ParentWindow:  dialogParentIdentifier(options.Parent),
	}, nil
}

func desktopSaveOptions(options SaveFileOptions) (desktop.DialogOptions, error) {
	if err := validateDialogFilters("save file", options.Filters, options.CurrentFilter); err != nil {
		return desktop.DialogOptions{}, err
	}
	if options.CurrentFolder != "" && !filepath.IsAbs(options.CurrentFolder) {
		return desktop.DialogOptions{}, fmt.Errorf("ui: save file: CurrentFolder must be an absolute path, got %q", options.CurrentFolder)
	}
	if options.CurrentFile != "" && !filepath.IsAbs(options.CurrentFile) {
		return desktop.DialogOptions{}, fmt.Errorf("ui: save file: CurrentFile must be an absolute path, got %q", options.CurrentFile)
	}
	return desktop.DialogOptions{
		Title:         options.Title,
		AcceptLabel:   options.AcceptLabel,
		Modal:         options.Modal,
		Filters:       desktopDialogFilters(options.Filters),
		CurrentFilter: desktopDialogCurrentFilter(options.CurrentFilter),
		CurrentFolder: options.CurrentFolder,
		CurrentName:   options.CurrentName,
		CurrentFile:   options.CurrentFile,
		ParentWindow:  dialogParentIdentifier(options.Parent),
	}, nil
}

func validateDialogFilters(context string, filters []FileFilter, current *FileFilter) error {
	for i := range filters {
		for j, pattern := range filters[i].Patterns {
			if err := validateDialogPatternKind(pattern.Kind); err != nil {
				return fmt.Errorf("ui: %s: filter %q pattern %d: %w", context, filters[i].Name, j, err)
			}
		}
	}
	if current == nil {
		return nil
	}
	for j, pattern := range current.Patterns {
		if err := validateDialogPatternKind(pattern.Kind); err != nil {
			return fmt.Errorf("ui: %s: current filter %q pattern %d: %w", context, current.Name, j, err)
		}
	}
	return nil
}

func validateDialogPatternKind(kind FileFilterPatternKind) error {
	switch kind {
	case FileFilterPatternGlob, FileFilterPatternMIMEType:
		return nil
	default:
		return fmt.Errorf("invalid filter pattern kind %d", kind)
	}
}

// desktopDialogFilters converts and deep-copies the public snapshot so later
// caller mutations cannot alter an in-flight request.
func desktopDialogFilters(filters []FileFilter) []desktop.FileFilter {
	if len(filters) == 0 {
		return nil
	}
	converted := make([]desktop.FileFilter, len(filters))
	for i, filter := range filters {
		patterns := make([]desktop.FileFilterPattern, len(filter.Patterns))
		for j, pattern := range filter.Patterns {
			kind := desktop.FileFilterPatternGlob
			if pattern.Kind == FileFilterPatternMIMEType {
				kind = desktop.FileFilterPatternMIMEType
			}
			patterns[j] = desktop.FileFilterPattern{Kind: kind, Pattern: pattern.Pattern}
		}
		converted[i] = desktop.FileFilter{Name: filter.Name, Patterns: patterns}
	}
	return converted
}

func desktopDialogCurrentFilter(current *FileFilter) *desktop.FileFilter {
	if current == nil {
		return nil
	}
	converted := desktopDialogFilters([]FileFilter{*current})
	return &converted[0]
}
