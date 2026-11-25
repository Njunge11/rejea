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

	"github.com/Njunge11/rejea/internal/anthropic"
	"github.com/Njunge11/rejea/internal/chat"
	"github.com/Njunge11/rejea/internal/chunk"
	"github.com/Njunge11/rejea/internal/cloudflare"
	"github.com/Njunge11/rejea/internal/cohere"
	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/embed"
	"github.com/Njunge11/rejea/internal/ingest"
	"github.com/Njunge11/rejea/internal/models"
	"github.com/Njunge11/rejea/internal/parse"
	"github.com/Njunge11/rejea/internal/pdf"
	"github.com/Njunge11/rejea/internal/queue"
	"github.com/Njunge11/rejea/internal/rerank"
	"github.com/Njunge11/rejea/internal/server"
	"github.com/Njunge11/rejea/internal/storage"
	"github.com/Njunge11/rejea/internal/tokens"
)

// Config is the configuration of the service. Each part of the service
// declares its own settings, and Config has one field for each part.
type Config struct {
	// DatabaseURL is the connection URL of the PostgreSQL database.
	DatabaseURL string
	// StartCheckTimeout is the longest wait for the answer of another
	// service when this service starts.
	StartCheckTimeout time.Duration
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
	PDF       pdf.Config
	Parse     parse.Config
	Word      parse.WordConfig
	Chunk     chunk.Config
	Tokens    tokens.Config
	Models    models.Config
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
	// The longest wait for the answer of another service when this
	// service starts.
	startCheck := env.duration("REJEA_START_CHECK_TIMEOUT", 10*time.Second)
	// Each job of the parse queue has one PDF open.
	parseWorkers := env.count("REJEA_PARSE_WORKERS", 1)

	// Each provider of a model has a default model of its own.
	embedProvider := env.oneOf("REJEA_EMBED_PROVIDER", models.EmbedProviders()...)
	rerankProvider := env.oneOf("REJEA_RERANK_PROVIDER", models.RerankProviders()...)
	chatProvider := env.oneOf("REJEA_CHAT_PROVIDER", models.ChatProviders()...)
	// The keys had the names of their providers before. A settings file
	// with an old name gets the new name, not a model that is not set.
	if env.getenv("CLOUDFLARE_API_TOKEN") != "" {
		env.fault("CLOUDFLARE_API_TOKEN has a new name: change the name of the line to REJEA_EMBED_API_KEY (for the reranker of Cloudflare, give REJEA_RERANK_API_KEY the same token)")
	}
	if env.getenv("COHERE_API_KEY") != "" {
		env.fault("COHERE_API_KEY has a new name: change the name of the line to REJEA_RERANK_API_KEY")
	}
	// One limit for the answer of each model provider.
	maxAnswerBytes := env.size("REJEA_MODEL_MAX_ANSWER_BYTES", 16<<20) // 16 MiB
	cfg := Config{
		DatabaseURL:       env.required("DATABASE_URL"),
		StartCheckTimeout: startCheck,
		HTTPAddr:          env.text("REJEA_HTTP_ADDR", "127.0.0.1:8080"),
		LogLevel:          env.logLevel("REJEA_LOG_LEVEL"),
		LogJSON:           env.oneOf("REJEA_LOG_FORMAT", "text", "json") == "json",
		Storage: storage.Config{
			// The migrate command does not use the bucket, so these two
			// have no default and are not required here.
			Bucket:         env.text("REJEA_S3_BUCKET", ""),
			Region:         env.text("REJEA_S3_REGION", ""),
			Endpoint:       env.text("REJEA_S3_ENDPOINT", ""),
			PublicEndpoint: env.text("REJEA_S3_PUBLIC_ENDPOINT", ""),
			UploadURLLife:  env.duration("REJEA_UPLOAD_URL_LIFE", time.Hour),
			ViewURLLife:    env.duration("REJEA_VIEW_URL_LIFE", time.Hour),
			// No default: the distribution is in the AWS account of the
			// developer. With no values, a view URL goes to the bucket.
			CloudFront: storage.CloudFront{
				Domain:         env.text("REJEA_CLOUDFRONT_DOMAIN", ""),
				KeyPairID:      env.text("REJEA_CLOUDFRONT_KEY_PAIR_ID", ""),
				PrivateKeyFile: env.text("REJEA_CLOUDFRONT_PRIVATE_KEY_FILE", ""),
			},
			MaxFileBytes: maxFileBytes,
			ReadTimeout:  env.duration("REJEA_S3_READ_TIMEOUT", time.Minute),
			RangeBytes:   env.size("REJEA_S3_RANGE_BYTES", 8<<20), // 8 MiB
			CheckTimeout: startCheck,
		},
		Queue: queue.Config{
			FileWorkers:  env.count("REJEA_FILE_WORKERS", 1),
			ParseWorkers: parseWorkers,
			IndexWorkers: env.count("REJEA_INDEX_WORKERS", 1),
			JobTimeout:   env.duration("REJEA_JOB_TIMEOUT", 60*time.Minute),
			MaxAttempts:  env.count("REJEA_JOB_MAX_ATTEMPTS", 8),
			StopTimeout:  env.duration("REJEA_JOB_STOP_TIMEOUT", 10*time.Second),
		},
		Documents: documents.Config{
			MaxUploadFiles:   env.count("REJEA_MAX_UPLOAD_FILES", 100),
			MaxFileBytes:     maxFileBytes,
			UploadMaxAge:     env.duration("REJEA_UPLOAD_MAX_AGE", 24*time.Hour),
			ListLimit:        env.count("REJEA_LIST_LIMIT", 50),
			MaxListLimit:     env.count("REJEA_MAX_LIST_LIMIT", 1000),
			PagesLimit:       env.count("REJEA_PAGES_LIMIT", 100),
			MaxPagesLimit:    env.count("REJEA_MAX_PAGES_LIMIT", 1000),
			ReindexBatchSize: env.count("REJEA_REINDEX_BATCH_SIZE", 500),

			SearchLimit:        env.count("REJEA_SEARCH_LIMIT", 10),
			MaxSearchLimit:     env.count("REJEA_SEARCH_MAX_LIMIT", 50),
			SearchCandidates:   env.count("REJEA_SEARCH_CANDIDATES", 50),
			SearchRankConstant: env.count("REJEA_SEARCH_RANK_CONSTANT", 60),
			MaxQueryLength:     env.count("REJEA_SEARCH_MAX_QUERY_LENGTH", 2000),
			MaxSearchDocuments: env.count("REJEA_SEARCH_MAX_DOCUMENTS", 1000),
			SearchTimeout:      env.duration("REJEA_SEARCH_TIMEOUT", 20*time.Second),
			Rerank:             env.oneOf("REJEA_RERANK", "false", "true") == "true",
			RerankCandidates:   env.count("REJEA_RERANK_CANDIDATES", 50),
		},
		Ingest: ingest.Config{
			FailureSaveTimeout:  env.duration("REJEA_FAILURE_SAVE_TIMEOUT", 10*time.Second),
			UploadCheckInterval: env.duration("REJEA_UPLOAD_CHECK_INTERVAL", time.Hour),
			MaxFileBytes:        maxFileBytes,
			MaxArchiveFiles:     env.count("REJEA_MAX_ARCHIVE_FILES", 1000),
			MaxUnpackedBytes:    env.size("REJEA_MAX_UNPACKED_BYTES", 1_000_000_000), // 1 GB
			MaxArchiveListBytes: env.size("REJEA_MAX_ARCHIVE_LIST_BYTES", 16<<20),    // 16 MiB
		},
		PDF: pdf.Config{
			DPI:           env.count("REJEA_PAGE_DPI", 216),
			MaxPagePixels: env.count("REJEA_MAX_PAGE_PIXELS", 100_000_000),
			MaxOpen:       parseWorkers,
		},
		Parse: parse.Config{
			Name:           env.text("REJEA_PARSER_NAME", "PaddleOCR-VL-1.6"),
			URL:            env.text("REJEA_PARSER_URL", "http://127.0.0.1:8112"),
			Timeout:        env.duration("REJEA_PARSER_TIMEOUT", 5*time.Minute),
			MaxAnswerBytes: env.size("REJEA_PARSER_MAX_ANSWER_BYTES", 16<<20), // 16 MiB
			ReadCharts:     env.oneOf("REJEA_PARSER_READ_CHARTS", "true", "false") == "true",
			ReadImageText:  env.oneOf("REJEA_PARSER_READ_IMAGE_TEXT", "true", "false") == "true",
		},
		Word: parse.WordConfig{
			Path:    env.text("REJEA_WORD_READER_PATH", "bin/office-oxide"),
			Timeout: env.duration("REJEA_WORD_READER_TIMEOUT", time.Minute),
			TempDir: env.text("REJEA_TEMP_DIR", ""),
		},
		Chunk: chunk.Config{
			// Measured with the search report (testset/reports): with the
			// reranker, the search finds the same from 1,024 to 4,096
			// tokens. A chunk of 1,024 is on fewer pages, so a result
			// names its source more exactly.
			MaxTokens: env.count("REJEA_CHUNK_MAX_TOKENS", 1024),
		},
		Tokens: tokens.Config{
			Path: env.text("REJEA_TOKENIZER_PATH", "models/qwen3-embedding-0.6b/tokenizer.json"),
		},
		Models: models.Config{
			// Each model has three settings with the same form: the
			// provider, the model and the API key. No key has a default:
			// each developer has an account of their own.
			Embed: embed.Config{
				Provider:   embedProvider,
				Model:      env.text("REJEA_EMBED_MODEL", models.EmbedModel(embedProvider)),
				APIKey:     env.text("REJEA_EMBED_API_KEY", ""),
				Dimensions: env.count("REJEA_EMBED_DIMENSIONS", 1024),
				// Cloudflare documents 32 texts for one request, and no
				// limit for the tokens of a request. Measured in November
				// 2026: it refused some requests in these limits ("input
				// too big"). The client sends a refused batch again as two
				// halves.
				BatchSize:   env.count("REJEA_EMBED_BATCH_SIZE", 32),
				BatchTokens: env.count("REJEA_EMBED_BATCH_TOKENS", 16000),
				Timeout:     env.duration("REJEA_EMBED_TIMEOUT", 2*time.Minute),
				// The retrieval instruction of the Qwen3 model card.
				QueryInstruction: env.text("REJEA_EMBED_QUERY_INSTRUCTION",
					"Given a web search query, retrieve relevant passages that answer the query"),
			},
			Rerank: rerank.Config{
				Provider: rerankProvider,
				Model:    env.text("REJEA_RERANK_MODEL", models.RerankModel(rerankProvider)),
				APIKey:   env.text("REJEA_RERANK_API_KEY", ""),
				Timeout:  env.duration("REJEA_RERANK_TIMEOUT", 10*time.Second),
			},
			Chat: chat.Config{
				Provider: chatProvider,
				Model:    env.text("REJEA_CHAT_MODEL", models.ChatModel(chatProvider)),
				APIKey:   env.text("REJEA_CHAT_API_KEY", ""),
				// The largest answer that a request with no stream gets
				// before the time limits of HTTP.
				MaxAnswerTokens: env.count("REJEA_CHAT_MAX_ANSWER_TOKENS", 16000),
				Timeout:         env.duration("REJEA_CHAT_TIMEOUT", 5*time.Minute),
			},
			MaxAttempts:  env.count("REJEA_MODEL_MAX_ATTEMPTS", 3),
			RetryWaitMin: env.duration("REJEA_MODEL_RETRY_WAIT_MIN", time.Second),
			RetryWaitMax: env.duration("REJEA_MODEL_RETRY_WAIT_MAX", 30*time.Second),
			Cloudflare: cloudflare.Config{
				// The name that the tools of Cloudflare use. The API token
				// is the key of the model that uses the account.
				AccountID:      env.text("CLOUDFLARE_ACCOUNT_ID", ""),
				BaseURL:        env.text("REJEA_CLOUDFLARE_API_URL", "https://api.cloudflare.com/client/v4"),
				MaxAnswerBytes: maxAnswerBytes,
			},
			Cohere: cohere.Config{
				BaseURL:        env.text("REJEA_COHERE_API_URL", "https://api.cohere.com"),
				MaxAnswerBytes: maxAnswerBytes,
				// Cohere cuts a text at 4,096 tokens when the request gives
				// no number. Its tokens are not the tokens of the embedding
				// model, so the default is the limit of the embedding model.
				MaxTextTokens: env.count("REJEA_COHERE_MAX_TEXT_TOKENS", 8192),
			},
			Anthropic: anthropic.Config{
				BaseURL: env.text("REJEA_ANTHROPIC_API_URL", "https://api.anthropic.com"),
			},
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
	if cfg.Documents.PagesLimit > cfg.Documents.MaxPagesLimit {
		env.fault("REJEA_PAGES_LIMIT (%d) must not be more than REJEA_MAX_PAGES_LIMIT (%d)",
			cfg.Documents.PagesLimit, cfg.Documents.MaxPagesLimit)
	}
	if cfg.Documents.SearchLimit > cfg.Documents.MaxSearchLimit {
		env.fault("REJEA_SEARCH_LIMIT (%d) must not be more than REJEA_SEARCH_MAX_LIMIT (%d)",
			cfg.Documents.SearchLimit, cfg.Documents.MaxSearchLimit)
	}
	// A result comes from the candidates, so a search cannot give more
	// results than candidates. pgvector permits 1000 at most for
	// hnsw.ef_search, which gets this number.
	if cfg.Documents.SearchCandidates < cfg.Documents.MaxSearchLimit || cfg.Documents.SearchCandidates > 1000 {
		env.fault("REJEA_SEARCH_CANDIDATES (%d) must be a number from REJEA_SEARCH_MAX_LIMIT (%d) to 1000",
			cfg.Documents.SearchCandidates, cfg.Documents.MaxSearchLimit)
	}
	// The merge has the chunks of two lists at most, so the reranker cannot
	// read more.
	if cfg.Documents.RerankCandidates > 2*cfg.Documents.SearchCandidates {
		env.fault("REJEA_RERANK_CANDIDATES (%d) must not be more than 2 times REJEA_SEARCH_CANDIDATES (%d): the merge of the two searches has no more chunks",
			cfg.Documents.RerankCandidates, cfg.Documents.SearchCandidates)
	}
	// The answer must come before the server stops the write of it.
	if cfg.Documents.SearchTimeout >= cfg.Server.WriteTimeout {
		env.fault("REJEA_SEARCH_TIMEOUT (%s) must be shorter than REJEA_HTTP_WRITE_TIMEOUT (%s)",
			cfg.Documents.SearchTimeout, cfg.Server.WriteTimeout)
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
