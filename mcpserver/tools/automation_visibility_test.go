// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
)

func Test_automationListVisibility(t *testing.T) {
	openCh := &model.Channel{Type: model.ChannelTypeOpen}
	dmCh := &model.Channel{Type: model.ChannelTypeDirect}

	tests := []struct {
		name                    string
		isSysadmin              bool
		channel                 *model.Channel
		openPrivateAdmin        bool
		openPrivateFetchFailed  bool
		wantVisible             bool
	}{
		{name: "sysadmin open", isSysadmin: true, channel: openCh, wantVisible: true},
		{name: "nil channel non-admin", channel: nil, wantVisible: true},
		{name: "dm non-admin", channel: dmCh, wantVisible: true},
		{name: "open admin", channel: openCh, openPrivateAdmin: true, wantVisible: true},
		{name: "open non-admin", channel: openCh, openPrivateAdmin: false, wantVisible: false},
		{name: "open member fetch failed", channel: openCh, openPrivateFetchFailed: true, wantVisible: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := automationListVisibility(tt.isSysadmin, tt.channel, tt.openPrivateAdmin, tt.openPrivateFetchFailed)
			assert.Equal(t, tt.wantVisible, got)
		})
	}
}
