package xim

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	fcitxPortalService    = "org.freedesktop.portal.Fcitx"
	fcitxInputMethodPath  = dbus.ObjectPath("/org/freedesktop/portal/inputmethod")
	inputMethodInterface  = "org.fcitx.Fcitx.InputMethod1"
	inputContextInterface = "org.fcitx.Fcitx.InputContext1"
	operationTimeout      = 250 * time.Millisecond
	cleanupTimeout        = 250 * time.Millisecond
)

type dbusConnection interface {
	Object(dest string, path dbus.ObjectPath) dbus.BusObject
	BusObject() dbus.BusObject
	AddMatchSignalContext(ctx context.Context, options ...dbus.MatchOption) error
	RemoveMatchSignalContext(ctx context.Context, options ...dbus.MatchOption) error
	Signal(ch chan<- *dbus.Signal)
	RemoveSignal(ch chan<- *dbus.Signal)
	Close() error
}

// NewClient creates a persistent private session-bus connection and Fcitx
// input context. The context controls the connection lifetime; each D-Bus
// operation also has an independent deadline.
func NewClient(ctx context.Context, program, display string) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("xim: connect session bus: %w", err)
	}
	return newClientWithConn(ctx, conn, program, display)
}

func operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, operationTimeout)
}

func newClientWithConn(ctx context.Context, conn dbusConnection, program, display string) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	client := &Client{
		ctx:      ctx,
		conn:     conn,
		incoming: make(chan *dbus.Signal, 64),
		signals:  make(chan *dbus.Signal, 64),
		done:     make(chan struct{}),
	}
	conn.Signal(client.incoming)
	inputMethod := conn.Object(fcitxPortalService, fcitxInputMethodPath)
	if len(display) < len("x11:") || display[:len("x11:")] != "x11:" {
		display = "x11:" + display
	}
	arguments := []struct {
		Name  string
		Value string
	}{{"program", program}, {"display", display}}
	var path dbus.ObjectPath
	var token []byte
	createCtx, cancelCreate := operationContext(ctx)
	err := inputMethod.CallWithContext(createCtx, inputMethodInterface+".CreateInputContext", 0, arguments).Store(&path, &token)
	cancelCreate()
	if err != nil {
		conn.RemoveSignal(client.incoming)
		var closeErr error
		if err := conn.Close(); err != nil {
			closeErr = fmt.Errorf("xim: close session bus: %w", err)
		}
		return nil, errors.Join(fmt.Errorf("xim: create input context: %w", err), closeErr)
	}
	client.inputContext = conn.Object(fcitxPortalService, path)

	ownerCtx, cancelOwner := operationContext(ctx)
	err = conn.BusObject().CallWithContext(ownerCtx, "org.freedesktop.DBus.GetNameOwner", 0, fcitxPortalService).Store(&client.fcitxSender)
	cancelOwner()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("xim: resolve Fcitx service owner: %w", err), client.Close())
	}
	if client.fcitxSender == "" {
		return nil, errors.Join(errors.New("xim: resolve Fcitx service owner: empty unique name"), client.Close())
	}

	capabilities := uint64(1<<1 | 1<<4)
	capabilityCtx, cancelCapability := operationContext(ctx)
	err = client.inputContext.CallWithContext(capabilityCtx, inputContextInterface+".SetCapability", 0, capabilities).Err
	cancelCapability()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("xim: set input context capabilities: %w", err), client.Close())
	}
	matchCtx, cancelMatch := operationContext(ctx)
	err = conn.AddMatchSignalContext(matchCtx, inputContextMatch(path)...)
	cancelMatch()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("xim: subscribe input context: %w", err), client.Close())
	}
	client.forwardWG.Add(1)
	go client.forwardSignals()
	return client, nil
}

func inputContextMatch(path dbus.ObjectPath) []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchInterface(inputContextInterface),
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchSender(fcitxPortalService),
	}
}

func (c *Client) SetFocus(focused bool) error {
	if c == nil || c.closed || c.inputContext == nil {
		return errClientClosed
	}
	c.dispatchSignals()
	if !focused {
		c.focused = false
		c.composition.cancel()
		focusCtx, cancelFocus := operationContext(c.ctx)
		err := c.inputContext.CallWithContext(focusCtx, inputContextInterface+".FocusOut", 0).Err
		cancelFocus()
		c.dispatchSignals()
		if err != nil {
			return fmt.Errorf("xim: focus out: %w", err)
		}
		return nil
	}

	c.focused = true
	focusCtx, cancelFocus := operationContext(c.ctx)
	err := c.inputContext.CallWithContext(focusCtx, inputContextInterface+".FocusIn", 0).Err
	cancelFocus()
	if err != nil {
		c.focused = false
		c.composition.cancel()
	}
	c.dispatchSignals()
	if err != nil {
		return fmt.Errorf("xim: focus in: %w", err)
	}
	return nil
}

func (c *Client) SetSpot(x, y, width, height int) error {
	if c == nil || c.closed || c.inputContext == nil {
		return errClientClosed
	}
	c.dispatchSignals()
	spotCtx, cancelSpot := operationContext(c.ctx)
	err := c.inputContext.CallWithContext(spotCtx, inputContextInterface+".SetCursorRect", 0,
		int32(x), int32(y), int32(width), int32(height)).Err
	cancelSpot()
	c.dispatchSignals()
	if err != nil {
		return fmt.Errorf("xim: set cursor rectangle: %w", err)
	}
	return nil
}

func (c *Client) ProcessKeyEvent(keyval, keycode, state uint32, isRelease bool, time uint32) (bool, error) {
	if c == nil || c.closed || c.inputContext == nil {
		return false, errClientClosed
	}
	c.dispatchSignals()
	var handled bool
	keyCtx, cancelKey := operationContext(c.ctx)
	err := c.inputContext.CallWithContext(keyCtx, inputContextInterface+".ProcessKeyEvent", 0,
		keyval, keycode, state, isRelease, time).Store(&handled)
	cancelKey()
	c.dispatchSignals()
	if err != nil {
		return false, fmt.Errorf("xim: process key event: %w", err)
	}
	return handled, nil
}

// Close stops signal forwarding, cancels local composition, unsubscribes
// signals, destroys the remote input context, and finally closes the connection.
func (c *Client) Close() (result error) {
	if c == nil || c.closed {
		return nil
	}
	c.closed = true
	c.focused = false
	c.composition.cancel()
	c.eventHandler = nil
	c.SetWakeHandler(nil)
	c.stopSignalForwarding()

	conn := c.conn
	inputContext := c.inputContext
	c.conn = nil
	c.inputContext = nil
	if conn == nil {
		return nil
	}
	defer func() {
		if c.incoming != nil {
			conn.RemoveSignal(c.incoming)
		}
		if err := conn.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("xim: close session bus: %w", err))
		}
	}()

	if inputContext != nil {
		unsubscribeCtx, cancelUnsubscribe := context.WithTimeout(context.Background(), cleanupTimeout)
		err := conn.RemoveMatchSignalContext(unsubscribeCtx, inputContextMatch(inputContext.Path())...)
		cancelUnsubscribe()
		if err != nil {
			result = errors.Join(result, fmt.Errorf("xim: unsubscribe input context: %w", err))
		}

		destroyCtx, cancelDestroy := context.WithTimeout(context.Background(), cleanupTimeout)
		err = inputContext.CallWithContext(destroyCtx, inputContextInterface+".DestroyIC", 0).Err
		cancelDestroy()
		if err != nil {
			result = errors.Join(result, fmt.Errorf("xim: destroy input context: %w", err))
		}
	}
	return result
}
