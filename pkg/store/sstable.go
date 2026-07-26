package store

// 本文件实现 SSTable v1/v2 的 DataBlock、MetaBlock、IndexBlock、Footer 编解码和点查。
// 写入始终产生带 CRC32C 的当前版本；打开时只加载索引和元数据，DataBlock 按需读取。

import (
	"bytes"

	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// SSTable 文件布局：
//
//	[DataBlock 1][DataBlock 2]...[DataBlock N]
//	[MetaBlock]
//	[IndexBlock]
//	[Footer]
//
// Footer 固定放在文件末尾，里面保存 MetaBlock 和 IndexBlock 的位置。
// 打开文件时先读 Footer，再根据 Footer 定位索引和元数据。
const (
	// Magic 用于识别当前文件是否是 SamKV 的 SSTable 文件。
	// 当前 UTF-8 编码恰好占 6 字节；修改它会改变 Footer 大小，必须配合新格式版本。
	Magic = "流萤"

	// ReStartInterval 表示 DataBlock 前缀压缩时，每隔多少条记录写一个完整 key。
	// 它属于磁盘格式的一部分，调整后仍可顺序解码，但会改变新文件的压缩率和重启点布局。
	ReStartInterval = 16

	legacySSTableVersion  uint32 = 1
	currentSSTableVersion uint32 = 2
	defaultDataBlockSize         = 4 * 1024
	magicSize                    = len(Magic)
	versionOffset                = magicSize
	metaOffsetOffset             = versionOffset + 4
	metaSizeOffset               = metaOffsetOffset + 8
	indexOffsetOffset            = metaSizeOffset + 8
	indexSizeOffset              = indexOffsetOffset + 8
	footerSize                   = indexSizeOffset + 8
)

var (
	// ErrSSTableNotFound 表示查询的 key 不在当前 SSTable 中。
	ErrSSTableNotFound = errors.New("sstable: key not found")
	// ErrInvalidSSTable 表示 SSTable 文件格式非法或内容被截断。
	ErrInvalidSSTable = errors.New("sstable: invalid file")
)

// Record 是 SSTable 中最小的 key/value 记录。
// Deleted=true 表示墓碑记录：该 key 已被删除，用来覆盖旧 SSTable 中的旧值。
// 普通记录允许空 Value；墓碑的 Val 会被编码但业务上应保持为空。
type Record struct {
	Key     string
	Val     string
	Deleted bool
}

// BlockHandle 描述一个 block 在 SSTable 文件中的物理位置。
// Offset 是相对文件起始位置的偏移量，Size 是 block 字节长度。
type BlockHandle struct {
	Offset uint64
	Size   uint64
}

// IndexEntry 是 IndexBlock 中的一条索引。
// 它记录一个 DataBlock 的 key 范围，以及这个 DataBlock 的文件位置。
type IndexEntry struct {
	FirstKey string
	LastKey  string
	Handle   BlockHandle
}

// MetaBlock 保存整张 SSTable 的元数据。
// 当前包含 key 范围、记录数量和 BloomFilter，后续可以扩展时间范围等信息。
type MetaBlock struct {
	RecordCount      uint64
	MinKey           string
	MaxKey           string
	Filter           *BloomFilter
	HasTimeRange     bool
	MinTimestamp     int64
	MaxTimestamp     int64
	LabelFilter      *BloomFilter
	LabelCardinality map[string]uint64
}

// Footer 固定大小，永远写在 SSTable 文件末尾。
// 打开文件时先读取 Footer，再找到 MetaBlock 和 IndexBlock。
type Footer struct {
	Version     uint32
	MetaHandle  BlockHandle
	IndexHandle BlockHandle
}

// SStable 表示一张不可变的 Sorted String Table。
// 内存构建时 rs 保存排序后的记录；从磁盘打开时主要依赖 file、index 和 meta 查询。
// Get/Scan 可并发读取；Close 不得与读取并发，并且应在对象不再使用时调用。
type SStable struct {
	path    string
	file    *os.File
	version uint32
	rs      []Record
	bf      *BloomFilter
	index   []IndexEntry
	meta    MetaBlock
	footer  Footer
	cache   *BlockCache
}

// NewSStable 在内存中创建一张 SSTable 描述对象。
// 它不会写磁盘，主要用于测试或构建阶段查看排序记录和 BloomFilter。
// 输入可以无序或为空；函数复制切片、按 key 排序，并让重复 key 的最后一个输入版本获胜。
func NewSStable(rs []Record) (*SStable, error) {
	records := normalizeRecords(rs)
	bf, err := buildBloomFilter(records)
	if err != nil {
		return nil, err
	}

	meta, err := buildSSTableMeta(records, bf)
	if err != nil {
		return nil, err
	}
	return &SStable{rs: records, bf: bf, meta: meta}, nil
}

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

// normalizeRecords 对记录按 key 排序，并合并重复 key。
// 重复 key 保留排序后遇到的最后一条记录，符合后写覆盖前写的语义。
func encodeMetaBlock(meta MetaBlock) ([]byte, error) {
	if meta.Filter == nil {
		return nil, errors.New("sstable: missing bloom filter")
	}
	filterData, err := meta.Filter.MarshalBinary()
	if err != nil {
		return nil, err
	}
	extensionData, err := encodeMetaExtension(meta)
	if err != nil {
		return nil, err
	}

	minKey := []byte(meta.MinKey)
	maxKey := []byte(meta.MaxKey)
	var buf bytes.Buffer
	if err := writeUint64(&buf, meta.RecordCount); err != nil {
		return nil, err
	}
	if err := writeUint32(&buf, uint32(len(minKey))); err != nil {
		return nil, err
	}
	if err := writeUint32(&buf, uint32(len(maxKey))); err != nil {
		return nil, err
	}
	if err := writeUint32(&buf, uint32(len(filterData))); err != nil {
		return nil, err
	}
	if err := writeUint32(&buf, uint32(len(extensionData))); err != nil {
		return nil, err
	}
	if _, err := buf.Write(minKey); err != nil {
		return nil, err
	}
	if _, err := buf.Write(maxKey); err != nil {
		return nil, err
	}
	if _, err := buf.Write(filterData); err != nil {
		return nil, err
	}
	if _, err := buf.Write(extensionData); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeMetaBlock 解码 MetaBlock，并恢复 BloomFilter。
func decodeMetaBlock(data []byte) (MetaBlock, error) {
	if len(data) < 24 {
		return MetaBlock{}, ErrInvalidSSTable
	}
	offset := 0
	recordCount := binary.LittleEndian.Uint64(data[offset:])
	offset += 8
	minKeyLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	maxKeyLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	filterLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	extensionLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4

	if minKeyLen < 0 || maxKeyLen < 0 || filterLen < 0 || extensionLen < 0 {
		return MetaBlock{}, ErrInvalidSSTable
	}
	if minKeyLen > len(data)-offset {
		return MetaBlock{}, ErrInvalidSSTable
	}
	minKey := string(data[offset : offset+minKeyLen])
	offset += minKeyLen
	if maxKeyLen > len(data)-offset {
		return MetaBlock{}, ErrInvalidSSTable
	}
	maxKey := string(data[offset : offset+maxKeyLen])
	offset += maxKeyLen
	if filterLen > len(data)-offset {
		return MetaBlock{}, ErrInvalidSSTable
	}

	var filter BloomFilter
	if err := filter.UnmarshalBinary(data[offset : offset+filterLen]); err != nil {
		return MetaBlock{}, err
	}
	offset += filterLen
	if extensionLen > len(data)-offset {
		return MetaBlock{}, ErrInvalidSSTable
	}

	meta := MetaBlock{RecordCount: recordCount, MinKey: minKey, MaxKey: maxKey, Filter: &filter}
	if extensionLen > 0 {
		if err := decodeMetaExtension(data[offset:offset+extensionLen], &meta); err != nil {
			return MetaBlock{}, err
		}
	}
	offset += extensionLen
	if offset != len(data) {
		return MetaBlock{}, ErrInvalidSSTable
	}
	return meta, nil
}

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

// writeAll 保证 data 被完整写入，避免短写被当作成功。
func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// writeUint32 以小端序写入 uint32。
func writeUint32(w io.Writer, v uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

// writeUint64 以小端序写入 uint64。
func writeUint64(w io.Writer, v uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}
