package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Njunge11/rejea/internal/rerank"
)

// Reranker is the rerank.Provider for the reranker models of Workers AI
// that have the request form of bge-reranker-base.
type Reranker struct {
	client *client
}

// NewReranker makes a Reranker. It sends no request. The client makes the
// new attempts of a request that failed.
func NewReranker(cfg Config, httpClient *http.Client) (*Reranker, error) {
	client, err := newClient(cfg, httpClient)
	if errors.Is(err, errNotSet) {
		return nil, rerank.ErrNotSet
	}
	if err != nil {
		return nil, err
	}
	return &Reranker{client: client}, nil
}

// rerankReasons is the reason of the rerank port for each reason of a
// request that failed.
var rerankReasons = reasons{
	busy:      rerank.ErrBusy,
	refused:   rerank.ErrRefused,
	noModel:   rerank.ErrNoModel,
	quota:     rerank.ErrQuota,
	tooLarge:  rerank.ErrTooLarge,
	input:     rerank.ErrInput,
	badAnswer: rerank.ErrAnswer,
}

// rerankRequest is the body of a request, in the form of the input schema
// of the model.
type rerankRequest struct {
	Query    string          `json:"query"`
	Contexts []rerankContext `json:"contexts"`
}

type rerankContext struct {
	Text string `json:"text"`
}

// rerankResult is the result of a request. The output schema of the model
// has "response". Each answer of Workers AI also has "meta", with the
// number of tokens that Cloudflare bills for.
type rerankResult struct {
	Response []struct {
		// ID is the position of the text in the request, from 0.
		ID    *int     `json:"id"`
		Score *float64 `json:"score"`
	} `json:"response"`
	Meta struct {
		CostMetricName1  string  `json:"cost_metric_name_1"`
		CostMetricValue1 float64 `json:"cost_metric_value_1"`
	} `json:"meta"`
}

// Rerank sends one request to the model. The model gives the scores in the
// order from the best, each with the position of its text: the answer has
// them in the sequence of the texts.
func (r *Reranker) Rerank(ctx context.Context, request rerank.Request) (rerank.Answer, error) {
	body := rerankRequest{Query: request.Query, Contexts: make([]rerankContext, len(request.Texts))}
	for i, text := range request.Texts {
		body.Contexts[i].Text = text
	}
	var result rerankResult
	if err := r.client.send(ctx, request.Model, body, &result); err != nil {
		return rerank.Answer{}, rerankReasons.of(err, "make the request for the reranker model")
	}
	scores := make([]float64, len(request.Texts))
	scored := make([]bool, len(request.Texts))
	for _, item := range result.Response {
		if item.ID == nil || item.Score == nil || *item.ID < 0 || *item.ID >= len(scores) || scored[*item.ID] {
			return rerank.Answer{}, fmt.Errorf("%w: a score has no text of the request", rerank.ErrAnswer)
		}
		scores[*item.ID], scored[*item.ID] = *item.Score, true
	}
	if len(result.Response) != len(scores) {
		return rerank.Answer{}, fmt.Errorf("%w: %d scores for %d texts", rerank.ErrAnswer, len(result.Response), len(scores))
	}
	tokens := 0
	if result.Meta.CostMetricName1 == "input_tokens" {
		tokens = int(result.Meta.CostMetricValue1)
	}
	return rerank.Answer{Scores: scores, Tokens: tokens}, nil
}
