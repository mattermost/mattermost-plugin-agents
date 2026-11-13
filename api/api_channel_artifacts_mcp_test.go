// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestTruncateUTF8(t *testing.T) {
	text, total, returned, hasMore := truncateUTF8("abcdefghij", 4)
	assert.Equal(t, "abcd", text)
	assert.Equal(t, 10, total)
	assert.Equal(t, 4, returned)
	assert.True(t, hasMore)

	text, total, returned, hasMore = truncateUTF8("hi", 0)
	assert.Equal(t, "hi", text)
	assert.Equal(t, 2, total)
	assert.Equal(t, 2, returned)
	assert.False(t, hasMore)
}

func TestHandleMCPListChannelArtifactsUnavailable(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	a := &API{config: &testConfigImpl{}}
	body, err := json.Marshal(MCPListChannelArtifactsRequest{ChannelID: model.NewId()})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, tools.ChannelArtifactsListPath, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Mattermost-User-Id", model.NewId())

	a.handleMCPListChannelArtifacts(c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var resp MCPListChannelArtifactsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Contains(t, resp.Error, "channel artifacts are not available")
}

func TestHandleMCPGetChannelArtifactUnavailable(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	a := &API{config: &testConfigImpl{}}
	body, err := json.Marshal(MCPGetChannelArtifactRequest{ArtifactID: model.NewId()})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, tools.ChannelArtifactsGetPath, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Mattermost-User-Id", model.NewId())

	a.handleMCPGetChannelArtifact(c)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.True(t, strings.Contains(rec.Body.String(), "channel artifacts are not available"))
}

func TestMCPEntryFrom(t *testing.T) {
	e := &channelartifacts.Entry{
		ID: model.NewId(), ChannelID: model.NewId(), Title: "T", Version: 3, SHA256: "abc",
	}
	out := mcpEntryFrom(e)
	assert.Equal(t, e.ID, out.ID)
	assert.Equal(t, e.Title, out.Title)
	assert.Equal(t, 3, out.Version)
}
