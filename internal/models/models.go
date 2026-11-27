// Package models connects the model ports of the service to their
// providers. It has the registry (the name of a provider and the function
// that makes its adapter) and the configurator (it reads the settings and
// connects a port, an adapter and the shared HTTP client).
package models

import (
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"

	"github.com/Njunge11/rejea/internal/bedrock"
	"github.com/Njunge11/rejea/internal/cloudflare"
	"github.com/Njunge11/rejea/internal/cohere"
	"github.com/Njunge11/rejea/internal/embed"
	"github.com/Njunge11/rejea/internal/rerank"
)

// Config is the settings of the models. Each model has the name of its
// provider, the name of the model, the API key and the address of the API:
// a change of provider changes these four values, and no name of a
// setting.
type Config struct {
	// Embed is the embedding model.
	Embed embed.Config
	// Rerank is the reranker model.
	Rerank rerank.Config
	// MaxAttempts is the largest number of attempts for one request to a
	// provider. The attempts after the first are for a request that got no
	// answer, or an answer that tells to try again.
	MaxAttempts int
	// RetryWaitMin and RetryWaitMax are the limits of the wait before a
	// new attempt. The wait is two times as long after each attempt.
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
	// MaxAnswerBytes is the largest answer of the embedding model or the
	// reranker model that an adapter reads. The answer is in memory as a
	// whole.
	MaxAnswerBytes int64
}

// entry is one provider of a port. P is the adapter type of the port.
type entry[P any] struct {
	// model is the name of the model when the settings give none.
	model string
	// url is the address of the API when the settings give none. A
	// provider whose address has the ID of the account has no default.
	url string
	// open makes the adapter.
	open func(cfg Config, client *http.Client) (P, error)
}

// adapter makes the adapter of the provider that the settings name. The
// kind names the port in the message for a provider that is not in the
// registry.
func adapter[P any](registry map[string]entry[P], kind, name string, cfg Config, logger *slog.Logger) (P, error) {
	var none P
	found, known := registry[name]
	if !known {
		return none, fmt.Errorf("the %s provider is %q: the providers are %s",
			kind, name, strings.Join(slices.Sorted(maps.Keys(registry)), ", "))
	}
	client, err := httpClient(cfg, logger)
	if err != nil {
		return none, err
	}
	return found.open(cfg, client)
}

// The provider of each model when the settings name none.
const (
	DefaultEmbedProvider  = "cloudflare"
	DefaultRerankProvider = "cloudflare"
)

// embedders is the registry of the embed port: the name of each provider,
// its default model and the function that makes its adapter. A new
// provider is one new entry.
var embedders = map[string]entry[embed.Provider]{
	"cloudflare": {
		model: "@cf/qwen/qwen3-embedding-0.6b",
		open: func(cfg Config, client *http.Client) (embed.Provider, error) {
			return cloudflare.NewEmbedder(cloudflare.Config{
				APIToken: cfg.Embed.APIKey, BaseURL: cfg.Embed.APIURL, MaxAnswerBytes: cfg.MaxAnswerBytes,
			}, client)
		},
	},
}

// EmbedProviders gives the names of the providers of the embed port.
func EmbedProviders() []string {
	return slices.Sorted(maps.Keys(embedders))
}

// EmbedModel gives the default model of a provider of the embed port.
func EmbedModel(provider string) string {
	return embedders[provider].model
}

// EmbedURL gives the default address of a provider of the embed port.
func EmbedURL(provider string) string {
	return embedders[provider].url
}

// Embedder makes the embedding client that the settings name. It sends no
// request. With no credentials for the provider, its error is
// embed.ErrNotSet. The counter counts tokens as the embedding model does.
func Embedder(cfg Config, counter embed.Counter, logger *slog.Logger) (*embed.Client, error) {
	provider, err := adapter(embedders, "embedding", cfg.Embed.Provider, cfg, logger)
	if err != nil {
		return nil, err
	}
	return embed.New(provider, counter, logger, cfg.Embed)
}

