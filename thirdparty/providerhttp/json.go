// Package providerhttp provides bounded JSON requests for external tool APIs.
package providerhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

var ErrResponse = errors.New("invalid or oversized provider response")

func New(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// Post returns the upstream status without exposing error bodies or credentials.
func Post(ctx context.Context, client *http.Client, endpoint, authHeader, credential string, input, output any, limit int64) (int, error) {
	return PostHeaders(ctx, client, endpoint, map[string]string{authHeader: credential}, input, output, limit)
}

// PostHeaders is Post for a service that needs more than one request header,
// or none for credentials.
func PostHeaders(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, input, output any, limit int64) (int, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return res.StatusCode, err
	}
	if int64(len(raw)) > limit || len(raw) == 0 || json.Unmarshal(raw, output) != nil {
		return res.StatusCode, ErrResponse
	}
	return res.StatusCode, nil
}
