// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestOnLicenseChangedRunsEveryListener(t *testing.T) {
	calls := 0
	p := &Plugin{licenseChangeListeners: []func(){
		func() { calls++ },
		func() { calls++ },
	}}

	p.OnLicenseChanged(nil, &model.License{SkuShortName: model.LicenseShortSkuEnterprise})

	require.Equal(t, 2, calls)
}
