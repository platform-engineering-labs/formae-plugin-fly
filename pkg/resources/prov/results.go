// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package prov

import "github.com/platform-engineering-labs/formae/pkg/plugin/resource"

// FailCreate builds a Failure CreateResult.
func FailCreate(code resource.OperationErrorCode, msg string) *resource.CreateResult {
	return &resource.CreateResult{ProgressResult: fail(resource.OperationCreate, code, msg)}
}

// FailUpdate builds a Failure UpdateResult.
func FailUpdate(code resource.OperationErrorCode, msg string) *resource.UpdateResult {
	return &resource.UpdateResult{ProgressResult: fail(resource.OperationUpdate, code, msg)}
}

// FailDelete builds a Failure DeleteResult.
func FailDelete(code resource.OperationErrorCode, msg string) *resource.DeleteResult {
	return &resource.DeleteResult{ProgressResult: fail(resource.OperationDelete, code, msg)}
}

// FailStatus builds a Failure StatusResult.
func FailStatus(code resource.OperationErrorCode, msg string) *resource.StatusResult {
	return &resource.StatusResult{ProgressResult: fail(resource.OperationCheckStatus, code, msg)}
}

func fail(op resource.Operation, code resource.OperationErrorCode, msg string) *resource.ProgressResult {
	return &resource.ProgressResult{
		Operation:       op,
		OperationStatus: resource.OperationStatusFailure,
		ErrorCode:       code,
		StatusMessage:   msg,
	}
}

// SuccessCreate builds a synchronous-success CreateResult carrying the created
// resource's properties.
//
// The properties are NOT optional decoration. Formae resolves a `res.<field>`
// reference against the properties stored for the producing resource, and it
// stores what Create returns — it does not call Read first. A Create that
// reports only a NativeID logs "No properties to split for resource", stores
// nothing, and every resolvable pointing at it fails with NotFound until the
// consuming operation gives up. Found the hard way: a Postgres Attachment
// referencing `app.res.name` retried nine times over two and a half minutes and
// failed the apply, even though the app had been created successfully.
func SuccessCreate(nativeID string, props any) *resource.CreateResult {
	return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
		Operation:          resource.OperationCreate,
		OperationStatus:    resource.OperationStatusSuccess,
		NativeID:           nativeID,
		ResourceProperties: MustMarshal(props),
	}}
}

// InProgressCreate builds an async CreateResult. RequestID is the native id —
// the Machines API hands out no separate operation handle, and inventing one
// would only be state to lose across a plugin restart.
func InProgressCreate(nativeID, msg string) *resource.CreateResult {
	return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusInProgress,
		NativeID:        nativeID,
		RequestID:       nativeID,
		StatusMessage:   msg,
	}}
}

// SuccessUpdate builds a synchronous-success UpdateResult carrying the updated
// properties, for the same reason as SuccessCreate.
func SuccessUpdate(nativeID string, props any) *resource.UpdateResult {
	return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
		Operation:          resource.OperationUpdate,
		OperationStatus:    resource.OperationStatusSuccess,
		NativeID:           nativeID,
		ResourceProperties: MustMarshal(props),
	}}
}

// InProgressUpdate builds an async UpdateResult.
func InProgressUpdate(nativeID, msg string) *resource.UpdateResult {
	return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
		Operation:       resource.OperationUpdate,
		OperationStatus: resource.OperationStatusInProgress,
		NativeID:        nativeID,
		RequestID:       nativeID,
		StatusMessage:   msg,
	}}
}

// SuccessDelete returns an (idempotent) success DeleteResult.
func SuccessDelete(nativeID string) *resource.DeleteResult {
	return &resource.DeleteResult{ProgressResult: &resource.ProgressResult{
		Operation:       resource.OperationDelete,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}}
}

// SuccessStatus reports a settled async operation with no properties to carry.
// Only for resources whose Create already returned them synchronously.
func SuccessStatus(nativeID string) *resource.StatusResult {
	return &resource.StatusResult{ProgressResult: &resource.ProgressResult{
		Operation:       resource.OperationCheckStatus,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
	}}
}

// SuccessStatusWithProps reports a settled async operation and carries the
// resource's properties. Async resources must use this: their Create returns
// InProgress with nothing to store, so Status is the only place the properties
// can reach formae — and until they do, every resolvable pointing at the
// resource is unresolvable. See the note on SuccessCreate.
func SuccessStatusWithProps(nativeID string, props any) *resource.StatusResult {
	return &resource.StatusResult{ProgressResult: &resource.ProgressResult{
		Operation:          resource.OperationCheckStatus,
		OperationStatus:    resource.OperationStatusSuccess,
		NativeID:           nativeID,
		ResourceProperties: MustMarshal(props),
	}}
}

// InProgressStatus reports an async operation that has not settled yet.
func InProgressStatus(nativeID, msg string) *resource.StatusResult {
	return &resource.StatusResult{ProgressResult: &resource.ProgressResult{
		Operation:       resource.OperationCheckStatus,
		OperationStatus: resource.OperationStatusInProgress,
		NativeID:        nativeID,
		RequestID:       nativeID,
		StatusMessage:   msg,
	}}
}

// NotFoundRead builds a NotFound ReadResult, the signal formae uses to prune a
// resource from inventory.
func NotFoundRead(resourceType string) *resource.ReadResult {
	return &resource.ReadResult{ResourceType: resourceType, ErrorCode: resource.OperationErrorCodeNotFound}
}

// FailRead builds a failed ReadResult.
func FailRead(resourceType string, code resource.OperationErrorCode) *resource.ReadResult {
	return &resource.ReadResult{ResourceType: resourceType, ErrorCode: code}
}

// OKRead builds a successful ReadResult from a properties value.
func OKRead(resourceType string, props any) *resource.ReadResult {
	return &resource.ReadResult{ResourceType: resourceType, Properties: string(MustMarshal(props))}
}
