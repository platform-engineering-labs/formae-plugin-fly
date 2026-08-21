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

// ResourceTypeMachine is the FLY::Apps::Machine resource type.
const ResourceTypeMachine = "FLY::Apps::Machine"

// Machine lifecycle states, from the Machines API.
const (
	machineStateCreated    = "created"
	machineStateStarting   = "starting"
	machineStateStarted    = "started"
	machineStateStopping   = "stopping"
	machineStateStopped    = "stopped"
	machineStateSuspended  = "suspended"
	machineStateReplacing  = "replacing"
	machineStateDestroying = "destroying"
	machineStateDestroyed  = "destroyed"
	machineStateFailed     = "failed"
)

func init() {
	registry.Register(
		ResourceTypeMachine,
		[]resource.Operation{
			resource.OperationCreate,
			resource.OperationRead,
			resource.OperationUpdate,
			resource.OperationDelete,
			resource.OperationCheckStatus,
			resource.OperationList,
		},
		func(c *flytransport.Client, cfg *registry.TargetConfig) prov.Provisioner {
			return &Machine{Client: c, Target: cfg}
		},
	)
}

// Machine — FLY::Apps::Machine.
//
// API mapping:
//
//	POST   /v1/apps/{app}/machines                Create (async)
//	GET    /v1/apps/{app}/machines/{id}           Read / Status
//	POST   /v1/apps/{app}/machines/{id}           Update (async, replaces config)
//	DELETE /v1/apps/{app}/machines/{id}?force     Delete
//	GET    /v1/orgs/{org}/machines                List (org-wide, for discovery)
//
// Native id is "{app}/{machine_id}" — machine ids are unique only within an app.
type Machine struct {
	Client *flytransport.Client
	Target *registry.TargetConfig
}

// -----------------------------------------------------------------------------
// Forma-facing shapes (camelCase, matching the Pkl field names)
// -----------------------------------------------------------------------------

type MachineGuestProps struct {
	CPUKind  string `json:"cpuKind,omitempty"`
	CPUs     int    `json:"cpus,omitempty"`
	MemoryMB int    `json:"memoryMb,omitempty"`
}

type MachinePortProps struct {
	Port       int      `json:"port,omitempty"`
	Handlers   []string `json:"handlers,omitempty"`
	ForceHTTPS *bool    `json:"forceHttps,omitempty"`
}

type MachineServiceProps struct {
	InternalPort       int                `json:"internalPort,omitempty"`
	Protocol           string             `json:"protocol,omitempty"`
	Ports              []MachinePortProps `json:"ports,omitempty"`
	Autostart          *bool              `json:"autostart,omitempty"`
	Autostop           string             `json:"autostop,omitempty"`
	MinMachinesRunning *int               `json:"minMachinesRunning,omitempty"`
}

type MachineMountProps struct {
	Volume string `json:"volume,omitempty"`
	Path   string `json:"path,omitempty"`
	Name   string `json:"name,omitempty"`
}

type MachineRestartProps struct {
	Policy     string `json:"policy,omitempty"`
	MaxRetries *int   `json:"maxRetries,omitempty"`
}

// MachineProperties is the forma-facing shape. Nested config is flattened onto
// the resource because a forma reads better as one object than as
// `config { image = ... }`, and formae's diff engine works per top-level field.
type MachineProperties struct {
	AppName string `json:"appName,omitempty"`
	Name    string `json:"name,omitempty"`
	Region  string `json:"region,omitempty"`

	Image       string                `json:"image,omitempty"`
	Env         map[string]string     `json:"env,omitempty"`
	Guest       *MachineGuestProps    `json:"guest,omitempty"`
	Services    []MachineServiceProps `json:"services,omitempty"`
	Mounts      []MachineMountProps   `json:"mounts,omitempty"`
	Restart     *MachineRestartProps  `json:"restart,omitempty"`
	Metadata    map[string]string     `json:"metadata,omitempty"`
	Entrypoint  []string              `json:"entrypoint,omitempty"`
	Cmd         []string              `json:"cmd,omitempty"`
	AutoDestroy *bool                 `json:"autoDestroy,omitempty"`

	// Outputs.
	ID        string `json:"id,omitempty"`
	State     string `json:"state,omitempty"`
	PrivateIP string `json:"privateIp,omitempty"`
}

// -----------------------------------------------------------------------------
// API-facing shapes (snake_case, matching fly.MachineConfig)
// -----------------------------------------------------------------------------

