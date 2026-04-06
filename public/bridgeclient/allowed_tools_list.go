// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"fmt"
)

// AllowedToolsList is a slice of AllowedToolRef with JSON that accepts either full
// objects {"server_origin","name"} or legacy string elements (tool name only).
type AllowedToolsList []AllowedToolRef

// UnmarshalJSON implements json.Unmarshaler.
func (l *AllowedToolsList) UnmarshalJSON(data []byte) error {
	*l = nil
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for i, item := range raw {
		if len(item) == 0 {
			continue
		}
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			*l = append(*l, AllowedToolRef{Name: s})
			continue
		}
		var ref AllowedToolRef
		if err := json.Unmarshal(item, &ref); err != nil {
			return fmt.Errorf("allowed_tools[%d]: %w", i, err)
		}
		*l = append(*l, ref)
	}
	return nil
}

// MarshalJSON implements json.Marshaler.
func (l AllowedToolsList) MarshalJSON() ([]byte, error) {
	return json.Marshal([]AllowedToolRef(l))
}
