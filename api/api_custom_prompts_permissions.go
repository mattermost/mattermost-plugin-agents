// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/customprompts"
)

// requirePromptOwnership fetches the prompt by ID and verifies that the
// requesting user is its creator. Returns the prompt on success, or aborts
// the request with the appropriate status code on failure.
func (a *API) requirePromptOwnership(c *gin.Context, promptID, userID string) (customprompts.CustomPrompt, bool) {
	prompt, err := a.customPromptsStore.Get(promptID)
	if err != nil {
		c.AbortWithError(http.StatusNotFound, fmt.Errorf("prompt not found: %w", err))
		return customprompts.CustomPrompt{}, false
	}

	if prompt.CreatorID != userID {
		c.AbortWithError(http.StatusNotFound, errors.New("prompt not found or not accessible"))
		return customprompts.CustomPrompt{}, false
	}

	return prompt, true
}
