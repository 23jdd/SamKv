package store

// 本文件实现 Store 写入路径上的有序 MemTable 和近似容量统计。
// Store 负责协调冻结与写入；直接使用 MemTable 时，不要让 MarkImmutable/Clear 与 Put 并发。

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
// 零值不可用，必须通过 NewMemTable 创建。点读写可并发，但冻结和清空需要由所有者串行协调。
type MemTable struct {
	table *skiplist.SkipList[string, MemValue]
	size  atomic.Int64
	limit atomic.Int64

	mutable          atomic.Bool
	walSegmentCutoff atomic.Uint64
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

// Put 插入 key/value。由于跳表节点不可变，重复 key 不会替换旧值。
// 如果 key 已存在，会替换旧记录；如果旧记录是墓碑，会重新变成普通值。
// 空 key/value 在 MemTable 层合法；冻结后返回 ErrImmutableMemTable。
func (mt *MemTable) Put(key string, value string) error {
	if !mt.mutable.Load() {
		return ErrImmutableMemTable
	}

	newValue := MemValue{Value: value}
	oldValue, replaced := mt.table.Append(key, newValue)
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
// 冻结后的 MemTable 会拒绝新的 Put/Delete。调用方必须先阻止新写入，再冻结并导出快照；
// 它与已经越过 mutable 检查的并发 Put/Delete 之间不提供事务屏障。
func (mt *MemTable) MarkImmutable() {
	mt.mutable.Store(false)
}

// SetWALSegmentCutoff 记录该不可变表对应的最后一个 WAL segment。
// Store 在 Manifest 发布成功后才能调用 PruneThrough 删除此边界及以前的段。
func (mt *MemTable) SetWALSegmentCutoff(segmentID uint64) {
	mt.walSegmentCutoff.Store(segmentID)
}

// WALSegmentCutoff 返回冻结时封存的 WAL segment ID；0 表示没有可回收段。
func (mt *MemTable) WALSegmentCutoff() uint64 {
	return mt.walSegmentCutoff.Load()
}

// Mutable 返回当前 MemTable 是否仍允许写入。
func (mt *MemTable) Mutable() bool {
	return mt.mutable.Load()
}

// Size 返回当前 MemTable 的近似大小。
// 数值用于刷盘阈值而非精确内存计费，包含固定估算开销且不含跳表所有真实分配。
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
// Store 正常流程会创建新 MemTable，而不是复用已冻结实例；不得与外部读写并发用于事务重置。
func (mt *MemTable) Clear() {
	mt.table.Clear()
	mt.size.Store(0)
	mt.walSegmentCutoff.Store(0)
	mt.mutable.Store(true)
}

// ComputeSize 计算一条普通 key/value 记录在 MemTable 中的近似大小。
// 参数应为非负字节长度；函数不校验负数，也不代表 Go 堆上的精确占用。
func ComputeSize(keylen int, valuelen int) int {
	return keylen + valuelen + ApproximateEntrySize
}

func recordSize(key string, value MemValue) int {
	if value.Deleted {
		return ComputeSize(len(key), 0)
	}
	return ComputeSize(len(key), len(value.Value))
}
