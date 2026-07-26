//go:build windows

package main

// 本文件为 Windows 后台进程设置独立进程组、脱离控制台并隐藏窗口。

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
