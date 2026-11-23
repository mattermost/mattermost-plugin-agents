// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// interPluginPlaybooks answers like Playbooks behind core's plugin-to-plugin
// routing: core drops Mattermost-User-Id, and Playbooks takes the acting user
// only from Mattermost-Plugin-Acting-User-Id.
func interPluginPlaybooks(t *testing.T, wantUserID string, seen *[]string) *plugintest.API {
	t.Helper()
	api := &plugintest.API{}
	api.On("PluginHTTP", mock.Anything).Return(func(req *http.Request) *http.Response {
		req.Header.Del("Mattermost-User-Id")
		*seen = append(*seen, req.Method+" "+req.URL.Path)
		rec := httptest.NewRecorder()
		if req.Header.Get("Mattermost-Plugin-Acting-User-Id") != wantUserID {
			rec.WriteHeader(http.StatusUnauthorized)
			return rec.Result()
		}
		_, _ = rec.WriteString(`{"name":"Launch","current_status":"InProgress","checklists":[{"items":[{"state":"closed"},{"state":""}]}]}`)
		return rec.Result()
	})
	return api
}

func TestProjectPlaybooksActAsTheUser(t *testing.T) {
	tests := []struct {
		name string
		call func(*testing.T, projectPlaybooks) error
		want string
	}{
		{
			name: "checklist progress",
			call: func(t *testing.T, p projectPlaybooks) error {
				summary, err := p.RunSummary(context.Background(), "run1", "alice")
				if err == nil {
					assert.True(t, summary.Available)
					assert.Equal(t, 2, summary.TotalTasks)
					assert.Equal(t, 1, summary.CompletedTasks)
					assert.Equal(t, "/playbooks/runs/run1", summary.URL, "the link stays in the app")
				}
				return err
			},
			want: "GET /playbooks/api/v0/runs/run1",
		},
		{
			name: "finish the run",
			call: func(_ *testing.T, p projectPlaybooks) error {
				return p.FinishRun(context.Background(), "run1", "alice")
			},
			want: "PUT /playbooks/api/v0/runs/run1/finish",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen []string
			api := interPluginPlaybooks(t, "alice", &seen)
			require.NoError(t, tt.call(t, projectPlaybooks{api: api}))
			assert.Equal(t, []string{tt.want}, seen)
		})
	}
}
