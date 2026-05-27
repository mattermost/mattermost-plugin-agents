// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/conversations"
	"github.com/stretchr/testify/require"
)

func TestLoopInAgentHTTPStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "not post owner",
			err:  conversations.ErrLoopInNotPostOwner,
			want: http.StatusForbidden,
		},
		{
			name: "wrong agent",
			err:  conversations.ErrLoopInWrongAgent,
			want: http.StatusForbidden,
		},
		{
			name: "already mentioned",
			err:  conversations.ErrLoopInAlreadyMentioned,
			want: http.StatusBadRequest,
		},
		{
			name: "wrapped no context",
			err:  errors.Join(errors.New("outer"), conversations.ErrLoopInNoAgentContext),
			want: http.StatusBadRequest,
		},
		{
			name: "unexpected",
			err:  errors.New("database failed"),
			want: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, loopInAgentHTTPStatus(tc.err))
		})
	}
}
