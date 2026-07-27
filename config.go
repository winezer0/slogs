package slogs

// Config holds logging configuration.
type Config struct {
	// Level is the minimum log level: debug, info, warn, error.
	Level string `yaml:"level"`
	// Format is the console output format: "text" (human-readable) or "json" (structured).
	Format string `yaml:"format"`
	// FilePath is the log file path; empty means no file output.
	FilePath string `yaml:"file_path"`
	// MaxSize is the maximum size in megabytes of a single log file before rotation.
	MaxSize int `yaml:"max_size"`
	// MaxBackups is the maximum number of old log files to retain.
	MaxBackups int `yaml:"max_backups"`
	// MaxAge is the maximum number of days to retain old log files.
	MaxAge int `yaml:"max_age"`
	// Compress determines whether rotated files are compressed.
	Compress bool `yaml:"compress"`
}

// DefaultConfig returns a sensible default configuration (info level, text console, no file).
func DefaultConfig() Config {
	return Config{
		Level:      "info",
		Format:     "text",
		FilePath:   "",
		MaxSize:    100,
		MaxBackups: 3,
		MaxAge:     30,
		Compress:   true,
	}
}

// NewConfig creates a Config with the given level, file path, and format,
// applying defaults for rotation parameters.
func NewConfig(level, filePath, format string) Config {
	if level == "" {
		level = "info"
	}
	if format == "" {
		format = "text"
	}
	return Config{
		Level:      level,
		Format:     format,
		FilePath:   filePath,
		MaxSize:    100,
		MaxBackups: 3,
		MaxAge:     30,
		Compress:   true,
	}
}
