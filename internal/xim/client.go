// Package xim bridges Fcitx D-Bus preedit and committed text.
package xim

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/zhoudm1743/meltemi/internal/driver"
)

var errClientClosed = errors.New("xim: client is closed")

// Client owns a private D-Bus connection and one Fcitx input context. Methods
// and event callbacks must run on the UI thread. A forwarding goroutine only
// queues raw signals and requests a wake; UI methods dispatch the queued signals.
type Client struct {
	ctx          context.Context
	conn         dbusConnection
	inputContext dbus.BusObject
	fcitxSender  string
	incoming     chan *dbus.Signal
	signals      chan *dbus.Signal
	done         chan struct{}
	forwardWG    sync.WaitGroup
	wakeMu       sync.RWMutex
	wakeHandler  func()
	composition  composition
	eventHandler func(driver.Event)
	focused      bool
	closed       bool
	dispatching  bool
}

// SetWakeHandler sets the thread-safe wake hook used after a raw signal is
// queued. The hook must not dispatch signals or emit UI events.
func (c *Client) SetWakeHandler(handler func()) {
	if c == nil {
		return
	}
	c.wakeMu.Lock()
	c.wakeHandler = handler
	c.wakeMu.Unlock()
}

func (c *Client) forwardSignals() {
	defer c.forwardWG.Done()
	for {
		select {
		case <-c.done:
			return
		case signal, ok := <-c.incoming:
			if !ok {
				return
			}
			select {
			case c.signals <- signal:
			case <-c.done:
				return
			}
			c.wakeMu.RLock()
			handler := c.wakeHandler
			c.wakeMu.RUnlock()
			if handler != nil {
				handler()
			}
		}
	}
}

func (c *Client) stopSignalForwarding() {
	if c.done != nil {
		close(c.done)
	}
	c.forwardWG.Wait()
}

// SetEventHandler sets the single recipient of committed EventRune events.
func (c *Client) SetEventHandler(handler func(driver.Event)) {
	if c == nil || c.closed {
		return
	}
	c.eventHandler = handler
}

// Preedit returns the current state after dispatching pending Fcitx signals on
// the calling UI thread.
func (c *Client) Preedit() Preedit {
	if c == nil {
		return Preedit{}
	}
	c.dispatchSignals()
	return c.composition.snapshot()
}

type formattedPreeditPart struct {
	Text   string
	Format int32
}

func (c *Client) dispatchSignals() {
	if c == nil || c.closed || c.dispatching || c.signals == nil {
		return
	}
	c.dispatching = true
	defer func() {
		c.dispatching = false
	}()
	for {
		select {
		case signal, ok := <-c.signals:
			if !ok {
				return
			}
			c.handleSignal(signal)
		default:
			return
		}
	}
}

func (c *Client) handleSignal(signal *dbus.Signal) {
	if signal == nil || c.closed || !c.focused || c.inputContext == nil ||
		signal.Path != c.inputContext.Path() || signal.Sender != c.fcitxSender {
		return
	}
	switch signal.Name {
	case inputContextInterface + ".UpdateFormattedPreedit":
		var parts []formattedPreeditPart
		var caret int32
		if err := dbus.Store(signal.Body, &parts, &caret); err != nil {
			return
		}
		var text strings.Builder
		for _, part := range parts {
			text.WriteString(part.Text)
		}
		if text.Len() == 0 {
			c.composition.cancel()
			return
		}
		value := text.String()
		byteCaret := int(caret)
		if byteCaret < 0 {
			byteCaret = 0
		} else if byteCaret > len(value) {
			byteCaret = len(value)
		}
		runeCaret := 0
		for offset := range value {
			if offset >= byteCaret {
				break
			}
			runeCaret++
		}
		c.composition.draw(value, runeCaret)
	case inputContextInterface + ".CommitString":
		var text string
		if err := dbus.Store(signal.Body, &text); err != nil || text == "" {
			return
		}
		c.composition.commit()
		for _, r := range text {
			if c.closed {
				return
			}
			if c.eventHandler != nil {
				c.eventHandler(driver.Event{Kind: driver.EventRune, Rune: r})
			}
		}
	}
}
