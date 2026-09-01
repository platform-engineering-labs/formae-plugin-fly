// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apps

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceTypeSecretKey is the FLY::Apps::SecretKey resource type.
const ResourceTypeSecretKey = "FLY::Apps::SecretKey"

func init() {
	registry.Register(
		ResourceTypeSecretKey,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &SecretKey{Client: c, Target: cfg}
		},
	)
}

// SecretKey — FLY::Apps::SecretKey.
//
//	POST   /v1/apps/{app}/secretkeys/{name}           Create or update with given material
//	POST   /v1/apps/{app}/secretkeys/{name}/generate  Create with generated material
//	GET    /v1/apps/{app}/secretkeys[/{name}]         Read / List
//	DELETE /v1/apps/{app}/secretkeys/{name}           Delete
//
// Native id is "{app}/{name}".
//
// This is NOT an environment-variable secret — that is FLY::Apps::Secrets. A
// secret key is app-scoped KMS key material backing the encrypt/decrypt/sign/
// verify endpoints. Those operations are runtime verbs and are not modelled.
//
// Key material is write-only and optional: omit `value` and Fly generates the
// key, which is the safer default and what most callers want. When supplied,
// `value` is base64 in the forma and sent as the byte array the API expects.
type SecretKey struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// SecretKeyProperties is the forma-facing shape.
type SecretKeyProperties struct {
	AppName string `json:"appName,omitempty"`
	Name    string `json:"name,omitempty"`
	KeyType string `json:"keyType,omitempty"`
	// Value is base64-encoded key material. Write-only; the API never returns
	// it. Omit to have Fly generate the key.
	Value string `json:"value,omitempty"`

	// Outputs.
	PublicKey string `json:"publicKey,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

type secretKeyAPI struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	PublicKey []byte `json:"public_key"`
	CreatedAt string `json:"created_at"`
}

func (a secretKeyAPI) toProps(app string) SecretKeyProperties {
	p := SecretKeyProperties{
		AppName:   app,
		Name:      a.Name,
		KeyType:   a.Type,
		CreatedAt: a.CreatedAt,
	}
	if len(a.PublicKey) > 0 {
		p.PublicKey = base64.StdEncoding.EncodeToString(a.PublicKey)
	}
	return p
}

// write performs the create-or-update call, choosing the generate endpoint when
// no material was supplied.
func (s *SecretKey) write(ctx context.Context, p SecretKeyProperties) error {
	path := "/v1/apps/" + p.AppName + "/secretkeys/" + p.Name
	body := map[string]any{}
	if p.KeyType != "" {
		body["type"] = p.KeyType
	}
	if p.Value == "" {
		// No material supplied: let Fly generate it. Safer default than asking
		// a user to paste key bytes into a forma.
		return s.Client.Do(ctx, flytransport.Request{
			Method: "POST", Path: path + "/generate", Body: body,
		}, nil)
	}
	raw, err := base64.StdEncoding.DecodeString(p.Value)
	if err != nil {
		return err
	}
	// The API wants a JSON array of byte values. Go marshals []byte as a base64
	// string, so the bytes are widened to []int to get the array form.
	ints := make([]int, len(raw))
	for i, b := range raw {
		ints[i] = int(b)
	}
	body["value"] = ints
	return s.Client.Do(ctx, flytransport.Request{Method: "POST", Path: path, Body: body}, nil)
}

func (s *SecretKey) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p SecretKeyProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.AppName == "" || p.Name == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"appName and name are required"), nil
	}
	if err := s.write(ctx, p); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	// Key material is write-only, so it is dropped from the stored properties.
	stored := p
	stored.Value = ""
	return prov.SuccessCreate(prov.JoinTwoPart(p.AppName, p.Name), stored), nil
}

func (s *SecretKey) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	app, name, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	var api secretKeyAPI
	if err := s.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + app + "/secretkeys/" + name,
	}, &api); err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	if api.Name == "" {
		api.Name = name
	}
	return prov.OKRead(req.ResourceType, api.toProps(app)), nil
}

// Update rewrites the key. Note this rotates the material when `value` is
// omitted, because the generate endpoint is the only create-or-update path
// available without supplying bytes.
func (s *SecretKey) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	app, name, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	var desired SecretKeyProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	desired.AppName, desired.Name = app, name
	if err := s.write(ctx, desired); err != nil {
		return prov.FailUpdate(flytransport.ClassifyError(err), err.Error()), nil
	}
	stored := desired
	stored.Value = ""
	return prov.SuccessUpdate(req.NativeID, stored), nil
}

func (s *SecretKey) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	app, name, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if err := s.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/apps/" + app + "/secretkeys/" + name,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

func (s *SecretKey) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

// List fans out over the org's apps: secret keys have no org-wide endpoint.
func (s *SecretKey) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	apps, err := listAppNames(ctx, s.Client, s.Target)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, app := range apps {
		var resp struct {
			SecretKeys []secretKeyAPI `json:"secret_keys"`
		}
		if err := s.Client.Do(ctx, flytransport.Request{
			Method: "GET", Path: "/v1/apps/" + app + "/secretkeys",
		}, &resp); err != nil {
			continue
		}
		for _, k := range resp.SecretKeys {
			if k.Name == "" {
				continue
			}
			ids = append(ids, prov.JoinTwoPart(app, k.Name))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
