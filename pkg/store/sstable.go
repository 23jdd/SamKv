package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
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
	Magic = "流萤"

	// ReStartInterval 表示 DataBlock 前缀压缩时，每隔多少条记录写一个完整 key。
	ReStartInterval = 16

	sstableVersion       uint32 = 1
	defaultDataBlockSize        = 4 * 1024
	magicSize                   = len(Magic)
	versionOffset               = magicSize
	metaOffsetOffset            = versionOffset + 4
	metaSizeOffset              = metaOffsetOffset + 8
	indexOffsetOffset           = metaSizeOffset + 8
	indexSizeOffset             = indexOffsetOffset + 8
	footerSize                  = indexSizeOffset + 8
)

var (
	// ErrSSTableNotFound 表示查询的 key 不在当前 SSTable 中。
	ErrSSTableNotFound = errors.New("sstable: key not found")
	// ErrInvalidSSTable 表示 SSTable 文件格式非法或内容被截断。
	ErrInvalidSSTable = errors.New("sstable: invalid file")
)

// Record 是 SSTable 中最小的 key/value 记录。
// Deleted=true 表示墓碑记录：该 key 已被删除，用来覆盖旧 SSTable 中的旧值。
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
	RecordCount uint64
	MinKey      string
	MaxKey      string
	Filter      *BloomFilter
}

// Footer 固定大小，永远写在 SSTable 文件末尾。
// 打开文件时先读取 Footer，再找到 MetaBlock 和 IndexBlock。
type Footer struct {
	MetaHandle  BlockHandle
	IndexHandle BlockHandle
}

// SStable 表示一张不可变的 Sorted String Table。
// 内存构建时 rs 保存排序后的记录；从磁盘打开时主要依赖 file、index 和 meta 查询。
type SStable struct {
	path   string
	file   *os.File
	rs     []Record
	bf     *BloomFilter
	index  []IndexEntry
	meta   MetaBlock
	footer Footer
}

// NewSStable 在内存中创建一张 SSTable 描述对象。
// 它不会写磁盘，主要用于测试或构建阶段查看排序记录和 BloomFilter。
func NewSStable(rs []Record) (*SStable, error) {
	records := normalizeRecords(rs)
	bf, err := buildBloomFilter(records)
	if err != nil {
		return nil, err
	}

	s := &SStable{rs: records, bf: bf}
	if len(records) > 0 {
		s.meta = MetaBlock{
			RecordCount: uint64(len(records)),
			MinKey:      records[0].Key,
			MaxKey:      records[len(records)-1].Key,
			Filter:      bf,
		}
	}
	return s, nil
}

// WriteSStable 将 records 写成一个完整的 SSTable 文件。
// 写入顺序是 DataBlocks -> MetaBlock -> IndexBlock -> Footer。
func WriteSStable(path string, rs []Record) (*SStable, error) {
	records := normalizeRecords(rs)
	bf, err := buildBloomFilter(records)
	if err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
		}
	}()

	var offset uint64
	var index []IndexEntry
	for _, blockRecords := range splitDataBlocks(records, defaultDataBlockSize) {
		blockData, err := EncodeDataBlock(blockRecords)
		if err != nil {
			return nil, err
		}
		if err := writeAll(file, blockData); err != nil {
			return nil, err
		}
		index = append(index, IndexEntry{
			FirstKey: blockRecords[0].Key,
			LastKey:  blockRecords[len(blockRecords)-1].Key,
			Handle: BlockHandle{
				Offset: offset,
				Size:   uint64(len(blockData)),
			},
		})
		offset += uint64(len(blockData))
	}

	meta := MetaBlock{RecordCount: uint64(len(records)), Filter: bf}
	if len(records) > 0 {
		meta.MinKey = records[0].Key
		meta.MaxKey = records[len(records)-1].Key
	}
	metaData, err := encodeMetaBlock(meta)
	if err != nil {
		return nil, err
	}
	metaHandle := BlockHandle{Offset: offset, Size: uint64(len(metaData))}
	if err := writeAll(file, metaData); err != nil {
		return nil, err
	}
	offset += uint64(len(metaData))

	indexData, err := encodeIndexBlock(index)
	if err != nil {
		return nil, err
	}
	indexHandle := BlockHandle{Offset: offset, Size: uint64(len(indexData))}
	if err := writeAll(file, indexData); err != nil {
		return nil, err
	}
	offset += uint64(len(indexData))

	footer := Footer{MetaHandle: metaHandle, IndexHandle: indexHandle}
	footerData := encodeFooter(footer)
	if err := writeAll(file, footerData); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	ok = true

	return &SStable{
		path:   path,
		rs:     records,
		bf:     bf,
		index:  index,
		meta:   meta,
		footer: footer,
	}, nil
}

// OpenSStable 打开磁盘上的 SSTable 文件。
// 它只加载 Footer、MetaBlock 和 IndexBlock，DataBlock 会在查询时按需读取。
func OpenSStable(path string) (*SStable, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
		}
	}()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() < int64(footerSize) {
		return nil, ErrInvalidSSTable
	}

	footerData := make([]byte, footerSize)
	if _, err := file.ReadAt(footerData, stat.Size()-int64(footerSize)); err != nil {
		return nil, err
	}
	footer, err := decodeFooter(footerData)
	if err != nil {
		return nil, err
	}

	metaData, err := readBlock(file, footer.MetaHandle)
	if err != nil {
		return nil, err
	}
	meta, err := decodeMetaBlock(metaData)
	if err != nil {
		return nil, err
	}

	indexData, err := readBlock(file, footer.IndexHandle)
	if err != nil {
		return nil, err
	}
	index, err := decodeIndexBlock(indexData)
	if err != nil {
		return nil, err
	}

	ok = true
	return &SStable{
		path:   path,
		file:   file,
		bf:     meta.Filter,
		index:  index,
		meta:   meta,
		footer: footer,
	}, nil
}

