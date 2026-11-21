// Package config reads the configuration of the service from the environment.
package config

import "errors"

// Config is the configuration of the service.
type Config struct {
	// DatabaseURL is the connection URL of the PostgreSQL database.
	DatabaseURL string
	// HTTPAddr is the address that the HTTP server listens on.
	HTTPAddr string
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
	return cfg, nil
}
