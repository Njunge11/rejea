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
	// faults are the faults in the body of the request, when the problem
	// has more than one part.
	faults []fault
}

// fault is one fault in the body of a request. The pointer tells where the
// fault is: a JSON Pointer as a URI fragment, such as "#/files/0/size".
type fault struct {
	Detail  string `json:"detail"`
	Pointer string `json:"pointer"`
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
// status. The "errors" member is an extension, as in the example of the
// RFC for a request that is not valid.
type problem struct {
	Title  string  `json:"title"`
	Status int     `json:"status"`
	Detail string  `json:"detail,omitempty"`
	Errors []fault `json:"errors,omitempty"`
}

// writeError turns an error into an answer. An apiError gives its status
// and its detail. Each other error gives the status 500 with no detail,
// because the text of an internal error can show how the service works.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	body := problem{Status: http.StatusInternalServerError}
	if apiErr, ok := errors.AsType[*apiError](err); ok {
		body = problem{Status: apiErr.status, Detail: apiErr.detail, Errors: apiErr.faults}
	}
	body.Title = http.StatusText(body.Status)
	if body.Status >= http.StatusInternalServerError {
		logger.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path, "err", err)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	if err := writeJSON(w, body.Status, body); err != nil {
		logger.ErrorContext(r.Context(), "write the error answer", "err", err)
	}
}
