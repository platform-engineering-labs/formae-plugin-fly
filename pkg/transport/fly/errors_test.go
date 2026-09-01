// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package fly

import (
	"errors"
	"fmt"
	"testing"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestClassifyStatus(t *testing.T) {
	tests := []struct {
		status int
		want   resource.OperationErrorCode
	}{
		{400, resource.OperationErrorCodeInvalidRequest},
		{401, resource.OperationErrorCodeInvalidCredentials},
		{403, resource.OperationErrorCodeAccessDenied},
		{404, resource.OperationErrorCodeNotFound},
		{410, resource.OperationErrorCodeNotFound},
		{409, resource.OperationErrorCodeAlreadyExists},
		{412, resource.OperationErrorCodeInvalidRequest},
		{422, resource.OperationErrorCodeInvalidRequest},
		{429, resource.OperationErrorCodeThrottling},
		{500, resource.OperationErrorCodeServiceInternalError},
		{502, resource.OperationErrorCodeServiceInternalError},
		{599, resource.OperationErrorCodeServiceInternalError},
		{302, resource.OperationErrorCodeInternalFailure},
	}
	for _, tt := range tests {
		if got := ClassifyStatus(tt.status); got != tt.want {
			t.Errorf("ClassifyStatus(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestClassifyErrorUnwrapsAPIError(t *testing.T) {
	wrapped := fmt.Errorf("create machine: %w", &APIError{StatusCode: 429})
	if got := ClassifyError(wrapped); got != resource.OperationErrorCodeThrottling {
		t.Errorf("ClassifyError(wrapped 429) = %q, want Throttling", got)
	}
	if got := ClassifyError(errors.New("dial tcp: connection refused")); got != resource.OperationErrorCodeInternalFailure {
		t.Errorf("ClassifyError(plain) = %q, want InternalFailure", got)
	}
}

// IsNotFound must match flaps' habit of reporting a destroyed app as HTTP 400
// rather than 404. Without this, a child resource whose app was deleted out of
// band never leaves the inventory: sync reads 400, calls it InvalidRequest, and
// never prunes.
func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"404", &APIError{StatusCode: 404}, true},
		// Managed Postgres answers 410 for an already-deleted cluster or
		// attachment; delete has to stay idempotent across that.
		{"410 gone", &APIError{StatusCode: 410}, true},
		{"404 wrapped", fmt.Errorf("read: %w", &APIError{StatusCode: 404}), true},
		{"400 could not find app", &APIError{StatusCode: 400, Message: "could not find app"}, true},
		{"400 App not found", &APIError{StatusCode: 400, Message: "App not found"}, true},
		{"400 does not exist", &APIError{StatusCode: 400, Message: "machine does not exist"}, true},
		{"400 unknown app", &APIError{StatusCode: 400, Message: "unknown app my-app"}, true},
		{"400 real validation error", &APIError{StatusCode: 400, Message: "invalid guest size"}, false},
		{"401", &APIError{StatusCode: 401}, false},
		{"500", &APIError{StatusCode: 500, Message: "not found in cache"}, false},
		{"nil", nil, false},
		{"non-api", errors.New("boom"), false},
	}
	for _, tt := range tests {
		if got := IsNotFound(tt.err); got != tt.want {
			t.Errorf("IsNotFound(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAPIErrorMessage(t *testing.T) {
	e := &APIError{StatusCode: 422, Message: "name is taken", Body: `{"error":"name is taken"}`}
	if got := e.Error(); got != "fly API: HTTP 422: name is taken" {
		t.Errorf("Error() = %q", got)
	}
	// Falls back to the raw body when no known message field is present.
	e2 := &APIError{StatusCode: 500, Body: "upstream exploded"}
	if got := e2.Error(); got != "fly API: HTTP 500: upstream exploded" {
		t.Errorf("Error() = %q", got)
	}
}

// api.machines.dev fronts more than one upstream and they disagree on the error
// field name: {"message":...} from the apps/certs/IP service, {"error":...}
// from flaps. Both must decode.
func TestExtractMessage(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{`{"message":"Unauthorized"}`, "Unauthorized"},
		{`{"error":"Authenticate: token validation error"}`, "Authenticate: token validation error"},
		{`{"msg":"nope"}`, "nope"},
		{`{"error":"Organization not found"}`, "Organization not found"},
		{`{"unrelated":1}`, ""},
		{`404 page not found`, ""},
		{``, ""},
	}
	for _, tt := range tests {
		if got := extractMessage([]byte(tt.body)); got != tt.want {
			t.Errorf("extractMessage(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}
