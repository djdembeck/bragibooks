package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Config holds the application configuration.
type Config struct {
	Server      ServerConfig      `mapstructure:"server" yaml:"server" json:"server"`
	Database    DatabaseConfig    `mapstructure:"database" yaml:"database" json:"database"`
	M4bMerge    M4bMergeConfig    `mapstructure:"m4b_merge" yaml:"m4b_merge" json:"m4b_merge"`
	APIKey      APIKeyConfig      `mapstructure:"api_key" yaml:"api_key" json:"api_key"`
	Directories DirectoriesConfig `mapstructure:"directories" yaml:"directories" json:"directories"`
	Processing  ProcessingConfig  `mapstructure:"processing" yaml:"processing" json:"processing"`
}

// ServerConfig holds server-related configuration.
type ServerConfig struct {
	Host string `mapstructure:"host" yaml:"host" json:"host"`
	Port int    `mapstructure:"port" yaml:"port" json:"port"`
}

// DatabaseConfig holds SQLite database configuration.
type DatabaseConfig struct {
	Path string `mapstructure:"path" yaml:"path" json:"path"`
}

// M4bMergeConfig holds m4b-merge CLI configuration.
type M4bMergeConfig struct {
	Binary string `mapstructure:"binary" yaml:"binary" json:"binary"`
}

// APIKeyConfig holds the audiobookdb API key.
type APIKeyConfig struct {
	APIKey  string `mapstructure:"api_key" yaml:"api_key" json:"-"`
	BaseURL string `mapstructure:"base_url" yaml:"base_url" json:"base_url"`
}

// DirectoriesConfig holds directory paths for input, output, and completed files.
type DirectoriesConfig struct {
	InputDir     string `mapstructure:"input_dir" yaml:"input_dir" json:"input_dir"`
	OutputDir    string `mapstructure:"output_dir" yaml:"output_dir" json:"output_dir"`
	CompletedDir string `mapstructure:"completed_dir" yaml:"completed_dir" json:"completed_dir"`
}

// ProcessingConfig holds audiobook processing configuration.
type ProcessingConfig struct {
	NumCPUs    int    `mapstructure:"num_cpus" yaml:"num_cpus" json:"num_cpus"`
	PathFormat string `mapstructure:"path_format" yaml:"path_format" json:"path_format"`
	Region     string `mapstructure:"region" yaml:"region" json:"region"`
	LogLevel   string `mapstructure:"log_level" yaml:"log_level" json:"log_level"`
}

// ConfigManager manages thread-safe config updates and persistence.
// Wrap a *Config with ConfigManager when the config needs to be
// mutable at runtime (e.g., via PUT /api/settings).
type ConfigManager struct {
	mu         *sync.Mutex
	configPath string
	cfg        *Config
	yamlKeys   map[string]bool
}

// NewConfigManager creates a ConfigManager with default configuration.
// If config/config.yaml exists, it loads values from there.
func NewConfigManager() *ConfigManager {
	cfg, configPath, err := Load()
	if err != nil {
		// If loading fails, fall back to defaults
		setDefaultsOnConfig(&cfg)
	}

	// Compute yamlKeys from the config file.
	yamlKeys := make(map[string]bool)
	if configPath != "" {
		if raw, readErr := os.ReadFile(configPath); readErr == nil {
			var rawMap map[string]any
			if yaml.Unmarshal(raw, &rawMap) == nil {
				for _, k := range flattenYAMLKeys(rawMap, "") {
					yamlKeys[k] = true
				}
			}
		}
	}

	return &ConfigManager{
		mu:         &sync.Mutex{},
		cfg:        &cfg,
		configPath: configPath,
		yamlKeys:   yamlKeys,
	}
}

// Config returns the current Config.
func (cm *ConfigManager) Config() *Config {
	return cm.cfg
}

// Lock acquires the config mutex. Used by the API handler to ensure
// atomicity across apply-validate-save sequences.
func (cm *ConfigManager) Lock() {
	cm.mu.Lock()
}

// Unlock releases the config mutex.
func (cm *ConfigManager) Unlock() {
	cm.mu.Unlock()
}

// Snapshot returns a copy of the config for later restoration via Restore.
func (cm *ConfigManager) Snapshot() Config {
	return *cm.cfg
}

// Restore replaces the current config with the provided snapshot.
func (cm *ConfigManager) Restore(snap Config) {
	*cm.cfg = snap
}

// Save persists the current config to the YAML file using atomic writes.
// If no config file was loaded (configPath is empty), defaults to config/config.yaml.
// The caller MUST hold ConfigManager.Lock() before calling Save.
func (cm *ConfigManager) Save() error {
	path := cm.configPath
	if path == "" {
		path = "config/config.yaml"
	}
	data, err := yaml.Marshal(cm.cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return atomicWriteFile(path, data, 0o644)
}

// ConfigPath returns the path to the loaded config file, or "" if no file was found.
func (cm *ConfigManager) ConfigPath() string {
	return cm.configPath
}

// YAMLKeys returns the set of dotted keys present in the loaded YAML config file.
// Returns an empty map if no YAML file was loaded.
func (cm *ConfigManager) YAMLKeys() map[string]bool {
	return cm.yamlKeys
}

// Load loads configuration: YAML file takes precedence over environment variables.
// If a key exists in config/config.yaml, it overrides any env var for that key.
// Returns the loaded Config and the path to the config file (or "" if no file found).
func Load() (Config, string, error) {
	v := viper.New()

	// Set config file name and paths.
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("config")
	v.AddConfigPath(".")
	v.AddConfigPath("/app/data")
	v.AddConfigPath("/config")

	// Enable environment variable support.
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	// Backward compat: old Python users may have REGION=us
	v.BindEnv("processing.region", "REGION", "PROCESSING_REGION")

	// Set default values.
	setDefaults(v)

	// Read config file.
	configFileNotFound := false
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			configFileNotFound = true
		} else {
			return Config{}, "", fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, "", fmt.Errorf("failed to unmarshal config: %w", err)
	}

	var configPath string

	// YAML overrides env vars: re-read the YAML directly and overwrite any
	// values that were explicitly set in the config file.
	if !configFileNotFound {
		configPath = v.ConfigFileUsed()

		var yamlOnly Config
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return Config{}, "", fmt.Errorf("failed to re-read config file for overrides: %w", err)
		}
		if err := yaml.Unmarshal(raw, &yamlOnly); err != nil {
			return Config{}, "", fmt.Errorf("failed to parse config file for overrides: %w", err)
		}
		if err := applyYAMLOverrides(&cfg, &yamlOnly, configPath); err != nil {
			return Config{}, "", fmt.Errorf("failed to apply YAML overrides: %w", err)
		}
	}

	return cfg, configPath, nil
}

