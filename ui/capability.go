package ui

// Capabilities reports desktop features that the current application and
// platform can actually provide.
type Capabilities struct {
	MenuBar    bool
	Tray       bool
	FileDialog bool
}

// Capabilities queries the current platform. MenuBar is the window-local,
// self-drawn menu bar; Tray requires a running StatusNotifierWatcher on Linux;
// FileDialog requires the XDG desktop portal FileChooser backend (Linux only
// for now).
func (a *App) Capabilities() Capabilities {
	capabilities := Capabilities{MenuBar: true}
	if a == nil {
		return capabilities
	}
	if a.newTrayService != nil {
		service := a.newTrayService()
		if service == nil {
			return capabilities
		}
		defer service.Close()
		capabilities.Tray = service.Capabilities().Tray
	}
	if a.newDialogService != nil {
		dialogService := a.newDialogService()
		if dialogService == nil {
			return capabilities
		}
		defer dialogService.Close()
		capabilities.FileDialog = dialogService.Capabilities().FileDialog
	}
	return capabilities
}
