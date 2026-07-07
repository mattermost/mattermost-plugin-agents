// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/v2/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var rootDSN = "postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable"

func setupCustomPromptsTestEnvironment(t *testing.T) (*TestEnvironment, *customprompts.Store) {
	t.Helper()

	if dsn := os.Getenv("PG_ROOT_DSN"); dsn != "" {
		rootDSN = dsn
	}

	rootDB, err := sqlx.Connect("postgres", rootDSN)
	if err != nil {
		t.Skipf("PostgreSQL not available, skipping integration test: %v", err)
	}
	defer rootDB.Close()

	dbName := fmt.Sprintf("customprompts_api_test_%d", model.GetMillis())
	_, err = rootDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err)

	testDSN := fmt.Sprintf("postgres://mmuser:mostest@localhost:5432/%s?sslmode=disable", dbName)
	db, err := sqlx.Connect("postgres", testDSN)
	require.NoError(t, err)

	t.Cleanup(func() {
		db.Close()
		rootConn, connErr := sqlx.Connect("postgres", rootDSN)
		if connErr != nil {
			return
		}
		defer rootConn.Close()
		_, _ = rootConn.Exec("DROP DATABASE " + dbName)
	})

	s := store.New(db)
	err = s.RunMigrations()
	require.NoError(t, err)

	dbClient := mmapi.NewTestDBClient(db)
	cpStore := customprompts.NewStore(dbClient)

	env := SetupTestEnvironment(t)
	env.api.customPromptsStore = cpStore

	return env, cpStore
}

func createTestPrompt(t *testing.T, cpStore *customprompts.Store, creatorID string, shared bool) customprompts.CustomPrompt {
	t.Helper()
	prompt, err := cpStore.Create(customprompts.CustomPrompt{
		CreatorID:   creatorID,
		Name:        "Test Prompt " + model.NewId()[:8],
		Description: "test",
		Template:    "hello",
		IsShared:    shared,
	})
	require.NoError(t, err)
	return prompt
}

func TestCustomPromptOwnership(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e, cpStore := setupCustomPromptsTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("LogError", mock.Anything).Maybe()

	t.Run("owner can update", func(t *testing.T) {
		prompt := createTestPrompt(t, cpStore, testUserID, false)
		req := httptest.NewRequest(http.MethodPut, "/custom-prompts/"+prompt.ID, strings.NewReader(`{"name":"Updated","description":"updated","template":"hi"}`))
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusNoContent, recorder.Code)
	})

	t.Run("non-owner cannot update", func(t *testing.T) {
		prompt := createTestPrompt(t, cpStore, testUserID, false)
		req := httptest.NewRequest(http.MethodPut, "/custom-prompts/"+prompt.ID, strings.NewReader(`{"name":"Hacked","description":"hacked","template":"hacked"}`))
		req.Header.Set("Mattermost-User-Id", testOtherUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	})

	t.Run("update nonexistent prompt", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/custom-prompts/"+model.NewId(), strings.NewReader(`{"name":"Ghost","description":"ghost","template":"ghost"}`))
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	})

	t.Run("owner can delete", func(t *testing.T) {
		prompt := createTestPrompt(t, cpStore, testUserID, false)
		req := httptest.NewRequest(http.MethodDelete, "/custom-prompts/"+prompt.ID, nil)
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusNoContent, recorder.Code)
	})

	t.Run("non-owner cannot delete", func(t *testing.T) {
		prompt := createTestPrompt(t, cpStore, testUserID, false)
		req := httptest.NewRequest(http.MethodDelete, "/custom-prompts/"+prompt.ID, nil)
		req.Header.Set("Mattermost-User-Id", testOtherUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	})

	t.Run("delete nonexistent prompt", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/custom-prompts/"+model.NewId(), nil)
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	})
}

func TestCustomPromptRenderVisibility(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e, cpStore := setupCustomPromptsTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("LogError", mock.Anything).Maybe()

	// Set up prompts for render to work: needs a.prompts to be non-nil
	prompts, err := llm.NewPrompts(fstest.MapFS{
		"empty.tmpl": &fstest.MapFile{Data: []byte("")},
	})
	require.NoError(t, err)
	e.api.prompts = prompts

	t.Run("owner can render own private prompt", func(t *testing.T) {
		prompt := createTestPrompt(t, cpStore, testUserID, false)
		e.mockAPI.On("GetUser", testUserID).Return(&model.User{
			Id:       testUserID,
			Username: "testuser",
		}, nil).Maybe()

		req := httptest.NewRequest(http.MethodPost, "/custom-prompts/"+prompt.ID+"/render", strings.NewReader(`{}`))
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)

		var resp map[string]string
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
		_, ok := resp["rendered"]
		require.True(t, ok, "response should contain 'rendered' key")
	})

	t.Run("any user can render shared prompt", func(t *testing.T) {
		prompt := createTestPrompt(t, cpStore, testUserID, true)
		e.mockAPI.On("GetUser", testOtherUserID).Return(&model.User{
			Id:       testOtherUserID,
			Username: "otheruser",
		}, nil).Maybe()

		req := httptest.NewRequest(http.MethodPost, "/custom-prompts/"+prompt.ID+"/render", strings.NewReader(`{}`))
		req.Header.Set("Mattermost-User-Id", testOtherUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code)

		var resp map[string]string
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&resp))
		_, ok := resp["rendered"]
		require.True(t, ok, "response should contain 'rendered' key")
	})

	t.Run("non-owner cannot render private prompt", func(t *testing.T) {
		prompt := createTestPrompt(t, cpStore, testUserID, false)
		req := httptest.NewRequest(http.MethodPost, "/custom-prompts/"+prompt.ID+"/render", strings.NewReader(`{}`))
		req.Header.Set("Mattermost-User-Id", testOtherUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	})

	t.Run("render nonexistent prompt", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/custom-prompts/"+model.NewId()+"/render", strings.NewReader(`{}`))
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusNotFound, recorder.Code)
	})
}
