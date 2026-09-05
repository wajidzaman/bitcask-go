package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"bitcask"
)

func main() {
	dbDir := "./data_demo"
	defer os.RemoveAll(dbDir)

	fmt.Println("=== Bitcask Learning - Phase 3 Demo (Multi-File Rotation) ===")

	// 1. Configure custom options with small MaxFileSize threshold (120 Bytes)
	opts := bitcask.Options{
		DirPath:     dbDir,
		MaxFileSize: 120,
	}

	fmt.Printf("1. Opening Bitcask DB in '%s' (MaxFileSize = %d bytes)...\n", dbDir, opts.MaxFileSize)

	db, err := bitcask.OpenWithOptions(opts)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}

	// 2. Insert multiple records to trigger file rotation
	keys := []string{"user:1", "user:2", "user:3", "user:4", "user:5"}
	vals := []string{
		"alice_padding_data_123456789",
		"bob_padding_data_123456789",
		"charlie_padding_data_123456789",
		"david_padding_data_123456789",
		"eve_padding_data_123456789",
	}

	fmt.Println("\n2. Writing Records (Log Rotation Active):")
	for i := range keys {
		err := db.Put([]byte(keys[i]), []byte(vals[i]))
		if err != nil {
			log.Fatalf("Failed to put key %s: %v", keys[i], err)
		}
		fmt.Printf("   -> Put('%s' => '%s')\n", keys[i], vals[i])
	}

	// 3. Read records back across historical rotated files
	fmt.Println("\n3. Reading Records via Keydir ($O(1)$ Single Seek across files):")
	for _, key := range keys {
		val, err := db.Get([]byte(key))
		if err != nil {
			log.Fatalf("Failed to get key %s: %v", key, err)
		}
		fmt.Printf("   <- Get('%s') => '%s'\n", key, string(val))
	}

	// 4. List rotated disk files
	fmt.Println("\n4. Inspected On-Disk Rotated Files:")
	entries, err := os.ReadDir(dbDir)
	if err == nil {
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) == ".data" {
				info, _ := entry.Info()
				fmt.Printf("   📁 Data File: '%s' | Size: %d bytes\n", entry.Name(), info.Size())
			}
		}
	}

	db.Close()
	fmt.Println("\n=== Phase 3 Demo Finished Successfully! ===")
}
