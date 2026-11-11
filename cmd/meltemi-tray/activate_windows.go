//go:build windows

package main

import "github.com/zhoudm1743/meltemi/internal/desktop"

func activateRestore() error {
	return desktop.ActivateWindowsTrayItem("Restore window")
}
