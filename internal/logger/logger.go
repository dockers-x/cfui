package logger

import (
	"container/ring"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

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

	// Create a single broadcast writer to avoid duplicate broadcasts
	broadcastWriter := newBroadcastWriter(broadcaster)

	// Wrap file writer with broadcaster - file output broadcasts to SSE clients
	fileWriter := io.MultiWriter(lumberjackLogger, broadcastWriter)

	// Create cores for both file and console output
	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(fileWriter),
		level,
	)

	// Console encoder with color - NO broadcaster to avoid duplicate broadcasts
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

// Shutdown performs graceful shutdown of logger and broadcaster
func Shutdown() {
	// Close broadcaster to stop background goroutine
	broadcasterMu.Lock()
	if broadcaster != nil {
		broadcaster.Close()
		broadcaster = nil
	}
	broadcasterMu.Unlock()

	// Sync and close logger
	Sync()
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
	subscribers map[chan string]*subscriberInfo
	buffer      *ring.Ring // Circular buffer for recent logs
	mu          sync.RWMutex
	bufferSize  int
	cleanupDone chan struct{}
	wg          sync.WaitGroup
}

// subscriberInfo holds metadata about a subscriber
type subscriberInfo struct {
	ch         chan string
	lastActive time.Time
	remoteAddr string // For debugging
}

const (
	subscriberTimeout    = 5 * time.Minute // Close inactive subscribers after 5 minutes
	cleanupInterval      = 1 * time.Minute // Check for inactive subscribers every minute
	subscriberBufferSize = 100             // Buffered channel size
)

// NewLogBroadcaster creates a new log broadcaster with a circular buffer
func NewLogBroadcaster(bufferSize int) *LogBroadcaster {
	b := &LogBroadcaster{
		subscribers: make(map[chan string]*subscriberInfo),
		buffer:      ring.New(bufferSize),
		bufferSize:  bufferSize,
		cleanupDone: make(chan struct{}),
	}

	// Start background cleanup goroutine
	b.wg.Add(1)
	go b.cleanupInactiveSubscribers()

	return b
}

// cleanupInactiveSubscribers periodically removes inactive subscribers
func (b *LogBroadcaster) cleanupInactiveSubscribers() {
	defer b.wg.Done()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.mu.Lock()
			now := time.Now()
			for ch, info := range b.subscribers {
				if now.Sub(info.lastActive) > subscriberTimeout {
					Sugar.Warnf("Removing inactive log subscriber (addr: %s, inactive: %v)",
						info.remoteAddr, now.Sub(info.lastActive))
					delete(b.subscribers, ch)
					close(ch)
				}
			}
			b.mu.Unlock()
		case <-b.cleanupDone:
			return
		}
	}
}

// Close stops the broadcaster and cleans up resources
func (b *LogBroadcaster) Close() {
	close(b.cleanupDone)
	b.wg.Wait()

	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers {
		close(ch)
	}
	b.subscribers = make(map[chan string]*subscriberInfo)
}

// Subscribe creates a new subscriber channel
func (b *LogBroadcaster) Subscribe(remoteAddr string) chan string {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan string, subscriberBufferSize)
	b.subscribers[ch] = &subscriberInfo{
		ch:         ch,
		lastActive: time.Now(),
		remoteAddr: remoteAddr,
	}
	return ch
}

// MarkActive updates the last active time for a subscriber
func (b *LogBroadcaster) MarkActive(ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if info, ok := b.subscribers[ch]; ok {
		info.lastActive = time.Now()
	}
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
	defer b.mu.Unlock()

	// Store in circular buffer
	b.buffer.Value = line
	b.buffer = b.buffer.Next()

	// Send to all subscribers (non-blocking)
	for ch, info := range b.subscribers {
		select {
		case ch <- line:
			info.lastActive = time.Now() // Update activity on successful send
		default:
			// Skip if channel is full (client too slow)
			// Don't update lastActive - this subscriber might be dead
		}
	}
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
	broadcaster *LogBroadcaster
	buffer      []byte
	mu          sync.Mutex
}

const maxBufferSize = 64 * 1024 // 64KB max buffer to prevent memory leak

func newBroadcastWriter(b *LogBroadcaster) *broadcastWriter {
	return &broadcastWriter{
		broadcaster: b,
		buffer:      make([]byte, 0, 1024), // Pre-allocate 1KB
	}
}

func (bw *broadcastWriter) Write(p []byte) (n int, err error) {
	bw.mu.Lock()
	defer bw.mu.Unlock()

	// Prevent buffer from growing indefinitely
	if len(bw.buffer) > maxBufferSize {
		// Buffer overflow - likely a log line without newline
		// Force broadcast accumulated data and reset
		if len(bw.buffer) > 0 {
			bw.broadcaster.Broadcast(string(bw.buffer))
		}
		bw.buffer = bw.buffer[:0]
	}

	// Append new data to buffer
	bw.buffer = append(bw.buffer, p...)

	// Broadcast each complete line
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

	return len(p), nil
}

// GetBroadcaster returns the global log broadcaster
func GetBroadcaster() *LogBroadcaster {
	broadcasterMu.RLock()
	defer broadcasterMu.RUnlock()
	return broadcaster
}
