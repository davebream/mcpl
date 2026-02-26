package shim

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectStaleSocket(t *testing.T) {
	t.Run("no socket file is not stale", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "mcpl.sock")
		assert.False(t, isStaleSocket(path))
	})

	t.Run("socket that can't connect is stale", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "mcpl.sock")
		// Create a regular file pretending to be a socket
		require.NoError(t, os.WriteFile(path, []byte{}, 0600))
		assert.True(t, isStaleSocket(path))
	})

	t.Run("active socket is not stale", func(t *testing.T) {
		// Use /tmp for short socket path (macOS 104-byte sun_path limit)
		dir, err := os.MkdirTemp("/tmp", "mcpl-st-*")
		require.NoError(t, err)
		t.Cleanup(func() { os.RemoveAll(dir) })
		path := filepath.Join(dir, "t.sock")

		listener, err := net.Listen("unix", path)
		require.NoError(t, err)
		defer listener.Close()

		assert.False(t, isStaleSocket(path))
	})
}
