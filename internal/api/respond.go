// Package api implements Finalechat's HTTP surface: the JSON API used by
// agents and the app, the server-sent event stream, and static delivery of
// the progressive web app and agent documentation.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ericflo/finalechat/internal/store"
)

// apiError is the JSON error envelope.
type apiError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string { return e.Message }

func errBadRequest(format string, args ...any) *apiError {
	return &apiError{Status: http.StatusBadRequest, Code: "bad_request", Message: fmt.Sprintf(format, args...)}
}

func errValidation(format string, args ...any) *apiError {
	return &apiError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: fmt.Sprintf(format, args...)}
}

var (
	errUnauthorized = &apiError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "Authentication required. Send `Authorization: Bearer fc_...` with an API token."}
	errForbidden    = &apiError{Status: http.StatusForbidden, Code: "forbidden", Message: "You do not have access to this resource."}
	errNotFound     = &apiError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found."}
	errConflict     = &apiError{Status: http.StatusConflict, Code: "conflict", Message: "Conflict."}
	errRateLimited  = &apiError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "Too many attempts. Try again in a minute."}
	errCSRF         = &apiError{Status: http.StatusForbidden, Code: "csrf", Message: "Cross-site request rejected."}
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
	case errors.Is(err, store.ErrNotFound):
		ae = errNotFound
	case errors.Is(err, store.ErrConflict):
		ae = errConflict
	case errors.Is(err, store.ErrInvalidState):
		ae = &apiError{Status: http.StatusConflict, Code: "invalid_state", Message: "The resource is not in a state that allows this operation."}
	default:
		ae = &apiError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Something went wrong on our side."}
	}
	writeJSON(w, ae.Status, map[string]any{"error": ae})
}

// maxJSONBody bounds request bodies; message bodies are validated separately
// against store.MaxBodyBytes.
const maxJSONBody = 1 << 20

func decodeJSON(r *http.Request, into any) error {
	if r.Body == nil {
		return errBadRequest("Request body is required.")
	}
	body := http.MaxBytesReader(nil, r.Body, maxJSONBody)
	dec := json.NewDecoder(body)
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return errBadRequest("Request body is required.")
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return &apiError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "Request body is too large (limit 1 MiB)."}
		}
		return errBadRequest("Invalid JSON: %v", err)
	}
	if dec.More() {
		return errBadRequest("Unexpected data after JSON object.")
	}
	return nil
}

// optionalBool reads a JSON boolean that may be absent.
type optionalBool struct {
	Set   bool
	Value bool
}

func (b *optionalBool) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var v bool
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	b.Set, b.Value = true, v
	return nil
}

func (b optionalBool) ptr() *bool {
	if !b.Set {
		return nil
	}
	v := b.Value
	return &v
}

// optionalString reads a JSON string that may be absent.
type optionalString struct {
	Set   bool
	Value string
}

func (s *optionalString) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	s.Set, s.Value = true, v
	return nil
}

func (s optionalString) ptr() *string {
	if !s.Set {
		return nil
	}
	v := s.Value
	return &v
}

func metaFrom(raw json.RawMessage) (store.JSON, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if len(raw) > 16*1024 {
		return nil, errValidation("meta must be at most 16 KiB.")
	}
	var m store.JSON
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errValidation("meta must be a JSON object.")
	}
	if m == nil {
		m = store.JSON{}
	}
	return m, nil
}
