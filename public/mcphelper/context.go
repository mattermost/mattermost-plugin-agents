// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcphelper

import "context"

// userIDKeyType is an unexported struct type used as the context key for the
// authenticated Mattermost user ID. Using a typed unexported struct (rather
// than a string) means no external package can stuff a fake user ID into the
// context under the same key.
type userIDKeyType struct{}

var userIDKey = userIDKeyType{}

// GetUserID returns the Mattermost user ID of the user whose request is being
// processed, extracted from the X-Mattermost-UserID header by ServeHTTP. It
// returns the empty string if no user ID was present (which can happen in
// unit tests that bypass ServeHTTP).
//
// Tool handlers call GetUserID to know which Mattermost user is invoking the
// tool — use this instead of parsing headers directly.
func GetUserID(ctx context.Context) string {
	if v, ok := ctx.Value(userIDKey).(string); ok {
		return v
	}
	return ""
}

// withUserID returns a derived context that carries the given user ID. Only
// ServeHTTP calls this; it is unexported to keep the injection point
// controlled.
func withUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}
