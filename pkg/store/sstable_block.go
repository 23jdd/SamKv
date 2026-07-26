package store

// 本文件实现 DataBlock 的 key 前缀压缩、restart point 编码和严格解码。
// 比较与共享前缀均以字节为单位，适用于二进制复合 key，不依赖 UTF-8 rune 边界。

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// EncodeDataBlock 把已排序记录编码成前缀压缩 DataBlock。
// 每 ReStartInterval 条记录写完整 key；超大 block 或写入失败返回错误。
func EncodeDataBlock(rs []Record) ([]byte, error) {
	var buf bytes.Buffer
	restarts := make([]uint32, 0, (len(rs)/ReStartInterval)+1)
	lastKey := []byte(nil)

	for i, record := range rs {
		key := []byte(record.Key)
		value := []byte(record.Val)

		shared := 0
		if i%ReStartInterval == 0 {
			if buf.Len() > int(^uint32(0)) {
				return nil, errors.New("sstable: data block too large")
			}
			restarts = append(restarts, uint32(buf.Len()))
		} else {
			shared = SharedLen(key, lastKey)
		}

		nonShared := len(key) - shared
		if err := writeUint32(&buf, uint32(shared)); err != nil {
			return nil, err
		}
		if err := writeUint32(&buf, uint32(nonShared)); err != nil {
			return nil, err
		}
		if err := writeUint32(&buf, uint32(len(value))); err != nil {
			return nil, err
		}
		flags := byte(0)
		if record.Deleted {
			flags = 1
		}
		if err := buf.WriteByte(flags); err != nil {
			return nil, err
		}
		if _, err := buf.Write(key[shared:]); err != nil {
			return nil, err
		}
		if _, err := buf.Write(value); err != nil {
			return nil, err
		}

		lastKey = append(lastKey[:0], key...)
	}

	for _, restart := range restarts {
		if err := writeUint32(&buf, restart); err != nil {
			return nil, err
		}
	}
	if err := writeUint32(&buf, uint32(len(restarts))); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeDataBlock 解码一个 DataBlock，并还原前缀压缩过的完整 key。
// 截断长度、shared 前缀越界和未知 flags 返回 ErrInvalidSSTable；返回记录拥有独立字符串数据。
func DecodeDataBlock(data []byte) ([]Record, error) {
	if len(data) < 4 {
		return nil, ErrInvalidSSTable
	}
	restartCount := binary.LittleEndian.Uint32(data[len(data)-4:])
	restartBytes := int(restartCount) * 4
	if restartBytes > len(data)-4 {
		return nil, ErrInvalidSSTable
	}
	entriesEnd := len(data) - 4 - restartBytes

	var records []Record
	offset := 0
	lastKey := []byte(nil)
	for offset < entriesEnd {
		if entriesEnd-offset < 13 {
			return nil, ErrInvalidSSTable
		}
		shared := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		nonShared := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		valueLen := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4

		if shared > len(lastKey) || nonShared < 0 || valueLen < 0 {
			return nil, ErrInvalidSSTable
		}
		if entriesEnd-offset < 1 {
			return nil, ErrInvalidSSTable
		}
		flags := data[offset]
		offset++
		if flags&^1 != 0 {
			return nil, ErrInvalidSSTable
		}

		if nonShared > entriesEnd-offset || valueLen > entriesEnd-offset-nonShared {
			return nil, ErrInvalidSSTable
		}

		key := make([]byte, shared+nonShared)
		copy(key, lastKey[:shared])
		copy(key[shared:], data[offset:offset+nonShared])
		offset += nonShared

		value := data[offset : offset+valueLen]
		offset += valueLen

		records = append(records, Record{Key: string(key), Val: string(value), Deleted: flags&1 != 0})
		lastKey = key
	}
	if offset != entriesEnd {
		return nil, ErrInvalidSSTable
	}
	return records, nil
}

// DecodeRcWithTrie 保留旧函数名以兼容已有调用。
// 实际行为是把记录编码成带前缀压缩和 restart point 的 DataBlock。
// 该兼容 API 无法返回编码错误；新代码应直接调用 EncodeDataBlock。
func DecodeRcWithTrie(rs []Record) []byte {
	data, _ := EncodeDataBlock(rs)
	return data
}

// SharedLen 返回两个 key 从头开始相同的字节数。
// 比较单位是字节而非 Unicode rune，nil/空输入返回 0。
func SharedLen(target []byte, source []byte) int {
	ml := min(len(target), len(source))
	for i := 0; i < ml; i++ {
		if target[i] != source[i] {
			return i
		}
	}
	return ml
}
