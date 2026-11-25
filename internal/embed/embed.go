// Package embed is the port for embedding models: a model turns a text
// into a vector, and texts with a similar meaning get vectors that are near
// to each other. The search compares the vector of a question with the
// vectors of the chunks.
//
// An adapter makes the requests to one provider: it is a Provider. The code
// of this package is the same for each adapter: the batches, the time
// limit, the check of the answer and the usage log.
package embed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// Embedder gives the vectors of texts. The two methods are those of the
// Embedder of LangChain: a model can treat a question and a document in
// different ways.
type Embedder interface {
	// Documents gives one vector for each text, in the sequence of the
	// texts. A text is a part of a document, such as a chunk.
	Documents(ctx context.Context, texts []string) ([][]float32, error)
	// Query gives the vector of a search question.
	Query(ctx context.Context, text string) ([]float32, error)
}

// Provider makes one request to the embedding model of one provider. An
// adapter implements it.
type Provider interface {
	// Embed gives one vector for each text of the request, in the sequence
	// of the texts. Its error has one of the reasons below.
	Embed(ctx context.Context, request Request) (Answer, error)
}

// Request is one request to a model.
type Request struct {
	// Model is the name that the provider gives the model.
	Model string
	// Texts are the texts, each with one character or more.
	Texts []string
	// Query is true when the texts are search questions, and false when
	// they are parts of documents.
	Query bool
	// Instruction tells the model the task of a search question in one
	// sentence. A document has no instruction.
	Instruction string
}

// Answer is the answer of a model to one request.
type Answer struct {
	// Vectors has one vector for each text.
	Vectors [][]float32
	// Tokens is the number of tokens that the provider counted in the
	// texts. The provider bills for them.
	Tokens int
}

// The reasons for which a request fails. An adapter gives one with each
// error, and the caller tells the developer what to do for each one.
var (
	// ErrNotSet: the provider has no credentials.
	ErrNotSet = errors.New("the embedding provider has no credentials")
	// ErrRefused: the provider refuses the credentials.
	ErrRefused = errors.New("the embedding provider refuses the credentials")
	// ErrNoModel: the provider has no model with this name.
	ErrNoModel = errors.New("the embedding provider has no model with this name")
	// ErrQuota: the account used all that it can use for now. A new
	// attempt succeeds only after the provider gives more.
	ErrQuota = errors.New("the account has no usage left at the embedding provider")
	// ErrBusy: the provider gave no answer, or it had no capacity. A new
	// attempt can succeed.
	ErrBusy = errors.New("the embedding provider gave no answer")
	// ErrInput: the provider refuses the texts of the request.
	ErrInput = errors.New("the embedding provider refuses the texts")
	// ErrTooLarge: the texts of the request are too large as a group. The
	// same texts in smaller requests can succeed.
	ErrTooLarge = errors.New("the request is too large for the embedding provider")
	// ErrAnswer: the answer does not have the vectors that the request
	// needs.
	ErrAnswer = errors.New("the answer of the embedding model is not correct")
)

// Config is the settings of the embedding model.
type Config struct {
	// Provider is the name of the provider in the registry.
	Provider string
	// Model is the name that the provider gives the model.
	Model string
	// APIKey is the API key of the account at the provider.
	APIKey string
	// Dimensions is the number of numbers in one vector. The column
	// chunks.embedding has the same number.
	Dimensions int
	// BatchSize is the largest number of texts in one request, and
	// BatchTokens is the largest number of tokens in them. A text that has
	// more tokens goes in a request of its own.
	BatchSize   int
	BatchTokens int
	// Timeout is the longest time for one request, with its new attempts.
	Timeout time.Duration
	// QueryInstruction tells the model the task of a search question.
	QueryInstruction string
}

// Counter counts the tokens of a text, as the embedding model counts them.
type Counter interface {
	Count(text string) (int, error)
}

// Client is an Embedder for the model of one provider. It is safe for use
// by many goroutines.
type Client struct {
	provider Provider
	counter  Counter
	cfg      Config
	logger   *slog.Logger
}

