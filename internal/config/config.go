package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// GatewayConfig is the root configuration for the LLM Token Gateway.
type GatewayConfig struct {
	Server     ServerConfig                `yaml:"server"`
	Providers  map[string]ProviderConfig   `yaml:"providers"`
	Agents     map[string]AgentConfig      `yaml:"agents"`
	Cache      CacheConfig                 `yaml:"cache"`
	Optimizer  OptimizerConfig             `yaml:"optimizer"`
	Classifier ClassifierConfig            `yaml:"classifier"`
	Metrics    MetricsConfig               `yaml:"metrics"`
}

type ServerConfig struct {
	ListenAddr string    `yaml:"listen_addr"`
	TLS        TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type ProviderConfig struct {
	BaseURL    string            `yaml:"base_url"`
	APIKeyEnv  string            `yaml:"api_key_env"`    // Env var name holding the API key
	Headers    map[string]string `yaml:"headers"`        // Extra headers per provider
	RateLimit  int               `yaml:"rate_limit_rpm"` // Requests per minute
	MaxRetries int               `yaml:"max_retries"`
}

type AgentConfig struct {
	Enabled        bool    `yaml:"enabled"`
	Optimize       bool    `yaml:"optimize"`
	TOONThreshold  float64 `yaml:"toon_threshold"`  // Min tabular eligibility (0.0-1.0)
	CompactJSON    bool    `yaml:"compact_json"`     // Minify JSON if TOON not applicable
	CacheTTL       string  `yaml:"cache_ttl"`        // e.g., "300s", "5m"
	MaxContextSize int     `yaml:"max_context_size"` // Max tokens before context pruning
	SpendLimitUSD  float64 `yaml:"spend_limit_usd"`  // Monthly spend cap per agent
}

func (a AgentConfig) GetCacheTTL() time.Duration {
	d, err := time.ParseDuration(a.CacheTTL)
	if err != nil {
		return 5 * time.Minute // default
	}
	return d
}

type CacheConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Backend  string `yaml:"backend"` // "redis" or "memory"
	RedisURL string `yaml:"redis_url"`
	MaxSize  int    `yaml:"max_size_mb"`
	DefaultTTL string `yaml:"default_ttl"`
}

type OptimizerConfig struct {
	TOONEnabled        bool    `yaml:"toon_enabled"`
	CompactJSONEnabled bool    `yaml:"compact_json_enabled"`
	ContextPruning     bool    `yaml:"context_pruning"`
	MinSavingsPercent  float64 `yaml:"min_savings_percent"` // Don't encode if savings < this
	MaxPayloadSize     int     `yaml:"max_payload_size_kb"` // Skip optimization above this
	StripWhitespace    bool    `yaml:"strip_whitespace"`
	DeduplicateTools   bool    `yaml:"deduplicate_tool_results"`
}

type ClassifierConfig struct {
	JSONDetection      bool    `yaml:"json_detection"`
	MinArrayLength     int     `yaml:"min_array_length"`     // Min rows for tabular encoding
	UniformityThreshold float64 `yaml:"uniformity_threshold"` // How similar fields must be
}

type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Endpoint   string `yaml:"endpoint"`   // Prometheus push endpoint (optional)
	DetailLevel string `yaml:"detail_level"` // "basic", "detailed", "verbose"
}

// Load reads and parses the YAML configuration file.
func Load(path string) (*GatewayConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Expand environment variables in the config
	expanded := os.ExpandEnv(string(data))

	cfg := &GatewayConfig{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

func (c *GatewayConfig) validate() error {
	if c.Server.ListenAddr == "" {
		c.Server.ListenAddr = ":8443"
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider must be configured")
	}
	for name, p := range c.Providers {
		if p.BaseURL == "" {
			return fmt.Errorf("provider %q: base_url is required", name)
		}
		if p.APIKeyEnv == "" {
			return fmt.Errorf("provider %q: api_key_env is required", name)
		}
		if os.Getenv(p.APIKeyEnv) == "" {
			return fmt.Errorf("provider %q: env var %s is not set", name, p.APIKeyEnv)
		}
	}
	return nil
}
