package bitcask

// Options contains configuration settings for the Bitcask storage engine.
type Options struct {
	DirPath     string
	MaxFileSize int64 // Maximum size in bytes before active file rotates into immutable file
}

// DefaultOptions provides default configuration for a given database directory.
func DefaultOptions(dirPath string) Options {
	return Options{
		DirPath:     dirPath,
		MaxFileSize: 64 * 1024 * 1024, // 64 MB default active file threshold
	}
}
