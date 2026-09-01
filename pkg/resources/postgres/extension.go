// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package postgres

import (
	"context"
	"encoding/json"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceTypeExtension is the FLY::Postgres::Extension resource type.
const ResourceTypeExtension = "FLY::Postgres::Extension"

func init() {
	registry.Register(
		ResourceTypeExtension,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Extension{Client: c, Target: cfg}
		},
	)
}

// Extension — FLY::Postgres::Extension.
//
//	POST   /v1/postgres/{cluster}/databases/{db}/extensions        Enable
//	GET    /v1/postgres/{cluster}/databases/{db}/extensions        List
//	DELETE /v1/postgres/{cluster}/databases/{db}/extensions/{name} Disable
//
// Native id is "{cluster_id}/{database_name}/{extension_name}".
//
// The list endpoint returns every extension the cluster *offers*, installed or
// not, each with an `installed` object that is null when it is not enabled.
// "Exists" for this resource therefore means `installed != null`, not
// "appears in the list" — every extension appears in the list.
type Extension struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// ExtensionProperties is the forma-facing shape.
type ExtensionProperties struct {
	ClusterID    string `json:"clusterId,omitempty"`
	DatabaseName string `json:"databaseName,omitempty"`
	Name         string `json:"name,omitempty"`
	Schema       string `json:"schema,omitempty"`
	CreateSchema *bool  `json:"createSchema,omitempty"`

	// Outputs.
	Version string `json:"version,omitempty"`
	System  *bool  `json:"system,omitempty"`
}

func (e *Extension) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p ExtensionProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.ClusterID == "" || p.DatabaseName == "" || p.Name == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"clusterId, databaseName and name are required"), nil
	}
	body := map[string]any{"name": p.Name}
	if p.Schema != "" {
		body["schema"] = p.Schema
	}
	if p.CreateSchema != nil {
		body["create_schema"] = *p.CreateSchema
	}
	if err := e.Client.Do(ctx, flytransport.Request{
		Method: "POST",
		Path:   "/v1/postgres/" + p.ClusterID + "/databases/" + p.DatabaseName + "/extensions",
		Body:   body,
	}, nil); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessCreate(prov.JoinThreePart(p.ClusterID, p.DatabaseName, p.Name)), nil
}

func (e *Extension) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	cluster, db, name, err := prov.ParseThreePart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	exts, err := listExtensions(ctx, e.Client, cluster, db)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	for _, ext := range exts {
		if ext.Name != name {
			continue
		}
		// Present in the catalogue but not installed means the resource does
		// not exist, even though the API happily returned a row for it.
		if ext.Installed == nil {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.OKRead(req.ResourceType, ExtensionProperties{
			ClusterID:    cluster,
			DatabaseName: db,
			Name:         ext.Name,
			Schema:       ext.Installed.Schema,
			Version:      ext.Installed.Version,
			System:       ext.System,
		}), nil
	}
	return prov.NotFoundRead(req.ResourceType), nil
}

func (e *Extension) Update(_ context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Postgres::Extension has no update path; enable and disable are the only operations"), nil
}

func (e *Extension) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	cluster, db, name, err := prov.ParseThreePart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if err := e.Client.Do(ctx, flytransport.Request{
		Method: "DELETE",
		Path:   "/v1/postgres/" + cluster + "/databases/" + db + "/extensions/" + name,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

func (e *Extension) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

// List reports only the extensions that are actually installed. Reporting the
// whole catalogue would hand discovery hundreds of phantom resources per
// database.
func (e *Extension) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	if e.Target == nil || e.Target.Org == "" {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	clusters, err := ClusterIDs(ctx, e.Client, e.Target.Org)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, cluster := range clusters {
		dbs, err := databaseNames(ctx, e.Client, cluster)
		if err != nil {
			continue
		}
		for _, db := range dbs {
			exts, err := listExtensions(ctx, e.Client, cluster, db)
			if err != nil {
				continue
			}
			for _, ext := range exts {
				if ext.Name == "" || ext.Installed == nil {
					continue
				}
				ids = append(ids, prov.JoinThreePart(cluster, db, ext.Name))
			}
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

type extensionAPI struct {
	Name      string `json:"name"`
	System    *bool  `json:"system"`
	Installed *struct {
		Schema  string `json:"schema"`
		Version string `json:"version"`
	} `json:"installed"`
}

func listExtensions(ctx context.Context, client *flytransport.Client, cluster, db string) ([]extensionAPI, error) {
	var resp struct {
		Data []extensionAPI `json:"data"`
	}
	if err := client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/postgres/" + cluster + "/databases/" + db + "/extensions",
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}
