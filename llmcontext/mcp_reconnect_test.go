// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llmcontext

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

func TestMCPToolErrorResult(t *testing.T) {
	const (
		ticketsOrigin = "https://tickets.example.com/mcp"
		twinOrigin    = ticketsOrigin + "/"
		githubOrigin  = "https://github.example.com/mcp"
	)
	lapsed := &mcp.ReconnectNeededError{
		Server: llm.UnconnectedMCPServer{
			Name:         "Tickets",
			ServerOrigin: ticketsOrigin,
			Description:  "Support tickets",
			Tools: []llm.ToolInfo{
				{Name: "search", ServerOrigin: ticketsOrigin},
				{Name: "close", ServerOrigin: ticketsOrigin},
				{Name: "delete", ServerOrigin: ticketsOrigin},
			},
		},
		Err: errors.New("401"),
	}
	alreadyListed := []llm.UnconnectedMCPServer{{Name: "GitHub", ServerOrigin: githubOrigin}}

	tests := []struct {
		name        string
		err         error
		offer       bool
		listed      []llm.UnconnectedMCPServer
		wantResult  string
		wantOffered []string
		wantRemoved bool
	}{
		{
			name:       "other failures keep their text and the catalog",
			err:        errors.New("tool exploded"),
			offer:      true,
			wantResult: "tool exploded",
		},
		{
			name:        "lapsed authorization offers the server for the rest of the turn",
			err:         lapsed,
			offer:       true,
			wantResult:  lapsed.ConnectGuidance(),
			wantOffered: []string{"Tickets"},
			wantRemoved: true,
		},
		{
			name:        "lapsed authorization joins servers already offered",
			err:         lapsed,
			offer:       true,
			listed:      alreadyListed,
			wantResult:  lapsed.ConnectGuidance(),
			wantOffered: []string{"GitHub", "Tickets"},
			wantRemoved: true,
		},
		{
			name:        "without connection requests the server's tools still go away",
			err:         lapsed,
			wantResult:  lapsed.Error(),
			wantRemoved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := llm.NewToolStore()
			store.AddTools([]llm.Tool{
				{Name: "tickets__search", ServerOrigin: ticketsOrigin},
				{Name: "twin__search", ServerOrigin: twinOrigin},
			})
			store.SetUnloadedMCPTools([]llm.Tool{{Name: "tickets__close", ServerOrigin: ticketsOrigin}})
			c := &llm.Context{
				Tools:                 store,
				UnconnectedMCPServers: slices.Clone(tt.listed),
			}
			if tt.offer {
				c.ToolCatalog.MCPServerOrigins = map[string]string{"Tickets": ticketsOrigin}
			}

			assert.Equal(t, tt.wantResult, MCPToolErrorResult(c, tt.err))

			assert.Equal(t, !tt.wantRemoved, store.GetTool("tickets__search") != nil)
			assert.Equal(t, !tt.wantRemoved, store.IsUnloadedMCPTool("tickets__close"))
			assert.NotNil(t, store.GetTool("twin__search"), "a server whose URL differs only by a slash keeps its tools")

			connect := store.GetTool(mcp.RequestConnectionToolName)
			if tt.wantOffered == nil {
				assert.Nil(t, connect)
				return
			}
			require.NotNil(t, connect)
			for _, name := range tt.wantOffered {
				assert.NoError(t, connect.ValidateArguments([]byte(`{"server":"`+name+`"}`)))
			}
			var offered []string
			for _, server := range c.UnconnectedMCPServers {
				offered = append(offered, server.Name)
			}
			assert.Equal(t, tt.wantOffered, offered)
			tickets := c.UnconnectedMCPServers[len(c.UnconnectedMCPServers)-1]
			assert.Equal(t, []llm.ToolInfo{{Name: "search", ServerOrigin: ticketsOrigin}, {Name: "close", ServerOrigin: ticketsOrigin}}, tickets.Tools,
				"only tools the agent could use from the server are described")
		})
	}
}
