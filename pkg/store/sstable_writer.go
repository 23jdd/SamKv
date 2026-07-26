package store

// 本文件负责 SSTable 构建：规范化输入、生成 Bloom Filter、切分 DataBlock，
// 再按 DataBlock -> MetaBlock -> IndexBlock -> Footer 顺序写入临时文件并原子发布。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// WriteSStable 将 records 写成一个完整的 SSTable 文件。
// 写入顺序是 DataBlocks -> MetaBlock -> IndexBlock -> Footer。
// 输入会复制、排序和按 key 去重；空输入会生成合法空表。path 应是未使用的唯一文件名，
// 函数先写 path.tmp、Sync 后 Rename，成功返回的对象可读取且 Close 可安全调用。
func WriteSStable(path string, rs []Record) (*SStable, error) {
	return writeSStable(path, rs, nil)
}

// writeSStableWithLimiter 仅供后台 Compaction 使用；多个调用可共享同一 limiter 控制聚合带宽。
func writeSStableWithLimiter(path string, rs []Record, limiter byteRateLimiter) (*SStable, error) {
	return writeSStable(path, rs, limiter)
}

func writeSStable(path string, rs []Record, limiter byteRateLimiter) (*SStable, error) {
	records := normalizeRecords(rs)
	bf, err := buildBloomFilter(records)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	tmpPath := path + ".tmp"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	writer := newRateLimitedWriter(context.Background(), file, limiter)

	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	var offset uint64
	var index []IndexEntry
	for _, blockRecords := range splitDataBlocks(records, defaultDataBlockSize) {
		blockData, err := EncodeDataBlock(blockRecords)
		if err != nil {
			return nil, err
		}
		encodedBlock := encodeChecksummedBlock(blockData)
		if err := writeAll(writer, encodedBlock); err != nil {
			return nil, err
		}
		index = append(index, IndexEntry{
			FirstKey: blockRecords[0].Key,
			LastKey:  blockRecords[len(blockRecords)-1].Key,
			Handle: BlockHandle{
				Offset: offset,
				Size:   uint64(len(encodedBlock)),
			},
		})
		offset += uint64(len(encodedBlock))
	}

	meta, err := buildSSTableMeta(records, bf)
	if err != nil {
		return nil, err
	}
	metaData, err := encodeMetaBlock(meta)
	if err != nil {
		return nil, err
	}
	encodedMeta := encodeChecksummedBlock(metaData)
	metaHandle := BlockHandle{Offset: offset, Size: uint64(len(encodedMeta))}
	if err := writeAll(writer, encodedMeta); err != nil {
		return nil, err
	}
	offset += uint64(len(encodedMeta))

	indexData, err := encodeIndexBlock(index)
	if err != nil {
		return nil, err
	}
	encodedIndex := encodeChecksummedBlock(indexData)
	indexHandle := BlockHandle{Offset: offset, Size: uint64(len(encodedIndex))}
	if err := writeAll(writer, encodedIndex); err != nil {
		return nil, err
	}
	offset += uint64(len(encodedIndex))

	footer := Footer{
		Version:     currentSSTableVersion,
		MetaHandle:  metaHandle,
		IndexHandle: indexHandle,
	}
	footerData := encodeFooter(footer)
	if err := writeAll(writer, footerData); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		return nil, os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, err
	}
	if err := syncStoreDirectory(filepath.Dir(path)); err != nil {
		_ = os.Remove(path)
		_ = syncStoreDirectory(filepath.Dir(path))
		return nil, err
	}
	ok = true

	return &SStable{
		path:    path,
		version: currentSSTableVersion,
		rs:      records,
		bf:      bf,
		index:   index,
		meta:    meta,
		footer:  footer,
	}, nil
}

// normalizeRecords 对记录按 key 稳定排序并合并重复 key。
// 同一 key 保留输入中最后一条记录，从而维持后写覆盖前写语义。
func normalizeRecords(rs []Record) []Record {
	records := make([]Record, len(rs))
	copy(records, rs)
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Key < records[j].Key
	})

	out := records[:0]
	for _, record := range records {
		if len(out) > 0 && out[len(out)-1].Key == record.Key {
			out[len(out)-1] = record
			continue
		}
		out = append(out, record)
	}
	return out
}

// buildBloomFilter 使用 store 包已有 BloomFilter，为当前 SSTable 的所有 key 建过滤器。
func buildBloomFilter(records []Record) (*BloomFilter, error) {
	if len(records) == 0 {
		return NewBloomFilterWithSize(64, 4)
	}
	bf, err := NewBloomFilter(uint64(len(records)), 0.01)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		bf.AddString(record.Key)
	}
	return bf, nil
}

// splitDataBlocks 按目标字节大小把有序记录切成多个 DataBlock。
// 单条记录超过目标大小时仍会单独形成一个 block。
func splitDataBlocks(records []Record, targetSize int) [][]Record {
	if len(records) == 0 {
		return nil
	}

	blocks := make([][]Record, 0)
	start := 0
	for start < len(records) {
		end := start + 1
		for end < len(records) {
			candidate, err := EncodeDataBlock(records[start : end+1])
			if err != nil || len(candidate) > targetSize {
				break
			}
			end++
		}
		blocks = append(blocks, records[start:end])
		start = end
	}
	return blocks
}

// encodeMetaBlock 编码 MetaBlock。
// BloomFilter 直接复用 bloomfilter.go 中的 MarshalBinary 格式。