// Close 关闭 SSTable 持有的文件句柄。
func (s *SStable) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// Get 查询 key 对应的 value。
// 查询流程：BloomFilter 快速排除 -> IndexBlock 定位 DataBlock -> 解码 DataBlock 后二分查找。
// 如果查询到墓碑，返回 ok=false。
func (s *SStable) Get(key string) (string, bool, error) {
	record, ok, err := s.GetRecord(key)
	if err != nil || !ok || record.Deleted {
		return "", false, err
	}
	return record.Val, true, nil
}

// GetRecord 查询 key 对应的原始 SSTable 记录。
// 返回的 Record 可能是墓碑，调用方需要检查 Deleted 字段。
func (s *SStable) GetRecord(key string) (Record, bool, error) {
	if s == nil {
		return Record{}, false, ErrInvalidSSTable
	}
	if s.bf != nil && !s.bf.ContainsString(key) {
		return Record{}, false, nil
	}

	if s.file == nil {
		idx := sort.Search(len(s.rs), func(i int) bool {
			return s.rs[i].Key >= key
		})
		if idx < len(s.rs) && s.rs[idx].Key == key {
			return s.rs[idx], true, nil
		}
		return Record{}, false, nil
	}

	entry, ok := s.findIndexEntry(key)
	if !ok {
		return Record{}, false, nil
	}
	blockData, err := readBlock(s.file, entry.Handle)
	if err != nil {
		return Record{}, false, err
	}
	records, err := DecodeDataBlock(blockData)
	if err != nil {
		return Record{}, false, err
	}
	idx := sort.Search(len(records), func(i int) bool {
		return records[i].Key >= key
	})
	if idx < len(records) && records[idx].Key == key {
		return records[idx], true, nil
	}
	return Record{}, false, nil
}

// Contains 判断 key 是否存在于当前 SSTable。
func (s *SStable) Contains(key string) (bool, error) {
	_, ok, err := s.Get(key)
	return ok, err
}

// Meta 返回 SSTable 的元数据快照。
func (s *SStable) Meta() MetaBlock {
	return s.meta
}

// Index 返回索引项副本，避免调用方修改内部索引。
func (s *SStable) Index() []IndexEntry {
	index := make([]IndexEntry, len(s.index))
	copy(index, s.index)
	return index
}

// findIndexEntry 根据 key 在 IndexBlock 中找到可能包含它的 DataBlock。
func (s *SStable) findIndexEntry(key string) (IndexEntry, bool) {
	idx := sort.Search(len(s.index), func(i int) bool {
		return s.index[i].LastKey >= key
	})
	if idx >= len(s.index) {
		return IndexEntry{}, false
	}
	entry := s.index[idx]
	if key < entry.FirstKey || key > entry.LastKey {
		return IndexEntry{}, false
	}
	return entry, true
}

// EncodeDataBlock 编码一个 DataBlock。
// 单条记录格式：sharedKeyLen、nonSharedKeyLen、valueLen、flags、keySuffix、value。
// block 末尾写 restart offsets 和 restart count，用于之后支持块内快速查找。
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
func DecodeRcWithTrie(rs []Record) []byte {
	data, _ := EncodeDataBlock(rs)
	return data
}

// SharedLen 返回两个 key 从头开始相同的字节数。
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
func encodeMetaBlock(meta MetaBlock) ([]byte, error) {
	if meta.Filter == nil {
		return nil, errors.New("sstable: missing bloom filter")
	}
	filterData, err := meta.Filter.MarshalBinary()
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
	if err := writeUint32(&buf, 0); err != nil {
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
	offset += 4

	if minKeyLen < 0 || maxKeyLen < 0 || filterLen < 0 {
		return MetaBlock{}, ErrInvalidSSTable
	}
	if minKeyLen > len(data)-offset || maxKeyLen > len(data)-offset-minKeyLen || filterLen > len(data)-offset-minKeyLen-maxKeyLen {
		return MetaBlock{}, ErrInvalidSSTable
	}

	minKey := string(data[offset : offset+minKeyLen])
	offset += minKeyLen
	maxKey := string(data[offset : offset+maxKeyLen])
	offset += maxKeyLen

	var filter BloomFilter
	if err := filter.UnmarshalBinary(data[offset : offset+filterLen]); err != nil {
		return MetaBlock{}, err
	}
	offset += filterLen
	if offset != len(data) {
		return MetaBlock{}, ErrInvalidSSTable
	}

	return MetaBlock{RecordCount: recordCount, MinKey: minKey, MaxKey: maxKey, Filter: &filter}, nil
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
	binary.LittleEndian.PutUint32(data[versionOffset:], sstableVersion)
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
	if version != sstableVersion {
		return Footer{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidSSTable, version)
	}
	return Footer{
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

// readBlock 根据 BlockHandle 从文件中读取完整 block。
func readBlock(file *os.File, handle BlockHandle) ([]byte, error) {
	if handle.Size > uint64(int(^uint(0)>>1)) {
		return nil, errors.New("sstable: block too large")
	}
	data := make([]byte, int(handle.Size))
	n, err := file.ReadAt(data, int64(handle.Offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if n != len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	return data, nil
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
