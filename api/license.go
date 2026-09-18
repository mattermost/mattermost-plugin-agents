// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
)

// licenseErrorResponse is the JSON body returned when a request is denied for
// licensing reasons. LicenseRequired carries enterprise.Level.Key() so the
// webapp can name the level in its own copy.
type licenseErrorResponse struct {
	Error           string `json:"error"`
	LicenseRequired string `json:"license_required"`
}

// abortNotLicensed writes a 403 describing the capability and the license
// level it requires. Any non-license error is written as a 403 with its
// message and no level hint.
func abortNotLicensed(c *gin.Context, err error) {
	_ = c.Error(err)
	resp := licenseErrorResponse{Error: err.Error()}
	var licErr *enterprise.LicenseError
	if errors.As(err, &licErr) {
		resp.LicenseRequired = licErr.RequiredLevel.Key()
	}
	c.AbortWithStatusJSON(http.StatusForbidden, resp)
}

// requireCapability aborts the request with a license error when capability is not
// available and reports whether the handler may proceed. A nil license
// checker fails closed.
func (a *API) requireCapability(c *gin.Context, capability enterprise.Capability) bool {
	if err := a.licenseChecker.Check(capability); err != nil {
		abortNotLicensed(c, err)
		return false
	}
	return true
}

// capabilityRequired is the middleware form of requireCapability.
func (a *API) capabilityRequired(capability enterprise.Capability) gin.HandlerFunc {
	return func(c *gin.Context) {
		a.requireCapability(c, capability)
	}
}
