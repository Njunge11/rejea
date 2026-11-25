// Package anthropic is the adapter for the Claude models of Anthropic. It
// makes the requests of the Messages API with the SDK of Anthropic, and it
// gives each error the reason that the port knows.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/Njunge11/rejea/internal/chat"
	"github.com/Njunge11/rejea/internal/provider"
)

// Config is the settings of the Anthropic account.
type Config struct {
	// APIKey is an API key of the Anthropic account.
	APIKey string
	// BaseURL is the address of the Anthropic API.
	BaseURL string
}

// Chat is the chat.Provider for the Claude models.
type Chat struct {
	client sdk.Client
}

// NewChat makes a Chat. It sends no request. The HTTP client makes the new
// attempts of a request that failed, so the SDK makes none.
func NewChat(cfg Config, httpClient *http.Client) (*Chat, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, chat.ErrNotSet
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("the URL of the Anthropic API is %q: it must be an http or https URL with a host", cfg.BaseURL)
	}
	return &Chat{client: sdk.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.BaseURL),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
	)}, nil
}

// reasons is the reason of the chat port for each reason of a request that
// failed.
var reasons = provider.Reasons{
	provider.Busy:      chat.ErrBusy,
	provider.Refused:   chat.ErrRefused,
	provider.NoModel:   chat.ErrNoModel,
	provider.Quota:     chat.ErrQuota,
	provider.TooLarge:  chat.ErrTooLarge,
	provider.Input:     chat.ErrInput,
	provider.BadAnswer: chat.ErrAnswer,
}

// Complete sends one request to the model.
//
// The request asks for the refusal fallback of the API: when a safety
// classifier of the model declines the request, Anthropic gives the same
// request to a different Claude model in the same call. Anthropic selects
// that model. A model that has no fallback ignores the field.
func (c *Chat) Complete(ctx context.Context, request chat.Request) (chat.Answer, error) {
	params := sdk.BetaMessageNewParams{
		Model:     request.Model,
		MaxTokens: int64(request.MaxTokens),
		Messages:  []sdk.BetaMessageParam{sdk.NewBetaUserMessage(sdk.NewBetaTextBlock(request.Prompt))},
		Betas:     []sdk.AnthropicBeta{sdk.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: sdk.BetaFallbacksParamOfDefault(),
	}
	if request.System != "" {
		// The instructions are the same for each question of one kind, so
		// the API keeps them: a later request pays less for them.
		params.System = []sdk.BetaTextBlockParam{{Text: request.System, CacheControl: sdk.NewBetaCacheControlEphemeralParam()}}
	}
	if request.Schema != nil {
		params.OutputConfig = sdk.BetaOutputConfigParam{Format: sdk.BetaJSONOutputFormatParam{Schema: request.Schema}}
	}
	message, err := c.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return chat.Answer{}, reasons.Of(failure(err), "")
	}

	switch message.StopReason {
	case sdk.BetaStopReasonRefusal:
		detail := "the provider gives no reason"
		if said := strings.TrimSpace(string(message.StopDetails.Category) + " " + message.StopDetails.Explanation); said != "" {
			detail = said
		}
		return chat.Answer{}, fmt.Errorf("%w: %s", chat.ErrDeclined, detail)
	case sdk.BetaStopReasonMaxTokens:
		return chat.Answer{}, fmt.Errorf("%w: it stopped at the limit of the request (%d tokens)", chat.ErrCut, request.MaxTokens)
	}
	var text strings.Builder
	for _, block := range message.Content {
		if part, ok := block.AsAny().(sdk.BetaTextBlock); ok {
			text.WriteString(part.Text)
		}
	}
	usage := message.Usage
	return chat.Answer{
		Text:  text.String(),
		Model: message.Model,
		// The API counts the tokens that it reads from its cache and that
		// it writes there apart from the other input tokens.
		InputTokens:  int(usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens),
		OutputTokens: int(usage.OutputTokens),
		CachedTokens: int(usage.CacheReadInputTokens),
	}, nil
}

// Check asks the API for the model: the answer proves the API key and the
// name of the model. The provider bills nothing for it.
func (c *Chat) Check(ctx context.Context, model string) error {
	if _, err := c.client.Models.Get(ctx, model, sdk.ModelGetParams{}); err != nil {
		return reasons.Of(failure(err), "")
	}
	return nil
}

// failure gives the fault for a request that failed. The statuses are those
// of the error list of the Anthropic API.
// https://platform.claude.com/docs/en/api/errors
func failure(err error) *provider.Fault {
	apiErr, ok := errors.AsType[*sdk.Error](err)
	if !ok {
		// No answer of the API. The error of the HTTP client has the URL:
		// the cause alone tells what failed.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return &provider.Fault{Reason: provider.Busy, Detail: err}
	}
	detail := fmt.Sprintf("Anthropic answers with status %d", apiErr.StatusCode)
	if apiErr.Message != "" {
		detail += ": " + apiErr.Message
	}
	why := provider.Busy
	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		why = provider.Refused
	case http.StatusNotFound:
		why = provider.NoModel
	case http.StatusPaymentRequired:
		why = provider.Quota
	case http.StatusRequestEntityTooLarge:
		why = provider.TooLarge
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		why = provider.Input
	}
	return &provider.Fault{Reason: why, Detail: errors.New(detail)}
}
