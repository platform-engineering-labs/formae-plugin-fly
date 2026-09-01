// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apps

import (
	"context"
	"encoding/json"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceTypeVolumeSnapshot is the FLY::Apps::VolumeSnapshot resource type.
const ResourceTypeVolumeSnapshot = "FLY::Apps::VolumeSnapshot"

func init() {
	registry.Register(
		ResourceTypeVolumeSnapshot,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &VolumeSnapshot{Client: c, Target: cfg}
		},
	)
}

// VolumeSnapshot — FLY::Apps::VolumeSnapshot.
//
//	POST /v1/apps/{app}/volumes/{volume}/snapshots   Create
//	GET  /v1/apps/{app}/volumes/{volume}/snapshots   List / Read
//
// Native id is "{app}/{volume_id}/{snapshot_id}".
//
// Same caveat as FLY::Postgres::Backup, and for the same reason: Fly exposes no
// delete endpoint for snapshots, so `formae destroy` reports success and the
// snapshot remains until it ages out under the volume's snapshot_retention. The
// id is server-assigned, so re-applying after a destroy takes a new snapshot
// rather than reconciling to the existing one.
//
// Volumes take scheduled snapshots on their own by default
// (auto_backup_enabled). Use this resource for an explicit snapshot at apply
// time — before a migration, say — not as a backup schedule.
type VolumeSnapshot struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// VolumeSnapshotProperties is the forma-facing shape.
type VolumeSnapshotProperties struct {
	AppName  string `json:"appName,omitempty"`
	VolumeID string `json:"volumeId,omitempty"`

	// Outputs.
	ID        string `json:"id,omitempty"`
	Status    string `json:"status,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Digest    string `json:"digest,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

type volumeSnapshotAPI struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Size      int64  `json:"size"`
	Digest    string `json:"digest"`
	CreatedAt string `json:"created_at"`
}

func (a volumeSnapshotAPI) toProps(app, volume string) VolumeSnapshotProperties {
	return VolumeSnapshotProperties{
		AppName:   app,
		VolumeID:  volume,
		ID:        a.ID,
		Status:    a.Status,
		Size:      a.Size,
		Digest:    a.Digest,
		CreatedAt: a.CreatedAt,
	}
}

// Create takes a snapshot. The POST does not identify the new snapshot, so the
// id is recovered by diffing the listing before and after.
func (v *VolumeSnapshot) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p VolumeSnapshotProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.AppName == "" || p.VolumeID == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"appName and volumeId are required"), nil
	}
	before, err := listSnapshots(ctx, v.Client, p.AppName, p.VolumeID)
	if err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	seen := make(map[string]struct{}, len(before))
	for _, s := range before {
		seen[s.ID] = struct{}{}
	}
	if err := v.Client.Do(ctx, flytransport.Request{
		Method: "POST",
		Path:   "/v1/apps/" + p.AppName + "/volumes/" + p.VolumeID + "/snapshots",
	}, nil); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	after, err := listSnapshots(ctx, v.Client, p.AppName, p.VolumeID)
	if err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	for _, s := range after {
		if _, known := seen[s.ID]; !known && s.ID != "" {
			return prov.SuccessCreate(prov.JoinThreePart(p.AppName, p.VolumeID, s.ID)), nil
		}
	}
	return prov.FailCreate(resource.OperationErrorCodeServiceInternalError,
		"snapshot was requested but no new snapshot appeared in the listing"), nil
}

func (v *VolumeSnapshot) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	app, volume, id, err := prov.ParseThreePart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	snaps, err := listSnapshots(ctx, v.Client, app, volume)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	for _, s := range snaps {
		if s.ID == id {
			return prov.OKRead(req.ResourceType, s.toProps(app, volume)), nil
		}
	}
	return prov.NotFoundRead(req.ResourceType), nil
}

func (v *VolumeSnapshot) Update(_ context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Apps::VolumeSnapshot is immutable once taken"), nil
}

// Delete cannot actually delete: Fly exposes no endpoint for removing a volume
// snapshot. Success keeps destroy from wedging; the message says what happened.
func (v *VolumeSnapshot) Delete(_ context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	res := prov.SuccessDelete(req.NativeID)
	res.ProgressResult.StatusMessage =
		"Fly exposes no delete endpoint for volume snapshots; the snapshot remains and will expire under the volume's snapshot_retention"
	return res, nil
}

func (v *VolumeSnapshot) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

// List walks the org's volumes, then each volume's snapshots.
func (v *VolumeSnapshot) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	if v.Target == nil || v.Target.Org == "" {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	var resp struct {
		Volumes []struct {
			ID      string `json:"id"`
			AppName string `json:"app_name"`
		} `json:"volumes"`
	}
	if err := v.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/orgs/" + v.Target.Org + "/volumes",
		Query: map[string]string{"summary": "true"},
	}, &resp); err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, vol := range resp.Volumes {
		if vol.ID == "" || vol.AppName == "" {
			continue
		}
		snaps, err := listSnapshots(ctx, v.Client, vol.AppName, vol.ID)
		if err != nil {
			continue
		}
		for _, s := range snaps {
			if s.ID == "" {
				continue
			}
			ids = append(ids, prov.JoinThreePart(vol.AppName, vol.ID, s.ID))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func listSnapshots(ctx context.Context, client *flytransport.Client, app, volume string) ([]volumeSnapshotAPI, error) {
	var snaps []volumeSnapshotAPI
	if err := client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + app + "/volumes/" + volume + "/snapshots",
	}, &snaps); err != nil {
		return nil, err
	}
	return snaps, nil
}
