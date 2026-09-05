package bitcask

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"time"
)

const (
	// HeaderSize represents fixed size of header:
	// CRC32 (4B) + Timestamp (8B) + KeySize (4B) + ValueSize (4B) = 20 Bytes
	HeaderSize = 20

	// TombstoneValueSize is a special ValueSize (4,294,967,295) representing a deleted key.
	TombstoneValueSize uint32 = 0xFFFFFFFF
)

var (
	ErrCorruptedRecord = errors.New("record corrupted: CRC mismatch")
	ErrHeaderTooShort  = errors.New("header buffer too short")
)

// Header represents the 20-byte fixed metadata at the start of every Bitcask record.
type Header struct {
	CRC       uint32
	Timestamp uint64
	KeySize   uint32
	ValueSize uint32
}

// IsTombstone returns true if this record header represents a deleted key.
func (h Header) IsTombstone() bool {
	return h.ValueSize == TombstoneValueSize
}


// Record represents a complete Bitcask entry in memory.
type Record struct {
	Header Header
	Key    []byte
	Value  []byte
}

// EncodeRecord serializes a key-value pair into binary Bitcask format:
// [CRC32: 4B | Timestamp: 8B | KeySize: 4B | ValueSize: 4B | Key | Value]
func EncodeRecord(key []byte, value []byte) ([]byte, uint64, error) {
	keySize := uint32(len(key))
	valueSize := uint32(len(value))
	timestamp := uint64(time.Now().UnixNano() / 1000) // Microseconds

	totalSize := HeaderSize + keySize + valueSize
	buf := make([]byte, totalSize)

	// Encode header fields (skipping CRC initially)
	binary.BigEndian.PutUint64(buf[4:12], timestamp)
	binary.BigEndian.PutUint32(buf[12:16], keySize)
	binary.BigEndian.PutUint32(buf[16:20], valueSize)

	// Copy Key and Value
	copy(buf[HeaderSize:HeaderSize+keySize], key)
	copy(buf[HeaderSize+keySize:], value)

	// Compute CRC32 checksum over [Timestamp + KeySize + ValueSize + Key + Value]
	checksum := crc32.ChecksumIEEE(buf[4:])
	binary.BigEndian.PutUint32(buf[0:4], checksum)

	return buf, timestamp, nil
}

// EncodeTombstone serializes a deletion marker record into binary Bitcask format:
// [CRC32: 4B | Timestamp: 8B | KeySize: 4B | TombstoneValueSize(0xFFFFFFFF): 4B | Key]
func EncodeTombstone(key []byte) ([]byte, uint64, error) {
	keySize := uint32(len(key))
	timestamp := uint64(time.Now().UnixNano() / 1000) // Microseconds

	totalSize := HeaderSize + keySize
	buf := make([]byte, totalSize)

	// Encode header fields (ValueSize = TombstoneValueSize)
	binary.BigEndian.PutUint64(buf[4:12], timestamp)
	binary.BigEndian.PutUint32(buf[12:16], keySize)
	binary.BigEndian.PutUint32(buf[16:20], TombstoneValueSize)

	// Copy Key
	copy(buf[HeaderSize:], key)

	// Compute CRC32 checksum over [Timestamp + KeySize + ValueSize + Key]
	checksum := crc32.ChecksumIEEE(buf[4:])
	binary.BigEndian.PutUint32(buf[0:4], checksum)

	return buf, timestamp, nil
}


// DecodeHeader deserializes the 20-byte record header.
func DecodeHeader(buf []byte) (Header, error) {
	if len(buf) < HeaderSize {
		return Header{}, ErrHeaderTooShort
	}

	header := Header{
		CRC:       binary.BigEndian.Uint32(buf[0:4]),
		Timestamp: binary.BigEndian.Uint64(buf[4:12]),
		KeySize:   binary.BigEndian.Uint32(buf[12:16]),
		ValueSize: binary.BigEndian.Uint32(buf[16:20]),
	}

	return header, nil
}

// VerifyCRC validates that the payload matches the expected checksum.
// payloadBuf contains [Timestamp (8B) + KeySize (4B) + ValueSize (4B) + Key + Value].
func VerifyCRC(expectedCRC uint32, payloadBuf []byte) bool {
	return crc32.ChecksumIEEE(payloadBuf) == expectedCRC
}
