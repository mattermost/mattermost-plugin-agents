// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	ltplugins "github.com/mattermost/mattermost-load-test-ng/loadtest/plugins"
)

func init() {
	ltplugins.RegisterController(ltplugins.TypeSimulController, func() ltplugins.Controller {
		return NewSimulController()
	})
}
