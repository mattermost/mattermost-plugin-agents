// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestRemoveChannelArtifacts(t *testing.T) {
	notFound := model.NewAppError("Get", "app.channel.get.app_error", nil, "", http.StatusNotFound)

	tests := []struct {
		name    string
		channel *model.Channel
		err     error
		want    bool
		wantErr bool
	}{
		{name: "missing channel", err: notFound, want: true},
		{name: "archived channel", channel: &model.Channel{DeleteAt: 1}},
		{name: "open channel", channel: &model.Channel{}},
		{name: "nil channel", channel: nil},
		{name: "lookup failure", err: errors.New("timeout"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := removeChannelArtifacts(tt.channel, tt.err)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
