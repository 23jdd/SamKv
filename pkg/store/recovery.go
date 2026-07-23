package store

import (
	"errors"
	"io"
	"os"

	"github.com/23jdd/SamKv/pkg/wal"
)

// Recover 从 reader 顺序回放 WAL 记录。
// 文件尾部只有半条记录时会忽略尾部，完整记录损坏则返回错误。
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

// RecoverWALFile 回放 WAL 文件，并截断崩溃留下的不完整尾部。
// 截断很重要，否则新记录追加到半条记录后面会让后续恢复永远失败。
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
