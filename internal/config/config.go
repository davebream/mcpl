package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

type ServerConfig struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Serialize bool              `json:"serialize,omitempty"`
}

type Config struct {
	IdleTimeout       string                   `json:"idle_timeout,omitempty"`
	ServerIdleTimeout string                   `json:"server_idle_timeout,omitempty"`
	LogLevel          string                   `json:"log_level,omitempty"`
	Servers           map[string]*ServerConfig `json:"servers"`
}

func DefaultConfig() *Config {
	return &Config{
		IdleTimeout:       "30m",
		ServerIdleTimeout: "10m",
		LogLevel:          "info",
		Servers:           make(map[string]*ServerConfig),
	}
}

func Load(path string) (*Config, error) {
	// Verify file permissions before reading (trust boundary check)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0077 != 0 {
		return nil, fmt.Errorf("config file %s has insecure permissions %o (expected 0600). Fix with: chmod 600 %s", path, perm, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.Servers == nil {
		cfg.Servers = make(map[string]*ServerConfig)
	}
	return &cfg, nil
}

func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	return AtomicWriteFile(path, data, 0600)
}

var envVarPattern = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)

// ResolveEnv resolves $VAR references in env values from the process environment.
func ResolveEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	resolved := make(map[string]string, len(env))
	for k, v := range env {
		resolved[k] = envVarPattern.ReplaceAllStringFunc(v, func(match string) string {
			return os.Getenv(match[1:]) // strip leading $
		})
	}
	return resolved
}
