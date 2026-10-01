// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/conversations"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	mmapimocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	"github.com/mattermost/mattermost-plugin-agents/v2/search"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	searchTestTeamID    = "searchteam0000000000000000"
	searchTestChannelID = "searchchannel0000000000000"
)

func licensedSearchService(getSearch func() embeddings.EmbeddingSearch, client mmapi.Client) *search.Search {
	return search.New(getSearch, client, enterprisetest.CheckerAt(enterprise.LevelEnterprise))
}

func enabledSearchService(t *testing.T) *search.Search {
	me := mocks.NewMockEmbeddingSearch(t)
	return licensedSearchService(func() embeddings.EmbeddingSearch { return me }, nil)
}

// useSearchConversations swaps in a conversations service backed by client so
// requests that pass validation reach the Mattermost client.
func (e *TestEnvironment) useSearchConversations(client mmapi.Client) {
	e.api.conversationsService = conversations.New(e.api.prompts, client, nil, e.api.contextBuilder, e.bots, nil, nil, nil, nil, e.config)
}

func postSearchRun(e *TestEnvironment, body []byte, botUsername string) *http.Response {
	request := httptest.NewRequest(http.MethodPost, "/search/run?botUsername="+botUsername, bytes.NewReader(body))
	request.Header.Add("Mattermost-User-ID", testUserID)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	e.api.ServeHTTP(&plugin.Context{}, recorder, request)
	return recorder.Result()
}

func TestHandleRunSearch(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name           string
		setup          func(t *testing.T, e *TestEnvironment)
		requestBody    SearchRequest
		expectedStatus int
	}{
		{
			name:           "empty query",
			requestBody:    SearchRequest{Query: "   "},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "query exceeds max length",
			requestBody:    SearchRequest{Query: strings.Repeat("a", maxSearchQueryLength+1)},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "malformed team ID",
			requestBody:    SearchRequest{Query: "test query", TeamID: "team123"},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "malformed channel ID",
			requestBody:    SearchRequest{Query: "test query", ChannelID: "channel123"},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "user cannot view team",
			setup: func(_ *testing.T, e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToTeam", testUserID, searchTestTeamID, model.PermissionViewTeam).Return(false)
			},
			requestBody:    SearchRequest{Query: "test query", TeamID: searchTestTeamID},
			expectedStatus: http.StatusForbidden,
		},
		{
			name: "user cannot read channel",
			setup: func(_ *testing.T, e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToChannel", testUserID, searchTestChannelID, model.PermissionReadChannel).Return(false)
			},
			requestBody:    SearchRequest{Query: "test query", ChannelID: searchTestChannelID},
			expectedStatus: http.StatusForbidden,
		},
		{
			name: "question post cannot be created",
			setup: func(t *testing.T, e *TestEnvironment) {
				e.mockAPI.On("HasPermissionToTeam", testUserID, searchTestTeamID, model.PermissionViewTeam).Return(true)
				e.mockAPI.On("GetTeam", searchTestTeamID).Return(&model.Team{Id: searchTestTeamID, Name: "team"}, nil)
				e.mockAPI.On("HasPermissionToChannel", testUserID, searchTestChannelID, model.PermissionReadChannel).Return(true)
				e.mockAPI.On("GetChannel", searchTestChannelID).Return(&model.Channel{Id: searchTestChannelID}, nil)
				e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID}, nil)

				client := mmapimocks.NewMockClient(t)
				client.On("DM", testUserID, mock.Anything, mock.Anything).Return(errors.New("DM failed"))
				e.useSearchConversations(client)
			},
			requestBody:    SearchRequest{Query: "test query", TeamID: searchTestTeamID, ChannelID: searchTestChannelID},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.searchService = enabledSearchService(t)
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
			if test.setup != nil {
				test.setup(t, e)
			}

			bodyBytes, err := json.Marshal(test.requestBody)
			require.NoError(t, err)

			require.Equal(t, test.expectedStatus, postSearchRun(e, bodyBytes, "test-bot").StatusCode)
		})
	}
}

