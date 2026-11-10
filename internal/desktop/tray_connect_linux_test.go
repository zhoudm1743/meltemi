//go:build linux

package desktop

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

type trayConnectResult struct {
	conn dbusConnection
	err  error
}

func waitTrayConnect(t *testing.T, done <-chan trayConnectResult, cleanup func()) trayConnectResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(2 * time.Second):
		cleanup()
		select {
		case result := <-done:
			t.Fatalf("session-bus connect exceeded its context deadline; after cleanup result = %v", result.err)
		case <-time.After(time.Second):
			t.Fatal("session-bus connect remained blocked after cleanup")
		}
		return trayConnectResult{}
	}
}

func TestTrayConnectBoundsStalledDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	started := make(chan struct{})
	done := make(chan trayConnectResult, 1)
	go func() {
		conn, err := connectSessionBusAddress(ctx, "unix:path=/tmp/stalled.sock", func(ctx context.Context, network, address string) (net.Conn, error) {
			close(started)
			if network != "unix" || address != "/tmp/stalled.sock" {
				return nil, fmt.Errorf("dial target = %s %q", network, address)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return nil, errors.New("released by test cleanup")
			}
		})
		done <- trayConnectResult{conn: conn, err: err}
	}()
	<-started
	result := waitTrayConnect(t, done, func() { close(release) })
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Errorf("stalled dial error = %v, want context deadline exceeded", result.err)
	}
	if result.conn != nil {
		t.Error("stalled dial returned a live connection")
	}
}

func TestTrayConnectClosesTransportAfterStalledAuth(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 256)
		for {
			if _, err := server.Read(buffer); err != nil {
				serverDone <- err
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan trayConnectResult, 1)
	go func() {
		conn, err := connectSessionBusAddress(ctx, "unix:path=/tmp/auth-stall.sock", func(context.Context, string, string) (net.Conn, error) {
			return client, nil
		})
		done <- trayConnectResult{conn: conn, err: err}
	}()
	result := waitTrayConnect(t, done, func() { _ = client.Close() })
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Errorf("stalled auth error = %v, want context deadline exceeded", result.err)
	}
	if result.conn != nil {
		t.Error("stalled auth returned a live connection")
	}
	select {
	case err := <-serverDone:
		if err == nil {
			t.Error("server observed a clean read despite the failed auth transport not being closed")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled auth left the client transport open")
	}
}

func serveStalledTrayHello(server net.Conn, helloSeen chan<- struct{}, done chan<- error) {
	reader := bufio.NewReader(server)
	fail := func(stage string, err error) {
		if err == nil {
			err = fmt.Errorf("unexpected auth exchange at %s", stage)
		}
		done <- fmt.Errorf("%s: %w", stage, err)
	}
	null, err := reader.ReadByte()
	if err != nil || null != 0 {
		fail("null byte", err)
		return
	}
	line, err := reader.ReadString('\n')
	if err != nil || line != "AUTH\r\n" {
		fail("initial AUTH", err)
		return
	}
	if _, err := server.Write([]byte("REJECTED EXTERNAL\r\n")); err != nil {
		fail("REJECTED", err)
		return
	}
	line, err = reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "AUTH EXTERNAL") {
		fail("EXTERNAL", err)
		return
	}
	if _, err := server.Write([]byte("OK 0123456789abcdef0123456789abcdef\r\n")); err != nil {
		fail("OK", err)
		return
	}
	line, err = reader.ReadString('\n')
	if err != nil || line != "BEGIN\r\n" {
		fail("BEGIN", err)
		return
	}
	if _, err := reader.ReadByte(); err != nil {
		fail("Hello", err)
		return
	}
	close(helloSeen)
	buffer := make([]byte, 256)
	for {
		if _, err := reader.Read(buffer); err != nil {
			done <- err
			return
		}
	}
}

func TestTrayConnectClosesTransportAfterStalledHello(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	helloSeen := make(chan struct{})
	serverDone := make(chan error, 1)
	go serveStalledTrayHello(server, helloSeen, serverDone)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan trayConnectResult, 1)
	go func() {
		conn, err := connectSessionBusAddress(ctx, "unix:path=/tmp/hello-stall.sock", func(context.Context, string, string) (net.Conn, error) {
			return client, nil
		})
		done <- trayConnectResult{conn: conn, err: err}
	}()
	result := waitTrayConnect(t, done, func() { _ = client.Close() })
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Errorf("stalled Hello error = %v, want context deadline exceeded", result.err)
	}
	if result.conn != nil {
		t.Error("stalled Hello returned a live connection")
	}
	select {
	case <-helloSeen:
	case <-time.After(time.Second):
		t.Fatal("connection did not reach Hello")
	}
	select {
	case err := <-serverDone:
		if err == nil {
			t.Error("server observed a clean read despite the failed Hello transport not being closed")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled Hello left the client transport open")
	}
}

func TestTrayConnectClosesPartialTransportAfterDialFailure(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		_, err := server.Read(make([]byte, 1))
		serverDone <- err
	}()
	dialErr := errors.New("partial dial failed")
	conn, err := connectSessionBusAddress(context.Background(), "unix:path=/tmp/partial-dial.sock", func(context.Context, string, string) (net.Conn, error) {
		return client, dialErr
	})
	if conn != nil || !errors.Is(err, dialErr) {
		t.Errorf("partial dial result = %v, %v, want nil connection and the dial error", conn, err)
	}
	select {
	case err := <-serverDone:
		if err == nil {
			t.Error("partial dial transport remained readable instead of being closed")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("partial dial failure left its transport open")
	}
}
