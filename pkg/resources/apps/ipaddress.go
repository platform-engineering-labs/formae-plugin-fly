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

// ResourceTypeIPAddress is the FLY::Apps::IPAddress resource type.
const ResourceTypeIPAddress = "FLY::Apps::IPAddress"

func init() {
	registry.Register(
		ResourceTypeIPAddress,
		// No OperationUpdate: Fly picks the address, so every input is
		// createOnly and there is no update path.
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &IPAddress{Client: c, Target: cfg}
		},
	)
}

// IPAddress — FLY::Apps::IPAddress.
//
// API mapping:
//
//	POST   /v1/apps/{app}/ip_assignments       Allocate
//	GET    /v1/apps/{app}/ip_assignments       Read / List (no per-address GET)
//	DELETE /v1/apps/{app}/ip_assignments/{ip}  Release
//
// Native id is "{app}/{ip}". An app with machines and services is unreachable
// from the internet until it has an address: `fly deploy` allocates one
// implicitly, the raw Machines API does not.
type IPAddress struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// IPAddressProperties is the forma-facing shape.
//
// The field is `addressType`, not `type`: `type` is the Pkl discriminator
// formae.Resource already uses for the resource type itself.
type IPAddressProperties struct {
	AppName     string `json:"appName,omitempty"`
	AddressType string `json:"addressType,omitempty"`
	Region      string `json:"region,omitempty"`
	Network     string `json:"network,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`

	// Outputs.
	IP     string `json:"ip,omitempty"`
	Shared *bool  `json:"shared,omitempty"`
}

type ipAssignmentAPI struct {
	IP          string `json:"ip,omitempty"`
	Region      string `json:"region,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	Shared      *bool  `json:"shared,omitempty"`
}

// toProps maps an assignment back. addressType is carried over from the
// desired/native id rather than read back: the list response reports `shared`
// but not the request type ("shared_v4" vs "v4" vs "v6"), so re-deriving it
// would be a guess.
func (a ipAssignmentAPI) toProps(appName, addressType string) IPAddressProperties {
	return IPAddressProperties{
		AppName:     appName,
		AddressType: addressType,
		Region:      a.Region,
		ServiceName: a.ServiceName,
		IP:          a.IP,
		Shared:      a.Shared,
	}
}

func (i *IPAddress) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p IPAddressProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.AppName == "" || p.AddressType == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"appName and addressType are required"), nil
	}
	body := map[string]any{"type": p.AddressType}
	if p.Region != "" {
		body["region"] = p.Region
	}
	if p.Network != "" {
		body["network"] = p.Network
	}
	if p.ServiceName != "" {
		body["service_name"] = p.ServiceName
	}
	if i.Target != nil && i.Target.Org != "" {
		// assignIPRequest carries org_slug; private (6PN) addresses need it to
		// resolve the network.
		body["org_slug"] = i.Target.Org
	}
	var apiResp ipAssignmentAPI
	if err := i.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/apps/" + p.AppName + "/ip_assignments", Body: body,
	}, &apiResp); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	if apiResp.IP == "" {
		return prov.FailCreate(resource.OperationErrorCodeServiceInternalError,
			"allocation response carried no address"), nil
	}
	return prov.SuccessCreate(prov.JoinTwoPart(p.AppName, apiResp.IP)), nil
}

// Read scans the app's assignments for the address: there is no per-address GET.
func (i *IPAddress) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	app, ip, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	assignments, err := i.assignments(ctx, app)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	for _, a := range assignments {
		if a.IP != ip {
			continue
		}
		return prov.OKRead(req.ResourceType, a.toProps(app, addressTypeFor(a))), nil
	}
	return prov.NotFoundRead(req.ResourceType), nil
}

// addressTypeFor infers the request type from what the listing does report.
// `shared` plus the address family is enough to distinguish the three types the
// plugin can allocate; anything else stays empty rather than guessing.
func addressTypeFor(a ipAssignmentAPI) string {
	isV6 := false
	for _, ch := range a.IP {
		if ch == ':' {
			isV6 = true
			break
		}
	}
	switch {
	case isV6:
		return "v6"
	case a.Shared != nil && *a.Shared:
		return "shared_v4"
	default:
		return "v4"
	}
}

func (i *IPAddress) Update(_ context.Context, _ *resource.UpdateRequest) (*resource.UpdateResult, error) {
	return prov.FailUpdate(resource.OperationErrorCodeNotUpdatable,
		"FLY::Apps::IPAddress has no update path; Fly allocates the address and every input is createOnly"), nil
}

func (i *IPAddress) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	app, ip, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if err := i.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/apps/" + app + "/ip_assignments/" + ip,
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

// Status reports Success: allocation is immediate.
func (i *IPAddress) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	id := req.RequestID
	if id == "" {
		id = req.NativeID
	}
	return prov.SuccessStatus(id), nil
}

// List fans out over the org's apps: IP assignments have no org-wide endpoint.
func (i *IPAddress) List(ctx context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	ids := make([]string, 0)
	apps, err := listAppNames(ctx, i.Client, i.Target)
	if err != nil {
		return &resource.ListResult{NativeIDs: ids}, nil
	}
	for _, app := range apps {
		assignments, err := i.assignments(ctx, app)
		if err != nil {
			continue
		}
		for _, a := range assignments {
			if a.IP == "" {
				continue
			}
			ids = append(ids, prov.JoinTwoPart(app, a.IP))
		}
	}
	return &resource.ListResult{NativeIDs: ids}, nil
}

func (i *IPAddress) assignments(ctx context.Context, app string) ([]ipAssignmentAPI, error) {
	var resp struct {
		IPs []ipAssignmentAPI `json:"ips"`
	}
	if err := i.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + app + "/ip_assignments",
	}, &resp); err != nil {
		return nil, err
	}
	return resp.IPs, nil
}
