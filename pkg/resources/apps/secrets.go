// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apps

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceTypeSecrets is the FLY::Apps::Secrets resource type.
const ResourceTypeSecrets = "FLY::Apps::Secrets"

// secretsNativeIDChild is the constant second segment of the bag's native id.
//
// The id is "{app}/secrets" rather than the bare app name because formae keys
// inventory by (target, nativeID) regardless of resource type: a bare app name
// would make the bag a duplicate of its own App resource, and one of the two
// would get deleted.
const secretsNativeIDChild = "secrets"

func init() {
	registry.Register(
		ResourceTypeSecrets,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Secrets{Client: c, Target: cfg}
		},
	)
}

// Secrets — FLY::Apps::Secrets.
//
// API mapping:
//
//	POST   /v1/apps/{app}/secrets              Bulk set  {"values": {...}}
//	GET    /v1/apps/{app}/secrets              List names + digests
//	DELETE /v1/apps/{app}/secrets/{name}       Delete one
//
// The endpoint is a bulk bag per app, so the whole bag is one resource: every
// mutation becomes a single atomic call, which avoids the lost-update race that
// per-secret resources hit when applied concurrently (each concurrent
// single-item write is a read-modify-write on the shared bag, last writer wins).
//
// Values are write-only. The list endpoint can reveal them via
// ?show_secrets=true and this plugin deliberately never asks: pulling secret
// values into formae's state to detect drift on them would be a worse trade
// than not detecting it. Added and removed *names* are still detected.
type Secrets struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// SecretsProperties is the forma-facing shape.
type SecretsProperties struct {
	AppName string            `json:"appName,omitempty"`
	Values  map[string]string `json:"values,omitempty"`
}

// appFromSecretsNativeID extracts the app name from "{app}/secrets", tolerating
// a bare app name for forward/backward compatibility.
func appFromSecretsNativeID(nativeID string) string {
	if app, _, err := prov.ParseTwoPart(nativeID); err == nil {
		return app
	}
	return nativeID
}

func (s *Secrets) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p SecretsProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.AppName == "" || len(p.Values) == 0 {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"appName and at least one value are required"), nil
	}
	if err := s.bulkSet(ctx, p.AppName, p.Values); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	// Values are write-only, so the stored properties carry only the app name —
	// the same shape Read reports.
	return prov.SuccessCreate(prov.JoinTwoPart(p.AppName, secretsNativeIDChild),
		SecretsProperties{AppName: p.AppName}), nil
}

func (s *Secrets) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	app := appFromSecretsNativeID(req.NativeID)
	if app == "" {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	names, err := s.names(ctx, app)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	// The endpoint stays 200 with an empty list after the last secret is
	// deleted out of band. The bag exists only while it holds at least one
	// secret; otherwise report NotFound so formae clears it from inventory.
	if len(names) == 0 {
		return prov.NotFoundRead(req.ResourceType), nil
	}
	// Values are write-only, so only the app name is reported. Formae does not
	// diff write-only fields.
	return prov.OKRead(req.ResourceType, SecretsProperties{AppName: app}), nil
}

func (s *Secrets) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	app := appFromSecretsNativeID(req.NativeID)
	if app == "" {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, "native id required"), nil
	}
	var prior, desired SecretsProperties
	if len(req.PriorProperties) > 0 {
		if err := json.Unmarshal(req.PriorProperties, &prior); err != nil {
			return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
		}
	}
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}

	// Names present before but absent from the desired bag are removed. The
	// prior *values* are write-only and may well be absent from state, but the
	// prior *names* are what matter here.
	removed := make([]string, 0)
	for name := range prior.Values {
		if _, keep := desired.Values[name]; !keep {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	// Fly has no bulk secret delete, only DELETE .../secrets/{name}.
	for _, name := range removed {
		if err := s.Client.Do(ctx, flytransport.Request{
			Method: "DELETE", Path: "/v1/apps/" + app + "/secrets/" + name,
		}, nil); err != nil && !flytransport.IsNotFound(err) {
			return prov.FailUpdate(flytransport.ClassifyError(err), err.Error()), nil
		}
	}
	if len(desired.Values) > 0 {
		if err := s.bulkSet(ctx, app, desired.Values); err != nil {
			return prov.FailUpdate(flytransport.ClassifyError(err), err.Error()), nil
		}
	}
	// A running machine keeps the environment it booted with: the new value
	// only reaches it on restart. The plugin does not restart machines, because
	// a service interruption should not be an invisible side effect of a secret
	// update.
	return prov.SuccessUpdate(req.NativeID, SecretsProperties{AppName: app}), nil
}

func (s *Secrets) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	app := appFromSecretsNativeID(req.NativeID)
	if app == "" {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, "native id required"), nil
	}
	names, err := s.names(ctx, app)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.SuccessDelete(req.NativeID), nil
		}
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	sort.Strings(names)
	for _, name := range names {
		if err := s.Client.Do(ctx, flytransport.Request{
			Method: "DELETE", Path: "/v1/apps/" + app + "/secrets/" + name,
		}, nil); err != nil && !flytransport.IsNotFound(err) {
			return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
		}
	}
	return prov.SuccessDelete(req.NativeID), nil
}

// Status reports Success: secret writes are synchronous.
func (s *Secrets) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

// List fans out: there is no org-wide secrets endpoint, so this lists the org's
// apps and then asks each one for its secrets. On a large org that is slow, and
// it is the first thing to revisit if discovery times out.
func (s *Secrets) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	apps, err := listAppNames(ctx, s.Client, s.Target)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, app := range apps {
		names, err := s.names(ctx, app)
		if err != nil || len(names) == 0 {
			continue
		}
		ids = append(ids, prov.JoinTwoPart(app, secretsNativeIDChild))
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

// bulkSet writes the whole bag in one call. AppSecretsUpdateRequest is
// {"values": {name: value}} — a map, not the list-of-objects shape some other
// providers' secret endpoints take.
func (s *Secrets) bulkSet(ctx context.Context, app string, values map[string]string) error {
	return s.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/apps/" + app + "/secrets",
		Body: map[string]any{"values": values},
	}, nil)
}

// names lists the secret names on an app. show_secrets is deliberately not
// requested — see the type comment.
func (s *Secrets) names(ctx context.Context, app string) ([]string, error) {
	var resp struct {
		Secrets []struct {
			Name string `json:"name"`
		} `json:"secrets"`
	}
	if err := s.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + app + "/secrets",
	}, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Secrets))
	for _, sec := range resp.Secrets {
		if sec.Name != "" {
			names = append(names, sec.Name)
		}
	}
	return names, nil
}
