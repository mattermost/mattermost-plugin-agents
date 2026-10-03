// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// StructuredOutputCapabilityResolver answers "is this service/model
// combination positively known to accept a native JSON schema?" for the model
// that would actually be used. Anything short of positive knowledge is false,
// which routes the request to the prompt fallback. It lives here rather than in
// bifrost/ so llm/ can consume the decision without importing bifrost, which
// imports llm.
type StructuredOutputCapabilityResolver func(svc ServiceConfig, model string) bool
