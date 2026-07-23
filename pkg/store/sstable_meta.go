package store

import (
	"bytes"
	"encoding/binary"
	"sort"

	"github.com/23jdd/SamKv/pkg/utils"
)

const metaExtensionVersion uint32 = 1

func buildSSTableMeta(records []Record, keyFilter *BloomFilter) (MetaBlock, error) {
	meta := MetaBlock{
		RecordCount:      uint64(len(records)),
		Filter:           keyFilter,
		LabelCardinality: make(map[string]uint64),
	}
	if len(records) > 0 {
		meta.MinKey = records[0].Key
		meta.MaxKey = records[len(records)-1].Key
	}

	labelCount := 0
	cardinality := make(map[string]map[string]struct{})
	decodedKeys := make([]utils.Key, 0, len(records))
	for _, record := range records {
		key, err := utils.DecodeKey([]byte(record.Key))
		if err != nil {
			continue
		}
		decodedKeys = append(decodedKeys, key)
		labelCount += len(key.Labels)
		if !meta.HasTimeRange || key.Timestamp < meta.MinTimestamp {
			meta.MinTimestamp = key.Timestamp
		}
		if !meta.HasTimeRange || key.Timestamp > meta.MaxTimestamp {
			meta.MaxTimestamp = key.Timestamp
		}
		meta.HasTimeRange = true
		for _, label := range key.Labels {
			values := cardinality[label.Name]
			if values == nil {
				values = make(map[string]struct{})
				cardinality[label.Name] = values
			}
			values[label.Value] = struct{}{}
		}
	}

	var err error
	if labelCount == 0 {
		meta.LabelFilter, err = NewBloomFilterWithSize(64, 4)
	} else {
		meta.LabelFilter, err = NewBloomFilter(uint64(labelCount), 0.01)
	}
	if err != nil {
		return MetaBlock{}, err
	}
	for _, key := range decodedKeys {
		for _, label := range key.Labels {
			meta.LabelFilter.Add(labelToken(label))
		}
	}
	for name, values := range cardinality {
		meta.LabelCardinality[name] = uint64(len(values))
	}
	return meta, nil
}

// MayContainLabels 用标签 BloomFilter 快速排除一定不包含查询标签的 SSTable。
func (s *SStable) MayContainLabels(labels []utils.Label) bool {
	if s == nil || len(labels) == 0 || s.meta.LabelFilter == nil {
		return true
	}
	for _, label := range labels {
		if !s.meta.LabelFilter.Contains(labelToken(label)) {
			return false
		}
	}
	return true
}

// OverlapsTimeRange 使用 MetaBlock 的时间范围排除无关 SSTable。
func (s *SStable) OverlapsTimeRange(startTimestamp, endTimestamp int64) bool {
	if s == nil || !s.meta.HasTimeRange {
		return true
	}
	return s.meta.MaxTimestamp >= startTimestamp && s.meta.MinTimestamp <= endTimestamp
}

func labelToken(label utils.Label) []byte {
	token := make([]byte, 0, len(label.Name)+len(label.Value)+1)
	token = append(token, label.Name...)
	token = append(token, 0)
	token = append(token, label.Value...)
	return token
}

func encodeMetaExtension(meta MetaBlock) ([]byte, error) {
	var labelFilterData []byte
	var err error
	if meta.LabelFilter != nil {
		labelFilterData, err = meta.LabelFilter.MarshalBinary()
		if err != nil {
			return nil, err
		}
	}

	names := make([]string, 0, len(meta.LabelCardinality))
	for name := range meta.LabelCardinality {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	if err := writeUint32(&buf, metaExtensionVersion); err != nil {
		return nil, err
	}
	flags := byte(0)
	if meta.HasTimeRange {
		flags = 1
	}
	if err := buf.WriteByte(flags); err != nil {
		return nil, err
	}
	if err := writeUint64(&buf, uint64(meta.MinTimestamp)); err != nil {
		return nil, err
	}
	if err := writeUint64(&buf, uint64(meta.MaxTimestamp)); err != nil {
		return nil, err
	}
	if err := writeUint32(&buf, uint32(len(labelFilterData))); err != nil {
		return nil, err
	}
	if err := writeUint32(&buf, uint32(len(names))); err != nil {
		return nil, err
	}
	if _, err := buf.Write(labelFilterData); err != nil {
		return nil, err
	}
	for _, name := range names {
		if err := writeUint32(&buf, uint32(len(name))); err != nil {
			return nil, err
		}
		if err := writeUint64(&buf, meta.LabelCardinality[name]); err != nil {
			return nil, err
		}
		if _, err := buf.WriteString(name); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeMetaExtension(data []byte, meta *MetaBlock) error {
	const headerSize = 4 + 1 + 8 + 8 + 4 + 4
	if len(data) < headerSize {
		return ErrInvalidSSTable
	}
	offset := 0
	if binary.LittleEndian.Uint32(data[offset:]) != metaExtensionVersion {
		return ErrInvalidSSTable
	}
	offset += 4
	flags := data[offset]
	offset++
	if flags&^byte(1) != 0 {
		return ErrInvalidSSTable
	}
	meta.HasTimeRange = flags&1 != 0
	meta.MinTimestamp = int64(binary.LittleEndian.Uint64(data[offset:]))
	offset += 8
	meta.MaxTimestamp = int64(binary.LittleEndian.Uint64(data[offset:]))
	offset += 8
	if meta.HasTimeRange && meta.MinTimestamp > meta.MaxTimestamp {
		return ErrInvalidSSTable
	}

	filterLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	statsCount := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if filterLen < 0 || filterLen > len(data)-offset {
		return ErrInvalidSSTable
	}
	if filterLen > 0 {
		var filter BloomFilter
		if err := filter.UnmarshalBinary(data[offset : offset+filterLen]); err != nil {
			return err
		}
		meta.LabelFilter = &filter
	}
	offset += filterLen

	meta.LabelCardinality = make(map[string]uint64, statsCount)
	for i := 0; i < statsCount; i++ {
		if len(data)-offset < 12 {
			return ErrInvalidSSTable
		}
		nameLen := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		cardinality := binary.LittleEndian.Uint64(data[offset:])
		offset += 8
		if nameLen < 0 || nameLen > len(data)-offset {
			return ErrInvalidSSTable
		}
		name := string(data[offset : offset+nameLen])
		offset += nameLen
		if name == "" {
			return ErrInvalidSSTable
		}
		meta.LabelCardinality[name] = cardinality
	}
	if offset != len(data) {
		return ErrInvalidSSTable
	}
	return nil
}
