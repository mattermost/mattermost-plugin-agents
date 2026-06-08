// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package controller

import (
	"github.com/mattermost/mattermost-load-test-ng/loadtest/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// simulAPI narrows mattermost-load-test-ng's user.User to APIs used by this controller,
// so unit tests can use small fakes without implementing the full User interface.
type simulAPI interface {
	Store() store.UserStore
	CreatePost(*model.Post) (string, error)
	CreateDirectChannel(string) (string, error)
	GetUsersByIds([]string, int64) ([]string, error)
	GetUsersByUsernames([]string) ([]string, error)
}
