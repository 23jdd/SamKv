package store

// 本文件实现按调用顺序编码的批量 Put。
// Batch 在提交前由调用方独占构建，WriteBatch 再一次持有 Store 写锁完成 WAL 追加和内存应用。

import (
	"errors"

	"github.com/23jdd/SamKv/pkg/wal"
)

// ErrInvalidBatch 表示批次包含空 key、未知操作类型或无法编码的 WAL 记录。
var ErrInvalidBatch = errors.New("store: invalid batch")

// BatchOperationType 区分批量写入；零值及未知值无效。
type BatchOperationType uint8

const (
	BatchPut BatchOperationType = iota + 1
)

// BatchOperation 是批量写中的单个 Put。
type BatchOperation struct {
	Type  BatchOperationType
	Key   string
	Value string
}

// Batch 收集一组按顺序执行的写操作。
// Batch 不是并发安全的；提交期间及提交后修改它都不会影响已经编码的 WAL 数据。
type Batch struct {
	operations []BatchOperation
}

// NewBatch 创建空批次；空批次提交是无副作用的成功操作。
func NewBatch() *Batch {
	return &Batch{}
}

// Put 在批次尾部追加写入并返回自身，便于链式构建；空 key 会在 WriteBatch 时被拒绝。
func (batch *Batch) Put(key, value string) *Batch {
	batch.operations = append(batch.operations, BatchOperation{Type: BatchPut, Key: key, Value: value})
	return batch
}

// Len 返回操作数；nil Batch 的长度为 0。
func (batch *Batch) Len() int {
	if batch == nil {
		return 0
	}
	return len(batch.operations)
}

// WriteBatch 将整批 WAL 记录一次追加到缓冲区，再按顺序更新 MemTable。
// 它减少锁竞争和 WAL 提交次数；恢复时仍按单条记录顺序重放。
// 这不是可回滚事务：崩溃造成 WAL 尾部截断时可能只恢复完整前缀。nil/空批次直接返回 nil，
// 即使 Store 已关闭；非空批次会检查 Store 状态，并在任何内存修改前完成整批 WAL 追加。
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
		}
	}
	st.stats.writeOperations.Add(uint64(len(batch.operations)))
	st.maybeFreezeLocked()
	return nil
}
