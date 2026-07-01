// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

// requireID returns an error when id is not a syntactically valid Mattermost ID.
// field is the argument name and is embedded in the error so the model can tell
// which parameter was malformed.
func requireID(field, id string) error {
	if !model.IsValidId(id) {
		return fmt.Errorf("%s must be a valid ID", field)
	}
	return nil
}

// optionalID is like requireID but treats an empty value as valid, for arguments
// that may be omitted.
func optionalID(field, id string) error {
	if id == "" {
		return nil
	}
	return requireID(field, id)
}
