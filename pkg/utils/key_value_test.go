package utils

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

func TestEncodeKeyNormalizesLabelsAndDecodes(t *testing.T) {
	labelsA := []Label{{Name: "level", Value: "ERROR"}, {Name: "app", Value: "nginx"}, {Name: "host", Value: "server1"}}
	labelsB := []Label{{Name: "host", Value: "server1"}, {Name: "app", Value: "nginx"}, {Name: "level", Value: "ERROR"}}

	keyA, err := EncodeKey(1704103200000000000, labelsA, 1)
	if err != nil {
		t.Fatalf("EncodeKey(A) error = %v", err)
	}
	keyB, err := EncodeKey(1704103200000000000, labelsB, 1)
	if err != nil {
		t.Fatalf("EncodeKey(B) error = %v", err)
	}
	if !bytes.Equal(keyA, keyB) {
		t.Fatal("EncodeKey() differs for same labels in different order")
	}

	decoded, err := DecodeKey(keyA)
	if err != nil {
		t.Fatalf("DecodeKey() error = %v", err)
	}
	wantLabels := []Label{{Name: "app", Value: "nginx"}, {Name: "host", Value: "server1"}, {Name: "level", Value: "ERROR"}}
	if decoded.Timestamp != 1704103200000000000 || decoded.Sequence != 1 || !reflect.DeepEqual(decoded.Labels, wantLabels) {
		t.Fatalf("DecodeKey() = %#v", decoded)
	}
}

func TestEncodeKeySortsByTimestampThenLabelsThenSequence(t *testing.T) {
	older, err := EncodeKey(10, []Label{{Name: "app", Value: "nginx"}}, 99)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := EncodeKey(11, []Label{{Name: "app", Value: "nginx"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(older, newer) >= 0 {
		t.Fatal("older timestamp key should sort before newer timestamp key")
	}

	seq1, err := EncodeKey(11, []Label{{Name: "app", Value: "nginx"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	seq2, err := EncodeKey(11, []Label{{Name: "app", Value: "nginx"}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(seq1, seq2) >= 0 {
		t.Fatal("lower sequence key should sort before higher sequence key")
	}
}

func TestEncodeKeyString(t *testing.T) {
	got, err := EncodeKeyString(time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC), []Label{{Name: "level", Value: "ERROR"}, {Name: "app", Value: "nginx"}}, 1)
	if err != nil {
		t.Fatalf("EncodeKeyString() error = %v", err)
	}
	want := "2024-01-01T10:00:00Z|app=nginx|level=ERROR|00001"
	if got != want {
		t.Fatalf("EncodeKeyString() = %q, want %q", got, want)
	}
}

func TestValueCompressionAndBinaryRoundTrip(t *testing.T) {
	message := []byte("nginx error nginx error nginx error")
	value, err := NewValue(1704103200000000000, message)
	if err != nil {
		t.Fatalf("NewValue() error = %v", err)
	}
	if bytes.Equal(value.Message, message) {
		t.Fatal("Value.Message is not compressed")
	}

	encoded, err := value.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalValue(encoded)
	if err != nil {
		t.Fatalf("UnmarshalValue() error = %v", err)
	}
	plain, err := decoded.DecompressedMessage()
	if err != nil {
		t.Fatalf("DecompressedMessage() error = %v", err)
	}
	if !bytes.Equal(plain, message) {
		t.Fatalf("decompressed message = %q, want %q", plain, message)
	}
	if decoded.Timestamp != value.Timestamp || decoded.Compression != CompressionGzip {
		t.Fatalf("decoded value = %#v", decoded)
	}
}

func TestValueWithoutCompressionRoundTrip(t *testing.T) {
	message := []byte("raw log")
	value, err := NewValueWithCompression(1, message, CompressionNone)
	if err != nil {
		t.Fatalf("NewValueWithCompression() error = %v", err)
	}
	plain, err := value.DecompressedMessage()
	if err != nil {
		t.Fatalf("DecompressedMessage() error = %v", err)
	}
	if !bytes.Equal(plain, message) {
		t.Fatalf("plain = %q, want %q", plain, message)
	}
}
