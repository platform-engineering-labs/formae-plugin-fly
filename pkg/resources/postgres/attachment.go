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

// ResourceTypeAttachment is the FLY::Postgres::Attachment resource type.
const ResourceTypeAttachment = "FLY::Postgres::Attachment"

func init() {
	registry.Register(
		ResourceTypeAttachment,
		// An attachment is a link between two things: both ends are its
		// identity, so there is nothing to update.
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Attachment{Client: c, Target: cfg}
		},
	)
}

// Attachment — FLY::Postgres::Attachment.
//
//	POST   /v1/postgres/{cluster}/attachments        Attach an app
//	DELETE /v1/postgres/{cluster}/attachments/{app}  Detach
//	GET    /v1/postgres/{cluster}                    Read (via attached_apps)
//
// Native id is "{cluster_id}/{app_name}".
//
// Attaching is what wires a Fly app to a cluster: it provisions a database and
// a user for the app and injects a DATABASE_URL secret. Because that secret is
// created by Fly rather than by this plugin, it is not modelled as a
// FLY::Apps::Secrets entry — the two would fight over the same key.
type Attachment struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// AttachmentProperties is the forma-facing shape.
type AttachmentProperties struct {
	ClusterID string `json:"clusterId,omitempty"`
	AppName   string `json:"appName,omitempty"`
}

func (a *Attachment) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p AttachmentProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.ClusterID == "" || p.AppName == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"clusterId and appName are required"), nil
	}
	if err := a.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/postgres/" + p.ClusterID + "/attachments",
		Body: map[string]any{"app_name": p.AppName},
	}, nil); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessCreate(prov.JoinTwoPart(p.ClusterID, p.AppName)), nil
}

// Read checks the cluster's attached_apps list: there is no per-attachment GET.
func (a *Attachment) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	cluster, app, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	apps, err := attachedApps(ctx, a.Client, cluster)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	for _, name := range apps {
		if name == app {
			return prov.OKRead(req.ResourceType, AttachmentProperties{ClusterID: cluster, AppName: app}), nil
		}
	}
	return prov.NotFoundRead(req.ResourceType), nil
}

func (a *Attachment) Update(_ context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Postgres::Attachment has no update path; both ends are its identity"), nil
}

func (a *Attachment) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	cluster, app, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	// 410 Gone for an already-detached app is NotFound to the transport.
	if err := a.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/postgres/" + cluster + "/attachments/" + app,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

func (a *Attachment) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

func (a *Attachment) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	if a.Target == nil || a.Target.Org == "" {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	clusters, err := ClusterIDs(ctx, a.Client, a.Target.Org)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, cluster := range clusters {
		apps, err := attachedApps(ctx, a.Client, cluster)
		if err != nil {
			continue
		}
		for _, name := range apps {
			ids = append(ids, prov.JoinTwoPart(cluster, name))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func attachedApps(ctx context.Context, client *flytransport.Client, cluster string) ([]string, error) {
	var resp struct {
		Data struct {
			AttachedApps []struct {
				Name string `json:"name"`
			} `json:"attached_apps"`
		} `json:"data"`
	}
	if err := client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/postgres/" + cluster,
	}, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Data.AttachedApps))
	for _, app := range resp.Data.AttachedApps {
		if app.Name != "" {
			names = append(names, app.Name)
		}
	}
	return names, nil
}
