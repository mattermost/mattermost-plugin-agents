// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

// AccessMode represents the security access level for MCP operations
type AccessMode string

const (
	// AccessModeLocal indicates the mode has local filesystem access and can execute local operations
	AccessModeLocal AccessMode = "local"
	// AccessModeRemote indicates the mode operates over network and has security restrictions
	AccessModeRemote AccessMode = "remote"
)
