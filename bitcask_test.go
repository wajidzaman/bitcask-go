package bitcask

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)


func TestPhase1_PutAndGet(t *testing.T) {
	testDir := "./test_data_put_get"
	os.RemoveAll(testDir) // Clean up any previous test artifacts
	//defer os.RemoveAll(testDir)

	db, err := Open(testDir)
	if err != nil {
		t.Fatalf("failed to open bitcask db: %v", err)
	}
	defer db.Close()

	// 1. Put key-value pair
	key := []byte("name")
	val := []byte("Antigravity Bitcask Engine")

	if err := db.Put(key, val); err != nil {
		t.Fatalf("failed to put key: %v", err)
	}

	// 2. Get key-value pair
	retrievedVal, err := db.Get(key)
	if err != nil {
		t.Fatalf("failed to get key: %v", err)
	}

	if !bytes.Equal(retrievedVal, val) {
		t.Errorf("expected value %s, got %s", string(val), string(retrievedVal))
	}

	// 3. Test non-existent key
	_, err = db.Get([]byte("non_existent_key"))
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestPhase1_MultipleKeys(t *testing.T) {
	testDir := "./test_data_multi"
	os.RemoveAll(testDir) // Clean up any previous test artifacts
	defer os.RemoveAll(testDir)

	db, err := Open(testDir)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	pairs := map[string]string{
		"user:1": "alice",
		"user:2": "bob",
		"user:3": "charlie",
		"city":   "San Francisco",
		"lang":   "Go",
	}

	for k, v := range pairs {
		if err := db.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("failed to put %s: %v", k, err)
		}
	}

	for k, expectedV := range pairs {
		v, err := db.Get([]byte(k))
		if err != nil {
			t.Fatalf("failed to get %s: %v", k, err)
		}
		if string(v) != expectedV {
			t.Errorf("for key %s: expected %s, got %s", k, expectedV, string(v))
		}
	}

	// Check file size on disk
	dataPath := filepath.Join(testDir, "00001.data")
	stat, err := os.Stat(dataPath)
	if err != nil {
		t.Fatalf("failed to stat data file: %v", err)
	}

	if stat.Size() == 0 {
		t.Errorf("expected data file size > 0, got 0")
	}

	db.Close()
}

func TestPhase2_OverwriteAndDelete(t *testing.T) {
	testDir := "./test_data_phase2"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	db, err := Open(testDir)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	key := []byte("user:status")

	// 1. Initial Put
	if err := db.Put(key, []byte("offline")); err != nil {
		t.Fatalf("failed to put: %v", err)
	}

	// 2. Overwrite key (Append new record to log, update Keydir)
	if err := db.Put(key, []byte("online")); err != nil {
		t.Fatalf("failed to overwrite: %v", err)
	}

	val, err := db.Get(key)
	if err != nil {
		t.Fatalf("failed to get overwritten key: %v", err)
	}
	if string(val) != "online" {
		t.Errorf("expected 'online', got '%s'", string(val))
	}

	// 3. Delete key (Appends Tombstone to log, removes from Keydir)
	if err := db.Delete(key); err != nil {
		t.Fatalf("failed to delete key: %v", err)
	}

	// 4. Get deleted key -> expect ErrKeyNotFound
	_, err = db.Get(key)
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound after deletion, got %v", err)
	}

	// 5. Deleting a non-existent key should return ErrKeyNotFound
	if err := db.Delete(key); err != ErrKeyNotFound {
		t.Errorf("deleting non-existent key should return ErrKeyNotFound, got %v", err)
	}
}

func TestPhase3_FileRotation(t *testing.T) {
	testDir := "./test_data_phase3"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	// Set a tiny MaxFileSize threshold (100 Bytes) to trigger rotation frequently
	opts := Options{
		DirPath:     testDir,
		MaxFileSize: 100,
	}

	db, err := OpenWithOptions(opts)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// Write 5 records, each record ~35 bytes -> should spawn at least 2-3 data files
	pairs := map[string]string{
		"key:1": "value_one_padding_bytes_12345",
		"key:2": "value_two_padding_bytes_12345",
		"key:3": "value_three_padding_bytes_12345",
		"key:4": "value_four_padding_bytes_12345",
		"key:5": "value_five_padding_bytes_12345",
	}

	for k, v := range pairs {
		if err := db.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("failed to put key %s: %v", k, err)
		}
	}

	// Verify all keys can be read cleanly across historical files
	for k, expectedVal := range pairs {
		val, err := db.Get([]byte(k))
		if err != nil {
			t.Fatalf("failed to get key %s: %v", k, err)
		}
		if string(val) != expectedVal {
			t.Errorf("for key %s: expected %s, got %s", k, expectedVal, string(val))
		}
	}

	// Verify that multiple .data files were actually created in directory
	files, err := os.ReadDir(testDir)
	if err != nil {
		t.Fatalf("failed to read test dir: %v", err)
	}

	dataFilesCount := 0
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".data" {
			dataFilesCount++
		}
	}

	if dataFilesCount < 2 {
		t.Errorf("expected at least 2 rotated .data files, found %d", dataFilesCount)
	}
}