type machineGuestAPI struct {
	CPUKind  string `json:"cpu_kind,omitempty"`
	CPUs     int    `json:"cpus,omitempty"`
	MemoryMB int    `json:"memory_mb,omitempty"`
}

type machinePortAPI struct {
	Port       int      `json:"port,omitempty"`
	Handlers   []string `json:"handlers,omitempty"`
	ForceHTTPS *bool    `json:"force_https,omitempty"`
}

type machineServiceAPI struct {
	InternalPort       int              `json:"internal_port,omitempty"`
	Protocol           string           `json:"protocol,omitempty"`
	Ports              []machinePortAPI `json:"ports,omitempty"`
	Autostart          *bool            `json:"autostart,omitempty"`
	Autostop           string           `json:"autostop,omitempty"`
	MinMachinesRunning *int             `json:"min_machines_running,omitempty"`
}

type machineMountAPI struct {
	Volume string `json:"volume,omitempty"`
	Path   string `json:"path,omitempty"`
	Name   string `json:"name,omitempty"`
}

type machineRestartAPI struct {
	Policy     string `json:"policy,omitempty"`
	MaxRetries *int   `json:"max_retries,omitempty"`
}

type machineInitAPI struct {
	Entrypoint []string `json:"entrypoint,omitempty"`
	Cmd        []string `json:"cmd,omitempty"`
}

type machineConfigAPI struct {
	Image       string              `json:"image,omitempty"`
	Env         map[string]string   `json:"env,omitempty"`
	Guest       *machineGuestAPI    `json:"guest,omitempty"`
	Services    []machineServiceAPI `json:"services,omitempty"`
	Mounts      []machineMountAPI   `json:"mounts,omitempty"`
	Restart     *machineRestartAPI  `json:"restart,omitempty"`
	Metadata    map[string]string   `json:"metadata,omitempty"`
	Init        *machineInitAPI     `json:"init,omitempty"`
	AutoDestroy *bool               `json:"auto_destroy,omitempty"`
}

type machineAPI struct {
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Region    string            `json:"region,omitempty"`
	State     string            `json:"state,omitempty"`
	PrivateIP string            `json:"private_ip,omitempty"`
	Config    *machineConfigAPI `json:"config,omitempty"`
}

// toConfigAPI renders the forma-facing properties as a fly.MachineConfig.
func (p MachineProperties) toConfigAPI() machineConfigAPI {
	cfg := machineConfigAPI{
		Image:       p.Image,
		Env:         p.Env,
		Metadata:    p.Metadata,
		AutoDestroy: p.AutoDestroy,
	}
	if p.Guest != nil {
		cfg.Guest = &machineGuestAPI{
			CPUKind:  p.Guest.CPUKind,
			CPUs:     p.Guest.CPUs,
			MemoryMB: p.Guest.MemoryMB,
		}
	}
	for _, s := range p.Services {
		svc := machineServiceAPI{
			InternalPort:       s.InternalPort,
			Protocol:           s.Protocol,
			Autostart:          s.Autostart,
			Autostop:           s.Autostop,
			MinMachinesRunning: s.MinMachinesRunning,
		}
		for _, pt := range s.Ports {
			// Direct conversion: the two shapes differ only in their JSON tags,
			// so the compiler enforces that they stay field-for-field identical.
			svc.Ports = append(svc.Ports, machinePortAPI(pt))
		}
		cfg.Services = append(cfg.Services, svc)
	}
	for _, m := range p.Mounts {
		cfg.Mounts = append(cfg.Mounts, machineMountAPI(m))
	}
	if p.Restart != nil {
		cfg.Restart = &machineRestartAPI{Policy: p.Restart.Policy, MaxRetries: p.Restart.MaxRetries}
	}
	if len(p.Cmd) > 0 || len(p.Entrypoint) > 0 {
		cfg.Init = &machineInitAPI{Cmd: p.Cmd, Entrypoint: p.Entrypoint}
	}
	return cfg
}

