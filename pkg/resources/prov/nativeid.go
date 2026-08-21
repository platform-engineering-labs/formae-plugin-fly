// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package prov

import (
	"fmt"
	"strings"
)

// Fly IDs are only unique within an app, so every app-scoped resource carries
// the app name in its native id: "{app_name}/{child_id}".
//
// SplitN with n=2 on purpose: a certificate hostname or an IPv6 address can
// contain no "/", but keeping the tail unsplit means a future child id that
// does contain one still round-trips.

// ParseTwoPart splits "{app}/{child}" into its two segments. Either segment
// empty is an error.
func ParseTwoPart(id string) (app, child string, err error) {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("native id must be {app}/{child}, got %q", id)
	}
	return parts[0], parts[1], nil
}

// JoinTwoPart formats an app-scoped native id.
func JoinTwoPart(app, child string) string {
	return app + "/" + child
}
