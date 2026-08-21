// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apps

import (
	"context"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func newVolume(t *testing.T, routes map[string]route) (*Volume, *stub) {
	t.Helper()
	s := newStub(t, routes)
	return &Volume{Client: s.client(), Target: s.target()}, s
}

func TestVolumeCreateIsSync(t *testing.T) {
	v, s := newVolume(t, map[string]route{
		"POST /v1/apps/my-api/volumes": {200, `{"id":"vol_9x1kj4rz","name":"data","region":"fra","size_gb":1,"state":"created","zone":"a1b2"}`},
	})
	res, err := v.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeVolume,
		Properties: mustJSON(t, map[string]any{
			"appName": "my-api", "name": "data", "sizeGb": 1, "fstype": "ext4",
		}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v: %s", res.ProgressResult.OperationStatus, res.ProgressResult.StatusMessage)
	}
	if res.ProgressResult.NativeID != "my-api/vol_9x1kj4rz" {
		t.Errorf("NativeID = %q", res.ProgressResult.NativeID)
	}
	body := s.only().Body
	if body["size_gb"] != float64(1) || body["fstype"] != "ext4" || body["region"] != "fra" {
		t.Errorf("body = %+v", body)
	}
}

func TestVolumeReadMapsBackAndDropsCounters(t *testing.T) {
	v, _ := newVolume(t, map[string]route{
		"GET /v1/apps/my-api/volumes/vol_9x1kj4rz": {200, `{
			"id":"vol_9x1kj4rz","name":"data","region":"fra","size_gb":3,
			"state":"created","zone":"a1b2","attached_machine_id":"17811943c9d489",
			"auto_backup_enabled":true,"snapshot_retention":5,
			"bytes_used":12345,"blocks_free":99,"block_size":4096}`},
	})
	res, err := v.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeVolume, NativeID: "my-api/vol_9x1kj4rz",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	props := decodeProps(t, res.Properties)
	if props["appName"] != "my-api" || props["sizeGb"] != float64(3) ||
		props["attachedMachineId"] != "17811943c9d489" || props["snapshotRetention"] != float64(5) {
		t.Errorf("props = %+v", props)
	}
	// bytes_used and friends move as the volume is written to; surfacing them
	// would be perpetual drift.
	for _, k := range []string{"bytesUsed", "blocksFree", "blockSize", "bytes_used"} {
		if _, ok := props[k]; ok {
			t.Errorf("%s leaked into properties", k)
		}
	}
}

func TestVolumeUpdateExtendsAndSetsBackupSettings(t *testing.T) {
	v, s := newVolume(t, map[string]route{
		"PUT /v1/apps/my-api/volumes/vol_1/extend": {200, `{"needs_restart":false,"volume":{"id":"vol_1","size_gb":5}}`},
		"PUT /v1/apps/my-api/volumes/vol_1":        {200, `{"id":"vol_1","size_gb":5}`},
	})
	yes := true
	res, err := v.Update(context.Background(), &resource.UpdateRequest{
		NativeID:          "my-api/vol_1",
		PriorProperties:   mustJSON(t, VolumeProperties{AppName: "my-api", Name: "data", SizeGB: 1}),
		DesiredProperties: mustJSON(t, VolumeProperties{AppName: "my-api", Name: "data", SizeGB: 5, AutoBackupEnabled: &yes}),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v: %s", res.ProgressResult.OperationStatus, res.ProgressResult.StatusMessage)
	}
	if len(s.calls) != 2 {
		t.Fatalf("want extend + settings calls, got %+v", s.calls)
	}
	if s.calls[0].Path != "/v1/apps/my-api/volumes/vol_1/extend" || s.calls[0].Body["size_gb"] != float64(5) {
		t.Errorf("extend call = %+v", s.calls[0])
	}
	if s.calls[1].Body["auto_backup_enabled"] != true {
		t.Errorf("settings call = %+v", s.calls[1])
	}
}

// Fly cannot shrink a volume. Refuse loudly rather than leaving the volume
// bigger than the forma says — that would be drift the user cannot see.
func TestVolumeUpdateRefusesShrink(t *testing.T) {
	v, s := newVolume(t, map[string]route{})
	res, err := v.Update(context.Background(), &resource.UpdateRequest{
		NativeID:          "my-api/vol_1",
		PriorProperties:   mustJSON(t, VolumeProperties{SizeGB: 10}),
		DesiredProperties: mustJSON(t, VolumeProperties{SizeGB: 1}),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode = %q, want InvalidRequest", res.ProgressResult.ErrorCode)
	}
	if len(s.calls) != 0 {
		t.Error("shrink reached the API")
	}
}

// Nothing mutable changed: no wire traffic at all.
func TestVolumeUpdateNoopMakesNoCalls(t *testing.T) {
	v, s := newVolume(t, map[string]route{})
	res, err := v.Update(context.Background(), &resource.UpdateRequest{
		NativeID:          "my-api/vol_1",
		PriorProperties:   mustJSON(t, VolumeProperties{SizeGB: 3}),
		DesiredProperties: mustJSON(t, VolumeProperties{SizeGB: 3}),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v", res.ProgressResult.OperationStatus)
	}
	if len(s.calls) != 0 {
		t.Errorf("noop update made calls: %+v", s.calls)
	}
}

func TestVolumeDeleteIdempotent(t *testing.T) {
	v, _ := newVolume(t, map[string]route{
		"DELETE /v1/apps/my-api/volumes/gone": {404, `{"error":"not found"}`},
	})
	res, _ := v.Delete(context.Background(), &resource.DeleteRequest{NativeID: "my-api/gone"})
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v", res.ProgressResult.OperationStatus)
	}
}

func TestVolumeListUsesOrgEndpoint(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/orgs/test-org/volumes": {200, `{"volumes":[
			{"id":"vol_1","app_name":"one"},{"id":"vol_2","app_name":"two"}],"next_cursor":""}`},
	})
	v := &Volume{Client: s.client(), Target: s.target()}
	res, err := v.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 2 || res.NativeIDs[0] != "one/vol_1" {
		t.Errorf("NativeIDs = %v", res.NativeIDs)
	}
	if res.NextPageToken != nil {
		t.Errorf("NextPageToken = %v, want nil for an empty cursor", *res.NextPageToken)
	}
}
