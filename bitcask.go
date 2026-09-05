package bitcask

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// replayLogs iterates over all .data files in chronological order, rebuilds the Keydir in RAM,
// and truncates any torn write / corrupted payload at the active file tail.
func (bc *Bitcask) replayLogs() error {
	// Collect all file IDs in sorted order
	var fileIDs []uint32
	for id := range bc.immutableFiles {
		fileIDs = append(fileIDs, id)
	}
	fileIDs = append(fileIDs, bc.fileID)

	sort.Slice(fileIDs, func(i, j int) bool {
		return fileIDs[i] < fileIDs[j]
	})

	for _, id := range fileIDs {
		var file *os.File
		if id == bc.fileID {
			file = bc.activeFile
		} else {
			file = bc.immutableFiles[id]
		}

		var offset uint64 = 0
		for {
			headerBuf := make([]byte, HeaderSize)
			n, err := file.ReadAt(headerBuf, int64(offset))
			if n == 0 && (err == io.EOF || err != nil) {
				break // Clean EOF reached
			}

			// Partial header read at EOF (Torn Write)
			if n < HeaderSize {
				if id == bc.fileID {
					_ = bc.activeFile.Truncate(int64(offset))
					break
				}
				return fmt.Errorf("truncated header in immutable file %d at offset %d", id, offset)
			}

			header, err := DecodeHeader(headerBuf)
			if err != nil {
				if id == bc.fileID {
					_ = bc.activeFile.Truncate(int64(offset))
					break
				}
				return fmt.Errorf("corrupt header in file %d at offset %d: %w", id, offset, err)
			}

			var valSize uint32 = 0
			if !header.IsTombstone() {
				valSize = header.ValueSize
			}

			payloadLen := header.KeySize + valSize
			payloadBuf := make([]byte, payloadLen)

			pn, err := file.ReadAt(payloadBuf, int64(offset+HeaderSize))
			if uint32(pn) < payloadLen {
				// Partial payload read at EOF (Torn Write)
				if id == bc.fileID {
					_ = bc.activeFile.Truncate(int64(offset))
					break
				}
				return fmt.Errorf("truncated payload in immutable file %d at offset %d", id, offset)
			}

			// Verify CRC: checksum covers header fields (from timestamp) + payload
			crcBuffer := make([]byte, 16+payloadLen)
			copy(crcBuffer[:16], headerBuf[4:20])
			copy(crcBuffer[16:], payloadBuf)

			if !VerifyCRC(header.CRC, crcBuffer) {
				if id == bc.fileID {
					_ = bc.activeFile.Truncate(int64(offset))
					break
				}
				return ErrCorruptedRecord
			}


			key := string(payloadBuf[:header.KeySize])

			if header.IsTombstone() {
				bc.keydir.Delete(key)
			} else {
				recordPos := offset
				valuePos := offset + HeaderSize + uint64(header.KeySize)
				bc.keydir.Put(key, IndexEntry{
					FileID:    id,
					ValueSize: header.ValueSize,
					ValuePos:  valuePos,
					RecordPos: recordPos,
					Timestamp: header.Timestamp,
				})
			}

			offset += HeaderSize + uint64(payloadLen)
		}
	}

	// Update active file write pointer to reflect valid end of file
	stat, err := bc.activeFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat active file after replay: %w", err)
	}
	bc.writePos = uint64(stat.Size())

	return nil
}



// loadDataFiles scans the DB directory for all .data files, sorts them,
// opens historical files as immutable descriptors, and sets the highest ID as active file.
func (bc *Bitcask) loadDataFiles() error {
	entries, err := os.ReadDir(bc.opts.DirPath)
	if err != nil {
		return fmt.Errorf("failed to read db directory: %w", err)
	}

	var fileIDs []uint32
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".data") {
			continue
		}

		nameWithoutExt := strings.TrimSuffix(entry.Name(), ".data")
		id, err := strconv.ParseUint(nameWithoutExt, 10, 32)
		if err != nil {
			continue // Skip non-matching filenames
		}
		fileIDs = append(fileIDs, uint32(id))
	}

	// If no data files exist, start with file ID 1
	if len(fileIDs) == 0 {
		fileIDs = append(fileIDs, 1)
	}

	// Sort file IDs in ascending order
	sort.Slice(fileIDs, func(i, j int) bool {
		return fileIDs[i] < fileIDs[j]
	})

	// Open immutable files (all except the last/highest ID)
	for i := 0; i < len(fileIDs)-1; i++ {
		id := fileIDs[i]
		fileName := filepath.Join(bc.opts.DirPath, fmt.Sprintf("%05d.data", id))
		file, err := os.OpenFile(fileName, os.O_RDONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open immutable file %s: %w", fileName, err)
		}
		bc.immutableFiles[id] = file
	}

	// Open the active file (highest ID) in read-write append mode
	activeID := fileIDs[len(fileIDs)-1]
	activeFileName := filepath.Join(bc.opts.DirPath, fmt.Sprintf("%05d.data", activeID))
	activeFile, err := os.OpenFile(activeFileName, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open active file %s: %w", activeFileName, err)
	}

	bc.fileID = activeID
	bc.activeFile = activeFile

	return nil
}


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

