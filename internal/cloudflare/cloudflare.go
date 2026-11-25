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

// reason is why a request failed. Each adapter gives the reason of its
// port for each one.
type reason int

const (
	// busy: no answer, or no capacity. A new attempt can succeed.
	busy reason = iota
	// refused: Cloudflare refuses the credentials.
	refused
	// noModel: Cloudflare has no model with this name.
	noModel
	// quota: the account used all that it can use for now.
	quota
	// tooLarge: the texts of the request are too large as a group.
	tooLarge
	// input: Cloudflare refuses the texts of the request.
	input
	// badAnswer: the answer is not the answer of the API.
	badAnswer
)

// fault is a request that failed, with its reason.
type fault struct {
	reason reason
	detail error
}

func (f *fault) Error() string { return f.detail.Error() }

func (f *fault) Unwrap() error { return f.detail }

// reasons gives the reason of a port for each reason of this package.
type reasons map[reason]error

// of gives the error of the port for an error of send. The work tells what
// failed when the error has no reason.
func (r reasons) of(err error, work string) error {
	if f, ok := errors.AsType[*fault](err); ok {
		return fmt.Errorf("%w: %w", r[f.reason], f.detail)
	}
	return fmt.Errorf("%s: %w", work, err)
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
// reason is a *fault.
func (c *client) send(ctx context.Context, model string, body, result any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	// The name of a model has "/" characters, which are parts of the path.
	address := c.run.String() + "/" + strings.TrimPrefix(model, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.cfg.APIToken)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		// The error of the HTTP client has the URL, and the URL has the
		// account ID. The cause alone tells what failed.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return &fault{reason: busy, detail: err}
	}
	defer response.Body.Close() //nolint:errcheck // The read of the body found each error.

	// One byte more than the limit tells that the answer is too large.
	raw, err := io.ReadAll(io.LimitReader(response.Body, c.cfg.MaxAnswerBytes+1))
	if err != nil {
		return &fault{reason: busy, detail: fmt.Errorf("read the answer: %w", err)}
	}
	if int64(len(raw)) > c.cfg.MaxAnswerBytes {
		return &fault{reason: badAnswer, detail: fmt.Errorf("it is larger than %d bytes", c.cfg.MaxAnswerBytes)}
	}
	var decoded envelope
	decodeErr := json.Unmarshal(raw, &decoded)
	if response.StatusCode != http.StatusOK || !decoded.Success {
		return failure(response.StatusCode, decoded)
	}
	if decodeErr == nil {
		decodeErr = json.Unmarshal(decoded.Result, result)
	}
	if decodeErr != nil {
		return &fault{reason: badAnswer, detail: fmt.Errorf("it is not the JSON of the API: %w", decodeErr)}
	}
	return nil
}

// failure gives the fault for an answer that is not a success.
func failure(status int, decoded envelope) *fault {
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

	why := busy
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		why = refused
	case code == codeNoRoute || code == codeNoModel || status == http.StatusNotFound || status == http.StatusGone:
		why = noModel
	case code == codeDailyLimit:
		why = quota
	case status == http.StatusRequestEntityTooLarge || (code == codeInput && strings.Contains(detail, "input too big")):
		why = tooLarge
	case status == http.StatusBadRequest:
		why = input
	}
	return &fault{reason: why, detail: errors.New(detail)}
}
