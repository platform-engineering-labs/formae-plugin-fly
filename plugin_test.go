// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Every type in schema/pkl/core/fly.pkl must have a provisioner behind it. A
// schema type with no registration is a resource formae will plan and the plugin
// will then refuse.
func TestEveryDeclaredTypeIsRegistered(t *testing.T) {
	want := []string{
		"FLY::Apps::App",
		"FLY::Apps::Certificate",
		"FLY::Apps::IPAddress",
		"FLY::Apps::Machine",
		"FLY::Apps::Secrets",
		"FLY::Apps::Volume",
	}
	got := registry.ResourceTypes()
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("registered = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("registered[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Registering an operation the provisioner refuses is worse than not
// registering it: formae plans it and the apply fails. App, Certificate and
// IPAddress have no update path on the API.
func TestTypesWithoutUpdatePathDoNotRegisterUpdate(t *testing.T) {
	for _, rt := range []string{"FLY::Apps::App", "FLY::Apps::Certificate", "FLY::Apps::IPAddress"} {
		for _, op := range registry.GetOperations(rt) {
			if op == resource.OperationUpdate {
				t.Errorf("%s registers Update but the API has no update endpoint", rt)
			}
		}
	}
	for _, rt := range []string{"FLY::Apps::Machine", "FLY::Apps::Volume", "FLY::Apps::Secrets"} {
		found := false
		for _, op := range registry.GetOperations(rt) {
			if op == resource.OperationUpdate {
				found = true
			}
		}
		if !found {
			t.Errorf("%s should register Update", rt)
		}
	}
}

func TestUnknownResourceTypeIsAHardError(t *testing.T) {
	t.Setenv(flytransport.EnvAccessToken, "tok")
	p := &Plugin{}
	_, err := p.Create(context.Background(), &resource.CreateRequest{ResourceType: "FLY::Nope::Nope"})
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("err = %v, want ErrNotImplemented so a bad type stops the reconcile loop", err)
	}
}

// A missing token is a fixable misconfiguration: report it on the result, not as
// a Go error that formae cannot retry past.
func TestMissingTokenReportsInvalidCredentialsOnTheResult(t *testing.T) {
	t.Setenv(flytransport.EnvAccessToken, "")
	t.Setenv(flytransport.EnvAPIToken, "")
	p := &Plugin{}
	res, err := p.Create(context.Background(), &resource.CreateRequest{
		ResourceType: "FLY::Apps::App",
		TargetConfig: json.RawMessage(`{"Org":"my-org"}`),
	})
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeInvalidCredentials {
		t.Errorf("ErrorCode = %q", res.ProgressResult.ErrorCode)
	}
}

// Discovery walks every type across every target; an unhandled type or a target
// with no credentials must yield nothing rather than fail the sync.
func TestListNeverHardFails(t *testing.T) {
	t.Setenv(flytransport.EnvAccessToken, "")
	t.Setenv(flytransport.EnvAPIToken, "")
	p := &Plugin{}
	for _, rt := range []string{"FLY::Nope::Nope", "FLY::Apps::App"} {
		res, err := p.List(context.Background(), &resource.ListRequest{ResourceType: rt})
		if err != nil {
			t.Errorf("List(%s) err = %v", rt, err)
		}
		if len(res.NativeIDs) != 0 {
			t.Errorf("List(%s) = %v", rt, res.NativeIDs)
		}
	}
}

func TestParseTargetConfig(t *testing.T) {
	cfg, err := parseTargetConfig(json.RawMessage(`{"Org":"my-org","Region":"fra","BaseUrl":"http://x"}`))
	if err != nil {
		t.Fatalf("parseTargetConfig: %v", err)
	}
	if cfg.Org != "my-org" || cfg.Region != "fra" || cfg.BaseURL != "http://x" {
		t.Errorf("cfg = %+v", cfg)
	}
	// An absent target config is legitimate — not every operation needs one.
	if cfg, err := parseTargetConfig(nil); err != nil || cfg.Org != "" {
		t.Errorf("empty config: cfg = %+v, err = %v", cfg, err)
	}
	if _, err := parseTargetConfig(json.RawMessage(`not json`)); err == nil {
		t.Error("malformed target config should error")
	}
}

// The client is cached across calls (it holds no target state) but must be
// rebuilt when the target points at a different endpoint.
func TestClientIsCachedPerBaseURL(t *testing.T) {
	t.Setenv(flytransport.EnvAccessToken, "tok")
	p := &Plugin{}
	c1, _, err := p.getDeps(json.RawMessage(`{"Org":"a"}`))
	if err != nil {
		t.Fatalf("getDeps: %v", err)
	}
	c2, cfg2, err := p.getDeps(json.RawMessage(`{"Org":"b"}`))
	if err != nil {
		t.Fatalf("getDeps: %v", err)
	}
	if c1 != c2 {
		t.Error("client was rebuilt for the same base URL")
	}
	// Target config must NOT be cached: a stale org makes discovery list the
	// wrong organization's apps.
	if cfg2.Org != "b" {
		t.Errorf("target org = %q, want the current request's b", cfg2.Org)
	}
	c3, _, err := p.getDeps(json.RawMessage(`{"Org":"b","BaseUrl":"http://other"}`))
	if err != nil {
		t.Fatalf("getDeps: %v", err)
	}
	if c3 == c2 {
		t.Error("client was reused for a different base URL")
	}
}

func TestRateLimitIsOnePerSecond(t *testing.T) {
	p := &Plugin{}
	rl := p.RateLimit()
	if rl.Scope != model.RateLimitScopeNamespace {
		t.Errorf("Scope = %v", rl.Scope)
	}
	// Fly documents 1 req/s per action; formae's knob is per-namespace.
	if rl.MaxRequestsPerSecondForNamespace != 1 {
		t.Errorf("MaxRequestsPerSecondForNamespace = %v, want 1", rl.MaxRequestsPerSecondForNamespace)
	}
}

func TestLabelConfigCoversTypesWithoutAName(t *testing.T) {
	p := &Plugin{}
	lc := p.LabelConfig()
	if lc.DefaultQuery != "$.name" {
		t.Errorf("DefaultQuery = %q", lc.DefaultQuery)
	}
	for rt, want := range map[string]string{
		"FLY::Apps::Certificate": "$.hostname",
		"FLY::Apps::IPAddress":   "$.ip",
		"FLY::Apps::Secrets":     "$.appName",
	} {
		if lc.ResourceOverrides[rt] != want {
			t.Errorf("override[%s] = %q, want %q", rt, lc.ResourceOverrides[rt], want)
		}
	}
}
