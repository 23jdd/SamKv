package store

// 本文件实现按 DataBlock 懒加载的 SSTable 范围迭代器。
// 迭代器保留墓碑；调用期间不能并发关闭所属 SSTable。

import "sort"

// SSTableIterator 按 key 升序遍历一张表的半开区间。
// 使用 Valid/Record/Next 驱动；遍历结束后通过 Error 检查延迟读取错误，并调用 Close 释放引用。
type SSTableIterator struct {
	table       *SStable
	startKey    string
	endKey      string
	blockIndex  int
	records     []Record
	recordIndex int
	valid       bool
	err         error
	closed      bool
}

// NewIterator 创建 [startKey, endKey) 范围迭代器并定位第一条记录。
// 空边界表示无限制；startKey>=endKey 返回合法空迭代器，nil 表返回 ErrInvalidSSTable。
func (s *SStable) NewIterator(startKey, endKey string) (*SSTableIterator, error) {
	if s == nil {
		return nil, ErrInvalidSSTable
	}
	iterator := &SSTableIterator{table: s, startKey: startKey, endKey: endKey}
	if endKey != "" && startKey != "" && startKey >= endKey {
		return iterator, nil
	}
	if s.meta.RecordCount == 0 || !keyRangesOverlap(startKey, endKey, s.meta.MinKey, s.meta.MaxKey) {
		return iterator, nil
	}

	if s.file == nil {
		iterator.records = s.rs
		if startKey != "" {
			iterator.recordIndex = sort.Search(len(iterator.records), func(index int) bool {
				return iterator.records[index].Key >= startKey
			})
		}
		iterator.positionCurrent()
		return iterator, nil
	}
	iterator.blockIndex = sort.Search(len(s.index), func(index int) bool {
		return startKey == "" || s.index[index].LastKey >= startKey
	})
	iterator.loadNextBlock()
	if iterator.err != nil {
		return nil, iterator.err
	}
	return iterator, nil
}

// Valid 报告 Record 是否可读取；遍历结束、读取失败或 Close 后均返回 false。
func (it *SSTableIterator) Valid() bool {
	return it != nil && !it.closed && it.err == nil && it.valid
}

// Record 返回当前位置记录；Valid=false 时返回零值。
// Record 可能是墓碑，调用方必须检查 Deleted。
func (it *SSTableIterator) Record() Record {
	if !it.Valid() {
		return Record{}
	}
	return it.records[it.recordIndex]
}

// Next 前进到下一条记录。遍历结束后重复调用是安全的。
func (it *SSTableIterator) Next() {
	if !it.Valid() {
		return
	}
	it.recordIndex++
	it.positionCurrent()
}

// Error 返回迭代期间遇到的首个 DataBlock 读取、校验或解码错误。
func (it *SSTableIterator) Error() error {
	if it == nil {
		return ErrInvalidSSTable
	}
	return it.err
}

// Close 使迭代器失效并释放对表和当前 Block 记录的引用；可重复调用。
func (it *SSTableIterator) Close() error {
	if it == nil || it.closed {
		return nil
	}
	it.closed = true
	it.valid = false
	it.records = nil
	it.table = nil
	return nil
}

func (it *SSTableIterator) positionCurrent() {
	for !it.closed && it.err == nil {
		if it.recordIndex < len(it.records) {
			key := it.records[it.recordIndex].Key
			if it.endKey != "" && key >= it.endKey {
				it.valid = false
				return
			}
			it.valid = true
			return
		}
		if it.table == nil || it.table.file == nil {
			it.valid = false
			return
		}
		it.loadNextBlock()
		if it.valid || it.err != nil {
			return
		}
	}
}

func (it *SSTableIterator) loadNextBlock() {
	it.valid = false
	it.records = nil
	it.recordIndex = 0
	for it.table != nil && it.blockIndex < len(it.table.index) {
		entry := it.table.index[it.blockIndex]
		it.blockIndex++
		if it.endKey != "" && entry.FirstKey >= it.endKey {
			return
		}
		if it.startKey != "" && entry.LastKey < it.startKey {
			continue
		}
		data, release, err := it.table.readDataBlock(entry.Handle, true)
		if err != nil {
			it.err = err
			return
		}
		records, err := DecodeDataBlock(data)
		release()
		if err != nil {
			it.err = err
			return
		}
		it.records = records
		if it.startKey != "" {
			it.recordIndex = sort.Search(len(records), func(index int) bool {
				return records[index].Key >= it.startKey
			})
		}
		it.positionCurrent()
		return
	}
}
