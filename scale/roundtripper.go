// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scale

import "net/http"

// RoundTripper is a custom http.RoundTripper that replaces the standard
// Authorization header (set by the OpenAI SDK) with Scale's custom auth headers:
//   - x-api-key: <apiKey>
//   - x-selected-account-id: <accountID> (if non-empty, required for ScaleGov)
type RoundTripper struct {
	Base      http.RoundTripper
	APIKey    string
	AccountID string
}

func (t *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid mutating the original
	clone := req.Clone(req.Context())

	// Remove the Authorization header added by the OpenAI SDK
	clone.Header.Del("Authorization")

	// Set Scale's custom auth headers
	clone.Header.Set("x-api-key", t.APIKey)
	if t.AccountID != "" {
		clone.Header.Set("x-selected-account-id", t.AccountID)
	}

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}
