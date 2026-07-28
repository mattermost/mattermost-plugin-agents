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
