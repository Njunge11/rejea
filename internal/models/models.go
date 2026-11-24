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

	"github.com/Njunge11/rejea/internal/cloudflare"
	"github.com/Njunge11/rejea/internal/embed"
)

// Config is the settings of the models.
type Config struct {
	// Embed is the embedding model.
	Embed embed.Config
	// MaxAttempts is the largest number of attempts for one request to a
	// provider. The attempts after the first are for a request that got no
	// answer, or an answer that tells to try again.
	MaxAttempts int
	// RetryWaitMin and RetryWaitMax are the limits of the wait before a
	// new attempt. The wait is two times as long after each attempt.
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
	// Cloudflare is the Cloudflare account.
	Cloudflare cloudflare.Config
}

// embedders is the registry of the embed port: the name of each provider
// and the function that makes its adapter. A new provider is one new line.
var embedders = map[string]func(cfg Config, client *http.Client) (embed.Provider, error){
	"cloudflare": func(cfg Config, client *http.Client) (embed.Provider, error) {
		return cloudflare.NewEmbedder(cfg.Cloudflare, client)
	},
}

// EmbedProviders gives the names of the providers of the embed port.
func EmbedProviders() []string {
	return slices.Sorted(maps.Keys(embedders))
}

// Embedder makes the embedding client that the settings name. It sends no
// request. With no credentials for the provider, its error is
// embed.ErrNotSet. The counter counts tokens as the embedding model does.
func Embedder(cfg Config, counter embed.Counter, logger *slog.Logger) (*embed.Client, error) {
	open, known := embedders[cfg.Embed.Provider]
	if !known {
		return nil, fmt.Errorf("the embedding provider is %q: the providers are %s",
			cfg.Embed.Provider, strings.Join(EmbedProviders(), ", "))
	}
	client, err := httpClient(cfg, logger)
	if err != nil {
		return nil, err
	}
	provider, err := open(cfg, client)
	if err != nil {
		return nil, err
	}
	return embed.New(provider, counter, logger, cfg.Embed)
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
