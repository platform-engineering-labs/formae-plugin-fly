// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package postgres implements the FLY::Postgres::* resources — Fly.io Managed
// Postgres.
//
// These are NOT app-scoped: a cluster belongs to an organization and lives
// under the top-level /v1/postgres tree, so native ids are cluster ids rather
// than "{app}/{child}". Every response in this tree is wrapped in a {"data":…}
// envelope, unlike the Machines endpoints.
package postgres

import (
	"context"
	"encoding/json"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceTypeCluster is the FLY::Postgres::Cluster resource type.
const ResourceTypeCluster = "FLY::Postgres::Cluster"

// Managed Postgres cluster lifecycle states.
const (
	clusterStatusCreating     = "creating"
	clusterStatusInitializing = "initializing"
	clusterStatusReady        = "ready"
	clusterStatusDeleting     = "deleting"
	clusterStatusDeleted      = "deleted"
	clusterStatusFailed       = "failed"
)

func init() {
	registry.Register(
		ResourceTypeCluster,
		// No OperationUpdate: the API exposes no cluster-update endpoint. Plan,
		// disk size and version are all createOnly.
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Cluster{Client: c, Target: cfg}
		},
	)
}

// Cluster — FLY::Postgres::Cluster.
//
//	POST   /v1/postgres                    Create (async)
//	GET    /v1/postgres/{id}               Read / Status
//	DELETE /v1/postgres/{id}               Delete
//	GET    /v1/postgres?org_slug={org}     List
//
// Native id is the bare cluster id: clusters are org-scoped, not app-scoped.
type Cluster struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// Endpoint is a host/port pair the cluster answers on.
type Endpoint struct {
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
}

// ClusterProperties is the forma-facing shape.
type ClusterProperties struct {
	Name           string `json:"name,omitempty"`
	Org            string `json:"org,omitempty"`
	Region         string `json:"region,omitempty"`
	Plan           string `json:"plan,omitempty"`
	DiskSizeGB     int    `json:"diskSizeGb,omitempty"`
	PGMajorVersion string `json:"pgMajorVersion,omitempty"`
	PoolMode       string `json:"poolMode,omitempty"`
	PostGISEnabled *bool  `json:"postgisEnabled,omitempty"`

	// Outputs.
	ID       string    `json:"id,omitempty"`
	Status   string    `json:"status,omitempty"`
	CPUKind  string    `json:"cpuKind,omitempty"`
	CPUs     int       `json:"cpus,omitempty"`
	MemoryMB int       `json:"memoryMb,omitempty"`
	Replicas int       `json:"replicas,omitempty"`
	Primary  *Endpoint `json:"primaryEndpoint,omitempty"`
	Pooler   *Endpoint `json:"poolerEndpoint,omitempty"`
}

type clusterAPI struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	Region         string `json:"region,omitempty"`
	Plan           string `json:"plan,omitempty"`
	Status         string `json:"status,omitempty"`
	DiskSizeGB     int    `json:"disk_size_gb,omitempty"`
	PGMajorVersion string `json:"pg_major_version,omitempty"`
	PostGISEnabled *bool  `json:"postgis_enabled,omitempty"`
	CPUKind        string `json:"cpu_kind,omitempty"`
	CPUs           int    `json:"cpus,omitempty"`
	MemoryMB       int    `json:"memory_mb,omitempty"`
	Replicas       int    `json:"replicas,omitempty"`
	Organization   struct {
		Slug string `json:"slug,omitempty"`
	} `json:"organization,omitempty"`
	Endpoints *struct {
		Primary *struct {
			Direct *Endpoint `json:"direct,omitempty"`
			Pooler *Endpoint `json:"pooler,omitempty"`
		} `json:"primary,omitempty"`
	} `json:"endpoints,omitempty"`
}

// toProps maps the API shape to forma properties.
//
// poolMode is absent: the API accepts it on create and never reports it back,
// so surfacing it from Read would be inventing a value. It stays a create-only
// input the plugin sends and does not diff.
func (a clusterAPI) toProps() ClusterProperties {
	p := ClusterProperties{
		Name:           a.Name,
		Org:            a.Organization.Slug,
		Region:         a.Region,
		Plan:           a.Plan,
		DiskSizeGB:     a.DiskSizeGB,
		PGMajorVersion: a.PGMajorVersion,
		PostGISEnabled: a.PostGISEnabled,
		ID:             a.ID,
		Status:         a.Status,
		CPUKind:        a.CPUKind,
		CPUs:           a.CPUs,
		MemoryMB:       a.MemoryMB,
		Replicas:       a.Replicas,
	}
	if a.Endpoints != nil && a.Endpoints.Primary != nil {
		p.Primary = a.Endpoints.Primary.Direct
		p.Pooler = a.Endpoints.Primary.Pooler
	}
	return p
}

