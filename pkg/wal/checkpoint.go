package wal

import (
	"errors"
	"os"
	"path/filepath"
)

// Flush 将当前 WAL 内存 buffer 同步刷到 wal.log。
// Checkpoint 前必须先 Flush，避免仍在内存中的 WAL 记录丢失。
func (wm *WalManger) Flush() error {
	wm.writeMu.Lock()
	defer wm.writeMu.Unlock()
	return wm.flushBufferLocked()
}

// Reset 清空当前 wal.log，并重新打开一个可继续追加的新文件。
// Windows 的追加句柄不能直接 Truncate，因此必须在 writeMu 保护下关闭后重建。
func (wm *WalManger) Reset() error {
	wm.writeMu.Lock()
	defer wm.writeMu.Unlock()

	if err := wm.flushBufferLocked(); err != nil {
		return err
	}
	if err := wm.activeWriter.file.Close(); err != nil {
		return err
	}

	path := filepath.Join(wm.Dir, "wal.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	wm.activeWriter.file = file
	return file.Sync()
}

// Close 停止后台刷盘协程，刷出剩余 buffer，并关闭 wal.log。
func (wm *WalManger) Close() error {
	var closeErr error
	wm.closeOnce.Do(func() {
		wm.bufmu.Lock()
		wm.closed = true
		wm.flushCond.Broadcast()
		wm.bufmu.Unlock()

		close(wm.done)
		wm.backgroundWG.Wait()

		wm.writeMu.Lock()
		defer wm.writeMu.Unlock()

		if err := wm.flushBufferLocked(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
		if err := wm.activeWriter.file.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	})
	return closeErr
}
