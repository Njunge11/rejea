package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/embed"
	"github.com/Njunge11/rejea/internal/models"
)

// startEmbedder makes the client of the embedding model and proves its
// settings with one small request.
//
//   - No credentials: a warning, and no client. The service starts all the
//     same: a document is parsed, and it waits for its embeddings.
//   - Settings that are wrong (the credentials, the name of the model, the
//     number of dimensions): an error, which stops the service.
//   - A provider that gives no answer now, or an account with no usage
//     left: a warning. A later request can succeed.
func startEmbedder(ctx context.Context, logger *slog.Logger, cfg config.Config, counter embed.Counter) (*embed.Client, error) {
	embedder, err := models.Embedder(cfg.Models, counter, logger)
	if errors.Is(err, embed.ErrNotSet) {
		logger.WarnContext(ctx, "no embedding provider is set: a document is parsed, and it waits for its embeddings",
			"to_correct", `set CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_API_TOKEN in .env: see "Embeddings" in the README`)
		return nil, nil //nolint:nilnil // No client and no error: the service starts with no embeddings.
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.StartCheckTimeout)
	defer cancel()
	switch err := embedder.Check(ctx); {
	case err == nil:
		logger.InfoContext(ctx, "embedding model ready",
			"provider", cfg.Models.Embed.Provider, "model", cfg.Models.Embed.Model, "dimensions", cfg.Models.Embed.Dimensions)
	case errors.Is(err, embed.ErrBusy), errors.Is(err, embed.ErrQuota), errors.Is(err, context.DeadlineExceeded):
		logger.WarnContext(ctx, "the embedding model does not answer now: a document waits for its embeddings",
			"provider", cfg.Models.Embed.Provider, "err", err)
	default:
		return nil, err
	}
	return embedder, nil
}
