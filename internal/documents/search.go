package documents

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/embed"
	"github.com/Njunge11/rejea/internal/rerank"
)

// QueryEmbedder gives the vector of a search question. The client of the
// embedding model implements it.
type QueryEmbedder interface {
	Query(ctx context.Context, text string) ([]float32, error)
}

// Reranker gives one score for each text: how well the text answers the
// question. A higher score is better. The client of the reranker model
// implements it.
type Reranker interface {
	Scores(ctx context.Context, query string, texts []string) ([]float64, error)
}

// searchable is the statuses of the documents that the search reads. A
// document in the index work again (make reindex) has the status indexing
// and keeps its old chunks until the new chunks take their place, so the
// search finds it during that work. A document in its first index work
// has no chunks. A failed document is not in the search.
var searchable = []string{StatusIndexed, StatusIndexing}

// SearchRequest is one question to the search.
type SearchRequest struct {
	// Query is the question, in the words of a person.
	Query string
	// Limit is the largest number of results. With 0, the search gives the
	// default number.
	Limit int
	// DocumentIDs keeps the search in these documents. With no ID, the
	// search reads each document.
	DocumentIDs []uuid.UUID
	// Metadata keeps the search in the documents whose metadata has each
	// name and value of this object.
	Metadata map[string]any
	// Rerank tells if the search uses the reranker. With nil, the setting
	// of the service decides.
	Rerank *bool
}

// SearchResult is one chunk that the search found. The results of a
// search are in the order from the best to the worst.
type SearchResult struct {
	database.SearchResult
	// RerankScore is the score that the reranker gave the chunk for the
	// question, from 0 to 1. It is nil for a search without the reranker.
	RerankScore *float64
}

// Unavailable tells that the service cannot do the work now, and that the
// request is correct. The detail is for the client. The cause is for the
// log: it can name a setting.
type Unavailable struct {
	Detail string
	Cause  error
}

func (e *Unavailable) Error() string { return fmt.Sprintf("%s (%v)", e.Detail, e.Cause) }

func (e *Unavailable) Unwrap() error { return e.Cause }

// Search gives the chunks that agree best with a question. It is hybrid
// search: a keyword search (BM25) and a vector search of the same
// question, with their two lists merged by reciprocal rank fusion.
//
// The keyword search finds the exact words and numbers of the question.
// The vector search finds text with the same meaning in other words. A
// chunk that is high in the two lists comes first.
//
// With the reranker, the search has a second stage (retrieve, then
// rerank): the reranker model reads the question together with each of
// the best chunks of the merge and gives each a score, and the results are
// in the order of that score.
//
// The error is an InvalidArgument when the request has a value that the
// rules refuse. It is an Unavailable when a model that the search needs is
// not set or gives no answer.
func (s *Service) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	query := strings.TrimSpace(request.Query)
	switch length := utf8.RuneCountInString(query); {
	case query == "":
		return nil, &InvalidArgument{Detail: "The query is empty: give the question as text."}
	case strings.ContainsRune(query, 0):
		return nil, &InvalidArgument{Detail: "The query has a NUL character, which the database cannot read."}
	case length > s.cfg.MaxQueryLength:
		return nil, &InvalidArgument{Detail: fmt.Sprintf("The query has %d characters, and the largest number is %d.", length, s.cfg.MaxQueryLength)}
	}
	limit := request.Limit
	if limit == 0 {
		limit = s.cfg.SearchLimit
	}
	if limit < 0 || limit > s.cfg.MaxSearchLimit {
		return nil, &InvalidArgument{Detail: fmt.Sprintf("The limit must be a number from 1 to %d.", s.cfg.MaxSearchLimit)}
	}
	if len(request.DocumentIDs) > s.cfg.MaxSearchDocuments {
		return nil, &InvalidArgument{Detail: fmt.Sprintf("The filter has %d document IDs, and the largest number is %d.", len(request.DocumentIDs), s.cfg.MaxSearchDocuments)}
	}
	if hasNUL(request.Metadata) {
		return nil, &InvalidArgument{Detail: "The metadata filter has a NUL character, which the database cannot read."}
	}
	metadata := []byte("{}")
	if len(request.Metadata) > 0 {
		encoded, err := json.Marshal(request.Metadata)
		if err != nil {
			return nil, fmt.Errorf("encode the metadata filter: %w", err)
		}
		metadata = encoded
	}
	documentIDs := request.DocumentIDs
	if documentIDs == nil {
		documentIDs = []uuid.UUID{}
	}
	rerank := s.cfg.Rerank
	if request.Rerank != nil {
		rerank = *request.Rerank
	}
	// The reranker selects the results from more chunks than the limit.
	merged := limit
	if rerank {
		merged = max(limit, s.cfg.RerankCandidates)
	}

	ctx, cancel := context.WithTimeout(ctx, s.cfg.SearchTimeout)
	defer cancel()
	vector, err := s.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}

	var found []database.SearchResult
	// One transaction: the scan settings of the vector index apply to the
	// search only.
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := database.SetVectorScan(ctx, tx, s.cfg.SearchCandidates); err != nil {
			return err
		}
		found, err = database.SearchChunks(ctx, tx, database.Search{
			Text:         query,
			Vector:       vector,
			Statuses:     searchable,
			DocumentIDs:  documentIDs,
			Metadata:     metadata,
			Candidates:   s.cfg.SearchCandidates,
			RankConstant: s.cfg.SearchRankConstant,
			Limit:        merged,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &Unavailable{Detail: "The search took too long. Try again.", Cause: err}
		}
		return nil, err
	}
	results := make([]SearchResult, len(found))
	for i, f := range found {
		results[i] = SearchResult{SearchResult: f}
	}
	if !rerank {
		return results, nil
	}
	if err := s.rerank(ctx, query, results); err != nil {
		return nil, err
	}
	return results[:min(limit, len(results))], nil
}

