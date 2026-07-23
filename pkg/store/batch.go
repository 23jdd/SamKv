package store

import (
	"errors"

	"github.com/23jdd/SamKv/pkg/wal"
)

var ErrInvalidBatch = errors.New("store: invalid batch")

type BatchOperationType uint8

const (
	BatchPut BatchOperationType = iota + 1
	BatchDelete
)

// BatchOperation 是批量写中的单个 Put 或 Delete。
type BatchOperation struct {
	Type  BatchOperationType
	Key   string
	Value string
}

// Batch 收集一组按顺序执行的写操作。
type Batch struct {
	operations []BatchOperation
}

func NewBatch() *Batch {
	return &Batch{}
}

func (batch *Batch) Put(key, value string) *Batch {
	batch.operations = append(batch.operations, BatchOperation{Type: BatchPut, Key: key, Value: value})
	return batch
}

func (batch *Batch) Delete(key string) *Batch {
	batch.operations = append(batch.operations, BatchOperation{Type: BatchDelete, Key: key})
	return batch
}

func (batch *Batch) Len() int {
	if batch == nil {
		return 0
	}
	return len(batch.operations)
}

// WriteBatch 将整批 WAL 记录一次追加到缓冲区，再按顺序更新 MemTable。
// 它减少锁竞争和 WAL 提交次数；恢复时仍按单条记录顺序重放。
func (st *StoreManger) WriteBatch(batch *Batch) error {
	if batch == nil || len(batch.operations) == 0 {
		return nil
	}

	walRecords := make([]*wal.Record, 0, len(batch.operations))
	var walData []byte
	for _, operation := range batch.operations {
		var record *wal.Record
		switch operation.Type {
		case BatchPut:
			record = wal.PutRecord([]byte(operation.Key), []byte(operation.Value))
		case BatchDelete:
			record = wal.DeleteRecord([]byte(operation.Key))
		default:
			return ErrInvalidBatch
		}
		encoded, err := record.Encode()
		if err != nil {
			return errors.Join(ErrInvalidBatch, err)
		}
		walRecords = append(walRecords, record)
		walData = append(walData, encoded...)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.checkWritableLocked(); err != nil {
		return err
	}
	if err := st.wm.AppendLog(walData); err != nil {
		return err
	}

	for i, operation := range batch.operations {
		switch walRecords[i].Type {
		case wal.RecordPut:
			if err := st.mem.Put(operation.Key, operation.Value); err != nil {
				return err
			}
		case wal.RecordDelete:
			if err := st.mem.Delete(operation.Key); err != nil {
				return err
			}
		}
	}
	st.stats.writeOperations.Add(uint64(len(batch.operations)))
	st.maybeFreezeLocked()
	return nil
}
