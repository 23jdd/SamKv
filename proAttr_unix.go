//go:build unix

package main

// 本文件为 Unix 后台进程设置新 session，使子进程脱离当前控制终端。

import (
	"os/exec"
	"syscall"
)

func setProAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // 创建新 session，脱离控制终端
	}
}
