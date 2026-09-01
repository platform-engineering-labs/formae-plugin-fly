// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package postgres

import (
	"context"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// --- Database ---------------------------------------------------------------

func TestDatabaseCreateAndScopedNativeID(t *testing.T) {
	s := newStub(t, map[string]route{
		"POST /v1/postgres/pgc_1/databases": {200, `{"data":{"name":"appdb"}}`},
	})
	d := &Database{Client: s.client(), Target: s.target()}
	res, err := d.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeDatabase,
		Properties:   mustJSON(t, map[string]any{"clusterId": "pgc_1", "name": "appdb"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.NativeID != "pgc_1/appdb" {
		t.Errorf("NativeID = %q", res.ProgressResult.NativeID)
	}
	if s.only().Body["name"] != "appdb" {
		t.Errorf("body = %+v", s.only().Body)
	}
}

// There is no per-database GET, so Read scans the cluster's list.
func TestDatabaseReadScansList(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/postgres/pgc_1/databases": {200, `{"data":[{"name":"postgres"},{"name":"appdb"}]}`},
	})
	d := &Database{Client: s.client(), Target: s.target()}
	res, _ := d.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeDatabase, NativeID: "pgc_1/appdb",
	})
	if res.ErrorCode != "" {
		t.Fatalf("ErrorCode = %q", res.ErrorCode)
	}
	props := decodeProps(t, res.Properties)
	if props["clusterId"] != "pgc_1" || props["name"] != "appdb" {
		t.Errorf("props = %+v", props)
	}
}

func TestDatabaseReadMissingIsNotFound(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/postgres/pgc_1/databases": {200, `{"data":[{"name":"postgres"}]}`},
	})
	d := &Database{Client: s.client(), Target: s.target()}
	res, _ := d.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeDatabase, NativeID: "pgc_1/gone",
	})
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q, want NotFound", res.ErrorCode)
	}
}

// --- User -------------------------------------------------------------------

func TestUserCreateAndRoleUpdate(t *testing.T) {
	s := newStub(t, map[string]route{
		"POST /v1/postgres/pgc_1/users":      {200, `{"data":{"username":"app","role":"writer"}}`},
		"PATCH /v1/postgres/pgc_1/users/app": {204, ``},
	})
	u := &User{Client: s.client(), Target: s.target()}
	res, err := u.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{"clusterId": "pgc_1", "username": "app", "role": "writer"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.NativeID != "pgc_1/app" {
		t.Errorf("NativeID = %q", res.ProgressResult.NativeID)
	}

	upd, err := u.Update(context.Background(), &resource.UpdateRequest{
		NativeID:          "pgc_1/app",
		DesiredProperties: mustJSON(t, UserProperties{ClusterID: "pgc_1", Username: "app", Role: "reader"}),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("update status = %v", upd.ProgressResult.OperationStatus)
	}
	// Role is the only mutable field, so the PATCH body carries only it.
	last := s.calls[len(s.calls)-1]
	if last.Method != "PATCH" || last.Body["role"] != "reader" || len(last.Body) != 1 {
		t.Errorf("patch call = %+v", last)
	}
}

func TestUserReadReportsRole(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/postgres/pgc_1/users": {200, `{"data":[{"username":"app","role":"writer"}]}`},
	})
	u := &User{Client: s.client(), Target: s.target()}
	res, _ := u.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeUser, NativeID: "pgc_1/app",
	})
	props := decodeProps(t, res.Properties)
	if props["role"] != "writer" || props["username"] != "app" {
		t.Errorf("props = %+v", props)
	}
	// The password must never appear: the plugin does not call the credentials
	// endpoint that would reveal it.
	if _, ok := props["password"]; ok {
		t.Error("password leaked into Read output")
	}
}

// --- Attachment -------------------------------------------------------------

func TestAttachmentCreateAndReadViaAttachedApps(t *testing.T) {
	s := newStub(t, map[string]route{
		"POST /v1/postgres/pgc_1/attachments": {200, `{"data":{"app_name":"my-api","postgres_cluster_id":"pgc_1"}}`},
		"GET /v1/postgres/pgc_1":              {200, `{"data":{"id":"pgc_1","attached_apps":[{"name":"my-api"}]}}`},
	})
	a := &Attachment{Client: s.client(), Target: s.target()}
	res, err := a.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{"clusterId": "pgc_1", "appName": "my-api"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.NativeID != "pgc_1/my-api" {
		t.Errorf("NativeID = %q", res.ProgressResult.NativeID)
	}
	// There is no per-attachment GET; existence is read off the cluster.
	rd, _ := a.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeAttachment, NativeID: "pgc_1/my-api",
	})
	if rd.ErrorCode != "" {
		t.Fatalf("ErrorCode = %q", rd.ErrorCode)
	}
	if decodeProps(t, rd.Properties)["appName"] != "my-api" {
		t.Errorf("props = %s", rd.Properties)
	}
}

func TestAttachmentDeleteIdempotentOn410(t *testing.T) {
	s := newStub(t, map[string]route{
		"DELETE /v1/postgres/pgc_1/attachments/my-api": {410, `{"error":"gone"}`},
	})
	a := &Attachment{Client: s.client(), Target: s.target()}
	res, _ := a.Delete(context.Background(), &resource.DeleteRequest{NativeID: "pgc_1/my-api"})
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v, want Success on 410", res.ProgressResult.OperationStatus)
	}
}

