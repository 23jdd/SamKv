package utils

// 本文件固定 CompressionType 的磁盘编号和配置解析边界。

import (
	"errors"
	"fmt"
	"sync"
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

func TestSnappyCompressionRoundTrip(t *testing.T) {
	message := []byte("repeated log message repeated log message repeated log message")
	value, err := NewValueWithCompression(42, message, CompressionSnappy)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := value.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decoded.DecompressedMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(message) || decoded.Compression != CompressionSnappy {
		t.Fatalf("decoded = %q with %v", plain, decoded.Compression)
	}
}

func TestSnappyRejectsCorruptPayload(t *testing.T) {
	value := Value{Message: []byte{0xff}, Compression: CompressionSnappy}
	if _, err := value.DecompressedMessage(); err == nil {
		t.Fatal("corrupt Snappy payload was accepted")
	}
}

func TestLZ4CompressionRoundTrip(t *testing.T) {
	message := []byte("lz4 access log lz4 access log lz4 access log")
	value, err := NewValueWithCompression(43, message, CompressionLZ4)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := value.DecompressedMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(message) {
		t.Fatalf("decoded = %q, want %q", plain, message)
	}
}

func TestLZ4RejectsCorruptFrame(t *testing.T) {
	value := Value{Message: []byte{0xff, 0x00}, Compression: CompressionLZ4}
	if _, err := value.DecompressedMessage(); err == nil {
		t.Fatal("corrupt LZ4 frame was accepted")
	}
}

func TestZstdCompressionRoundTrip(t *testing.T) {
	message := []byte("zstd structured log zstd structured log zstd structured log")
	value, err := NewValueWithCompression(44, message, CompressionZstd)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := value.DecompressedMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(message) {
		t.Fatalf("decoded = %q, want %q", plain, message)
	}
}

func TestZstdCodecsSupportConcurrentValues(t *testing.T) {
	const workers = 16
	var wg sync.WaitGroup
	errorsCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			message := []byte(fmt.Sprintf("worker-%d repeated repeated repeated", id))
			value, err := NewValueWithCompression(int64(id), message, CompressionZstd)
			if err != nil {
				errorsCh <- err
				return
			}
			plain, err := value.DecompressedMessage()
			if err != nil {
				errorsCh <- err
				return
			}
			if string(plain) != string(message) {
				errorsCh <- fmt.Errorf("worker %d decoded %q", id, plain)
			}
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
}

func TestZstdRejectsCorruptFrame(t *testing.T) {
	value := Value{Message: []byte{0xff, 0x00}, Compression: CompressionZstd}
	if _, err := value.DecompressedMessage(); err == nil {
		t.Fatal("corrupt Zstd frame was accepted")
	}
}
