// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package websearch

import (
	"errors"
	"net/url"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// failSpanFixed marks the span as failed using a fixed message. Spans must never
// carry request content or credentials, so callers must not pass error text.
func failSpanFixed(span trace.Span, msg string) {
	span.RecordError(errors.New(msg))
	span.SetStatus(codes.Error, msg)
}

// redactTransportError drops the query string (search terms, API keys) from the
// URL embedded in transport errors so the error is safe to log.
func redactTransportError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	redacted := *urlErr
	if u, parseErr := url.Parse(urlErr.URL); parseErr == nil {
		u.RawQuery = ""
		u.User = nil
		redacted.URL = u.String()
	} else {
		redacted.URL = "[redacted]"
	}
	return &redacted
}
