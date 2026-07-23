package store

import (
	"io"
	"github.com/23jdd/SamKv/pkg/wal"
	"errors"
)
func Recover(r io.Reader, mem *MemTable) error {
	for {
		record, err := wal.ReadRecord(r)

		if errors.Is(err, io.EOF) {
			return nil
		}

		if errors.Is(err, io.ErrUnexpectedEOF) {
			// 最后一条记录只写了一部分。
			// 通常可以忽略尾部。
			return nil
		}

		if err != nil {
			return err
		}

		switch record.Type {
		case wal.RecordPut:
			mem.Put(string(record.Key), string(record.Value))

		case wal.RecordDelete:
			mem.Delete(string(record.Key))
		}
	}
}