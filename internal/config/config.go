// Package config reads the configuration of the service from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
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
	// FileWorkers, ParseWorkers and IndexWorkers are the numbers of jobs that
	// each queue runs at the same time.
	FileWorkers  int
	ParseWorkers int
	IndexWorkers int
	// JobTimeout is the longest time that one attempt of a job can run.
	JobTimeout time.Duration
	// JobMaxAttempts is the number of times that a job runs before it fails
	// with no new attempt.
	JobMaxAttempts int
}

const (
	defaultHTTPAddr       = "127.0.0.1:8080"
	defaultWorkers        = 1
	defaultJobTimeout     = 60 * time.Minute
	defaultJobMaxAttempts = 8
)

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
	for _, setting := range []struct {
		name  string
		value *int
		def   int
	}{
		{"REJEA_FILE_WORKERS", &cfg.FileWorkers, defaultWorkers},
		{"REJEA_PARSE_WORKERS", &cfg.ParseWorkers, defaultWorkers},
		{"REJEA_INDEX_WORKERS", &cfg.IndexWorkers, defaultWorkers},
		{"REJEA_JOB_MAX_ATTEMPTS", &cfg.JobMaxAttempts, defaultJobMaxAttempts},
	} {
		*setting.value = setting.def
		text := getenv(setting.name)
		if text == "" {
			continue
		}
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("%s: %q is not a whole number of 1 or more", setting.name, text)
		}
		*setting.value = n
	}
	cfg.JobTimeout = defaultJobTimeout
	if text := getenv("REJEA_JOB_TIMEOUT"); text != "" {
		timeout, err := time.ParseDuration(text)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("REJEA_JOB_TIMEOUT: %q is not a time of more than zero, such as 60m", text)
		}
		cfg.JobTimeout = timeout
	}
	return cfg, nil
}
