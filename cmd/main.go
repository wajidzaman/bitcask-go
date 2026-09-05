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

	fmt.Println("=== Bitcask Learning - Phase 1 Demo ===")
	fmt.Printf("1. Opening Bitcask DB in '%s'...\n", dbDir)

	db, err := bitcask.Open(dbDir)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}

	// 2. Insert records
	keys := []string{"name", "framework", "version"}
	vals := []string{"Bitcask Engine", "Go 1.27", "v0.1.0"}

	fmt.Println("\n2. Writing Records (Append-Only Log):")
	for i := range keys {
		err := db.Put([]byte(keys[i]), []byte(vals[i]))
		if err != nil {
			log.Fatalf("Failed to put key %s: %v", keys[i], err)
		}
		fmt.Printf("   -> Put('%s' => '%s')\n", keys[i], vals[i])
	}

	// 3. Read records back
	fmt.Println("\n3. Reading Records via In-Memory Keydir Index ($O(1)$ Single Seek):")
	for _, key := range keys {
		val, err := db.Get([]byte(key))
		if err != nil {
			log.Fatalf("Failed to get key %s: %v", key, err)
		}
		fmt.Printf("   <- Get('%s') => '%s'\n", key, string(val))
	}

	// 4. Inspect raw file on disk
	dataFile := filepath.Join(dbDir, "00001.data")
	info, err := os.Stat(dataFile)
	if err == nil {
		fmt.Printf("\n4. Disk File Status: '%s' size = %d bytes\n", dataFile, info.Size())
	}

	db.Close()
	fmt.Println("\n=== Phase 1 Execution Finished Successfully! ===")
}
