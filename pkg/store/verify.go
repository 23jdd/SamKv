package store

import (
	"errors"
	"fmt"
)

var ErrSSTableCorrupt = errors.New("sstable: corruption detected")

// SSTableVerification 描述单个 SSTable 的完整性校验结果。
type SSTableVerification struct {
	Path       string
	Version    uint32
	DataBlocks int
	Records    int
}

// StoreVerification 汇总 Store 中全部 SSTable 的校验结果。
type StoreVerification struct {
	Tables  int
	Blocks  int
	Records int
	Results []SSTableVerification
}

// Verify 校验全部 DataBlock、记录顺序、元数据范围和 BloomFilter。
func (s *SStable) Verify() (SSTableVerification, error) {
	result := SSTableVerification{
		Path:       s.Path(),
		Version:    s.Version(),
		DataBlocks: len(s.index),
	}
	records, err := s.scan("", "", false)
	if err != nil {
		return result, fmt.Errorf("%w: %s: %w", ErrSSTableCorrupt, s.Path(), err)
	}
	result.Records = len(records)
	if uint64(len(records)) != s.meta.RecordCount {
		return result, fmt.Errorf("%w: %s: record count %d != %d", ErrSSTableCorrupt, s.Path(), len(records), s.meta.RecordCount)
	}
	for i, record := range records {
		if i > 0 && records[i-1].Key >= record.Key {
			return result, fmt.Errorf("%w: %s: keys are not strictly sorted", ErrSSTableCorrupt, s.Path())
		}
		if s.meta.Filter != nil && !s.meta.Filter.ContainsString(record.Key) {
			return result, fmt.Errorf("%w: %s: bloom filter false negative", ErrSSTableCorrupt, s.Path())
		}
	}
	if len(records) > 0 && (records[0].Key != s.meta.MinKey || records[len(records)-1].Key != s.meta.MaxKey) {
		return result, fmt.Errorf("%w: %s: key range does not match metadata", ErrSSTableCorrupt, s.Path())
	}
	return result, nil
}

// Verify 校验当前 Store 引用的全部 SSTable。
func (st *StoreManger) Verify() (StoreVerification, error) {
	st.mu.RLock()
	if st.closed {
		st.mu.RUnlock()
		return StoreVerification{}, ErrStoreClosed
	}
	tables := append([]*SStable(nil), st.sstables...)
	st.mu.RUnlock()

	report := StoreVerification{Tables: len(tables), Results: make([]SSTableVerification, 0, len(tables))}
	for _, table := range tables {
		result, err := table.Verify()
		report.Blocks += result.DataBlocks
		report.Records += result.Records
		report.Results = append(report.Results, result)
		if err != nil {
			return report, err
		}
	}
	return report, nil
}
