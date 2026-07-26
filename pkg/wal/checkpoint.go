package wal

// 本文件实现显式 Flush、Checkpoint 后的 WAL 重写以及幂等关闭。
// Replace/Reset 与追加共享 writeMu，因此不会发布只写了一半的新日志文件。

import (
	"errors"
	"os"
	"path/filepath"
)

// Flush 将当前 WAL 内存 buffer 同步刷到 wal.log。
// Checkpoint 前必须先 Flush，避免仍在内存中的 WAL 记录丢失。
// 空缓冲返回 nil；此前记录已经在对应写入路径完成 Sync。
func (wm *WalManger) Flush() error {
	wm.writeMu.Lock()
	defer wm.writeMu.Unlock()
	return wm.flushBufferLocked()
}

// Reset 清空当前 wal.log，并重新打开一个可继续追加的新文件。
// Windows 的追加句柄不能直接 Truncate，因此必须在 writeMu 保护下关闭后重建。
// 只有上层确认全部 WAL 状态已进入 SSTable 后才能调用，否则会永久丢失恢复信息。
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

// Replace 用 data 原子替换 wal.log 的持久化内容。
// Store 在刷出 Immutable MemTable 后用它保留仍在内存中的记录，避免 WAL 无限增长。
// data 必须是零条或多条完整编码记录；函数不验证内容，传入任意字节会使之后恢复失败。
func (wm *WalManger) Replace(data []byte) error {
	wm.writeMu.Lock()
	defer wm.writeMu.Unlock()

	if err := wm.flushBufferLocked(); err != nil {
		return err
	}

	targetPath := filepath.Join(wm.Dir, "wal.log")
	tmpPath := targetPath + ".tmp"
	backupPath := targetPath + ".bak"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	tmpOK := false
	defer func() {
		_ = tmp.Close()
		if !tmpOK {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := writeFileData(tmp, data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := wm.activeWriter.file.Close(); err != nil {
		return err
	}

	publishErr := os.Rename(tmpPath, targetPath)
	if publishErr != nil {
		_ = os.Remove(backupPath)
		if err := os.Rename(targetPath, backupPath); err != nil {
			_ = wm.reopenActiveWriter(targetPath)
			return errors.Join(publishErr, err)
		}
		if err := os.Rename(tmpPath, targetPath); err != nil {
			_ = os.Rename(backupPath, targetPath)
			_ = wm.reopenActiveWriter(targetPath)
			return err
		}
	}
	tmpOK = true

	if err := wm.reopenActiveWriter(targetPath); err != nil {
		return err
	}
	_ = os.Remove(backupPath)
	return nil
}

func (wm *WalManger) reopenActiveWriter(path string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	wm.activeWriter.file = file
	return nil
}

func writeFileData(file *os.File, data []byte) error {
	for len(data) > 0 {
		n, err := file.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return os.ErrInvalid
		}
		data = data[n:]
	}
	return nil
}

// Close 停止后台刷盘协程，刷出剩余 buffer，并关闭 wal.log。
// Close 可重复调用并返回第一次关闭结果；开始关闭后新的追加返回 os.ErrClosed。
func (wm *WalManger) Close() error {
	var closeErr error
	wm.closeOnce.Do(func() {
		wm.bufmu.Lock()
		wm.closed = true
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
