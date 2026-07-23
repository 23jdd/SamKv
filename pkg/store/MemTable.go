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

// MemValue 是 MemTable 中保存的值。
// Deleted=true 表示这条记录是墓碑，用于覆盖旧 SSTable 中可能存在的旧值。
type MemValue struct {
	Value   string
	Deleted bool
}

// MemTable 是写入路径上的有序内存表。
// SkipList 自己负责保护节点结构；MemTable 只用 atomic 维护 size 和 mutable 状态。
type MemTable struct {
	table *skiplist.SkipList[string, MemValue]
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
		table: skiplist.New[string, MemValue](Compare),
	}
	mt.limit.Store(int64(limit))
	mt.mutable.Store(true)
	return mt
}

// Get 根据 key 查询 value。
// 如果 key 对应的是墓碑，返回 ok=false。
func (mt *MemTable) Get(key string) (string, bool) {
	value, ok := mt.table.Get(key)
	if !ok || value.Deleted {
		return "", false
	}
	return value.Value, true
}

// Put 插入或更新 key/value。
// 如果 key 已存在，会替换旧记录；如果旧记录是墓碑，会重新变成普通值。
func (mt *MemTable) Put(key string, value string) error {
	if !mt.mutable.Load() {
		return ErrImmutableMemTable
	}

	newValue := MemValue{Value: value}
	oldValue, replaced := mt.table.Set(key, newValue)
	if replaced {
		mt.size.Add(int64(recordSize(key, newValue) - recordSize(key, oldValue)))
		return nil
	}

	mt.size.Add(int64(recordSize(key, newValue)))
	return nil
}

// Delete 写入 key 的墓碑记录。
// 墓碑必须保留到 SSTable/Compaction 层，否则旧 SSTable 中的值可能被重新查出来。
func (mt *MemTable) Delete(key string) error {
	if !mt.mutable.Load() {
		return ErrImmutableMemTable
	}

	newValue := MemValue{Deleted: true}
	oldValue, replaced := mt.table.Set(key, newValue)
	if replaced {
		mt.size.Add(int64(recordSize(key, newValue) - recordSize(key, oldValue)))
		return nil
	}

	mt.size.Add(int64(recordSize(key, newValue)))
	return nil
}

// Entries 返回 MemTable 当前内容的有序快照。
// 返回值包含墓碑记录，可以直接传给 WriteSStable。
func (mt *MemTable) Entries() []Record {
	entries := mt.table.Entries()
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		records = append(records, Record{
			Key:     entry.Key,
			Val:     entry.Value.Value,
			Deleted: entry.Value.Deleted,
		})
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
// 注意：墓碑也会占一个 key，因为它需要参与 flush 和后续 compaction。
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

// ComputeSize 计算一条普通 key/value 记录在 MemTable 中的近似大小。
func ComputeSize(keylen int, valuelen int) int {
	return keylen + valuelen + ApproximateEntrySize
}

func recordSize(key string, value MemValue) int {
	if value.Deleted {
		return ComputeSize(len(key), 0)
	}
	return ComputeSize(len(key), len(value.Value))
}
