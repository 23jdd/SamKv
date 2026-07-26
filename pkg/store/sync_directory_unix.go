//go:build unix

package store

// 本文件在 Unix 上同步数据目录，持久化 Manifest/SSTable 的创建、重命名和删除目录项。

import "os"

func syncStoreDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
