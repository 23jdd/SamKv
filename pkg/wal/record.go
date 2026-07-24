package wal

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"

	bufferpool "github.com/23jdd/SamKv/pkg/pool"
)

type RecordType uint8

const (
	RecordPut RecordType = iota + 1
	RecordDelete
)

const (
	headerSize          = 8 // CRC32 + PayloadLength
	fixedPayloadSize    = 17
	minRecordBufferSize = 4 * 1024 // 恢复小记录时仍从 4 KiB 池开始复用。
)

var (
	ErrInvalidRecord  = errors.New("invalid wal record")
	ErrChecksum       = errors.New("wal checksum mismatch")
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

type Record struct {
	Type     RecordType
	Sequence uint64
	Key      []byte
	Value    []byte
}

func PutRecord(key []byte, val []byte) *Record {
	return &Record{Type: RecordPut, Key: key, Value: val}
}
func DeleteRecord(key []byte) *Record {
	return &Record{Type: RecordDelete, Key: key}
}
func (r *Record) Encode() ([]byte, error) {
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
func ReadRecord(r io.Reader) (*Record, error) {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	payloadLength := binary.LittleEndian.Uint32(header[4:8])
	const maxRecordSize = 64 << 20 // 64 MiB
	if payloadLength > maxRecordSize {
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
