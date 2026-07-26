package store

// 本文件负责回放 WAL：完整记录必须通过校验，只有进程崩溃留下的不完整文件尾可以被忽略或截断。

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/23jdd/SamKv/pkg/wal"
)

// Recover 从 reader 顺序回放 WAL 记录。
// 文件尾部只有半条记录时会忽略尾部，完整记录损坏则返回错误。
// reader 版本无法修复原始介质；需要继续追加 WAL 时应使用 RecoverWALFile。
func Recover(reader io.Reader, mem *MemTable) error {
	for {
		record, err := wal.ReadRecord(reader)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := applyWALRecord(mem, record); err != nil {
			return err
		}
	}
}

// RecoverWALDirectory 按 segment ID 顺序回放目录中的 WAL。
// 新格式默认跳过长度完整的坏 record 并修复末段半条尾记录；没有 segment 时兼容旧 wal.log。
func RecoverWALDirectory(dir string, mem *MemTable) (wal.RecoveryReport, error) {
	segments, err := wal.ListSegments(dir)
	if err != nil {
		return wal.RecoveryReport{}, err
	}
	if len(segments) == 0 {
		err := RecoverWALFile(filepath.Join(dir, "wal.log"), mem)
		return wal.RecoveryReport{Records: mem.Len()}, err
	}
	return wal.ReplaySegments(dir, wal.DefaultRecoveryOptions(), func(record *wal.Record) error {
		return applyWALRecord(mem, record)
	})
}

// RecoverWALFile 回放单个旧版 WAL 文件，并截断崩溃留下的不完整尾部。
// 文件不存在视为空 WAL；新代码应使用 RecoverWALDirectory。
func RecoverWALFile(path string, mem *MemTable) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0644)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	var lastGoodOffset int64
	for {
		record, err := wal.ReadRecord(file)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return file.Truncate(lastGoodOffset)
		}
		if err != nil {
			return err
		}
		if err := applyWALRecord(mem, record); err != nil {
			return err
		}

		lastGoodOffset, err = file.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
	}
}

func applyWALRecord(mem *MemTable, record *wal.Record) error {
	switch record.Type {
	case wal.RecordPut:
		return mem.Put(string(record.Key), string(record.Value))
	case wal.RecordDelete:
		return mem.Delete(string(record.Key))
	default:
		return wal.ErrInvalidRecord
	}
}
