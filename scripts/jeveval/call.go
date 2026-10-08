package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// endpoint is the one internal/notice.New calls; nothing else is contacted.
const endpoint = "https://api.typesafe.ai/v1/systemone"

// APIError is a non-200 answer. Body is the API's own error text, bounded;
// it never holds the key or the screen.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

// call posts one request: pinned endpoint, no redirect, no retry, the
// caller's deadline. It returns the raw response body.
func call(ctx context.Context, client *http.Client, key string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(data) > 65536 {
		return nil, errors.New("invalid response size")
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.ReplaceAll(strings.TrimSpace(string(data)), key, "[redacted]")
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return nil, &APIError{Status: resp.StatusCode, Body: msg}
	}
	return data, nil
}
