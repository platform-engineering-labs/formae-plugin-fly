// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

// Package prov defines the Provisioner interface every FLY resource type
// implements, plus shared helpers (native-id parsing, canned failure results).
//
// The shape mirrors formae-plugin-supabase's pkg/resources/prov: one interface,
// thin helpers, no behaviour. Per-resource state lives in the struct types in
// pkg/resources/apps.
package prov

import (
	"context"
	"encoding/json"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Provisioner is the per-resource-type contract. The main plugin dispatches by
// ResourceType to the implementation registered for it.
type Provisioner interface {
	Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error)
	Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error)
	Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error)
	Delete(ctx context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error)
	Status(ctx context.Context, req *resource.StatusRequest) (*resource.StatusResult, error)
	List(ctx context.Context, req *resource.ListRequest) (*resource.ListResult, error)
}

// MustMarshal serialises a value to JSON, panicking on impossible inputs.
// Reserved for shapes we own — never user data.
func MustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("prov: marshal failed: " + err.Error())
	}
	return b
}
