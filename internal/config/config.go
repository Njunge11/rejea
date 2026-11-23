// Package config reads the configuration of the service from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/ingest"
	"github.com/Njunge11/rejea/internal/queue"
	"github.com/Njunge11/rejea/internal/server"
	"github.com/Njunge11/rejea/internal/storage"
)

// Config is the configuration of the service. Each part of the service
// declares its own settings, and Config has one field for each part.
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

	Storage   storage.Config
	Queue     queue.Config
	Documents documents.Config
	Ingest    ingest.Config
	Server    server.Config
}

// Load reads the configuration through getenv, which has the signature of
// os.Getenv.
//
// Each setting is one line: its field, its name in the environment and its
// default. A value that is not set gets the default. Load reads all the
// settings before it returns, so its error names each value that is wrong,
// and not only the first.
func Load(getenv func(string) string) (Config, error) {
	env := &environment{getenv: getenv}

	// The largest size of one file is a limit of three parts.
	maxFileBytes := env.size("REJEA_MAX_FILE_BYTES", 1<<30) // 1 GiB

	cfg := Config{
		DatabaseURL: env.required("DATABASE_URL"),
		HTTPAddr:    env.text("REJEA_HTTP_ADDR", "127.0.0.1:8080"),
		LogLevel:    env.logLevel("REJEA_LOG_LEVEL"),
		LogJSON:     env.oneOf("REJEA_LOG_FORMAT", "text", "json") == "json",
		Storage: storage.Config{
			// The migrate command does not use the bucket, so these two
			// have no default and are not required here.
			Bucket:        env.text("REJEA_S3_BUCKET", ""),
			Region:        env.text("REJEA_S3_REGION", ""),
			UploadURLLife: env.duration("REJEA_UPLOAD_URL_LIFE", time.Hour),
			MaxFileBytes:  maxFileBytes,
			ReadTimeout:   env.duration("REJEA_S3_READ_TIMEOUT", time.Minute),
			RangeBytes:    env.size("REJEA_S3_RANGE_BYTES", 8<<20), // 8 MiB
		},
		Queue: queue.Config{
			FileWorkers:  env.count("REJEA_FILE_WORKERS", 1),
			ParseWorkers: env.count("REJEA_PARSE_WORKERS", 1),
			IndexWorkers: env.count("REJEA_INDEX_WORKERS", 1),
			JobTimeout:   env.duration("REJEA_JOB_TIMEOUT", 60*time.Minute),
			MaxAttempts:  env.count("REJEA_JOB_MAX_ATTEMPTS", 8),
			StopTimeout:  env.duration("REJEA_JOB_STOP_TIMEOUT", 10*time.Second),
		},
		Documents: documents.Config{
			MaxUploadFiles: env.count("REJEA_MAX_UPLOAD_FILES", 100),
			MaxFileBytes:   maxFileBytes,
			UploadMaxAge:   env.duration("REJEA_UPLOAD_MAX_AGE", 24*time.Hour),
			ListLimit:      env.count("REJEA_LIST_LIMIT", 50),
			MaxListLimit:   env.count("REJEA_MAX_LIST_LIMIT", 1000),
		},
		Ingest: ingest.Config{
			FailureSaveTimeout:  env.duration("REJEA_FAILURE_SAVE_TIMEOUT", 10*time.Second),
			UploadCheckInterval: env.duration("REJEA_UPLOAD_CHECK_INTERVAL", time.Hour),
			MaxFileBytes:        maxFileBytes,
			MaxArchiveFiles:     env.count("REJEA_MAX_ARCHIVE_FILES", 1000),
			MaxUnpackedBytes:    env.size("REJEA_MAX_UNPACKED_BYTES", 1_000_000_000), // 1 GB
			MaxArchiveListBytes: env.size("REJEA_MAX_ARCHIVE_LIST_BYTES", 16<<20),    // 16 MiB
		},
		Server: server.Config{
			ReadHeaderTimeout: env.duration("REJEA_HTTP_READ_HEADER_TIMEOUT", 10*time.Second),
			ReadTimeout:       env.duration("REJEA_HTTP_READ_TIMEOUT", 30*time.Second),
			WriteTimeout:      env.duration("REJEA_HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:       env.duration("REJEA_HTTP_IDLE_TIMEOUT", 5*time.Minute),
			ShutdownTimeout:   env.duration("REJEA_SHUTDOWN_TIMEOUT", 10*time.Second),
			HealthTimeout:     env.duration("REJEA_HEALTH_TIMEOUT", 2*time.Second),
			MaxRequestBytes:   env.size("REJEA_MAX_REQUEST_BYTES", 1<<20), // 1 MiB
		},
	}
	// A client can start its upload until the upload URL ends. A document
	// that fails before that time fails with an upload in work.
	if cfg.Documents.UploadMaxAge <= cfg.Storage.UploadURLLife {
		env.fault("REJEA_UPLOAD_MAX_AGE (%s) must be longer than REJEA_UPLOAD_URL_LIFE (%s)",
			cfg.Documents.UploadMaxAge, cfg.Storage.UploadURLLife)
	}
	if cfg.Documents.ListLimit > cfg.Documents.MaxListLimit {
		env.fault("REJEA_LIST_LIMIT (%d) must not be more than REJEA_MAX_LIST_LIMIT (%d)",
			cfg.Documents.ListLimit, cfg.Documents.MaxListLimit)
	}
	if err := errors.Join(env.faults...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// environment reads settings of each type and keeps each fault that it
// finds. The caller reads all the settings and then looks at the faults one
// time, as the errWriter of the Go blog post "Errors are values" does.
type environment struct {
	getenv func(string) string
	faults []error
}

func (e *environment) fault(format string, args ...any) {
	e.faults = append(e.faults, fmt.Errorf(format, args...))
}

// text reads a setting that can be any text.
func (e *environment) text(name, def string) string {
	if value := e.getenv(name); value != "" {
		return value
	}
	return def
}

// required reads a setting that has no default.
func (e *environment) required(name string) string {
	value := e.getenv(name)
	if value == "" {
		e.fault("%s is not set", name)
	}
	return value
}

// oneOf reads a setting that has a fixed list of values. The first value
// is the default.
func (e *environment) oneOf(name string, values ...string) string {
	value := e.text(name, values[0])
	if !slices.Contains(values, value) {
		e.fault("%s: %q is not %s", name, value, strings.Join(values, " or "))
		return values[0]
	}
	return value
}

// logLevel reads a log level. The default is info.
func (e *environment) logLevel(name string) slog.Level {
	var level slog.Level
	if value := e.getenv(name); value != "" {
		if err := level.UnmarshalText([]byte(value)); err != nil {
			e.fault("%s: %q is not debug, info, warn or error", name, value)
		}
	}
	return level
}

// count reads a whole number of 1 or more that fits an int on each
// platform.
func (e *environment) count(name string, def int) int {
	return int(e.whole(name, int64(def), math.MaxInt32))
}

// size reads a number of bytes, 1 or more.
func (e *environment) size(name string, def int64) int64 {
	return e.whole(name, def, math.MaxInt64)
}

func (e *environment) whole(name string, def, largest int64) int64 {
	value := e.getenv(name)
	if value == "" {
		return def
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 || n > largest {
		e.fault("%s: %q is not a whole number of 1 or more", name, value)
		return def
	}
	return n
}

// duration reads a time of more than zero, in the format of
// time.ParseDuration.
func (e *environment) duration(name string, def time.Duration) time.Duration {
	value := e.getenv(name)
	if value == "" {
		return def
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		e.fault("%s: %q is not a time of more than zero, such as 60m", name, value)
		return def
	}
	return d
}
