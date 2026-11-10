//go:build !linux

package desktop

import "fmt"

type unsupportedTrayService struct{}

func newPlatformTrayService() TrayService { return unsupportedTrayService{} }

func (unsupportedTrayService) Capabilities() Capabilities { return Capabilities{} }

func (unsupportedTrayService) Show(TrayConfig) error {
	return fmt.Errorf("%w: StatusNotifierItem is only implemented on Linux", ErrTrayUnavailable)
}

func (unsupportedTrayService) Update(TrayConfig) error {
	return fmt.Errorf("%w: StatusNotifierItem is only implemented on Linux", ErrTrayUnavailable)
}

func (unsupportedTrayService) SetMenu(TrayMenu) error { return nil }

func (unsupportedTrayService) Hide() error  { return nil }
func (unsupportedTrayService) Close() error { return nil }
