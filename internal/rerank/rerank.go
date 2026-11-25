// Package rerank is the port for reranker models. A reranker reads a
// question together with one text and gives a score: how well the text
// answers the question. The search uses the scores to put its best chunks
// in a better order.
//
// The form is that of the rerank APIs of Cohere and of Cloudflare: one
// question and a list of texts go in, and one score for each text comes
// out.
//
// An adapter makes the requests to one provider: it is a Provider. The code
// of this package is the same for each adapter: the time limit, the check
// of the answer and the usage log.
package rerank

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Provider makes one request to the reranker model of one provider. An
// adapter implements it.
type Provider interface {
	// Rerank gives one score for each text of the request, in the sequence
	// of the texts. Its error has one of the reasons below.
	Rerank(ctx context.Context, request Request) (Answer, error)
}

// Request is one request to a model.
type Request struct {
	// Model is the name that the provider gives the model.
	Model string
	// Query is the question.
	Query string
	// Texts are the texts, each with one character or more.
	Texts []string
}

// Answer is the answer of a model to one request.
type Answer struct {
	// Scores has one score for each text. A higher score is a better
	// answer to the question.
	Scores []float64
	// Tokens is the number of tokens that the provider counted in the
	// request. The provider bills for them.
	Tokens int
}

// The reasons for which a request fails. An adapter gives one with each
// error, and the caller tells the developer what to do for each one.
var (
	// ErrNotSet: the provider has no credentials.
	ErrNotSet = errors.New("the reranker provider has no credentials")
	// ErrRefused: the provider refuses the credentials.
	ErrRefused = errors.New("the reranker provider refuses the credentials")
	// ErrNoModel: the provider has no model with this name.
	ErrNoModel = errors.New("the reranker provider has no model with this name")
	// ErrQuota: the account used all that it can use for now. A new
	// attempt succeeds only after the provider gives more.
	ErrQuota = errors.New("the account has no usage left at the reranker provider")
	// ErrBusy: the provider gave no answer, or it had no capacity. A new
	// attempt can succeed.
	ErrBusy = errors.New("the reranker provider gave no answer")
	// ErrInput: the provider refuses the question or the texts of the
	// request.
	ErrInput = errors.New("the reranker provider refuses the texts")
	// ErrTooLarge: the texts of the request are too large as a group.
	ErrTooLarge = errors.New("the request is too large for the reranker provider")
	// ErrAnswer: the answer does not have the scores that the request
	// needs.
	ErrAnswer = errors.New("the answer of the reranker model is not correct")
)

// Config is the settings of the reranker model.
type Config struct {
	// Provider is the name of the provider in the registry.
	Provider string
	// Model is the name that the provider gives the model.
	Model string
	// Timeout is the longest time for one request, with its new attempts.
	Timeout time.Duration
}

// Client gives the scores of the model of one provider. It is safe for use
// by many goroutines.
type Client struct {
	provider Provider
	cfg      Config
	logger   *slog.Logger
}

// New makes a Client. It sends no request.
func New(provider Provider, logger *slog.Logger, cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("the reranker model has no name")
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("the reranker model has a time limit of %s: it must be more than zero", cfg.Timeout)
	}
	return &Client{provider: provider, cfg: cfg, logger: logger}, nil
}

// Scores gives one score for each text, in the sequence of the texts: how
// well the text answers the question. A higher score is better. The
// question and each text must have one character or more.
func (c *Client) Scores(ctx context.Context, query string, texts []string) ([]float64, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("%w: the question is empty", ErrInput)
	}
	for i, text := range texts {
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("%w: text %d of the request is empty", ErrInput, i+1)
		}
	}
	if len(texts) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	start := time.Now()
	answer, err := c.provider.Rerank(ctx, Request{Model: c.cfg.Model, Query: query, Texts: texts})
	if err != nil {
		return nil, err
	}
	if len(answer.Scores) != len(texts) {
		return nil, fmt.Errorf("%w: %d scores for %d texts", ErrAnswer, len(answer.Scores), len(texts))
	}
	// The usage log: the provider bills for the tokens.
	c.logger.InfoContext(ctx, "texts reranked",
		"provider", c.cfg.Provider, "model", c.cfg.Model,
		"texts", len(texts), "tokens", answer.Tokens, "duration", time.Since(start))
	return answer.Scores, nil
}

// Check proves the settings with one small request: the credentials and
// the name of the model.
func (c *Client) Check(ctx context.Context) error {
	_, err := c.Scores(ctx, "check", []string{"check"})
	return err
}
