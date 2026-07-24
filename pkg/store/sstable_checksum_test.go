package store

import (
	"errors"
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