// toProps maps an API machine back to forma-facing properties. appName comes
// from the native id: the machine body does not carry it.
func (a machineAPI) toProps(appName string) MachineProperties {
	p := MachineProperties{
		AppName:   appName,
		Name:      a.Name,
		Region:    a.Region,
		ID:        a.ID,
		State:     a.State,
		PrivateIP: a.PrivateIP,
	}
	if a.Config == nil {
		return p
	}
	c := a.Config
	p.Image = c.Image
	p.Env = c.Env
	p.Metadata = c.Metadata
	p.AutoDestroy = c.AutoDestroy
	if c.Guest != nil {
		p.Guest = &MachineGuestProps{
			CPUKind: c.Guest.CPUKind, CPUs: c.Guest.CPUs, MemoryMB: c.Guest.MemoryMB,
		}
	}
	for _, s := range c.Services {
		svc := MachineServiceProps{
			InternalPort:       s.InternalPort,
			Protocol:           s.Protocol,
			Autostart:          s.Autostart,
			Autostop:           s.Autostop,
			MinMachinesRunning: s.MinMachinesRunning,
		}
		for _, pt := range s.Ports {
			svc.Ports = append(svc.Ports, MachinePortProps(pt))
		}
		p.Services = append(p.Services, svc)
	}
	for _, m := range c.Mounts {
		p.Mounts = append(p.Mounts, MachineMountProps(m))
	}
	if c.Restart != nil {
		p.Restart = &MachineRestartProps{Policy: c.Restart.Policy, MaxRetries: c.Restart.MaxRetries}
	}
	if c.Init != nil {
		p.Cmd = c.Init.Cmd
		p.Entrypoint = c.Init.Entrypoint
	}
	return p
}

// -----------------------------------------------------------------------------
// CRUD
// -----------------------------------------------------------------------------

func (m *Machine) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	var p MachineProperties
	if err := json.Unmarshal(req.Properties, &p); err != nil {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if p.AppName == "" || p.Image == "" {
		return prov.FailCreate(resource.OperationErrorCodeInvalidRequest,
			"appName and image are required"), nil
	}
	body := map[string]any{"config": p.toConfigAPI()}
	if p.Name != "" {
		body["name"] = p.Name
	}
	if region := m.region(p.Region); region != "" {
		body["region"] = region
	}
	var apiResp machineAPI
	if err := m.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/apps/" + p.AppName + "/machines", Body: body,
	}, &apiResp); err != nil {
		return prov.FailCreate(flytransport.ClassifyError(err), err.Error()), nil
	}
	if apiResp.ID == "" {
		return prov.FailCreate(resource.OperationErrorCodeServiceInternalError,
			"create response carried no machine id"), nil
	}
	// The API answers with state "created"; the machine still has to pull its
	// image and boot. Status() polls until it reaches a resting state.
	return prov.InProgressCreate(prov.JoinTwoPart(p.AppName, apiResp.ID),
		"machine created, waiting for it to boot"), nil
}

func (m *Machine) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	app, id, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailRead(req.ResourceType, resource.OperationErrorCodeInvalidRequest), nil
	}
	apiResp, err := m.get(ctx, app, id)
	if err != nil {
		if flytransport.IsNotFound(err) {
			return prov.NotFoundRead(req.ResourceType), nil
		}
		return prov.FailRead(req.ResourceType, flytransport.ClassifyError(err)), nil
	}
	// A machine mid-destroy is gone as far as desired state is concerned;
	// reporting it would keep resurrecting it in the inventory.
	if apiResp.State == machineStateDestroyed || apiResp.State == machineStateDestroying {
		return prov.NotFoundRead(req.ResourceType), nil
	}
	return prov.OKRead(req.ResourceType, apiResp.toProps(app)), nil
}

func (m *Machine) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	app, id, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	var desired MachineProperties
	if err := json.Unmarshal(req.DesiredProperties, &desired); err != nil {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	if desired.Image == "" {
		return prov.FailUpdate(resource.OperationErrorCodeInvalidRequest, "image is required"), nil
	}
	// The endpoint replaces the machine's config wholesale, so the whole
	// desired config goes on the wire. `region` is deliberately absent: it is
	// createOnly, so formae plans a replacement for a region change and sending
	// it here would ask the API to migrate a machine it cannot migrate.
	body := map[string]any{"config": desired.toConfigAPI()}
	if desired.Name != "" {
		body["name"] = desired.Name
	}
	var apiResp machineAPI
	if err := m.Client.Do(ctx, flytransport.Request{
		Method: "POST", Path: "/v1/apps/" + app + "/machines/" + id, Body: body,
	}, &apiResp); err != nil {
		return prov.FailUpdate(flytransport.ClassifyError(err), err.Error()), nil
	}
	// Updating the config reboots the machine into a new version.
	return prov.InProgressUpdate(req.NativeID, "machine config replaced, waiting for the new version to boot"), nil
}

