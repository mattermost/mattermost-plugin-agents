// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"testing"
)

func TestValidateID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name:    "valid 26 char uppercase ID",
			id:      "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
			wantErr: false,
		},
		{
			name:    "valid 26 char lowercase ID",
			id:      "abcdefghijklmnopqrstuvwxyz",
			wantErr: false,
		},
		{
			name:    "valid 26 char mixed case ID",
			id:      "AbCdEfGhIjKlMnOpQrStUvWxYz",
			wantErr: false,
		},
		{
			name:    "valid 26 char alphanumeric ID",
			id:      "ABC123DEF456GHI789JKL0MN12",
			wantErr: false,
		},
		{
			name:    "too short ID",
			id:      "ABCDEF",
			wantErr: true,
		},
		{
			name:    "too long ID",
			id:      "ABCDEFGHIJKLMNOPQRSTUVWXYZ123",
			wantErr: true,
		},
		{
			name:    "empty ID",
			id:      "",
			wantErr: true,
		},
		{
			name:    "ID with path traversal",
			id:      "../../../etc/passwd1234567",
			wantErr: true,
		},
		{
			name:    "ID with special characters",
			id:      "ABCDEFGHIJKLMNOPQRSTUV!@#$",
			wantErr: true,
		},
		{
			name:    "ID with spaces",
			id:      "ABCDEFGHIJKLMNOPQRSTUV    ",
			wantErr: true,
		},
		{
			name:    "ID with slashes",
			id:      "ABCDEFGHIJKLMNOPQRSTUV/XYZ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
