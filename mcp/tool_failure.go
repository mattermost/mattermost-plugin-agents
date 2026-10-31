// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
)

// Tool call failure classes. They are a fixed vocabulary so a class never
// carries request or response content.
const (
	toolFailureReturnedError    = "tool_returned_error"
	toolFailureEmptyResult      = "empty_result"
	toolFailureNotConnected     = "not_connected"
	toolFailureContextCanceled  = "context_canceled"
	toolFailureDeadlineExceeded = "deadline_exceeded"
	toolFailureUnauthorized     = "unauthorized"
	toolFailureOAuthExchange    = "oauth_token_error"
	toolFailureHTTPStatus       = "http_status"
	toolFailureJSONRPC          = "jsonrpc_error"
	toolFailureConnectionClosed = "connection_closed"
	toolFailureSessionMissing   = "session_missing"
	toolFailureNetworkTimeout   = "network_timeout"
	toolFailureNetwork          = "network_error"
	toolFailureUnknown          = "unknown"
)

// classifiedError is a tool call failure the client produced itself, carrying
// the class it is reported under. Error() is the text callers already see.
type classifiedError struct {
	class string
	msg   string
}

func (e *classifiedError) Error() string { return e.msg }

// toolFailure is the safe summary of a failed tool call for logs and spans.
// Its fields come from a fixed vocabulary, a numeric code, or a Go type name,
// never from error text.
type toolFailure struct {
	class string
	// code is the HTTP status or JSON-RPC error code, 0 when there is none.
	code int
	// errType is the Go type of the innermost error, set only for unknown.
	errType string
}

// Error is the class: the span records it in place of the underlying error,
// whose text can echo the upstream response.
func (f *toolFailure) Error() string { return f.class }

func (f *toolFailure) logFields() []any {
	fields := []any{"errorClass", f.class}
	if f.code != 0 {
		fields = append(fields, "errorCode", f.code)
	}
	if f.errType != "" {
		fields = append(fields, "errorType", f.errType)
	}
	return fields
}

// classifyToolCallError reduces err to a toolFailure without reading its text
// beyond the fixed status phrases in httpStatusFromError.
func classifyToolCallError(err error) *toolFailure {
	if classified, ok := errors.AsType[*classifiedError](err); ok {
		return &toolFailure{class: classified.class}
	}
	if errors.Is(err, context.Canceled) {
		return &toolFailure{class: toolFailureContextCanceled}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &toolFailure{class: toolFailureDeadlineExceeded}
	}
	if _, ok := errors.AsType[*mcpUnauthorized](err); ok {
		return &toolFailure{class: toolFailureUnauthorized}
	}
	if retrieveErr, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
		failure := &toolFailure{class: toolFailureOAuthExchange}
		if retrieveErr.Response != nil {
			failure.code = retrieveErr.Response.StatusCode
		}
		return failure
	}
	if code := httpStatusFromError(err); code != 0 {
		return &toolFailure{class: toolFailureHTTPStatus, code: code}
	}
	if rpcErr, ok := errors.AsType[*jsonrpc.Error](err); ok {
		return &toolFailure{class: toolFailureJSONRPC, code: int(rpcErr.Code)}
	}
	if errors.Is(err, mcp.ErrSessionMissing) {
		return &toolFailure{class: toolFailureSessionMissing}
	}
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return &toolFailure{class: toolFailureConnectionClosed}
	}
	if netErr, ok := errors.AsType[net.Error](err); ok {
		if netErr.Timeout() {
			return &toolFailure{class: toolFailureNetworkTimeout}
		}
		return &toolFailure{class: toolFailureNetwork}
	}
	return &toolFailure{class: toolFailureUnknown, errType: innermostErrorType(err)}
}

// httpStatusFromError returns the HTTP status the MCP SDK reports for a
// rejected request. The SDK exposes no typed status, only the status phrase at
// the end of the message, so the phrase is matched against the fixed set of
// standard statuses and only the matching number is returned.
func httpStatusFromError(err error) int {
	msg := err.Error()
	for code := http.StatusBadRequest; code <= http.StatusNetworkAuthenticationRequired; code++ {
		if text := http.StatusText(code); text != "" && strings.HasSuffix(msg, ": "+text) {
			return code
		}
	}
	return 0
}

// innermostErrorType names the Go type at the bottom of err's wrap chain.
func innermostErrorType(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			break
		}
		err = next
	}
	return fmt.Sprintf("%T", err)
}

// maxLoggedToolNameLen bounds the tool name in a log line; the name comes from
// the remote server's tool list.
const maxLoggedToolNameLen = 128

func loggableToolName(name string) string {
	if len(name) > maxLoggedToolNameLen {
		name = name[:maxLoggedToolNameLen]
	}
	return llm.SanitizeNonPrintableChars(name)
}

// recordToolCallFailure reports a failed tool call on span and, for remote and
// plugin servers, as a debug log line. Only the classification leaves this
// function: the error text, tool arguments, and result content are never read
// into the log or the span. The embedded server logs its own calls.
func (c *Client) recordToolCallFailure(span trace.Span, toolName string, err error) {
	failure := classifyToolCallError(err)

	span.SetAttributes(telemetry.MCPErrorClass.String(failure.class))
	if failure.code != 0 {
		span.SetAttributes(telemetry.MCPErrorCode.Int(failure.code))
	}
	span.RecordError(failure)
	span.SetStatus(codes.Error, failure.class)

	if c.config.Name == EmbeddedClientKey {
		return
	}
	fields := []any{"userID", c.userID, "serverID", c.config.Name, "tool", loggableToolName(toolName)}
	c.log.Debug("MCP tool call failed", append(fields, failure.logFields()...)...)
}
