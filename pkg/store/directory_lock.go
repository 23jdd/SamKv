package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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
