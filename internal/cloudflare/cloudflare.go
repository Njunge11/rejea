// Package cloudflare is the adapter for the models of Cloudflare Workers
// AI. It makes the requests of the REST API, and it gives each error the
// reason that the port knows.
package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Njunge11/rejea/internal/provider"
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

// client makes the requests of the REST API. Each adapter of this package
// has one.
type client struct {
	cfg  Config
	run  *url.URL
	http *http.Client
}

// errNotSet tells that the settings have no account ID or no API token.
// Each adapter gives the reason of its port in its place.
var errNotSet = errors.New("the Cloudflare account has no credentials")

// newClient makes a client. It sends no request. The HTTP client makes the
// new attempts of a request that failed.
func newClient(cfg Config, httpClient *http.Client) (*client, error) {
	if strings.TrimSpace(cfg.AccountID) == "" || strings.TrimSpace(cfg.APIToken) == "" {
		return nil, errNotSet
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("the URL of the Cloudflare API is %q: it must be an http or https URL with a host", cfg.BaseURL)
	}
	if cfg.MaxAnswerBytes < 1 {
		return nil, fmt.Errorf("the answer limit of a model is %d bytes: it must be 1 or more", cfg.MaxAnswerBytes)
	}
	return &client{cfg: cfg, run: base.JoinPath("accounts", cfg.AccountID, "ai", "run"), http: httpClient}, nil
}

// envelope is the form of each answer of the Cloudflare API.
type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

// The codes of Workers AI that tell more than the HTTP status.
// https://developers.cloudflare.com/workers-ai/platform/errors/
const (
	codeDailyLimit = 3036 // "You have used up your daily free allocation"
	codeInput      = 3030 // "invalid input", or "input too big" for texts that are too large as a group
	codeNoRoute    = 7000 // "No route for that URI": the name of the model is not a path of the API
	codeNoModel    = 5007 // "No such model"
)

// send makes one request to a model and reads its result. An error with a
// reason is a *provider.Fault.
func (c *client) send(ctx context.Context, model string, body, result any) error {
	// The name of a model has "/" characters, which are parts of the path.
	address := c.run.String() + "/" + strings.TrimPrefix(model, "/")
	status, raw, err := provider.Post(ctx, c.http, address, c.cfg.APIToken, body, c.cfg.MaxAnswerBytes)
	if err != nil {
		return err
	}
	var decoded envelope
	decodeErr := json.Unmarshal(raw, &decoded)
	if status != http.StatusOK || !decoded.Success {
		return failure(status, decoded)
	}
	if decodeErr == nil {
		decodeErr = json.Unmarshal(decoded.Result, result)
	}
	if decodeErr != nil {
		return &provider.Fault{Reason: provider.BadAnswer, Detail: fmt.Errorf("it is not the JSON of the API: %w", decodeErr)}
	}
	return nil
}

// failure gives the fault for an answer that is not a success.
func failure(status int, decoded envelope) *provider.Fault {
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

	why := provider.Busy
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		why = provider.Refused
	case code == codeNoRoute || code == codeNoModel || status == http.StatusNotFound || status == http.StatusGone:
		why = provider.NoModel
	case code == codeDailyLimit:
		why = provider.Quota
	case status == http.StatusRequestEntityTooLarge || (code == codeInput && strings.Contains(detail, "input too big")):
		why = provider.TooLarge
	case status == http.StatusBadRequest:
		why = provider.Input
	}
	return &provider.Fault{Reason: why, Detail: errors.New(detail)}
}
