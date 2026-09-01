// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceTypeVolume is the FLY::Apps::Volume resource type.
const ResourceTypeVolume = "FLY::Apps::Volume"

func init() {
	registry.Register(
		ResourceTypeVolume,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Volume{Client: c, Target: cfg}
		},
	)
}

// Volume — FLY::Apps::Volume.
//
// API mapping:
//
//	POST   /v1/apps/{app}/volumes                Create (sync)
//	GET    /v1/apps/{app}/volumes/{id}           Read
//	PUT    /v1/apps/{app}/volumes/{id}           Update (backup settings only)
//	PUT    /v1/apps/{app}/volumes/{id}/extend    Grow the volume
//	DELETE /v1/apps/{app}/volumes/{id}           Delete
//	GET    /v1/orgs/{org}/volumes                List (org-wide, for discovery)
//
// Native id is "{app}/{volume_id}".
type Volume struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// VolumeProperties is the forma-facing shape.
type VolumeProperties struct {
	AppName           string `json:"appName,omitempty"`
	Name              string `json:"name,omitempty"`
	Region            string `json:"region,omitempty"`
	SizeGB            int    `json:"sizeGb,omitempty"`
	Encrypted         *bool  `json:"encrypted,omitempty"`
	FSType            string `json:"fstype,omitempty"`
	RequireUniqueZone *bool  `json:"requireUniqueZone,omitempty"`
	AutoBackupEnabled *bool  `json:"autoBackupEnabled,omitempty"`
	SnapshotRetention *int   `json:"snapshotRetention,omitempty"`

	// Outputs.
	ID                string `json:"id,omitempty"`
	State             string `json:"state,omitempty"`
	Zone              string `json:"zone,omitempty"`
	AttachedMachineID string `json:"attachedMachineId,omitempty"`
}

// volumeAPI is the Fly-API-facing shape. The block/byte counters the API also
// returns are omitted: they move as the volume is written to and would surface
// as perpetual drift.
type volumeAPI struct {
	ID                string `json:"id,omitempty"`
	Name              string `json:"name,omitempty"`
	Region            string `json:"region,omitempty"`
	SizeGB            int    `json:"size_gb,omitempty"`
	Encrypted         *bool  `json:"encrypted,omitempty"`
	FSType            string `json:"fstype,omitempty"`
	AutoBackupEnabled *bool  `json:"auto_backup_enabled,omitempty"`
	SnapshotRetention *int   `json:"snapshot_retention,omitempty"`
	State             string `json:"state,omitempty"`
	Zone              string `json:"zone,omitempty"`
	AttachedMachineID string `json:"attached_machine_id,omitempty"`
}

func (a volumeAPI) toProps(appName string) VolumeProperties {
	return VolumeProperties{
		AppName:           appName,
		Name:              a.Name,
		Region:            a.Region,
		SizeGB:            a.SizeGB,
		Encrypted:         a.Encrypted,
		FSType:            a.FSType,
		AutoBackupEnabled: a.AutoBackupEnabled,
		SnapshotRetention: a.SnapshotRetention,
		ID:                a.ID,
		State:             a.State,
		Zone:              a.Zone,
		AttachedMachineID: a.AttachedMachineID,
	}
}

func (v *Volume) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p VolumeProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.AppName == "" || p.Name == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"appName and name are required"), nil
	}
	body := map[string]any{"name": p.Name}
	if region := v.region(p.Region); region != "" {
		body["region"] = region
	}
	if p.SizeGB > 0 {
		body["size_gb"] = p.SizeGB
	}
	if p.Encrypted != nil {
		body["encrypted"] = *p.Encrypted
	}
	if p.FSType != "" {
		body["fstype"] = p.FSType
	}
	if p.RequireUniqueZone != nil {
		body["require_unique_zone"] = *p.RequireUniqueZone
	}
	if p.AutoBackupEnabled != nil {
		body["auto_backup_enabled"] = *p.AutoBackupEnabled
	}
	if p.SnapshotRetention != nil {
		body["snapshot_retention"] = *p.SnapshotRetention
	}
	var apiResp volumeAPI
	if err := v.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/apps/" + p.AppName + "/volumes", Body: body,
	}, &apiResp); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	if apiResp.ID == "" {
		return prov.FailCreate(resource.OperationErrorCodeServiceInternalError,
			"create response carried no volume id"), nil
	}
	// Provisioning is fast and the response already carries the volume, so
	// there is nothing to poll for.
	return prov.SuccessCreate(prov.JoinTwoPart(p.AppName, apiResp.ID), apiResp.toProps(p.AppName)), nil
}

