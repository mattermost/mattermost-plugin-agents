// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *API) handleOAuthCallback(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	state := c.Query("state")
	code := c.Query("code")
	errorParam := c.Query("error")

	// Handle error responses
	if errorParam != "" {
		errorDescription := c.Query("error_description")
		a.pluginAPI.Log.Error("OAuth authorization failed", "error", errorParam, "description", errorDescription)

		c.Header("Content-Type", "text/html")
		c.String(http.StatusBadRequest, `
<!DOCTYPE html>
<html>
<head>
	<title>Authorization Failed</title>
</head>
<body>
	<script>
		// Close window immediately
		window.close();
	</script>
</body>
</html>`)
		return
	}

	// Validate required parameters
	if state == "" || code == "" {
		a.pluginAPI.Log.Error("Missing required OAuth parameters", "state", state, "code", code)

		c.Header("Content-Type", "text/html")
		c.String(http.StatusBadRequest, `
<!DOCTYPE html>
<html>
<head>
	<title>Authorization Failed</title>
</head>
<body>
	<script>
		// Close window immediately
		window.close();
	</script>
</body>
</html>`)
		return
	}

	// Process the OAuth callback
	_, err := a.mcpClientManager.ProcessOAuthCallback(c.Request.Context(), userID, state, code)
	if err != nil {
		a.pluginAPI.Log.Error("Failed to process OAuth callback", "error", err)

		c.Header("Content-Type", "text/html")
		c.String(http.StatusInternalServerError, `
<!DOCTYPE html>
<html>
<head>
	<title>Authorization Failed</title>
</head>
<body>
	<script>
		// Close window immediately
		window.close();
	</script>
</body>
</html>`)
		return
	}

	// Success response
	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, `
<!DOCTYPE html>
<html>
<head>
	<title>Authorization Successful</title>
</head>
<body>
	<script>
		// Close window immediately
		window.close();
	</script>
</body>
</html>`)
}
