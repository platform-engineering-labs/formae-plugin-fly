// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package apps implements every FLY::Apps::* resource. Each file registers one
// resource type with the registry via init(); pkg/../fly.go dispatches by type.
package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceTypeApp is the FLY::Apps::App resource type.
const ResourceTypeApp = "FLY::Apps::App"

// orgAliasPersonal is the write-only alias for a user's personal organization.
// Fly accepts it on create and reports the real slug on read.
const orgAliasPersonal = "personal"

func init() {
	registry.Register(
		ResourceTypeApp,
		// No OperationUpdate: the Machines API has no app-update endpoint, so
		// every App field is createOnly and a change is a replacement.
		// Registering Update would only let formae plan something the plugin
		// then has to refuse.
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &App{Client: c, Target: cfg}
		},
	)
}

// App — FLY::Apps::App.
//
// API mapping:
//
//	POST   /v1/apps                      Create (sync, 201)
//	GET    /v1/apps/{app_name}           Read
//	DELETE /v1/apps/{app_name}           Delete
//	GET    /v1/apps?org_slug={org}       List
//
// An app is a namespace: nothing bills until a Machine runs inside it, and
// there is nothing to converge, so create and delete are both synchronous.
type App struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// AppProperties is the forma-facing shape (matches the Pkl field names).
type AppProperties struct {
	Name             string `json:"name,omitempty"`
	Org              string `json:"org,omitempty"`
	Network          string `json:"network,omitempty"`
	EnableSubdomains *bool  `json:"enableSubdomains,omitempty"`

	// Outputs.
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
}

// appAPI is the Fly-API-facing shape. Only the fields we surface are declared:
// machine_count and volume_count change with the app's contents and would show
// up as drift on a field nobody declared.
type appAPI struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Status       string `json:"status,omitempty"`
	Network      string `json:"network,omitempty"`
	Organization struct {
		Slug string `json:"slug,omitempty"`
	} `json:"organization,omitempty"`
}

func (a appAPI) toProps() AppProperties {
	return AppProperties{
		Name:    a.Name,
		Org:     a.Organization.Slug,
		Network: a.Network,
		ID:      a.ID,
		Status:  a.Status,
	}
}

func (a *App) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p AppProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.Name == "" || p.Org == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"name and org are required"), nil
	}
	// "personal" is an alias Fly accepts on the way in and never gives back:
	// POST /v1/apps?org_slug=personal succeeds, but GET /v1/apps/{name} reports
	// the organization's real slug. `org` is createOnly, so formae would compare
	// the desired "personal" against the actual slug, see drift on an immutable
	// field, and plan a replacement on every single reconcile — forever.
	//
	// Refuse it up front with the real slug in the message, rather than letting
	// the first apply succeed and every apply after it churn.
	if strings.EqualFold(p.Org, orgAliasPersonal) {
		hint := a.resolveOrgSlug(ctx)
		msg := `org = "personal" is an alias Fly does not echo back, which would make every reconcile replace the app; use the organization's real slug`
		if hint != "" {
			msg += ` (yours is "` + hint + `")`
		}
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, msg), nil
	}
	// CreateAppRequest's field is "name", not "app_name" — the older GraphQL
	// mutation and some community providers use app_name and it is silently
	// ignored here.
	body := map[string]any{"name": p.Name, "org_slug": p.Org}
	if p.Network != "" {
		body["network"] = p.Network
	}
	if p.EnableSubdomains != nil {
		body["enable_subdomains"] = *p.EnableSubdomains
	}
	// The 201 body does not echo the app's name or org, so the native id is the
	// name we sent. That is safe because app names are caller-chosen and
	// globally unique: a collision fails with 422 rather than binding to
	// someone else's app.
	//
	// Note the OpenAPI spec is wrong here. It declares CreateAppResponse{token};
	// the live API returns {"id": "...", "created_at": ...}. Observed against
	// api.machines.dev, 2026-09-01. Nothing here reads the body, so the
	// discrepancy is harmless — but do not trust the spec on this endpoint.
	if err := a.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/apps", Body: body,
	}, nil); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	// Read back rather than echoing the request. Formae stores exactly what
	// Create returns and resolves `app.res.id` against it, and `id` only exists
	// server-side. One extra GET buys a working resolvable; if it fails the
	// create still succeeded, so fall back to what we know.
	if api, err := a.get(ctx, p.Name); err == nil {
		return prov.SuccessCreate(p.Name, api.toProps()), nil
	}
	return prov.SuccessCreate(p.Name, p), nil
}

func (a *App) get(ctx context.Context, name string) (appAPI, error) {
	var apiResp appAPI
	err := a.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + name,
	}, &apiResp)
	return apiResp, err
}

// resolveOrgSlug best-effort looks up the token's own organization slug, so the
// "personal" rejection can name the value the user should have written. Returns
// "" if the lookup fails — a better error message is not worth failing over.
func (a *App) resolveOrgSlug(ctx context.Context) string {
	var resp struct {
		Tokens []struct {
			OrgSlug string `json:"org_slug"`
		} `json:"tokens"`
	}
	if err := a.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/tokens/current",
	}, &resp); err != nil {
		return ""
	}
	if len(resp.Tokens) == 0 {
		return ""
	}
	return resp.Tokens[0].OrgSlug
}

func (a *App) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	if req.NativeID == "" {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	var apiResp appAPI
	if err := a.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + req.NativeID,
	}, &apiResp); err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	return prov.OKRead(req.ResourceType, apiResp.toProps()), nil
}

// Update always refuses: there is no app-update endpoint. The schema marks
// every field createOnly so formae plans a replacement instead; this is the
// backstop for a plan that somehow reaches here.
func (a *App) Update(_ context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Apps::App has no update path; every field is createOnly and a change replaces the app"), nil
}

func (a *App) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	if req.NativeID == "" {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, "native id required"), nil
	}
	// Deleting an app cascades: machines, volumes, secrets, certificates and IP
	// assignments all go with it.
	if err := a.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/apps/" + req.NativeID,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

// Status reports Success: app create and delete are synchronous, so an
// operation that got this far has already settled.
func (a *App) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

func (a *App) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	names, err := a.listNames(ctx)
	if err != nil {
		// Discovery runs across every target; one unreadable org must not fail
		// the whole sync.
		return &resource.ListResult{NativeIDs: []string{}}, nil
	}
	return &resource.ListResult{NativeIDs: names}, nil
}

// listNames enumerates the app names in the target's org. Shared with the
// resources that have no org-wide endpoint of their own and must fan out.
func (a *App) listNames(ctx context.Context) ([]string, error) {
	return listAppNames(ctx, a.Client, a.Target)
}

// listAppNames is the package-level helper: GET /v1/apps?org_slug={org}.
//
// org_slug is a required query parameter — without it the API answers 404, not
// a full listing — so a target with no org is an error rather than a wildcard.
func listAppNames(ctx context.Context, client *flytransport.Client, cfg *registry.TargetConfig) ([]string, error) {
	if cfg == nil || cfg.Org == "" {
		return nil, fmt.Errorf("target config requires org to list apps")
	}
	var resp struct {
		Apps []appAPI `json:"apps"`
	}
	if err := client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps",
		Query: map[string]string{"org_slug": cfg.Org},
	}, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Apps))
	for _, app := range resp.Apps {
		if app.Name == "" {
			continue
		}
		names = append(names, app.Name)
	}
	return names, nil
}
