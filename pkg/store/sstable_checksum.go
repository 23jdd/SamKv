package store

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const blockChecksumSize = 4

var (
	ErrBlockChecksum   = errors.New("sstable: block checksum mismatch")
	blockChecksumTable = crc32.MakeTable(crc32.Castagnoli)
)

// encodeChecksummedBlock ? block payload ??? CRC32-Castagnoli?
func encodeChecksummedBlock(payload []byte) []byte {
	encoded := make([]byte, len(payload)+blockChecksumSize)
	copy(encoded, payload)
	binary.LittleEndian.PutUint32(encoded[len(payload):], crc32.Checksum(payload, blockChecksumTable))
	return encoded
}

// verifyChecksummedBlock ?? block ???????????????? payload?
func verifyChecksummedBlock(encoded []byte) ([]byte, error) {
	if len(encoded) < blockChecksumSize {
		return nil, ErrInvalidSSTable
	}
	payload := encoded[:len(encoded)-blockChecksumSize]
	want := binary.LittleEndian.Uint32(encoded[len(payload):])
	got := crc32.Checksum(payload, blockChecksumTable)
	if got != want {
		return nil, ErrBlockChecksum
	}
	return payload, nil
}
