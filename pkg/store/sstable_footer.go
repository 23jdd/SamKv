package store

// 本文件定义固定大小 Footer 的编解码和 BlockHandle 边界校验。
// Magic、版本号和字段偏移属于持久化格式；旧版本读取兼容通过 Footer.Version 决定。

import (
	"encoding/binary"
	"fmt"
)

// encodeFooter 编码固定大小 Footer。
func encodeFooter(footer Footer) []byte {
	data := make([]byte, footerSize)
	copy(data[:magicSize], []byte(Magic))
	version := footer.Version
	if version == 0 {
		version = currentSSTableVersion
	}
	binary.LittleEndian.PutUint32(data[versionOffset:], version)
	binary.LittleEndian.PutUint64(data[metaOffsetOffset:], footer.MetaHandle.Offset)
	binary.LittleEndian.PutUint64(data[metaSizeOffset:], footer.MetaHandle.Size)
	binary.LittleEndian.PutUint64(data[indexOffsetOffset:], footer.IndexHandle.Offset)
	binary.LittleEndian.PutUint64(data[indexSizeOffset:], footer.IndexHandle.Size)
	return data
}

// decodeFooter 解码 Footer，并校验 magic 和版本号。
func decodeFooter(data []byte) (Footer, error) {
	if len(data) != footerSize {
		return Footer{}, ErrInvalidSSTable
	}
	if string(data[:magicSize]) != Magic {
		return Footer{}, fmt.Errorf("%w: bad magic", ErrInvalidSSTable)
	}
	version := binary.LittleEndian.Uint32(data[versionOffset:])
	if version < legacySSTableVersion || version > currentSSTableVersion {
		return Footer{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidSSTable, version)
	}
	return Footer{
		Version: version,
		MetaHandle: BlockHandle{
			Offset: binary.LittleEndian.Uint64(data[metaOffsetOffset:]),
			Size:   binary.LittleEndian.Uint64(data[metaSizeOffset:]),
		},
		IndexHandle: BlockHandle{
			Offset: binary.LittleEndian.Uint64(data[indexOffsetOffset:]),
			Size:   binary.LittleEndian.Uint64(data[indexSizeOffset:]),
		},
	}, nil
}

// validateFooterHandles 校验元数据块和索引块都位于 Footer 之前且互不重叠。
func validateFooterHandles(footer Footer, footerOffset uint64) error {
	if err := validateBlockHandle(footer.MetaHandle, footerOffset); err != nil {
		return err
	}
	if err := validateBlockHandle(footer.IndexHandle, footerOffset); err != nil {
		return err
	}
	if footer.MetaHandle.Offset+footer.MetaHandle.Size > footer.IndexHandle.Offset {
		return ErrInvalidSSTable
	}
	return nil
}

// validateBlockHandle 使用减法校验范围，避免 Offset+Size 发生整数溢出。
func validateBlockHandle(handle BlockHandle, limit uint64) error {
	if handle.Offset > limit || handle.Size > limit-handle.Offset {
		return ErrInvalidSSTable
	}
	return nil
}
