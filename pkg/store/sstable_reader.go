package store

// 本文件负责 SSTable 打开、Footer 与 BlockHandle 校验、点查、Block Cache 和读取缓冲复用。
// 打开时只加载 MetaBlock/IndexBlock；DataBlock 在 Get 或 Scan 时按需读取并校验 CRC32C。

import (
	"errors"
	"io"
	"os"
	"sort"

	bufferpool "github.com/23jdd/SamKv/pkg/pool"
)

// sstableBlockBufferPool 复用 SSTable 读取缓冲；超出最大桶的超大 Block 不进入池。
var sstableBlockBufferPool = bufferpool.NewTieredPool(
	defaultDataBlockSize,
	16*1024,
	64*1024,
	256*1024,
	1024*1024,
)

// OpenSStable 打开并验证一张磁盘 SSTable。
// 文件过短、Footer 非法、Block 越界或 Meta/Index 校验失败时返回 ErrInvalidSSTable 或具体 I/O 错误。
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
	footerOffset := uint64(stat.Size() - int64(footerSize))
	if err := validateFooterHandles(footer, footerOffset); err != nil {
		return nil, err
	}

	metaData, err := readBlock(file, footer.MetaHandle, footer.Version >= currentSSTableVersion)
	if err != nil {
		return nil, err
	}
	meta, err := decodeMetaBlock(metaData)
	releaseBlock(metaData)
	if err != nil {
		return nil, err
	}

	indexData, err := readBlock(file, footer.IndexHandle, footer.Version >= currentSSTableVersion)
	if err != nil {
		return nil, err
	}
	index, err := decodeIndexBlock(indexData)
	releaseBlock(indexData)
	if err != nil {
		return nil, err
	}
	for _, entry := range index {
		if err := validateBlockHandle(entry.Handle, footer.MetaHandle.Offset); err != nil {
			return nil, err
		}
	}

	ok = true
	return &SStable{
		path:    path,
		file:    file,
		version: footer.Version,
		bf:      meta.Filter,
		index:   index,
		meta:    meta,
		footer:  footer,
	}, nil
}

// SetBlockCache 设置 Store 共享的只读 Block Cache。
// 传入 nil 会禁用该表后续读取缓存；Store 应在表发布给并发读者前完成设置。
func (s *SStable) SetBlockCache(cache *BlockCache) {
	if s != nil {
		s.cache = cache
	}
}

// Close 关闭 SSTable 持有的文件句柄。
// 内存表和 nil 接收者返回 nil；磁盘表只应关闭一次，且不能与 Get/Scan 并发。
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
// 不存在和墓碑都返回空字符串、false；需要查看墓碑时使用 GetRecord。磁盘损坏通过 error 返回。
func (s *SStable) Get(key string) (string, bool, error) {
	record, ok, err := s.GetRecord(key)
	if err != nil || !ok || record.Deleted {
		return "", false, err
	}
	return record.Val, true, nil
}

// GetRecord 查询 key 对应的原始 SSTable 记录。
// 返回的 Record 可能是墓碑，调用方需要检查 Deleted 字段。
// nil 表返回 ErrInvalidSSTable；Bloom Filter 或 key 范围排除时返回零值、false、nil。
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
	blockData, release, err := s.readDataBlock(entry.Handle, true)
	if err != nil {
		return Record{}, false, err
	}
	records, err := DecodeDataBlock(blockData)
	release()
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

func (s *SStable) readDataBlock(handle BlockHandle, useCache bool) ([]byte, func(), error) {
	key := blockCacheKey{
		path:    s.path,
		offset:  handle.Offset,
		size:    handle.Size,
		version: s.Version(),
	}
	if useCache {
		if data, ok := s.cache.get(key); ok {
			return data, func() {}, nil
		}
	}
	data, err := readBlock(s.file, handle, s.version >= currentSSTableVersion)
	if err != nil {
		return nil, func() {}, err
	}
	if useCache {
		s.cache.put(key, data)
	}
	return data, func() { releaseBlock(data) }, nil
}

// Contains 判断 key 是否存在于当前 SSTable。
// 墓碑按不存在处理；磁盘校验或读取错误会原样返回。
func (s *SStable) Contains(key string) (bool, error) {
	_, ok, err := s.Get(key)
	return ok, err
}

// Version 返回当前 SSTable 的磁盘格式版本。
// nil 接收者返回 0；内存构建或未显式设置版本的表按当前版本报告。
func (s *SStable) Version() uint32 {
	if s == nil {
		return 0
	}
	if s.version == 0 {
		return currentSSTableVersion
	}
	return s.version
}

// Meta 返回 SSTable 的元数据快照。
// LabelCardinality map 会复制；BloomFilter 指针是只读共享对象，调用方不得 Reset 或 Add。
func (s *SStable) Meta() MetaBlock {
	meta := s.meta
	if s.meta.LabelCardinality != nil {
		meta.LabelCardinality = make(map[string]uint64, len(s.meta.LabelCardinality))
		for name, cardinality := range s.meta.LabelCardinality {
			meta.LabelCardinality[name] = cardinality
		}
	}
	return meta
}

// Index 返回索引项副本，避免调用方修改内部索引。
// IndexEntry 中的字符串不可变；BlockHandle 仅用于诊断，不应绕过校验直接读取文件。
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
// rs 通常应按 key 严格递增；函数可编码空切片，但该结果不代表一个含记录的 DataBlock。
// readBlock 根据 BlockHandle 从文件中读取完整 block。
// 返回的缓冲来自分级池，调用方解码完成后必须调用 releaseBlock。
func readBlock(file *os.File, handle BlockHandle, checksummed bool) ([]byte, error) {
	if handle.Size > uint64(int(^uint(0)>>1)) {
		return nil, errors.New("sstable: block too large")
	}
	data := sstableBlockBufferPool.Get(int(handle.Size))
	n, err := file.ReadAt(data, int64(handle.Offset))
	if err != nil && !errors.Is(err, io.EOF) {
		sstableBlockBufferPool.Put(data)
		return nil, err
	}
	if n != len(data) {
		sstableBlockBufferPool.Put(data)
		return nil, io.ErrUnexpectedEOF
	}
	if checksummed {
		payload, err := verifyChecksummedBlock(data)
		if err != nil {
			sstableBlockBufferPool.Put(data)
			return nil, err
		}
		return payload, nil
	}
	return data, nil
}

// releaseBlock 归还只在解码期间使用的 SSTable Block 缓冲。
func releaseBlock(data []byte) {
	sstableBlockBufferPool.Put(data)
}
