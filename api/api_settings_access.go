// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
)

const (
	settingsAccessLevelKey       = "settings_access_level"
	settingsAccessSystemAdminKey = "settings_access_system_admin"
)

// settingsAccessLevel returns how much of the plugin configuration userID may
// reach: system admins everything, other users what the delegation lists grant.
func (a *API) settingsAccessLevel(userID string) config.SettingsAccessLevel {
	level, _ := a.settingsAccess(userID)
	return level
}

// settingsAccess returns userID's level and whether that level comes from
// being a system admin rather than from the delegation lists.
func (a *API) settingsAccess(userID string) (config.SettingsAccessLevel, bool) {
	if userID == "" {
		return config.SettingsAccessNone, false
	}
	if isSystemAdmin(a.pluginAPI, userID) {
		return config.SettingsAccessWrite, true
	}
	return a.config.SettingsAccess().LevelFor(userID), false
}

// settingsAccessFromContext returns the level the authorization middleware
// recorded for this request, or none when it did not run.
func settingsAccessFromContext(c *gin.Context) config.SettingsAccessLevel {
	if level, ok := c.Get(settingsAccessLevelKey); ok {
		if l, ok := level.(config.SettingsAccessLevel); ok {
			return l
		}
	}
	return config.SettingsAccessNone
}

// settingsCallerIsSystemAdmin reports whether the authorization middleware
// admitted this request as a system admin, false when it did not run.
func settingsCallerIsSystemAdmin(c *gin.Context) bool {
	return c.GetBool(settingsAccessSystemAdminKey)
}

func (a *API) requireSettingsAccess(c *gin.Context, minimum config.SettingsAccessLevel) {
	level, systemAdmin := a.settingsAccess(c.GetHeader("Mattermost-User-Id"))
	if level < minimum {
		c.AbortWithError(http.StatusForbidden, fmt.Errorf("must be a system admin or have %s access to the Agents settings", minimum))
		return
	}
	c.Set(settingsAccessLevelKey, level)
	c.Set(settingsAccessSystemAdminKey, systemAdmin)
}

// settingsReadAuthorizationRequired admits system admins and users with
// read-only or read/write access to the plugin configuration.
func (a *API) settingsReadAuthorizationRequired(c *gin.Context) {
	a.requireSettingsAccess(c, config.SettingsAccessRead)
}

// settingsWriteAuthorizationRequired admits system admins and users with
// read/write access to the plugin configuration.
func (a *API) settingsWriteAuthorizationRequired(c *gin.Context) {
	a.requireSettingsAccess(c, config.SettingsAccessWrite)
}

type mySettingsAccessResponse struct {
	// Level is "none", "read" or "write".
	Level string `json:"level"`
}

// handleGetMySettingsAccess tells the requesting user how much of the plugin
// configuration they may reach, so the webapp can show the Settings view.
// GET /settings/access
func (a *API) handleGetMySettingsAccess(c *gin.Context) {
	level := a.settingsAccessLevel(c.GetHeader("Mattermost-User-Id"))
	c.JSON(http.StatusOK, mySettingsAccessResponse{Level: level.String()})
}

// handleGetSettingsAccess returns the delegation lists.
// GET /admin/settings-access
func (a *API) handleGetSettingsAccess(c *gin.Context) {
	cfg, err := a.configStore.GetConfig()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to get config: %w", err))
		return
	}
	var access config.SettingsAccess
	if cfg != nil && cfg.SettingsAccess != nil {
		access = *cfg.SettingsAccess
	}
	normalized, err := access.Normalize()
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("stored settings access is invalid: %w", err))
		return
	}
	c.JSON(http.StatusOK, normalized)
}

// handlePutSettingsAccess replaces the delegation lists, leaving the rest of
// the configuration as stored.
// PUT /admin/settings-access
func (a *API) handlePutSettingsAccess(c *gin.Context) {
	var access config.SettingsAccess
	if err := c.BindJSON(&access); err != nil {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	normalized, err := access.Normalize()
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	saved, err := a.configStore.UpdateConfig(func(prev *config.Config) (config.Config, error) {
		if prev == nil {
			return config.Config{}, errors.New("the plugin configuration has not been created yet")
		}
		next := *prev.Clone()
		next.SettingsAccess = &normalized
		return next, nil
	})
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to save settings access: %w", err))
		return
	}

	audit.AddParam(auditRec(c), "read_only_user_ids", audit.TruncateIDs(normalized.ReadOnlyUserIDs))
	audit.AddParam(auditRec(c), "read_write_user_ids", audit.TruncateIDs(normalized.ReadWriteUserIDs))
	audit.AddParam(auditRec(c), "persisted", true)

	a.configUpdater.Update(&saved)
	if err := a.clusterNotifier.PublishConfigUpdate(); err != nil {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("failed to notify cluster of config update: %w", err))
		return
	}

	c.JSON(http.StatusOK, normalized)
}
