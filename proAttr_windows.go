//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const (
	DETACHED_PROCESS = 0x00000008
	CREATE_NO_WINDOW = 0x08000000
)

func setProAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP |
			DETACHED_PROCESS |
			CREATE_NO_WINDOW,
	}
}
