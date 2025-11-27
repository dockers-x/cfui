package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	Logger *zap.Logger
	Sugar  *zap.SugaredLogger
)

// Config holds logger configuration
type Config struct {
	LogDir     string
	MaxSize    int  // megabytes
	MaxBackups int  // number of backups
	MaxAge     int  // days
	Compress   bool // compress rotated files
	LogLevel   string
}

// DefaultConfig returns default logger configuration
// Note: LogDir should be set by the caller to ensure proper persistence in Docker
func DefaultConfig() *Config {
	// Priority: LOG_DIR > DATA_DIR/logs > ./data/logs
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		dataDir := os.Getenv("DATA_DIR")
		if dataDir != "" {
			logDir = filepath.Join(dataDir, "logs")
		} else {
			// Fallback to local logs directory for non-Docker usage
			logDir = "./data/logs"
		}
	}

	return &Config{
		LogDir:     logDir,
		MaxSize:    100,  // 100 MB
		MaxBackups: 10,   // keep 10 backups
		MaxAge:     30,   // 30 days
		Compress:   true, // compress old logs
		LogLevel:   "info",
	}
}

// Initialize sets up the global logger with file rotation
func Initialize(cfg *Config) error {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Create log directory if it doesn't exist
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return err
	}

	// Parse log level
	level := zapcore.InfoLevel
	if cfg.LogLevel != "" {
		if err := level.UnmarshalText([]byte(cfg.LogLevel)); err == nil {
			// level parsed successfully
		}
	}

	// Setup lumberjack for log rotation
	logFile := filepath.Join(cfg.LogDir, "cfui.log")
	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   cfg.Compress,
		LocalTime:  true,
	}

	// Create encoder config
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// Create cores for both file and console output
	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(lumberjackLogger),
		level,
	)

	// Console encoder with color
	consoleEncoderConfig := encoderConfig
	consoleEncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(consoleEncoderConfig),
		zapcore.AddSync(os.Stdout),
		level,
	)

	// Combine cores
	core := zapcore.NewTee(fileCore, consoleCore)

	// Create logger with caller and stacktrace
	Logger = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	Sugar = Logger.Sugar()

	// Sync on shutdown
	return nil
}

// Sync flushes any buffered log entries
func Sync() {
	if Logger != nil {
		_ = Logger.Sync()
	}
	if Sugar != nil {
		_ = Sugar.Sync()
	}
}

// RecoverPanic recovers from panic and logs it
func RecoverPanic() {
	if r := recover(); r != nil {
		if Sugar != nil {
			Sugar.Errorf("Recovered from panic: %v", r)
			Sugar.Error("Stack trace will be printed above")
		}
		panic(r) // re-panic to allow proper shutdown
	}
}

// RecoverPanicWithHandler recovers from panic, logs it, and executes a custom handler
func RecoverPanicWithHandler(handler func(interface{})) {
	if r := recover(); r != nil {
		if Sugar != nil {
			Sugar.Errorf("Recovered from panic: %v", r)
		}
		if handler != nil {
			handler(r)
		}
	}
}
