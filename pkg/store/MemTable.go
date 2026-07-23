package store

import (
	"errors"
	"sync/atomic"

	skiplist "github.com/23jdd/SamKv/pkg/skipList"
)

const (
	// ApproximateEntrySize 是 MemTable 中每条记录除 key/value 外的估算开销。
	// 它用于近似判断 MemTable 是否达到刷盘阈值，不要求精确等于 Go 对象真实内存占用。
	ApproximateEntrySize = 24
)

var (
	// ErrImmutableMemTable 表示当前 MemTable 已被冻结，不能继续写入。
	ErrImmutableMemTable = errors.New("memtable: immutable")

	// ErrIMut 保留旧错误名，兼容已有调用。
	ErrIMut = ErrImmutableMemTable
)

// MemTable 是写入路径上的有序内存表。
// SkipList 自己负责保护节点结构；MemTable 只用 atomic 维护 size 和 mutable 状态。
type MemTable struct {
	table *skiplist.SkipList[string, string]
	size  atomic.Int64
	limit atomic.Int64

	mutable atomic.Bool
}

// Compare 定义 MemTable 中 key 的排序方式。
func Compare(a string, b string) int {
	if a > b {
		return 1
	} else if a == b {
		return 0
	}
	return -1
}

// NewMemTable 创建一个可写 MemTable。
// limit 是触发刷盘的近似大小阈值；limit <= 0 表示永不自动触发。
func NewMemTable(limit int) *MemTable {
	mt := &MemTable{
		table: skiplist.New[string, string](Compare),
	}
	mt.limit.Store(int64(limit))
	mt.mutable.Store(true)
	return mt
}

// Get 根据 key 查询 value。
func (mt *MemTable) Get(key string) (string, bool) {
	return mt.table.Get(key)
}

// Put 插入或更新 key/value。
// 如果 key 已存在，会替换旧 value，并按 value 长度差更新近似 size。
func (mt *MemTable) Put(key string, value string) error {
	if !mt.mutable.Load() {
		return ErrImmutableMemTable
	}

	oldValue, replaced := mt.table.Set(key, value)
	if replaced {
		mt.size.Add(int64(len(value) - len(oldValue)))
		return nil
	}

	mt.size.Add(int64(ComputeSize(len(key), len(value))))
	return nil
}

// Delete 删除 key。
// 删除成功时会扣减该 key/value 的近似大小。
func (mt *MemTable) Delete(key string) error {
	if !mt.mutable.Load() {
		return ErrImmutableMemTable
	}

	oldValue, deleted := mt.table.Delete(key)
	if deleted {
		newSize := mt.size.Add(-int64(ComputeSize(len(key), len(oldValue))))
		if newSize < 0 {
			mt.size.Store(0)
		}
	}
	return nil
}

// Entries 返回 MemTable 当前内容的有序快照。
// 返回值已经转换成 SSTable 使用的 Record 类型，可以直接传给 WriteSStable。
func (mt *MemTable) Entries() []Record {
	entries := mt.table.Entries()
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		records = append(records, Record{Key: entry.Key, Val: entry.Value})
	}
	return records
}

// Flush 返回可写入 SSTable 的有序记录快照。
// 真正的磁盘写入由 Store/SSTable 层负责，MemTable 只负责导出内存数据。
func (mt *MemTable) Flush() []Record {
	return mt.Entries()
}

// MarkImmutable 将 MemTable 冻结为只读。
// 冻结后的 MemTable 会拒绝新的 Put/Delete。
func (mt *MemTable) MarkImmutable() {
	mt.mutable.Store(false)
}

// Mutable 返回当前 MemTable 是否仍允许写入。
func (mt *MemTable) Mutable() bool {
	return mt.mutable.Load()
}

// Size 返回当前 MemTable 的近似大小。
func (mt *MemTable) Size() int {
	return int(mt.size.Load())
}

// Len 返回当前 MemTable 中 key 的数量。
func (mt *MemTable) Len() int {
	return mt.table.Len()
}

// ShouldFlush 判断当前 MemTable 是否达到刷盘阈值。
func (mt *MemTable) ShouldFlush() bool {
	limit := mt.limit.Load()
	return limit > 0 && mt.size.Load() >= limit
}

// Clear 清空 MemTable，并恢复为可写状态。
func (mt *MemTable) Clear() {
	mt.table.Clear()
	mt.size.Store(0)
	mt.mutable.Store(true)
}

// ComputeSize 计算一条 key/value 记录在 MemTable 中的近似大小。
func ComputeSize(keylen int, valuelen int) int {
	return keylen + valuelen + ApproximateEntrySize
}
