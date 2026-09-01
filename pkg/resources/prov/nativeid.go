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

// ParseThreePart splits "{a}/{b}/{c}" into its three segments. Used by the
// resources that hang two levels deep: a Postgres extension lives in a database
// which lives in a cluster, and a volume snapshot lives in a volume which lives
// in an app.
func ParseThreePart(id string) (a, b, c string, err error) {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("native id must be {a}/{b}/{c}, got %q", id)
	}
	return parts[0], parts[1], parts[2], nil
}

// JoinThreePart formats a two-level-deep native id.
func JoinThreePart(a, b, c string) string {
	return a + "/" + b + "/" + c
}
