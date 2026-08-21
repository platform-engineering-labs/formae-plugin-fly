// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package fly

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientRequiresToken(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("NewClient with no token should fail")
	}
	c, err := NewClient(Config{Token: "fm2_abc"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
}

func TestDoSendsBearerTokenAndDecodes(t *testing.T) {
	var gotAuth, gotPath, gotQuery, gotCT, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotCT = r.Header.Get("Content-Type")
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"my-app","status":"deployed"}`))
	}))
	defer srv.Close()

	c, err := NewClient(Config{Token: "fm2_secret", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var out struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	err = c.Do(context.Background(), Request{
		Method: "POST",
		Path:   "/v1/apps",
		Body:   map[string]string{"app_name": "my-app"},
		Query:  map[string]string{"org_slug": "my-org"},
	}, &out)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer fm2_secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotMethod != "POST" || gotPath != "/v1/apps" {
		t.Errorf("got %s %s", gotMethod, gotPath)
	}
	if gotQuery != "org_slug=my-org" {
		t.Errorf("query = %q", gotQuery)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if out.Name != "my-app" || out.Status != "deployed" {
		t.Errorf("decoded = %+v", out)
	}
}

// A GET must not advertise a JSON body it does not have.
func TestDoOmitsContentTypeWithoutBody(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "t", BaseURL: srv.URL})
	if err := c.Do(context.Background(), Request{Method: "GET", Path: "/v1/apps/x"}, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCT != "" {
		t.Errorf("Content-Type = %q, want empty", gotCT)
	}
}

func TestDoReturnsAPIErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"error":"name has already been taken"}`))
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "t", BaseURL: srv.URL})
	err := c.Do(context.Background(), Request{Method: "POST", Path: "/v1/apps"}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 422 {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Message != "name has already been taken" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

// Several Fly endpoints (DELETE machine, POST secrets) answer 200 with no body.
// Decoding must not turn that into an error.
func TestDoTolerates204AndEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "t", BaseURL: srv.URL})
	var out map[string]any
	if err := c.Do(context.Background(), Request{Method: "DELETE", Path: "/v1/apps/x/machines/y"}, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestDoHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, _ := NewClient(Config{Token: "t", BaseURL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Do(ctx, Request{Method: "GET", Path: "/v1/apps"}, nil); err == nil {
		t.Fatal("want error from cancelled context")
	}
}

func TestTokenFromEnvPrefersAccessToken(t *testing.T) {
	// flyctl does env.First(FLY_ACCESS_TOKEN, FLY_API_TOKEN) —
	// internal/config/config.go:150. Match it so a token that works with
	// `fly` works here.
	t.Setenv(EnvAccessToken, "access")
	t.Setenv(EnvAPIToken, "api")
	if got := TokenFromEnv(); got != "access" {
		t.Errorf("TokenFromEnv() = %q, want access", got)
	}
	t.Setenv(EnvAccessToken, "")
	if got := TokenFromEnv(); got != "api" {
		t.Errorf("TokenFromEnv() with only FLY_API_TOKEN = %q, want api", got)
	}
	t.Setenv(EnvAPIToken, "")
	if got := TokenFromEnv(); got != "" {
		t.Errorf("TokenFromEnv() with neither = %q, want empty", got)
	}
}
