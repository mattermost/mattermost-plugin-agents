// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildLLMContextUserRequestGenerativeUI(t *testing.T) {
	tests := []struct {
		name     string
		enabled  func() bool
		expected bool
	}{
		{name: "unset", enabled: nil, expected: false},
		{name: "disabled", enabled: func() bool { return false }, expected: false},
		{name: "enabled", enabled: func() bool { return true }, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newTestBuilder(t, &emptyToolProvider{}, nil)
			if tt.enabled != nil {
				builder.SetGenerativeUIEnabled(tt.enabled)
			}

			ctx := builder.BuildLLMContextUserRequest(newTestBot(), testUser(), nil)
			assert.Equal(t, tt.expected, ctx.GenerativeUI)
		})
	}
}

func TestBuildLLMContextUserRequestGenerativeUIFollowsConfigChanges(t *testing.T) {
	enabled := false
	builder := newTestBuilder(t, &emptyToolProvider{}, nil)
	builder.SetGenerativeUIEnabled(func() bool { return enabled })

	assert.False(t, builder.BuildLLMContextUserRequest(newTestBot(), testUser(), nil).GenerativeUI)

	enabled = true
	assert.True(t, builder.BuildLLMContextUserRequest(newTestBot(), testUser(), nil).GenerativeUI)
}
