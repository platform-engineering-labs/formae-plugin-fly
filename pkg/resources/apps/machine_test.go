// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apps

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func newMachine(t *testing.T, routes map[string]route) (*Machine, *stub) {
	t.Helper()
	s := newStub(t, routes)
	return &Machine{Client: s.client(), Target: s.target()}, s
}

const machineCreateResp = `{
	"id":"17811943c9d489","name":"api-1","region":"fra","state":"created",
	"private_ip":"fdaa:0:1::3",
	"config":{"image":"flyio/hellofly:latest","guest":{"cpu_kind":"shared","cpus":1,"memory_mb":256}}}`

// Create must return InProgress: the API answers with state "created" and the
// machine then pulls its image and boots.
func TestMachineCreateIsAsync(t *testing.T) {
	m, s := newMachine(t, map[string]route{
		"POST /v1/apps/my-api/machines": {200, machineCreateResp},
	})
	res, err := m.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeMachine,
		Properties: mustJSON(t, map[string]any{
			"appName": "my-api",
			"name":    "api-1",
			"image":   "flyio/hellofly:latest",
			"guest":   map[string]any{"cpuKind": "shared", "cpus": 1, "memoryMb": 256},
			"env":     map[string]any{"PORT": "8080"},
			"services": []any{map[string]any{
				"internalPort": 8080,
				"protocol":     "tcp",
				"autostart":    true,
				"autostop":     "stop",
				"ports": []any{
					map[string]any{"port": 443, "handlers": []any{"tls", "http"}, "forceHttps": false},
					map[string]any{"port": 80, "handlers": []any{"http"}, "forceHttps": true},
				},
			}},
			"mounts":     []any{map[string]any{"volume": "vol_abc", "path": "/data"}},
			"restart":    map[string]any{"policy": "on-failure", "maxRetries": 3},
			"metadata":   map[string]any{"role": "api"},
			"entrypoint": []any{"/bin/sh"},
			"cmd":        []any{"-c", "server"},
		}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pr := res.ProgressResult
	if pr.OperationStatus != resource.OperationStatusInProgress {
		t.Fatalf("status = %v, want InProgress: %s", pr.OperationStatus, pr.StatusMessage)
	}
	if pr.NativeID != "my-api/17811943c9d489" {
		t.Errorf("NativeID = %q, want app-scoped id", pr.NativeID)
	}
	// RequestID is the native id: the Machines API hands out no separate
	// operation handle.
	if pr.RequestID != pr.NativeID {
		t.Errorf("RequestID = %q, want %q", pr.RequestID, pr.NativeID)
	}

	body := s.only().Body
	if body["name"] != "api-1" {
		t.Errorf("name = %v", body["name"])
	}
	// region falls back to the target default when the resource omits it.
	if body["region"] != "fra" {
		t.Errorf("region = %v, want the target default fra", body["region"])
	}
	cfg, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("config missing: %+v", body)
	}
	if cfg["image"] != "flyio/hellofly:latest" {
		t.Errorf("image = %v", cfg["image"])
	}
	guest := cfg["guest"].(map[string]any)
	if guest["cpu_kind"] != "shared" || guest["memory_mb"] != float64(256) {
		t.Errorf("guest = %+v, want snake_case keys", guest)
	}
	svc := cfg["services"].([]any)[0].(map[string]any)
	if svc["internal_port"] != float64(8080) {
		t.Errorf("service = %+v, want internal_port", svc)
	}
	if svc["autostop"] != "stop" || svc["autostart"] != true {
		t.Errorf("autostop/autostart = %v/%v", svc["autostop"], svc["autostart"])
	}
	p0 := svc["ports"].([]any)[0].(map[string]any)
	if p0["port"] != float64(443) || p0["force_https"] != false {
		t.Errorf("port = %+v", p0)
	}
	mnt := cfg["mounts"].([]any)[0].(map[string]any)
	if mnt["path"] != "/data" || mnt["volume"] != "vol_abc" {
		t.Errorf("mount = %+v", mnt)
	}
	if r := cfg["restart"].(map[string]any); r["policy"] != "on-failure" || r["max_retries"] != float64(3) {
		t.Errorf("restart = %+v", r)
	}
	init := cfg["init"].(map[string]any)
	if len(init["cmd"].([]any)) != 2 || init["entrypoint"].([]any)[0] != "/bin/sh" {
		t.Errorf("init = %+v", init)
	}
	if cfg["env"].(map[string]any)["PORT"] != "8080" {
		t.Errorf("env = %+v", cfg["env"])
	}
}

