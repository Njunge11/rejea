package cloudflare

import (
	"context"
	"errors"
	"net/http"

	"github.com/Njunge11/rejea/internal/embed"
)

// Embedder is the embed.Provider for the embedding models of Workers AI
// that have the request form of the Qwen3 embedding models.
type Embedder struct {
	client *client
}

// NewEmbedder makes an Embedder. It sends no request. The client makes the
// new attempts of a request that failed.
func NewEmbedder(cfg Config, httpClient *http.Client) (*Embedder, error) {
	client, err := newClient(cfg, httpClient)
	if errors.Is(err, errNotSet) {
		return nil, embed.ErrNotSet
	}
	if err != nil {
		return nil, err
	}
	return &Embedder{client: client}, nil
}

// embedReasons is the reason of the embed port for each reason of a
// request that failed.
var embedReasons = reasons{
	busy:      embed.ErrBusy,
	refused:   embed.ErrRefused,
	noModel:   embed.ErrNoModel,
	quota:     embed.ErrQuota,
	tooLarge:  embed.ErrTooLarge,
	input:     embed.ErrInput,
	badAnswer: embed.ErrAnswer,
}

// embedRequest is the body of a request. The model gets the texts of
// "documents" as they are. To each text of "queries" it adds the
// instruction, in the form that the model card of Qwen3 gives.
type embedRequest struct {
	Documents   []string `json:"documents,omitempty"`
	Queries     []string `json:"queries,omitempty"`
	Instruction string   `json:"instruction,omitempty"`
}

// embedResult is the result of a request.
type embedResult struct {
	Data  [][]float32 `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}

// Embed sends one request to the model.
func (e *Embedder) Embed(ctx context.Context, request embed.Request) (embed.Answer, error) {
	body := embedRequest{Documents: request.Texts}
	if request.Query {
		body = embedRequest{Queries: request.Texts, Instruction: request.Instruction}
	}
	var result embedResult
	if err := e.client.send(ctx, request.Model, body, &result); err != nil {
		return embed.Answer{}, embedReasons.of(err, "make the request for the embedding model")
	}
	return embed.Answer{Vectors: result.Data, Tokens: result.Usage.PromptTokens}, nil
}
