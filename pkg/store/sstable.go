package store

// 本文件定义 SSTable v1/v2 的稳定格式常量、核心类型和内存构造入口。
// 各 Block 编解码、writer、reader 分散在同包专用文件中，导出 API 与磁盘格式保持不变。

import (
	"errors"

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
