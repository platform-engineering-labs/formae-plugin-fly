// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package fly

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// APIError represents a non-2xx response from the Fly.io Machines API.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	detail := e.Message
	if detail == "" {
		detail = e.Body
	}
	return fmt.Sprintf("fly API: HTTP %d: %s", e.StatusCode, detail)
}

// extractMessage best-effort pulls the error text out of a Fly error body.
//
// api.machines.dev fronts more than one upstream and they disagree on the field
// name: the apps/certificates/IP service answers {"message":"Unauthorized"},
// flaps (machines/volumes/secrets) answers
// {"error":"Authenticate: token validation error"}. Probe both, plus "msg".
// Returns "" for a non-JSON body such as the router's bare "404 page not found".
func extractMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Message string `json:"message"`
		Error   string `json:"error"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	switch {
	case probe.Message != "":
		return probe.Message
	case probe.Error != "":
		return probe.Error
	case probe.Msg != "":
		return probe.Msg
	}
	return ""
}

// notFoundPhrases are the message fragments flaps uses when it reports a
// missing app or machine as HTTP 400 instead of 404.
var notFoundPhrases = []string{
	"could not find app",
	"not found",
	"does not exist",
	"unknown app",
}

// IsNotFound reports whether err means the resource is gone.
//
// Beyond a plain 404, this matches HTTP 400 whose message says the app or
// machine is missing. Flaps genuinely does that for operations against a
// destroyed app, and without the extra match a child resource whose app was
// deleted out of band never leaves the inventory: sync reads the 400,
// classifies it as InvalidRequest, and never prunes. The match is on the
// message and not the bare status because a real malformed request is also a
// 400 — "invalid guest size" must stay InvalidRequest.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode == 404 {
		return true
	}
	if apiErr.StatusCode != 400 {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	for _, phrase := range notFoundPhrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// ClassifyStatus maps an HTTP status to a formae operation error code.
func ClassifyStatus(status int) resource.OperationErrorCode {
	switch {
	case status == 400, status == 412, status == 422:
		// 412 is a failed machine-lease precondition; 422 is Fly's validation
		// failure, including a duplicate app name.
		return resource.OperationErrorCodeInvalidRequest
	case status == 401:
		return resource.OperationErrorCodeInvalidCredentials
	case status == 403:
		// Valid token, insufficient scope — a read-only or app-scoped token.
		return resource.OperationErrorCodeAccessDenied
	case status == 404:
		return resource.OperationErrorCodeNotFound
	case status == 409:
		return resource.OperationErrorCodeAlreadyExists
	case status == 429:
		return resource.OperationErrorCodeThrottling
	case status >= 500 && status <= 599:
		return resource.OperationErrorCodeServiceInternalError
	default:
		return resource.OperationErrorCodeInternalFailure
	}
}

// ClassifyError maps an error from Do() to a formae operation error code.
func ClassifyError(err error) resource.OperationErrorCode {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if IsNotFound(err) {
			return resource.OperationErrorCodeNotFound
		}
		return ClassifyStatus(apiErr.StatusCode)
	}
	return resource.OperationErrorCodeInternalFailure
}