// New makes a Client. It sends no request.
func New(provider Provider, counter Counter, logger *slog.Logger, cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("the embedding model has no name")
	}
	if cfg.Dimensions < 1 || cfg.BatchSize < 1 || cfg.BatchTokens < 1 {
		return nil, fmt.Errorf("the embedding model has %d dimensions, and a batch of %d texts and %d tokens: each must be 1 or more",
			cfg.Dimensions, cfg.BatchSize, cfg.BatchTokens)
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("the embedding model has a time limit of %s: it must be more than zero", cfg.Timeout)
	}
	return &Client{provider: provider, counter: counter, cfg: cfg, logger: logger}, nil
}

// Documents gives one vector for each text. It sends the texts in batches,
// one request after the other. Each text must have one character or more.
func (c *Client) Documents(ctx context.Context, texts []string) ([][]float32, error) {
	batches, err := c.batches(texts)
	if err != nil {
		return nil, err
	}
	vectors := make([][]float32, 0, len(texts))
	for _, batch := range batches {
		part, err := c.documents(ctx, batch)
		if err != nil {
			return nil, err
		}
		vectors = append(vectors, part...)
	}
	return vectors, nil
}

// batches puts the texts in groups, in their sequence: a group has no
// more texts than BatchSize and no more tokens than BatchTokens.
func (c *Client) batches(texts []string) ([][]string, error) {
	var batches [][]string
	start, tokens := 0, 0
	for i, text := range texts {
		count, err := c.counter.Count(text)
		if err != nil {
			return nil, fmt.Errorf("count the tokens of text %d: %w", i+1, err)
		}
		if i > start && (i-start == c.cfg.BatchSize || tokens+count > c.cfg.BatchTokens) {
			batches = append(batches, texts[start:i])
			start, tokens = i, 0
		}
		tokens += count
	}
	if start < len(texts) {
		batches = append(batches, texts[start:])
	}
	return batches, nil
}

// documents sends one batch. When the provider says that a batch is too
// large, the two halves of the batch go in two requests. A provider can
// refuse a batch that is in the limits of the settings: Cloudflare
// publishes no size limit for a request, and it does not always give the
// same answer to the same request.
func (c *Client) documents(ctx context.Context, batch []string) ([][]float32, error) {
	vectors, err := c.embed(ctx, Request{Model: c.cfg.Model, Texts: batch})
	if err == nil || !errors.Is(err, ErrTooLarge) || len(batch) == 1 {
		return vectors, err
	}
	c.logger.WarnContext(ctx, "a batch was too large for the embedding provider: its halves are sent",
		"texts", len(batch), "err", err)
	half := len(batch) / 2
	first, err := c.documents(ctx, batch[:half])
	if err != nil {
		return nil, err
	}
	second, err := c.documents(ctx, batch[half:])
	if err != nil {
		return nil, err
	}
	return slices.Concat(first, second), nil
}

// Query gives the vector of a search question.
func (c *Client) Query(ctx context.Context, text string) ([]float32, error) {
	vectors, err := c.embed(ctx, Request{
		Model: c.cfg.Model, Texts: []string{text}, Query: true, Instruction: c.cfg.QueryInstruction,
	})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

// Check proves the settings with one small request: the credentials, the
// name of the model and the number of dimensions.
func (c *Client) Check(ctx context.Context) error {
	_, err := c.Query(ctx, "check")
	return err
}

// embed sends one request and examines its answer.
func (c *Client) embed(ctx context.Context, request Request) ([][]float32, error) {
	for i, text := range request.Texts {
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("%w: text %d of the request is empty", ErrInput, i+1)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	start := time.Now()
	answer, err := c.provider.Embed(ctx, request)
	if err != nil {
		return nil, err
	}
	if len(answer.Vectors) != len(request.Texts) {
		return nil, fmt.Errorf("%w: %d vectors for %d texts", ErrAnswer, len(answer.Vectors), len(request.Texts))
	}
	for _, vector := range answer.Vectors {
		if len(vector) != c.cfg.Dimensions {
			return nil, fmt.Errorf("%w: a vector of the model %s has %d dimensions, and the setting is %d",
				ErrAnswer, c.cfg.Model, len(vector), c.cfg.Dimensions)
		}
	}
	// The usage log: the provider bills for the tokens.
	kind := "documents"
	if request.Query {
		kind = "query"
	}
	c.logger.InfoContext(ctx, "texts embedded",
		"provider", c.cfg.Provider, "model", c.cfg.Model, "kind", kind,
		"texts", len(request.Texts), "tokens", answer.Tokens, "duration", time.Since(start))
	return answer.Vectors, nil
}
