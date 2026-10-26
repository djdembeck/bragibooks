package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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
	Host        string            `mapstructure:"host" yaml:"host" json:"host"`
	Port        int               `mapstructure:"port" yaml:"port" json:"port"`
	APIEndpoint APIEndpointConfig `mapstructure:"api_endpoint" yaml:"api_endpoint" json:"api_endpoint"`
}

// APIEndpointConfig holds the external-API (remote key auth) state.
type APIEndpointConfig struct {
	Enabled bool `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	// KeyDigest is the SHA-256 hex of the raw API key. It is populated from
	// the settings DB row only and is deliberately excluded from every
	// serialized/config-file path (the `-` tags keep it out of the full
	// config YAML Save and viper/mapstructure decoding).
	KeyDigest string `mapstructure:"-" yaml:"-" json:"-"`
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
// The live config is stored in an atomic.Pointer: Config() returns the
// current immutable snapshot and UpdateSettings swaps in a new one, so
// readers and writers never observe a half-mutated struct.
type ConfigManager struct {
	mu         *sync.Mutex
	configPath string
	cfg        atomic.Pointer[Config]
	yamlKeys   map[string]bool
}

// NewConfigManager creates a ConfigManager with default configuration.
// If config/config.yaml exists, it loads values from there.
// A load failure is logged (with the reason) and the manager degrades to
// defaults gracefully; it never aborts startup.
func NewConfigManager() *ConfigManager {
	cfg, configPath, _ := loadWith("")
	var yamlKeys map[string]bool
	if configPath == "" {
		// No file read (defaults or silent not-found): log once if there
		// was a real parse error.
		if err := checkLoadError(); err != nil {
			log.Printf("Warning: failed to load configuration (%v); falling back to built-in defaults, no YAML config keys trusted", err)
			setDefaultsOnConfig(&cfg)
		}
		yamlKeys = make(map[string]bool)
	} else {
		// Compute yamlKeys from the config file.
		yamlKeys = yamlKeysFromFile(configPath)
	}

	cm := &ConfigManager{
		mu:         &sync.Mutex{},
		configPath: configPath,
		yamlKeys:   yamlKeys,
	}
	cm.cfg.Store(&cfg)
	return cm
}

// lastLoadError holds the reason the most recent loadWith failed to
// resolve a file, so degraded mode can be reported with the cause.
var lastLoadErrorMu sync.Mutex
var lastLoadError error

func checkLoadError() error {
	lastLoadErrorMu.Lock()
	defer lastLoadErrorMu.Unlock()
	e := lastLoadError
	lastLoadError = nil
	return e
}

// loadWith implements the shared viper-based config load. explicitPath
// empty uses the conventional search paths (Load's behavior). Mirrors the
// original Load() exactly: env keys win over YAML only for keys forced by
// hasEnv (see overrideKey).
func loadWith(explicitPath string) (Config, string, error) {
	v := viper.New()

	// Set config file name and paths.
	v.SetConfigType("yaml")
	if explicitPath != "" {
		v.SetConfigFile(explicitPath)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath("config")
		v.AddConfigPath(".")
		v.AddConfigPath("/app/data")
		v.AddConfigPath("/config")
	}

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
		notFound := false
		var nfe viper.ConfigFileNotFoundError
		if errors.As(err, &nfe) || (explicitPath != "" && os.IsNotExist(err)) {
			notFound = true
		}
		if notFound {
			configFileNotFound = true
		} else {
			setLoadError(fmt.Errorf("failed to read config file: %w", err))
			return Config{}, "", fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		setLoadError(fmt.Errorf("failed to unmarshal config: %w", err))
		return Config{}, "", fmt.Errorf("failed to unmarshal config: %w", err)
	}

	var configPath string
	if !configFileNotFound {
		configPath = v.ConfigFileUsed()

		raw, err := os.ReadFile(configPath)
		if err != nil {
			setLoadError(fmt.Errorf("failed to re-read config file for overrides: %w", err))
			return Config{}, "", fmt.Errorf("failed to re-read config file for overrides: %w", err)
		}
		var yamlOnly Config
		if err := yaml.Unmarshal(raw, &yamlOnly); err != nil {
			setLoadError(fmt.Errorf("failed to parse config file for overrides: %w", err))
			return Config{}, "", fmt.Errorf("failed to parse config file for overrides: %w", err)
		}
		for _, key := range flattenYAMLKeys(parseYAMLMap(raw), "") {
			overrideKey(&cfg, &yamlOnly, key)
		}
	}

	// Env vars keep priority over the YAML file for every key the env
	// mechanism knows (row 13): re-assert them after the override pass.
	applyEnvPriority(&cfg)
	expandUserPaths(&cfg)
	return cfg, configPath, nil
}

func setLoadError(err error) {
	lastLoadErrorMu.Lock()
	lastLoadError = err
	lastLoadErrorMu.Unlock()
}

// Load loads configuration from the conventional search paths.
// Returns the loaded Config and the path to the config file (or "" if none).
func Load() (Config, string, error) {
	return loadWith("")
}

// yamlKeysFromFile returns the set of dotted keys present in the YAML file
// at path (empty map for a missing/unreadable file).
func yamlKeysFromFile(configPath string) map[string]bool {
	yamlKeys := make(map[string]bool)
	if configPath == "" {
		return yamlKeys
	}
	if raw, readErr := os.ReadFile(configPath); readErr == nil {
		var rawMap map[string]any
		if yaml.Unmarshal(raw, &rawMap) == nil {
			for _, k := range flattenYAMLKeys(rawMap, "") {
				yamlKeys[k] = true
			}
		}
	}
	return yamlKeys
}

// Config returns the current Config. The returned pointer is an immutable
// snapshot; updates go through SaveKeys (and friends), which swap in a new
// copy. Callers must not mutate the returned Config.
func (cm *ConfigManager) Config() *Config {
	return cm.cfg.Load()
}

// Store atomically replaces the current config with a new immutable copy.
// Callers build the new full config (typically from Config() with a few
// fields changed) and pass the pointer; readers of Config() never observe
// a partially updated struct.
func (cm *ConfigManager) Store(cfg *Config) {
	cm.cfg.Store(cfg)
}

// APIEndpointEnvManaged reports whether the SERVER_API_ENDPOINT_ENABLED
// env var is set (to any non-empty value). When true the enabled flag is
// deployment-managed: the UI enable/disable endpoints must refuse and the
// UI renders the state read-only. This is the single source for that rule.
func (cm *ConfigManager) APIEndpointEnvManaged() bool {
	return os.Getenv("SERVER_API_ENDPOINT_ENABLED") != ""
}

// AddYAMLKeys records that the given dotted keys are now explicitly present
// in the config file (e.g. after a settings update wrote them), so future
// loads treat file values as authoritative for those keys.
func (cm *ConfigManager) AddYAMLKeys(keys ...string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for _, k := range keys {
		cm.yamlKeys[k] = true
	}
}

// Save persists the current config (all keys) to the YAML file using atomic
// writes. If no config file was loaded (configPath is empty), defaults to
// defaultConfigPath() (the /config volume in the container image,
// config/config.yaml elsewhere).
func (cm *ConfigManager) Save() error {
	path := cm.configPath
	if path == "" {
		path = defaultConfigPath()
	}
	data, err := yaml.Marshal(cm.Config())
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return atomicWriteFile(path, data, 0o644)
}

// SaveKeys persists ONLY the explicitly changed keys to the YAML file,
// merged into the existing file content, preserving every previously written
// key. This is the settings-update persistence path: writing the whole
// struct (the old Save behavior) would freeze current values of all unedited
// keys over their env-var values (finding: partial YAML save).
func (cm *ConfigManager) SaveKeys(changed map[string]any) error {
	path := cm.configPath
	if path == "" {
		path = defaultConfigPath()
	}

	// Changed keys serialized as a YAML map: top-level section maps get
	// unmarshaled nested; scalar top-level keys map to the key itself.
	var changedFlat map[string]any
	{
		data, err := yaml.Marshal(changed)
		if err != nil {
			return fmt.Errorf("failed to marshal changed keys: %w", err)
		}
		if err := yaml.Unmarshal(data, &changedFlat); err != nil {
			return fmt.Errorf("failed to re-parse changed keys: %w", err)
		}
		if changedFlat == nil {
			changedFlat = make(map[string]any)
		}
	}

	existing := make(map[string]any)
	if raw, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(raw, &existing); err != nil {
			return fmt.Errorf("failed to parse existing config file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing config file: %w", err)
	}

	for topKey, section := range changedFlat {
		switch s := section.(type) {
		case map[string]any:
			cur, ok := existing[topKey].(map[string]any)
			if !ok {
				cur = make(map[string]any)
			}
			for k, v := range s {
				cur[k] = v
			}
			existing[topKey] = cur
		default:
			existing[topKey] = section
		}
	}

	// Preserve the conventional top-level section order for readable diffs.
	ordered := make(map[string]any, len(existing))
	for _, k := range []string{"server", "database", "m4b_merge", "api_key", "directories", "processing"} {
		if v, ok := existing[k]; ok {
			ordered[k] = v
		}
	}
	for k, v := range existing {
		if _, ok := ordered[k]; !ok {
			ordered[k] = v
		}
	}

	data, err := yaml.Marshal(ordered)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return atomicWriteFile(path, data, 0o644)
}

// SetConfigPath overrides the persisted path used by Save/SaveKeys.
// Calling it after construction makes both the save target and the path
// used by LoadFrom explicit.
func (cm *ConfigManager) SetConfigPath(path string) {
	cm.mu.Lock()
	cm.configPath = path
	cm.mu.Unlock()
}

// LoadFrom loads configuration from the explicit YAML file at path and
// swaps it into the live config. A silently missing file leaves the live
// config on defaults with ConfigPath() == ""; a genuine parse error
// returns the error (row 23: load errors must be visible to the caller,
// which decides degradation).
func (cm *ConfigManager) LoadFrom(path string) (string, error) {
	cfg, resolved, err := loadWith(path)
	if err != nil {
		return "", err
	}
	cm.mu.Lock()
	cm.configPath = resolved
	if resolved != "" {
		cm.yamlKeys = yamlKeysFromFile(resolved)
	}
	cm.mu.Unlock()
	cm.cfg.Store(&cfg)
	return resolved, nil
}

// ConfigPath returns the path to the loaded config file, or "" if no file was found.
func (cm *ConfigManager) ConfigPath() string {
	return cm.configPath
}

// YAMLKeys returns the set of dotted keys present in the loaded YAML config file.
// Returns an empty map if no YAML file was loaded.
func (cm *ConfigManager) YAMLKeys() map[string]bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	out := make(map[string]bool, len(cm.yamlKeys))
	for k, v := range cm.yamlKeys {
		out[k] = v
	}
	return out
}

// isEnvTrue reports whether the named env var is set to a true-ish value.
func isEnvTrue(key string) bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// hasEnv reports whether the env var backing the dotted key is set to a
// non-empty value. The loader's env names are the uppercase dot-replaced
// form of the key (plus the REGION/PROCESSING_REGION aliases for
// processing.region).
func hasEnv(key string) bool {
	if key == "processing.region" {
		return os.Getenv("REGION") != "" || os.Getenv("PROCESSING_REGION") != ""
	}
	return os.Getenv(strings.ReplaceAll(strings.ToUpper(key), ".", "_")) != ""
}

// expandTilde expands a leading ~ in a path to the user's home directory.
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

// expandUserPaths expands ~ in directory keys once, at apply time, so the
// live config converges on expanded form regardless of whether the values
// came from the file, env vars, or the settings DB. The DB copy may store
// the raw "~" form (it is what the UI sent), and the live config may need
// the expanded form even when YAML outranked the DB — expanding here makes
// all three sources interchangeable.
func expandUserPaths(cfg *Config) {
	cfg.Directories.InputDir = expandTilde(cfg.Directories.InputDir)
	cfg.Directories.OutputDir = expandTilde(cfg.Directories.OutputDir)
	cfg.Directories.CompletedDir = expandTilde(cfg.Directories.CompletedDir)
}

// Env-var precedence (row 13): effective precedence per key is env var >
// YAML file > default. The YAML override pass (in loadWith, via overrideKey
// + hasEnv) only applies a YAML key when its env var is unset, which keeps
// deployment variables (e.g. API_KEY_API_KEY) authoritative even after a
// settings save wrote a file copy. Viper's AutomaticEnv already layers env
// over defaults at Unmarshal time, but that env value can be MISSING while
// an explicit file key is present and vice versa; re-assert the same env
// vars here, using the identical names hasEnv resolves, so the live config
// unconditionally matches the documented precedence.
func applyEnvPriority(cfg *Config) {
	envVal := func(dotted string) string {
		if dotted == "processing.region" {
			if v := os.Getenv("REGION"); v != "" {
				return v
			}
			return os.Getenv("PROCESSING_REGION")
		}
		return os.Getenv(strings.ReplaceAll(strings.ToUpper(dotted), ".", "_"))
	}
	if v := envVal("server.host"); v != "" {
		cfg.Server.Host = v
	}
	if v := envVal("server.port"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}
	if v := envVal("database.path"); v != "" {
		cfg.Database.Path = v
	}
	if isEnvTrue("SERVER_API_ENDPOINT_ENABLED") {
		cfg.Server.APIEndpoint.Enabled = true
	}
	if v := envVal("m4b_merge.binary"); v != "" {
		cfg.M4bMerge.Binary = v
	}
	if v := envVal("api_key.api_key"); v != "" {
		cfg.APIKey.APIKey = v
	}
	if v := envVal("api_key.base_url"); v != "" {
		cfg.APIKey.BaseURL = v
	}
	if v := envVal("processing.region"); v != "" {
		cfg.Processing.Region = v
	}
}

// parseYAMLMap unmarshals raw YAML bytes into a generic map (nil on error).
func parseYAMLMap(raw []byte) map[string]any {
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
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

// overrideKey copies a single dotted key from yamlOnly into target — but
// only when the env var for that key is unset (env wins, so deployment
// config is never silently frozen by a settings save).
func overrideKey(target, yamlOnly *Config, key string) {
	if hasEnv(key) {
		return
	}
	switch key {
	// Server
	case "server.host":
		target.Server.Host = yamlOnly.Server.Host
	case "server.port":
		target.Server.Port = yamlOnly.Server.Port
	case "server.api_endpoint.enabled":
		target.Server.APIEndpoint.Enabled = yamlOnly.Server.APIEndpoint.Enabled
	// Database
	case "database.path":
		target.Database.Path = yamlOnly.Database.Path
	// M4bMerge
	case "m4b_merge.binary":
		target.M4bMerge.Binary = yamlOnly.M4bMerge.Binary
	// APIKey — the API key persists to the YAML file via settings saves,
	// so it is honored here (env BRAGI_API_KEY_API_KEY keeps priority).
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

// defaultConfigPath is the fallback YAML location for settings writes when
// no config file was loaded. /config is the writable, app-owned volume in
// the container image (the same tree the built-in search paths probe);
// locally the conventional relative path applies (finding: container
// default paths).
func defaultConfigPath() string {
	if st, err := os.Stat("/config"); err == nil && st.IsDir() {
		return "/config/config.yaml"
	}
	return "config/config.yaml"
}

// setDefaults sets default configuration values for Viper.
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.api_endpoint.enabled", false)
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
