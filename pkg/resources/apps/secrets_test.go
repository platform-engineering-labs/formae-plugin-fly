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

func newSecrets(t *testing.T, routes map[string]route) (*Secrets, *stub) {
	t.Helper()
	s := newStub(t, routes)
	return &Secrets{Client: s.client(), Target: s.target()}, s
}

func TestSecretsCreateBulkSetsAndScopesNativeID(t *testing.T) {
	sec, s := newSecrets(t, map[string]route{
		"POST /v1/apps/my-api/secrets": {200, `{"version":3}`},
	})
	res, err := sec.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeSecrets,
		Properties: mustJSON(t, map[string]any{
			"appName": "my-api",
			"values":  map[string]any{"DATABASE_URL": "postgres://x", "API_KEY": "k"},
		}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v: %s", res.ProgressResult.OperationStatus, res.ProgressResult.StatusMessage)
	}
	// Not the bare app name: formae keys inventory by (target, nativeID)
	// regardless of type, so a bare name would collide with the App resource.
	if res.ProgressResult.NativeID != "my-api/secrets" {
		t.Errorf("NativeID = %q, want my-api/secrets", res.ProgressResult.NativeID)
	}
	// AppSecretsUpdateRequest is {"values": {...}} — a map, not a list of
	// {name, value} objects.
	vals, ok := s.only().Body["values"].(map[string]any)
	if !ok {
		t.Fatalf("body = %+v", s.only().Body)
	}
	if vals["DATABASE_URL"] != "postgres://x" || vals["API_KEY"] != "k" {
		t.Errorf("values = %+v", vals)
	}
}

func TestSecretsCreateRequiresAppAndValues(t *testing.T) {
	for _, props := range []map[string]any{
		{"appName": "my-api"},
		{"values": map[string]any{"A": "b"}},
		{"appName": "my-api", "values": map[string]any{}},
	} {
		sec, s := newSecrets(t, map[string]route{})
		res, _ := sec.Create(context.Background(), &resource.CreateRequest{Properties: mustJSON(t, props)})
		if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
			t.Errorf("props %+v should fail", props)
		}
		if len(s.calls) != 0 {
			t.Errorf("props %+v hit the API", props)
		}
	}
}

// Read must never reveal values: show_secrets is not requested and the
// properties carry only the app name.
func TestSecretsReadOmitsValues(t *testing.T) {
	sec, s := newSecrets(t, map[string]route{
		"GET /v1/apps/my-api/secrets": {200, `{"secrets":[
			{"name":"DATABASE_URL","digest":"abc","created_at":"2026-01-01T00:00:00Z"}]}`},
	})
	res, err := sec.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeSecrets, NativeID: "my-api/secrets",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.ErrorCode != "" {
		t.Fatalf("ErrorCode = %q", res.ErrorCode)
	}
	props := decodeProps(t, res.Properties)
	if props["appName"] != "my-api" {
		t.Errorf("appName = %v", props["appName"])
	}
	if _, ok := props["values"]; ok {
		t.Error("Read leaked values into properties")
	}
	if q := s.only().Query; q != "" {
		t.Errorf("query = %q; show_secrets must never be sent", q)
	}
}

// The endpoint stays 200 with an empty list once the last secret is gone.
// Report NotFound so formae prunes the bag instead of keeping a phantom.
func TestSecretsReadEmptyBagIsNotFound(t *testing.T) {
	sec, _ := newSecrets(t, map[string]route{
		"GET /v1/apps/my-api/secrets": {200, `{"secrets":[]}`},
	})
	res, _ := sec.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeSecrets, NativeID: "my-api/secrets",
	})
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q, want NotFound", res.ErrorCode)
	}
}

func TestSecretsReadAppGoneIsNotFound(t *testing.T) {
	sec, _ := newSecrets(t, map[string]route{
		"GET /v1/apps/dead/secrets": {400, `{"error":"could not find app dead"}`},
	})
	res, _ := sec.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeSecrets, NativeID: "dead/secrets",
	})
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q, want NotFound", res.ErrorCode)
	}
}