func (c *Cluster) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p ClusterProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	org := p.Org
	if org == "" && c.Target != nil {
		org = c.Target.Org
	}
	if org == "" || p.Plan == "" || p.Region == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"org, plan and region are required"), nil
	}
	body := map[string]any{"org_slug": org, "plan": p.Plan, "region": p.Region}
	if p.Name != "" {
		body["name"] = p.Name
	}
	if p.DiskSizeGB > 0 {
		body["disk_size_gb"] = p.DiskSizeGB
	}
	if p.PGMajorVersion != "" {
		body["pg_major_version"] = p.PGMajorVersion
	}
	if p.PoolMode != "" {
		body["pool_mode"] = p.PoolMode
	}
	if p.PostGISEnabled != nil {
		body["postgis_enabled"] = *p.PostGISEnabled
	}
	var resp struct {
		Data clusterAPI `json:"data"`
	}
	if err := c.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/postgres", Body: body,
	}, &resp); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	if resp.Data.ID == "" {
		return prov.FailCreate(resource.OperationErrorCodeServiceInternalError,
			"create response carried no cluster id"), nil
	}
	// Provisioning a real Postgres cluster takes minutes and `endpoints` is only
	// populated once it reaches `ready`, so anything downstream that needs a
	// connection host must wait.
	return prov.InProgressCreate(resp.Data.ID, "cluster created, waiting for it to become ready"), nil
}

func (c *Cluster) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	if req.NativeID == "" {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	api, err := c.get(ctx, req.NativeID)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	// A cluster mid-delete is gone as far as desired state goes; reporting it
	// keeps resurrecting it in the inventory.
	if api.Status == clusterStatusDeleted || api.Status == clusterStatusDeleting {
		return prov.NotFoundRead(req.ResourceType), nil
	}
	return prov.OKRead(req.ResourceType, api.toProps()), nil
}

func (c *Cluster) Update(_ context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Postgres::Cluster has no update path; plan, disk size and version are createOnly"), nil
}

func (c *Cluster) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	if req.NativeID == "" {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, "native id required"), nil
	}
	// 410 Gone for an already-deleted cluster is classified as NotFound by the
	// transport, so this stays idempotent.
	if err := c.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/postgres/" + req.NativeID,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

// Status polls the cluster until it settles. The status walk is
// creating → initializing → ready.
func (c *Cluster) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	if id == "" {
		return prov.FailStatus(resource.OperationErrorCodeInvalidRequest, "request id required"), nil
	}
	api, err := c.get(ctx, id)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.FailStatus(resource.OperationErrorCodeNotFound,
				"cluster disappeared while waiting for it to become ready"), nil
		}
		return prov.FailStatus(flytransport.ClassifyError(err), err.Error()), nil
	}
	switch api.Status {
	case clusterStatusReady:
		// Same reason as Machine: Create returned InProgress, so this is where
		// formae first learns the cluster's endpoints and sizing. A
		// `cluster.res.id` reference stays unresolvable until it does — and the
		// endpoints only exist once the cluster is ready anyway.
		return prov.SuccessStatusWithProps(id, api.toProps()), nil
	case clusterStatusCreating, clusterStatusInitializing:
		return prov.InProgressStatus(id, "cluster is "+api.Status), nil
	case clusterStatusFailed:
		return prov.FailStatus(resource.OperationErrorCodeServiceInternalError,
			"cluster entered the failed state"), nil
	case clusterStatusDeleting, clusterStatusDeleted:
		return prov.FailStatus(resource.OperationErrorCodeNotFound,
			"cluster was deleted while waiting for it to become ready"), nil
	default:
		// Unknown states keep polling rather than failing — Fly can add them.
		return prov.InProgressStatus(id, "cluster is in unrecognised state "+api.Status), nil
	}
}

func (c *Cluster) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	empty := &resource.ListResult{NativeIDs: []string{}}
	if c.Target == nil || c.Target.Org == "" {
		return empty, nil
	}
	ids, err := ClusterIDs(ctx, c.Client, c.Target.Org)
	if err != nil {
		return empty, nil
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

// ClusterIDs lists the non-deleted cluster ids in an org. Shared with the
// child resources, which have no org-wide endpoint and must walk clusters.
func ClusterIDs(ctx context.Context, client *flytransport.Client, org string) ([]string, error) {
	var resp struct {
		Data []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			DeletedAt string `json:"deleted_at"`
		} `json:"data"`
	}
	if err := client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/postgres",
		Query: map[string]string{"org_slug": org},
	}, &resp); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resp.Data))
	for _, cl := range resp.Data {
		// include_deleted defaults to false, but the summary carries deleted_at
		// and a deleting status — belt and braces, since importing a tombstone
		// would give the user a resource they cannot manage.
		if cl.ID == "" || cl.DeletedAt != "" ||
			cl.Status == clusterStatusDeleted || cl.Status == clusterStatusDeleting {
			continue
		}
		ids = append(ids, cl.ID)
	}
	return ids, nil
}

func (c *Cluster) get(ctx context.Context, id string) (clusterAPI, error) {
	var resp struct {
		Data clusterAPI `json:"data"`
	}
	err := c.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/postgres/" + id,
	}, &resp)
	return resp.Data, err
}
