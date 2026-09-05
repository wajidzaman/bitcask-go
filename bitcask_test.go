package bitcask

import (
	"bytes"
	"os"
	"path/filepath"
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
