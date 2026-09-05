package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"bitcask"
)


func main() {
	dbDir := "./data_demo"
	defer os.RemoveAll(dbDir)

	fmt.Println("=== Bitcask Learning - Phase 6 Demo (Compaction & Merge) ===")

	opts := bitcask.Options{
		DirPath:     dbDir,
		MaxFileSize: 120, // Small file threshold (120 bytes) to force log rotations
	}

	fmt.Printf("1. Opening Bitcask DB in '%s'...\n", dbDir)
	db, err := bitcask.OpenWithOptions(opts)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}

	keys := []string{"user:1", "user:2", "user:3", "user:4", "user:5"}

	// 2. Initial writes
	fmt.Println("\n2. Writing initial 5 keys...")
	for _, key := range keys {
		_ = db.Put([]byte(key), []byte("initial_value_payload_12345"))
	}

	// 3. Overwrite all 5 keys 5 times (creates 25 stale records on disk across rotated files)
	fmt.Println("3. Overwriting all keys 5 times (generating stale disk records)...")
	for round := 1; round <= 5; round++ {
		for _, key := range keys {
			val := fmt.Sprintf("updated_round_%d_payload_12345", round)
			_ = db.Put([]byte(key), []byte(val))
		}
	}

	// 4. Delete user:3 (appends Tombstone)
	fmt.Println("4. Deleting 'user:3' (appending Tombstone marker)...")
	_ = db.Delete([]byte("user:3"))

	// Print disk status before merge
	printDiskStatus(dbDir, "BEFORE Compaction (Merge)")

	// 5. Trigger Merge (Compaction & Garbage Collection)
	fmt.Println("\n5. Invoking db.Merge() [Compaction & Garbage Collection]...")
	if err := db.Merge(); err != nil {
		log.Fatalf("Merge failed: %v", err)
	}

	// Print disk status after merge
	printDiskStatus(dbDir, "AFTER Compaction (Merge)")

	// 6. Verify data integrity after compaction
	fmt.Println("\n6. Verifying Data Integrity post-Merge:")
	for _, key := range keys {
		val, err := db.Get([]byte(key))
		if err == bitcask.ErrKeyNotFound {
			fmt.Printf("   <- Get('%s') => Key Deleted (Tombstone replayed!)\n", key)
		} else if err == nil {
			fmt.Printf("   <- Get('%s') => '%s'\n", key, string(val))
		}
	}

	db.Close()
	fmt.Println("\n=== Phase 6 Demo Finished Successfully! ===")
}

func printDiskStatus(dir string, label string) {
	fmt.Printf("\n--- Disk Status: %s ---\n", label)
	var totalBytes int64
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".data") {
			info, _ := entry.Info()
			totalBytes += info.Size()
			fmt.Printf("   📁 File: '%s' | Size: %d bytes\n", entry.Name(), info.Size())
		}
	}
	fmt.Printf("   📊 Total Disk Usage: %d bytes\n", totalBytes)
}
