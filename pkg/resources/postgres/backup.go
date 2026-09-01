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

// ResourceTypeBackup is the FLY::Postgres::Backup resource type.
const ResourceTypeBackup = "FLY::Postgres::Backup"

func init() {
	registry.Register(
		ResourceTypeBackup,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Backup{Client: c, Target: cfg}
		},
	)
}

// Backup — FLY::Postgres::Backup.
//
//	POST /v1/postgres/{cluster}/backups   Create
//	GET  /v1/postgres/{cluster}/backups   List / Read
//
// Native id is "{cluster_id}/{backup_id}".
//
// READ THIS BEFORE USING IT. A backup is closer to an event than to a piece of
// infrastructure, and it fits desired-state modelling badly:
//
//   - There is no delete endpoint. Fly exposes no way to remove a backup; they
//     age out under the cluster's retention policy. `formae destroy` therefore
//     reports success and leaves the backup in place. The alternative — failing
//     the destroy — would wedge every stack containing one.
//   - Backup ids are server-assigned, so a forma cannot name one. Re-applying
//     the same forma after a destroy takes a *new* backup rather than
//     reconciling to the existing one.
//
// It is modelled because the endpoint is declarative enough to catalogue and
// some users want "take a backup at apply time" in a pipeline. If you want
// scheduled backups, use the cluster's retention policy instead.
type Backup struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// BackupProperties is the forma-facing shape.
type BackupProperties struct {
	ClusterID string `json:"clusterId,omitempty"`
	Type      string `json:"backupType,omitempty"`

	// Outputs.
	ID         string `json:"id,omitempty"`
	Status     string `json:"status,omitempty"`
	SizeBytes  int64  `json:"sizeBytes,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

type backupAPI struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	SizeBytes  int64  `json:"size_bytes"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

func (a backupAPI) toProps(cluster string) BackupProperties {
	return BackupProperties{
		ClusterID:  cluster,
		Type:       a.Type,
		ID:         a.ID,
		Status:     a.Status,
		SizeBytes:  a.SizeBytes,
		StartedAt:  a.StartedAt,
		FinishedAt: a.FinishedAt,
	}
}

// Create takes a backup. The POST answers 202 without a body identifying the
// new backup, so the id is recovered by listing and taking the newest row the
// caller did not already have — see newestBackupID.
func (b *Backup) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p BackupProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.ClusterID == "" || p.Type == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"clusterId and backupType are required"), nil
	}
	before, err := listBackups(ctx, b.Client, p.ClusterID)
	if err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	seen := make(map[string]struct{}, len(before))
	for _, bk := range before {
		seen[bk.ID] = struct{}{}
	}
	if err := b.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/postgres/" + p.ClusterID + "/backups",
		Body: map[string]any{"type": p.Type},
	}, nil); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	after, err := listBackups(ctx, b.Client, p.ClusterID)
	if err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	for _, bk := range after {
		if _, known := seen[bk.ID]; !known && bk.ID != "" {
			return prov.SuccessCreate(prov.JoinTwoPart(p.ClusterID, bk.ID)), nil
		}
	}
	return prov.FailCreate(resource.OperationErrorCodeServiceInternalError,
		"backup was requested but no new backup appeared in the listing"), nil
}

func (b *Backup) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	cluster, id, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	backups, err := listBackups(ctx, b.Client, cluster)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	for _, bk := range backups {
		if bk.ID == id {
			return prov.OKRead(req.ResourceType, bk.toProps(cluster)), nil
		}
	}
	// Aged out under the retention policy.
	return prov.NotFoundRead(req.ResourceType), nil
}

func (b *Backup) Update(_ context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Postgres::Backup is immutable once taken"), nil
}

// Delete cannot actually delete. Fly exposes no endpoint for removing a backup;
// they expire under the cluster's retention policy. Success is reported so a
// destroy is not wedged forever, and the message says what really happened.
func (b *Backup) Delete(_ context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	res := prov.SuccessDelete(req.NativeID)
	res.ProgressResult.StatusMessage =
		"Fly exposes no delete endpoint for Postgres backups; the backup remains and will expire under the cluster's retention policy"
	return res, nil
}

func (b *Backup) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

func (b *Backup) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	if b.Target == nil || b.Target.Org == "" {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	clusters, err := ClusterIDs(ctx, b.Client, b.Target.Org)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, cluster := range clusters {
		backups, err := listBackups(ctx, b.Client, cluster)
		if err != nil {
			continue
		}
		for _, bk := range backups {
			if bk.ID == "" {
				continue
			}
			ids = append(ids, prov.JoinTwoPart(cluster, bk.ID))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func listBackups(ctx context.Context, client *flytransport.Client, cluster string) ([]backupAPI, error) {
	var resp struct {
		Data []backupAPI `json:"data"`
	}
	if err := client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/postgres/" + cluster + "/backups",
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}
