//go:build linux

package ui

import (
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestTrayQuitFinishesWhenSessionConnectStalls(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "stalled-session-bus.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on fake session bus: %v", err)
	}
	defer listener.Close()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+dbus.EscapeBusAddressValue(socket))

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	app, _ := newUITestApp(newUITestBackend(), &uiTestLog{})
	tray, err := NewTray(testTrayImage(), "stalled connect")
	if err != nil {
		t.Fatalf("NewTray: %v", err)
	}
	showDone := make(chan error, 1)
	go func() { showDone <- app.ShowTray(tray) }()

	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
	case err := <-acceptErr:
		t.Fatalf("accept fake session-bus connection: %v", err)
	case <-time.After(time.Second):
		t.Fatal("ShowTray did not dial the fake session bus")
	}
	defer serverConn.Close()
	serverClosed := make(chan struct{})
	go func() {
		defer close(serverClosed)
		buffer := make([]byte, 256)
		for {
			if _, err := serverConn.Read(buffer); err != nil {
				return
			}
		}
	}()

	quitDone := make(chan struct{})
	go func() {
		app.Quit()
		close(quitDone)
	}()
	select {
	case <-quitDone:
	case <-time.After(4 * time.Second):
		_ = serverConn.Close()
		select {
		case <-showDone:
		case <-time.After(time.Second):
			t.Error("ShowTray remained blocked after releasing the stalled transport")
		}
		select {
		case <-quitDone:
		case <-time.After(time.Second):
			t.Error("Quit remained blocked after releasing the stalled transport")
		}
		t.Fatal("Quit did not finish within the bounded session-connect timeout")
	}
	select {
	case err := <-showDone:
		if !errors.Is(err, ErrTrayUnavailable) {
			t.Errorf("ShowTray error = %v, want ErrTrayUnavailable", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ShowTray did not return after Quit finished")
	}
	select {
	case <-serverClosed:
	case <-time.After(time.Second):
		t.Fatal("failed session-bus establishment left the transport open")
	}
}
