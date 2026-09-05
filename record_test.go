package bitcask

import (
	"testing"
)

func TestEncodeDecodeRecord(t *testing.T) {
	key := []byte("user:1001")
	val := []byte("alice_johnson")

	encoded, ts, err := EncodeRecord(key, val)
	if err != nil {
		t.Fatalf("unexpected encoding error: %v", err)
	}

	if len(encoded) != HeaderSize+len(key)+len(val) {
		t.Fatalf("expected encoded length %d, got %d", HeaderSize+len(key)+len(val), len(encoded))
	}

	header, err := DecodeHeader(encoded[:HeaderSize])
	if err != nil {
		t.Fatalf("unexpected header decoding error: %v", err)
	}

	if header.KeySize != uint32(len(key)) {
		t.Errorf("expected KeySize %d, got %d", len(key), header.KeySize)
	}

	if header.ValueSize != uint32(len(val)) {
		t.Errorf("expected ValueSize %d, got %d", len(val), header.ValueSize)
	}

	if header.Timestamp != ts {
		t.Errorf("expected Timestamp %d, got %d", ts, header.Timestamp)
	}

	// Verify CRC
	if !VerifyCRC(header.CRC, encoded[4:]) {
		t.Errorf("CRC verification failed for valid record")
	}

	// Corrupt payload and test CRC verification failure
	corrupted := make([]byte, len(encoded))
	copy(corrupted, encoded)
	corrupted[len(corrupted)-1] ^= 0xFF // Flip last bit

	if VerifyCRC(header.CRC, corrupted[4:]) {
		t.Errorf("CRC verification should have failed for corrupted payload")
	}
}