// applyYAMLOverrides copies non-zero values from yamlOnly into target for
// every field that was explicitly set in the YAML file.
func applyYAMLOverrides(target *Config, yamlOnly *Config, configPath string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to re-read config file for overrides: %w", err)
	}
	var rawMap map[string]any
	if err := yaml.Unmarshal(raw, &rawMap); err != nil {
		return fmt.Errorf("failed to parse config file for overrides: %w", err)
	}
	keys := flattenYAMLKeys(rawMap, "")
	for _, key := range keys {
		overrideKey(target, yamlOnly, key)
	}
	return nil
}

// flattenYAMLKeys returns all dotted keys present in a nested map.
func flattenYAMLKeys(m map[string]any, prefix string) []string {
	var keys []string
	for k, v := range m {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		switch child := v.(type) {
		case map[string]any:
			keys = append(keys, flattenYAMLKeys(child, fullKey)...)
		default:
			keys = append(keys, fullKey)
		}
	}
	return keys
}

// overrideKey copies a single dotted key from yamlOnly into target.
func overrideKey(target, yamlOnly *Config, key string) {
	switch key {
	// Server
	case "server.host":
		target.Server.Host = yamlOnly.Server.Host
	case "server.port":
		target.Server.Port = yamlOnly.Server.Port
	// Database
	case "database.path":
		target.Database.Path = yamlOnly.Database.Path
	// M4bMerge
	case "m4b_merge.binary":
		target.M4bMerge.Binary = yamlOnly.M4bMerge.Binary
	// APIKey
	case "api_key.api_key":
		target.APIKey.APIKey = yamlOnly.APIKey.APIKey
	case "api_key.base_url":
		target.APIKey.BaseURL = yamlOnly.APIKey.BaseURL
	// Directories
	case "directories.input_dir":
		target.Directories.InputDir = yamlOnly.Directories.InputDir
	case "directories.output_dir":
		target.Directories.OutputDir = yamlOnly.Directories.OutputDir
	case "directories.completed_dir":
		target.Directories.CompletedDir = yamlOnly.Directories.CompletedDir
	// Processing
	case "processing.num_cpus":
		target.Processing.NumCPUs = yamlOnly.Processing.NumCPUs
	case "processing.path_format":
		target.Processing.PathFormat = yamlOnly.Processing.PathFormat
	case "processing.region":
		target.Processing.Region = yamlOnly.Processing.Region
	case "processing.log_level":
		target.Processing.LogLevel = yamlOnly.Processing.LogLevel
	}
}

// setDefaults sets default configuration values for Viper.
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "")
	v.SetDefault("server.port", 8080)
	v.SetDefault("database.path", "config/bragibooks.db")
	v.SetDefault("m4b_merge.binary", "m4b-merge")
	v.SetDefault("api_key.api_key", "")
	v.SetDefault("api_key.base_url", "https://audiobookdb.org/api")
	v.SetDefault("directories.input_dir", "")
	v.SetDefault("directories.output_dir", "")
	v.SetDefault("directories.completed_dir", "")
	v.SetDefault("processing.num_cpus", 1)
	v.SetDefault("processing.path_format", "{author}/{title}")
	v.SetDefault("processing.region", "us")
	v.SetDefault("processing.log_level", "info")
}

// setDefaultsOnConfig sets default values directly on a Config struct.
// Used when Viper-based loading fails entirely.
func setDefaultsOnConfig(cfg *Config) {
	cfg.Server.Host = ""
	cfg.Server.Port = 8080
	cfg.Database.Path = "config/bragibooks.db"
	cfg.M4bMerge.Binary = "m4b-merge"
	cfg.APIKey.APIKey = ""
	cfg.APIKey.BaseURL = "https://audiobookdb.org/api"
	cfg.Directories.InputDir = ""
	cfg.Directories.OutputDir = ""
	cfg.Directories.CompletedDir = ""
	cfg.Processing.NumCPUs = 1
	cfg.Processing.PathFormat = "{author}/{title}"
	cfg.Processing.Region = "us"
	cfg.Processing.LogLevel = "info"
}

// atomicWriteFile writes data to path atomically using a temp file + rename.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".bragibooks-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	var committed bool
	var closed bool
	defer func() {
		if !closed {
			tmp.Close()
		}
		if !committed {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("failed to sync config: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp config: %w", err)
	}
	closed = true

	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("failed to chmod config: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Docker bind-mounted files live on overlayfs where rename may fail.
		// Fall back to a direct truncate+write.
		if writeErr := os.WriteFile(path, data, perm); writeErr != nil {
			return fmt.Errorf("failed to rename config (%w) and fallback write also failed: %v", err, writeErr)
		}
		os.Remove(tmpPath)
	}

	committed = true
	return nil
}
