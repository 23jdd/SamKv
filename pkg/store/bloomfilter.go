package store

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"sync"
)

const bloomFilterVersion uint32 = 1

// BloomFilter 是一个并发安全的布隆过滤器。
//
// Bloom Filter 的判断结果：
//   - Contains 返回 false：元素一定不存在
//   - Contains 返回 true：元素可能存在
type BloomFilter struct {
	mu sync.RWMutex

	// m 是 bit 数量。
	m uint64

	// k 是 hash 函数数量。
	k uint64

	// bits 用 uint64 数组保存 bit set。
	bits []uint64

	// count 是已经添加的元素数量。
	// 同一个元素重复添加也会增加 count，因此它只是近似值。
	count uint64
}

// NewBloomFilter 根据预计元素数量和目标误判率创建 Bloom Filter。
//
// expectedItems 必须大于 0。
// falsePositiveRate 必须位于 (0, 1) 之间。
//
// 示例：
//
//	bf, err := NewBloomFilter(100_000, 0.01)
func NewBloomFilter(
	expectedItems uint64,
	falsePositiveRate float64,
) (*BloomFilter, error) {
	if expectedItems == 0 {
		return nil, errors.New("bloomfilter: expectedItems must be greater than 0")
	}

	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		return nil, errors.New(
			"bloomfilter: falsePositiveRate must be between 0 and 1",
		)
	}

	// 最优 bit 数量：
	//
	// m = -n * ln(p) / (ln(2)^2)
	m := uint64(math.Ceil(
		-float64(expectedItems) *
			math.Log(falsePositiveRate) /
			(math.Ln2 * math.Ln2),
	))

	if m == 0 {
		m = 1
	}

	// 为了方便使用 uint64 数组，让 m 对齐到 64 bit。
	wordCount := (m + 63) / 64
	m = wordCount * 64

	// 最优 hash 数量：
	//
	// k = m/n * ln(2)
	k := uint64(math.Round(
		float64(m) / float64(expectedItems) * math.Ln2,
	))

	if k == 0 {
		k = 1
	}

	return &BloomFilter{
		m:    m,
		k:    k,
		bits: make([]uint64, wordCount),
	}, nil
}

// NewBloomFilterWithSize 使用指定 bit 数量和 hash 数量创建 Bloom Filter。
//
// bitSize 表示 Bloom Filter 总 bit 数。
// hashCount 表示每个 key 计算多少个 bit 位置。
func NewBloomFilterWithSize(
	bitSize uint64,
	hashCount uint64,
) (*BloomFilter, error) {
	if bitSize == 0 {
		return nil, errors.New("bloomfilter: bitSize must be greater than 0")
	}

	if hashCount == 0 {
		return nil, errors.New("bloomfilter: hashCount must be greater than 0")
	}

	wordCount := (bitSize + 63) / 64
	bitSize = wordCount * 64

	return &BloomFilter{
		m:    bitSize,
		k:    hashCount,
		bits: make([]uint64, wordCount),
	}, nil
}

// Add 向 Bloom Filter 中添加一个 key。
func (b *BloomFilter) Add(key []byte) {
	if len(key) == 0 {
		return
	}

	h1, h2 := bloomHashes(key)

	b.mu.Lock()
	defer b.mu.Unlock()

	for i := uint64(0); i < b.k; i++ {
		index := bloomIndex(h1, h2, i, b.m)
		b.setBit(index)
	}

	b.count++
}

// AddString 添加字符串 key。
func (b *BloomFilter) AddString(key string) {
	b.Add([]byte(key))
}

// Contains 判断 key 是否可能存在。
//
// 返回 false 表示 key 一定不存在。
// 返回 true 表示 key 可能存在。
func (b *BloomFilter) Contains(key []byte) bool {
	if len(key) == 0 {
		return false
	}

	h1, h2 := bloomHashes(key)

	b.mu.RLock()
	defer b.mu.RUnlock()

	for i := uint64(0); i < b.k; i++ {
		index := bloomIndex(h1, h2, i, b.m)

		if !b.hasBit(index) {
			return false
		}
	}

	return true
}

// ContainsString 判断字符串 key 是否可能存在。
func (b *BloomFilter) ContainsString(key string) bool {
	return b.Contains([]byte(key))
}

// Reset 清空 Bloom Filter。
func (b *BloomFilter) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	clear(b.bits)
	b.count = 0
}

// BitSize 返回 Bloom Filter 的总 bit 数量。
func (b *BloomFilter) BitSize() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.m
}

// HashCount 返回 hash 函数数量。
func (b *BloomFilter) HashCount() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.k
}

// Count 返回 Add 调用次数。
//
// 注意：重复添加同一个 key 也会增加 Count。
func (b *BloomFilter) Count() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.count
}

