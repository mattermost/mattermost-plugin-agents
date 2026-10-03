// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcpserver_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/delegation"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcpserver/tools"
)

type stubDelegationService struct {
	available bool
}

func (s *stubDelegationService) Delegate(context.Context, delegation.Request) (string, error) {
	return "", nil
}

func (s *stubDelegationService) Available() bool {
	return s.available
}

func TestInMemoryServerAskAgentVisibility(t *testing.T) {
	tests := []struct {
		name     string
		service  tools.DelegationService
		wantList bool
	}{
		{name: "hidden without a delegation service", service: nil, wantList: false},
		{name: "hidden while the delegation service is not wired", service: &stubDelegationService{available: false}, wantList: false},
		{name: "listed when the delegation service is available", service: &stubDelegationService{available: true}, wantList: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := mcpserver.InMemoryConfig{
				BaseConfig: mcpserver.BaseConfig{MMServerURL: "http://localhost:8065"},
			}
			server, err := mcpserver.NewInMemoryServer(config, &testLogger{t: t}, nil, nil, func() bool { return true }, tc.service)
			require.NoError(t, err)

			names, _ := listInMemoryToolNames(t, server)
			require.Contains(t, names, "list_agents")
			if tc.wantList {
				require.Contains(t, names, "ask_agent")
			} else {
				require.NotContains(t, names, "ask_agent")
			}
		})
	}
}
