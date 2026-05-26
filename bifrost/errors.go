// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bifrost

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// bifrostErrorString builds a human-readable description of a *schemas.BifrostError,
// falling back through the available fields so that an empty Error.Message (which
// happens when the provider returns a body that doesn't match bifrost's error
// shape, or for transport/cancellation paths) doesn't leave the operator with
// an empty string.
//
// Priority:
//  1. Error.Message if non-empty.
//  2. The wrapped Go error's string (Error.Error.Error()) if set.
//  3. A synthesized string built from StatusCode, Type/Error.Type, and Error.Code.
//
// The result is always non-empty.
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
