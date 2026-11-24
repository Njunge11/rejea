package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Njunge11/rejea/internal/documents"
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

// problemOf gives the answer for an error. An apiError gives its status
// and its detail. An error of the rules of the service gives the status
// that agrees with it. Each other error gives the status 500 with no
// detail, because the text of an internal error can show how the service
// works.
func problemOf(err error) problem {
	if apiErr, ok := errors.AsType[*apiError](err); ok {
		return problem{Status: apiErr.status, Detail: apiErr.detail, Errors: apiErr.faults}
	}
	if invalid, ok := errors.AsType[*documents.Invalid](err); ok {
		faults := make([]fault, len(invalid.Faults))
		for i, f := range invalid.Faults {
			faults[i] = fault{Detail: f.Detail, Pointer: "#/files"}
			if f.File >= 0 {
				faults[i].Pointer = fmt.Sprintf("#/files/%d/%s", f.File, f.Field)
			}
		}
		return problem{Status: http.StatusUnprocessableEntity, Detail: "The request is not valid.", Errors: faults}
	}
	if invalid, ok := errors.AsType[*documents.InvalidArgument](err); ok {
		return problem{Status: http.StatusBadRequest, Detail: invalid.Detail}
	}
	if conflict, ok := errors.AsType[*documents.Conflict](err); ok {
		return problem{Status: http.StatusConflict, Detail: conflict.Detail}
	}
	if unavailable, ok := errors.AsType[*documents.Unavailable](err); ok {
		return problem{Status: http.StatusServiceUnavailable, Detail: unavailable.Detail}
	}
	if errors.Is(err, documents.ErrNotFound) {
		return problem{Status: http.StatusNotFound, Detail: "No document has this id."}
	}
	return problem{Status: http.StatusInternalServerError}
}

// writeError turns an error into an answer. Each endpoint returns its
// error, and this function is the one place that makes the answer.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	body := problemOf(err)
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
