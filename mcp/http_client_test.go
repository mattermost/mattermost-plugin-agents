// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAuthorizationHeaderInCustomHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{name: "nil", headers: nil, want: false},
		{name: "empty", headers: map[string]string{}, want: false},
		{name: "Authorization", headers: map[string]string{"Authorization": "Bearer x"}, want: true},
		{name: "lowercase", headers: map[string]string{"authorization": "Bearer x"}, want: true},
		{name: "other only", headers: map[string]string{"X-Custom": "1"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, authorizationHeaderInCustomHeaders(tt.headers))
		})
	}
}
