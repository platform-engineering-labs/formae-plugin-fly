// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
)

type call struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

type route struct {
	status int
	body   string
}

// stub is a fake Managed Postgres API. Unmatched routes fail the test loudly
// rather than returning a plausible zero value.
type stub struct {
	t      *testing.T
	routes map[string]route
	calls  []call
	srv    *httptest.Server

	// handler gets first refusal, for tests where the same route must answer
	// differently on successive calls (create-then-list diffing).
	handler func(method, path string) (status int, body string, handled bool)
}

func newStub(t *testing.T, routes map[string]route) *stub {
	t.Helper()
	s := &stub{t: t, routes: routes}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &c.Body)
		}
		s.calls = append(s.calls, c)

		if s.handler != nil {
			if status, body, handled := s.handler(r.Method, r.URL.Path); handled {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
				return
			}
		}
		key := r.Method + " " + r.URL.Path
		rt, ok := s.routes[key]
		if !ok {
			s.t.Errorf("stub: unexpected request %s", key)
			w.WriteHeader(599)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rt.status)
		_, _ = w.Write([]byte(rt.body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stub) client() *flytransport.Client {
	s.t.Helper()
	c, err := flytransport.NewClient(flytransport.Config{Token: "t", BaseURL: s.srv.URL})
	if err != nil {
		s.t.Fatalf("NewClient: %v", err)
	}
	return c
}

func (s *stub) target() *registry.TargetConfig {
	return &registry.TargetConfig{Org: "test-org", Region: "fra"}
}

func (s *stub) only() call {
	s.t.Helper()
	if len(s.calls) != 1 {
		s.t.Fatalf("want 1 request, got %d: %+v", len(s.calls), s.calls)
	}
	return s.calls[0]
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func decodeProps(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("decode properties %q: %v", s, err)
	}
	return m
}
