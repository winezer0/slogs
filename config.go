package slogs

// LogConfig holds logging configuration.
type LogConfig struct {
	// ConsoleLevel is the minimum log level for console output: debug, info, warn, error.
	ConsoleLevel string `yaml:"console_level"`

	// FileLevel is the minimum log level for file output: debug, info, warn, error.
	// Empty string defaults to "debug".
	FileLevel string `yaml:"file_level"`

	// ConsoleFormat is the console (stdout) output format:
	//   ""        - default mask "LCM"
	//   "text"    - slog text (key=value)
	//   "json"    - slog json
	//   "off"     - disable console output
	//   otherwise - mask format (T=time, L=level, C=caller, M=message), e.g. "TLCM", "CM"
	ConsoleFormat string `yaml:"console_format"`

	// LogFileFormat is the file output format (same options as ConsoleFormat;
	// empty defaults to "json").
	LogFileFormat string `yaml:"log_file_format"`
	// LogFilePath is the log file path; empty means no file output.
	LogFilePath string `yaml:"log_file_path"`
	// MaxSize is the maximum size in megabytes of a single log file before rotation.
	MaxSize int `yaml:"max_size"`
	// MaxBackups is the maximum number of old log files to retain.
	MaxBackups int `yaml:"max_backups"`
	// MaxAge is the maximum number of days to retain old log files.
	MaxAge int `yaml:"max_age"`
	// Compress determines whether rotated files are compressed.
	Compress bool `yaml:"compress"`
}

// NewConfig creates a LogConfig with the given console level, file path,
// and console format, applying defaults for rotation parameters.
// FileLevel is left empty (defaults to "debug" at runtime).
//
// The format argument is stored verbatim in ConsoleFormat:
//   - "text", "json", "off" → console text / json / disabled
//   - a mask string (e.g. "TLCM", "CM") → console mask format
//   - empty → console defaults to mask "LCM"
func NewConfig(level, filePath, format string) LogConfig {
	if level == "" {
		level = "info"
	}
	return LogConfig{
		ConsoleLevel:  level,
		ConsoleFormat: format,

		FileLevel:     "debug",
		LogFileFormat: "json",
		LogFilePath:   filePath,

		MaxSize:    100,
		MaxBackups: 3,
		MaxAge:     30,
		Compress:   true,
	}
}

// DefaultConfig 创建日志配置实例，提供全部默认值
func DefaultConfig() LogConfig {
	return NewConfig("info", "", "LCM")
}
