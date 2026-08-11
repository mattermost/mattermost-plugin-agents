// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package audit carries server audit records through context so that
// handlers and services can enrich the record the API middleware created,
// mirroring how attributes are added to the ambient OpenTelemetry span.
//
// The middleware in the api package creates the record, stores it in the
// request context via WithRecord, and logs it when the request finishes.
// Deeper layers call RecordFromContext and the nil-safe Add* helpers; when
// the operation is not audited these are no-ops.
package audit

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// recordKey types the context key under which the audit record is stashed.
// Unexported so callers must go through WithRecord / RecordFromContext.
type recordKey struct{}

// WithRecord returns a context carrying rec. A nil rec returns ctx unchanged.
func WithRecord(ctx context.Context, rec *model.AuditRecord) context.Context {
	if rec == nil {
		return ctx
	}
	return context.WithValue(ctx, recordKey{}, rec)
}

// RecordFromContext returns the audit record carried by ctx, or nil when the
// current operation is not audited.
func RecordFromContext(ctx context.Context) *model.AuditRecord {
	rec, _ := ctx.Value(recordKey{}).(*model.AuditRecord)
	return rec
}
