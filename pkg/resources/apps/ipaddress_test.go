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

func newIP(t *testing.T, routes map[string]route) (*IPAddress, *stub) {
	t.Helper()
	s := newStub(t, routes)
	return &IPAddress{Client: s.client(), Target: s.target()}, s
}

func TestIPAddressCreateSendsTypeAndOrg(t *testing.T) {
	i, s := newIP(t, map[string]route{
		"POST /v1/apps/my-api/ip_assignments": {200, `{"ip":"66.241.125.1","region":"global","shared":true}`},
	})
	res, err := i.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeIPAddress,
		Properties:   mustJSON(t, map[string]any{"appName": "my-api", "addressType": "shared_v4"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v: %s", res.ProgressResult.OperationStatus, res.ProgressResult.StatusMessage)
	}
	if res.ProgressResult.NativeID != "my-api/66.241.125.1" {
		t.Errorf("NativeID = %q", res.ProgressResult.NativeID)
	}
	body := s.only().Body
	// assignIPRequest's field is "type"; the forma-facing name is addressType
	// because "type" is already the Pkl resource discriminator.
	if body["type"] != "shared_v4" {
		t.Errorf("body = %+v", body)
	}
	if body["org_slug"] != "test-org" {
		t.Errorf("org_slug = %v; private 6PN addresses need it to resolve the network", body["org_slug"])
	}
}

func TestIPAddressCreateRejectsEmptyAllocation(t *testing.T) {
	i, _ := newIP(t, map[string]route{
		"POST /v1/apps/my-api/ip_assignments": {200, `{}`},
	})
	res, _ := i.Create(context.Background(), &resource.CreateRequest{
		Properties: mustJSON(t, map[string]any{"appName": "my-api", "addressType": "v6"}),
	})
	if res.ProgressResult.OperationStatus != resource.OperationStatusFailure {
		t.Error("an allocation with no address must fail, not produce an empty native id")
	}
}

// There is no per-address GET, so Read scans the app's assignments.
func TestIPAddressReadScansAssignments(t *testing.T) {
	i, _ := newIP(t, map[string]route{
		"GET /v1/apps/my-api/ip_assignments": {200, `{"ips":[
			{"ip":"66.241.125.1","shared":true,"region":"global"},
			{"ip":"2a09:8280:1::1:2b4c","region":"global","shared":false}]}`},
	})
	res, err := i.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeIPAddress, NativeID: "my-api/2a09:8280:1::1:2b4c",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	props := decodeProps(t, res.Properties)
	if props["ip"] != "2a09:8280:1::1:2b4c" || props["appName"] != "my-api" {
		t.Errorf("props = %+v", props)
	}
	// The listing reports `shared` and the address, not the original request
	// type, so the type is inferred from the address family rather than guessed.
	if props["addressType"] != "v6" {
		t.Errorf("addressType = %v, want v6", props["addressType"])
	}
}

func TestIPAddressReadInfersSharedV4(t *testing.T) {
	i, _ := newIP(t, map[string]route{
		"GET /v1/apps/my-api/ip_assignments": {200, `{"ips":[{"ip":"66.241.125.1","shared":true}]}`},
	})
	res, _ := i.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeIPAddress, NativeID: "my-api/66.241.125.1",
	})
	if got := decodeProps(t, res.Properties)["addressType"]; got != "shared_v4" {
		t.Errorf("addressType = %v, want shared_v4", got)
	}
}

// Released out of band: the address is simply absent from the listing.
func TestIPAddressReadMissingIsNotFound(t *testing.T) {
	i, _ := newIP(t, map[string]route{
		"GET /v1/apps/my-api/ip_assignments": {200, `{"ips":[{"ip":"1.1.1.1"}]}`},
	})
	res, _ := i.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeIPAddress, NativeID: "my-api/9.9.9.9",
	})
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q, want NotFound", res.ErrorCode)
	}
}

func TestIPAddressDeleteIdempotent(t *testing.T) {
	i, _ := newIP(t, map[string]route{
		"DELETE /v1/apps/my-api/ip_assignments/9.9.9.9": {404, `{"message":"Not Found"}`},
	})
	res, _ := i.Delete(context.Background(), &resource.DeleteRequest{NativeID: "my-api/9.9.9.9"})
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v", res.ProgressResult.OperationStatus)
	}
}

func TestIPAddressListFansOut(t *testing.T) {
	i, _ := newIP(t, map[string]route{
		"GET /v1/apps":                    {200, `{"apps":[{"name":"one"},{"name":"two"}]}`},
		"GET /v1/apps/one/ip_assignments": {200, `{"ips":[{"ip":"1.1.1.1"},{"ip":""}]}`},
		"GET /v1/apps/two/ip_assignments": {403, `{"message":"Unauthorized"}`},
	})
	res, err := i.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 1 || res.NativeIDs[0] != "one/1.1.1.1" {
		t.Errorf("NativeIDs = %v", res.NativeIDs)
	}
}

func TestIPAddressUpdateIsNotUpdatable(t *testing.T) {
	i, s := newIP(t, map[string]route{})
	res, _ := i.Update(context.Background(), &resource.UpdateRequest{NativeID: "my-api/1.1.1.1"})
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeNotUpdatable {
		t.Errorf("ErrorCode = %q", res.ProgressResult.ErrorCode)
	}
	if len(s.calls) != 0 {
		t.Error("Update hit the API")
	}
}

// addressType is inferred because the listing never reports the requested type,
// and the field is non-nullable so Read cannot omit it. These three round-trip;
// a private 6PN address would come back as "v6", which is why the schema does
// not advertise private_v6 as supported.
func TestAddressTypeInference(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name string
		in   ipAssignmentAPI
		want string
	}{
		{"shared v4", ipAssignmentAPI{IP: "66.241.125.1", Shared: &yes}, "shared_v4"},
		{"dedicated v4", ipAssignmentAPI{IP: "137.66.30.7", Shared: &no}, "v4"},
		{"v4 with no shared flag", ipAssignmentAPI{IP: "137.66.30.7"}, "v4"},
		{"v6", ipAssignmentAPI{IP: "2a09:8280:1::1:2b4c", Shared: &no}, "v6"},
		// A 6PN address is IPv6 and is indistinguishable from a public one here.
		{"private 6PN reads back as v6", ipAssignmentAPI{IP: "fdaa:0:1::3"}, "v6"},
	}
	for _, tt := range tests {
		if got := addressTypeFor(tt.in); got != tt.want {
			t.Errorf("%s: addressTypeFor(%q) = %q, want %q", tt.name, tt.in.IP, got, tt.want)
		}
	}
}
