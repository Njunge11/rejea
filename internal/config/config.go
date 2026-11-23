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
	// S3Bucket is the name of the S3 bucket of the files and S3Region is its
	// AWS Region. The migrate command does not use them.
	S3Bucket string
	S3Region string
	// UploadURLLife is the time for which an upload URL can start an upload.
	UploadURLLife time.Duration
	// MaxUploadFiles is the largest number of files in one request for
	// upload URLs.
	MaxUploadFiles int
	// MaxFileBytes is the largest size of one uploaded file.
	MaxFileBytes int64
	// MaxRequestBytes is the largest size of the JSON body of a request.
	MaxRequestBytes int64
	// The time limits of the HTTP server.
	HTTPReadHeaderTimeout time.Duration // to read the request headers
	HTTPReadTimeout       time.Duration // to read the full request
	HTTPWriteTimeout      time.Duration // to write the response
	HTTPIdleTimeout       time.Duration // to wait for the next request on a kept connection
	// ShutdownTimeout is the time that open requests get to finish when
	// the service stops.
	ShutdownTimeout time.Duration
	// HealthTimeout is the time that the database gets to answer the
	// health check.
	HealthTimeout time.Duration
	// JobStopTimeout is the time that the jobs in work get to finish when
	// the service stops. After it, the jobs are cancelled.
	JobStopTimeout time.Duration
	// S3ReadTimeout is the longest time that a connection to S3 can be
	// silent before a read fails.
	S3ReadTimeout time.Duration
	// FailureSaveTimeout is the time that the database gets to save the
	// failure of a document.
	FailureSaveTimeout time.Duration
}

const (
	defaultHTTPAddr       = "127.0.0.1:8080"
	defaultWorkers        = 1
	defaultJobTimeout     = 60 * time.Minute
	defaultJobMaxAttempts = 8
	defaultUploadURLLife  = time.Hour
	defaultMaxUploadFiles = 100
	defaultMaxFileBytes   = 1 << 30 // 1 GiB
	defaultMaxReqBytes    = 1 << 20 // 1 MiB

	defaultHTTPReadHeaderTimeout = 10 * time.Second
	defaultHTTPReadTimeout       = 30 * time.Second
	defaultHTTPWriteTimeout      = 30 * time.Second
	defaultHTTPIdleTimeout       = 5 * time.Minute
	defaultShutdownTimeout       = 10 * time.Second
	defaultHealthTimeout         = 2 * time.Second
	defaultJobStopTimeout        = 10 * time.Second
	defaultS3ReadTimeout         = time.Minute
	defaultFailureSaveTimeout    = 10 * time.Second
)

// Load reads the configuration through getenv, which has the signature of
// os.Getenv.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL: getenv("DATABASE_URL"),
		HTTPAddr:    defaultHTTPAddr,
		S3Bucket:    getenv("REJEA_S3_BUCKET"),
		S3Region:    getenv("REJEA_S3_REGION"),
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
	// A count has 32 bits, so that it fits an int on each platform. A size
	// in bytes has 64 bits.
	for _, setting := range []struct {
		name string
		bits int
		def  int64
		set  func(int64)
	}{
		{"REJEA_FILE_WORKERS", 32, defaultWorkers, func(n int64) { cfg.FileWorkers = int(n) }},
		{"REJEA_PARSE_WORKERS", 32, defaultWorkers, func(n int64) { cfg.ParseWorkers = int(n) }},
		{"REJEA_INDEX_WORKERS", 32, defaultWorkers, func(n int64) { cfg.IndexWorkers = int(n) }},
		{"REJEA_JOB_MAX_ATTEMPTS", 32, defaultJobMaxAttempts, func(n int64) { cfg.JobMaxAttempts = int(n) }},
		{"REJEA_MAX_UPLOAD_FILES", 32, defaultMaxUploadFiles, func(n int64) { cfg.MaxUploadFiles = int(n) }},
		{"REJEA_MAX_FILE_BYTES", 64, defaultMaxFileBytes, func(n int64) { cfg.MaxFileBytes = n }},
		{"REJEA_MAX_REQUEST_BYTES", 64, defaultMaxReqBytes, func(n int64) { cfg.MaxRequestBytes = n }},
	} {
		n := setting.def
		if text := getenv(setting.name); text != "" {
			var err error
			n, err = strconv.ParseInt(text, 10, setting.bits)
			if err != nil || n < 1 {
				return Config{}, fmt.Errorf("%s: %q is not a whole number of 1 or more", setting.name, text)
			}
		}
		setting.set(n)
	}
	for _, setting := range []struct {
		name  string
		value *time.Duration
		def   time.Duration
	}{
		{"REJEA_JOB_TIMEOUT", &cfg.JobTimeout, defaultJobTimeout},
		{"REJEA_UPLOAD_URL_LIFE", &cfg.UploadURLLife, defaultUploadURLLife},
		{"REJEA_HTTP_READ_HEADER_TIMEOUT", &cfg.HTTPReadHeaderTimeout, defaultHTTPReadHeaderTimeout},
		{"REJEA_HTTP_READ_TIMEOUT", &cfg.HTTPReadTimeout, defaultHTTPReadTimeout},
		{"REJEA_HTTP_WRITE_TIMEOUT", &cfg.HTTPWriteTimeout, defaultHTTPWriteTimeout},
		{"REJEA_HTTP_IDLE_TIMEOUT", &cfg.HTTPIdleTimeout, defaultHTTPIdleTimeout},
		{"REJEA_SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout, defaultShutdownTimeout},
		{"REJEA_HEALTH_TIMEOUT", &cfg.HealthTimeout, defaultHealthTimeout},
		{"REJEA_JOB_STOP_TIMEOUT", &cfg.JobStopTimeout, defaultJobStopTimeout},
		{"REJEA_S3_READ_TIMEOUT", &cfg.S3ReadTimeout, defaultS3ReadTimeout},
		{"REJEA_FAILURE_SAVE_TIMEOUT", &cfg.FailureSaveTimeout, defaultFailureSaveTimeout},
	} {
		*setting.value = setting.def
		text := getenv(setting.name)
		if text == "" {
			continue
		}
		d, err := time.ParseDuration(text)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("%s: %q is not a time of more than zero, such as 60m", setting.name, text)
		}
		*setting.value = d
	}
	return cfg, nil
}
