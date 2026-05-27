// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// bifrostErrorString returns a non-empty description of a bifrost error.
// Error.Message is blank when the provider response body doesn't match bifrost's
// expected error shape, and on transport/cancellation paths — so fall back to
// the wrapped Go error, then to status/type/code, before giving up.
func bifrostErrorString(bifrostErr *schemas.BifrostError) string {
	if bifrostErr == nil {
		return "<nil bifrost error>"
	}

	if bifrostErr.Error != nil {
		if msg := strings.TrimSpace(bifrostErr.Error.Message); msg != "" {
			return msg
		}
		if bifrostErr.Error.Error != nil {
			if msg := strings.TrimSpace(bifrostErr.Error.Error.Error()); msg != "" {
				return msg
			}
		}
	}

	var parts []string
	if bifrostErr.StatusCode != nil {
		parts = append(parts, fmt.Sprintf("status=%d", *bifrostErr.StatusCode))
	}
	if t := errorType(bifrostErr); t != "" {
		parts = append(parts, fmt.Sprintf("type=%s", t))
	}
	if bifrostErr.Error != nil && bifrostErr.Error.Code != nil && *bifrostErr.Error.Code != "" {
		parts = append(parts, fmt.Sprintf("code=%s", *bifrostErr.Error.Code))
	}

	if len(parts) == 0 {
		return "empty bifrost error"
	}
	return "empty bifrost error (" + strings.Join(parts, " ") + ")"
}

func errorType(bifrostErr *schemas.BifrostError) string {
	if bifrostErr.Error != nil && bifrostErr.Error.Type != nil && *bifrostErr.Error.Type != "" {
		return *bifrostErr.Error.Type
	}
	if bifrostErr.Type != nil && *bifrostErr.Type != "" {
		return *bifrostErr.Type
	}
	return ""
}
