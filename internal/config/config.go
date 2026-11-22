// Package config reads the configuration of the service from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
)

// Config is the configuration of the service.
type Config struct {
	// DatabaseURL is the connection URL of the PostgreSQL database.
	DatabaseURL string
	// HTTPAddr is the address that the HTTP server listens on.
	HTTPAddr string
	// LogLevel is the lowest level that the service logs.
	LogLevel slog.Level
	// LogJSON is true when each log line is a JSON object. If not, a line is
	// text for a person to read.
	LogJSON bool
}

const defaultHTTPAddr = "127.0.0.1:8080"

// Load reads the configuration through getenv, which has the signature of
// os.Getenv.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL: getenv("DATABASE_URL"),
		HTTPAddr:    defaultHTTPAddr,
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is not set")
	}
	if addr := getenv("REJEA_HTTP_ADDR"); addr != "" {
		cfg.HTTPAddr = addr
	}
	if level := getenv("REJEA_LOG_LEVEL"); level != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(level)); err != nil {
			return Config{}, fmt.Errorf("REJEA_LOG_LEVEL: %q is not debug, info, warn or error", level)
		}
	}
	switch format := getenv("REJEA_LOG_FORMAT"); format {
	case "", "text":
	case "json":
		cfg.LogJSON = true
	default:
		return Config{}, fmt.Errorf("REJEA_LOG_FORMAT: %q is not text or json", format)
	}
	return cfg, nil
}
