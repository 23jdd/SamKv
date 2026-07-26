package main

// 本文件实现 start/stop/status 后台进程管理，以及 PID/日志文件路径。
// PID 文件仅是辅助记录而非内核锁；异常退出可能留下陈旧 PID，Windows 状态检查尤其依赖该文件。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

const (
	daemonEnv   = "_IS_DAEMON"
	pidFileName = "SamKv.pid"
)

// startDaemon 重新执行当前二进制并把输出追加到平台日志文件。
// 成功后父进程调用 os.Exit(0)，因此只能从命令入口调用，不能作为可回收控制流的库函数使用。
func startDaemon(args ...string) error {
	if isRunning() {
		return fmt.Errorf("daemon already running")
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)

	// 日志文件
	logFile := getLogFile()
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	// 重新执行自身
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), daemonEnv+"=1")
	cmd.Stdin = nil
	cmd.Stdout = f
	cmd.Stderr = f
	// 平台特定：脱离父进程/终端
	setProAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork failed: %w", err)
	}

	// 保存 PID
	savePID(cmd.Process.Pid)
	fmt.Println("Command", cmd)
	fmt.Printf("Daemon started, PID: %d\n", cmd.Process.Pid)
	fmt.Printf("Log file: %s\n", logFile)

	// 父进程立即退出
	os.Exit(0)
	return nil
}

// stopDaemon 根据 PID 文件发送终止信号；Windows 使用强制 Kill，Unix 使用 SIGTERM。
// PID 可能已被操作系统复用，生产部署更适合使用 systemd、Windows Service 或容器编排器管理进程。
func stopDaemon() error {
	pid, err := readPID()
	if err != nil {
		return fmt.Errorf("daemon not running")
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(getPIDFile())
		return fmt.Errorf("process not found")
	}

	switch runtime.GOOS {
	case "windows":
		// Windows 没有 SIGTERM，直接 Kill
		err = proc.Kill()
	default:
		err = proc.Signal(syscall.SIGTERM)
	}

	if err != nil {
		return fmt.Errorf("send signal: %w", err)
	}

	os.Remove(getPIDFile())
	fmt.Println("Daemon stoped", "PID: ", pid)
	return nil
}

// status 状态检查
func status() {
	pid, err := readPID()
	if err != nil {
		fmt.Println("Daemon is not running")
		return
	}

	// Unix: signal 0 检测进程是否存在
	// Windows: FindProcess 总是成功，这里简化处理
	if runtime.GOOS != "windows" {
		proc, _ := os.FindProcess(pid)
		if proc != nil {
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				fmt.Println("Daemon is not running (stale PID file)")
				os.Remove(getPIDFile())
				return
			}
		}
	}

	fmt.Printf("Daemon is running, PID: %d\n", pid)
	fmt.Printf("Log file: %s\n", getLogFile())
}

// 辅助函数
func isRunning() bool {
	_, err := readPID()
	return err == nil
}

func savePID(pid int) {
	path := getPIDFile()
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

func readPID() (int, error) {
	data, err := os.ReadFile(getPIDFile())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(data))
}

func getPIDFile() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), pidFileName)
	}
	// Unix: 优先 /var/run，没权限则用 /tmp
	if _, err := os.Stat("/var/run"); err == nil {
		return "/var/run/" + pidFileName
	}
	return filepath.Join(os.TempDir(), pidFileName)
}

func getLogFile() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "Samkv.log")
	}
	return "/var/log/Samkv.log"
}

func logf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	f, err := os.OpenFile(getLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
}
