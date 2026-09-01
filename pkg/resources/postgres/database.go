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

// ResourceTypeDatabase is the FLY::Postgres::Database resource type.
const ResourceTypeDatabase = "FLY::Postgres::Database"

func init() {
	registry.Register(
		ResourceTypeDatabase,
		// A database has exactly one field — its name — and renaming is not an
		// operation the API offers, so there is nothing to update.
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Database{Client: c, Target: cfg}
		},
	)
}

// Database — FLY::Postgres::Database.
//
//	POST   /v1/postgres/{cluster}/databases          Create
//	GET    /v1/postgres/{cluster}/databases          List (no per-database GET)
//	DELETE /v1/postgres/{cluster}/databases/{name}   Delete
//
// Native id is "{cluster_id}/{database_name}".
type Database struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// DatabaseProperties is the forma-facing shape.
type DatabaseProperties struct {
	ClusterID string `json:"clusterId,omitempty"`
	Name      string `json:"name,omitempty"`
}

func (d *Database) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p DatabaseProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.ClusterID == "" || p.Name == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"clusterId and name are required"), nil
	}
	if err := d.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/postgres/" + p.ClusterID + "/databases",
		Body: map[string]any{"name": p.Name},
	}, nil); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessCreate(prov.JoinTwoPart(p.ClusterID, p.Name)), nil
}

// Read scans the cluster's database list: there is no per-database GET.
func (d *Database) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	cluster, name, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	names, err := databaseNames(ctx, d.Client, cluster)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	for _, n := range names {
		if n == name {
			return prov.OKRead(req.ResourceType, DatabaseProperties{ClusterID: cluster, Name: name}), nil
		}
	}
	return prov.NotFoundRead(req.ResourceType), nil
}

func (d *Database) Update(_ context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Postgres::Database has no update path; the name is its identity"), nil
}

func (d *Database) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	cluster, name, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if err := d.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/postgres/" + cluster + "/databases/" + name,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

func (d *Database) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

// List walks the org's clusters — databases have no org-wide endpoint.
func (d *Database) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	if d.Target == nil || d.Target.Org == "" {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	clusters, err := ClusterIDs(ctx, d.Client, d.Target.Org)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, cluster := range clusters {
		names, err := databaseNames(ctx, d.Client, cluster)
		if err != nil {
			continue
		}
		for _, n := range names {
			ids = append(ids, prov.JoinTwoPart(cluster, n))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func databaseNames(ctx context.Context, client *flytransport.Client, cluster string) ([]string, error) {
	var resp struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/postgres/" + cluster + "/databases",
	}, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Data))
	for _, db := range resp.Data {
		if db.Name != "" {
			names = append(names, db.Name)
		}
	}
	return names, nil
}
