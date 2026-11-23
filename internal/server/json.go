package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

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
