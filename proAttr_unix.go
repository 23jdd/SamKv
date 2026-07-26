//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

func setProAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // 创建新 session，脱离控制终端
	}
}
