// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package registry

import (
	"sync"
	"testing"

	"github.com/platform-engineering-labs/formae-plugin-fly/pkg/resources/prov"
	flytransport "github.com/platform-engineering-labs/formae-plugin-fly/pkg/transport/fly"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func reset() {
	mu.Lock()
	defer mu.Unlock()
	registrations = make(map[string]*registration)
}

func TestRegisterAndLookup(t *testing.T) {
	reset()
	t.Cleanup(reset)
	ops := []resource.Operation{resource.OperationCreate, resource.OperationRead}
	Register("FLY::Test::Thing", ops, func(*flytransport.Client, *TargetConfig) prov.Provisioner { return nil })

	if !Has("FLY::Test::Thing") {
		t.Fatal("Has = false after Register")
	}
	if _, ok := GetFactory("FLY::Test::Thing"); !ok {
		t.Error("GetFactory = false")
	}
	if got := GetOperations("FLY::Test::Thing"); len(got) != 2 {
		t.Errorf("GetOperations = %v", got)
	}
	if types := ResourceTypes(); len(types) != 1 || types[0] != "FLY::Test::Thing" {
		t.Errorf("ResourceTypes = %v", types)
	}
	if _, ok := GetFactory("FLY::Test::Missing"); ok {
		t.Error("GetFactory returned a factory for an unregistered type")
	}
	if GetOperations("FLY::Test::Missing") != nil {
		t.Error("GetOperations returned ops for an unregistered type")
	}
}

// A duplicate registration is a programming error caught at package load, not a
// silent last-writer-wins that would make dispatch depend on import order.
func TestRegisterPanicsOnDuplicate(t *testing.T) {
	reset()
	t.Cleanup(reset)
	f := func(*flytransport.Client, *TargetConfig) prov.Provisioner { return nil }
	Register("FLY::Test::Dup", nil, f)
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register did not panic")
		}
	}()
	Register("FLY::Test::Dup", nil, f)
}

// The registry is read from every concurrent CRUD call the agent issues.
func TestRegistryConcurrentReads(t *testing.T) {
	reset()
	t.Cleanup(reset)
	Register("FLY::Test::Race", nil, func(*flytransport.Client, *TargetConfig) prov.Provisioner { return nil })
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = GetFactory("FLY::Test::Race")
			_ = ResourceTypes()
			_ = Has("FLY::Test::Race")
		}()
	}
	wg.Wait()
}
