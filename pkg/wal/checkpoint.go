package wal

// 本文件实现显式 Flush、世代式 Replace、已封存 segment 回收以及幂等关闭。
// Replace/PruneThrough 与追加共享 writeMu；新 segment 同步并发布后才允许删除旧段。

import (
	"errors"
	"os"
	"path/filepath"
)

var (
	// ErrInvalidPrune 表示调用方试图删除当前活动 segment 或更高编号。
	ErrInvalidPrune = errors.New("wal: prune boundary reaches active segment")
)

// Flush 将当前 WAL 内存 buffer 同步刷到活动 segment。
// Checkpoint 前必须先 Flush，避免仍在内存中的 WAL 记录丢失。
// 空缓冲返回 nil；此前记录已经在对应写入路径完成 Sync。
func (wm *WalManger) Flush() error {
	wm.writeMu.Lock()
	defer wm.writeMu.Unlock()
	return wm.flushBufferLocked()
}

// Reset 发布一个新的空 segment，再删除所有旧 segment。
// 只有上层确认全部旧 WAL 状态已进入 SSTable/Manifest 后才能调用，否则会永久丢失恢复信息。
func (wm *WalManger) Reset() error {
	return wm.Replace(nil)
}

// Replace 把 data 写入更高 ID 的新 segment，Sync 并 Rename 发布后才切换活动句柄和删除旧段。
// data 必须是零条或多条完整编码 record；崩溃发生在发布前会保留旧段，发布后但回收前会保留新旧两组。
// Store 的新 Checkpoint 路径使用 Rotate+PruneThrough；Replace 仅保留给兼容调用方。
func (wm *WalManger) Replace(data []byte) error {
	if _, framed := encodedRecordEnds(data); !framed {
		return ErrInvalidRecord
	}

	wm.writeMu.Lock()
	defer wm.writeMu.Unlock()
	if err := wm.flushBufferLocked(); err != nil {
		return err
	}

	nextID := wm.activeWriter.segment.ID + 1
	targetPath := SegmentPath(wm.Dir, nextID)
	tmp, err := os.CreateTemp(wm.Dir, "."+filepath.Base(targetPath)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	published := false
	defer func() {
		_ = tmp.Close()
		if !published {
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
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return err
	}
	published = true
	if err := syncWALDirectory(wm.Dir); err != nil {
		return err
	}

	nextFile, err := os.OpenFile(targetPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	oldFile := wm.activeWriter.file
	wm.activeWriter = &WalWriter{
		file: nextFile,
		segment: Segment{
			ID:   nextID,
			Path: targetPath,
			Size: int64(len(data)),
		},
	}
	wm.activeWriter.records = countSegmentRecords(targetPath)

	closeErr := oldFile.Close()
	_, pruneErr := wm.pruneThroughLocked(nextID - 1)
	return errors.Join(closeErr, pruneErr)
}

// PruneThrough 删除 ID <= maxID 的已封存 segment。
// 调用方必须先持久化引用这些记录的 Manifest；重复调用会忽略已经不存在的旧段。
func (wm *WalManger) PruneThrough(maxID uint64) (int, error) {
	wm.writeMu.Lock()
	defer wm.writeMu.Unlock()
	return wm.pruneThroughLocked(maxID)
}

func (wm *WalManger) pruneThroughLocked(maxID uint64) (int, error) {
	if maxID == 0 {
		return 0, nil
	}
	if maxID >= wm.activeWriter.segment.ID {
		return 0, ErrInvalidPrune
	}
	segments, err := ListSegments(wm.Dir)
	if err != nil {
		return 0, err
	}

	removed := 0
	var removeErr error
	for _, segment := range segments {
		if segment.ID > maxID {
			break
		}
		if err := os.Remove(segment.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			removeErr = errors.Join(removeErr, err)
			continue
		}
		removed++
	}
	if removed > 0 {
		removeErr = errors.Join(removeErr, syncWALDirectory(wm.Dir))
	}
	return removed, removeErr
}

func (wm *WalManger) reopenActiveWriter(path string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	id, ok := ParseSegmentID(path)
	if !ok {
		_ = file.Close()
		return ErrAmbiguousLayout
	}
	wm.activeWriter.file = file
	wm.activeWriter.segment = Segment{ID: id, Path: path, Size: info.Size()}
	wm.activeWriter.records = countSegmentRecords(path)
	return nil
}

func writeFileData(file *os.File, data []byte) error {
	for len(data) > 0 {
		n, err := file.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return os.ErrInvalid
		}
	}
	return nil
}

// Close 停止后台刷盘协程，刷出剩余 buffer，并关闭当前活动 segment。
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
