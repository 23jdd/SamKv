package utils

// 本文件固定 CompressionType 的磁盘编号和配置解析边界。

import (
	"errors"
	"testing"
)

func TestCompressionTypeStableIDsAndNames(t *testing.T) {
	cases := []struct {
		name string
		kind CompressionType
		id   byte
	}{
		{"none", CompressionNone, 0},
		{"gzip", CompressionGzip, 1},
		{"snappy", CompressionSnappy, 2},
		{"lz4", CompressionLZ4, 3},
		{"zstd", CompressionZstd, 4},
	}
	for _, tc := range cases {
		if byte(tc.kind) != tc.id || tc.kind.String() != tc.name || !tc.kind.Valid() {
			t.Fatalf("compression %q = id %d, string %q, valid %v", tc.name, tc.kind, tc.kind, tc.kind.Valid())
		}
		parsed, err := ParseCompressionType("  " + tc.name + "  ")
		if err != nil || parsed != tc.kind {
			t.Fatalf("ParseCompressionType(%q) = %v, %v", tc.name, parsed, err)
		}
	}
}

func TestCompressionTypeRejectsUnknownValues(t *testing.T) {
	if CompressionType(255).Valid() {
		t.Fatal("unknown compression is valid")
	}
	if _, err := ParseCompressionType("brotli"); !errors.Is(err, ErrUnsupportedCompression) {
		t.Fatalf("ParseCompressionType() error = %v", err)
	}
	value := Value{Compression: CompressionType(255)}
	if _, err := value.MarshalBinary(); !errors.Is(err, ErrUnsupportedCompression) {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
}
