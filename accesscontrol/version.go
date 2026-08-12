// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import "github.com/Masterminds/semver/v3"

// MinServerVersionForABAC is the first Mattermost server version that ships
// the ABAC plugin APIs (EvaluateAccessControl and the access-control policy
// PAP methods). plugin.json's min_server_version is intentionally lower: the
// plugin runs on older servers with pure legacy access checks.
const MinServerVersionForABAC = "11.10.0"

// ServerSupportsABAC reports whether a server at the given version carries
// the ABAC plugin APIs. Selection must be version-based, never probe-based:
// a transient probe failure must never select passthrough (fail-open).
// An unparseable version errs toward supported so transport failures still
// deny (fail closed) rather than silently dropping enforcement.
func ServerSupportsABAC(serverVersion string) bool {
	v, err := semver.NewVersion(serverVersion)
	if err != nil {
		return true
	}
	return !v.LessThan(semver.MustParse(MinServerVersionForABAC))
}
