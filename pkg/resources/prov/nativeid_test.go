// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package prov

import "testing"

func TestParseTwoPart(t *testing.T) {
	tests := []struct {
		id        string
		app       string
		child     string
		wantError bool
	}{
		{"my-api/17811943c9d489", "my-api", "17811943c9d489", false},
		{"my-api/vol_9x1kj4rz", "my-api", "vol_9x1kj4rz", false},
		{"my-api/secrets", "my-api", "secrets", false},
		// Certificate hostnames and IPv6 addresses land in the child segment
		// verbatim.
		{"my-api/api.example.com", "my-api", "api.example.com", false},
		{"my-api/2a09:8280:1::1:2b4c", "my-api", "2a09:8280:1::1:2b4c", false},
		{"my-api", "", "", true},
		{"/child", "", "", true},
		{"app/", "", "", true},
		{"", "", "", true},
	}
	for _, tt := range tests {
		app, child, err := ParseTwoPart(tt.id)
		if (err != nil) != tt.wantError {
			t.Errorf("ParseTwoPart(%q) err = %v, wantError %v", tt.id, err, tt.wantError)
			continue
		}
		if err != nil {
			continue
		}
		if app != tt.app || child != tt.child {
			t.Errorf("ParseTwoPart(%q) = (%q,%q), want (%q,%q)", tt.id, app, child, tt.app, tt.child)
		}
		if got := JoinTwoPart(app, child); got != tt.id {
			t.Errorf("JoinTwoPart round-trip = %q, want %q", got, tt.id)
		}
	}
}
