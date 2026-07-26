package store

// 本文件管理数据目录的 LOCK 文件，防止同一台机器上的多个进程并发写入同一 Store。
// 锁依赖操作系统文件锁语义；网络文件系统是否提供可靠互斥由其挂载和服务器实现决定。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrDataDirLocked 表示数据目录已被另一个存活的 Store 实例持有。
// LOCK 文件本身可以长期存在，真正的所有权由内核锁而不是文件是否存在决定。
var ErrDataDirLocked = errors.New("store: data directory is locked")

// directoryLock 在 Store 生命周期内持有数据目录的进程级排他锁。
type directoryLock struct {
	file *os.File
}

func acquireDirectoryLock(dir string) (*directoryLock, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "LOCK"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := tryLockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	lock := &directoryLock{file: file}
	if err := lock.writeOwner(); err != nil {
		_ = lock.release()
		return nil, err
	}
	return lock, nil
}

func (lock *directoryLock) writeOwner() error {
	if err := lock.file.Truncate(0); err != nil {
		return err
	}
	if _, err := lock.file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(lock.file, "pid=%d\n", os.Getpid()); err != nil {
		return err
	}
	return lock.file.Sync()
}

func (lock *directoryLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := errors.Join(unlockFile(lock.file), lock.file.Close())
	lock.file = nil
	return err
}
