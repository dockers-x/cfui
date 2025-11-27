package logger

import (
	"bufio"
	"container/ring"
	"io"
	"os"
	"path/filepath"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	Logger        *zap.Logger
	Sugar         *zap.SugaredLogger
	broadcaster   *LogBroadcaster
	broadcasterMu sync.RWMutex
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

	// Initialize broadcaster with buffer for 500 recent log lines
	broadcasterMu.Lock()
	broadcaster = NewLogBroadcaster(500)
	broadcasterMu.Unlock()

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

	// Wrap file writer with broadcaster using io.MultiWriter
	fileWriter := io.MultiWriter(lumberjackLogger, newBroadcastWriter(io.Discard, broadcaster))

	// Create cores for both file and console output
	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(fileWriter),
		level,
	)

	// Console encoder with color - also add broadcaster
	consoleEncoderConfig := encoderConfig
	consoleEncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder

	// Console output goes to both stdout AND broadcaster
	consoleWriter := io.MultiWriter(os.Stdout, newBroadcastWriter(io.Discard, broadcaster))

	consoleCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(consoleEncoderConfig),
		zapcore.AddSync(consoleWriter),
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

// LogBroadcaster broadcasts log lines to multiple subscribers
type LogBroadcaster struct {
	subscribers map[chan string]struct{}
	buffer      *ring.Ring // Circular buffer for recent logs
	mu          sync.RWMutex
	bufferSize  int
}

// NewLogBroadcaster creates a new log broadcaster with a circular buffer
func NewLogBroadcaster(bufferSize int) *LogBroadcaster {
	return &LogBroadcaster{
		subscribers: make(map[chan string]struct{}),
		buffer:      ring.New(bufferSize),
		bufferSize:  bufferSize,
	}
}

// Subscribe creates a new subscriber channel
func (b *LogBroadcaster) Subscribe() chan string {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan string, 100) // Buffered to prevent blocking
	b.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber
func (b *LogBroadcaster) Unsubscribe(ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.subscribers, ch)
	close(ch)
}

// Broadcast sends a log line to all subscribers
func (b *LogBroadcaster) Broadcast(line string) {
	b.mu.Lock()
	// Store in circular buffer
	b.buffer.Value = line
	b.buffer = b.buffer.Next()

	// Send to all subscribers (non-blocking)
	for ch := range b.subscribers {
		select {
		case ch <- line:
		default:
			// Skip if channel is full (client too slow)
		}
	}
	b.mu.Unlock()
}

// GetRecentLogs returns the recent logs from the circular buffer
func (b *LogBroadcaster) GetRecentLogs() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	logs := make([]string, 0, b.bufferSize)
	b.buffer.Do(func(v interface{}) {
		if v != nil {
			if line, ok := v.(string); ok && line != "" {
				logs = append(logs, line)
			}
		}
	})
	return logs
}

// Write implements io.Writer interface
func (b *LogBroadcaster) Write(p []byte) (n int, err error) {
	line := string(p)
	b.Broadcast(line)
	return len(p), nil
}

// broadcastWriter wraps an io.Writer and broadcasts lines
type broadcastWriter struct {
	writer      io.Writer
	broadcaster *LogBroadcaster
	scanner     *bufio.Scanner
	buffer      []byte
}

func newBroadcastWriter(w io.Writer, b *LogBroadcaster) *broadcastWriter {
	return &broadcastWriter{
		writer:      w,
		broadcaster: b,
	}
}

func (bw *broadcastWriter) Write(p []byte) (n int, err error) {
	// Write to underlying writer first
	n, err = bw.writer.Write(p)

	// Broadcast each complete line
	bw.buffer = append(bw.buffer, p...)
	for {
		idx := -1
		for i, b := range bw.buffer {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx == -1 {
			break
		}
		line := string(bw.buffer[:idx+1])
		bw.broadcaster.Broadcast(line)
		bw.buffer = bw.buffer[idx+1:]
	}

	return n, err
}

// GetBroadcaster returns the global log broadcaster
func GetBroadcaster() *LogBroadcaster {
	broadcasterMu.RLock()
	defer broadcasterMu.RUnlock()
	return broadcaster
}
