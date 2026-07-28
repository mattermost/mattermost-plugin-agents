// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// auditRecordGinKey is the gin context key under which the middleware stores
// the request's audit record for handler enrichment via auditRec.
const auditRecordGinKey = "auditRecord"

// auditMiddleware emits a server audit record for every route registered in
// the audit event registry. It is registered before authorization middleware,
// so denied requests (401/403) still produce fail records — the registry key,
// c.HandlerName(), is resolved at route match time regardless of aborts.
//
// The middleware creates and logs the record; handlers add object identifiers
// through auditRec (gin layer) or audit.RecordFromContext (service layer).
// Audit persistence is governed entirely by the server's audit configuration,
// so the record is emitted unconditionally.
func (a *API) auditMiddleware(pluginCtx *plugin.Context) gin.HandlerFunc {
	if pluginCtx == nil {
		pluginCtx = &plugin.Context{}
	}
	return func(c *gin.Context) {
		event, audited := a.auditEvents[c.HandlerName()]
		if !audited {
			c.Next()
			return
		}

		userID := c.GetHeader("Mattermost-User-Id")
		rec := plugin.MakeAuditRecordWithContext(event, model.AuditStatusFail, pluginCtx, userID, c.Request.URL.Path)

		// Carry the request's trace ID so an auditor can pivot from the
		// record to the full trace. The otelgin middleware runs first, so
		// the server span is already in the request context.
		if sc := trace.SpanFromContext(c.Request.Context()).SpanContext(); sc.HasTraceID() {
			rec.AddMeta(audit.MetaTraceID, sc.TraceID().String())
		}

		// Expose the record to handlers (gin context) and to services below
		// them (request context), mirroring ambient-span enrichment.
		c.Set(auditRecordGinKey, rec)
		c.Request = c.Request.WithContext(audit.WithRecord(c.Request.Context(), rec))

		// Emit from a defer so a panicking handler still produces a fail
		// record; the panic is re-raised for gin's Recovery middleware,
		// which turns it into the 500 response.
		defer func() {
			if r := recover(); r != nil {
				rec.AddErrorCode(http.StatusInternalServerError)
				rec.AddErrorDesc("panic during request handling")
				a.pluginAPI.Audit.Record(rec)
				panic(r)
			}

			status := c.Writer.Status()
			if status < 400 {
				// 3xx counts as success: OAuth start answers with a 302.
				rec.Success()
			} else {
				rec.AddErrorCode(status)
				if last := c.Errors.Last(); last != nil {
					// Clamped: bind errors and wrapped store errors can
					// quote fragments of request text, and the description
					// must never become an unbounded content channel.
					rec.AddErrorDesc(audit.TruncateDescription(last.Error()))
				}
			}

			a.pluginAPI.Audit.Record(rec)
		}()

		c.Next()
	}
}

// auditRec returns the audit record for the current request, or nil when the
// route is not audited. Handlers enrich it with object identifiers using the
// nil-safe audit.AddParam helper.
func auditRec(c *gin.Context) *model.AuditRecord {
	rec, _ := c.Value(auditRecordGinKey).(*model.AuditRecord)
	return rec
}
