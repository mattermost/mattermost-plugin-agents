// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"fmt"
	"unicode"
)

// ValidateID validates that an ID matches the expected Mattermost ID format.
// Valid IDs are 26 characters long containing only letters A-Z/a-z and digits 0-9
// (zbase32-encoded UUID v4 without padding).
func ValidateID(id string) error {
	if len(id) != 26 {
		return fmt.Errorf("invalid ID length: expected 26, got %d", len(id))
	}
	for _, c := range id {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			return fmt.Errorf("invalid ID character: %c", c)
		}
	}
	return nil
}
