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
	mu             sync.RWMutex
	opts           Options
	activeFile     *os.File
	fileID         uint32
	immutableFiles map[uint32]*os.File
	keydir         *Keydir
	writePos       uint64
}

// Open initializes a Bitcask database in the specified directory using default options.
func Open(dirPath string) (*Bitcask, error) {
	return OpenWithOptions(DefaultOptions(dirPath))
}

// OpenWithOptions initializes a Bitcask database with custom options.
func OpenWithOptions(opts Options) (*Bitcask, error) {
	if err := os.MkdirAll(opts.DirPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	fileID := uint32(1)
	activeFileName := filepath.Join(opts.DirPath, fmt.Sprintf("%05d.data", fileID))

	file, err := os.OpenFile(activeFileName, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open active file: %w", err)
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat active file: %w", err)
	}

	bc := &Bitcask{
		opts:           opts,
		activeFile:     file,
		fileID:         fileID,
		immutableFiles: make(map[uint32]*os.File),
		keydir:         NewKeydir(),
		writePos:       uint64(stat.Size()),
	}

	return bc, nil
}

// rotateActiveFile closes active file, adds it to immutableFiles map, and spawns new active file.
// Must be called under bc.mu.Lock().
func (bc *Bitcask) rotateActiveFile() error {
	if err := bc.activeFile.Sync(); err != nil {
		return err
	}
	bc.immutableFiles[bc.fileID] = bc.activeFile

	bc.fileID++
	activeFileName := filepath.Join(bc.opts.DirPath, fmt.Sprintf("%05d.data", bc.fileID))

	file, err := os.OpenFile(activeFileName, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open new active file %s: %w", activeFileName, err)
	}

	bc.activeFile = file
	bc.writePos = 0
	return nil
}

// Put writes a key-value pair to the database.
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

	// Check if active file needs rotation
	if bc.writePos+uint64(len(encoded)) > uint64(bc.opts.MaxFileSize) {
		if err := bc.rotateActiveFile(); err != nil {
			return fmt.Errorf("failed to rotate active file: %w", err)
		}
	}

	recordPos := bc.writePos
	keySize := uint32(len(key))
	valueSize := uint32(len(value))
	valuePos := recordPos + HeaderSize + uint64(keySize)

	n, err := bc.activeFile.Write(encoded)
	if err != nil {
		return fmt.Errorf("failed to write record to file: %w", err)
	}

	bc.writePos += uint64(n)

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

	if _, ok := bc.keydir.Get(string(key)); !ok {
		return ErrKeyNotFound
	}

	encoded, _, err := EncodeTombstone(key)
	if err != nil {
		return fmt.Errorf("failed to encode tombstone: %w", err)
	}

	// Check if active file needs rotation
	if bc.writePos+uint64(len(encoded)) > uint64(bc.opts.MaxFileSize) {
		if err := bc.rotateActiveFile(); err != nil {
			return fmt.Errorf("failed to rotate active file: %w", err)
		}
	}

	n, err := bc.activeFile.Write(encoded)
	if err != nil {
		return fmt.Errorf("failed to write tombstone to disk: %w", err)
	}

	bc.writePos += uint64(n)
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
	targetFileID := bc.fileID
	var targetFile *os.File

	if ok {
		if entry.FileID == targetFileID {
			targetFile = bc.activeFile
		} else {
			targetFile = bc.immutableFiles[entry.FileID]
		}
	}
	bc.mu.RUnlock()

	if !ok {
		return nil, ErrKeyNotFound
	}

	if targetFile == nil {
		return nil, fmt.Errorf("file handle for file_id %d not found", entry.FileID)
	}

	keySize := uint32(len(key))
	recordLen := HeaderSize + keySize + entry.ValueSize
	buf := make([]byte, recordLen)

	_, err := targetFile.ReadAt(buf, int64(entry.RecordPos))
	if err != nil {
		return nil, fmt.Errorf("failed to read record from disk file %d: %w", entry.FileID, err)
	}

	header, err := DecodeHeader(buf[:HeaderSize])
	if err != nil {
		return nil, fmt.Errorf("failed to decode record header: %w", err)
	}

	payloadBuf := buf[4:]
	if !VerifyCRC(header.CRC, payloadBuf) {
		return nil, ErrCorruptedRecord
	}

	valueBuf := make([]byte, entry.ValueSize)
	copy(valueBuf, buf[HeaderSize+keySize:])

	return valueBuf, nil
}

// Close flushes buffered writes and closes all data files.
func (bc *Bitcask) Close() error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.activeFile != nil {
		_ = bc.activeFile.Sync()
		_ = bc.activeFile.Close()
	}

	for _, file := range bc.immutableFiles {
		_ = file.Sync()
		_ = file.Close()
	}

	return nil
}

