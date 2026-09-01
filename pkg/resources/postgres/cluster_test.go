// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"context"
	"testing"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func newCluster(t *testing.T, routes map[string]route) (*Cluster, *stub) {
	t.Helper()
	s := newStub(t, routes)
	return &Cluster{Client: s.client(), Target: s.target()}, s
}

// Every Postgres response is wrapped in a {"data": …} envelope, unlike the
// Machines endpoints. Getting that wrong yields a silently empty struct.
func TestClusterCreateIsAsyncAndUnwrapsData(t *testing.T) {
	c, s := newCluster(t, map[string]route{
		"POST /v1/postgres": {201, `{"data":{"id":"pgc_abc123","status":"creating","name":"db"}}`},
	})
	res, err := c.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeCluster,
		Properties: mustJSON(t, map[string]any{
			"name": "db", "region": "fra", "plan": "basic",
			"diskSizeGb": 10, "pgMajorVersion": "17", "poolMode": "transaction",
		}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Fatalf("status = %v, want InProgress: %s", pr.OperationStatus, pr.StatusMessage)
	}
	if pr.NativeID != "pgc_abc123" || pr.RequestID != "pgc_abc123" {
		t.Errorf("NativeID/RequestID = %q/%q", pr.NativeID, pr.RequestID)
	}
	body := s.only().Body
	// org falls back to the target when the resource omits it.
	if body["org_slug"] != "test-org" {
		t.Errorf("org_slug = %v", body["org_slug"])
	}
	for k, want := range map[string]any{
		"plan": "basic", "region": "fra", "name": "db",
		"disk_size_gb": float64(10), "pg_major_version": "17", "pool_mode": "transaction",
	} {
		if body[k] != want {
			t.Errorf("body[%q] = %v, want %v", k, body[k], want)
		}
	}
}

func TestClusterCreateRequiresPlanRegionOrg(t *testing.T) {
	// Target with no org, so the fallback cannot rescue a missing org either.
	s := newStub(t, map[string]route{})
	c := &Cluster{Client: s.client(), Target: &registry.TargetConfig{}}
	for _, props := range []map[string]any{
		{"plan": "basic", "region": "fra"}, // no org anywhere
		{"region": "fra", "org": "o"},      // no plan
		{"plan": "basic", "org": "o"},      // no region
	} {
		res, _ := c.Create(context.Background(), &resource.CreateRequest{Properties: mustJSON(t, props)})
		if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
			t.Errorf("props %+v should fail validation", props)
		}
	}
	if len(s.calls) != 0 {
		t.Errorf("validation reached the API: %+v", s.calls)
	}
}

func TestClusterReadMapsEndpoints(t *testing.T) {
	c, _ := newCluster(t, map[string]route{
		"GET /v1/postgres/pgc_abc123": {200, `{"data":{
			"id":"pgc_abc123","name":"db","region":"fra","plan":"basic","status":"ready",
			"disk_size_gb":10,"pg_major_version":"17","cpu_kind":"shared","cpus":2,
			"memory_mb":1024,"replicas":1,
			"organization":{"slug":"test-org","name":"Test"},
			"endpoints":{"primary":{
				"direct":{"host":"direct.example.com","port":5432},
				"pooler":{"host":"pooler.example.com","port":6543}}}}}`},
	})
	res, err := c.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeCluster, NativeID: "pgc_abc123",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	props := decodeProps(t, res.Properties)
	if props["org"] != "test-org" || props["status"] != "ready" || props["cpus"] != float64(2) {
		t.Errorf("props = %+v", props)
	}
	// The nested endpoints object is flattened to two named fields; a forma
	// that wants a connection host should not have to walk primary.direct.host.
	pe := props["primaryEndpoint"].(map[string]any)
	if pe["host"] != "direct.example.com" || pe["port"] != float64(5432) {
		t.Errorf("primaryEndpoint = %+v", pe)
	}
	po := props["poolerEndpoint"].(map[string]any)
	if po["host"] != "pooler.example.com" || po["port"] != float64(6543) {
		t.Errorf("poolerEndpoint = %+v", po)
	}
	// poolMode is write-only: the API never reports it, so Read must not
	// invent one.
	if _, ok := props["poolMode"]; ok {
		t.Error("poolMode leaked into Read output")
	}
}

