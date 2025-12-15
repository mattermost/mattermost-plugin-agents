// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package types

import "github.com/mattermost/mattermost-plugin-ai/llm"

// ServerConfig interface defines common configuration methods for all server types
// This interface is in the types package to avoid circular dependencies
type ServerConfig interface {
	GetMMServerURL() string
	GetMMInternalServerURL() string
	GetDevMode() bool
	GetTrackAIGenerated() bool
	GetServiceByID(id string) (llm.ServiceConfig, bool)
}
