// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.enterprise for license information.

package enterprise

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

func devConfig() *model.Config {
	cfg := &model.Config{}
	cfg.ServiceSettings.EnableTesting = model.NewPointer(true)
	cfg.ServiceSettings.EnableDeveloper = model.NewPointer(true)
	return cfg
}

func TestLevelFor(t *testing.T) {
	tests := []struct {
		name    string
		config  *model.Config
		license *model.License
		want    Level
	}{
		{name: "nil license", license: nil, want: LevelUnlicensed},
		{name: "empty license", license: &model.License{}, want: LevelUnlicensed},
		{name: "unknown sku without features", license: &model.License{SkuShortName: "mystery"}, want: LevelUnlicensed},
		{name: "professional", license: &model.License{SkuShortName: model.LicenseShortSkuProfessional}, want: LevelProfessional},
		{name: "legacy E10", license: &model.License{SkuShortName: model.LicenseShortSkuE10}, want: LevelProfessional},
		{name: "enterprise", license: &model.License{SkuShortName: model.LicenseShortSkuEnterprise}, want: LevelEnterprise},
		{name: "legacy E20", license: &model.License{SkuShortName: model.LicenseShortSkuE20}, want: LevelEnterprise},
		{name: "entry behaves as enterprise", license: &model.License{SkuShortName: model.LicenseShortSkuMattermostEntry}, want: LevelEnterprise},
		{name: "enterprise advanced", license: &model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced}, want: LevelEnterpriseAdvanced},
		{
			name:    "unknown sku with future features",
			license: &model.License{SkuShortName: "mystery", Features: &model.Features{FutureFeatures: model.NewPointer(true)}},
			want:    LevelEnterprise,
		},
		{
			name:    "unknown sku with ldap",
			license: &model.License{SkuShortName: "mystery", Features: &model.Features{LDAP: model.NewPointer(true)}},
			want:    LevelProfessional,
		},
		{name: "development bypass without license", config: devConfig(), want: LevelEnterpriseAdvanced},
		{name: "development bypass with professional license", config: devConfig(), license: &model.License{SkuShortName: model.LicenseShortSkuProfessional}, want: LevelEnterpriseAdvanced},
		{
			name: "only EnableTesting is not development",
			config: func() *model.Config {
				cfg := &model.Config{}
				cfg.ServiceSettings.EnableTesting = model.NewPointer(true)
				return cfg
			}(),
			want: LevelUnlicensed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, LevelFor(tc.config, tc.license))
		})
	}
}

func TestLicenseCheckerFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		checker *LicenseChecker
	}{
		{name: "nil checker", checker: nil},
		{name: "nil plugin client", checker: NewLicenseChecker(nil)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, LevelUnlicensed, tc.checker.Level())
			require.False(t, tc.checker.Allows(CapMultiplayerChannels))
			require.False(t, tc.checker.IsBasicsLicensed())
			require.False(t, tc.checker.IsMultiLLMLicensed())
			limit, capped := tc.checker.AgentLimit()
			require.True(t, capped)
			require.Equal(t, FreeAgentLimit, limit)
			var licErr *LicenseError
			require.ErrorAs(t, tc.checker.Check(CapThreadSummarization), &licErr)
			require.ErrorIs(t, licErr, ErrNotLicensed)
		})
	}
}

func TestLicenseCheckerLevelFromPluginAPI(t *testing.T) {
	tests := []struct {
		name    string
		license *model.License
		config  *model.Config
		want    Level
	}{
		{name: "unlicensed", license: nil, config: &model.Config{}, want: LevelUnlicensed},
		{name: "professional", license: &model.License{SkuShortName: model.LicenseShortSkuProfessional}, config: &model.Config{}, want: LevelProfessional},
		{name: "enterprise", license: &model.License{SkuShortName: model.LicenseShortSkuEnterprise}, config: &model.Config{}, want: LevelEnterprise},
		{name: "advanced", license: &model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced}, config: &model.Config{}, want: LevelEnterpriseAdvanced},
		{name: "development", license: nil, config: devConfig(), want: LevelEnterpriseAdvanced},
		{name: "nil config fails closed", license: nil, config: nil, want: LevelUnlicensed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			mockAPI.On("GetLicense").Return(tc.license)
			mockAPI.On("GetConfig").Return(tc.config)
			checker := NewLicenseChecker(pluginapi.NewClient(mockAPI, nil))
			require.Equal(t, tc.want, checker.Level())
		})
	}
}

