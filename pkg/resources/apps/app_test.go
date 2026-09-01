// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apps

import (
	"context"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func newApp(t *testing.T, routes map[string]route) (*App, *stub) {
	t.Helper()
	s := newStub(t, routes)
	return &App{Client: s.client(), Target: s.target()}, s
}

func TestAppCreateIsSyncAndUsesRequestedNameAsNativeID(t *testing.T) {
	a, s := newApp(t, map[string]route{
		// Observed live response. The OpenAPI spec claims
		// CreateAppResponse{token}; the API actually returns {id, created_at}
		// and echoes neither name nor org, so the native id has to be the name
		// we sent.
		"POST /v1/apps": {201, `{"id":"pxovqy22k4ey1j2k","created_at":1788245238000}`},
	})
	res, err := a.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeApp,
		Properties: mustJSON(t, map[string]any{
			"name": "formae-test-app",
			"org":  "my-org",
		}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := res.ProgressResult.OperationStatus; got != resource.OperationStatusSuccess {
		t.Fatalf("status = %v: %s", got, res.ProgressResult.StatusMessage)
	}
	if res.ProgressResult.NativeID != "formae-test-app" {
		t.Errorf("NativeID = %q, want the app name", res.ProgressResult.NativeID)
	}
	c := s.only()
	if c.Body["app_name"] != nil {
		t.Errorf("body carries app_name; the spec's CreateAppRequest field is name: %+v", c.Body)
	}
	if c.Body["name"] != "formae-test-app" || c.Body["org_slug"] != "my-org" {
		t.Errorf("body = %+v", c.Body)
	}
}

// A deploy token, or a name someone else already took, both come back 422.
func TestAppCreateClassifies422AsInvalidRequest(t *testing.T) {
	a, _ := newApp(t, map[string]route{
		"POST /v1/apps": {422, `{"error":"name has already been taken"}`},
	})
	res, err := a.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{"name": "taken", "org": "my-org"}),
	})
	if err != nil {
		t.Fatalf("Create returned a hard error, want it on the result: %v", err)
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode = %q", res.ProgressResult.ErrorCode)
	}
}

func TestAppCreateRequiresNameAndOrg(t *testing.T) {
	for _, props := range []map[string]any{
		{"org": "my-org"},
		{"name": "x"},
		{},
	} {
		// No routes: a validation failure must not reach the network.
		a, s := newApp(t, map[string]route{})
		res, err := a.Create(context.Background(), &resource.CreateRequest{Properties: mustJSON(t, props)})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
			t.Errorf("props %+v should fail validation", props)
		}
		if len(s.calls) != 0 {
			t.Errorf("props %+v hit the API before validating", props)
		}
	}
}

func TestAppReadMapsOrgSlug(t *testing.T) {
	a, _ := newApp(t, map[string]route{
		"GET /v1/apps/formae-test-app": {200, `{
			"id":"z4x9mn2p","name":"formae-test-app","status":"deployed",
			"network":"formae-test-app","machine_count":2,
			"organization":{"slug":"my-org","name":"My Org"}}`},
	})
	res, err := a.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeApp, NativeID: "formae-test-app",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.ErrorCode != "" {
		t.Fatalf("ErrorCode = %q", res.ErrorCode)
	}
	props := decodeProps(t, res.Properties)
	for k, want := range map[string]any{
		"name":    "formae-test-app",
		"org":     "my-org",
		"id":      "z4x9mn2p",
		"status":  "deployed",
		"network": "formae-test-app",
	} {
		if props[k] != want {
			t.Errorf("props[%q] = %v, want %v", k, props[k], want)
		}
	}
	// machine_count and volume_count are noise: they change with the app's
	// contents and would surface as drift on a field nobody declared.
	if _, ok := props["machineCount"]; ok {
		t.Error("machineCount leaked into properties")
	}
}

func TestAppReadNotFound(t *testing.T) {
	a, _ := newApp(t, map[string]route{
		"GET /v1/apps/gone": {404, `{"error":"Not Found"}`},
	})
	res, _ := a.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeApp, NativeID: "gone",
	})
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q, want NotFound", res.ErrorCode)
	}
}

