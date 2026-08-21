// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package fly is a minimal HTTP client for the Fly.io Machines REST API
// (https://api.machines.dev).
//
// Scope is deliberately narrow: Bearer-token auth, JSON in / JSON out,
// status-code-driven errors, no retries. Formae's rate limiter shapes the
// request rate and its reconciler re-drives failed operations, so a backoff
// loop in here would be a second, invisible one fighting the first.
//
// One transport covers every resource this plugin implements — apps, machines,
// volumes, secrets, certificates and IP assignments are all REST endpoints on
// this host. See docs/ARCHITECTURE.md for why there is no GraphQL client.
package fly

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	// DefaultBaseURL is the public Machines API endpoint. From inside a Fly
	// WireGuard network http://_api.internal:4280 also works; set BaseURL to
	// use it.
	DefaultBaseURL = "https://api.machines.dev"

	// EnvAccessToken and EnvAPIToken are the two env vars flyctl accepts, in
	// flyctl's own precedence order (internal/config/config.go:150 does
	// env.First(FLY_ACCESS_TOKEN, FLY_API_TOKEN)).
	EnvAccessToken = "FLY_ACCESS_TOKEN"
	EnvAPIToken    = "FLY_API_TOKEN"

	defaultTimeout = 60 * time.Second
)

// TokenFromEnv resolves the API token the same way flyctl does: FLY_ACCESS_TOKEN
// first, FLY_API_TOKEN as fallback. Returns "" when neither is set.
//
// flyctl has a third source — the access_token key in ~/.fly/config.yml — that
// this plugin deliberately does not read: it runs inside the formae agent, often
// in a container with no $HOME/.fly, and one export covers the case.
func TokenFromEnv() string {
	if t := os.Getenv(EnvAccessToken); t != "" {
		return t
	}
	return os.Getenv(EnvAPIToken)
}

// Config configures the HTTP client.
type Config struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
	UserAgent  string
}

// Client talks to the Fly.io Machines API.
type Client struct {
	baseURL   string
	token     string
	http      *http.Client
	userAgent string
}

// NewClient constructs a client. Token is required.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("fly: API token is required (set %s or %s)", EnvAccessToken, EnvAPIToken)
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "formae-plugin-fly"
	}
	return &Client{baseURL: base, token: cfg.Token, http: hc, userAgent: ua}, nil
}

// BaseURL reports the endpoint this client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// Request describes one API call.
type Request struct {
	Method string
	Path   string      // begins with "/", e.g. "/v1/apps"
	Body   interface{} // marshalled to JSON when non-nil
	Query  map[string]string
}

// Do executes a request. On 2xx it decodes the JSON body into out (when out is
// non-nil and the body is non-empty). On non-2xx it returns *APIError.
func (c *Client) Do(ctx context.Context, req Request, out interface{}) error {
	var bodyReader io.Reader
	if req.Body != nil {
		b, err := json.Marshal(req.Body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, c.baseURL+req.Path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if len(req.Query) > 0 {
		q := httpReq.URL.Query()
		for k, v := range req.Query {
			q.Set(k, v)
		}
		httpReq.URL.RawQuery = q.Encode()
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    extractMessage(respBody),
			Body:       string(respBody),
		}
	}

	// Several endpoints (DELETE machine, POST secrets) answer 2xx with no body.
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
