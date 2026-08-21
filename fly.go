// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"errors"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// ErrNotImplemented is returned by stub methods that need implementation.
var ErrNotImplemented = errors.New("not implemented")

// Plugin implements the Formae ResourcePlugin interface.
// The SDK automatically provides identity methods (Name, Version, Namespace)
// by reading formae-plugin.pkl at startup.
type Plugin struct{}

// Compile-time check: Plugin must satisfy ResourcePlugin interface.
var _ plugin.ResourcePlugin = &Plugin{}

// =============================================================================
// Configuration Methods
// =============================================================================

// RateLimit returns the rate limiting configuration for this plugin.
// Adjust MaxRequestsPerSecondForNamespace based on your provider's API limits.
func (p *Plugin) RateLimit() model.RateLimitConfig {
	return model.RateLimitConfig{
		Scope:                            model.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 10, // TODO: Adjust based on provider limits
	}
}

// DiscoveryFilters returns filters to exclude certain resources from discovery.
// Resources matching ALL conditions in a filter are excluded.
// Return nil if you want to discover all resources.
func (p *Plugin) DiscoveryFilters() []model.MatchFilter {
	// Example: exclude resources with a specific tag
	// return []model.MatchFilter{
	//     {
	//         ResourceTypes: []string{"FLY::Service::Resource"},
	//         Conditions: []model.FilterCondition{
	//             {PropertyPath: "$.Tags[?(@.Key=='skip-discovery')].Value", PropertyValue: "true"},
	//         },
	//     },
	// }
	return nil
}

// LabelConfig returns the configuration for extracting human-readable labels
// from discovered resources.
func (p *Plugin) LabelConfig() model.LabelConfig {
	return model.LabelConfig{
		// Default JSONPath query to extract label from resources
		// Example for tagged resources: $.Tags[?(@.Key=='Name')].Value
		DefaultQuery: "$.Name", // TODO: Adjust for your provider

		// Override for specific resource types
		ResourceOverrides: map[string]string{
			// "FLY::Service::SpecialResource": "$.DisplayName",
		},
	}
}

// =============================================================================
// CRUD Operations
// =============================================================================

// Create provisions a new resource.
func (p *Plugin) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	// TODO: Implement resource creation
	//
	// 1. Parse req.Properties to get resource configuration (json.RawMessage)
	// 2. Parse req.TargetConfig to get provider credentials/config
	// 3. Call your provider's API to create the resource
	// 4. Return ProgressResult with:
	//    - NativeID: the provider's identifier for the resource
	//    - OperationStatus: Success, Failure, or InProgress
	//    - If InProgress, set RequestID for status polling

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       resource.OperationErrorCodeInternalFailure,
			StatusMessage:   "Create not implemented",
		},
	}, ErrNotImplemented
}

// Read retrieves the current state of a resource.
func (p *Plugin) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	// TODO: Implement resource read
	//
	// 1. Use req.NativeID to identify the resource
	// 2. Parse req.TargetConfig for provider credentials
	// 3. Call your provider's API to get current state
	// 4. Return ReadResult with Properties as JSON string

	return &resource.ReadResult{
		ResourceType: req.ResourceType,
		ErrorCode:    resource.OperationErrorCodeInternalFailure,
	}, ErrNotImplemented
}

// Update modifies an existing resource.
func (p *Plugin) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	// TODO: Implement resource update
	//
	// 1. Use req.NativeID to identify the resource
	// 2. Use req.PatchDocument for changes (JSON Patch format)
	//    Or compare req.PriorProperties with req.DesiredProperties
	// 3. Call your provider's API to apply changes
	// 4. Return ProgressResult with status

	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationUpdate,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       resource.OperationErrorCodeInternalFailure,
			StatusMessage:   "Update not implemented",
		},
	}, ErrNotImplemented
}

// Delete removes a resource.
func (p *Plugin) Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	// TODO: Implement resource deletion
	//
	// 1. Use req.NativeID to identify the resource
	// 2. Parse req.TargetConfig for provider credentials
	// 3. Call your provider's API to delete the resource
	// 4. Return ProgressResult with status

	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       resource.OperationErrorCodeInternalFailure,
			StatusMessage:   "Delete not implemented",
		},
	}, ErrNotImplemented
}

// Status checks the progress of an async operation.
// Called when Create/Update/Delete return InProgress status.
func (p *Plugin) Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	// TODO: Implement status checking for async operations
	//
	// 1. Use req.RequestID to identify the operation
	// 2. Call your provider's API to check operation status
	// 3. Return ProgressResult with current status
	//
	// If your provider's operations are synchronous, return Success immediately.

	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCheckStatus,
			OperationStatus: resource.OperationStatusFailure,
			ErrorCode:       resource.OperationErrorCodeInternalFailure,
			StatusMessage:   "Status not implemented",
		},
	}, ErrNotImplemented
}

// List returns all resource identifiers of a given type.
// Called during discovery to find unmanaged resources.
func (p *Plugin) List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error) {
	// TODO: Implement resource listing for discovery
	//
	// 1. Use req.ResourceType to determine what to list
	// 2. Parse req.TargetConfig for provider credentials
	// 3. Use req.PageToken/PageSize for pagination
	// 4. Call your provider's API to list resources
	// 5. Return NativeIDs and NextPageToken (if more pages)

	return &resource.ListResult{
		NativeIDs:     []string{},
		NextPageToken: nil,
	}, ErrNotImplemented
}
