package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// APIError is an error with a stable wire `code` (see _docs/api-reference.md "Error codes").
// Handlers return it; anything else that escapes a handler becomes a 500 InternalError.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

func badRequest(code, format string, args ...any) *APIError {
	return &APIError{http.StatusBadRequest, code, fmt.Sprintf(format, args...)}
}

func notFound(code, format string, args ...any) *APIError {
	return &APIError{http.StatusNotFound, code, fmt.Sprintf(format, args...)}
}

func conflict(code, format string, args ...any) *APIError {
	return &APIError{http.StatusConflict, code, fmt.Sprintf(format, args...)}
}

func noSuchBucket(name string) *APIError {
	return notFound("NoSuchBucket", "Bucket '%s' does not exist", name)
}

func noSuchKey(bucket, key string) *APIError {
	return notFound("NoSuchKey", "Object '%s' does not exist in bucket '%s'", key, bucket)
}

type errorResponse struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{code, message, time.Now().UTC()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
