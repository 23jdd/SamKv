package store

import (
	"errors"
	"math"
	"time"

	"github.com/23jdd/SamKv/pkg/utils"
)

var (
	ErrInvalidTimeRange = errors.New("store: invalid time range")
	ErrDuplicateLabel   = errors.New("store: duplicate label name")
)

// LogEntry 是面向日志场景的结构化记录。
// 标签只编码进 Key，Message 会按 utils.Value 的格式压缩后保存。
type LogEntry struct {
	Timestamp time.Time
	Labels    []utils.Label
	Sequence  uint64
	Message   []byte
}

// WriteLog 生成“时间 + 有序标签 + 序列号”复合 key，并写入压缩后的日志内容。
// Sequence 为 0 时由 Store 自动分配；返回值是最终使用的序列号。
func (st *StoreManger) WriteLog(entry LogEntry) (uint64, error) {
	if err := validateQueryLabels(entry.Labels); err != nil {
		return 0, err
	}

	sequence := entry.Sequence
	if sequence == 0 {
		sequence = st.sequence.Add(1)
	} else {
		st.observeSequence(sequence)
	}

	timestamp := entry.Timestamp.UnixNano()
	key, err := utils.EncodeKey(timestamp, entry.Labels, sequence)
	if err != nil {
		return 0, err
	}
	value, err := utils.NewValue(timestamp, entry.Message)
	if err != nil {
		return 0, err
	}
	encodedValue, err := value.MarshalBinary()
	if err != nil {
		return 0, err
	}
	if err := st.Put(string(key), string(encodedValue)); err != nil {
		return 0, err
	}
	return sequence, nil
}

// Query 按闭区间 [startTime, endTime] 查询日志，并用 labels 做子集匹配。
// 返回结果按复合 key 排序，即先按时间，再按标签和序列号排序。
func (st *StoreManger) Query(startTime, endTime time.Time, labels []utils.Label) ([]LogEntry, error) {
	if endTime.Before(startTime) {
		return nil, ErrInvalidTimeRange
	}
	if err := validateQueryLabels(labels); err != nil {
		return nil, err
	}

	startNanos := startTime.UnixNano()
	endNanos := endTime.UnixNano()
	startKey := string(utils.TimePrefix(startNanos))
	endKey := ""
	if endNanos < math.MaxInt64 {
		endKey = string(utils.TimePrefix(endNanos + 1))
	}

	records, err := st.scanWithTableFilter(startKey, endKey, func(table *SStable) bool {
		return table.OverlapsTimeRange(startNanos, endNanos) && table.MayContainLabels(labels)
	})
	if err != nil {
		return nil, err
	}

	expected := make(map[string]string, len(labels))
	for _, label := range labels {
		expected[label.Name] = label.Value
	}

	entries := make([]LogEntry, 0, len(records))
	for _, record := range records {
		decodedKey, err := utils.DecodeKey([]byte(record.Key))
		if err != nil {
			return nil, err
		}
		if !labelsMatch(decodedKey.Labels, expected) {
			continue
		}

		value, err := utils.UnmarshalValue([]byte(record.Val))
		if err != nil {
			return nil, err
		}
		message, err := value.DecompressedMessage()
		if err != nil {
			return nil, err
		}
		entries = append(entries, LogEntry{
			Timestamp: time.Unix(0, decodedKey.Timestamp).UTC(),
			Labels:    append([]utils.Label(nil), decodedKey.Labels...),
			Sequence:  decodedKey.Sequence,
			Message:   message,
		})
	}
	return entries, nil
}

func validateQueryLabels(labels []utils.Label) error {
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		if _, ok := seen[label.Name]; ok {
			return ErrDuplicateLabel
		}
		seen[label.Name] = struct{}{}
	}
	_, err := utils.EncodeKey(0, labels, 0)
	return err
}

func labelsMatch(labels []utils.Label, expected map[string]string) bool {
	if len(expected) == 0 {
		return true
	}
	actual := make(map[string]string, len(labels))
	for _, label := range labels {
		actual[label.Name] = label.Value
	}
	for name, value := range expected {
		if actual[name] != value {
			return false
		}
	}
	return true
}

func (st *StoreManger) observeSequence(sequence uint64) {
	for {
		current := st.sequence.Load()
		if current >= sequence || st.sequence.CompareAndSwap(current, sequence) {
			return
		}
	}
}

// restoreSequence 从 Manifest、WAL 和旧 SSTable 中恢复最大序列号。
// 这保证进程重启后自动分配的序列号不会覆盖同一时间和标签下的旧日志。
func (st *StoreManger) restoreSequence() error {
	maxSequence := st.sequence.Load()
	observeRecords := func(records []Record) {
		for _, record := range records {
			key, err := utils.DecodeKey([]byte(record.Key))
			if err == nil && key.Sequence > maxSequence {
				maxSequence = key.Sequence
			}
		}
	}

	for _, table := range st.sstables {
		records, err := table.AllRecords()
		if err != nil {
			return err
		}
		observeRecords(records)
	}
	for _, immutable := range st.immutables {
		observeRecords(immutable.Entries())
	}
	observeRecords(st.mem.Entries())
	st.sequence.Store(maxSequence)
	return nil
}
