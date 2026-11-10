package desktop

import "errors"

var ErrDialogUnavailable = errors.New("desktop: dialog unavailable")

var ErrDialogCanceled = errors.New("desktop: dialog canceled")

var (
	errDialogClosed        = errors.New("desktop: dialog service closed")
	errDialogNilCallback   = errors.New("desktop: nil dialog callback")
	errDialogFilterPattern = errors.New("desktop: invalid dialog filter pattern kind")
	errDialogFolderNotAbs  = errors.New("desktop: current folder must be an absolute path")
	errDialogFileNotAbs    = errors.New("desktop: current file must be an absolute path")
)

// FileFilterPatternKind selects how a FileFilterPattern matches files on the
// portal wire: 0 is a glob pattern and 1 is a MIME type.
type FileFilterPatternKind uint8

const (
	FileFilterPatternGlob FileFilterPatternKind = iota
	FileFilterPatternMIMEType
)

type FileFilterPattern struct {
	Kind    FileFilterPatternKind
	Pattern string
}

// FileFilter is one immutable named entry of the FileChooser filters option.
type FileFilter struct {
	Name     string
	Patterns []FileFilterPattern
}

// DialogOptions is the immutable snapshot of one Open or Save request. The
// service copies anything it puts on the wire and never mutates the source.
type DialogOptions struct {
	Title       string
	AcceptLabel string
	Modal       bool
	Multiple    bool
	Directory   bool
	Filters     []FileFilter
	// CurrentFilter optionally requests that this filter be pre-selected at
	// dialog creation; the portal expects it to match an entry in Filters
	// when the list is nonempty. Wire option current_filter, type (sa(us)).
	CurrentFilter *FileFilter
	CurrentFolder string
	CurrentName   string
	CurrentFile   string
	// ParentWindow carries a pre-converted portal parent identifier; the
	// service never inspects native window handles. It stays empty on the
	// current X11 backend, which cannot declare a modal parent.
	ParentWindow string
}

// DialogResult is the immutable outcome of one completed dialog.
type DialogResult struct {
	Paths []string
}

// DialogRequest is one in-flight dialog. Close cancels it through the portal
// and is idempotent; after Close no response can reach the callback again.
type DialogRequest interface {
	Close() error
}

// DialogService opens native file dialogs through the desktop portal.
type DialogService interface {
	Capabilities() Capabilities
	Open(DialogOptions, func(DialogResult, error)) (DialogRequest, error)
	Save(DialogOptions, func(DialogResult, error)) (DialogRequest, error)
	Close() error
}

func NewDialogService() DialogService {
	return newPlatformDialogService()
}
