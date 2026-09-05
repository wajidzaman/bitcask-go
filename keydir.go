package bitcask

// IndexEntry represents the in-memory location metadata for a single key.
type IndexEntry struct {
	FileID    uint32
	ValueSize uint32
	ValuePos  uint64 // Offset where value payload begins
	RecordPos uint64 // Offset where record header begins
	Timestamp uint64
}

// Keydir is the in-memory hash table mapping keys to their on-disk IndexEntry.
type Keydir struct {
	entries map[string]IndexEntry
}

// NewKeydir initializes a new empty Keydir hash table.
func NewKeydir() *Keydir {
	return &Keydir{
		entries: make(map[string]IndexEntry),
	}
}

// Put adds or updates a key in the Keydir index.
func (kd *Keydir) Put(key string, entry IndexEntry) {
	kd.entries[key] = entry
}

// Get retrieves an IndexEntry for a given key.
func (kd *Keydir) Get(key string) (IndexEntry, bool) {
	entry, ok := kd.entries[key]
	return entry, ok
}

// Delete removes a key from the Keydir index.
func (kd *Keydir) Delete(key string) {
	delete(kd.entries, key)
}

// Size returns total count of keys in index.
func (kd *Keydir) Size() int {
	return len(kd.entries)
}
