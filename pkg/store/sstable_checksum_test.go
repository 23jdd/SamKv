package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestChecksummedBlockRoundTrip(t *testing.T) {
	payload := []byte("data block payload")
	encoded := encodeChecksummedBlock(payload)
	decoded, err := verifyChecksummedBlock(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(payload) {
		t.Fatalf("decoded = %q, want %q", decoded, payload)
	}
}

func TestChecksummedBlockDetectsCorruption(t *testing.T) {
	encoded := encodeChecksummedBlock([]byte("payload"))
	encoded[0] ^= 0xff
	if _, err := verifyChecksummedBlock(encoded); !errors.Is(err, ErrBlockChecksum) {
		t.Fatalf("verify error = %v, want ErrBlockChecksum", err)
	}
}

func TestChecksummedBlockRejectsTruncatedTrailer(t *testing.T) {
	if _, err := verifyChecksummedBlock([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidSSTable) {
		t.Fatalf("verify error = %v, want ErrInvalidSSTable", err)
	}
}
func TestSStableV2DetectsDataBlockCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data-corrupt.sst")
	created, err := WriteSStable(path, []Record{{Key: "key", Val: "value"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.Version() != currentSSTableVersion {
		t.Fatalf("version = %d, want %d", created.Version(), currentSSTableVersion)
	}
	handle := created.Index()[0].Handle
	flipFileByte(t, path, int64(handle.Offset))

	table, err := OpenSStable(path)
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	if _, _, err := table.GetRecord("key"); !errors.Is(err, ErrBlockChecksum) {
		t.Fatalf("GetRecord() error = %v, want ErrBlockChecksum", err)
	}
}

func TestSStableV2DetectsMetadataCorruptionOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta-corrupt.sst")
	created, err := WriteSStable(path, []Record{{Key: "key", Val: "value"}})
	if err != nil {
		t.Fatal(err)
	}
	flipFileByte(t, path, int64(created.footer.MetaHandle.Offset))
	if _, err := OpenSStable(path); !errors.Is(err, ErrBlockChecksum) {
		t.Fatalf("OpenSStable() error = %v, want ErrBlockChecksum", err)
	}
}

func flipFileByte(t *testing.T, path string, offset int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var value [1]byte
	if _, err := file.ReadAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	value[0] ^= 0xff
	if _, err := file.WriteAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}