func TestMachineCreateRequiresAppAndImage(t *testing.T) {
	for _, props := range []map[string]any{
		{"image": "x"},
		{"appName": "my-api"},
	} {
		m, s := newMachine(t, map[string]route{})
		res, err := m.Create(context.Background(), &resource.CreateRequest{Properties: mustJSON(t, props)})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
			t.Errorf("props %+v should fail validation", props)
		}
		if len(s.calls) != 0 {
			t.Errorf("props %+v hit the API", props)
		}
	}
}

func TestMachineReadMapsConfigBack(t *testing.T) {
	m, _ := newMachine(t, map[string]route{
		"GET /v1/apps/my-api/machines/17811943c9d489": {200, `{
			"id":"17811943c9d489","name":"api-1","region":"fra","state":"started",
			"private_ip":"fdaa:0:1::3",
			"config":{
				"image":"flyio/hellofly:latest",
				"env":{"PORT":"8080"},
				"auto_destroy":false,
				"guest":{"cpu_kind":"shared","cpus":1,"memory_mb":256},
				"metadata":{"role":"api"},
				"restart":{"policy":"on-failure","max_retries":3},
				"init":{"cmd":["-c","server"],"entrypoint":["/bin/sh"]},
				"mounts":[{"volume":"vol_abc","path":"/data","name":"data"}],
				"services":[{"internal_port":8080,"protocol":"tcp","autostart":true,"autostop":"stop",
					"ports":[{"port":443,"handlers":["tls","http"],"force_https":true}]}]}}`},
	})
	res, err := m.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeMachine, NativeID: "my-api/17811943c9d489",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if res.ErrorCode != "" {
		t.Fatalf("ErrorCode = %q", res.ErrorCode)
	}
	props := decodeProps(t, res.Properties)
	if props["appName"] != "my-api" {
		t.Errorf("appName = %v — Read must reconstruct it from the native id, the API body has no app name", props["appName"])
	}
	for k, want := range map[string]any{
		"id":        "17811943c9d489",
		"name":      "api-1",
		"region":    "fra",
		"state":     "started",
		"privateIp": "fdaa:0:1::3",
		"image":     "flyio/hellofly:latest",
	} {
		if props[k] != want {
			t.Errorf("props[%q] = %v, want %v", k, props[k], want)
		}
	}
	guest := props["guest"].(map[string]any)
	if guest["cpuKind"] != "shared" || guest["memoryMb"] != float64(256) {
		t.Errorf("guest = %+v, want camelCase keys back", guest)
	}
	svc := props["services"].([]any)[0].(map[string]any)
	if svc["internalPort"] != float64(8080) {
		t.Errorf("service = %+v", svc)
	}
	if svc["ports"].([]any)[0].(map[string]any)["forceHttps"] != true {
		t.Errorf("port = %+v", svc["ports"])
	}
	if props["mounts"].([]any)[0].(map[string]any)["path"] != "/data" {
		t.Errorf("mounts = %+v", props["mounts"])
	}
	if props["restart"].(map[string]any)["maxRetries"] != float64(3) {
		t.Errorf("restart = %+v", props["restart"])
	}
	if props["cmd"].([]any)[1] != "server" || props["entrypoint"].([]any)[0] != "/bin/sh" {
		t.Errorf("cmd/entrypoint = %v/%v", props["cmd"], props["entrypoint"])
	}
}

func TestMachineReadRejectsBareNativeID(t *testing.T) {
	m, s := newMachine(t, map[string]route{})
	res, _ := m.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeMachine, NativeID: "17811943c9d489",
	})
	if res.ErrorCode != resource.OperationErrorCodeInvalidRequest {
		t.Errorf("ErrorCode = %q, want InvalidRequest", res.ErrorCode)
	}
	if len(s.calls) != 0 {
		t.Error("hit the API with an unparseable native id")
	}
}