// rerank gives each result the score of the reranker and puts the results
// in the order of that score, the best first. Two results with the same
// score keep the order of the merge. An Unavailable tells the client to
// try again, or tells that the service has no reranker model.
func (s *Service) rerank(ctx context.Context, query string, results []SearchResult) error {
	if s.reranker == nil {
		return &Unavailable{
			Detail: `The service has no reranker model. Send "rerank": false for a search without it.`,
			Cause:  fmt.Errorf(`%w: set the credentials of the reranker provider in .env and start the service again (see "Reranker" in the README)`, rerank.ErrNotSet),
		}
	}
	texts := make([]string, len(results))
	for i, r := range results {
		texts[i] = r.Text
	}
	scores, err := s.reranker.Scores(ctx, query, texts)
	switch {
	case err == nil:
	case errors.Is(err, rerank.ErrBusy), errors.Is(err, context.DeadlineExceeded):
		return &Unavailable{Detail: `The reranker model gives no answer now. Try again, or send "rerank": false for a search without it.`, Cause: err}
	case errors.Is(err, rerank.ErrQuota):
		return &Unavailable{Detail: `The reranker model has no usage left for today. Send "rerank": false for a search without it.`, Cause: err}
	default:
		return fmt.Errorf("rerank the results: %w", err)
	}
	for i := range results {
		results[i].RerankScore = &scores[i]
	}
	slices.SortStableFunc(results, func(a, b SearchResult) int {
		return cmp.Compare(*b.RerankScore, *a.RerankScore)
	})
	return nil
}

// queryVector gives the vector of the question. An Unavailable tells the
// client to try again, or tells that the service has no embedding model.
func (s *Service) queryVector(ctx context.Context, query string) ([]float32, error) {
	if s.embedder == nil {
		return nil, &Unavailable{
			Detail: "The search is not ready: the service has no embedding model.",
			Cause:  fmt.Errorf(`%w: set CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_API_TOKEN in .env and start the service again (see "Embeddings" in the README)`, embed.ErrNotSet),
		}
	}
	vector, err := s.embedder.Query(ctx, query)
	switch {
	case err == nil:
		return vector, nil
	case errors.Is(err, embed.ErrBusy), errors.Is(err, context.DeadlineExceeded):
		return nil, &Unavailable{Detail: "The embedding model gives no answer now. Try again.", Cause: err}
	case errors.Is(err, embed.ErrQuota):
		return nil, &Unavailable{Detail: "The embedding model has no usage left for today.", Cause: err}
	case errors.Is(err, embed.ErrInput), errors.Is(err, embed.ErrTooLarge):
		return nil, &InvalidArgument{Detail: "The embedding model refuses the query. Make it shorter."}
	}
	return nil, fmt.Errorf("embed the query: %w", err)
}