// OpenWithOptions initializes a Bitcask database with custom options and recovers state from disk.
func OpenWithOptions(opts Options) (*Bitcask, error) {
	if err := os.MkdirAll(opts.DirPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	bc := &Bitcask{
		opts:           opts,
		immutableFiles: make(map[uint32]*os.File),
		keydir:         NewKeydir(),
	}

	// 1. Discover existing .data files, set up immutable descriptors and active file
	if err := bc.loadDataFiles(); err != nil {
		return nil, fmt.Errorf("failed to load data files: %w", err)
	}

	// 2. Replay log records from disk to rebuild in-memory Keydir & truncate torn write tail
	if err := bc.replayLogs(); err != nil {
		_ = bc.Close()
		return nil, fmt.Errorf("failed to replay logs: %w", err)
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
		bc.activeFile = nil
	}

	for id, file := range bc.immutableFiles {
		_ = file.Sync()
		_ = file.Close()
		delete(bc.immutableFiles, id)
	}

	return nil
}

// Merge reclaims disk space by compacting immutable log files, discarding dead/deleted records.
func (bc *Bitcask) Merge() error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if len(bc.immutableFiles) == 0 {
		return nil // No immutable files to compact
	}

	// 1. Rotate current active file so all uncompacted records become immutable
	if err := bc.rotateActiveFile(); err != nil {
		return fmt.Errorf("failed to rotate active file for merge: %w", err)
	}

	// 2. Setup temporary merge directory
	mergeDir := filepath.Join(bc.opts.DirPath, ".merge_temp")
	_ = os.RemoveAll(mergeDir)
	if err := os.MkdirAll(mergeDir, 0755); err != nil {
		return fmt.Errorf("failed to create merge temp dir: %w", err)
	}
	defer os.RemoveAll(mergeDir)

	// 3. Open temporary Bitcask instance inside mergeDir
	mergeDB, err := OpenWithOptions(Options{
		DirPath:     mergeDir,
		MaxFileSize: bc.opts.MaxFileSize,
	})
	if err != nil {
		return fmt.Errorf("failed to open merge db instance: %w", err)
	}

	// 4. Collect and sort all immutable file IDs
	var fileIDs []uint32
	for id := range bc.immutableFiles {
		fileIDs = append(fileIDs, id)
	}
	sort.Slice(fileIDs, func(i, j int) bool {
		return fileIDs[i] < fileIDs[j]
	})

	// 5. Scan immutable files and copy only live, non-deleted records
	for _, id := range fileIDs {
		file := bc.immutableFiles[id]
		var offset uint64 = 0

		for {
			headerBuf := make([]byte, HeaderSize)
			n, err := file.ReadAt(headerBuf, int64(offset))
			if n < HeaderSize || err == io.EOF {
				break // End of file or incomplete record
			}

			header, err := DecodeHeader(headerBuf)
			if err != nil {
				break
			}

			var valSize uint32 = 0
			if !header.IsTombstone() {
				valSize = header.ValueSize
			}

			payloadLen := header.KeySize + valSize
			payloadBuf := make([]byte, payloadLen)

			pn, err := file.ReadAt(payloadBuf, int64(offset+HeaderSize))
			if uint32(pn) < payloadLen {
				break
			}

			// Check if this record is still the live version in Keydir
			key := string(payloadBuf[:header.KeySize])
			entry, ok := bc.keydir.Get(key)

			if ok && entry.FileID == id && entry.RecordPos == offset && !header.IsTombstone() {
				value := payloadBuf[header.KeySize:]
				if err := mergeDB.Put([]byte(key), value); err != nil {
					mergeDB.Close()
					return fmt.Errorf("failed to write live record to merge DB: %w", err)
				}
			}

			offset += HeaderSize + uint64(payloadLen)
		}
	}

	_ = mergeDB.Close()

	// 6. Close and remove old immutable files
	for id, file := range bc.immutableFiles {
		_ = file.Close()
		fileName := filepath.Join(bc.opts.DirPath, fmt.Sprintf("%05d.data", id))
		_ = os.Remove(fileName)
		delete(bc.immutableFiles, id)
	}

	if bc.activeFile != nil {
		_ = bc.activeFile.Close()
		activeFileName := filepath.Join(bc.opts.DirPath, fmt.Sprintf("%05d.data", bc.fileID))
		_ = os.Remove(activeFileName)
		bc.activeFile = nil
	}

	// 7. Move compacted files from mergeDir into primary db directory
	mergeEntries, err := os.ReadDir(mergeDir)
	if err != nil {
		return fmt.Errorf("failed to read merge temp directory: %w", err)
	}

	for _, entry := range mergeEntries {
		if strings.HasSuffix(entry.Name(), ".data") {
			oldPath := filepath.Join(mergeDir, entry.Name())
			newPath := filepath.Join(bc.opts.DirPath, entry.Name())
			if err := os.Rename(oldPath, newPath); err != nil {
				return fmt.Errorf("failed to move merged file %s: %w", entry.Name(), err)
			}
		}
	}

	// 8. Re-open data files and reload index
	bc.keydir = NewKeydir()
	if err := bc.loadDataFiles(); err != nil {
		return fmt.Errorf("failed to reload data files after merge: %w", err)
	}

	if err := bc.replayLogs(); err != nil {
		return fmt.Errorf("failed to replay logs after merge: %w", err)
	}

	return nil
}