// Flaps reports a machine in a destroyed app as HTTP 400, not 404. Read must
// still say NotFound or the resource never leaves the inventory.
func TestMachineReadTreats400AppGoneAsNotFound(t *testing.T) {
	m, _ := newMachine(t, map[string]route{
		"GET /v1/apps/dead-app/machines/abc": {400, `{"error":"could not find app dead-app"}`},
	})
	res, _ := m.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeMachine, NativeID: "dead-app/abc",
	})
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q, want NotFound", res.ErrorCode)
	}
}

func TestMachineUpdateIsAsyncAndSendsWholeConfig(t *testing.T) {
	m, s := newMachine(t, map[string]route{
		"POST /v1/apps/my-api/machines/abc": {200, machineCreateResp},
	})
	res, err := m.Update(context.Background(), &resource.UpdateRequest{
		NativeID: "my-api/abc",
		DesiredProperties: mustJSON(t, map[string]any{
			"appName": "my-api",
			"name":    "api-1",
			"image":   "flyio/hellofly:v2",
			"env":     map[string]any{"PORT": "9090"},
		}),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusInProgress {
		t.Fatalf("status = %v, want InProgress: %s",
			res.ProgressResult.OperationStatus, res.ProgressResult.StatusMessage)
	}
	body := s.only().Body
	cfg := body["config"].(map[string]any)
	if cfg["image"] != "flyio/hellofly:v2" {
		t.Errorf("image = %v", cfg["image"])
	}
	// Update replaces the machine config wholesale, so region must not be sent
	// — it is createOnly and formae plans a replacement for a region change.
	if _, ok := body["region"]; ok {
		t.Errorf("update sent region: %+v", body)
	}
}

func TestMachineDeleteForces(t *testing.T) {
	m, s := newMachine(t, map[string]route{
		"DELETE /v1/apps/my-api/machines/abc": {200, `{"ok":true}`},
	})
	res, err := m.Delete(context.Background(), &resource.DeleteRequest{NativeID: "my-api/abc"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v", res.ProgressResult.OperationStatus)
	}
	// A running machine cannot be destroyed without force, and "destroy" is
	// exactly what a formae delete means.
	if q := s.only().Query; q != "force=true" {
		t.Errorf("query = %q, want force=true", q)
	}
}

func TestMachineDeleteIdempotent(t *testing.T) {
	m, _ := newMachine(t, map[string]route{
		"DELETE /v1/apps/my-api/machines/gone": {404, `{"error":"machine not found"}`},
	})
	res, _ := m.Delete(context.Background(), &resource.DeleteRequest{NativeID: "my-api/gone"})
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v, want Success", res.ProgressResult.OperationStatus)
	}
}

func TestMachineStatusMapsState(t *testing.T) {
	tests := []struct {
		state string
		want  resource.OperationStatus
	}{
		{"created", resource.OperationStatusInProgress},
		{"starting", resource.OperationStatusInProgress},
		{"replacing", resource.OperationStatusInProgress},
		{"stopping", resource.OperationStatusInProgress},
		{"started", resource.OperationStatusSuccess},
		// A machine asked to stop (autostop, or a stopped-by-design worker)
		// has arrived at a legitimate resting state.
		{"stopped", resource.OperationStatusSuccess},
		{"suspended", resource.OperationStatusSuccess},
		{"failed", resource.OperationStatusFailure},
		{"destroyed", resource.OperationStatusFailure},
		{"destroying", resource.OperationStatusFailure},
	}
	for _, tt := range tests {
		m, _ := newMachine(t, map[string]route{
			"GET /v1/apps/my-api/machines/abc": {200, `{"id":"abc","state":"` + tt.state + `"}`},
		})
		res, err := m.Status(context.Background(), &resource.StatusRequest{RequestID: "my-api/abc"})
		if err != nil {
			t.Fatalf("Status(%s): %v", tt.state, err)
		}
		if got := res.ProgressResult.OperationStatus; got != tt.want {
			t.Errorf("state %q -> %v, want %v", tt.state, got, tt.want)
		}
	}
}

// Create is synchronous on the API side; if the machine has vanished by the
// time we poll, the create did not stick.
func TestMachineStatusNotFoundIsFailure(t *testing.T) {
	m, _ := newMachine(t, map[string]route{
		"GET /v1/apps/my-api/machines/abc": {404, `{"error":"not found"}`},
	})
	res, _ := m.Status(context.Background(), &resource.StatusRequest{RequestID: "my-api/abc"})
	if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
		t.Errorf("status = %v, want Failure", res.ProgressResult.OperationStatus)
	}
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q", res.ProgressResult.ErrorCode)
	}
}

