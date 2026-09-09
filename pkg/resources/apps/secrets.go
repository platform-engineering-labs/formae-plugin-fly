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
// The bag is a first-class secret (formae.Secret): Read asks the list endpoint
// for the values with ?show_secrets=true and reports them on decodedValues, so
// a consumer can reference one as `bag.res.secretValue.at("KEY")`. The agent
// re-reads on every plugin call, so a secret rotated out of band — including
// the DATABASE_URL a Postgres attachment injects, which formae never wrote —
// resolves to its current value without an apply.
//
// Reveal is scoped to Read on purpose. List walks every app in the org, and
// revealing there would pull the whole org's plaintext through the agent for
// resources nobody asked to manage. A token allowed to list secrets but not to
// reveal them still reads the bag, just without the values.
//
// The authored `values` field stays write-only: it is never echoed back, so
// value drift is still not detected — only an added or removed name.
type Secrets struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// SecretsProperties is the forma-facing shape.
type SecretsProperties struct {
	AppName string            `json:"appName,omitempty"`
	Values  map[string]string `json:"values,omitempty"`

	// DecodedValues is the read side of the bag, populated by Read alone. It
	// is the resource's secret value property, so `bag.res.secretValue.at(k)`
	// resolves against it. Kept separate from Values so an authored bag and a
	// read one never look alike: Values is what the author wrote and is never
	// echoed back, which is what keeps values out of drift detection.
	DecodedValues map[string]string `json:"decodedValues,omitempty"`
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
	secrets, err := s.list(ctx, app, true)
	if err != nil && flytransport.ClassifyError(err) == resource.OperationErrorCodeAccessDenied {
		// The token may list secrets but not reveal them. Read the names alone
		// rather than failing: everything except the secretValue accessor keeps
		// working, and a plugin that refused to read at all would break sync
		// for every read-only token that works today.
		secrets, err = s.list(ctx, app, false)
	}
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	// The endpoint stays 200 with an empty list after the last secret is
	// deleted out of band. The bag exists only while it holds at least one
	// secret; otherwise report NotFound so formae clears it from inventory.
	if len(secrets) == 0 {
		return prov.NotFoundRead(req.ResourceType), nil
	}
	props := SecretsProperties{AppName: app}
	for _, sec := range secrets {
		if sec.Value == "" {
			continue
		}
		if props.DecodedValues == nil {
			props.DecodedValues = make(map[string]string, len(secrets))
		}
		props.DecodedValues[sec.Name] = sec.Value
	}
	return prov.OKRead(req.ResourceType, props), nil
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

// appSecret is one entry of the bag. Value is populated only when the request
// asked to reveal.
type appSecret struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// list reads the app's bag. reveal asks Fly for the values as well as the
// names; only Read passes true — see the type comment.
func (s *Secrets) list(ctx context.Context, app string, reveal bool) ([]appSecret, error) {
	req := flytransport.Request{Method: "GET", Path: "/v1/apps/" + app + "/secrets"}
	if reveal {
		req.Query = map[string]string{"show_secrets": "true"}
	}
	var resp struct {
		Secrets []appSecret `json:"secrets"`
	}
	if err := s.Client.Do(ctx, req, &resp); err != nil {
		return nil, err
	}
	out := make([]appSecret, 0, len(resp.Secrets))
	for _, sec := range resp.Secrets {
		if sec.Name != "" {
			out = append(out, sec)
		}
	}
	return out, nil
}

// names lists the secret names on an app, without revealing anything.
func (s *Secrets) names(ctx context.Context, app string) ([]string, error) {
	secrets, err := s.list(ctx, app, false)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(secrets))
	for _, sec := range secrets {
		names = append(names, sec.Name)
	}
	return names, nil
}
