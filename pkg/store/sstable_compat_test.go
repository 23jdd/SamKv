package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSStableSupportsLegacyVersionOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sst")
	if err := writeLegacySStableForTest(path, []Record{
		{Key: "a", Val: "one"},
		{Key: "b", Val: "two"},
	}); err != nil {
		t.Fatal(err)
	}

	table, err := OpenSStable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	if table.Version() != legacySSTableVersion {
		t.Fatalf("version = %d, want %d", table.Version(), legacySSTableVersion)
	}
	if value, ok, err := table.Get("b"); err != nil || !ok || value != "two" {
		t.Fatalf("Get(b) = %q, %v, %v", value, ok, err)
	}
}

func TestDecodeFooterRejectsFutureVersion(t *testing.T) {
	data := encodeFooter(Footer{Version: currentSSTableVersion + 1})
	if _, err := decodeFooter(data); !errors.Is(err, ErrInvalidSSTable) {
		t.Fatalf("decodeFooter() error = %v, want ErrInvalidSSTable", err)
	}
}

func writeLegacySStableForTest(path string, records []Record) error {
	records = normalizeRecords(records)
	filter, err := buildBloomFilter(records)
	if err != nil {
		return err
	}
	meta, err := buildSSTableMeta(records, filter)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	var offset uint64
	index := make([]IndexEntry, 0)
	for _, blockRecords := range splitDataBlocks(records, defaultDataBlockSize) {
		data, err := EncodeDataBlock(blockRecords)
		if err != nil {
			return err
		}
		if err := writeAll(file, data); err != nil {
			return err
		}
		index = append(index, IndexEntry{
			FirstKey: blockRecords[0].Key,
			LastKey:  blockRecords[len(blockRecords)-1].Key,
			Handle:   BlockHandle{Offset: offset, Size: uint64(len(data))},
		})
		offset += uint64(len(data))
	}

	metaData, err := encodeMetaBlock(meta)
	if err != nil {
		return err
	}
	metaHandle := BlockHandle{Offset: offset, Size: uint64(len(metaData))}
	if err := writeAll(file, metaData); err != nil {
		return err
	}
	offset += uint64(len(metaData))

	indexData, err := encodeIndexBlock(index)
	if err != nil {
		return err
	}
	indexHandle := BlockHandle{Offset: offset, Size: uint64(len(indexData))}
	if err := writeAll(file, indexData); err != nil {
		return err
	}
	footer := encodeFooter(Footer{
		Version:     legacySSTableVersion,
		MetaHandle:  metaHandle,
		IndexHandle: indexHandle,
	})
	if err := writeAll(file, footer); err != nil {
		return err
	}
	return file.Sync()
}
