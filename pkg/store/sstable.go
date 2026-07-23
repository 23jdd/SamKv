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

const (
	Magic           = "SAMKVST1"
	ReStartInterval = 16

	sstableVersion       uint32 = 1
	defaultDataBlockSize        = 4 * 1024
	footerSize                  = 48
)

var (
	ErrSSTableNotFound = errors.New("sstable: key not found")
	ErrInvalidSSTable  = errors.New("sstable: invalid file")
)

type Record struct {
	Key string
	Val string
}

type BlockHandle struct {
	Offset uint64
	Size   uint64
}

type IndexEntry struct {
	FirstKey string
	LastKey  string
	Handle   BlockHandle
}

type MetaBlock struct {
	RecordCount uint64
	MinKey      string
	MaxKey      string
	Filter      *BloomFilter
}

type Footer struct {
	MetaHandle  BlockHandle
	IndexHandle BlockHandle
}

type SStable struct {
	path   string
	file   *os.File
	rs     []Record
	bf     *BloomFilter
	index  []IndexEntry
	meta   MetaBlock
	footer Footer
}

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
	if stat.Size() < footerSize {
		return nil, ErrInvalidSSTable
	}

	footerData := make([]byte, footerSize)
	if _, err := file.ReadAt(footerData, stat.Size()-footerSize); err != nil {
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

func (s *SStable) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *SStable) Get(key string) (string, bool, error) {
	if s == nil {
		return "", false, ErrInvalidSSTable
	}
	if s.bf != nil && !s.bf.ContainsString(key) {
		return "", false, nil
	}

	if s.file == nil {
		idx := sort.Search(len(s.rs), func(i int) bool {
			return s.rs[i].Key >= key
		})
		if idx < len(s.rs) && s.rs[idx].Key == key {
			return s.rs[idx].Val, true, nil
		}
		return "", false, nil
	}

	entry, ok := s.findIndexEntry(key)
	if !ok {
		return "", false, nil
	}
	blockData, err := readBlock(s.file, entry.Handle)
	if err != nil {
		return "", false, err
	}
	records, err := DecodeDataBlock(blockData)
	if err != nil {
		return "", false, err
	}
	idx := sort.Search(len(records), func(i int) bool {
		return records[i].Key >= key
	})
	if idx < len(records) && records[idx].Key == key {
		return records[idx].Val, true, nil
	}
	return "", false, nil
}

func (s *SStable) Contains(key string) (bool, error) {
	_, ok, err := s.Get(key)
	return ok, err
}

func (s *SStable) Meta() MetaBlock {
	return s.meta
}

func (s *SStable) Index() []IndexEntry {
	index := make([]IndexEntry, len(s.index))
	copy(index, s.index)
	return index
}

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
		if entriesEnd-offset < 12 {
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
		if nonShared > entriesEnd-offset || valueLen > entriesEnd-offset-nonShared {
			return nil, ErrInvalidSSTable
		}

		key := make([]byte, shared+nonShared)
		copy(key, lastKey[:shared])
		copy(key[shared:], data[offset:offset+nonShared])
		offset += nonShared

		value := data[offset : offset+valueLen]
		offset += valueLen

		records = append(records, Record{Key: string(key), Val: string(value)})
		lastKey = key
	}
	if offset != entriesEnd {
		return nil, ErrInvalidSSTable
	}
	return records, nil
}

// DecodeRcWithTrie keeps the old helper name, but it actually encodes records
// with prefix compression and restart points.
func DecodeRcWithTrie(rs []Record) []byte {
	data, _ := EncodeDataBlock(rs)
	return data
}

func SharedLen(target []byte, source []byte) int {
	ml := min(len(target), len(source))
	for i := 0; i < ml; i++ {
		if target[i] != source[i] {
			return i
		}
	}
	return ml
}

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

func encodeFooter(footer Footer) []byte {
	data := make([]byte, footerSize)
	copy(data[0:8], []byte(Magic))
	binary.LittleEndian.PutUint32(data[8:], sstableVersion)
	binary.LittleEndian.PutUint64(data[16:], footer.MetaHandle.Offset)
	binary.LittleEndian.PutUint64(data[24:], footer.MetaHandle.Size)
	binary.LittleEndian.PutUint64(data[32:], footer.IndexHandle.Offset)
	binary.LittleEndian.PutUint64(data[40:], footer.IndexHandle.Size)
	return data
}

func decodeFooter(data []byte) (Footer, error) {
	if len(data) != footerSize {
		return Footer{}, ErrInvalidSSTable
	}
	if string(data[0:8]) != Magic {
		return Footer{}, fmt.Errorf("%w: bad magic", ErrInvalidSSTable)
	}
	version := binary.LittleEndian.Uint32(data[8:])
	if version != sstableVersion {
		return Footer{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidSSTable, version)
	}
	return Footer{
		MetaHandle: BlockHandle{
			Offset: binary.LittleEndian.Uint64(data[16:]),
			Size:   binary.LittleEndian.Uint64(data[24:]),
		},
		IndexHandle: BlockHandle{
			Offset: binary.LittleEndian.Uint64(data[32:]),
			Size:   binary.LittleEndian.Uint64(data[40:]),
		},
	}, nil
}

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

func writeUint32(w io.Writer, v uint32) error {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

func writeUint64(w io.Writer, v uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}
