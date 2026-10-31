// Package config loads and validates typed application configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maxid/gable/tenant"
)

// Duration is a YAML-friendly time.Duration.
//
// It accepts Go duration strings ("250ms", "10s") and integers, which are
// interpreted as seconds.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && value.Tag == "!!int" {
		var seconds int64
		if err := value.Decode(&seconds); err != nil {
			return err
		}
		*d = Duration(time.Duration(seconds) * time.Second)
		return nil
	}
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Config is the application configuration.
type Config struct {
	App    App           `yaml:"app"`
	Log    Log           `yaml:"log"`
	HTTP   HTTP          `yaml:"http"`
	DB     DB            `yaml:"db"`
	Tenant tenant.Config `yaml:"tenant"`
}

// App identifies the application.
type App struct {
	Name string `yaml:"name"`
	Env  string `yaml:"env"` // development | production | test
}

// Log configures the glogs-backed logger.
type Log struct {
	Dir    string `yaml:"dir"`
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
}

// HTTP configures the HTTP server that the web module will start.
type HTTP struct {
	Addr         string   `yaml:"addr"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
}

// DB configures the database connection.
type DB struct {
	Driver  string `yaml:"driver"` // mysql | postgres
	DSN     string `yaml:"dsn"`
	MaxOpen int    `yaml:"max_open"`
	MaxIdle int    `yaml:"max_idle"`
}

// Default returns the development defaults.
func Default() *Config {
	return &Config{
		App: App{Name: "app", Env: "development"},
		Log: Log{Dir: "logs", Level: "info", Format: "text"},
		HTTP: HTTP{
			Addr:         ":8080",
			ReadTimeout:  Duration(10 * time.Second),
			WriteTimeout: Duration(10 * time.Second),
		},
		DB:     DB{MaxOpen: 20, MaxIdle: 5},
		Tenant: tenant.DefaultConfig(),
	}
}

// Load reads a YAML file, expands ${VAR} references and applies GABLE_*
// environment overrides.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return LoadBytes(data)
}

// LoadBytes parses configuration from YAML bytes.
func LoadBytes(data []byte) (*Config, error) {
	cfg := Default()
	expanded := os.ExpandEnv(string(data))
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyEnv(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks the values the framework depends on.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.App.Name) == "" {
		return fmt.Errorf("config: app.name is required")
	}
	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: log.level %q is not one of debug/info/warn/error", c.Log.Level)
	}
	switch strings.ToLower(c.Log.Format) {
	case "text", "json":
	default:
		return fmt.Errorf("config: log.format %q is not text or json", c.Log.Format)
	}
	if strings.TrimSpace(c.HTTP.Addr) == "" {
		return fmt.Errorf("config: http.addr is required")
	}
	if c.DB.MaxOpen < 0 || c.DB.MaxIdle < 0 {
		return fmt.Errorf("config: db.max_open and db.max_idle must not be negative")
	}
	if err := c.Tenant.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

func applyEnv(c *Config) {
	setString(&c.App.Name, "GABLE_APP_NAME")
	setString(&c.App.Env, "GABLE_APP_ENV")
	setString(&c.Log.Dir, "GABLE_LOG_DIR")
	setString(&c.Log.Level, "GABLE_LOG_LEVEL")
	setString(&c.Log.Format, "GABLE_LOG_FORMAT")
	setString(&c.HTTP.Addr, "GABLE_HTTP_ADDR")
	setString(&c.DB.Driver, "GABLE_DB_DRIVER")
	setString(&c.DB.DSN, "GABLE_DB_DSN")
	if mode, ok := os.LookupEnv("GABLE_TENANT_MODE"); ok {
		c.Tenant.Mode = tenant.Mode(mode)
	}
	setString(&c.Tenant.RootCode, "GABLE_TENANT_ROOT_CODE")
	setString(&c.Tenant.BaseDomain, "GABLE_TENANT_BASE_DOMAIN")
	setString(&c.Tenant.Header, "GABLE_TENANT_HEADER")
	if raw, ok := os.LookupEnv("GABLE_TENANT_ROOT_ID"); ok {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
			c.Tenant.RootID = id
		}
	}
}

func setString(target *string, key string) {
	if value, ok := os.LookupEnv(key); ok {
		*target = value
	}
}