// A cluster mid-delete must read as gone, or sync keeps resurrecting it.
func TestClusterReadDeletingIsNotFound(t *testing.T) {
	for _, st := range []string{"deleting", "deleted"} {
		c, _ := newCluster(t, map[string]route{
			"GET /v1/postgres/pgc_x": {200, `{"data":{"id":"pgc_x","status":"` + st + `"}}`},
		})
		res, _ := c.Read(context.Background(), &resource.ReadRequest{
			ResourceType: ResourceTypeCluster, NativeID: "pgc_x",
		})
		if res.ErrorCode != resource.OperationErrorCodeNotFound {
			t.Errorf("status %q -> ErrorCode %q, want NotFound", st, res.ErrorCode)
		}
	}
}

func TestClusterStatusWalk(t *testing.T) {
	tests := []struct {
		state string
		want  resource.OperationStatus
	}{
		{"creating", resource.OperationStatusInProgress},
		{"initializing", resource.OperationStatusInProgress},
		{"ready", resource.OperationStatusSuccess},
		{"failed", resource.OperationStatusFailure},
		{"deleting", resource.OperationStatusFailure},
		{"deleted", resource.OperationStatusFailure},
		// Fly can add states; an unknown one keeps polling rather than failing.
		{"resizing", resource.OperationStatusInProgress},
	}
	for _, tt := range tests {
		c, _ := newCluster(t, map[string]route{
			"GET /v1/postgres/pgc_x": {200, `{"data":{"id":"pgc_x","status":"` + tt.state + `"}}`},
		})
		res, err := c.Status(context.Background(), &resource.StatusRequest{RequestID: "pgc_x"})
		if err != nil {
			t.Fatalf("Status(%s): %v", tt.state, err)
		}
		if got := res.ProgressResult.OperationStatus; got != tt.want {
			t.Errorf("status %q -> %v, want %v", tt.state, got, tt.want)
		}
	}
}

// Managed Postgres answers 410 Gone for an already-deleted cluster. Delete has
// to stay idempotent across that, not just across 404.
func TestClusterDeleteIdempotentOn410(t *testing.T) {
	for _, code := range []int{404, 410} {
		c, _ := newCluster(t, map[string]route{
			"DELETE /v1/postgres/pgc_gone": {code, `{"error":"gone"}`},
		})
		res, _ := c.Delete(context.Background(), &resource.DeleteRequest{NativeID: "pgc_gone"})
		if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
			t.Errorf("HTTP %d on delete -> %v, want Success", code, res.ProgressResult.OperationStatus)
		}
	}
}

func TestClusterListSkipsDeleted(t *testing.T) {
	c, s := newCluster(t, map[string]route{
		"GET /v1/postgres": {200, `{"data":[
			{"id":"pgc_1","status":"ready"},
			{"id":"pgc_2","status":"deleted"},
			{"id":"pgc_3","status":"ready","deleted_at":"2026-01-01T00:00:00Z"},
			{"id":"","status":"ready"}]}`},
	})
	res, err := c.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 1 || res.NativeIDs[0] != "pgc_1" {
		t.Errorf("NativeIDs = %v, want only the live cluster", res.NativeIDs)
	}
	if q := s.only().Query; q != "org_slug=test-org" {
		t.Errorf("query = %q", q)
	}
}

func TestClusterUpdateIsNotUpdatable(t *testing.T) {
	c, s := newCluster(t, map[string]route{})
	res, _ := c.Update(context.Background(), &resource.UpdateRequest{NativeID: "pgc_x"})
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeNotUpdatable {
		t.Errorf("ErrorCode = %q", res.ProgressResult.ErrorCode)
	}
	if len(s.calls) != 0 {
		t.Error("Update hit the API")
	}
}
