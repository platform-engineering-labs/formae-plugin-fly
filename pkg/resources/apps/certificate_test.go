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

func newCert(t *testing.T, routes map[string]route) (*Certificate, *stub) {
	t.Helper()
	s := newStub(t, routes)
	return &Certificate{Client: s.client(), Target: s.target()}, s
}

// Create must be Success, not InProgress: ACME validation waits on DNS records
// only the user can publish, so InProgress would hang the apply forever.
func TestCertificateCreateDoesNotBlockOnValidation(t *testing.T) {
	c, s := newCert(t, map[string]route{
		"POST /v1/apps/my-api/certificates/acme": {201, `{
			"hostname":"api.example.com","status":"pending_validation","configured":false,
			"dns_requirements":{"cname":"my-api.fly.dev","a":["1.2.3.4"],
				"acme_challenge":{"name":"_acme-challenge.api","target":"api.example.com.flydns.net"},
				"ownership":{"name":"_fly-ownership.api","app_value":"tok123"}}}`},
	})
	res, err := c.Create(context.Background(), &resource.CreateRequest{
		ResourceType: ResourceTypeCertificate,
		Properties:   mustJSON(t, map[string]any{"appName": "my-api", "hostname": "api.example.com"}),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Fatalf("status = %v, want Success even though the cert is unvalidated", res.ProgressResult.OperationStatus)
	}
	if res.ProgressResult.NativeID != "my-api/api.example.com" {
		t.Errorf("NativeID = %q", res.ProgressResult.NativeID)
	}
	if s.only().Body["hostname"] != "api.example.com" {
		t.Errorf("body = %+v", s.only().Body)
	}
}

func TestCertificateReadFlattensDNSRequirements(t *testing.T) {
	c, _ := newCert(t, map[string]route{
		"GET /v1/apps/my-api/certificates/api.example.com": {200, `{
			"hostname":"api.example.com","status":"pending_validation","configured":false,
			"dns_requirements":{"cname":"my-api.fly.dev","aaaa":["2a09::1"],
				"acme_challenge":{"name":"_acme-challenge.api","target":"chal.flydns.net"},
				"ownership":{"name":"_fly-ownership.api","app_value":"tok123","org_value":"orgtok"}}}`},
	})
	res, err := c.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeCertificate, NativeID: "my-api/api.example.com",
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	props := decodeProps(t, res.Properties)
	if props["appName"] != "my-api" || props["hostname"] != "api.example.com" ||
		props["status"] != "pending_validation" || props["configured"] != false {
		t.Errorf("props = %+v", props)
	}
	d := props["dnsRequirements"].(map[string]any)
	if d["cname"] != "my-api.fly.dev" ||
		d["acmeChallengeName"] != "_acme-challenge.api" ||
		d["acmeChallengeTarget"] != "chal.flydns.net" ||
		d["ownershipName"] != "_fly-ownership.api" ||
		d["ownershipValue"] != "tok123" {
		t.Errorf("dnsRequirements = %+v", d)
	}
}

func TestCertificateReadNotFound(t *testing.T) {
	c, _ := newCert(t, map[string]route{
		"GET /v1/apps/my-api/certificates/gone.example.com": {404, `{"message":"Not Found"}`},
	})
	res, _ := c.Read(context.Background(), &resource.ReadRequest{
		ResourceType: ResourceTypeCertificate, NativeID: "my-api/gone.example.com",
	})
	if res.ErrorCode != resource.OperationErrorCodeNotFound {
		t.Errorf("ErrorCode = %q", res.ErrorCode)
	}
}

func TestCertificateUpdateIsNotUpdatable(t *testing.T) {
	c, s := newCert(t, map[string]route{})
	res, _ := c.Update(context.Background(), &resource.UpdateRequest{NativeID: "my-api/x"})
	if res.ProgressResult.ErrorCode != resource.OperationErrorCodeNotUpdatable {
		t.Errorf("ErrorCode = %q", res.ProgressResult.ErrorCode)
	}
	if len(s.calls) != 0 {
		t.Error("Update hit the API")
	}
}

func TestCertificateDeleteIdempotent(t *testing.T) {
	c, _ := newCert(t, map[string]route{
		"DELETE /v1/apps/my-api/certificates/gone.example.com": {404, `{"message":"Not Found"}`},
	})
	res, _ := c.Delete(context.Background(), &resource.DeleteRequest{NativeID: "my-api/gone.example.com"})
	if res.ProgressResult.OperationStatus != resource.OperationStatusSuccess {
		t.Errorf("status = %v", res.ProgressResult.OperationStatus)
	}
}

// The per-app certificate listing is cursor-paged. Stopping at the first page
// would silently report a partial answer, which discovery reads as "these are
// all the certificates" and would prune the rest.
func TestCertificateListFollowsCursor(t *testing.T) {
	page := 0
	s := newStub(t, map[string]route{"GET /v1/apps": {200, `{"apps":[{"name":"my-api"}]}`}})
	c := &Certificate{Client: s.client(), Target: s.target()}

	// The same route has to answer differently per call, which the static route
	// table cannot express.
	pages := []string{
		`{"certificates":[{"hostname":"a.example.com"}],"next_cursor":"c2"}`,
		`{"certificates":[{"hostname":"b.example.com"}],"next_cursor":""}`,
	}
	s.handler = func(path, query string) (int, string, bool) {
		if path != "/v1/apps/my-api/certificates" {
			return 0, "", false
		}
		body := pages[page]
		page++
		return 200, body, true
	}

	res, err := c.List(context.Background(), &resource.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.NativeIDs) != 2 ||
		res.NativeIDs[0] != "my-api/a.example.com" || res.NativeIDs[1] != "my-api/b.example.com" {
		t.Errorf("NativeIDs = %v, want both pages", res.NativeIDs)
	}
}
