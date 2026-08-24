// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package utils

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T {
	return &v
}
