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

	"github.com/Njunge11/rejea/internal/anthropic"
	"github.com/Njunge11/rejea/internal/bedrock"
	"github.com/Njunge11/rejea/internal/chat"
	"github.com/Njunge11/rejea/internal/cloudflare"
	"github.com/Njunge11/rejea/internal/cohere"
	"github.com/Njunge11/rejea/internal/embed"
	"github.com/Njunge11/rejea/internal/rerank"
)

// Config is the settings of the models. Each model has the name of its
// provider, the name of the model and the API key: a change of provider
// changes these three values, and no name of a setting. The settings of an
// account that are not a key are with the provider.
type Config struct {
	// Embed is the embedding model.
	Embed embed.Config
	// Rerank is the reranker model.
	Rerank rerank.Config
	// Chat is the chat model.
	Chat chat.Config
	// MaxAttempts is the largest number of attempts for one request to a
	// provider. The attempts after the first are for a request that got no
	// answer, or an answer that tells to try again.
	MaxAttempts int
	// RetryWaitMin and RetryWaitMax are the limits of the wait before a
	// new attempt. The wait is two times as long after each attempt.
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
	// Cloudflare is the Cloudflare account. Its API token is the key of
	// the model that uses it.
	Cloudflare cloudflare.Config
	// Cohere is the Cohere API. Its API key is the key of the model that
	// uses it.
	Cohere cohere.Config
	// Anthropic is the Anthropic API. Its API key is the key of the model
	// that uses it.
	Anthropic anthropic.Config
	// Bedrock is the Bedrock Runtime API of AWS. Its API key is the key of
	// the model that uses it.
	Bedrock bedrock.Config
}

// cloudflareWith gives the settings of the Cloudflare account with the key
// of one model.
func (c Config) cloudflareWith(key string) cloudflare.Config {
	account := c.Cloudflare
	account.APIToken = key
	return account
}

// entry is one provider of a port. P is the adapter type of the port.
type entry[P any] struct {
	// model is the name of the model when the settings give none.
	model string
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

// embedders is the registry of the embed port: the name of each provider,
// its default model and the function that makes its adapter. A new
// provider is one new entry.
var embedders = map[string]entry[embed.Provider]{
	"cloudflare": {
		model: "@cf/qwen/qwen3-embedding-0.6b",
		open: func(cfg Config, client *http.Client) (embed.Provider, error) {
			return cloudflare.NewEmbedder(cfg.cloudflareWith(cfg.Embed.APIKey), client)
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
		open: func(cfg Config, client *http.Client) (rerank.Provider, error) {
			account := cfg.Bedrock
			account.APIKey = cfg.Rerank.APIKey
			return bedrock.NewReranker(account, client)
		},
	},
	"cloudflare": {
		model: "@cf/baai/bge-reranker-base",
		open: func(cfg Config, client *http.Client) (rerank.Provider, error) {
			return cloudflare.NewReranker(cfg.cloudflareWith(cfg.Rerank.APIKey), client)
		},
	},
	"cohere": {
		model: "rerank-v5.0-fast",
		open: func(cfg Config, client *http.Client) (rerank.Provider, error) {
			account := cfg.Cohere
			account.APIKey = cfg.Rerank.APIKey
			return cohere.NewReranker(account, client)
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

// chats is the registry of the chat port: the name of each provider, its
// default model and the function that makes its adapter. A new provider is
// one new entry.
var chats = map[string]entry[chat.Provider]{
	"anthropic": {
		model: "claude-sonnet-5-5",
		open: func(cfg Config, client *http.Client) (chat.Provider, error) {
			account := cfg.Anthropic
			account.APIKey = cfg.Chat.APIKey
			return anthropic.NewChat(account, client)
		},
	},
}

// ChatProviders gives the names of the providers of the chat port.
func ChatProviders() []string {
	return slices.Sorted(maps.Keys(chats))
}

// ChatModel gives the default model of a provider of the chat port.
func ChatModel(provider string) string {
	return chats[provider].model
}

// Chat makes the chat client that the settings name. It sends no request.
// With no credentials for the provider, its error is chat.ErrNotSet.
func Chat(cfg Config, logger *slog.Logger) (*chat.Client, error) {
	provider, err := adapter(chats, "chat", cfg.Chat.Provider, cfg, logger)
	if err != nil {
		return nil, err
	}
	return chat.New(provider, logger, cfg.Chat)
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
