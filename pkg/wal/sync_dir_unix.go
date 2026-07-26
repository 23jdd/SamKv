//go:build unix

package wal

// 本文件在 Unix 上通过同步目录句柄持久化 segment 创建、重命名和删除产生的目录项。

import "os"

func syncWALDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
