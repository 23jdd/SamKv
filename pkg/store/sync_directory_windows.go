//go:build windows

package store

// 本文件定义 Windows 数据目录同步边界；文件内容先 FlushFileBuffers，目录项由 NTFS 日志保证一致性。

func syncStoreDirectory(string) error {
	return nil
}
