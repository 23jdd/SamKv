//go:build windows

package store

// 本文件在 Windows 上使用 LockFileEx 非阻塞锁定 LOCK 文件的固定字节区域。

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockRegionOffset 避开 LOCK 文件中用于人工查看的 pid 文本。
const lockRegionOffset = 1 << 30

func tryLockFile(file *os.File) error {
	overlapped := windows.Overlapped{Offset: lockRegionOffset}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return errors.Join(ErrDataDirLocked, err)
	}
	return err
}

func unlockFile(file *os.File) error {
	overlapped := windows.Overlapped{Offset: lockRegionOffset}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