func TestPhase4_LoadDataFiles(t *testing.T) {
	testDir := "./test_data_load_files"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)



	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	// Pre-create 3 dummy data files and 1 non-data file
	dummyFiles := []string{"00001.data", "00002.data", "00003.data", "notes.txt"}
	for _, f := range dummyFiles {
		path := filepath.Join(testDir, f)
		if err := os.WriteFile(path, []byte("dummy data"), 0644); err != nil {
			t.Fatalf("failed to create dummy file %s: %v", f, err)
		}
	}

	bc := &Bitcask{
		opts: Options{
			DirPath: testDir,
		},
		immutableFiles: make(map[uint32]*os.File),
		keydir:         NewKeydir(),
	}

	// Execute loadDataFiles
	if err := bc.loadDataFiles(); err != nil {
		t.Fatalf("loadDataFiles failed: %v", err)
	}
	defer bc.Close()

	// Verify active file ID is 3 (highest ID)
	if bc.fileID != 3 {
		t.Errorf("expected active fileID 3, got %d", bc.fileID)
	}

	// Verify immutable files contains file IDs 1 and 2
	if len(bc.immutableFiles) != 2 {
		t.Errorf("expected 2 immutable files, got %d", len(bc.immutableFiles))
	}

	if _, ok := bc.immutableFiles[1]; !ok {
		t.Errorf("expected immutableFiles to contain file 1")
	}

	if _, ok := bc.immutableFiles[2]; !ok {
		t.Errorf("expected immutableFiles to contain file 2")
	}
}

func TestPhase4_RestartRecovery(t *testing.T) {
	testDir := "./test_data_recovery"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	// 1. Open DB, put keys, delete one key, and close
	db, err := Open(testDir)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	if err := db.Put([]byte("key:1"), []byte("val:1")); err != nil {
		t.Fatalf("put failed: %v", err)
	}
	if err := db.Put([]byte("key:2"), []byte("val:2")); err != nil {
		t.Fatalf("put failed: %v", err)
	}
	if err := db.Put([]byte("key:3"), []byte("val:3")); err != nil {
		t.Fatalf("put failed: %v", err)
	}

	// Delete key:2 via Tombstone
	if err := db.Delete([]byte("key:2")); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Overwrite key:1
	if err := db.Put([]byte("key:1"), []byte("val:1_updated")); err != nil {
		t.Fatalf("overwrite failed: %v", err)
	}

	db.Close() // RAM Keydir is completely wiped!

	// 2. Reopen DB -> should replay log from disk and rebuild Keydir
	reopenedDB, err := Open(testDir)
	if err != nil {
		t.Fatalf("failed to reopen db: %v", err)
	}
	defer reopenedDB.Close()

	// 3. Verify key:1 has updated value
	val1, err := reopenedDB.Get([]byte("key:1"))
	if err != nil {
		t.Fatalf("failed to get key:1 after restart: %v", err)
	}
	if string(val1) != "val:1_updated" {
		t.Errorf("expected 'val:1_updated', got '%s'", string(val1))
	}

	// 4. Verify key:3 exists
	val3, err := reopenedDB.Get([]byte("key:3"))
	if err != nil {
		t.Fatalf("failed to get key:3 after restart: %v", err)
	}
	if string(val3) != "val:3" {
		t.Errorf("expected 'val:3', got '%s'", string(val3))
	}

	// 5. Verify deleted key:2 returns ErrKeyNotFound
	_, err = reopenedDB.Get([]byte("key:2"))
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound for key:2, got %v", err)
	}
}

func TestPhase4_TornWriteTruncation(t *testing.T) {
	testDir := "./test_data_torn_write"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	// 1. Write valid records and close
	db, err := Open(testDir)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	if err := db.Put([]byte("good:key"), []byte("good:value")); err != nil {
		t.Fatalf("put failed: %v", err)
	}
	db.Close()

	// Measure valid file size
	dataPath := filepath.Join(testDir, "00001.data")
	statBefore, err := os.Stat(dataPath)
	if err != nil {
		t.Fatalf("failed to stat data file: %v", err)
	}
	validSize := statBefore.Size()

	// 2. Simulate torn write / crash by appending 12 corrupted garbage bytes to active file
	f, err := os.OpenFile(dataPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to open file for corrupt append: %v", err)
	}
	_, _ = f.Write([]byte("GARBAGE_BYTES"))
	f.Close()

	statCorrupt, _ := os.Stat(dataPath)
	if statCorrupt.Size() <= validSize {
		t.Fatalf("expected file size to grow after corruption append")
	}

	// 3. Reopen DB -> should detect corrupt tail and truncate file back to validSize
	reopenedDB, err := Open(testDir)
	if err != nil {
		t.Fatalf("failed to reopen db: %v", err)
	}
	defer reopenedDB.Close()

	// 4. Verify valid record is still intact
	val, err := reopenedDB.Get([]byte("good:key"))
	if err != nil {
		t.Fatalf("failed to get good:key after recovery: %v", err)
	}
	if string(val) != "good:value" {
		t.Errorf("expected 'good:value', got '%s'", string(val))
	}

	// 5. Verify file was truncated back to validSize
	statAfter, _ := os.Stat(dataPath)
	if statAfter.Size() != validSize {
		t.Errorf("expected file size truncated to %d, got %d", validSize, statAfter.Size())
	}
}