// The API has no app-update endpoint. Report NotUpdatable rather than
// pretending an update happened — the schema marks every field createOnly, so
// formae should be planning a replacement and this path is a backstop.
func TestAppUpdateIsNotUpdatable(t *testing.T) {
	a, s := newApp(t, map[string]route{})
	res, err := a.Update(context.Background(), &resource.UpdateRequest{NativeID: "x"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeNotUpdatable {
		t.Errorf("ErrorCode = %q, want NotUpdatable", res.ProgressResult.ErrorCode)
	}
	if len(s.calls) != 0 {
		t.Error("Update hit the API")
	}
}

func TestAppDeleteIsIdempotent(t *testing.T) {
	// Already gone: 404 on delete is success.
	a, _ := newApp(t, map[string]route{
		"DELETE /v1/apps/gone": {404, `{"error":"Not Found"}`},
	})
	res, err := a.Delete(context.Background(), &resource.DeleteRequest{NativeID: "gone"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v, want Success on a missing app", res.ProgressResult.OperationStatus)
	}
}

func TestAppDelete(t *testing.T) {
	a, s := newApp(t, map[string]route{
		"DELETE /v1/apps/formae-test-app": {202, ``},
	})
	res, err := a.Delete(context.Background(), &resource.DeleteRequest{NativeID: "formae-test-app"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v: %s", res.ProgressResult.OperationStatus, res.ProgressResult.StatusMessage)
	}
	if c := s.only(); c.Method != "DELETE" || c.Path != "/v1/apps/formae-test-app" {
		t.Errorf("call = %+v", c)
	}
}

func TestAppListScopesToTargetOrg(t *testing.T) {
	a, s := newApp(t, map[string]route{
		"GET /v1/apps": {200, `{"total_apps":2,"apps":[
			{"name":"one","id":"a"},{"name":"two","id":"b"},{"name":"","id":"c"}]}`},
	})
	res, err := a.List(context.Background(), &resource.ListRequest{ResourceType: ResourceTypeApp})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 2 || res.NativeIDs[0] != "one" || res.NativeIDs[1] != "two" {
		t.Errorf("NativeIDs = %v, want the two named apps", res.NativeIDs)
	}
	// GET /v1/apps requires org_slug; without it the API 404s.
	if c := s.only(); c.Query != "org_slug=test-org" {
		t.Errorf("query = %q, want org_slug=test-org", c.Query)
	}
}

// Discovery must not explode a whole sync because one org is unreadable.
func TestAppListSwallowsErrors(t *testing.T) {
	a, _ := newApp(t, map[string]route{
		"GET /v1/apps": {403, `{"error":"insufficient scope"}`},
	})
	res, err := a.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List returned a hard error: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("NativeIDs = %v, want empty", res.NativeIDs)
	}
}

func TestAppListWithoutOrgFails(t *testing.T) {
	s := newStub(t, map[string]route{})
	a := &App{Client: s.client(), Target: &registry.TargetConfig{}}
	res, err := a.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 0 {
		t.Errorf("NativeIDs = %v", res.NativeIDs)
	}
	if len(s.calls) != 0 {
		t.Error("List called the API without an org")
	}
}

// App create/delete are synchronous, so Status should never be reached — but
// the interface requires it, and reporting Success is the honest answer for an
// operation that already settled.
func TestAppStatusIsSuccess(t *testing.T) {
	a, _ := newApp(t, map[string]route{})
	res, err := a.Status(context.Background(), &resource.StatusRequest{NativeID: "x"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v", res.ProgressResult.OperationStatus)
	}
}

// Fly accepts org_slug=personal on create and then reports the organization's
// real slug on read. `org` is createOnly, so letting this through means formae
// diffs "personal" against the real slug and plans a replacement on every
// reconcile, forever. Verified against the live API 2026-09-01: creating with
// org_slug=personal read back as organization.slug="nico-axtmann".
func TestAppCreateRejectsPersonalOrgAlias(t *testing.T) {
	a, s := newApp(t, map[string]route{
		"GET /v1/tokens/current": {200, `{"tokens":[{"org_slug":"real-slug","organization":"Real Name"}]}`},
	})
	res, err := a.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeApp,
		Properties:   mustJSON(t, map[string]any{"name": "x", "org": "personal"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
		t.Fatal("org=personal must be refused; it would replace-loop forever")
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode = %q", res.ProgressResult.ErrorCode)
	}
	// The message must name the slug the user should have written, or it just
	// tells them they are wrong without saying what is right.
	if !strings.Contains(res.ProgressResult.StatusMessage, "real-slug") {
		t.Errorf("message should name the real slug, got: %s", res.ProgressResult.StatusMessage)
	}
	// Case-insensitive, and it must not reach POST /v1/apps.
	for _, c := range s.calls {
		if c.Method == "POST" {
			t.Errorf("create reached the API: %+v", c)
		}
	}
}

func TestAppCreateRejectsPersonalAliasCaseInsensitively(t *testing.T) {
	for _, alias := range []string{"Personal", "PERSONAL"} {
		// The slug lookup is best-effort: when it fails, the rejection still has
		// to happen, just with a less helpful message.
		a, s := newApp(t, map[string]route{
			"GET /v1/tokens/current": {500, `{"error":"boom"}`},
		})
		res, _ := a.Create(context.Background(), &resource.CreateRequest{
			Properties: mustJSON(t, map[string]any{"name": "x", "org": alias}),
		})
		if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
			t.Errorf("org=%q should be refused", alias)
		}
		for _, c := range s.calls {
			if c.Method == "POST" {
				t.Errorf("org=%q reached the API", alias)
			}
		}
	}
}