func TestMachineStatusFallsBackToNativeID(t *testing.T) {
	m, _ := newMachine(t, map[string]route{
		"GET /v1/apps/my-api/machines/abc": {200, `{"id":"abc","state":"started"}`},
	})
	res, err := m.Status(context.Background(), &resource.StatusRequest{NativeID: "my-api/abc"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v", res.ProgressResult.OperationStatus)
	}
}

// Discovery uses the org-wide endpoint: one call instead of one per app, which
// is what keeps a large org inside the conformance harness's window.
// Discovery fans out over the org's apps rather than using
// GET /v1/orgs/{org}/machines. Fly documents that endpoint as "a point in time"
// whose "recent machine changes, including creations and destructions, may take
// time to propagate" — a machine created seconds ago is routinely absent from
// it, which made every discovery run miss freshly created machines. The per-app
// endpoint is the authoritative list and is immediately consistent, which is
// why Read against it has always worked.
func TestMachineListFansOutOverApps(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/apps":              {200, `{"apps":[{"name":"one"},{"name":"two"}]}`},
		"GET /v1/apps/one/machines": {200, `[{"id":"m1"},{"id":"m2"}]`},
		"GET /v1/apps/two/machines": {200, `[{"id":"m3"}]`},
	})
	m := &Machine{Client: s.client(), Target: s.target()}
	res, err := m.List(context.Background(), &resource.ListRequest{ResourceType: ResourceTypeMachine})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"one/m1", "one/m2", "two/m3"}
	if len(res.NativeIDs) != len(want) {
		t.Fatalf("NativeIDs = %v, want %v", res.NativeIDs, want)
	}
	for i, id := range want {
		if res.NativeIDs[i] != id {
			t.Errorf("NativeIDs[%d] = %q, want %q", i, res.NativeIDs[i], id)
		}
	}
	// The org-wide index is not consulted at all.
	for _, c := range s.calls {
		if strings.Contains(c.Path, "/orgs/") {
			t.Errorf("called %s; the org-wide machine index is not trustworthy for discovery", c.Path)
		}
	}
	// Discovery only needs ids, so the machine config is left on the wire.
	for _, c := range s.calls {
		if strings.HasSuffix(c.Path, "/machines") && c.Query != "summary=true" {
			t.Errorf("%s query = %q, want summary=true", c.Path, c.Query)
		}
	}
}

// One app the token cannot read must not blank the whole scan.
func TestMachineListSkipsUnreadableApps(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/apps":                 {200, `{"apps":[{"name":"one"},{"name":"denied"}]}`},
		"GET /v1/apps/one/machines":    {200, `[{"id":"m1"}]`},
		"GET /v1/apps/denied/machines": {403, `{"error":"insufficient scope"}`},
	})
	m := &Machine{Client: s.client(), Target: s.target()}
	res, err := m.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 1 || res.NativeIDs[0] != "one/m1" {
		t.Errorf("NativeIDs = %v, want just one/m1", res.NativeIDs)
	}
}

// A machine row without an id is unusable as a native id.
func TestMachineListSkipsRowsWithoutAnID(t *testing.T) {
	s := newStub(t, map[string]route{
		"GET /v1/apps":              {200, `{"apps":[{"name":"one"}]}`},
		"GET /v1/apps/one/machines": {200, `[{"id":""},{"id":"m2"}]`},
	})
	m := &Machine{Client: s.client(), Target: s.target()}
	res, _ := m.List(context.Background(), &resource.ListRequest{})
	if len(res.NativeIDs) != 1 || res.NativeIDs[0] != "one/m2" {
		t.Errorf("NativeIDs = %v, want just one/m2", res.NativeIDs)
	}
}

