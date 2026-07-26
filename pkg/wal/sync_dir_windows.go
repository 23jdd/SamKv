//go:build windows

package wal

// 本文件定义 Windows 的 WAL 目录同步边界。
// 已发布文件本身会先 FlushFileBuffers；Go/Windows 不提供可移植的目录 fsync，重命名由 NTFS 日志保证一致性。

func syncWALDirectory(string) error {
	return nil
}
