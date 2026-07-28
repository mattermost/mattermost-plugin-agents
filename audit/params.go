// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package audit

import (
	"github.com/mattermost/mattermost/server/public/model"
)

// AddParam adds an event parameter to rec. It is a no-op when rec is nil so
// call sites reached outside an audited request (RecordFromContext returned
// nil) need no guards. Only identifier-shaped values belong here; the type
// set intentionally excludes maps and arbitrary structs.
func AddParam[T string | bool | int | int64 | []string](rec *model.AuditRecord, key string, val T) {
	if rec == nil {
		return
	}
	model.AddEventParameterToAuditRec(rec, key, val)
}

// maxIDLength bounds identifier-shaped values recorded before validation.
// Real Mattermost IDs are 26 characters and usernames max out at 64; anything
// longer is a malformed request whose full content does not belong in the log.
const maxIDLength = 128

// TruncateID clamps a not-yet-validated identifier for audit recording so an
// oversized request body cannot pump arbitrary content into the audit log.
func TruncateID(s string) string {
	if len(s) <= maxIDLength {
		return s
	}
	return s[:maxIDLength] + "…(truncated)"
}