func TestMachineListWithoutOrgIsEmpty(t *testing.T) {
	s := newStub(t, map[string]route{})
	m := &Machine{Client: s.client(), Target: &registry.TargetConfig{}}
	res, err := m.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 0 || len(s.calls) != 0 {
		t.Errorf("NativeIDs = %v, calls = %d", res.NativeIDs, len(s.calls))
	}
}

// Machine's Create returns InProgress, so Status is where formae first learns
// the machine's id, state and private IP. Without them `machine.res.privateIp`
// and friends are unresolvable.
func TestMachineStatusStartedCarriesProperties(t *testing.T) {
	m, _ := newMachine(t, map[string]route{
		"GET /v1/apps/my-api/machines/abc": {200, `{"id":"abc","name":"api-1","region":"fra",
			"state":"started","private_ip":"fdaa:0:1::3","config":{"image":"img"}}`},
	})
	res, err := m.Status(context.Background(), &resource.StatusRequest{RequestID: "my-api/abc"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	raw := res.ProgressResult.ResourceProperties
	if len(raw) == 0 {
		t.Fatal("started Status returned no properties; machine resolvables would never resolve")
	}
	props := decodeProps(t, string(raw))
	if props["id"] != "abc" || props["privateIp"] != "fdaa:0:1::3" || props["appName"] != "my-api" {
		t.Errorf("props = %+v", props)
	}
}

func TestMachineAutostopResponsesPreservePolicyAcrossOperations(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{`false`, "off"}, {`true`, "stop"}, {`"off"`, "off"},
		{`"stop"`, "stop"}, {`"suspend"`, "suspend"}, {`null`, ""},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			body := `{"id":"abc","state":"started","config":{"image":"image:v1","services":[{"internal_port":8787,"autostop":` + tt.raw + `}]}}`
			m, stub := newMachine(t, map[string]route{
				"GET /v1/apps/my-api/machines/abc":  {200, body},
				"POST /v1/apps/my-api/machines/abc": {200, body},
				"POST /v1/apps/my-api/machines":     {200, body},
			})
			props := mustJSON(t, map[string]any{"appName": "my-api", "image": "image:v1", "services": []any{map[string]any{"internalPort": 8787, "autostop": tt.want}}})
			created, err := m.Create(context.Background(), &resource.CreateRequest{Properties: props})
			if err != nil || created.ProgressResult.OperationStatus != resource.OperationStatusInProgress {
				t.Fatalf("Create: %v, %+v", err, created)
			}
			updated, err := m.Update(context.Background(), &resource.UpdateRequest{NativeID: "my-api/abc", DesiredProperties: props})
			if err != nil || updated.ProgressResult.OperationStatus != resource.OperationStatusInProgress {
				t.Fatalf("Update: %v, %+v", err, updated)
			}
			for _, call := range stub.calls {
				service := call.Body["config"].(map[string]any)["services"].([]any)[0].(map[string]any)
				value, exists := service["autostop"]
				if tt.want == "" {
					if exists {
						t.Fatalf("absent policy sent as %v", value)
					}
				} else if value != tt.want {
					t.Fatalf("outbound policy = %v, want %q", value, tt.want)
				}
			}
			read, err := m.Read(context.Background(), &resource.ReadRequest{NativeID: "my-api/abc"})
			if err != nil || read.ErrorCode != "" {
				t.Fatalf("Read: %v, %+v", err, read)
			}
			status, err := m.Status(context.Background(), &resource.StatusRequest{RequestID: "my-api/abc"})
			if err != nil || status.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
				t.Fatalf("Status: %v, %+v", err, status)
			}
			var got MachineProperties
			if err := json.Unmarshal([]byte(read.Properties), &got); err != nil {
				t.Fatal(err)
			}
			if got.Services[0].Autostop != tt.want {
				t.Fatalf("policy = %q, want %q", got.Services[0].Autostop, tt.want)
			}
		})
	}
}

func TestMachineAutostopRejectsUnexpectedJSONTypes(t *testing.T) {
	for _, raw := range []string{`123`, `{}`, `[]`} {
		var response machineAPI
		if err := json.Unmarshal([]byte(`{"config":{"services":[{"autostop":`+raw+`}]}}`), &response); err == nil {
			t.Errorf("accepted autostop %s", raw)
		}
	}
}
