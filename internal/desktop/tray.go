// Package desktop provides platform desktop services without exposing their
// native connection types to the public UI API.
package desktop

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
)

var ErrTrayUnavailable = errors.New("desktop: tray unavailable")

var (
	errTrayClosed      = errors.New("desktop: tray service closed")
	errTrayNotShown    = errors.New("desktop: tray is not shown")
	errInvalidTrayIcon = errors.New("desktop: invalid tray icon")
)

type Capabilities struct {
	Tray       bool
	FileDialog bool
}

type Icon struct {
	Width  int32
	Height int32
	Pixels []byte
}

type TrayMenuModifiers uint8

const (
	TrayMenuModShift TrayMenuModifiers = 1 << iota
	TrayMenuModCtrl
	TrayMenuModAlt
	TrayMenuModSuper
)

type TrayMenuShortcut struct {
	Modifiers TrayMenuModifiers
	Key       rune
}

type TrayMenuItemKind uint8

const (
	TrayMenuAction TrayMenuItemKind = iota
	TrayMenuSeparator
	TrayMenuSubmenu
)

// TrayMenu is an immutable native-menu snapshot. Source values are opaque
// identities used only to retain item IDs across presentation updates.
type TrayMenu struct {
	Source interface{}
	Items  []TrayMenuItem
}

type TrayMenuItem struct {
	Source   interface{}
	Text     string
	Kind     TrayMenuItemKind
	Enabled  bool
	Checked  bool
	Shortcut *TrayMenuShortcut
	Children []TrayMenuItem
	Activate func()
}

type TrayConfig struct {
	Icon    Icon
	Tooltip string
	OnError func(error)
}

type TrayService interface {
	Capabilities() Capabilities
	Show(TrayConfig) error
	Update(TrayConfig) error
	SetMenu(TrayMenu) error
	Hide() error
	Close() error
}

func NewTrayService() TrayService {
	return newPlatformTrayService()
}

func EncodeIcon(icon image.Image) (Icon, error) {
	if nilImage(icon) {
		return Icon{}, fmt.Errorf("%w: nil image", errInvalidTrayIcon)
	}
	bounds := icon.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return Icon{}, fmt.Errorf("%w: empty bounds %v", errInvalidTrayIcon, bounds)
	}
	const maxInt32 = int64(1<<31 - 1)
	if int64(width) > maxInt32 || int64(height) > maxInt32 {
		return Icon{}, fmt.Errorf("%w: dimensions %dx%d exceed D-Bus limits", errInvalidTrayIcon, width, height)
	}
	pixelCount := int64(width) * int64(height)
	if pixelCount > int64(^uint(0)>>1)/4 {
		return Icon{}, fmt.Errorf("%w: dimensions %dx%d overflow", errInvalidTrayIcon, width, height)
	}
	encoded := Icon{
		Width:  int32(width),
		Height: int32(height),
		Pixels: make([]byte, int(pixelCount)*4),
	}
	offset := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixel := color.NRGBAModel.Convert(icon.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			encoded.Pixels[offset+0] = pixel.A
			encoded.Pixels[offset+1] = pixel.R
			encoded.Pixels[offset+2] = pixel.G
			encoded.Pixels[offset+3] = pixel.B
			offset += 4
		}
	}
	return encoded, nil
}

func nilImage(icon image.Image) bool {
	if icon == nil {
		return true
	}
	value := reflect.ValueOf(icon)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func validateTrayConfig(config TrayConfig) error {
	width, height := config.Icon.Width, config.Icon.Height
	if width <= 0 || height <= 0 {
		return fmt.Errorf("%w: dimensions %dx%d", errInvalidTrayIcon, width, height)
	}
	want := int64(width) * int64(height) * 4
	if want > int64(^uint(0)>>1) || len(config.Icon.Pixels) != int(want) {
		return fmt.Errorf("%w: got %d bytes for %dx%d ARGB", errInvalidTrayIcon, len(config.Icon.Pixels), width, height)
	}
	return nil
}

func cloneTrayConfig(config TrayConfig) TrayConfig {
	config.Icon.Pixels = append([]byte(nil), config.Icon.Pixels...)
	return config
}

// deterministicTrayID uses the executable base name and first-shown tray
// configuration, avoiding PID, install-path and per-session name changes.
// Distinct trays with identical initial configurations therefore receive the
// same Id, while a later tray launched with a different initial icon or
// tooltip receives a different Id; this is not per-instance uniqueness.
func deterministicTrayID(config TrayConfig) string {
	application, err := os.Executable()
	if err != nil || application == "" {
		application = os.Args[0]
	}
	application = filepath.Base(application)
	if application == "" || application == "." || application == string(filepath.Separator) {
		application = "meltemi"
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\x00%s\x00%d\x00%d\x00", application, config.Tooltip, config.Icon.Width, config.Icon.Height)
	_, _ = hash.Write(config.Icon.Pixels)
	return fmt.Sprintf("meltemi-%x", hash.Sum(nil))
}
