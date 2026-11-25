package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Njunge11/rejea/internal/config"
	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/testset"
	"github.com/Njunge11/rejea/internal/tokens"
)

// The modes of the report: the searches that it measures.
const (
	rerankBoth = "both"
	rerankOn   = "on"
	rerankOff  = "off"
)

// errReport tells that the report was not made.
var errReport = errors.New("the search report was not made")

// searchReport measures the search with the questions of a test set. It
// reads the question file from stdin and writes the report as JSON to
// stdout. Each question goes through the search of this service, with the
// settings of this service, so the report has the true settings.
//
// The command runs beside the service (make search-report). It changes no
// row.
func searchReport(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("search-report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	rerank := flags.String("rerank", rerankBoth, "")
	pause := flags.Duration("pause", 0, "")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 || *pause < 0 ||
		(*rerank != rerankBoth && *rerank != rerankOn && *rerank != rerankOff) {
		return fmt.Errorf("%w: the arguments are not correct.\n%s", errReport, usage)
	}

	raw, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("%w: read the question file from stdin: %w", errReport, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return fmt.Errorf("%w: stdin has no question file. Give the file: rejea search-report < testset/questions.json (make search-report does it)", errReport)
	}
	set, err := testset.Parse("of stdin", raw)
	if err != nil {
		return err
	}

	counter, err := tokens.New(cfg.Tokens)
	if err != nil {
		return err
	}
	defer counter.Close() //nolint:errcheck // The command stops.
	embedder, err := startEmbedder(ctx, logger, cfg, counter)
	if err != nil {
		return err
	}
	if embedder == nil {
		return fmt.Errorf("%w: the service has no embedding model, and the search needs one", errReport)
	}
	modes := []bool{false, true}
	switch *rerank {
	case rerankOff:
		modes = []bool{false}
	case rerankOn:
		modes = []bool{true}
	}
	var reranker documents.Reranker
	if *rerank != rerankOff {
		if reranker, err = startReranker(ctx, logger, cfg); err != nil {
			return err
		}
		if reranker == nil {
			return fmt.Errorf("%w: the service has no reranker model. Set its credentials (see \"Reranker\" in the README), or measure without it: make search-report RERANK=off", errReport)
		}
	}
	docs := documents.New(pool, nil, nil, embedder, reranker, cfg.Documents)

	names, ids, err := testDocuments(ctx, docs, set)
	if err != nil {
		return err
	}
	chunks, err := docs.ChunkCount(ctx, ids)
	if err != nil {
		return err
	}

	hash := sha256.Sum256(raw)
	report := testset.Report{
		CreatedAt:       time.Now().UTC().Truncate(time.Second),
		QuestionsSHA256: hex.EncodeToString(hash[:]),
		Settings: testset.Settings{
			ChunkMaxTokens:   cfg.Chunk.MaxTokens,
			EmbedProvider:    cfg.Models.Embed.Provider,
			EmbedModel:       cfg.Models.Embed.Model,
			SearchCandidates: cfg.Documents.SearchCandidates,
			RankConstant:     cfg.Documents.SearchRankConstant,
			RerankProvider:   cfg.Models.Rerank.Provider,
			RerankModel:      cfg.Models.Rerank.Model,
			RerankCandidates: cfg.Documents.RerankCandidates,
		},
		Collection: testset.Collection{Documents: len(ids), Chunks: chunks},
	}
	var measured []testset.Question
	for _, question := range set.Questions {
		if question.Kind == testset.NoAnswer {
			report.NotMeasured++
			continue
		}
		measured = append(measured, question)
	}

	for _, withReranker := range modes {
		results := make([]testset.Result, 0, len(measured))
		for i, question := range measured {
			// A provider can permit only some requests each minute.
			if withReranker && *pause > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(*pause):
				}
			}
			start := time.Now()
			found, err := docs.Search(ctx, documents.SearchRequest{Query: question.Question, Limit: testset.Depth, Rerank: &withReranker})
			elapsed := time.Since(start)
			if err != nil {
				return reportFault(err, question.ID, withReranker)
			}
			inSet := make([]testset.Found, len(found))
			for j, f := range found {
				inSet[j] = testset.Found{Document: names[f.DocumentID], PageStart: f.PageStart, PageEnd: f.PageEnd}
			}
			results = append(results, question.Judge(inSet, elapsed))
			if (i+1)%10 == 0 || i+1 == len(measured) {
				// The progress goes to stderr: stdout is the report.
				if _, err := fmt.Fprintf(stderr, "search report: %d of %d questions, %s\n", i+1, len(measured), reportMode(withReranker)); err != nil {
					return err
				}
			}
		}
		report.Runs = append(report.Runs, testset.Measure(withReranker, results))
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

// testDocuments finds the documents of the test set in the service, by the
// hash of each file. It gives the ID in the test set for each ID in the
// service. Each document of the set must be in the search.
func testDocuments(ctx context.Context, docs *documents.Service, set testset.Set) (map[uuid.UUID]string, []uuid.UUID, error) {
	hashes := make([]string, len(set.Documents))
	for i, doc := range set.Documents {
		hashes[i] = doc.SHA256
	}
	found, err := docs.ByHash(ctx, hashes)
	if err != nil {
		return nil, nil, err
	}
	names := make(map[uuid.UUID]string, len(set.Documents))
	ids := make([]uuid.UUID, 0, len(set.Documents))
	var missing []string
	for _, doc := range set.Documents {
		switch stored, known := found[doc.SHA256]; {
		case !known:
			missing = append(missing, fmt.Sprintf("%s (%s) is not in the service: get the file from %s and upload it", doc.ID, doc.Filename, doc.URL))
		case stored.Status != documents.StatusIndexed:
			missing = append(missing, fmt.Sprintf("%s (%s) has the status %s, and the search reads a document with the status indexed: GET /documents/%s tells more", doc.ID, doc.Filename, stored.Status, stored.ID))
		default:
			names[stored.ID] = doc.ID
			ids = append(ids, stored.ID)
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("%w: the search cannot find %d of the %d documents of the test set:\n  %s",
			errReport, len(missing), len(set.Documents), strings.Join(missing, "\n  "))
	}
	return names, ids, nil
}

// reportMode gives the name of a mode of the search for a person.
func reportMode(withReranker bool) string {
	if withReranker {
		return "with the reranker"
	}
	return "without the reranker"
}

// reportFault gives the error for a search that failed, with what to do.
func reportFault(err error, question string, withReranker bool) error {
	unavailable, isUnavailable := errors.AsType[*documents.Unavailable](err)
	if !isUnavailable {
		return fmt.Errorf("%w: the search of the question %s (%s) failed: %w", errReport, question, reportMode(withReranker), err)
	}
	advice := "Run the command again."
	if withReranker {
		advice = "If the provider of the reranker permits only some requests each minute, give a pause between the questions: make search-report PAUSE=7s (a trial key of Cohere permits 10 requests each minute)."
	}
	return fmt.Errorf("%w: the search of the question %s (%s) failed: %s (%w). %s",
		errReport, question, reportMode(withReranker), unavailable.Detail, unavailable.Cause, advice)
}
