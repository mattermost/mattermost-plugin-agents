// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
)

func TestMCPChannelArtifactErrorStatus(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: channelartifacts.ErrForbidden, want: http.StatusForbidden},
		{err: fmt.Errorf("read: %w", channelartifacts.ErrForbidden), want: http.StatusForbidden},
		{err: channelartifacts.ErrNotFound, want: http.StatusNotFound},
		{err: channelartifacts.ErrSessionRequired, want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			(&API{}).writeMCPChannelArtifactError(c, tt.err)
			assert.Equal(t, tt.want, recorder.Code)
		})
	}
}
