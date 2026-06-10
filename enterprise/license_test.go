// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.enterprise for license information.

package enterprise

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/assert"
)

// TestIsMultiLLMLicensed verifies the SKU short names that unlock multi-LLM /
// multi-agent features. Mattermost Professional is advertised as including
// "Bring-Your-Own & Multi-LLM Integration" and "Interactive AI Bot Support"
// (MM-69186), so a Pro license must be sufficient.
func TestIsMultiLLMLicensed(t *testing.T) {
	devConfig := &model.Config{
		ServiceSettings: model.ServiceSettings{
			EnableTesting:   model.NewPointer(false),
			EnableDeveloper: model.NewPointer(false),
		},
	}

	tests := []struct {
		name    string
		license *model.License
		want    bool
	}{
		{
			name:    "no license is not multi-LLM licensed",
			license: nil,
			want:    false,
		},
		{
			name:    "professional license is multi-LLM licensed (MM-69186)",
			license: &model.License{SkuShortName: model.LicenseShortSkuProfessional},
			want:    true,
		},
		{
			name:    "entry license is multi-LLM licensed",
			license: &model.License{SkuShortName: model.LicenseShortSkuMattermostEntry},
			want:    true,
		},
		{
			name:    "enterprise license is multi-LLM licensed",
			license: &model.License{SkuShortName: model.LicenseShortSkuEnterprise},
			want:    true,
		},
		{
			name:    "enterprise advanced license is multi-LLM licensed",
			license: &model.License{SkuShortName: model.LicenseShortSkuEnterpriseAdvanced},
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)
			mockAPI.On("GetConfig").Return(devConfig).Maybe()
			mockAPI.On("GetLicense").Return(tc.license).Maybe()

			checker := NewLicenseChecker(pluginapi.NewClient(mockAPI, nil))
			assert.Equal(t, tc.want, checker.IsMultiLLMLicensed())
		})
	}
}
