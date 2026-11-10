//go:build !linux

package desktop

import "fmt"

type unsupportedDialogService struct{}

func newPlatformDialogService() DialogService { return unsupportedDialogService{} }

func (unsupportedDialogService) Capabilities() Capabilities { return Capabilities{} }

func (unsupportedDialogService) Open(DialogOptions, func(DialogResult, error)) (DialogRequest, error) {
	return nil, fmt.Errorf("%w: file dialogs are only implemented on Linux", ErrDialogUnavailable)
}

func (unsupportedDialogService) Save(DialogOptions, func(DialogResult, error)) (DialogRequest, error) {
	return nil, fmt.Errorf("%w: file dialogs are only implemented on Linux", ErrDialogUnavailable)
}

func (unsupportedDialogService) Close() error { return nil }
