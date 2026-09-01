// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apps

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// --- VolumeSnapshot ---------------------------------------------------------

// The POST does not identify the new snapshot, so Create diffs the listing.
func TestVolumeSnapshotCreateRecoversIDByDiffing(t *testing.T) {
	s := newStub(t, map[string]route{
		"POST /v1/apps/my-api/volumes/vol_1/snapshots": {200, ``},
	})
	n := 0
	s.handler = func(path, query string) (int, string, bool) {
		if path != "/v1/apps/my-api/volumes/vol_1/snapshots" {
			return 0, "", false
		}
		n++
		switch n {
		case 1:
			return 200, `[{"id":"snap_old","status":"created"}]`, true
		case 2:
			return 0, "", false // let the POST fall through to the route table
		default:
			return 200, `[{"id":"snap_old","status":"created"},{"id":"snap_new","status":"creating"}]`, true
		}
	}
	v := &VolumeSnapshot{Client: s.client(), Target: s.target()}
	res, err := v.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{"appName": "my-api", "volumeId": "vol_1"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.NativeID != "my-api/vol_1/snap_new" {
		t.Errorf("NativeID = %q, want the three-part id of the new snapshot", res.ProgressResult.NativeID)
	}
}

func TestVolumeSnapshotReadThreePart(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/apps/my-api/volumes/vol_1/snapshots": {200, `[{"id":"snap_1","status":"created","size":1024,"digest":"abc"}]`},
	})
	v := &VolumeSnapshot{Client: s.client(), Target: s.target()}
	res, _ := v.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeVolumeSnapshot, NativeID: "my-api/vol_1/snap_1",
	})
	if res.ErrorCode != "" {
		t.Fatalf("ErrorCode = %q", res.ErrorCode)
	}
	props := decodeProps(t, res.Properties)
	if props["appName"] != "my-api" || props["volumeId"] != "vol_1" || props["id"] != "snap_1" {
		t.Errorf("props = %+v", props)
	}
}

// Same contract as Postgres backups: no delete endpoint exists, so succeed and
// say what really happened rather than wedging every destroy.
func TestVolumeSnapshotDeleteIsSuccessButExplains(t *testing.T) {
	s := newStub(t, map[string]route{})
	v := &VolumeSnapshot{Client: s.client(), Target: s.target()}
	res, err := v.Delete(context.Background(), &resource.DeleteRequest{NativeID: "my-api/vol_1/snap_1"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatal("delete must succeed or destroy wedges")
	}
	if res.ProgressResult.StatusMessage == "" {
		t.Error("delete must explain the snapshot was not removed")
	}
	if len(s.calls) != 0 {
		t.Errorf("delete hit the API: %+v", s.calls)
	}
}

// --- SecretKey --------------------------------------------------------------

// No material supplied: use the generate endpoint. Safer default than asking a
// user to paste key bytes into a forma.
func TestSecretKeyCreateWithoutValueGenerates(t *testing.T) {
	s := newStub(t, map[string]route{
		"POST /v1/apps/my-api/secretkeys/signing/generate": {201, `{"name":"signing","type":"hs256"}`},
	})
	k := &SecretKey{Client: s.client(), Target: s.target()}
	res, err := k.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{"appName": "my-api", "name": "signing", "keyType": "hs256"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.NativeID != "my-api/signing" {
		t.Errorf("NativeID = %q", res.ProgressResult.NativeID)
	}
	c := s.only()
	if c.Path != "/v1/apps/my-api/secretkeys/signing/generate" {
		t.Errorf("path = %q, want the generate endpoint", c.Path)
	}
	if _, ok := c.Body["value"]; ok {
		t.Errorf("generate must not send a value: %+v", c.Body)
	}
}

// Supplied material is base64 in the forma and a JSON array of byte values on
// the wire — Go would otherwise marshal []byte back to base64, which the API
// does not accept.
func TestSecretKeyCreateWithValueSendsByteArray(t *testing.T) {
	s := newStub(t, map[string]route{
		"POST /v1/apps/my-api/secretkeys/signing": {201, `{"name":"signing"}`},
	})
	k := &SecretKey{Client: s.client(), Target: s.target()}
	_, err := k.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{
			"appName": "my-api", "name": "signing", "keyType": "hs256",
			"value": base64.StdEncoding.EncodeToString([]byte{1, 2, 255}),
		}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	c := s.only()
	arr, ok := c.Body["value"].([]any)
	if !ok {
		t.Fatalf("value should be a JSON array, got %T: %+v", c.Body["value"], c.Body)
	}
	if len(arr) != 3 || arr[0] != float64(1) || arr[2] != float64(255) {
		t.Errorf("value = %v", arr)
	}
}

func TestSecretKeyCreateRejectsBadBase64(t *testing.T) {
	s := newStub(t, map[string]route{})
	k := &SecretKey{Client: s.client(), Target: s.target()}
	res, _ := k.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{"appName": "a", "name": "n", "value": "!!not base64!!"}),
	})
	if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
		t.Error("malformed base64 must fail before reaching the API")
	}
	if len(s.calls) != 0 {
		t.Errorf("bad input reached the API: %+v", s.calls)
	}
}

func TestSecretKeyReadEncodesPublicKeyAndHidesMaterial(t *testing.T) {
	s := newStub(t, map[string]route{
		// public_key comes back as a JSON array of bytes.
		"GET /v1/apps/my-api/secretkeys/signing": {200, `{"name":"signing","type":"hs256","public_key":[1,2,255],"created_at":"2026-01-01T00:00:00Z"}`},
	})
	k := &SecretKey{Client: s.client(), Target: s.target()}
	res, _ := k.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeSecretKey, NativeID: "my-api/signing",
	})
	if res.ErrorCode != "" {
		t.Fatalf("ErrorCode = %q", res.ErrorCode)
	}
	props := decodeProps(t, res.Properties)
	want := base64.StdEncoding.EncodeToString([]byte{1, 2, 255})
	if props["publicKey"] != want {
		t.Errorf("publicKey = %v, want base64 %q", props["publicKey"], want)
	}
	// Private material is write-only and must never surface.
	if _, ok := props["value"]; ok {
		t.Error("key material leaked into Read output")
	}
}
