// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"fmt"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

// SettingsAccess lists the users a system admin has delegated access to. Read-only
// users see the plugin configuration without credentials; read/write users see
// and change all of it except these lists, and may turn on service account
// authentication for agents. Agent managers may edit and delete every agent, as
// the manage_others_agent permission allows, whether or not they are on the
// other two lists.
type SettingsAccess struct {
	ReadOnlyUserIDs     []string `json:"readOnlyUserIDs"`
	ReadWriteUserIDs    []string `json:"readWriteUserIDs"`
	AgentManagerUserIDs []string `json:"agentManagerUserIDs"`
}

// SettingsAccessLevel is how much of the configuration a user may reach.
type SettingsAccessLevel int

const (
	SettingsAccessNone SettingsAccessLevel = iota
	SettingsAccessRead
	SettingsAccessWrite
)

func (l SettingsAccessLevel) String() string {
	switch l {
	case SettingsAccessRead:
		return "read"
	case SettingsAccessWrite:
		return "write"
	default:
		return "none"
	}
}

// LevelFor returns the access the lists grant userID. Read/write wins when a
// user is on both lists.
func (s SettingsAccess) LevelFor(userID string) SettingsAccessLevel {
	if userID == "" {
		return SettingsAccessNone
	}
	if slices.Contains(s.ReadWriteUserIDs, userID) {
		return SettingsAccessWrite
	}
	if slices.Contains(s.ReadOnlyUserIDs, userID) {
		return SettingsAccessRead
	}
	return SettingsAccessNone
}

// ManagesAllAgents reports whether the agent managers list grants userID what
// the manage_others_agent permission does.
func (s SettingsAccess) ManagesAllAgents(userID string) bool {
	return userID != "" && slices.Contains(s.AgentManagerUserIDs, userID)
}

// Normalize validates every user ID, drops duplicates, and removes read/write
// users from the read-only list. Every list is non-nil afterwards.
func (s SettingsAccess) Normalize() (SettingsAccess, error) {
	readWrite, err := uniqueUserIDs(s.ReadWriteUserIDs)
	if err != nil {
		return SettingsAccess{}, err
	}
	readOnly, err := uniqueUserIDs(s.ReadOnlyUserIDs)
	if err != nil {
		return SettingsAccess{}, err
	}
	readOnly = slices.DeleteFunc(readOnly, func(id string) bool {
		return slices.Contains(readWrite, id)
	})
	agentManagers, err := uniqueUserIDs(s.AgentManagerUserIDs)
	if err != nil {
		return SettingsAccess{}, err
	}
	return SettingsAccess{ReadOnlyUserIDs: readOnly, ReadWriteUserIDs: readWrite, AgentManagerUserIDs: agentManagers}, nil
}

func uniqueUserIDs(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !model.IsValidId(id) {
			return nil, fmt.Errorf("invalid user ID %q", id)
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}
