// Package provider has the code that each adapter of a model provider
// shares: one JSON request over HTTP, and the reasons for which a request
// fails.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Reason is why a request failed. An adapter gives the reason of its port
// for each one.
type Reason int

const (
	// Busy: no answer, or no capacity. A new attempt can succeed.
	Busy Reason = iota
	// Refused: the provider refuses the credentials.
	Refused
	// NoModel: the provider has no model with this name.
	NoModel
	// Quota: the account used all that it can use for now.
	Quota
	// TooLarge: the texts of the request are too large as a group.
	TooLarge
	// Input: the provider refuses the texts of the request.
	Input
	// BadAnswer: the answer is not the answer of the API.
	BadAnswer
)

// Fault is a request that failed, with its reason.
type Fault struct {
	Reason Reason
	Detail error
}

func (f *Fault) Error() string { return f.Detail.Error() }

func (f *Fault) Unwrap() error { return f.Detail }

// Reasons gives the reason of a port for each Reason.
type Reasons map[Reason]error

// Of gives the error of the port for an error of a request. The work tells
// what failed when the error has no reason.
func (r Reasons) Of(err error, work string) error {
	if f, ok := errors.AsType[*Fault](err); ok {
		return fmt.Errorf("%w: %w", r[f.Reason], f.Detail)
	}
	return fmt.Errorf("%s: %w", work, err)
}

// Post sends the body as JSON with a bearer token and reads the answer. It
// gives the HTTP status and the bytes of the answer. The answer is in
// memory as a whole: maxBytes is the largest answer that it reads. An error
// with a reason is a *Fault.
func Post(ctx context.Context, client *http.Client, address, token string, body any, maxBytes int64) (int, []byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		// The error of the HTTP client has the URL, and the URL of a
		// provider can have an account ID. The cause alone tells what
		// failed.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return 0, nil, &Fault{Reason: Busy, Detail: err}
	}
	defer response.Body.Close() //nolint:errcheck // The read of the body found each error.

	// One byte more than the limit tells that the answer is too large.
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return 0, nil, &Fault{Reason: Busy, Detail: fmt.Errorf("read the answer: %w", err)}
	}
	if int64(len(raw)) > maxBytes {
		return 0, nil, &Fault{Reason: BadAnswer, Detail: fmt.Errorf("it is larger than %d bytes", maxBytes)}
	}
	return response.StatusCode, raw, nil
}
