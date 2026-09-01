// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package apps

import (
	"context"
	"encoding/json"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/registry"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ResourceTypeCertificate is the FLY::Apps::Certificate resource type.
const ResourceTypeCertificate = "FLY::Apps::Certificate"

func init() {
	registry.Register(
		ResourceTypeCertificate,
		// No OperationUpdate: hostname is the resource's server-side identity,
		// so there is nothing to update — a change is a replacement.
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Certificate{Client: c, Target: cfg}
		},
	)
}

// Certificate — FLY::Apps::Certificate.
//
// API mapping:
//
//	POST   /v1/apps/{app}/certificates/acme        Request an ACME certificate
//	GET    /v1/apps/{app}/certificates/{hostname}  Read
//	DELETE /v1/apps/{app}/certificates/{hostname}  Delete
//	GET    /v1/apps/{app}/certificates             List (per app, cursor-paged)
//
// Native id is "{app}/{hostname}".
type Certificate struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// CertificateDNSRequirements mirrors the API's dns_requirements, flattened:
// the nested acme_challenge and ownership objects each carry two fields, and a
// flat shape is easier to read in `formae inventory` output than three levels
// of nesting.
type CertificateDNSRequirements struct {
	CNAME               string   `json:"cname,omitempty"`
	A                   []string `json:"a,omitempty"`
	AAAA                []string `json:"aaaa,omitempty"`
	OwnershipName       string   `json:"ownershipName,omitempty"`
	OwnershipValue      string   `json:"ownershipValue,omitempty"`
	ACMEChallengeName   string   `json:"acmeChallengeName,omitempty"`
	ACMEChallengeTarget string   `json:"acmeChallengeTarget,omitempty"`
}

// CertificateProperties is the forma-facing shape.
type CertificateProperties struct {
	AppName  string `json:"appName,omitempty"`
	Hostname string `json:"hostname,omitempty"`

	// Outputs.
	Status          string                      `json:"status,omitempty"`
	Configured      *bool                       `json:"configured,omitempty"`
	DNSRequirements *CertificateDNSRequirements `json:"dnsRequirements,omitempty"`
}

type certificateAPI struct {
	Hostname        string `json:"hostname,omitempty"`
	Status          string `json:"status,omitempty"`
	Configured      *bool  `json:"configured,omitempty"`
	DNSRequirements *struct {
		CNAME         string   `json:"cname,omitempty"`
		A             []string `json:"a,omitempty"`
		AAAA          []string `json:"aaaa,omitempty"`
		AcmeChallenge *struct {
			Name   string `json:"name,omitempty"`
			Target string `json:"target,omitempty"`
		} `json:"acme_challenge,omitempty"`
		Ownership *struct {
			Name     string `json:"name,omitempty"`
			AppValue string `json:"app_value,omitempty"`
		} `json:"ownership,omitempty"`
	} `json:"dns_requirements,omitempty"`
}

func (a certificateAPI) toProps(appName, hostname string) CertificateProperties {
	if a.Hostname != "" {
		hostname = a.Hostname
	}
	p := CertificateProperties{
		AppName:    appName,
		Hostname:   hostname,
		Status:     a.Status,
		Configured: a.Configured,
	}
	if a.DNSRequirements == nil {
		return p
	}
	d := &CertificateDNSRequirements{
		CNAME: a.DNSRequirements.CNAME,
		A:     a.DNSRequirements.A,
		AAAA:  a.DNSRequirements.AAAA,
	}
	if c := a.DNSRequirements.AcmeChallenge; c != nil {
		d.ACMEChallengeName = c.Name
		d.ACMEChallengeTarget = c.Target
	}
	if o := a.DNSRequirements.Ownership; o != nil {
		d.OwnershipName = o.Name
		d.OwnershipValue = o.AppValue
	}
	p.DNSRequirements = d
	return p
}

// Create registers the ACME request and returns Success immediately.
//
// Deliberately not InProgress: validation waits on DNS records only the user
// can publish, so `status` can legitimately sit at pending_validation forever.
// Returning InProgress would hang the apply on an action formae cannot take.
// The records to publish come back in dnsRequirements.
func (c *Certificate) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p CertificateProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.AppName == "" || p.Hostname == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"appName and hostname are required"), nil
	}
	var apiResp certificateAPI
	if err := c.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/apps/" + p.AppName + "/certificates/acme",
		Body: map[string]any{"hostname": p.Hostname},
	}, &apiResp); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessCreate(prov.JoinTwoPart(p.AppName, p.Hostname),
		apiResp.toProps(p.AppName, p.Hostname)), nil
}

func (c *Certificate) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	app, hostname, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	var apiResp certificateAPI
	if err := c.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + app + "/certificates/" + hostname,
	}, &apiResp); err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	return prov.OKRead(req.ResourceType, apiResp.toProps(app, hostname)), nil
}

// Update always refuses: the hostname is the certificate's identity server-side.
func (c *Certificate) Update(_ context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Apps::Certificate has no update path; the hostname is its identity and a change replaces it"), nil
}

func (c *Certificate) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	app, hostname, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if err := c.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/apps/" + app + "/certificates/" + hostname,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

// Status reports Success: the ACME request is registered synchronously, and the
// validation that follows is not something to block an apply on.
func (c *Certificate) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

// List fans out over the org's apps: certificates have no org-wide endpoint.
// The per-app listing is cursor-paged, and this follows the cursor to the end
// rather than reporting a partial answer that would look like "no certificates".
func (c *Certificate) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	apps, err := listAppNames(ctx, c.Client, c.Target)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, app := range apps {
		cursor := ""
		for {
			query := map[string]string{}
			if cursor != "" {
				query["cursor"] = cursor
			}
			var resp struct {
				Certificates []struct {
					Hostname string `json:"hostname"`
				} `json:"certificates"`
				NextCursor string `json:"next_cursor"`
			}
			if err := c.Client.Do(ctx, flytransport.Request{
				Method: "GET", Path: "/v1/apps/" + app + "/certificates", Query: query,
			}, &resp); err != nil {
				break
			}
			for _, cert := range resp.Certificates {
				if cert.Hostname == "" {
					continue
				}
				ids = append(ids, prov.JoinTwoPart(app, cert.Hostname))
			}
			if resp.NextCursor == "" || resp.NextCursor == cursor {
				break
			}
			cursor = resp.NextCursor
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}
