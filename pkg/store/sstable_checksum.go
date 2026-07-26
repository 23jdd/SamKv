package store

// 本文件实现 SSTable v2 Block 尾部的 CRC32C 编码与校验。
// 校验覆盖 Data、Meta 和 Index payload，不覆盖 Footer；v1 兼容读取不会要求该 trailer。

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const blockChecksumSize = 4

var (
	// ErrBlockChecksum 表示 block 长度可读但 CRC32C 与 payload 不匹配。
	ErrBlockChecksum   = errors.New("sstable: block checksum mismatch")
	blockChecksumTable = crc32.MakeTable(crc32.Castagnoli)
)

// encodeChecksummedBlock 在 block payload 后追加 CRC32-Castagnoli 校验值。
func encodeChecksummedBlock(payload []byte) []byte {
	encoded := make([]byte, len(payload)+blockChecksumSize)
	copy(encoded, payload)
	binary.LittleEndian.PutUint32(encoded[len(payload):], crc32.Checksum(payload, blockChecksumTable))
	return encoded
}

// verifyChecksummedBlock 校验 block 尾部的校验值并返回原始 payload。
// 小于 4 字节返回 ErrInvalidSSTable；校验失败返回 ErrBlockChecksum。返回切片与 encoded 共享内存。
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
