// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var channelSelection = ToolSelection{ExcludeDMOnlyServers: true}

// A DM-only server must never be contacted for a request outside a DM.
func TestGetCatalogAccessNeverDialsDMOnlyServerOutsideDM(t *testing.T) {
	var requests atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(httpServer.Close)

	m := newRemoteAccessTestManager(t, []ServerConfig{{
		ID: accessAllowedID, Name: "Salesforce", Enabled: true, BaseURL: httpServer.URL, DMOnly: true,
	}}, &stubServerAccessChecker{}, httpServer.Client())

	access := m.GetCatalogAccessWithSelection(context.Background(), UserCatalogRequest("user-1"), channelSelection)
	assert.Empty(t, access.Tools)
	assert.Nil(t, access.Errors, "an excluded server must produce no connection errors or connect prompts")
	assert.Zero(t, requests.Load())
}

// A user who connected a DM-only server in a DM keeps a pooled session for it;
// a later channel request must not hand that session's tools to the LLM.
func TestGetCatalogAccessDropsCachedDMOnlyToolsOutsideDM(t *testing.T) {
	ctx := context.Background()
	req := UserCatalogRequest("user-1")
	dmOnly := startStreamableMCPServer(t, newTestMCPServer(0, "query_accounts"))
	shared := startStreamableMCPServer(t, newTestMCPServer(0, "search_docs"))

	m := newRemoteAccessTestManager(t, []ServerConfig{
		{ID: accessAllowedID, Name: "Salesforce", Enabled: true, BaseURL: dmOnly.URL, DMOnly: true},
		{ID: accessDeniedID, Name: "Docs", Enabled: true, BaseURL: shared.URL},
	}, &stubServerAccessChecker{}, http.DefaultClient)
	t.Cleanup(func() {
		if uc := m.clients[req.remoteKey()]; uc != nil {
			uc.Close()
		}
	})

	inDM := m.GetCatalogAccessWithSelection(ctx, req, ToolSelection{})
	require.Nil(t, inDM.Errors)
	require.True(t, hasToolFromOrigin(inDM.Tools, dmOnly.URL), "a DM catalogs the DM-only server")
	require.True(t, hasToolFromOrigin(inDM.Tools, shared.URL))

	inChannel := m.GetCatalogAccessWithSelection(ctx, req, channelSelection)
	assert.False(t, hasToolFromOrigin(inChannel.Tools, dmOnly.URL), "a cached DM session must not leak into a channel catalog")
	assert.True(t, hasToolFromOrigin(inChannel.Tools, shared.URL), "other servers stay available in channels")
}

func TestDescribeDMOnlyMCPServers(t *testing.T) {
	const (
		salesforceOrigin = "https://salesforce.example.com"
		hrOrigin         = "https://hr.example.com"
		docsOrigin       = "https://docs.example.com"
		salesforceID     = "ssssssssssssssssssssssssss"
		hrID             = "hhhhhhhhhhhhhhhhhhhhhhhhhh"
	)
	servers := []ServerConfig{
		{ID: salesforceID, Name: "Salesforce", Enabled: true, BaseURL: salesforceOrigin, DMOnly: true, Description: "CRM\naccounts", ServiceAccountHeaders: testServiceAccountHeaders()},
		{ID: hrID, Name: "HR", Enabled: true, BaseURL: hrOrigin, DMOnly: true},
		{Name: "Docs", Enabled: true, BaseURL: docsOrigin},
		{Name: "Off", Enabled: false, BaseURL: "https://off.example.com", DMOnly: true},
	}

	tests := []struct {
		name      string
		req       CatalogRequest
		selection ToolSelection
		denied    map[string]bool
		want      []llm.DMOnlyMCPServer
	}{
		{
			name:      "a DM selection excludes nothing, so nothing is described",
			req:       UserCatalogRequest("user-1"),
			selection: ToolSelection{},
		},
		{
			name:      "outside a DM every usable DM-only server is described",
			req:       UserCatalogRequest("user-1"),
			selection: channelSelection,
			want: []llm.DMOnlyMCPServer{
				{Name: "Salesforce", Description: "CRM accounts"},
				{Name: "HR"},
			},
		},
		{
			name:      "a server the access policy denies is not described",
			req:       UserCatalogRequest("user-1"),
			selection: channelSelection,
			denied:    map[string]bool{hrID: true},
			want:      []llm.DMOnlyMCPServer{{Name: "Salesforce", Description: "CRM accounts"}},
		},
		{
			name:      "a server outside the agent allowlist is not described",
			req:       UserCatalogRequest("user-1"),
			selection: ToolSelection{ExcludeDMOnlyServers: true, AllowedOrigins: []string{hrOrigin, docsOrigin}},
			want:      []llm.DMOnlyMCPServer{{Name: "HR"}},
		},
		{
			name:      "service-account catalogs describe only servers with service account headers",
			req:       ServiceAccountCatalogRequest("bot-1", "user-1"),
			selection: channelSelection,
			want:      []llm.DMOnlyMCPServer{{Name: "Salesforce", Description: "CRM accounts"}},
		},
		{
			name:      "an unlicensed remote selection describes nothing",
			req:       UserCatalogRequest("user-1"),
			selection: ToolSelection{ExcludeDMOnlyServers: true, ExcludeRemoteServers: true},
		},
		{
			name:      "an invalid request describes nothing",
			req:       CatalogRequest{RemoteOwnerID: "user-1"},
			selection: channelSelection,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &stubServerAccessChecker{denied: tt.denied}
			m := newRemoteAccessTestManager(t, servers, checker, nil)

			got := m.DescribeDMOnlyMCPServers(context.Background(), tt.req, tt.selection)
			assert.Equal(t, tt.want, got)
			for _, user := range checker.users {
				assert.Equal(t, tt.req.InvokingUserID, user, "access policy is evaluated for the invoking user")
			}
		})
	}
}
