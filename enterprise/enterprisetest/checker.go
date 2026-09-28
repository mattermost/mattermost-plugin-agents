// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.enterprise for license information.

// Package enterprisetest provides test-only helpers for building license
// checkers at a fixed level.
package enterprisetest

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// AllLevels lists every license level in ascending order, for table-driven
// tests that walk the tier boundaries.
var AllLevels = []enterprise.Level{
	enterprise.LevelUnlicensed,
	enterprise.LevelProfessional,
	enterprise.LevelEnterprise,
	enterprise.LevelEnterpriseAdvanced,
}

// LicenseFor returns a server license that resolves to level, or nil for
// LevelUnlicensed.
func LicenseFor(level enterprise.Level) *model.License {
	switch level {
	case enterprise.LevelProfessional:
		return &model.License{SkuShortName: model.LicenseShortSkuProfessional}
	case enterprise.LevelEnterprise:
		return &model.License{SkuShortName: model.LicenseShortSkuEnterprise}
	case enterprise.LevelEnterpriseAdvanced:
		return &model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced}
	default:
		return nil
	}
}

// StubLicense registers GetLicense and GetConfig expectations on mockAPI so a
// LicenseChecker built over it reports level.
func StubLicense(mockAPI *plugintest.API, level enterprise.Level) {
	mockAPI.On("GetLicense").Return(LicenseFor(level)).Maybe()
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
}

// CheckerAt returns a LicenseChecker that reports level.
func CheckerAt(level enterprise.Level) *enterprise.LicenseChecker {
	mockAPI := &plugintest.API{}
	StubLicense(mockAPI, level)
	return enterprise.NewLicenseChecker(pluginapi.NewClient(mockAPI, nil))
}
