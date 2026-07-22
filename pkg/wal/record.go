package wal


import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

type RecordType uint8

const (
	RecordPut RecordType = iota + 1
	RecordDelete
)

const (
	headerSize       = 8 // CRC32 + PayloadLength
	fixedPayloadSize = 17
)

var (
	ErrInvalidRecord  = errors.New("invalid wal record")
	ErrChecksum       = errors.New("wal checksum mismatch")
	ErrRecordTooLarge = errors.New("wal record too large")
)

type Record struct {
	Type     RecordType
	Sequence uint64
	Key      []byte
	Value    []byte
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

func ReadRecord(r io.Reader) (*Record, error) {
	header := make([]byte, headerSize)

	_, err := io.ReadFull(r, header)
	if err != nil {
		return nil, err
	}

	payloadLength := binary.LittleEndian.Uint32(header[4:8])

	const maxRecordSize = 64 << 20 // 64 MiB

	if payloadLength > maxRecordSize {
		return nil, ErrRecordTooLarge
	}

	payload := make([]byte, payloadLength)

	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	data := make([]byte, 0, headerSize+len(payload))
	data = append(data, header...)
	data = append(data, payload...)

	return Decode(data)
}