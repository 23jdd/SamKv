package wal

// 本文件定义 WAL 记录的 CRC32 帧格式以及流式读取。
// Decode 会复制 Key/Value，返回记录不再引用调用方或缓冲池中的底层字节。

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"

	bufferpool "github.com/23jdd/SamKv/pkg/pool"
)

// RecordType 区分写入和删除记录；零值及未知值都不是有效磁盘类型。
type RecordType uint8

const (
	// RecordPut 表示为 Key 写入 Value。
	RecordPut RecordType = iota + 1
	// RecordDelete 表示删除 Key；该类型的 Value 必须为空。
	RecordDelete
)

const (
	headerSize           = 8 // CRC32 + PayloadLength
	fixedPayloadSize     = 17
	maxRecordPayloadSize = 64 << 20
	minRecordBufferSize  = 4 * 1024 // 恢复小记录时仍从 4 KiB 池开始复用。
)

var (
	// ErrInvalidRecord 表示记录类型、长度或字段组合不符合 WAL 格式。
	ErrInvalidRecord = errors.New("invalid wal record")
	// ErrChecksum 表示 payload 长度完整但 CRC32 校验失败。
	ErrChecksum = errors.New("wal checksum mismatch")
	// ErrRecordTooLarge 表示流中声明的 payload 超过 64 MiB 安全上限。
	ErrRecordTooLarge = errors.New("wal record too large")
)

// walRecordBufferPool 复用 WAL 恢复读取缓冲；大于 1 MiB 的记录读取后直接释放。
var walRecordBufferPool = bufferpool.NewTieredPool(
	minRecordBufferSize,
	16*1024,
	64*1024,
	256*1024,
	1024*1024,
)

// Record 是一条可校验的 WAL 操作。
// PutRecord/DeleteRecord 不复制传入切片，调用方在 Encode 或 AppendRecord 返回前不得修改它们。
type Record struct {
	// Type 是写入或删除操作。
	Type RecordType
	// Sequence 由上层 Store 分配，用于恢复版本顺序；零值合法。
	Sequence uint64
	// Key 必须非空。
	Key []byte
	// Value 在 RecordPut 中可以为空，在 RecordDelete 中必须为空。
	Value []byte
}

// PutRecord 构造写入记录；空 key 会在 Encode 时被拒绝，空 value 合法。
func PutRecord(key []byte, val []byte) *Record {
	return &Record{Type: RecordPut, Key: key, Value: val}
}

// DeleteRecord 构造墓碑记录；空 key 会在 Encode 时被拒绝。
func DeleteRecord(key []byte) *Record {
	return &Record{Type: RecordDelete, Key: key}
}

// Encode 生成“CRC32、payload 长度、payload”的完整记录帧。
// nil Record、空 key、未知类型或带 value 的删除记录返回 ErrInvalidRecord。
func (r *Record) Encode() ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRecord
	}
	if len(r.Key) == 0 {
		return nil, errors.New("empty key")
	}

	if r.Type != RecordPut && r.Type != RecordDelete {
		return nil, errors.New("invalid record type")
	}

	if r.Type == RecordDelete && len(r.Value) != 0 {
		return nil, errors.New("delete record must not contain value")
	}

	payloadLength := fixedPayloadSize + len(r.Key) + len(r.Value)
	if payloadLength > maxRecordPayloadSize {
		return nil, ErrRecordTooLarge
	}

	payload := make([]byte, payloadLength)

	offset := 0

	payload[offset] = byte(r.Type)
	offset++

	binary.LittleEndian.PutUint64(
		payload[offset:offset+8],
		r.Sequence,
	)
	offset += 8

	binary.LittleEndian.PutUint32(
		payload[offset:offset+4],
		uint32(len(r.Key)),
	)
	offset += 4

	binary.LittleEndian.PutUint32(
		payload[offset:offset+4],
		uint32(len(r.Value)),
	)
	offset += 4

	copy(payload[offset:offset+len(r.Key)], r.Key)
	offset += len(r.Key)

	copy(payload[offset:offset+len(r.Value)], r.Value)

	result := make([]byte, headerSize+payloadLength)

	checksum := crc32.ChecksumIEEE(payload)

	binary.LittleEndian.PutUint32(result[0:4], checksum)
	binary.LittleEndian.PutUint32(result[4:8], uint32(payloadLength))
	copy(result[8:], payload)

	return result, nil
}