func (v *Volume) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	app, id, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	var apiResp volumeAPI
	if err := v.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + app + "/volumes/" + id,
	}, &apiResp); err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	return prov.OKRead(req.ResourceType, apiResp.toProps(app)), nil
}

// Update handles the two things a volume can change in place: its backup
// settings (PUT) and its size (PUT .../extend). Everything else is createOnly.
//
// A shrink is rejected rather than ignored: Fly cannot shrink a volume, and
// silently leaving it larger than the forma says would be drift the user cannot
// see.
func (v *Volume) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	app, id, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	var prior, desired VolumeProperties
	if len(req.PriorProperties) > 0 {
		if err := json.Unmarshal(req.PriorProperties, &prior); err != nil {
			return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
		}
	}
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}

	if desired.SizeGB > 0 && prior.SizeGB > 0 && desired.SizeGB != prior.SizeGB {
		if desired.SizeGB < prior.SizeGB {
			return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest,
				fmt.Sprintf("volumes cannot shrink: %d GB -> %d GB", prior.SizeGB, desired.SizeGB)), nil
		}
		if err := v.Client.Do(ctx, flytransport.Request{
			Method: "PUT", Path: "/v1/apps/" + app + "/volumes/" + id + "/extend",
			Body: map[string]any{"size_gb": desired.SizeGB},
		}, nil); err != nil {
			return prov.FailUpdate(flytransport.ClassifyError(err), err.Error()), nil
		}
	}

	body := map[string]any{}
	if desired.AutoBackupEnabled != nil {
		body["auto_backup_enabled"] = *desired.AutoBackupEnabled
	}
	if desired.SnapshotRetention != nil {
		body["snapshot_retention"] = *desired.SnapshotRetention
	}
	if len(body) > 0 {
		if err := v.Client.Do(ctx, flytransport.Request{
			Method: "PUT", Path: "/v1/apps/" + app + "/volumes/" + id, Body: body,
		}, nil); err != nil {
			return prov.FailUpdate(flytransport.ClassifyError(err), err.Error()), nil
		}
	}
	return prov.SuccessUpdate(req.NativeID, desired), nil
}

func (v *Volume) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	app, id, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if err := v.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/apps/" + app + "/volumes/" + id,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

// Status reports Success: volume operations are synchronous.
func (v *Volume) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

// List enumerates volumes org-wide in one call, for the same reason as Machine.
func (v *Volume) List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error) {
	empty := &resource.ListResult{NativeIDs: []string{}}
	if v.Target == nil || v.Target.Org == "" {
		return empty, nil
	}
	query := map[string]string{"summary": "true"}
	if req != nil && req.PageToken != nil && *req.PageToken != "" {
		query["cursor"] = *req.PageToken
	}
	var resp struct {
		Volumes []struct {
			ID      string `json:"id"`
			AppName string `json:"app_name"`
		} `json:"volumes"`
		NextCursor string `json:"next_cursor"`
	}
	if err := v.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/orgs/" + v.Target.Org + "/volumes", Query: query,
	}, &resp); err != nil {
		return empty, nil
	}
	ids := make([]string, 0, len(resp.Volumes))
	for _, vol := range resp.Volumes {
		if vol.ID == "" || vol.AppName == "" {
			continue
		}
		ids = append(ids, prov.JoinTwoPart(vol.AppName, vol.ID))
	}
	out := &resource.ListResult{NativeIDs: ids}
	if resp.NextCursor != "" {
		out.NextPageToken = &resp.NextCursor
	}
	return out, nil
}

func (v *Volume) region(resourceRegion string) string {
	if resourceRegion != "" {
		return resourceRegion
	}
	if v.Target != nil {
		return v.Target.Region
	}
	return ""
}
