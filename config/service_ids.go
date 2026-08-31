// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// ErrServiceIDConflict is returned when two non-empty service IDs in the
// incoming payload collide.
var ErrServiceIDConflict = errors.New("LLM service identity conflict")

// ValidateServiceIDUniqueness reports ErrServiceIDConflict when two non-empty
// IDs in services collide. Empty IDs are ignored so the caller can mint.
func ValidateServiceIDUniqueness(services []llm.ServiceConfig) error {
	seen := make(map[string]struct{}, len(services))
	for i := range services {
		id := services[i].ID
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("%w: service %q duplicates the ID of another entry in the payload", ErrServiceIDConflict, services[i].Name)
		}
		seen[id] = struct{}{}
	}
	return nil
}
