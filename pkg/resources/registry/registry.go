// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package registry holds the per-resource-type Provisioner factory map.
//
// Each file in pkg/resources/apps registers itself via init() at package import
// time. The main plugin looks the factory up by resource type and constructs a
// Provisioner bound to a live API client plus the parsed target config, so
// fly.go stays constant as resources are added.
package registry

import (
	"fmt"
	"sync"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// TargetConfig is the deployment-level config carried in the forma target.
// Kept here (rather than in package main) so resource packages can depend on it
// without an import cycle.
type TargetConfig struct {
	// Org is the Fly organization slug, or "personal". Required: app create
	// (POST /v1/apps body org_slug) and app list (GET /v1/apps?org_slug=) both
	// need it, and it is not derivable from a token.
	Org string `json:"Org"`

	// Region is the default Fly region code for machines and volumes when the
	// resource does not name one. Optional.
	Region string `json:"Region"`

	// BaseURL overrides the API endpoint. Optional; defaults to
	// https://api.machines.dev.
	BaseURL string `json:"BaseUrl"`
}

// Factory builds a Provisioner bound to a live client and target.
type Factory func(client *flytransport.Client, cfg *TargetConfig) prov.Provisioner

type registration struct {
	factory    Factory
	operations []resource.Operation
}

var (
	mu            sync.RWMutex
	registrations = make(map[string]*registration)
)

// Register associates a FLY resource type with the operations it genuinely
// supports and a Factory. Called from init() at package load.
//
// Registering an operation the provisioner does not implement is worse than
// leaving it out: formae will plan it and the plugin will fail it.
func Register(resourceType string, operations []resource.Operation, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registrations[resourceType]; exists {
		panic(fmt.Sprintf("duplicate registration for %q", resourceType))
	}
	registrations[resourceType] = &registration{factory: factory, operations: operations}
}

// GetFactory returns the factory for a resource type.
func GetFactory(resourceType string) (Factory, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := registrations[resourceType]
	if !ok {
		return nil, false
	}
	return r.factory, true
}

// GetOperations returns the registered operations for a resource type.
func GetOperations(resourceType string) []resource.Operation {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := registrations[resourceType]
	if !ok {
		return nil
	}
	return r.operations
}

// ResourceTypes returns every registered type.
func ResourceTypes() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registrations))
	for t := range registrations {
		out = append(out, t)
	}
	return out
}

// Has reports whether a type is registered.
func Has(resourceType string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := registrations[resourceType]
	return ok
}
