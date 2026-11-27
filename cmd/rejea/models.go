package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/embed"
	"github.com/Njunge11/rejea/internal/models"
	"github.com/Njunge11/rejea/internal/rerank"
)

// startEmbedder makes the client of the embedding model and proves its
// settings with one small request.
//
//   - No credentials: a warning, and no client. The service starts all the
//     same: a document is parsed, and its index job fails with the
//     correction until a provider is set.
//   - Settings that are wrong (the credentials, the name of the model, the
//     number of dimensions): an error, which stops the service.
//   - A provider that gives no answer now, or an account with no usage
//     left: a warning. A later request can succeed.
func startEmbedder(ctx context.Context, logger *slog.Logger, cfg config.Config, counter embed.Counter) (embed.Embedder, error) {
	embedder, err := models.Embedder(cfg.Models, counter, logger)
	if errors.Is(err, embed.ErrNotSet) {
		logger.WarnContext(ctx, "no embedding provider is set: a document is parsed, and its index job fails until one is set",
			"to_correct", `set REJEA_EMBED_API_KEY and REJEA_EMBED_API_URL in .env: see "Embeddings" in the README`)
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
		logger.WarnContext(ctx, "the embedding model does not answer now: the index work of a document tries again later",
			"provider", cfg.Models.Embed.Provider, "err", err)
	default:
		return nil, err
	}
	return embedder, nil
}

// startReranker makes the client of the reranker model and proves its
// settings with one small request. It has the rules of startEmbedder:
//
//   - No credentials: a warning, and no client. A search that asks for the
//     reranker then answers with the reason.
//   - Settings that are wrong (the credentials, the name of the model): an
//     error, which stops the service.
//   - A provider that gives no answer now, or an account with no usage
//     left: a warning. A later request can succeed.
func startReranker(ctx context.Context, logger *slog.Logger, cfg config.Config) (documents.Reranker, error) {
	reranker, err := models.Reranker(cfg.Models, logger)
	if errors.Is(err, rerank.ErrNotSet) {
		logger.WarnContext(ctx, "no reranker provider is set: a search that asks for the reranker fails until one is set",
			"provider", cfg.Models.Rerank.Provider,
			"to_correct", `set REJEA_RERANK_API_KEY in .env to the API key of the provider (the provider cloudflare needs REJEA_RERANK_API_URL also): see "Reranker" in the README`)
		return nil, nil //nolint:nilnil // No client and no error: the service starts with no reranker.
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.StartCheckTimeout)
	defer cancel()
	switch err := reranker.Check(ctx); {
	case err == nil:
		logger.InfoContext(ctx, "reranker model ready",
			"provider", cfg.Models.Rerank.Provider, "model", cfg.Models.Rerank.Model, "default", cfg.Documents.Rerank)
	case errors.Is(err, rerank.ErrBusy), errors.Is(err, rerank.ErrQuota), errors.Is(err, context.DeadlineExceeded):
		logger.WarnContext(ctx, "the reranker model does not answer now: a later search can succeed",
			"provider", cfg.Models.Rerank.Provider, "err", err)
	default:
		return nil, err
	}
	return reranker, nil
}

// errDimensions tells that the vectors of the embedding model do not fit
// the column that keeps them.
var errDimensions = errors.New("the embedding model does not fit the database")

// checkDimensions compares the dimensions of the embedding model with the
// dimensions of the column chunks.embedding. With different numbers, the
// database refuses each chunk.
func checkDimensions(ctx context.Context, db database.DB, cfg config.Config) error {
	column, err := database.EmbeddingDimensions(ctx, db)
	if err != nil {
		return err
	}
	if column != cfg.Models.Embed.Dimensions {
		return fmt.Errorf("%w: REJEA_EMBED_DIMENSIONS is %d, and the column chunks.embedding has %d dimensions",
			errDimensions, cfg.Models.Embed.Dimensions, column)
	}
	return nil
}