// The search conversation uses keyword search through search_posts, so it
// must start even when embedding search is not configured.
func TestHandleRunSearchWithoutEmbeddingSearch(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name          string
		searchService *search.Search
	}{
		{name: "embedding search not configured", searchService: search.New(nil, nil, enterprisetest.CheckerAt(enterprise.LevelEnterprise))},
		{name: "no search service", searchService: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.searchService = test.searchService
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})
			e.mockAPI.On("GetUser", testUserID).Return(&model.User{Id: testUserID}, nil)

			const dmChannelID = "searchdmchannel00000000000"
			client := mmapimocks.NewMockClient(t)
			client.On("DM", testUserID, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				post := args.Get(2).(*model.Post)
				post.Id = model.NewId()
				post.ChannelId = dmChannelID
			}).Return(nil)
			client.On("GetChannel", dmChannelID).Return(&model.Channel{Id: dmChannelID, Type: model.ChannelTypeDirect}, nil)
			// Stop the background answer at its first step; this test only
			// covers whether the search starts.
			client.On("CreatePost", mock.Anything).Return(errors.New("stop"))
			backgroundDone := make(chan struct{})
			client.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Run(func(mock.Arguments) { close(backgroundDone) }).Return()
			e.useSearchConversations(client)

			bodyBytes, err := json.Marshal(SearchRequest{Query: "test query"})
			require.NoError(t, err)

			resp := postSearchRun(e, bodyBytes, "test-bot")
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var body map[string]string
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, dmChannelID, body["channelid"])
			require.NotEmpty(t, body["postid"])

			select {
			case <-backgroundDone:
			case <-time.After(10 * time.Second):
				t.Fatal("background search never ran")
			}
		})
	}
}

func TestHandleRunSearchMalformedJSON(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name        string
		requestBody string
	}{
		{name: "completely invalid JSON", requestBody: "this is not json at all"},
		{name: "truncated JSON", requestBody: `{"query": "test`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.searchService = enabledSearchService(t)
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})

			require.Equal(t, http.StatusBadRequest, postSearchRun(e, []byte(test.requestBody), "test-bot").StatusCode)
		})
	}
}

func TestHandleRunSearchMissingUserHeader(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	tests := []struct {
		name    string
		headers map[string]string
	}{
		{name: "missing Mattermost-User-Id header", headers: map[string]string{}},
		{name: "empty Mattermost-User-Id header", headers: map[string]string{"Mattermost-User-Id": ""}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := SetupTestEnvironment(t)
			defer e.Cleanup(t)

			e.api.searchService = enabledSearchService(t)
			e.setupTestBot(llm.BotConfig{Name: "test-bot", DisplayName: "Test Bot"})

			bodyBytes, err := json.Marshal(SearchRequest{Query: "test query"})
			require.NoError(t, err)

			request := httptest.NewRequest(http.MethodPost, "/search/run?botUsername=test-bot", bytes.NewReader(bodyBytes))
			request.Header.Set("Content-Type", "application/json")
			for k, v := range test.headers {
				request.Header.Set(k, v)
			}
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, request)

			require.Equal(t, http.StatusUnauthorized, recorder.Result().StatusCode)
		})
	}
}

func TestHandleRunSearchEnforcesUsageRestrictions(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	e.api.searchService = enabledSearchService(t)
	e.setupTestBot(llm.BotConfig{
		Name:            "restricted-bot",
		DisplayName:     "Restricted Bot",
		UserAccessLevel: llm.UserAccessLevelBlock,
		UserIDs:         []string{testUserID},
	})

	bodyBytes, err := json.Marshal(SearchRequest{Query: "test query"})
	require.NoError(t, err)

	require.Equal(t, http.StatusForbidden, postSearchRun(e, bodyBytes, "restricted-bot").StatusCode)
}
