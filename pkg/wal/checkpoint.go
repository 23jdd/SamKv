package wal

import (
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

// Reset 清空当前 wal.log，并重新打开一个可继续追加的新日志文件。
// 只有在 MemTable 已经成功写成 SSTable 之后，才能调用 Reset。
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
	return nil
}

// Close 停止后台刷盘 goroutine，刷出剩余 buffer，并关闭 wal.log。
func (wm *WalManger) Close() error {
	var closeErr error
	wm.closeOnce.Do(func() {
		wm.bufmu.Lock()
		wm.closed = true
		wm.flushCond.Broadcast()
		wm.bufmu.Unlock()

		close(wm.done)

		wm.writeMu.Lock()
		defer wm.writeMu.Unlock()

		if err := wm.flushBufferLocked(); err != nil {
			closeErr = err
		}
		if err := wm.activeWriter.file.Close(); closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}
