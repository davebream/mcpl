package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// secretPatterns matches common secret/token formats for scrubbing.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|authorization)\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)bearer\s+\S+`),
	regexp.MustCompile(`sk_live_\S+`),
	regexp.MustCompile(`ghp_\S+`),
	regexp.MustCompile(`AKIA[A-Z0-9]{16}`),
}

const redactedPlaceholder = "[REDACTED]"

// ScrubSecrets replaces known secret patterns in a string.
func ScrubSecrets(s string) string {
	for _, pat := range secretPatterns {
		s = pat.ReplaceAllString(s, redactedPlaceholder)
	}
	return s
}

// RotatingWriter writes to a log file with size-based rotation.
// When the file exceeds maxBytes, it is renamed to .1 and a new file is opened.
// Old rotated files beyond maxAge are deleted.
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxAge   time.Duration
	file     *os.File
	size     int64
}

// NewRotatingWriter creates a writer that rotates at maxBytes and removes
// rotated files older than maxAge.
func NewRotatingWriter(path string, maxBytes int64, maxAge time.Duration) (*RotatingWriter, error) {
	rw := &RotatingWriter{
		path:     path,
		maxBytes: maxBytes,
		maxAge:   maxAge,
	}
	if err := rw.openFile(); err != nil {
		return nil, err
	}
	return rw, nil
}

func (rw *RotatingWriter) openFile() error {
	f, err := os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat log file: %w", err)
	}
	rw.file = f
	rw.size = info.Size()
	return nil
}

func (rw *RotatingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.size+int64(len(p)) > rw.maxBytes {
		if err := rw.rotate(); err != nil {
			// Best effort: continue writing to current file
			_ = err
		}
	}

	n, err := rw.file.Write(p)
	rw.size += int64(n)
	return n, err
}

func (rw *RotatingWriter) rotate() error {
	rw.file.Close()

	rotated := rw.path + ".1"
	if err := os.Rename(rw.path, rotated); err != nil {
		// If rename fails, truncate current file
		f, openErr := os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if openErr != nil {
			return fmt.Errorf("rotate: rename failed (%v), truncate also failed: %w", err, openErr)
		}
		rw.file = f
		rw.size = 0
		return fmt.Errorf("rotate rename: %w", err)
	}

	if err := rw.openFile(); err != nil {
		return err
	}

	// Clean up old rotated files
	go rw.cleanOld()
	return nil
}

func (rw *RotatingWriter) cleanOld() {
	dir := filepath.Dir(rw.path)
	base := filepath.Base(rw.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-rw.maxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, base+".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// Close closes the underlying file.
func (rw *RotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.file != nil {
		return rw.file.Close()
	}
	return nil
}

// ScrubbingHandler wraps a slog.Handler to scrub secret patterns from log attributes.
type ScrubbingHandler struct {
	inner slog.Handler
}

// NewScrubbingHandler wraps handler to scrub secrets from string attribute values.
func NewScrubbingHandler(inner slog.Handler) *ScrubbingHandler {
	return &ScrubbingHandler{inner: inner}
}

func (h *ScrubbingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *ScrubbingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Scrub the message
	r2 := slog.NewRecord(r.Time, r.Level, ScrubSecrets(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		r2.AddAttrs(scrubAttr(a))
		return true
	})
	return h.inner.Handle(ctx, r2)
}

func (h *ScrubbingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	scrubbed := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		scrubbed[i] = scrubAttr(a)
	}
	return &ScrubbingHandler{inner: h.inner.WithAttrs(scrubbed)}
}

func (h *ScrubbingHandler) WithGroup(name string) slog.Handler {
	return &ScrubbingHandler{inner: h.inner.WithGroup(name)}
}

func scrubAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, ScrubSecrets(a.Value.String()))
	}
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		scrubbed := make([]slog.Attr, len(attrs))
		for i, ga := range attrs {
			scrubbed[i] = scrubAttr(ga)
		}
		return slog.Group(a.Key, attrsToAny(scrubbed)...)
	}
	return a
}

func attrsToAny(attrs []slog.Attr) []any {
	result := make([]any, len(attrs))
	for i, a := range attrs {
		result[i] = a
	}
	return result
}

// Setup creates a production logger writing JSON to the specified log directory.
// If alsoStderr is true, logs are written to both the file and stderr (for foreground mode).
// Returns the logger and a cleanup function to close the log file.
func Setup(logDir string, level slog.Level, alsoStderr bool) (*slog.Logger, func(), error) {
	logPath := filepath.Join(logDir, "daemon.log")

	rw, err := NewRotatingWriter(logPath, 10*1024*1024, 7*24*time.Hour) // 10MB, 7 days
	if err != nil {
		return nil, nil, fmt.Errorf("setup logging: %w", err)
	}

	var writer io.Writer = rw
	if alsoStderr {
		writer = io.MultiWriter(rw, os.Stderr)
	}

	jsonHandler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	handler := NewScrubbingHandler(jsonHandler)
	logger := slog.New(handler)

	cleanup := func() {
		rw.Close()
	}

	return logger, cleanup, nil
}

// ServerLogger creates a child logger tagged with the server name.
func ServerLogger(parent *slog.Logger, serverName string) *slog.Logger {
	return parent.With("server", serverName)
}