// EstimatedFalsePositiveRate 计算当前近似误判率。
func (b *BloomFilter) EstimatedFalsePositiveRate() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.m == 0 {
		return 1
	}

	// p = (1 - e^(-kn/m))^k
	rate := math.Pow(
		1-math.Exp(
			-float64(b.k)*float64(b.count)/float64(b.m),
		),
		float64(b.k),
	)

	return rate
}

// MarshalBinary 将 Bloom Filter 序列化为二进制。
//
// 二进制格式：
//
//	[4 bytes]  version
//	[8 bytes]  m
//	[8 bytes]  k
//	[8 bytes]  count
//	[8 bytes]  wordCount
//	[N bytes]  bits
func (b *BloomFilter) MarshalBinary() ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	wordCount := uint64(len(b.bits))

	const headerSize = 4 + 8 + 8 + 8 + 8

	if wordCount > uint64((int(^uint(0)>>1)-headerSize)/8) {
		return nil, errors.New("bloomfilter: data is too large")
	}

	data := make([]byte, headerSize+int(wordCount)*8)

	offset := 0

	binary.LittleEndian.PutUint32(
		data[offset:],
		bloomFilterVersion,
	)
	offset += 4

	binary.LittleEndian.PutUint64(data[offset:], b.m)
	offset += 8

	binary.LittleEndian.PutUint64(data[offset:], b.k)
	offset += 8

	binary.LittleEndian.PutUint64(data[offset:], b.count)
	offset += 8

	binary.LittleEndian.PutUint64(data[offset:], wordCount)
	offset += 8

	for _, word := range b.bits {
		binary.LittleEndian.PutUint64(data[offset:], word)
		offset += 8
	}

	return data, nil
}

// UnmarshalBinary 从二进制恢复 Bloom Filter。
func (b *BloomFilter) UnmarshalBinary(data []byte) error {
	const headerSize = 4 + 8 + 8 + 8 + 8

	if len(data) < headerSize {
		return errors.New("bloomfilter: invalid binary data")
	}

	offset := 0

	version := binary.LittleEndian.Uint32(data[offset:])
	offset += 4

	if version != bloomFilterVersion {
		return errors.New("bloomfilter: unsupported version")
	}

	m := binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	k := binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	count := binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	wordCount := binary.LittleEndian.Uint64(data[offset:])
	offset += 8

	if m == 0 {
		return errors.New("bloomfilter: invalid bit size")
	}

	if k == 0 {
		return errors.New("bloomfilter: invalid hash count")
	}

	if wordCount == 0 {
		return errors.New("bloomfilter: invalid word count")
	}

	expectedWords := (m + 63) / 64
	if wordCount != expectedWords {
		return errors.New("bloomfilter: bit size does not match word count")
	}

	if wordCount > uint64((len(data)-headerSize)/8) {
		return errors.New("bloomfilter: truncated binary data")
	}

	expectedLength := headerSize + int(wordCount)*8
	if len(data) != expectedLength {
		return errors.New("bloomfilter: invalid binary data length")
	}

	bits := make([]uint64, wordCount)

	for i := range bits {
		bits[i] = binary.LittleEndian.Uint64(data[offset:])
		offset += 8
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.m = m
	b.k = k
	b.count = count
	b.bits = bits

	return nil
}

// setBit 设置指定位置的 bit。
// 调用者必须持有写锁。
func (b *BloomFilter) setBit(index uint64) {
	wordIndex := index / 64
	bitOffset := index % 64

	b.bits[wordIndex] |= uint64(1) << bitOffset
}

// hasBit 判断指定位置的 bit 是否为 1。
// 调用者必须持有读锁或写锁。
func (b *BloomFilter) hasBit(index uint64) bool {
	wordIndex := index / 64
	bitOffset := index % 64

	return b.bits[wordIndex]&(uint64(1)<<bitOffset) != 0
}

// bloomIndex 使用 double hashing 生成第 i 个位置。
//
// index(i) = h1 + i*h2 + i²
//
// 添加 i² 可以避免 h2 的一些特殊值导致位置重复。
func bloomIndex(
	h1 uint64,
	h2 uint64,
	i uint64,
	bitSize uint64,
) uint64 {
	return (h1 + i*h2 + i*i) % bitSize
}

// bloomHashes 为一个 key 计算两个基础 hash。
//
// 使用两个不同 offset basis 的 FNV-1a。
// 之后通过 double hashing 产生 k 个 hash 位置。
func bloomHashes(key []byte) (uint64, uint64) {
	first := fnv.New64a()
	_, _ = first.Write(key)
	h1 := first.Sum64()

	second := fnv.New64()
	_, _ = second.Write(key)
	h2 := second.Sum64()

	// h2 不能为 0，否则 double hashing 的所有位置可能相同。
	if h2 == 0 {
		h2 = 0x9e3779b97f4a7c15
	}

	return h1, h2
}
