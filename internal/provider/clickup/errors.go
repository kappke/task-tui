package clickup

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var (
	ErrMissingTokenSource = errors.New("clickup token source is not configured")
	ErrMalformedResponse  = errors.New("clickup response is malformed")
	ErrResponseTooLarge   = errors.New("clickup response body exceeds configured limit")
)

// Permanent marks failures that should not be retried by synchronization.
func (e *RequestError) Permanent() bool {
	return e != nil && errors.Is(e.Err, ErrMissingTokenSource)
}

// RequestError describes a failure before a response was received.
type RequestError struct {
	Method string
	URL    string
	Err    error
}

func (e *RequestError) Error() string {
	if e == nil {
		return "clickup request failed"
	}
	return fmt.Sprintf("clickup request %s %s failed: %v", e.Method, e.URL, e.Err)
}

func (e *RequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ResponseError describes a response that could not be consumed as a valid
// ClickUp response, such as a malformed JSON document or an oversized body.
type ResponseError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
	Err        error
}

func (e *ResponseError) Error() string {
	if e == nil {
		return "clickup response failed"
	}
	return fmt.Sprintf("clickup response %s %s failed: %v", e.Method, e.URL, e.Err)
}

func (e *ResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ResponseError) Permanent() bool {
	return e != nil && (errors.Is(e.Err, ErrMalformedResponse) || errors.Is(e.Err, ErrResponseTooLarge))
}

// APIError is returned for an HTTP response with a non-success status code.
// Body is bounded by ClientConfig.MaxBodyBytes and contains only the response
// payload, never request headers or credentials.
type APIError struct {
	StatusCode int
	Status     string
	Method     string
	URL        string
	Message    string
	Code       string
	Body       string
}

func (e *APIError) Error() string {
	if e == nil {
		return "clickup api request failed"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	if e.Status != "" {
		return fmt.Sprintf("clickup api %s %s: %s: %s", e.Method, e.URL, e.Status, message)
	}
	return fmt.Sprintf("clickup api %s %s: %d: %s", e.Method, e.URL, e.StatusCode, message)
}

func (e *APIError) Permanent() bool {
	if e == nil {
		return false
	}
	switch e.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	default:
		return false
	}
}

// RateLimitError is an APIError for HTTP 429 responses. RetryAfter is the
// duration represented by Retry-After. RetryAt is set when the header used an
// HTTP date rather than a number of seconds.
type RateLimitError struct {
	*APIError
	RetryAfter time.Duration
	RetryAt    time.Time
}

func (e *RateLimitError) Error() string {
	if e == nil || e.APIError == nil {
		return "clickup api rate limit exceeded"
	}
	message := e.APIError.Error()
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s (retry after %s)", message, e.RetryAfter)
	}
	return message
}

func (e *RateLimitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.APIError
}

func (e *RateLimitError) Permanent() bool { return false }
