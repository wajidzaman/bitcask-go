package bitcask

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrKeyNotFound = errors.New("key not found")
	ErrNilKey      = errors.New("key cannot be empty")
)

// Bitcask represents the key-value storage engine instance.
type Bitcask struct {
	mu         sync.RWMutex
	dirPath    string
	activeFile *os.File
	fileID     uint32
	keydir     *Keydir
	writePos   uint64
}

// Open initializes or opens a Bitcask database in the specified directory.
// For Phase 1, it manages a single active data file.
func Open(dirPath string) (*Bitcask, error) {
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	fileID := uint32(1)
	activeFileName := filepath.Join(dirPath, fmt.Sprintf("%05d.data", fileID))

	file, err := os.OpenFile(activeFileName, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open active file: %w", err)
	}

	// Get initial file offset
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat active file: %w", err)
	}

	bc := &Bitcask{
		dirPath:    dirPath,
		activeFile: file,
		fileID:     fileID,
		keydir:     NewKeydir(),
		writePos:   uint64(stat.Size()),
	}

	return bc, nil
}

// Put writes a key-value pair to the database.
// Sequential append to log file + atomic Keydir update.
func (bc *Bitcask) Put(key []byte, value []byte) error {
	if len(key) == 0 {
		return ErrNilKey
	}

	bc.mu.Lock()
	defer bc.mu.Unlock()

	encoded, timestamp, err := EncodeRecord(key, value)
	if err != nil {
		return fmt.Errorf("failed to encode record: %w", err)
	}

	recordPos := bc.writePos
	keySize := uint32(len(key))
	valueSize := uint32(len(value))
	valuePos := recordPos + HeaderSize + uint64(keySize)

	// Append record bytes to active file
	n, err := bc.activeFile.Write(encoded)
	if err != nil {
		return fmt.Errorf("failed to write record to file: %w", err)
	}

	// Advance write pointer
	bc.writePos += uint64(n)

	// Update Keydir index
	bc.keydir.Put(string(key), IndexEntry{
		FileID:    bc.fileID,
		ValueSize: valueSize,
		ValuePos:  valuePos,
		RecordPos: recordPos,
		Timestamp: timestamp,
	})

	return nil
}

// Delete appends a tombstone record to disk and removes the key from in-memory Keydir.
func (bc *Bitcask) Delete(key []byte) error {
	if len(key) == 0 {
		return ErrNilKey
	}

	bc.mu.Lock()
	defer bc.mu.Unlock()

	// 1. Check if key exists in memory
	if _, ok := bc.keydir.Get(string(key)); !ok {
		return ErrKeyNotFound
	}

	// 2. Encode Tombstone record (ValueSize = 0xFFFFFFFF)
	encoded, _, err := EncodeTombstone(key)
	if err != nil {
		return fmt.Errorf("failed to encode tombstone: %w", err)
	}

	// 3. Append Tombstone to disk file
	n, err := bc.activeFile.Write(encoded)
	if err != nil {
		return fmt.Errorf("failed to write tombstone to disk: %w", err)
	}

	// 4. Advance write offset
	bc.writePos += uint64(n)

	// 5. Remove key from in-memory Keydir
	bc.keydir.Delete(string(key))

	return nil
}


// Get fetches the value for a given key using a single disk read (`ReadAt`).
func (bc *Bitcask) Get(key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrNilKey
	}

	bc.mu.RLock()
	entry, ok := bc.keydir.Get(string(key))
	bc.mu.RUnlock()

	if !ok {
		return nil, ErrKeyNotFound
	}

	// Read entire record from disk to verify CRC
	keySize := uint32(len(key))
	recordLen := HeaderSize + keySize + entry.ValueSize
	buf := make([]byte, recordLen)

	_, err := bc.activeFile.ReadAt(buf, int64(entry.RecordPos))
	if err != nil {
		return nil, fmt.Errorf("failed to read record from disk: %w", err)
	}

	// Decode header and verify CRC
	header, err := DecodeHeader(buf[:HeaderSize])
	if err != nil {
		return nil, fmt.Errorf("failed to decode record header: %w", err)
	}

	payloadBuf := buf[4:]
	if !VerifyCRC(header.CRC, payloadBuf) {
		return nil, ErrCorruptedRecord
	}

	// Extract value payload
	valueBuf := make([]byte, entry.ValueSize)
	copy(valueBuf, buf[HeaderSize+keySize:])

	return valueBuf, nil
}

// Close flushes buffered writes and closes the database.
func (bc *Bitcask) Close() error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.activeFile != nil {
		if err := bc.activeFile.Sync(); err != nil {
			return err
		}
		return bc.activeFile.Close()
	}
	return nil
}