// Fly has no bulk secret delete, so removed names go one DELETE at a time,
// then the surviving bag is written in a single POST.
func TestSecretsUpdateRemovesThenBulkSets(t *testing.T) {
	sec, s := newSecrets(t, map[string]route{
		"DELETE /v1/apps/my-api/secrets/OLD_ONE": {200, `{}`},
		"DELETE /v1/apps/my-api/secrets/OLD_TWO": {200, `{}`},
		"POST /v1/apps/my-api/secrets":           {200, `{"version":4}`},
	})
	res, err := sec.Update(context.Background(), &resource.UpdateRequest{
		NativeID: "my-api/secrets",
		PriorProperties: mustJSON(t, SecretsProperties{AppName: "my-api", Values: map[string]string{
			"KEEP": "1", "OLD_ONE": "2", "OLD_TWO": "3",
		}}),
		DesiredProperties: mustJSON(t, SecretsProperties{AppName: "my-api", Values: map[string]string{
			"KEEP": "1", "NEW": "4",
		}}),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v: %s", res.ProgressResult.OperationStatus, res.ProgressResult.StatusMessage)
	}
	if len(s.calls) != 3 {
		t.Fatalf("want 2 deletes + 1 bulk set, got %d: %+v", len(s.calls), s.calls)
	}
	// Deletes are sorted so the request sequence is deterministic and testable.
	if s.calls[0].Path != "/v1/apps/my-api/secrets/OLD_ONE" ||
		s.calls[1].Path != "/v1/apps/my-api/secrets/OLD_TWO" {
		t.Errorf("delete order = %q, %q", s.calls[0].Path, s.calls[1].Path)
	}
	vals := s.calls[2].Body["values"].(map[string]any)
	if len(vals) != 2 || vals["NEW"] != "4" {
		t.Errorf("bulk set = %+v", vals)
	}
}

// A secret deleted out of band must not fail the update.
func TestSecretsUpdateToleratesMissingRemoval(t *testing.T) {
	sec, _ := newSecrets(t, map[string]route{
		"DELETE /v1/apps/my-api/secrets/OLD": {404, `{"error":"not found"}`},
		"POST /v1/apps/my-api/secrets":       {200, `{}`},
	})
	res, err := sec.Update(context.Background(), &resource.UpdateRequest{
		NativeID:          "my-api/secrets",
		PriorProperties:   mustJSON(t, SecretsProperties{Values: map[string]string{"OLD": "1"}}),
		DesiredProperties: mustJSON(t, SecretsProperties{Values: map[string]string{"NEW": "2"}}),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v: %s", res.ProgressResult.OperationStatus, res.ProgressResult.StatusMessage)
	}
}

func TestSecretsDeleteRemovesEveryName(t *testing.T) {
	sec, s := newSecrets(t, map[string]route{
		"GET /v1/apps/my-api/secrets":      {200, `{"secrets":[{"name":"A"},{"name":"B"}]}`},
		"DELETE /v1/apps/my-api/secrets/A": {200, `{}`},
		"DELETE /v1/apps/my-api/secrets/B": {200, `{}`},
	})
	res, err := sec.Delete(context.Background(), &resource.DeleteRequest{NativeID: "my-api/secrets"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v", res.ProgressResult.OperationStatus)
	}
	if len(s.calls) != 3 {
		t.Errorf("calls = %+v", s.calls)
	}
}

func TestSecretsDeleteOnDeadAppSucceeds(t *testing.T) {
	sec, _ := newSecrets(t, map[string]route{
		"GET /v1/apps/dead/secrets": {400, `{"error":"could not find app dead"}`},
	})
	res, _ := sec.Delete(context.Background(), &resource.DeleteRequest{NativeID: "dead/secrets"})
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v, want Success", res.ProgressResult.OperationStatus)
	}
}

// No org-wide secrets endpoint exists, so List fans out over the org's apps and
// only reports the ones that actually hold a secret.
func TestSecretsListFansOutAndSkipsEmptyApps(t *testing.T) {
	sec, s := newSecrets(t, map[string]route{
		"GET /v1/apps":               {200, `{"apps":[{"name":"one"},{"name":"two"},{"name":"three"}]}`},
		"GET /v1/apps/one/secrets":   {200, `{"secrets":[{"name":"A"}]}`},
		"GET /v1/apps/two/secrets":   {200, `{"secrets":[]}`},
		"GET /v1/apps/three/secrets": {403, `{"error":"insufficient scope"}`},
	})
	res, err := sec.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 1 || res.NativeIDs[0] != "one/secrets" {
		t.Errorf("NativeIDs = %v, want just one/secrets", res.NativeIDs)
	}
	if len(s.calls) != 4 {
		t.Errorf("calls = %d, want 1 app list + 3 secret lists", len(s.calls))
	}
}