// Decode 校验并解码一条完整记录。
// data 可以包含帧后的额外字节，但只解码头部声明的第一条记录；流式多记录应使用 ReadRecord。
func Decode(data []byte) (*Record, error) {
	if len(data) < headerSize {
		return nil, ErrInvalidRecord
	}

	expectedChecksum := binary.LittleEndian.Uint32(data[0:4])
	payloadLength := binary.LittleEndian.Uint32(data[4:8])

	if uint64(payloadLength) > uint64(len(data)-headerSize) {
		return nil, ErrInvalidRecord
	}

	payload := data[headerSize : headerSize+int(payloadLength)]

	actualChecksum := crc32.ChecksumIEEE(payload)
	if actualChecksum != expectedChecksum {
		return nil, ErrChecksum
	}

	if len(payload) < fixedPayloadSize {
		return nil, ErrInvalidRecord
	}

	offset := 0

	recordType := RecordType(payload[offset])
	offset++

	sequence := binary.LittleEndian.Uint64(
		payload[offset : offset+8],
	)
	offset += 8

	keyLength := binary.LittleEndian.Uint32(
		payload[offset : offset+4],
	)
	offset += 4

	valueLength := binary.LittleEndian.Uint32(
		payload[offset : offset+4],
	)
	offset += 4

	totalLength := uint64(fixedPayloadSize) +
		uint64(keyLength) +
		uint64(valueLength)

	if totalLength != uint64(len(payload)) {
		return nil, ErrInvalidRecord
	}

	keyEnd := offset + int(keyLength)
	if keyEnd > len(payload) {
		return nil, ErrInvalidRecord
	}

	key := append([]byte(nil), payload[offset:keyEnd]...)
	offset = keyEnd

	valueEnd := offset + int(valueLength)
	if valueEnd > len(payload) {
		return nil, ErrInvalidRecord
	}

	value := append([]byte(nil), payload[offset:valueEnd]...)

	record := &Record{
		Type:     recordType,
		Sequence: sequence,
		Key:      key,
		Value:    value,
	}

	switch record.Type {
	case RecordPut:
	case RecordDelete:
		if len(record.Value) != 0 {
			return nil, ErrInvalidRecord
		}
	default:
		return nil, ErrInvalidRecord
	}

	return record, nil
}

// ReadRecord 从流中读取并校验一条完整 WAL 记录。
// 干净 EOF 原样返回 io.EOF；截断帧返回 io.ErrUnexpectedEOF；payload 超过 64 MiB 返回 ErrRecordTooLarge。
func ReadRecord(r io.Reader) (*Record, error) {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	payloadLength := binary.LittleEndian.Uint32(header[4:8])
	if payloadLength > maxRecordPayloadSize {
		return nil, ErrRecordTooLarge
	}

	// Decode 会复制 key 和 value，因此函数返回前即可安全归还读取缓冲。
	data := walRecordBufferPool.Get(headerSize + int(payloadLength))
	defer walRecordBufferPool.Put(data)
	copy(data[:headerSize], header[:])
	if _, err := io.ReadFull(r, data[headerSize:]); err != nil {
		return nil, err
	}
	return Decode(data)
}

// encodedRecordEnds 返回 data 中每条完整 record 结束位置。
// 它只检查帧边界和最大长度，不重复计算 checksum；false 表示 AppendLog 传入了旧式原始字节。
func encodedRecordEnds(data []byte) ([]int, bool) {
	if len(data) == 0 {
		return nil, true
	}
	ends := make([]int, 0, 1)
	offset := 0
	for offset < len(data) {
		if len(data)-offset < headerSize {
			return nil, false
		}
		payloadLength := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		if payloadLength > maxRecordPayloadSize {
			return nil, false
		}
		frameLength := headerSize + int(payloadLength)
		if frameLength > len(data)-offset {
			return nil, false
		}
		offset += frameLength
		ends = append(ends, offset)
	}
	return ends, true
}