func TestPhase4_InspectDiskData(t *testing.T) {
	testDir := "./test_data_inspect"
	os.RemoveAll(testDir)
	// We intentionally do NOT defer os.RemoveAll(testDir) so you can inspect the binary files!

	opts := Options{
		DirPath:     testDir,
		MaxFileSize: 100, // Trigger rotation after ~100 bytes
	}

	db, err := OpenWithOptions(opts)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Write real Bitcask binary records (Header + Key + Value)
	_ = db.Put([]byte("user:100"), []byte("alice_johnson_payload_data_12345"))
	_ = db.Put([]byte("user:200"), []byte("bob_smith_payload_data_12345"))
	_ = db.Put([]byte("user:300"), []byte("charlie_brown_payload_data_12345"))
	_ = db.Delete([]byte("user:200"))                                      // Writes real Tombstone marker
	_ = db.Put([]byte("user:100"), []byte("alice_updated_val_payload_9999")) // Overwrite

	db.Close()

	// Reopen DB to verify full recovery from real disk records
	reopenedDB, err := OpenWithOptions(opts)
	if err != nil {
		t.Fatalf("failed to reopen db: %v", err)
	}
	defer reopenedDB.Close()

	// Verify recovered values
	val1, err := reopenedDB.Get([]byte("user:100"))
	if err != nil || string(val1) != "alice_updated_val_payload_9999" {
		t.Errorf("failed to recover updated user:100, got %s, err: %v", string(val1), err)
	}

	val3, err := reopenedDB.Get([]byte("user:300"))
	if err != nil || string(val3) != "charlie_brown_payload_data_12345" {
		t.Errorf("failed to recover user:300, got %s, err: %v", string(val3), err)
	}

	_, err = reopenedDB.Get([]byte("user:200"))
	if err != ErrKeyNotFound {
		t.Errorf("expected deleted user:200 to return ErrKeyNotFound, got %v", err)
	}
}

func TestPhase6_Merge(t *testing.T) {
	testDir := "./test_data_merge"
	os.RemoveAll(testDir)
	defer os.RemoveAll(testDir)

	opts := Options{
		DirPath:     testDir,
		MaxFileSize: 120, // Rotate every ~120 bytes
	}

	db, err := OpenWithOptions(opts)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	// 1. Write 30 keys
	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("user:%d", i)
		val := fmt.Sprintf("initial_payload_value_padding_bytes_%d", i)
		if err := db.Put([]byte(key), []byte(val)); err != nil {
			t.Fatalf("put failed: %v", err)
		}
	}

	// 2. Overwrite all 30 keys 4 times (creates 120 stale records on disk)
	for round := 1; round <= 4; round++ {
		for i := 0; i < 30; i++ {
			key := fmt.Sprintf("user:%d", i)
			val := fmt.Sprintf("updated_round_%d_payload_value_%d", round, i)
			if err := db.Put([]byte(key), []byte(val)); err != nil {
				t.Fatalf("overwrite failed: %v", err)
			}
		}
	}

	// 3. Delete 10 keys (creates 10 Tombstone records on disk)
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("user:%d", i)
		if err := db.Delete([]byte(key)); err != nil {
			t.Fatalf("delete failed: %v", err)
		}
	}

	// Helper to calculate total size of all .data files in testDir
	getDirSize := func() int64 {
		var total int64
		entries, _ := os.ReadDir(testDir)
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".data") {
				info, _ := entry.Info()
				total += info.Size()
			}
		}
		return total
	}

	sizeBeforeMerge := getDirSize()

	// 4. Trigger Merge (Compaction & Garbage Collection)
	if err := db.Merge(); err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	sizeAfterMerge := getDirSize()

	// 5. Assert disk size shrank significantly (at least 40% space reclamation)
	if sizeAfterMerge >= sizeBeforeMerge {
		t.Errorf("expected sizeAfterMerge (%d) < sizeBeforeMerge (%d)", sizeAfterMerge, sizeBeforeMerge)
	}

	// 6. Verify all 20 surviving live keys are readable and contain latest round values
	for i := 10; i < 30; i++ {
		key := fmt.Sprintf("user:%d", i)
		expectedVal := fmt.Sprintf("updated_round_4_payload_value_%d", i)
		val, err := db.Get([]byte(key))
		if err != nil {
			t.Fatalf("failed to get key %s after merge: %v", key, err)
		}
		if string(val) != expectedVal {
			t.Errorf("for key %s: expected '%s', got '%s'", key, expectedVal, string(val))
		}
	}

	// 7. Verify all 10 deleted keys return ErrKeyNotFound after merge
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("user:%d", i)
		_, err := db.Get([]byte(key))
		if err != ErrKeyNotFound {
			t.Errorf("expected deleted key %s to return ErrKeyNotFound after merge, got %v", key, err)
		}
	}
}




