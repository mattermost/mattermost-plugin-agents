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
	"github.com/stretchr/testify/assert"
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

func TestAuditCustomPrompts(t *testing.T) {
	// Distinctive user content planted in requests and stored prompts to
	// prove it never reaches the audit record.
	const (
		plantedTemplate = "PLANTED-TEMPLATE-CONTENT-xyzzy"
		plantedTitle    = "PLANTED-TITLE-CONTENT-xyzzy"
	)

	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e, cpStore := setupCustomPromptsTestEnvironment(t)
	defer e.Cleanup(t)

	e.mockAPI.On("LogError", mock.Anything).Maybe()

	// Stored prompts carry the planted content too, so ownership checks that
	// fetch the prompt cannot leak it into the record either.
	createPlantedPrompt := func(t *testing.T, creatorID string) customprompts.CustomPrompt {
		t.Helper()
		prompt, err := cpStore.Create(customprompts.CustomPrompt{
			CreatorID:   creatorID,
			Name:        plantedTitle,
			Description: "test",
			Template:    plantedTemplate,
			IsShared:    false,
		})
		require.NoError(t, err)
		return prompt
	}

	tests := []struct {
		name           string
		event          string
		buildRequest   func(t *testing.T) (req *http.Request, promptID string)
		expectedStatus int
		validateRecord func(t *testing.T, rec *model.AuditRecord, promptID string, responseBody []byte)
	}{
		{
			name:  "create success records prompt id and sharing flag",
			event: AuditEventCreateCustomPrompt,
			buildRequest: func(t *testing.T) (*http.Request, string) {
				body := `{"name":"` + plantedTitle + `","description":"d","template":"` + plantedTemplate + `","is_shared":true}`
				req := httptest.NewRequest(http.MethodPost, "/custom-prompts", strings.NewReader(body))
				req.Header.Set("Mattermost-User-Id", testUserID)
				return req, ""
			},
			expectedStatus: http.StatusCreated,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, _ string, responseBody []byte) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, testUserID, rec.Actor.UserId)
				assert.Equal(t, true, rec.EventData.Parameters["is_shared"])

				// The recorded ID must be the created prompt's actual ID, not
				// just any non-empty string.
				var created customprompts.CustomPrompt
				require.NoError(t, json.Unmarshal(responseBody, &created))
				require.NotEmpty(t, created.ID)
				assert.Equal(t, created.ID, rec.EventData.Parameters["prompt_id"])
			},
		},
		{
			name:  "create failing validation records a 400 fail without a prompt id",
			event: AuditEventCreateCustomPrompt,
			buildRequest: func(t *testing.T) (*http.Request, string) {
				// An over-length name fails Validate while the request still
				// carries both planted content fields.
				overlongName := plantedTitle + strings.Repeat("x", customprompts.MaxPromptNameLength)
				body := `{"name":"` + overlongName + `","description":"d","template":"` + plantedTemplate + `"}`
				req := httptest.NewRequest(http.MethodPost, "/custom-prompts", strings.NewReader(body))
				req.Header.Set("Mattermost-User-Id", testUserID)
				return req, ""
			},
			expectedStatus: http.StatusBadRequest,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, _ string, _ []byte) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, http.StatusBadRequest, rec.Error.Code)
				assert.NotContains(t, rec.EventData.Parameters, "prompt_id",
					"a create that never happened must not claim a prompt id")
			},
		},
		{
			name:  "update success records prompt id and sharing flag",
			event: AuditEventUpdateCustomPrompt,
			buildRequest: func(t *testing.T) (*http.Request, string) {
				prompt := createPlantedPrompt(t, testUserID)
				body := `{"name":"Updated","description":"updated","template":"` + plantedTemplate + `","is_shared":true}`
				req := httptest.NewRequest(http.MethodPut, "/custom-prompts/"+prompt.ID, strings.NewReader(body))
				req.Header.Set("Mattermost-User-Id", testUserID)
				return req, prompt.ID
			},
			expectedStatus: http.StatusNoContent,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, promptID string, _ []byte) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, testUserID, rec.Actor.UserId)
				assert.Equal(t, promptID, rec.EventData.Parameters["prompt_id"])
				assert.Equal(t, true, rec.EventData.Parameters["is_shared"])
			},
		},
		{
			name:  "update by non-owner records a 404 fail with the prompt id",
			event: AuditEventUpdateCustomPrompt,
			buildRequest: func(t *testing.T) (*http.Request, string) {
				prompt := createPlantedPrompt(t, testUserID)
				body := `{"name":"Hacked","description":"hacked","template":"` + plantedTemplate + `"}`
				req := httptest.NewRequest(http.MethodPut, "/custom-prompts/"+prompt.ID, strings.NewReader(body))
				req.Header.Set("Mattermost-User-Id", testOtherUserID)
				return req, prompt.ID
			},
			expectedStatus: http.StatusNotFound,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, promptID string, _ []byte) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, testOtherUserID, rec.Actor.UserId)
				assert.Equal(t, http.StatusNotFound, rec.Error.Code)
				assert.Equal(t, promptID, rec.EventData.Parameters["prompt_id"])
			},
		},
		{
			name:  "delete success records the prompt id",
			event: AuditEventDeleteCustomPrompt,
			buildRequest: func(t *testing.T) (*http.Request, string) {
				prompt := createPlantedPrompt(t, testUserID)
				req := httptest.NewRequest(http.MethodDelete, "/custom-prompts/"+prompt.ID, nil)
				req.Header.Set("Mattermost-User-Id", testUserID)
				return req, prompt.ID
			},
			expectedStatus: http.StatusNoContent,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, promptID string, _ []byte) {
				assert.Equal(t, model.AuditStatusSuccess, rec.Status)
				assert.Equal(t, testUserID, rec.Actor.UserId)
				assert.Equal(t, promptID, rec.EventData.Parameters["prompt_id"])
			},
		},
		{
			name:  "delete by non-owner records a 404 fail with the prompt id",
			event: AuditEventDeleteCustomPrompt,
			buildRequest: func(t *testing.T) (*http.Request, string) {
				prompt := createPlantedPrompt(t, testUserID)
				req := httptest.NewRequest(http.MethodDelete, "/custom-prompts/"+prompt.ID, nil)
				req.Header.Set("Mattermost-User-Id", testOtherUserID)
				return req, prompt.ID
			},
			expectedStatus: http.StatusNotFound,
			validateRecord: func(t *testing.T, rec *model.AuditRecord, promptID string, _ []byte) {
				assert.Equal(t, model.AuditStatusFail, rec.Status)
				assert.Equal(t, testOtherUserID, rec.Actor.UserId)
				assert.Equal(t, http.StatusNotFound, rec.Error.Code)
				assert.Equal(t, promptID, rec.EventData.Parameters["prompt_id"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := e.CaptureAuditRecords()

			req, promptID := tt.buildRequest(t)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{SessionId: "sessionid", IPAddress: "127.0.0.1"}, recorder, req)

			require.Equal(t, tt.expectedStatus, recorder.Result().StatusCode)
			require.Len(t, *records, 1, "exactly one audit record must be emitted")
			rec := (*records)[0]
			assert.Equal(t, tt.event, rec.EventName)
			assert.Equal(t, "sessionid", rec.Actor.SessionId)
			tt.validateRecord(t, rec, promptID, recorder.Body.Bytes())

			raw, err := json.Marshal(rec)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), plantedTemplate, "audit record must never carry the prompt template")
			assert.NotContains(t, string(raw), plantedTitle, "audit record must never carry the prompt title")
		})
	}
}