// --- Extension --------------------------------------------------------------

// The list endpoint returns the whole catalogue with installed==null for the
// ones that are off. Present-but-not-installed must read as NotFound.
func TestExtensionReadInstalledVsAvailable(t *testing.T) {
	body := `{"data":[
		{"name":"citext","system":false,"installed":{"schema":"public","version":"1.6"}},
		{"name":"postgis","system":false,"installed":null}]}`
	s := newStub(t, map[string]route{
		"GET /v1/postgres/pgc_1/databases/appdb/extensions": {200, body},
	})
	e := &Extension{Client: s.client(), Target: s.target()}

	on, _ := e.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeExtension, NativeID: "pgc_1/appdb/citext",
	})
	if on.ErrorCode != "" {
		t.Fatalf("installed extension ErrorCode = %q", on.ErrorCode)
	}
	props := decodeProps(t, on.Properties)
	if props["version"] != "1.6" || props["schema"] != "public" || props["databaseName"] != "appdb" {
		t.Errorf("props = %+v", props)
	}

	off, _ := e.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeExtension, NativeID: "pgc_1/appdb/postgis",
	})
	if off.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("available-but-not-installed = %q, want NotFound", off.ErrorCode)
	}
}

func TestExtensionCreateUsesThreePartID(t *testing.T) {
	s := newStub(t, map[string]route{
		"POST /v1/postgres/pgc_1/databases/appdb/extensions": {204, ``},
	})
	e := &Extension{Client: s.client(), Target: s.target()}
	res, err := e.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{
			"clusterId": "pgc_1", "databaseName": "appdb", "name": "citext",
			"schema": "public", "createSchema": true,
		}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.NativeID != "pgc_1/appdb/citext" {
		t.Errorf("NativeID = %q", res.ProgressResult.NativeID)
	}
	b := s.only().Body
	if b["name"] != "citext" || b["schema"] != "public" || b["create_schema"] != true {
		t.Errorf("body = %+v", b)
	}
}

// Discovery must report only installed extensions — the catalogue is hundreds
// of rows per database and would flood the inventory with phantoms.
func TestExtensionListOnlyInstalled(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/postgres":                                  {200, `{"data":[{"id":"pgc_1","status":"ready"}]}`},
		"GET /v1/postgres/pgc_1/databases":                  {200, `{"data":[{"name":"appdb"}]}`},
		"GET /v1/postgres/pgc_1/databases/appdb/extensions": {200, `{"data":[{"name":"citext","installed":{"version":"1.6"}},{"name":"postgis","installed":null}]}`},
	})
	e := &Extension{Client: s.client(), Target: s.target()}
	res, err := e.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 1 || res.NativeIDs[0] != "pgc_1/appdb/citext" {
		t.Errorf("NativeIDs = %v", res.NativeIDs)
	}
}

// --- Backup -----------------------------------------------------------------

// The POST answers 202 without naming the new backup, so Create diffs the
// listing before and after to recover the id.
func TestBackupCreateRecoversIDByDiffing(t *testing.T) {
	s := newStub(t, map[string]route{
		"POST /v1/postgres/pgc_1/backups": {202, ``},
	})
	getCount := 0
	s.handler = func(method, path string) (int, string, bool) {
		if method != "GET" || path != "/v1/postgres/pgc_1/backups" {
			return 0, "", false
		}
		getCount++
		if getCount == 1 {
			return 200, `{"data":[{"id":"bk_old","status":"completed"}]}`, true
		}
		return 200, `{"data":[{"id":"bk_old","status":"completed"},{"id":"bk_new","status":"running"}]}`, true
	}
	b := &Backup{Client: s.client(), Target: s.target()}
	res, err := b.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{"clusterId": "pgc_1", "backupType": "full"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.NativeID != "pgc_1/bk_new" {
		t.Errorf("NativeID = %q, want the newly appeared backup", res.ProgressResult.NativeID)
	}
}

// Fly has no delete endpoint for backups. Reporting failure would wedge every
// destroy; reporting bare success would hide the truth. So: success, and say so.
func TestBackupDeleteIsSuccessButSaysItCannotDelete(t *testing.T) {
	s := newStub(t, map[string]route{})
	b := &Backup{Client: s.client(), Target: s.target()}
	res, err := b.Delete(context.Background(), &resource.DeleteRequest{NativeID: "pgc_1/bk_1"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatal("delete must succeed or every destroy containing a backup wedges")
	}
	if res.ProgressResult.StatusMessage == "" {
		t.Error("delete must explain that the backup was not actually removed")
	}
	if len(s.calls) != 0 {
		t.Errorf("delete called the API even though no endpoint exists: %+v", s.calls)
	}
}

// Aged out under the retention policy.
func TestBackupReadGoneIsNotFound(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/postgres/pgc_1/backups": {200, `{"data":[{"id":"bk_other"}]}`},
	})
	b := &Backup{Client: s.client(), Target: s.target()}
	res, _ := b.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeBackup, NativeID: "pgc_1/bk_1",
	})
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q, want NotFound", res.ErrorCode)
	}
}