func (m *Machine) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	app, id, err := prov.ParseTwoPart(req.NativeID)
	if err != nil {
		return prov.FailDelete(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	// force=true because a running machine cannot be destroyed otherwise, and
	// "destroy this machine" is exactly what a formae delete means.
	if err := m.Client.Do(ctx, flytransport.Request{
		Method: "DELETE", Path: "/v1/apps/" + app + "/machines/" + id,
		Query: map[string]string{"force": "true"},
	}, nil); err != nil && !flytransport.IsNotFound(err) {
		return prov.FailDelete(flytransport.ClassifyError(err), err.Error()), nil
	}
	return prov.SuccessDelete(req.NativeID), nil
}

// Status polls the machine and maps its state.
//
// One plain GET per poll rather than the API's GET .../wait long-poll: holding
// a request open for up to 60s fights formae's own watchdog, and a long-poll
// that times out tells us nothing a cheap GET does not. Formae owns the cadence.
func (m *Machine) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	nativeID := req.RequestID
	if nativeID == "" {
		nativeID = req.NativeID
	}
	app, id, err := prov.ParseTwoPart(nativeID)
	if err != nil {
		return prov.FailStatus(resource.OperationErrorCodeInvalidRequest, err.Error()), nil
	}
	apiResp, err := m.get(ctx, app, id)
	if err != nil {
		if flytransport.IsNotFound(err) {
			// Create and Update are synchronous on the API side, so a machine
			// that has vanished by the time we poll means the operation did not
			// stick.
			return prov.FailStatus(resource.OperationErrorCodeNotFound,
				"machine disappeared while waiting for it to settle"), nil
		}
		return prov.FailStatus(flytransport.ClassifyError(err), err.Error()), nil
	}
	switch apiResp.State {
	case machineStateStarted, machineStateStopped, machineStateSuspended:
		// stopped and suspended are resting states, not failures: a machine
		// with autostop, or a worker created with skip_launch, legitimately
		// settles there.
		return prov.SuccessStatus(nativeID), nil
	case machineStateCreated, machineStateStarting, machineStateStopping, machineStateReplacing:
		return prov.InProgressStatus(nativeID, "machine is "+apiResp.State), nil
	case machineStateFailed:
		return prov.FailStatus(resource.OperationErrorCodeServiceInternalError,
			"machine entered the failed state"), nil
	case machineStateDestroying, machineStateDestroyed:
		return prov.FailStatus(resource.OperationErrorCodeNotFound,
			"machine was destroyed while waiting for it to settle"), nil
	default:
		// An unknown state is not a failure — Fly can add states. Keep polling
		// and let formae's own timeout decide.
		return prov.InProgressStatus(nativeID, "machine is in unrecognised state "+apiResp.State), nil
	}
}

// List enumerates machines org-wide in one call. The per-app endpoint would
// need one request per app, which on a large org is what times discovery out.
func (m *Machine) List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error) {
	empty := &resource.ListResult{NativeIDs: []string{}}
	if m.Target == nil || m.Target.Org == "" {
		return empty, nil
	}
	query := map[string]string{"summary": "true"} // discovery only needs ids
	if req != nil && req.PageToken != nil && *req.PageToken != "" {
		query["cursor"] = *req.PageToken
	}
	var resp struct {
		Machines []struct {
			ID      string `json:"id"`
			AppName string `json:"app_name"`
		} `json:"machines"`
		NextCursor string `json:"next_cursor"`
	}
	if err := m.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/orgs/" + m.Target.Org + "/machines", Query: query,
	}, &resp); err != nil {
		// One unreadable org must not fail the whole discovery sync.
		return empty, nil
	}
	ids := make([]string, 0, len(resp.Machines))
	for _, mc := range resp.Machines {
		if mc.ID == "" || mc.AppName == "" {
			continue
		}
		ids = append(ids, prov.JoinTwoPart(mc.AppName, mc.ID))
	}
	out := &resource.ListResult{NativeIDs: ids}
	if resp.NextCursor != "" {
		out.NextPageToken = &resp.NextCursor
	}
	return out, nil
}

// region resolves the effective region: the resource's own, else the target's
// default. FLY_REGION is deliberately not consulted — inside a Fly VM it means
// "the region I am running in", which would make applies behave differently
// depending on where the agent lives.
func (m *Machine) region(resourceRegion string) string {
	if resourceRegion != "" {
		return resourceRegion
	}
	if m.Target != nil {
		return m.Target.Region
	}
	return ""
}

func (m *Machine) get(ctx context.Context, app, id string) (machineAPI, error) {
	var apiResp machineAPI
	err := m.Client.Do(ctx, flytransport.Request{
		Method: "GET", Path: "/v1/apps/" + app + "/machines/" + id,
	}, &apiResp)
	return apiResp, err
}
