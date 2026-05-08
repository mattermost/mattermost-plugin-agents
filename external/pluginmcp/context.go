// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package pluginmcp

import "context"

// userIDKeyType is an unexported context key type so external packages cannot
// inject a fake user ID under the same key.
type userIDKeyType struct{}

var userIDKey = userIDKeyType{}

// GetUserID returns the Mattermost user ID extracted from X-Mattermost-UserID
// by ServeHTTP, or "" if none was set. Tool handlers should call this instead
// of reading headers directly.
func GetUserID(ctx context.Context) string {
	if v, ok := ctx.Value(userIDKey).(string); ok {
		return v
	}
	return ""
}

func withUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}
