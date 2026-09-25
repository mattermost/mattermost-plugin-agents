// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/customprompts"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCreateSharedCustomPromptByLevel(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	sharedBody := `{"name":"Shared","description":"d","template":"hello","is_shared":true}`

	for _, level := range enterprisetest.AllLevels {
		t.Run("shared/"+level.String(), func(t *testing.T) {
			if level < enterprise.LevelEnterprise {
				e := SetupTestEnvironment(t)
				defer e.Cleanup(t)
				e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
				e.OverrideLicense(enterprisetest.LicenseFor(level))
				e.mockAPI.On("LogError", mock.Anything).Maybe()

				req := httptest.NewRequest(http.MethodPost, "/custom-prompts", strings.NewReader(sharedBody))
				req.Header.Set("Mattermost-User-Id", testUserID)
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, req)
				requireLicenseDenied(t, recorder.Result(), enterprise.CapSharedPrompts)
				return
			}

			e, _ := setupCustomPromptsTestEnvironment(t)
			defer e.Cleanup(t)
			e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			e.mockAPI.On("LogError", mock.Anything).Maybe()

			req := httptest.NewRequest(http.MethodPost, "/custom-prompts", strings.NewReader(sharedBody))
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, http.StatusCreated, recorder.Result().StatusCode)
		})
	}

	t.Run("nil checker fails closed for shared create", func(t *testing.T) {
		e := SetupTestEnvironment(t)
		defer e.Cleanup(t)
		e.api.licenseChecker = nil
		e.mockAPI.On("LogError", mock.Anything).Maybe()

		req := httptest.NewRequest(http.MethodPost, "/custom-prompts", strings.NewReader(sharedBody))
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		requireLicenseDenied(t, recorder.Result(), enterprise.CapSharedPrompts)
	})
}

func TestUpdateCustomPromptShareTransitionByLevel(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e, cpStore := setupCustomPromptsTestEnvironment(t)
	defer e.Cleanup(t)
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.mockAPI.On("LogError", mock.Anything).Maybe()

	for _, level := range enterprisetest.AllLevels {
		t.Run("false to true/"+level.String(), func(t *testing.T) {
			prompt := createTestPrompt(t, cpStore, testUserID, false)
			e.OverrideLicense(enterprisetest.LicenseFor(level))

			body := `{"name":"Updated","description":"d","template":"hello","is_shared":true}`
			req := httptest.NewRequest(http.MethodPut, "/custom-prompts/"+prompt.ID, strings.NewReader(body))
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			resp := recorder.Result()

			if level < enterprise.LevelEnterprise {
				requireLicenseDenied(t, resp, enterprise.CapSharedPrompts)
				return
			}
			require.Equal(t, http.StatusNoContent, resp.StatusCode)
		})
	}

	t.Run("true to false is never gated", func(t *testing.T) {
		for _, level := range enterprisetest.AllLevels {
			t.Run(level.String(), func(t *testing.T) {
				prompt := createTestPrompt(t, cpStore, testUserID, true)
				e.OverrideLicense(enterprisetest.LicenseFor(level))

				body := `{"name":"Updated","description":"d","template":"hello","is_shared":false}`
				req := httptest.NewRequest(http.MethodPut, "/custom-prompts/"+prompt.ID, strings.NewReader(body))
				req.Header.Set("Mattermost-User-Id", testUserID)
				recorder := httptest.NewRecorder()
				e.api.ServeHTTP(&plugin.Context{}, recorder, req)
				require.Equal(t, http.StatusNoContent, recorder.Result().StatusCode)
			})
		}
	})
}

