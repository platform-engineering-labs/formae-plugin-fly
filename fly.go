// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	// Side-effect imports: every resource registers itself via init() at
	// package load.
	_ "github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/apps"
	_ "github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/postgres"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ErrNotImplemented is returned for resource types this plugin does not handle.
// It is the one dispatch error surfaced as a hard error, so an invalid resource
// type stops a reconcile loop instead of silently retrying forever.
var ErrNotImplemented = errors.New("resource type not implemented")

// Plugin implements plugin.ResourcePlugin. Every CRUD method delegates to the
// per-resource Provisioner registered by the side-effect import above, so this
// file stays constant as resources are added.
type Plugin struct {
	mu     sync.Mutex
	client *flytransport.Client
}

var _ plugin.ResourcePlugin = &Plugin{}

// getDeps parses the target config and returns a client bound to it.
//
// Target config is re-parsed on every request rather than cached. The agent
// issues operations against different targets in the same plugin process —
// discovery across two orgs, or a sync following a CRUD test that used a
// different target — and caching the first one seen pins the plugin to a stale
// `org`, which here means listing the wrong organization's apps. The HTTP
// client IS cached, keyed on base URL, because it holds no target state.
func (p *Plugin) getDeps(targetCfg json.RawMessage) (*flytransport.Client, *registry.TargetConfig, error) {
	cfg, err := parseTargetConfig(targetCfg)
	if err != nil {
		return nil, nil, err
	}
	token := flytransport.TokenFromEnv()
	if token == "" {
		return nil, nil, fmt.Errorf("%s or %s must be set",
			flytransport.EnvAccessToken, flytransport.EnvAPIToken)
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = flytransport.DefaultBaseURL
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil || p.client.BaseURL() != baseURL {
		c, err := flytransport.NewClient(flytransport.Config{BaseURL: baseURL, Token: token})
		if err != nil {
			return nil, nil, err
		}
		p.client = c
	}
	return p.client, cfg, nil
}

func parseTargetConfig(data json.RawMessage) (*registry.TargetConfig, error) {
	var cfg registry.TargetConfig
	if len(data) == 0 {
		return &cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid target config: %w", err)
	}
	return &cfg, nil
}

// dispatch resolves the Provisioner for a resource type.
func (p *Plugin) dispatch(resourceType string, targetCfg json.RawMessage) (prov.Provisioner, resource.OperationErrorCode, error) {
	factory, ok := registry.GetFactory(resourceType)
	if !ok {
		return nil, resource.OperationErrorCodeInvalidRequest,
			fmt.Errorf("%w: %q", ErrNotImplemented, resourceType)
	}
	c, t, err := p.getDeps(targetCfg)
	if err != nil {
		return nil, resource.OperationErrorCodeInvalidCredentials, err
	}
	return factory(c, t), "", nil
}

// =============================================================================
// Configuration
// =============================================================================

// RateLimit caps the plugin at 3 requests/second across the namespace.
//
// Fly documents 1 req/s per action with a short-term burst to 3, scoped per
// machine or app id, plus 5 req/s for GET machine (burst 10) and 100 app
// deletions/minute. Formae's limiter has a single per-namespace knob, so it
// cannot express "per action, per object" — every FLY request shares one budget.
//
// This started at 1, reasoning that it was the documented steady-state floor for
// the strictest action. That was too conservative, and CI proved it: machine
// discovery timed out after the harness's 2-minute window, and the agent logged
// "Discovery already running, consider configuring a longer interval" ten times
// in a single run. Discovery walks all 14 resource types, and five of them fan
// out one request per app or per cluster, so a sweep is dozens of serialised
// requests — at 1 req/s that is most of a minute before the machine listing is
// even reached.
//
// 3 is Fly's documented burst, and discovery is almost entirely GETs, which Fly
// limits far more loosely than writes. There were zero 429s anywhere in the run
// that failed, so 1 was not protecting against anything observable. If sustained
// 3 req/s does start drawing 429s, the transport already classifies them as
// Throttling, which formae treats as recoverable and retries — a slower apply
// rather than a failed one.
func (p *Plugin) RateLimit() model.RateLimitConfig {
	return model.RateLimitConfig{
		Scope:                            model.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 3,
	}
}

// DiscoveryFilters returns nil: Fly has no tag or label to opt out on, and
// guessing at name patterns would hide resources a user wanted to import.
func (p *Plugin) DiscoveryFilters() []model.MatchFilter { return nil }

// LabelConfig points each resource type at the field it actually identifies
// itself by.
func (p *Plugin) LabelConfig() model.LabelConfig {
	return model.LabelConfig{
		DefaultQuery: "$.name",
		ResourceOverrides: map[string]string{
			// Certificates and IPs have no name; a Secrets bag is identified by
			// the app it belongs to; the Postgres children and the two snapshot
			// -shaped resources are identified by whichever field is their key.
			"FLY::Apps::Certificate":    "$.hostname",
			"FLY::Apps::IPAddress":      "$.ip",
			"FLY::Apps::Secrets":        "$.appName",
			"FLY::Apps::VolumeSnapshot": "$.id",
			"FLY::Postgres::User":       "$.username",
			"FLY::Postgres::Attachment": "$.appName",
			"FLY::Postgres::Backup":     "$.id",
		},
	}
}

// =============================================================================
// CRUD dispatch
// =============================================================================

func (p *Plugin) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	pr, code, err := p.dispatch(req.ResourceType, req.TargetConfig)
	if err != nil {
		return prov.FailCreate(code, err.Error()), hardErr(err)
	}
	return pr.Create(ctx, req)
}

func (p *Plugin) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	pr, code, err := p.dispatch(req.ResourceType, req.TargetConfig)
	if err != nil {
		return prov.FailRead(req.ResourceType, code), hardErr(err)
	}
	return pr.Read(ctx, req)
}

func (p *Plugin) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	pr, code, err := p.dispatch(req.ResourceType, req.TargetConfig)
	if err != nil {
		return prov.FailUpdate(code, err.Error()), hardErr(err)
	}
	return pr.Update(ctx, req)
}

func (p *Plugin) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	pr, code, err := p.dispatch(req.ResourceType, req.TargetConfig)
	if err != nil {
		return prov.FailDelete(code, err.Error()), hardErr(err)
	}
	return pr.Delete(ctx, req)
}

func (p *Plugin) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	pr, code, err := p.dispatch(req.ResourceType, req.TargetConfig)
	if err != nil {
		return prov.FailStatus(code, err.Error()), hardErr(err)
	}
	return pr.Status(ctx, req)
}

func (p *Plugin) List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error) {
	pr, _, err := p.dispatch(req.ResourceType, req.TargetConfig)
	if err != nil {
		// Discovery walks every registered type across every target; a type this
		// plugin does not handle, or a target with no credentials, must return
		// nothing rather than fail the sync.
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}
	return pr.List(ctx, req)
}

// hardErr surfaces only "resource type not implemented" as a Go error, so an
// unknown type stops a reconcile loop. Credential and target-config problems are
// reported through the result's ErrorCode instead — the SDK does not read a nil
// error as success when the result's OperationStatus is Failure, and a hard
// error there would turn a fixable misconfiguration into an unretryable one.
func hardErr(err error) error {
	if errors.Is(err, ErrNotImplemented) {
		return err
	}
	return nil
}
