// Package cloudflare is the adapter for the models of Cloudflare Workers
// AI. It makes the requests of the REST API, and it gives each error the
// reason that the port knows.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Njunge11/rejea/internal/embed"
)

// Config is the settings of the Cloudflare account.
type Config struct {
	// AccountID is the ID of the Cloudflare account.
	AccountID string
	// APIToken is an API token with the permission to run Workers AI.
	APIToken string
	// BaseURL is the address of the Cloudflare API.
	BaseURL string
	// MaxAnswerBytes is the largest answer that the adapter reads. The
	// answer is in memory as a whole.
	MaxAnswerBytes int64
}

// Embedder is the embed.Provider for the embedding models of Workers AI
// that have the request form of the Qwen3 embedding models.
type Embedder struct {
	cfg    Config
	run    *url.URL
	client *http.Client
}

// NewEmbedder makes an Embedder. It sends no request. The client makes the
// new attempts of a request that failed.
func NewEmbedder(cfg Config, client *http.Client) (*Embedder, error) {
	if strings.TrimSpace(cfg.AccountID) == "" || strings.TrimSpace(cfg.APIToken) == "" {
		return nil, embed.ErrNotSet
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("the URL of the Cloudflare API is %q: it must be an http or https URL with a host", cfg.BaseURL)
	}
	if cfg.MaxAnswerBytes < 1 {
		return nil, fmt.Errorf("the answer limit of a model is %d bytes: it must be 1 or more", cfg.MaxAnswerBytes)
	}
	return &Embedder{cfg: cfg, run: base.JoinPath("accounts", cfg.AccountID, "ai", "run"), client: client}, nil
}

// embedRequest is the body of a request. The model gets the texts of
// "documents" as they are. To each text of "queries" it adds the
// instruction, in the form that the model card of Qwen3 gives.
type embedRequest struct {
	Documents   []string `json:"documents,omitempty"`
	Queries     []string `json:"queries,omitempty"`
	Instruction string   `json:"instruction,omitempty"`
}

// answer is the envelope of each answer of the Cloudflare API.
type answer struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result struct {
		Data  [][]float32 `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	} `json:"result"`
}

// The codes of Workers AI that tell more than the HTTP status.
// https://developers.cloudflare.com/workers-ai/platform/errors/
const (
	codeDailyLimit = 3036 // "You have used up your daily free allocation"
	codeInput      = 3030 // "invalid input", or "input too big" for texts that are too large as a group
	codeNoRoute    = 7000 // "No route for that URI": the name of the model is not a path of the API
	codeNoModel    = 5007 // "No such model"
)

// Embed sends one request to the model.
func (e *Embedder) Embed(ctx context.Context, request embed.Request) (embed.Answer, error) {
	body := embedRequest{Documents: request.Texts}
	if request.Query {
		body = embedRequest{Queries: request.Texts, Instruction: request.Instruction}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return embed.Answer{}, fmt.Errorf("make the request for the embedding model: %w", err)
	}
	// The name of a model has "/" characters, which are parts of the path.
	address := e.run.String() + "/" + strings.TrimPrefix(request.Model, "/")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(encoded))
	if err != nil {
		return embed.Answer{}, fmt.Errorf("make the request for the embedding model: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+e.cfg.APIToken)
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := e.client.Do(httpRequest)
	if err != nil {
		// The error of the HTTP client has the URL, and the URL has the
		// account ID. The cause alone tells what failed.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return embed.Answer{}, fmt.Errorf("%w: %w", embed.ErrBusy, err)
	}
	defer response.Body.Close() //nolint:errcheck // The read of the body found each error.

	// One byte more than the limit tells that the answer is too large.
	raw, err := io.ReadAll(io.LimitReader(response.Body, e.cfg.MaxAnswerBytes+1))
	if err != nil {
		return embed.Answer{}, fmt.Errorf("%w: read the answer: %w", embed.ErrBusy, err)
	}
	if int64(len(raw)) > e.cfg.MaxAnswerBytes {
		return embed.Answer{}, fmt.Errorf("%w: it is larger than %d bytes", embed.ErrAnswer, e.cfg.MaxAnswerBytes)
	}
	var decoded answer
	decodeErr := json.Unmarshal(raw, &decoded)
	if response.StatusCode != http.StatusOK || !decoded.Success {
		return embed.Answer{}, failure(response.StatusCode, decoded)
	}
	if decodeErr != nil {
		return embed.Answer{}, fmt.Errorf("%w: it is not the JSON of the API: %w", embed.ErrAnswer, decodeErr)
	}
	return embed.Answer{Vectors: decoded.Result.Data, Tokens: decoded.Result.Usage.PromptTokens}, nil
}

// failure gives the error for an answer that is not a success, with its
// reason.
func failure(status int, decoded answer) error {
	code := 0
	said := make([]string, 0, len(decoded.Errors))
	for _, e := range decoded.Errors {
		code = e.Code
		said = append(said, fmt.Sprintf("%s (code %d)", e.Message, e.Code))
	}
	detail := fmt.Sprintf("Cloudflare answers with status %d", status)
	if len(said) > 0 {
		detail += ": " + strings.Join(said, ", ")
	}

	reason := embed.ErrBusy
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		reason = embed.ErrRefused
	case code == codeNoRoute || code == codeNoModel || status == http.StatusNotFound || status == http.StatusGone:
		reason = embed.ErrNoModel
	case code == codeDailyLimit:
		reason = embed.ErrQuota
	case status == http.StatusRequestEntityTooLarge || (code == codeInput && strings.Contains(detail, "input too big")):
		reason = embed.ErrTooLarge
	case status == http.StatusBadRequest:
		reason = embed.ErrInput
	}
	return fmt.Errorf("%w: %s", reason, detail)
}
