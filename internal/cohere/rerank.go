// Package cohere is the adapter for the reranker models of Cohere. It
// makes the requests of the Rerank API (v2), and it gives each error the
// reason that the port knows.
package cohere

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Njunge11/rejea/internal/provider"
	"github.com/Njunge11/rejea/internal/rerank"
)

// Config is the settings of the Cohere account.
type Config struct {
	// APIKey is an API key of the Cohere account.
	APIKey string
	// BaseURL is the address of the Cohere API.
	BaseURL string
	// MaxAnswerBytes is the largest answer that the adapter reads. The
	// answer is in memory as a whole.
	MaxAnswerBytes int64
	// MaxTextTokens is the largest number of tokens of one text that the
	// model reads. Cohere cuts a longer text at this number. Its default
	// is 4,096.
	MaxTextTokens int
}

// Reranker is the rerank.Provider for the Rerank models of Cohere.
type Reranker struct {
	cfg     Config
	address string
	http    *http.Client
}

// NewReranker makes a Reranker. It sends no request. The client makes the
// new attempts of a request that failed.
func NewReranker(cfg Config, httpClient *http.Client) (*Reranker, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, rerank.ErrNotSet
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("the URL of the Cohere API is %q: it must be an http or https URL with a host", cfg.BaseURL)
	}
	if cfg.MaxAnswerBytes < 1 || cfg.MaxTextTokens < 1 {
		return nil, fmt.Errorf("the answer limit of a model is %d bytes, and a text for Cohere has %d tokens at most: each must be 1 or more",
			cfg.MaxAnswerBytes, cfg.MaxTextTokens)
	}
	return &Reranker{cfg: cfg, address: base.JoinPath("v2", "rerank").String(), http: httpClient}, nil
}

// reasons is the reason of the rerank port for each reason of a request
// that failed.
var reasons = provider.Reasons{
	provider.Busy:      rerank.ErrBusy,
	provider.Refused:   rerank.ErrRefused,
	provider.NoModel:   rerank.ErrNoModel,
	provider.Quota:     rerank.ErrQuota,
	provider.TooLarge:  rerank.ErrTooLarge,
	provider.Input:     rerank.ErrInput,
	provider.BadAnswer: rerank.ErrAnswer,
}

// request is the body of a request of the Rerank API.
type request struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	MaxTokensPerDoc int      `json:"max_tokens_per_doc"`
}

// result is the answer of the Rerank API. The results are in the order
// from the best, each with the position of its text in the request.
type result struct {
	Results []struct {
		Index          *int     `json:"index"`
		RelevanceScore *float64 `json:"relevance_score"`
	} `json:"results"`
	Meta struct {
		BilledUnits struct {
			InputTokens float64 `json:"input_tokens"`
		} `json:"billed_units"`
	} `json:"meta"`
	// Message tells the reason of an answer that is not a success.
	Message string `json:"message"`
}

// Rerank sends one request to the model. The answer has the scores in the
// sequence of the texts.
func (r *Reranker) Rerank(ctx context.Context, req rerank.Request) (rerank.Answer, error) {
	body := request{Model: req.Model, Query: req.Query, Documents: req.Texts, MaxTokensPerDoc: r.cfg.MaxTextTokens}
	status, raw, err := provider.Post(ctx, r.http, r.address, r.cfg.APIKey, body, r.cfg.MaxAnswerBytes)
	if err != nil {
		return rerank.Answer{}, reasons.Of(err, "make the request for the reranker model")
	}
	var decoded result
	decodeErr := json.Unmarshal(raw, &decoded)
	if status != http.StatusOK {
		return rerank.Answer{}, reasons.Of(failure(status, decoded.Message), "")
	}
	if decodeErr != nil {
		return rerank.Answer{}, fmt.Errorf("%w: it is not the JSON of the API: %w", rerank.ErrAnswer, decodeErr)
	}
	scores := make([]float64, len(req.Texts))
	scored := make([]bool, len(req.Texts))
	for _, item := range decoded.Results {
		if item.Index == nil || item.RelevanceScore == nil || *item.Index < 0 || *item.Index >= len(scores) || scored[*item.Index] {
			return rerank.Answer{}, fmt.Errorf("%w: a score has no text of the request", rerank.ErrAnswer)
		}
		scores[*item.Index], scored[*item.Index] = *item.RelevanceScore, true
	}
	if len(decoded.Results) != len(scores) {
		return rerank.Answer{}, fmt.Errorf("%w: %d scores for %d texts", rerank.ErrAnswer, len(decoded.Results), len(scores))
	}
	return rerank.Answer{Scores: scores, Tokens: int(decoded.Meta.BilledUnits.InputTokens)}, nil
}

// failure gives the fault for an answer that is not a success. The
// statuses are those of the error list of the Cohere API.
// https://docs.cohere.com/reference/errors
func failure(status int, message string) *provider.Fault {
	detail := fmt.Sprintf("Cohere answers with status %d", status)
	if message != "" {
		detail += ": " + message
	}
	why := provider.Busy
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		why = provider.Refused
	case http.StatusNotFound:
		why = provider.NoModel
	case http.StatusRequestEntityTooLarge:
		why = provider.TooLarge
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		why = provider.Input
	}
	return &provider.Fault{Reason: why, Detail: errors.New(detail)}
}
