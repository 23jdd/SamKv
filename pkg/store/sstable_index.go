package store

// 本文件编解码 IndexBlock。每项保存 DataBlock 的闭区间 key 范围及物理 BlockHandle，
// reader 可先二分索引再读取单个 DataBlock，避免点查扫描整张 SSTable。

import (
	"bytes"
	"encoding/binary"
)

// encodeIndexBlock 编码 IndexBlock。
// IndexBlock 保存每个 DataBlock 的 key 范围和 BlockHandle。
func encodeIndexBlock(index []IndexEntry) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeUint32(&buf, uint32(len(index))); err != nil {
		return nil, err
	}
	for _, entry := range index {
		firstKey := []byte(entry.FirstKey)
		lastKey := []byte(entry.LastKey)
		if err := writeUint32(&buf, uint32(len(firstKey))); err != nil {
			return nil, err
		}
		if err := writeUint32(&buf, uint32(len(lastKey))); err != nil {
			return nil, err
		}
		if err := writeUint64(&buf, entry.Handle.Offset); err != nil {
			return nil, err
		}
		if err := writeUint64(&buf, entry.Handle.Size); err != nil {
			return nil, err
		}
		if _, err := buf.Write(firstKey); err != nil {
			return nil, err
		}
		if _, err := buf.Write(lastKey); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// decodeIndexBlock 解码 IndexBlock。
func decodeIndexBlock(data []byte) ([]IndexEntry, error) {
	if len(data) < 4 {
		return nil, ErrInvalidSSTable
	}
	offset := 0
	count := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	index := make([]IndexEntry, 0, count)
	for i := 0; i < count; i++ {
		if len(data)-offset < 24 {
			return nil, ErrInvalidSSTable
		}
		firstKeyLen := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		lastKeyLen := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		handle := BlockHandle{
			Offset: binary.LittleEndian.Uint64(data[offset:]),
		}
		offset += 8
		handle.Size = binary.LittleEndian.Uint64(data[offset:])
		offset += 8

		if firstKeyLen < 0 || lastKeyLen < 0 {
			return nil, ErrInvalidSSTable
		}
		if firstKeyLen > len(data)-offset || lastKeyLen > len(data)-offset-firstKeyLen {
			return nil, ErrInvalidSSTable
		}
		firstKey := string(data[offset : offset+firstKeyLen])
		offset += firstKeyLen
		lastKey := string(data[offset : offset+lastKeyLen])
		offset += lastKeyLen

		index = append(index, IndexEntry{FirstKey: firstKey, LastKey: lastKey, Handle: handle})
	}
	if offset != len(data) {
		return nil, ErrInvalidSSTable
	}
	return index, nil
}
