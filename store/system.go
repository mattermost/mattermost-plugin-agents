// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// GetSystemValue retrieves a value from the Agents_System key-value table.
// Returns empty string if the key does not exist.
func (s *Store) GetSystemValue(key string) (string, error) {
	var value string
	err := s.db.Get(&value, "SELECT SValue FROM Agents_System WHERE SKey = $1", key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get system value for key %q: %w", key, err)
	}
	return value, nil
}

// SetSystemValue upserts a value in the Agents_System key-value table.
func (s *Store) SetSystemValue(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO Agents_System (SKey, SValue) VALUES ($1, $2)
		 ON CONFLICT (SKey) DO UPDATE SET SValue = $2`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("failed to set system value for key %q: %w", key, err)
	}
	return nil
}
