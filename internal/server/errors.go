package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// An endpoint is a handler that returns an error in place of an error
// answer. Thus each error becomes an answer in one place: writeError. The
// pattern is from the Go blog post "Error handling and Go".
type endpoint func(w http.ResponseWriter, r *http.Request) error

// handle makes an http.Handler from an endpoint.
func handle(logger *slog.Logger, fn endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			writeError(w, r, logger, err)
		}
	})
}

// apiError is an error that has an answer for the client. An endpoint
// returns it when the client must know the status and the reason.
type apiError struct {
	status int    // The HTTP status of the answer.
	detail string // Tells the client how to correct the problem.
	cause  error  // The error behind the problem. It goes to the log only.
}

func (e *apiError) Error() string {
	if e.cause == nil {
		return e.detail
	}
	return fmt.Sprintf("%s (%v)", e.detail, e.cause)
}

func (e *apiError) Unwrap() error { return e.cause }

// problem is the body of an error answer: the problem details object of
// RFC 9457. It has no "type" member. The RFC says that the HTTP status then
// tells the type of the problem and that the title is the text of the
// status.
type problem struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// writeError turns an error into an answer. An apiError gives its status
// and its detail. Each other error gives the status 500 with no detail,
// because the text of an internal error can show how the service works.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	status, detail := http.StatusInternalServerError, ""
	if apiErr, ok := errors.AsType[*apiError](err); ok {
		status, detail = apiErr.status, apiErr.detail
	}
	if status >= http.StatusInternalServerError {
		logger.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path, "err", err)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	body := problem{Title: http.StatusText(status), Status: status, Detail: detail}
	if err := writeJSON(w, status, body); err != nil {
		logger.ErrorContext(r.Context(), "write the error answer", "err", err)
	}
}
