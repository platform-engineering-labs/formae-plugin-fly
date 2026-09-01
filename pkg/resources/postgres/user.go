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

// ResourceTypeUser is the FLY::Postgres::User resource type.
const ResourceTypeUser = "FLY::Postgres::User"

func init() {
	registry.Register(
		ResourceTypeUser,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &User{Client: c, Target: cfg}
		},
	)
}

// User — FLY::Postgres::User.
//
//	POST   /v1/postgres/{cluster}/users               Create
//	GET    /v1/postgres/{cluster}/users               List (no per-user GET)
//	PATCH  /v1/postgres/{cluster}/users/{username}    Update role
//	DELETE /v1/postgres/{cluster}/users/{username}    Delete
//
// Native id is "{cluster_id}/{username}". Role is the one mutable field.
//
// The generated password is deliberately NOT surfaced. Create does not return
// it, and the separate GET .../credentials endpoint that would reveal it is not
// called: pulling a live database password into formae's state to populate an
// output field is a worse trade than making the user fetch it themselves with
// `fly mpg` when they need it.
type User struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// UserProperties is the forma-facing shape.
type UserProperties struct {
	ClusterID string `json:"clusterId,omitempty"`
	Username  string `json:"username,omitempty"`
	Role      string `json:"role,omitempty"`
}

func (u *User) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p UserProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.ClusterID == "" || p.Username == "" || p.Role == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"clusterId, username and role are required"), nil
	}
	if err := u.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/postgres/" + p.ClusterID + "/users",
		Body: map[string]any{"username": p.Username, "role": p.Role},
	}, nil); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessCreate(prov.JoinTwoPart(p.ClusterID, p.Username)), nil
}

// Read scans the cluster's user list: there is no per-user GET.
func (u *User) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	cluster, username, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	users, err := listUsers(ctx, u.Client, cluster)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	for _, usr := range users {
		if usr.Username == username {
			return prov.OKRead(req.ResourceType, UserProperties{
				ClusterID: cluster, Username: usr.Username, Role: usr.Role,
			}), nil
		}
	}
	return prov.NotFoundRead(req.ResourceType), nil
}

func (u *User) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	cluster, username, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	var desired UserProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if desired.Role == "" {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, "role is required"), nil
	}
	// Role is the only mutable field; username is the identity and clusterId is
	// createOnly, so formae plans a replacement for either.
	if err := u.Client.Do(ctx, flytransport.Request{
		Method: "PATCH", Path: "/v1/postgres/" + cluster + "/users/" + username,
		Body: map[string]any{"role": desired.Role},
	}, nil); err != nil {
		return prov.FailUpdate(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessUpdate(req.NativeID), nil
}

func (u *User) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	cluster, username, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if err := u.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/postgres/" + cluster + "/users/" + username,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

func (u *User) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

func (u *User) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	if u.Target == nil || u.Target.Org == "" {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	clusters, err := ClusterIDs(ctx, u.Client, u.Target.Org)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, cluster := range clusters {
		users, err := listUsers(ctx, u.Client, cluster)
		if err != nil {
			continue
		}
		for _, usr := range users {
			if usr.Username == "" {
				continue
			}
			ids = append(ids, prov.JoinTwoPart(cluster, usr.Username))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

type userAPI struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func listUsers(ctx context.Context, client *flytransport.Client, cluster string) ([]userAPI, error) {
	var resp struct {
		Data []userAPI `json:"data"`
	}
	if err := client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/postgres/" + cluster + "/users",
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}