func TestListCustomPromptsRespectsSharedLicense(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e, cpStore := setupCustomPromptsTestEnvironment(t)
	defer e.Cleanup(t)
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.mockAPI.On("LogError", mock.Anything).Maybe()

	own := createTestPrompt(t, cpStore, testUserID, false)
	shared := createTestPrompt(t, cpStore, testOtherUserID, true)

	for _, level := range enterprisetest.AllLevels {
		t.Run(level.String(), func(t *testing.T) {
			e.OverrideLicense(enterprisetest.LicenseFor(level))

			req := httptest.NewRequest(http.MethodGet, "/custom-prompts", nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

			var prompts []customprompts.CustomPrompt
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&prompts))
			ids := make(map[string]bool, len(prompts))
			for _, p := range prompts {
				ids[p.ID] = true
			}
			require.True(t, ids[own.ID], "own prompts are always listed")
			if level >= enterprise.LevelEnterprise {
				require.True(t, ids[shared.ID], "shared prompts are listed at Enterprise and above")
				return
			}
			require.False(t, ids[shared.ID], "shared prompts are omitted below Enterprise")
		})
	}

	t.Run("nil checker lists only own prompts", func(t *testing.T) {
		e.api.licenseChecker = nil

		req := httptest.NewRequest(http.MethodGet, "/custom-prompts", nil)
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		require.Equal(t, http.StatusOK, recorder.Result().StatusCode)

		var prompts []customprompts.CustomPrompt
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&prompts))
		ids := make(map[string]bool, len(prompts))
		for _, p := range prompts {
			ids[p.ID] = true
		}
		require.True(t, ids[own.ID])
		require.False(t, ids[shared.ID])
	})
}

func TestRenderAndPinSharedPromptByLevel(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e, cpStore := setupCustomPromptsTestEnvironment(t)
	defer e.Cleanup(t)
	e.api.licenseChecker = enterprise.NewLicenseChecker(e.client)
	e.mockAPI.On("LogError", mock.Anything).Maybe()

	promptsObj, err := llm.NewPrompts(fstest.MapFS{
		"empty.tmpl": &fstest.MapFile{Data: []byte("")},
	})
	require.NoError(t, err)
	e.api.prompts = promptsObj

	shared := createTestPrompt(t, cpStore, testOtherUserID, true)
	own := createTestPrompt(t, cpStore, testUserID, false)

	e.mockAPI.On("GetUser", testUserID).Return(&model.User{
		Id:       testUserID,
		Username: "testuser",
	}, nil).Maybe()

	for _, level := range enterprisetest.AllLevels {
		t.Run("render shared/"+level.String(), func(t *testing.T) {
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			req := httptest.NewRequest(http.MethodPost, "/custom-prompts/"+shared.ID+"/render", strings.NewReader(`{}`))
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			resp := recorder.Result()
			if level < enterprise.LevelEnterprise {
				requireLicenseDenied(t, resp, enterprise.CapSharedPrompts)
				return
			}
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("render own/"+level.String(), func(t *testing.T) {
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			req := httptest.NewRequest(http.MethodPost, "/custom-prompts/"+own.ID+"/render", strings.NewReader(`{}`))
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
		})

		t.Run("pin shared/"+level.String(), func(t *testing.T) {
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			body := fmt.Sprintf(`{"prompt_id":%q,"pinned":true}`, shared.ID)
			req := httptest.NewRequest(http.MethodPut, "/custom-prompts/pins", strings.NewReader(body))
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			resp := recorder.Result()
			if level < enterprise.LevelEnterprise {
				requireLicenseDenied(t, resp, enterprise.CapSharedPrompts)
				return
			}
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("unpin shared is never gated/"+level.String(), func(t *testing.T) {
			e.OverrideLicense(enterprisetest.LicenseFor(level))
			body := fmt.Sprintf(`{"prompt_id":%q,"pinned":false}`, shared.ID)
			req := httptest.NewRequest(http.MethodPut, "/custom-prompts/pins", strings.NewReader(body))
			req.Header.Set("Mattermost-User-Id", testUserID)
			recorder := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, recorder, req)
			require.Equal(t, http.StatusOK, recorder.Result().StatusCode)
		})
	}

	t.Run("nil checker denies render of shared prompt", func(t *testing.T) {
		e.api.licenseChecker = nil
		req := httptest.NewRequest(http.MethodPost, "/custom-prompts/"+shared.ID+"/render", strings.NewReader(`{}`))
		req.Header.Set("Mattermost-User-Id", testUserID)
		recorder := httptest.NewRecorder()
		e.api.ServeHTTP(&plugin.Context{}, recorder, req)
		requireLicenseDenied(t, recorder.Result(), enterprise.CapSharedPrompts)
	})
}
