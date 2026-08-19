// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// StructuredOutputCapability is the tri-state answer to "does this
// service/model combination accept a native JSON schema?". The tri-state
// matters: "unknown" is not "unsupported" for reporting purposes, but both
// keep the request on the prompt fallback path.
type StructuredOutputCapability string

const (
	// StructuredOutputCapabilityUnknown means support could not be established
	// (typically a model-dependent or operator-supplied endpoint).
	StructuredOutputCapabilityUnknown StructuredOutputCapability = "unknown"
	// StructuredOutputCapabilitySupported means the combination is positively
	// known to accept a native JSON schema.
	StructuredOutputCapabilitySupported StructuredOutputCapability = "supported"
	// StructuredOutputCapabilityUnsupported means the combination is known not
	// to accept a native JSON schema.
	StructuredOutputCapabilityUnsupported StructuredOutputCapability = "unsupported"
)

// StructuredOutputCapabilityResolver answers the capability question for a
// service and the model that would actually be used. It lives here rather than
// in bifrost/ so llm/ can consume the decision without importing bifrost,
// which imports llm.
type StructuredOutputCapabilityResolver func(svc ServiceConfig, model string) StructuredOutputCapability
