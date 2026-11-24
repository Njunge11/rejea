package documents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/embed"
)

// QueryEmbedder gives the vector of a search question. The client of the
// embedding model implements it.
type QueryEmbedder interface {
	Query(ctx context.Context, text string) ([]float32, error)
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
}

// SearchResult is one chunk that the search found. The results of a
// search are in the order from the best to the worst.
type SearchResult = database.SearchResult

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
// The error is an InvalidArgument when the request has a value that the
// rules refuse. It is an Unavailable when the embedding model is not set
// or gives no answer.
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

	ctx, cancel := context.WithTimeout(ctx, s.cfg.SearchTimeout)
	defer cancel()
	vector, err := s.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	// One transaction: the scan settings of the vector index apply to the
	// search only.
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := database.SetVectorScan(ctx, tx, s.cfg.SearchCandidates); err != nil {
			return err
		}
		results, err = database.SearchChunks(ctx, tx, database.Search{
			Text:         query,
			Vector:       vector,
			Statuses:     searchable,
			DocumentIDs:  documentIDs,
			Metadata:     metadata,
			Candidates:   s.cfg.SearchCandidates,
			RankConstant: s.cfg.SearchRankConstant,
			Limit:        limit,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &Unavailable{Detail: "The search took too long. Try again.", Cause: err}
		}
		return nil, err
	}
	return results, nil
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
