// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"fmt"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

// SettingsAccess lists the users a system admin has delegated access to the
// plugin configuration. Read-only users see it without credentials; read/write
// users see and change all of it except this list.
type SettingsAccess struct {
	ReadOnlyUserIDs  []string `json:"readOnlyUserIDs"`
	ReadWriteUserIDs []string `json:"readWriteUserIDs"`
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

// Normalize validates every user ID, drops duplicates, and removes read/write
// users from the read-only list. Both lists are non-nil afterwards.
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
	return SettingsAccess{ReadOnlyUserIDs: readOnly, ReadWriteUserIDs: readWrite}, nil
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