// rerankers is the registry of the rerank port: the name of each provider,
// its default model and the function that makes its adapter. A new
// provider is one new entry.
var rerankers = map[string]entry[rerank.Provider]{
	"bedrock": {
		model: "cohere.rerank-v3-5:0",
		// The address has the AWS Region of the model.
		url: "https://bedrock-runtime.us-east-1.amazonaws.com",
		open: func(cfg Config, client *http.Client) (rerank.Provider, error) {
			return bedrock.NewReranker(bedrock.Config{
				APIKey: cfg.Rerank.APIKey, BaseURL: cfg.Rerank.APIURL, MaxAnswerBytes: cfg.MaxAnswerBytes,
			}, client)
		},
	},
	"cloudflare": {
		model: "@cf/baai/bge-reranker-base",
		open: func(cfg Config, client *http.Client) (rerank.Provider, error) {
			return cloudflare.NewReranker(cloudflare.Config{
				APIToken: cfg.Rerank.APIKey, BaseURL: cfg.Rerank.APIURL, MaxAnswerBytes: cfg.MaxAnswerBytes,
			}, client)
		},
	},
	"cohere": {
		model: "rerank-v5.0-fast",
		url:   "https://api.cohere.com",
		open: func(cfg Config, client *http.Client) (rerank.Provider, error) {
			return cohere.NewReranker(cohere.Config{
				APIKey: cfg.Rerank.APIKey, BaseURL: cfg.Rerank.APIURL, MaxAnswerBytes: cfg.MaxAnswerBytes,
				MaxTextTokens: cfg.Rerank.MaxTextTokens,
			}, client)
		},
	},
}

// RerankProviders gives the names of the providers of the rerank port.
func RerankProviders() []string {
	return slices.Sorted(maps.Keys(rerankers))
}

// RerankModel gives the default model of a provider of the rerank port.
func RerankModel(provider string) string {
	return rerankers[provider].model
}

// RerankURL gives the default address of a provider of the rerank port.
func RerankURL(provider string) string {
	return rerankers[provider].url
}

// Reranker makes the reranker client that the settings name. It sends no
// request. With no credentials for the provider, its error is
// rerank.ErrNotSet.
func Reranker(cfg Config, logger *slog.Logger) (*rerank.Client, error) {
	provider, err := adapter(rerankers, "reranker", cfg.Rerank.Provider, cfg, logger)
	if err != nil {
		return nil, err
	}
	return rerank.New(provider, logger, cfg.Rerank)
}

// httpClient makes the HTTP client that each adapter uses. It makes the new
// attempts of a request, so an adapter has no code for them: for a
// request with no answer, for status 429 and for a status from 500. It
// obeys the Retry-After header of an answer.
func httpClient(cfg Config, logger *slog.Logger) (*http.Client, error) {
	if cfg.MaxAttempts < 1 {
		return nil, fmt.Errorf("a model request has %d attempts: it must have 1 or more", cfg.MaxAttempts)
	}
	if cfg.RetryWaitMin <= 0 || cfg.RetryWaitMax < cfg.RetryWaitMin {
		return nil, fmt.Errorf("the wait before a new attempt is from %s to %s: the first must be more than zero, and the second not less than the first",
			cfg.RetryWaitMin, cfg.RetryWaitMax)
	}
	client := retryablehttp.NewClient()
	client.RetryMax = cfg.MaxAttempts - 1
	client.RetryWaitMin = cfg.RetryWaitMin
	client.RetryWaitMax = cfg.RetryWaitMax
	// The library logs the URL of each request, and the URL of a provider
	// can have an account ID. The hook below logs the new attempts.
	client.Logger = nil
	client.RequestLogHook = func(_ retryablehttp.Logger, request *http.Request, attempt int) {
		if attempt > 0 {
			logger.WarnContext(request.Context(), "a model request is sent again",
				"host", request.URL.Host, "attempt", attempt+1, "of", cfg.MaxAttempts)
		}
	}
	// After the last attempt, the adapter gets the last answer: its status
	// and its body tell the reason.
	client.ErrorHandler = retryablehttp.PassthroughErrorHandler
	return client.StandardClient(), nil
}
