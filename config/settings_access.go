// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"fmt"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

// SettingsAccess lists the users a system admin has delegated access to.
// Read/write users see and change all of the plugin configuration except these
// lists, and may turn on service account authentication for agents. Agent
// managers may edit and delete every agent, as the manage_others_agent
// permission allows, whether or not they are on the read/write list.
type SettingsAccess struct {
	ReadWriteUserIDs    []string `json:"readWriteUserIDs"`
	AgentManagerUserIDs []string `json:"agentManagerUserIDs"`
}

// SettingsAccessLevel is how much of the configuration a user may reach.
type SettingsAccessLevel int

const (
	SettingsAccessNone SettingsAccessLevel = iota
	SettingsAccessWrite
)

func (l SettingsAccessLevel) String() string {
	switch l {
	case SettingsAccessWrite:
		return "write"
	default:
		return "none"
	}
}

// LevelFor returns the access the lists grant userID.
func (s SettingsAccess) LevelFor(userID string) SettingsAccessLevel {
	if userID == "" {
		return SettingsAccessNone
	}
	if slices.Contains(s.ReadWriteUserIDs, userID) {
		return SettingsAccessWrite
	}
	return SettingsAccessNone
}

// ManagesAllAgents reports whether the agent managers list grants userID what
// the manage_others_agent permission does.
func (s SettingsAccess) ManagesAllAgents(userID string) bool {
	return userID != "" && slices.Contains(s.AgentManagerUserIDs, userID)
}

// Normalize validates every user ID and drops duplicates. Every list is non-nil
// afterwards.
func (s SettingsAccess) Normalize() (SettingsAccess, error) {
	readWrite, err := uniqueUserIDs(s.ReadWriteUserIDs)
	if err != nil {
		return SettingsAccess{}, err
	}
	agentManagers, err := uniqueUserIDs(s.AgentManagerUserIDs)
	if err != nil {
		return SettingsAccess{}, err
	}
	return SettingsAccess{ReadWriteUserIDs: readWrite, AgentManagerUserIDs: agentManagers}, nil
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
