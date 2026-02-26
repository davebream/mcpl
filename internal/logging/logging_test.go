package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScrubSecrets(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"api key", "api_key=sk_test_1234", "[REDACTED]"},
		{"bearer token", "Bearer eyJhbGciOiJ", "[REDACTED]"},
		{"sk_live", "key is sk_live_abc123xyz", "key is [REDACTED]"},
		{"github token", "ghp_abc123def456ghi789", "[REDACTED]"},
		{"aws key", "AKIAIOSFODNN7EXAMPLE", "[REDACTED]"},
		{"no secret", "hello world", "hello world"},
		{"password field", "password: hunter2", "[REDACTED]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScrubSecrets(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRotatingWriter(t *testing.T) {
	t.Run("creates log file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")

		rw, err := NewRotatingWriter(path, 1024, 24*time.Hour)
		require.NoError(t, err)
		defer rw.Close()

		n, err := rw.Write([]byte("hello\n"))
		require.NoError(t, err)
		assert.Equal(t, 6, n)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "hello\n", string(data))
	})

	t.Run("rotates when max size exceeded", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")

		rw, err := NewRotatingWriter(path, 20, 24*time.Hour) // 20 byte limit
		require.NoError(t, err)
		defer rw.Close()

		// Write enough to trigger rotation
		rw.Write([]byte("1234567890\n"))  // 11 bytes
		rw.Write([]byte("abcdefghij\n"))  // 11 bytes, triggers rotation on next write
		rw.Write([]byte("after-rotate\n")) // this triggers rotation

		// Give cleanOld goroutine a moment
		time.Sleep(10 * time.Millisecond)

		// Rotated file should exist
		_, err = os.Stat(path + ".1")
		assert.NoError(t, err, "rotated file should exist")

		// Current file should contain the latest write
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(data), "after-rotate")
	})

	t.Run("cleans old rotated files", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")

		// Create a fake old rotated file
		oldRotated := path + ".old"
		os.WriteFile(oldRotated, []byte("old"), 0600)
		// Set its mod time to 10 days ago
		oldTime := time.Now().Add(-10 * 24 * time.Hour)
		os.Chtimes(oldRotated, oldTime, oldTime)

		rw := &RotatingWriter{
			path:   path,
			maxAge: 7 * 24 * time.Hour,
		}
		rw.cleanOld()

		_, err := os.Stat(oldRotated)
		assert.True(t, os.IsNotExist(err), "old rotated file should be cleaned up")
	})

	t.Run("file permissions are 0600", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")

		rw, err := NewRotatingWriter(path, 1024, 24*time.Hour)
		require.NoError(t, err)
		defer rw.Close()

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	})
}

func TestScrubbingHandler(t *testing.T) {
	t.Run("scrubs message", func(t *testing.T) {
		var buf bytes.Buffer
		inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		handler := NewScrubbingHandler(inner)
		logger := slog.New(handler)

		logger.Info("token=ghp_secrettoken123")

		var entry map[string]interface{}
		require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
		assert.Equal(t, "[REDACTED]", entry["msg"])
	})

	t.Run("scrubs string attributes", func(t *testing.T) {
		var buf bytes.Buffer
		inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		handler := NewScrubbingHandler(inner)
		logger := slog.New(handler)

		logger.Info("starting", "config", "api_key=secret123")

		var entry map[string]interface{}
		require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
		assert.Equal(t, "[REDACTED]", entry["config"])
	})

	t.Run("preserves non-secret attributes", func(t *testing.T) {
		var buf bytes.Buffer
		inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		handler := NewScrubbingHandler(inner)
		logger := slog.New(handler)

		logger.Info("server started", "pid", 1234, "port", 8080)

		var entry map[string]interface{}
		require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
		assert.Equal(t, "server started", entry["msg"])
		assert.Equal(t, float64(1234), entry["pid"])
	})

	t.Run("WithAttrs scrubs", func(t *testing.T) {
		var buf bytes.Buffer
		inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
		handler := NewScrubbingHandler(inner)
		logger := slog.New(handler).With("env", "password: hunter2")

		logger.Info("test")

		var entry map[string]interface{}
		require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
		assert.Equal(t, "[REDACTED]", entry["env"])
	})
}

func TestSetup(t *testing.T) {
	dir := t.TempDir()

	logger, cleanup, err := Setup(dir, slog.LevelInfo, false)
	require.NoError(t, err)
	defer cleanup()

	logger.Info("test message", "key", "value")

	// Verify file was created
	logPath := filepath.Join(dir, "daemon.log")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "test message")

	// Verify it's valid JSON
	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &entry))
	assert.Equal(t, "INFO", entry["level"])
}

func TestServerLogger(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(inner)

	serverLog := ServerLogger(logger, "context7")
	serverLog.Info("started")

	var entry map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "context7", entry["server"])
}