// TestCapabilityMatrix pins the tier chart: every capability at every level,
// including the boundary immediately below its minimum level.
func TestCapabilityMatrix(t *testing.T) {
	levels := []Level{LevelUnlicensed, LevelProfessional, LevelEnterprise, LevelEnterpriseAdvanced}
	tests := []struct {
		cap Capability
		min Level
	}{
		{CapMultiplayerChannels, LevelProfessional},
		{CapThreadSummarization, LevelProfessional},
		{CapChannelSummarization, LevelProfessional},
		{CapProviderWebSearch, LevelProfessional},
		{CapAgentAccessControls, LevelProfessional},
		{CapTokenAccounting, LevelProfessional},
		{CapMultipleLLMServices, LevelEnterprise},
		{CapModelFallback, LevelEnterpriseAdvanced},
		{CapStateChangingTools, LevelEnterprise},
		{CapSovereignWebSearch, LevelEnterprise},
		{CapToolApprovalPolicies, LevelEnterprise},
		{CapRemoteMCP, LevelEnterprise},
		{CapSemanticSearch, LevelEnterprise},
		{CapMeetings, LevelEnterprise},
		{CapMCPServiceAccount, LevelEnterprise},
		{CapSharedPrompts, LevelEnterprise},
		{CapChannelAutoReply, LevelEnterpriseAdvanced},
		{CapAttributeBasedAccess, LevelEnterpriseAdvanced},
		{Capability("unknown_capability"), LevelEnterpriseAdvanced},
	}

	for _, tc := range tests {
		t.Run(string(tc.cap), func(t *testing.T) {
			require.Equal(t, tc.min, RequiredLevel(tc.cap))
			for _, level := range levels {
				checker := checkerAt(t, level)
				require.Equal(t, level >= tc.min, checker.Allows(tc.cap), "level %s", level)
				err := checker.Check(tc.cap)
				if level >= tc.min {
					require.NoError(t, err)
					continue
				}
				var licErr *LicenseError
				require.ErrorAs(t, err, &licErr)
				require.Equal(t, tc.min, licErr.RequiredLevel)
				require.Equal(t, level, licErr.CurrentLevel)
				require.Contains(t, err.Error(), tc.min.String())
			}
		})
	}
	require.Len(t, capabilities, len(tests)-1, "every capability in the chart must be covered by this test")
}

func TestLimitsPerLevel(t *testing.T) {
	tests := []struct {
		level         Level
		agentLimit    int
		agentsCapped  bool
		serviceLimit  int
		servicesCappd bool
	}{
		{LevelUnlicensed, FreeAgentLimit, true, BaseServiceLimit, true},
		{LevelProfessional, ProfessionalAgentLimit, true, BaseServiceLimit, true},
		{LevelEnterprise, 0, false, 0, false},
		{LevelEnterpriseAdvanced, 0, false, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.level.String(), func(t *testing.T) {
			limit, capped := AgentLimitFor(tc.level)
			require.Equal(t, tc.agentsCapped, capped)
			require.Equal(t, tc.agentLimit, limit)
			limit, capped = ServiceLimitFor(tc.level)
			require.Equal(t, tc.servicesCappd, capped)
			require.Equal(t, tc.serviceLimit, limit)

			checker := checkerAt(t, tc.level)
			limit, capped = checker.AgentLimit()
			require.Equal(t, tc.agentsCapped, capped)
			require.Equal(t, tc.agentLimit, limit)
			limit, capped = checker.ServiceLimit()
			require.Equal(t, tc.servicesCappd, capped)
			require.Equal(t, tc.serviceLimit, limit)
		})
	}
}

func TestLicenseErrorMessage(t *testing.T) {
	err := NewLicenseError(CapMeetings, LevelProfessional)
	require.Equal(t, "Meeting transcription and summaries requires a Mattermost Enterprise license or higher; the current license level is Professional", err.Error())
	require.True(t, errors.Is(err, ErrNotLicensed))
	require.Equal(t, "enterprise", err.RequiredLevel.Key())
}

func TestAgentLimitError(t *testing.T) {
	tests := []struct {
		name     string
		level    Level
		wantNil  bool
		contains []string
		required Level
	}{
		{
			name:     "unlicensed names the free cap and Professional",
			level:    LevelUnlicensed,
			contains: []string{"1 AI agents", "Professional"},
			required: LevelProfessional,
		},
		{
			name:     "professional names the professional cap and Enterprise",
			level:    LevelProfessional,
			contains: []string{"3 AI agents", "Enterprise"},
			required: LevelEnterprise,
		},
		{
			name:    "enterprise is uncapped",
			level:   LevelEnterprise,
			wantNil: true,
		},
		{
			name:    "enterprise advanced is uncapped",
			level:   LevelEnterpriseAdvanced,
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := AgentLimitError(tc.level)
			if tc.wantNil {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, s := range tc.contains {
				require.Contains(t, err.Error(), s)
			}
			require.True(t, errors.Is(err, ErrNotLicensed))
			var licErr *LicenseError
			require.True(t, errors.As(err, &licErr))
			require.Equal(t, tc.required, licErr.RequiredLevel)
			require.Equal(t, tc.level, licErr.CurrentLevel)
		})
	}
}

// checkerAt returns a LicenseChecker backed by a plugin API reporting level.
func checkerAt(t *testing.T, level Level) *LicenseChecker {
	t.Helper()
	var license *model.License
	switch level {
	case LevelProfessional:
		license = &model.License{SkuShortName: model.LicenseShortSkuProfessional}
	case LevelEnterprise:
		license = &model.License{SkuShortName: model.LicenseShortSkuEnterprise}
	case LevelEnterpriseAdvanced:
		license = &model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced}
	}
	mockAPI := &plugintest.API{}
	mockAPI.On("GetLicense").Return(license).Maybe()
	mockAPI.On("GetConfig").Return(&model.Config{}).Maybe()
	return NewLicenseChecker(pluginapi.NewClient(mockAPI, nil))
}
