package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigLoadSave(t *testing.T) {
	t.Run("round-trip config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")

		cfg := &Config{
			IdleTimeout:       "30m",
			ServerIdleTimeout: "10m",
			LogLevel:          "info",
			Servers: map[string]*ServerConfig{
				"test-server": {
					Command: "echo",
					Args:    []string{"hello"},
					Env:     map[string]string{"FOO": "bar"},
				},
			},
		}

		err := cfg.Save(path)
		require.NoError(t, err)

		loaded, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, cfg.IdleTimeout, loaded.IdleTimeout)
		assert.Equal(t, cfg.Servers["test-server"].Command, loaded.Servers["test-server"].Command)
		assert.Equal(t, cfg.Servers["test-server"].Args, loaded.Servers["test-server"].Args)
		assert.Equal(t, cfg.Servers["test-server"].Env, loaded.Servers["test-server"].Env)
	})

	t.Run("saved file has 0600 permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")

		cfg := &Config{Servers: map[string]*ServerConfig{}}
		err := cfg.Save(path)
		require.NoError(t, err)

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	})

	t.Run("load nonexistent returns error", func(t *testing.T) {
		_, err := Load("/tmp/nonexistent-mcpl-test/config.json")
		assert.Error(t, err)
	})

	t.Run("load rejects insecure permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		os.WriteFile(path, []byte(`{"servers":{}}`), 0644)

		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insecure permissions")
	})

	t.Run("serialize field preserved", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")

		cfg := &Config{
			Servers: map[string]*ServerConfig{
				"slow": {Command: "npx", Serialize: true},
			},
		}
		err := cfg.Save(path)
		require.NoError(t, err)

		loaded, err := Load(path)
		require.NoError(t, err)
		assert.True(t, loaded.Servers["slow"].Serialize)
	})
}

func TestResolveEnv(t *testing.T) {
	t.Run("resolves $VAR references", func(t *testing.T) {
		t.Setenv("MY_SECRET", "s3cret")
		env := map[string]string{
			"API_KEY":  "$MY_SECRET",
			"LITERAL":  "plain-value",
			"COMBINED": "prefix-$MY_SECRET-suffix",
		}
		resolved := ResolveEnv(env)
		assert.Equal(t, "s3cret", resolved["API_KEY"])
		assert.Equal(t, "plain-value", resolved["LITERAL"])
		assert.Equal(t, "prefix-s3cret-suffix", resolved["COMBINED"])
	})

	t.Run("unset var resolves to empty string", func(t *testing.T) {
		env := map[string]string{"KEY": "$UNSET_VAR_MCPL_TEST"}
		resolved := ResolveEnv(env)
		assert.Equal(t, "", resolved["KEY"])
	})

	t.Run("nil env returns nil", func(t *testing.T) {
		assert.Nil(t, ResolveEnv(nil))
	})
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, "30m", cfg.IdleTimeout)
	assert.Equal(t, "10m", cfg.ServerIdleTimeout)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.NotNil(t, cfg.Servers)
}
