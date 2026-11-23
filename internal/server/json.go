package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// readJSON reads the body of a request as one JSON object of type T. Each
// endpoint that takes a JSON body uses this function. It refuses a body of
// more than maxBytes, a field that T does not have, and data after the
// object. A fault of the client comes back as an apiError that tells the
// client what to correct.
//
// The checks are from "How to Parse a JSON Request Body in Go" by Alex
// Edwards.
func readJSON[T any](w http.ResponseWriter, r *http.Request, maxBytes int64) (T, error) {
	var v T
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return v, &apiError{
				status: http.StatusUnsupportedMediaType,
				detail: "The Content-Type of the request must be application/json.",
			}
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		return v, bodyError(err)
	}
	// A second read must find the end of the body.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			return v, bodyError(err)
		}
		return v, badRequest("The body must have one JSON object and no more.")
	}
	return v, nil
}

// bodyError turns an error of the JSON decoder into an answer for the
// client. The text of the decoder does not go to the client, because it
// names Go types.
func bodyError(err error) error {
	// The decoder has no error value for an unknown field: see
	// https://github.com/golang/go/issues/29035.
	const unknownField = "json: unknown field "
	switch {
	case errors.Is(err, io.EOF):
		return badRequest("The body is empty.")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return badRequest("The body is not complete JSON.")
	case strings.HasPrefix(err.Error(), unknownField):
		return badRequest("The body has the unknown field " + strings.TrimPrefix(err.Error(), unknownField) + ".")
	}
	if syntaxErr, ok := errors.AsType[*json.SyntaxError](err); ok {
		return badRequest(fmt.Sprintf("The body is not valid JSON (at byte %d).", syntaxErr.Offset))
	}
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return badRequest(fmt.Sprintf("The field %q has a value of the wrong type (at byte %d).", typeErr.Field, typeErr.Offset))
	}
	if sizeErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return &apiError{
			status: http.StatusRequestEntityTooLarge,
			detail: fmt.Sprintf("The body is larger than %d bytes.", sizeErr.Limit),
		}
	}
	return fmt.Errorf("read the body: %w", err)
}

func badRequest(detail string) *apiError {
	return &apiError{status: http.StatusBadRequest, detail: detail}
}

// writeJSON writes v as the JSON body of an answer with the given status.
// Each JSON answer of the service goes through this function. The media
// type is application/json unless the caller set a different one.
func writeJSON(w http.ResponseWriter, status int, v any) error {
	// The body is made before the status is sent. Thus a value that has no
	// JSON form becomes an error answer, not half an answer.
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode the answer: %w", err)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	// A write that fails means that the client is gone.
	_, _ = w.Write(body)
	return nil
}
