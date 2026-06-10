// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.enterprise for license information.

package enterprise

import (
	"errors"

	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var ErrNotLicensed = errors.New("license does not support this feature")

type LicenseChecker struct {
	pluginAPIClient *pluginapi.Client
}

func NewLicenseChecker(pluginAPIClient *pluginapi.Client) *LicenseChecker {
	return &LicenseChecker{
		pluginAPIClient,
	}
}

// isAtLeastE20Licensed returns true when the server either has an E20 license or is configured for development.
func (e *LicenseChecker) isAtLeastE20Licensed() bool {
	config := e.pluginAPIClient.Configuration.GetConfig()
	license := e.pluginAPIClient.System.GetLicense()

	return pluginapi.IsE20LicensedOrDevelopment(config, license)
}

// isAtLeastE10Licensed returns true when the server either has at least an E10 license or is configured for development.
func (e *LicenseChecker) isAtLeastE10Licensed() bool {
	config := e.pluginAPIClient.Configuration.GetConfig()
	license := e.pluginAPIClient.System.GetLicense()

	return pluginapi.IsE10LicensedOrDevelopment(config, license)
}

// IsMultiLLMLicensed returns true when the server either has at least a Professional
// license or is configured for development. Pro is included because the Pro pricing
// page advertises "Bring-Your-Own & Multi-LLM Integration", "Interactive AI Bot Support",
// and "Agent Control Plane" as Pro tier features (MM-69186).
func (e *LicenseChecker) IsMultiLLMLicensed() bool {
	return e.isAtLeastE10Licensed()
}

// IsBasicsLicensed returns true when the server either has a license for basic features or is configured for development.
func (e *LicenseChecker) IsBasicsLicensed() bool {
	return e.isAtLeastE20Licensed()
}
