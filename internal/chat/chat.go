// Package chat is the port for chat models. A chat model reads
// instructions and a question, and writes an answer: text, or JSON in the
// form of a schema.
//
// An adapter makes the requests to one provider: it is a Provider. The code
// of this package is the same for each adapter: the time limit, the check
// of the answer and the usage log.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Provider makes the requests to the chat model of one provider. An adapter
// implements it. Each error has one of the reasons below.
type Provider interface {
	// Complete gives the answer of the model to one request.
	Complete(ctx context.Context, request Request) (Answer, error)
	// Check proves the credentials and the name of the model. It asks the
	// model no question.
	Check(ctx context.Context, model string) error
}

// Request is one request to a model.
type Request struct {
	// Model is the name that the provider gives the model.
	Model string
	// System is the instructions for the model. It can be empty.
	System string
	// Prompt is the question.
	Prompt string
	// Schema is the JSON Schema of the answer. With no schema, the answer
	// is text.
	Schema map[string]any
	// MaxTokens is the largest number of tokens that the model writes.
	MaxTokens int
}

// Answer is the answer of a model to one request.
type Answer struct {
	// Text is the answer. With a schema, it is JSON.
	Text string
	// Model is the model that wrote the answer. A provider can give the
	// request to a different model when the first one declines.
	Model string
	// InputTokens and OutputTokens are the tokens that the provider
	// counted. The provider bills for them.
	InputTokens  int
	OutputTokens int
	// CachedTokens is the part of the input tokens that the provider had
	// from an earlier request. It bills less for them.
	CachedTokens int
}

// The reasons for which a request fails. An adapter gives one with each
// error, and the caller tells the developer what to do for each one.
var (
	// ErrNotSet: the provider has no credentials.
	ErrNotSet = errors.New("the chat provider has no credentials")
	// ErrRefused: the provider refuses the credentials.
	ErrRefused = errors.New("the chat provider refuses the credentials")
	// ErrNoModel: the provider has no model with this name.
	ErrNoModel = errors.New("the chat provider has no model with this name")
	// ErrQuota: the account used all that it can use for now. A new
	// attempt succeeds only after the provider gives more.
	ErrQuota = errors.New("the account has no usage left at the chat provider")
	// ErrBusy: the provider gave no answer, or it had no capacity. A new
	// attempt can succeed.
	ErrBusy = errors.New("the chat provider gave no answer")
	// ErrInput: the provider refuses the request: the instructions, the
	// question, the schema or the number of tokens.
	ErrInput = errors.New("the chat provider refuses the request")
	// ErrTooLarge: the request is too large for the model.
	ErrTooLarge = errors.New("the request is too large for the chat provider")
	// ErrAnswer: the answer is empty, or it is not JSON when the request
	// has a schema.
	ErrAnswer = errors.New("the answer of the chat model is not correct")
	// ErrDeclined: the model declines to answer this question. The same
	// request gets the same result.
	ErrDeclined = errors.New("the chat model declines to answer")
	// ErrCut: the answer needs more tokens than the request permits.
	ErrCut = errors.New("the answer of the chat model is not complete")
)

// Config is the settings of the chat model.
type Config struct {
	// Provider is the name of the provider in the registry.
	Provider string
	// Model is the name that the provider gives the model.
	Model string
	// APIKey is the API key of the account at the provider.
	APIKey string
	// APIURL is the address of the API of the provider.
	APIURL string
	// MaxAnswerTokens is the largest number of tokens of one answer.
	MaxAnswerTokens int
	// Timeout is the longest time for one request, with its new attempts.
	Timeout time.Duration
}

// Client asks the model of one provider. It is safe for use by many
// goroutines.
type Client struct {
	provider Provider
	cfg      Config
	logger   *slog.Logger
}

// New makes a Client. It sends no request.
func New(provider Provider, logger *slog.Logger, cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("the chat model has no name")
	}
	if cfg.MaxAnswerTokens < 1 {
		return nil, fmt.Errorf("an answer of the chat model has %d tokens at most: it must be 1 or more", cfg.MaxAnswerTokens)
	}
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("the chat model has a time limit of %s: it must be more than zero", cfg.Timeout)
	}
	return &Client{provider: provider, cfg: cfg, logger: logger}, nil
}

// Question is one question for the model.
type Question struct {
	// System is the instructions for the model. It can be empty.
	System string
	// Prompt is the question. It must have one character or more.
	Prompt string
	// Schema is the JSON Schema of the answer. With no schema, the answer
	// is text.
	Schema map[string]any
}

// Ask gives the answer of the model to a question. With a schema, the
// answer is JSON in the form of the schema.
func (c *Client) Ask(ctx context.Context, question Question) (string, error) {
	if strings.TrimSpace(question.Prompt) == "" {
		return "", fmt.Errorf("%w: the question is empty", ErrInput)
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	start := time.Now()
	answer, err := c.provider.Complete(ctx, Request{
		Model:     c.cfg.Model,
		System:    question.System,
		Prompt:    question.Prompt,
		Schema:    question.Schema,
		MaxTokens: c.cfg.MaxAnswerTokens,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(answer.Text) == "" {
		return "", fmt.Errorf("%w: it is empty", ErrAnswer)
	}
	if question.Schema != nil && !json.Valid([]byte(answer.Text)) {
		return "", fmt.Errorf("%w: it is not JSON", ErrAnswer)
	}
	// The usage log: the provider bills for the tokens.
	c.logger.InfoContext(ctx, "chat model answered",
		"provider", c.cfg.Provider, "model", answer.Model,
		"input_tokens", answer.InputTokens, "cached_tokens", answer.CachedTokens,
		"output_tokens", answer.OutputTokens, "duration", time.Since(start))
	return answer.Text, nil
}

// Check proves the settings: the credentials and the name of the model.
func (c *Client) Check(ctx context.Context) error {
	return c.provider.Check(ctx, c.cfg.Model)
}
