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

// SuccessCreate builds a synchronous-success CreateResult.
func SuccessCreate(nativeID string) *resource.CreateResult {
	return &resource.CreateResult{ProgressResult: &resource.ProgressResult{
		Operation:       resource.OperationCreate,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
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

// SuccessUpdate builds a synchronous-success UpdateResult.
func SuccessUpdate(nativeID string) *resource.UpdateResult {
	return &resource.UpdateResult{ProgressResult: &resource.ProgressResult{
		Operation:       resource.OperationUpdate,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
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

// SuccessStatus reports a settled async operation.
func SuccessStatus(nativeID string) *resource.StatusResult {
	return &resource.StatusResult{ProgressResult: &resource.ProgressResult{
		Operation:       resource.OperationCheckStatus,
		OperationStatus: resource.OperationStatusSuccess,
		NativeID:        nativeID,
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
