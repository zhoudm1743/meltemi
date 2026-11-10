//go:build linux

package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

type trayBusDialContextFunc func(context.Context, string, string) (net.Conn, error)

func connectSessionBus(ctx context.Context) (dbusConnection, error) {
	address, err := sessionBusAddress(ctx)
	if err != nil {
		return nil, err
	}
	return connectSessionBusAddress(ctx, address, (&net.Dialer{}).DialContext)
}

func sessionBusAddress(ctx context.Context) (string, error) {
	if address := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); address != "" && address != "autolaunch:" {
		return address, nil
	}
	runtimeDirectory := fmt.Sprintf("/run/user/%d", os.Getuid())
	busPath := filepath.Join(runtimeDirectory, "bus")
	if _, err := os.Stat(busPath); err == nil {
		return "unix:path=" + dbus.EscapeBusAddressValue(busPath), nil
	}
	if data, err := os.ReadFile(filepath.Join(runtimeDirectory, "dbus-session")); err == nil {
		const prefix = "DBUS_SESSION_BUS_ADDRESS="
		content := string(data)
		if strings.HasPrefix(content, prefix) {
			address := strings.TrimRight(strings.TrimPrefix(content, prefix), "\r\n")
			if address != "" {
				return address, nil
			}
		}
	}
	output, err := exec.CommandContext(ctx, "dbus-launch").CombinedOutput()
	if err != nil {
		return "", trayConnectError(ctx, "discover session bus", err)
	}
	line, _, _ := strings.Cut(string(output), "\n")
	name, address, ok := strings.Cut(line, "=")
	if !ok || name != "DBUS_SESSION_BUS_ADDRESS" || address == "" {
		return "", errors.New("discover session bus: dbus-launch returned no address")
	}
	return address, nil
}

func connectSessionBusAddress(ctx context.Context, address string, dial trayBusDialContextFunc) (dbusConnection, error) {
	var result error
	for _, candidate := range strings.Split(address, ";") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := connectSingleSessionBus(ctx, candidate, dial)
		if err == nil {
			return conn, nil
		}
		result = errors.Join(result, err)
	}
	if result == nil {
		result = errors.New("empty session-bus address")
	}
	return nil, result
}

func connectSingleSessionBus(ctx context.Context, address string, dial trayBusDialContextFunc) (dbusConnection, error) {
	network, target, noncefile, err := trayBusDialTarget(address)
	if err != nil {
		return nil, err
	}
	transport, err := dial(ctx, network, target)
	if err != nil {
		if transport != nil {
			_ = transport.Close()
		}
		return nil, trayConnectError(ctx, "dial session bus", err)
	}
	keepTransport := false
	defer func() {
		if !keepTransport {
			_ = transport.Close()
		}
	}()
	deadlineReached := make(chan struct{})
	stopDeadline := context.AfterFunc(ctx, func() {
		_ = transport.SetDeadline(time.Now())
		close(deadlineReached)
	})
	var stopDeadlineOnce sync.Once
	stopDeadlineWatch := func() {
		stopDeadlineOnce.Do(func() {
			if !stopDeadline() {
				<-deadlineReached
			}
		})
	}
	defer stopDeadlineWatch()
	if deadline, ok := ctx.Deadline(); ok {
		if err := transport.SetDeadline(deadline); err != nil {
			return nil, trayConnectError(ctx, "deadline session bus", err)
		}
	}
	if noncefile != "" {
		nonce, err := os.ReadFile(noncefile)
		if err != nil {
			return nil, fmt.Errorf("read session-bus nonce: %w", err)
		}
		written, err := transport.Write(nonce)
		if err == nil && written != len(nonce) {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			return nil, trayConnectError(ctx, "write session-bus nonce", err)
		}
	}
	conn, err := dbus.NewConn(transport)
	if err != nil {
		return nil, fmt.Errorf("create session-bus connection: %w", err)
	}
	if err := conn.Auth(nil); err != nil {
		_ = conn.Close()
		return nil, trayConnectError(ctx, "authenticate session bus", err)
	}
	if err := conn.Hello(); err != nil {
		_ = conn.Close()
		return nil, trayConnectError(ctx, "session bus Hello", err)
	}
	stopDeadlineWatch()
	if err := transport.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clear session-bus setup deadline: %w", err)
	}
	keepTransport = true
	return conn, nil
}

func trayBusDialTarget(address string) (network, target, noncefile string, err error) {
	transport, encodedKeys, ok := strings.Cut(address, ":")
	if !ok || transport == "" {
		return "", "", "", fmt.Errorf("invalid session-bus address %q", address)
	}
	keys := make(map[string]string)
	for _, entry := range strings.Split(encodedKeys, ",") {
		key, encodedValue, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return "", "", "", fmt.Errorf("invalid session-bus address %q", address)
		}
		value, err := dbus.UnescapeBusAddressValue(encodedValue)
		if err != nil {
			return "", "", "", fmt.Errorf("invalid session-bus address %q: %w", address, err)
		}
		if _, exists := keys[key]; !exists {
			keys[key] = value
		}
	}
	switch transport {
	case "unix":
		path, abstract := keys["path"], keys["abstract"]
		switch {
		case path != "" && abstract == "":
			return "unix", path, "", nil
		case abstract != "" && path == "":
			return "unix", "@" + abstract, "", nil
		default:
			return "", "", "", fmt.Errorf("invalid unix session-bus address %q", address)
		}
	case "tcp", "nonce-tcp":
		network := "tcp"
		switch keys["family"] {
		case "":
		case "ipv4":
			network = "tcp4"
		case "ipv6":
			network = "tcp6"
		default:
			return "", "", "", fmt.Errorf("invalid TCP family in session-bus address %q", address)
		}
		if keys["host"] == "" || keys["port"] == "" {
			return "", "", "", fmt.Errorf("incomplete TCP session-bus address %q", address)
		}
		if transport == "nonce-tcp" && keys["noncefile"] == "" {
			return "", "", "", fmt.Errorf("incomplete nonce-tcp session-bus address %q", address)
		}
		return network, net.JoinHostPort(keys["host"], keys["port"]), keys["noncefile"], nil
	default:
		return "", "", "", fmt.Errorf("unsupported session-bus transport %q", transport)
	}
}

func trayConnectError(ctx context.Context, stage string, cause error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", stage, err)
	}
	return fmt.Errorf("%s: %w", stage, cause)
}
