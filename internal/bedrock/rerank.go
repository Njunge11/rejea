// Package bedrock is the adapter for the Cohere Rerank models on Amazon
// Bedrock. It makes the requests of the InvokeModel operation of the
// Bedrock Runtime API, with an Amazon Bedrock API key as the bearer token,
// and it gives each error the reason that the port knows.
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html
// https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys-use.html
package bedrock

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

// apiVersion is the version of the Cohere Rerank API that the models on
// Bedrock read. Bedrock refuses a request with a lower number.
const apiVersion = 2

// Config is the settings of the AWS account.
type Config struct {
	// APIKey is an Amazon Bedrock API key.
	APIKey string
	// BaseURL is the address of the Bedrock Runtime API of one AWS Region.
	BaseURL string
	// MaxAnswerBytes is the largest answer that the adapter reads. The
	// answer is in memory as a whole.
	MaxAnswerBytes int64
}

// Reranker is the rerank.Provider for the Cohere Rerank models on Amazon
// Bedrock.
type Reranker struct {
	cfg  Config
	base *url.URL
	http *http.Client
}

// NewReranker makes a Reranker. It sends no request. The client makes the
// new attempts of a request that failed.
func NewReranker(cfg Config, httpClient *http.Client) (*Reranker, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, rerank.ErrNotSet
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("the URL of the Bedrock Runtime API is %q: it must be an http or https URL with a host", cfg.BaseURL)
	}
	if cfg.MaxAnswerBytes < 1 {
		return nil, fmt.Errorf("the answer limit of a model is %d bytes: it must be 1 or more", cfg.MaxAnswerBytes)
	}
	return &Reranker{cfg: cfg, base: base, http: httpClient}, nil
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

// request is the body of a request: the form of the Cohere Rerank API. The
// name of the model is in the address. With no number of results, the
// answer has a score for each text.
type request struct {
	APIVersion int      `json:"api_version"`
	Query      string   `json:"query"`
	Documents  []string `json:"documents"`
}

// result is the answer of the model. The results are in the order from the
// best, each with the position of its text in the request.
type result struct {
	Results []struct {
		Index          *int     `json:"index"`
		RelevanceScore *float64 `json:"relevance_score"`
	} `json:"results"`
	// Message tells the reason of an answer that is not a success. Bedrock
	// writes the name as "message" or as "Message": the decoder reads the
	// two.
	Message string `json:"message"`
}

// Rerank sends one request to the model. The answer has the scores in the
// sequence of the texts. It has no number of tokens: AWS bills for each
// request, and the answer gives no count.
func (r *Reranker) Rerank(ctx context.Context, req rerank.Request) (rerank.Answer, error) {
	address := r.base.JoinPath("model", req.Model, "invoke").String()
	body := request{APIVersion: apiVersion, Query: req.Query, Documents: req.Texts}
	status, raw, err := provider.Post(ctx, r.http, address, r.cfg.APIKey, body, r.cfg.MaxAnswerBytes)
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
	return rerank.Answer{Scores: scores}, nil
}

// noModel is in the message of Bedrock for a model name that it does not
// know. Bedrock gives status 400 for it, as for a request with a wrong
// body.
const noModel = "model identifier is invalid"

// failure gives the fault for an answer that is not a success. The
// statuses are those of the error list of the InvokeModel operation.
func failure(status int, message string) *provider.Fault {
	detail := fmt.Sprintf("Amazon Bedrock answers with status %d", status)
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
	case http.StatusBadRequest:
		why = provider.Input
		if strings.Contains(message, noModel) {
			why = provider.NoModel
		}
	}
	return &provider.Fault{Reason: why, Detail: errors.New(detail)}
}
