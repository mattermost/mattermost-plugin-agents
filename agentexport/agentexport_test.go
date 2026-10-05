// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentexport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentdocs"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validDocument() Document {
	return Document{
		Kind:          Kind,
		SchemaVersion: SchemaVersion,
		AgentVersion:  3,
		Agent: Agent{
			Name:               "release-helper",
			DisplayName:        "Release Helper",
			CustomInstructions: "help with releases",
			MaxToolTurns:       30,
		},
	}
}

func exportedDocument(name, content string) AgentDocument {
	sum := sha256.Sum256([]byte(content))
	return AgentDocument{Name: name, MimeType: agentdocs.MimeTypeText, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), Content: []byte(content)}
}

func TestServers(t *testing.T) {
	cfg := config.MCPConfig{
		Servers: []config.MCPServerConfig{
			{Name: "Jira", BaseURL: "https://jira.example.com/mcp/", Enabled: true},
			{Name: "Disabled", BaseURL: "https://off.example.com/mcp", Enabled: false},
			{Name: "Duplicate", BaseURL: "https://jira.example.com/mcp", Enabled: true},
			{Name: "", BaseURL: "https://unnamed.example.com"},
			{Name: "No URL", BaseURL: "  "},
		},
		EmbeddedServer: config.MCPEmbeddedServerConfig{Enabled: true},
		PluginServers: []config.PluginServerConfig{
			{PluginID: "com.example.plugin", Name: "Example"},
			{PluginID: "com.example.nameless"},
			{Name: "No plugin ID"},
		},
	}

	tests := []struct {
		name     string
		mutate   func(cfg *config.MCPConfig)
		expected []Server
	}{
		{
			name: "all configured servers with their configured origins, deduplicated on normalized origin",
			expected: []Server{
				{Origin: "https://jira.example.com/mcp/", Name: "Jira"},
				{Origin: "https://off.example.com/mcp", Name: "Disabled"},
				{Origin: "https://unnamed.example.com", Name: "https://unnamed.example.com"},
				{Origin: config.MCPEmbeddedServerOrigin, Name: "Mattermost"},
				{Origin: "plugin://com.example.plugin", Name: "Example"},
				{Origin: "plugin://com.example.nameless", Name: "com.example.nameless"},
			},
		},
		{
			name:   "disabled embedded server is not offered",
			mutate: func(cfg *config.MCPConfig) { cfg.EmbeddedServer.Enabled = false },
			expected: []Server{
				{Origin: "https://jira.example.com/mcp/", Name: "Jira"},
				{Origin: "https://off.example.com/mcp", Name: "Disabled"},
				{Origin: "https://unnamed.example.com", Name: "https://unnamed.example.com"},
				{Origin: "plugin://com.example.plugin", Name: "Example"},
				{Origin: "plugin://com.example.nameless", Name: "com.example.nameless"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cfg
			if tt.mutate != nil {
				tt.mutate(&c)
			}
			assert.Equal(t, tt.expected, Servers(c))
		})
	}
}

func TestNewCarriesOnlyTransferableFields(t *testing.T) {
	agent := &llm.BotConfig{
		ID:                    "agentid",
		Name:                  "release-helper",
		DisplayName:           "Release Helper",
		CustomInstructions:    "help",
		ServiceID:             "svc-secret-id",
		Model:                 "gpt-x",
		EnableVision:          true,
		DisableTools:          true,
		ChannelAccessLevel:    llm.ChannelAccessLevelAllow,
		ChannelIDs:            []string{"channel-id"},
		UserAccessLevel:       llm.UserAccessLevelAllow,
		UserIDs:               []string{"user-id"},
		TeamIDs:               []string{"team-id"},
		EnabledNativeTools:    []string{"web_search"},
		ReasoningEnabled:      true,
		ReasoningEffort:       "high",
		ThinkingBudget:        2048,
		UseServiceAccountAuth: true,
		MaxToolTurns:          12,
		MCPDynamicToolLoading: true,
		EnabledMCPTools: []llm.EnabledMCPTool{
			{ServerOrigin: "https://jira.example.com/mcp/", ToolName: "create_issue"},
			{ServerOrigin: "https://jira.example.com/mcp", ToolName: "create_issue"},
			{ServerOrigin: config.MCPEmbeddedServerOrigin, ToolName: "search_posts"},
			{ServerOrigin: "https://gone.example.com", ToolName: "x"},
		},
		BotUserID:    "bot-user-id",
		CreatorID:    "creator-id",
		AdminUserIDs: []string{"admin-id"},
		CreateAt:     1,
		UpdateAt:     2,
	}

	documents := []AgentDocument{exportedDocument("handbook.txt", "handbook text")}
	doc := New(agent, 7, []Server{{Origin: "https://jira.example.com/mcp", Name: "Jira"}}, 1234, documents)

	assert.Equal(t, Document{
		Kind:          Kind,
		SchemaVersion: SchemaVersion,
		ExportedAt:    1234,
		AgentVersion:  7,
		Agent: Agent{
			Name:                  "release-helper",
			DisplayName:           "Release Helper",
			CustomInstructions:    "help",
			DisableTools:          true,
			MaxToolTurns:          12,
			MCPDynamicToolLoading: true,
			MCPTools: []MCPTool{
				{ServerOrigin: "https://jira.example.com/mcp", ServerName: "Jira", ToolName: "create_issue"},
				{ServerOrigin: config.MCPEmbeddedServerOrigin, ServerName: "Mattermost", ToolName: "search_posts"},
				{ServerOrigin: "https://gone.example.com", ServerName: "", ToolName: "x"},
			},
			Documents: documents,
		},
	}, doc)

	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	for _, leaked := range []string{"svc-secret-id", "gpt-x", "channel-id", "user-id", "team-id", "bot-user-id", "creator-id", "admin-id", "agentid", "web_search", "high"} {
		assert.NotContains(t, string(raw), leaked)
	}
}

func TestNewAlwaysHasMCPToolsAndDocumentsArrays(t *testing.T) {
	doc := New(&llm.BotConfig{Name: "a"}, 1, nil, 0, nil)
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"mcpTools":[]`)
	assert.Contains(t, string(raw), `"documents":[]`)
	assert.Contains(t, string(raw), `"schemaVersion":2`)
}

func TestDocumentContentIsBase64(t *testing.T) {
	doc := New(&llm.BotConfig{Name: "a"}, 1, nil, 0, []AgentDocument{exportedDocument("a.txt", "hello")})
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"content":"aGVsbG8="`)

	var decoded Document
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, []byte("hello"), decoded.Agent.Documents[0].Content)

	stripped, err := json.Marshal(decoded.WithoutDocumentContent())
	require.NoError(t, err)
	assert.NotContains(t, string(stripped), "content")
	assert.Equal(t, []byte("hello"), decoded.Agent.Documents[0].Content, "stripping does not modify the original")
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(d *Document)
		errSubstr string
		check     func(t *testing.T, d Document)
	}{
		{
			name:      "wrong kind",
			mutate:    func(d *Document) { d.Kind = "something-else" },
			errSubstr: "not a Mattermost agent export",
		},
		{
			name:      "unsupported schema version",
			mutate:    func(d *Document) { d.SchemaVersion = 3 },
			errSubstr: "unsupported agent export schema version 3",
		},
		{
			name:      "schema version 0",
			mutate:    func(d *Document) { d.SchemaVersion = 0 },
			errSubstr: "unsupported agent export schema version 0",
		},
		{
			name:   "schema version 1 without documents is accepted",
			mutate: func(d *Document) { d.SchemaVersion = 1 },
			check: func(t *testing.T, d Document) {
				assert.Equal(t, 1, d.SchemaVersion)
				assert.Empty(t, d.Agent.Documents)
			},
		},
		{
			name: "schema version 1 ignores documents, even invalid ones",
			mutate: func(d *Document) {
				d.SchemaVersion = 1
				tampered := exportedDocument("a.txt", "text")
				tampered.Content = []byte("tampered")
				d.Agent.Documents = []AgentDocument{exportedDocument("handbook.txt", "text"), tampered}
			},
			check: func(t *testing.T, d Document) {
				assert.Equal(t, []AgentDocument{}, d.Agent.Documents)
			},
		},
		{
			name: "documents are kept with trimmed names and lower-case checksums",
			mutate: func(d *Document) {
				doc := exportedDocument("  handbook.txt ", "text")
				doc.SHA256 = strings.ToUpper(doc.SHA256)
				d.Agent.Documents = []AgentDocument{doc}
			},
			check: func(t *testing.T, d Document) {
				want := exportedDocument("handbook.txt", "text")
				assert.Equal(t, []AgentDocument{want}, d.Agent.Documents)
			},
		},
		{
			name: "document checksum mismatch",
			mutate: func(d *Document) {
				doc := exportedDocument("a.txt", "text")
				doc.Content = []byte("tampered")
				doc.Size = int64(len(doc.Content))
				d.Agent.Documents = []AgentDocument{doc}
			},
			errSubstr: `reference document "a.txt" does not match its sha256 checksum`,
		},
		{
			name: "document size mismatch",
			mutate: func(d *Document) {
				doc := exportedDocument("a.txt", "text")
				doc.Size++
				d.Agent.Documents = []AgentDocument{doc}
			},
			errSubstr: `reference document "a.txt" is 4 bytes but its size says 5`,
		},
		{
			name: "document of an unsupported type",
			mutate: func(d *Document) {
				doc := exportedDocument("a.docx", "text")
				doc.MimeType = "application/msword"
				d.Agent.Documents = []AgentDocument{doc}
			},
			errSubstr: `unsupported type "application/msword"`,
		},
		{
			name: "document without content",
			mutate: func(d *Document) {
				d.Agent.Documents = []AgentDocument{exportedDocument("a.txt", "")}
			},
			errSubstr: `reference document "a.txt" has no content`,
		},
		{
			name: "document with an invalid name",
			mutate: func(d *Document) {
				d.Agent.Documents = []AgentDocument{exportedDocument("../a.txt", "text")}
			},
			errSubstr: "reference document 1: document name",
		},
		{
			name: "too many documents",
			mutate: func(d *Document) {
				for range agentdocs.MaxDocumentsPerAgent + 1 {
					d.Agent.Documents = append(d.Agent.Documents, exportedDocument("a.txt", "text"))
				}
			},
			errSubstr: "at most 20 reference documents",
		},
		{
			name: "documents over the total size limit",
			mutate: func(d *Document) {
				big := strings.Repeat("a", agentdocs.MaxDocumentBytes)
				for range 3 {
					d.Agent.Documents = append(d.Agent.Documents, exportedDocument("big.txt", big))
				}
			},
			errSubstr: "exceed the limit of 26214400 bytes in total",
		},
		{
			name:      "oversized instructions",
			mutate:    func(d *Document) { d.Agent.CustomInstructions = strings.Repeat("a", llm.MaxCustomInstructionsRunes+1) },
			errSubstr: "customInstructions exceeds maximum length",
		},
		{
			name:      "negative max tool turns",
			mutate:    func(d *Document) { d.Agent.MaxToolTurns = -1 },
			errSubstr: "maxToolTurns must be between",
		},
		{
			name:      "too many max tool turns",
			mutate:    func(d *Document) { d.Agent.MaxToolTurns = llm.MaxAllowedMaxToolTurns + 1 },
			errSubstr: "maxToolTurns must be between",
		},
		{
			name:      "tool without origin",
			mutate:    func(d *Document) { d.Agent.MCPTools = []MCPTool{{ToolName: "x"}} },
			errSubstr: "MCP tool entry 1",
		},
		{
			name: "origins normalized and duplicates removed",
			mutate: func(d *Document) {
				d.Agent.MCPTools = []MCPTool{
					{ServerOrigin: " https://a.example.com/ ", ServerName: "A", ToolName: " t1 "},
					{ServerOrigin: "https://a.example.com", ToolName: "t1"},
					{ServerOrigin: "https://a.example.com", ToolName: "t2"},
				}
			},
			check: func(t *testing.T, d Document) {
				assert.Equal(t, []MCPTool{
					{ServerOrigin: "https://a.example.com", ServerName: "A", ToolName: "t1"},
					{ServerOrigin: "https://a.example.com", ToolName: "t2"},
				}, d.Agent.MCPTools)
			},
		},
		{
			name: "missing tool list becomes empty",
			check: func(t *testing.T, d Document) {
				assert.NotNil(t, d.Agent.MCPTools)
				assert.Empty(t, d.Agent.MCPTools)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validDocument()
			if tt.mutate != nil {
				tt.mutate(&doc)
			}
			normalized, err := doc.Normalize()
			if tt.errSubstr != "" {
				require.ErrorIs(t, err, ErrInvalidDocument)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			tt.check(t, normalized)
		})
	}
}

func TestDocumentUnmarshal(t *testing.T) {
	tests := []struct {
		name            string
		raw             string
		expectedDynamic bool
	}{
		{name: "dynamic tool loading defaults on when absent", raw: `{"kind":"mattermost-agent","schemaVersion":1,"agent":{"name":"a"},"unknown":1}`, expectedDynamic: true},
		{name: "explicit false is kept", raw: `{"kind":"mattermost-agent","schemaVersion":1,"agent":{"name":"a","mcpDynamicToolLoading":false}}`, expectedDynamic: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc Document
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &doc))
			assert.Equal(t, tt.expectedDynamic, doc.Agent.MCPDynamicToolLoading)
		})
	}
}

func TestServerGroups(t *testing.T) {
	doc := validDocument()
	doc.Agent.MCPTools = []MCPTool{
		{ServerOrigin: "https://jira.example.com/mcp", ServerName: "Jira", ToolName: "create_issue"},
		{ServerOrigin: "https://gone.example.com", ToolName: "a"},
		{ServerOrigin: "https://jira.example.com/mcp", ServerName: "Jira", ToolName: "get_issue"},
	}
	doc, err := doc.Normalize()
	require.NoError(t, err)

	groups := doc.ServerGroups([]Server{{Origin: "https://jira.example.com/mcp/", Name: "Local Jira"}})
	assert.Equal(t, []ServerGroup{
		{SourceOrigin: "https://jira.example.com/mcp", SourceName: "Jira", ToolNames: []string{"create_issue", "get_issue"}, AutoTargetOrigin: "https://jira.example.com/mcp/"},
		{SourceOrigin: "https://gone.example.com", SourceName: "https://gone.example.com", ToolNames: []string{"a"}, AutoTargetOrigin: ""},
	}, groups)

	empty, err := validDocument().Normalize()
	require.NoError(t, err)
	assert.Equal(t, []ServerGroup{}, empty.ServerGroups(nil))
}

func TestResolveMCPTools(t *testing.T) {
	available := []Server{
		{Origin: "https://jira.example.com/mcp", Name: "Jira"},
		{Origin: "https://new.example.com/mcp", Name: "New"},
		{Origin: "https://slash.example.com/mcp/", Name: "Trailing slash"},
		{Origin: config.MCPEmbeddedServerOrigin, Name: "Mattermost"},
	}
	tools := []MCPTool{
		{ServerOrigin: "https://jira.example.com/mcp", ServerName: "Jira", ToolName: "create_issue"},
		{ServerOrigin: "https://old.example.com/mcp", ServerName: "Old", ToolName: "create_issue"},
		{ServerOrigin: "https://old.example.com/mcp", ServerName: "Old", ToolName: "search"},
		{ServerOrigin: "https://gone.example.com", ServerName: "Gone", ToolName: "x"},
	}

	tests := []struct {
		name       string
		autoEnable bool
		mappings   []ServerMapping
		expected   []llm.EnabledMCPTool
		errSubstr  string
	}{
		{
			name:      "unmapped server without a same-origin match is rejected",
			mappings:  []ServerMapping{{SourceOrigin: "https://old.example.com/mcp", TargetOrigin: "https://new.example.com/mcp"}},
			errSubstr: `MCP server "Gone" (https://gone.example.com) must be mapped to a server on this instance or removed`,
		},
		{
			name: "explicit mapping, removal, and auto-mapping",
			mappings: []ServerMapping{
				{SourceOrigin: "https://old.example.com/mcp/", TargetOrigin: "https://new.example.com/mcp"},
				{SourceOrigin: "https://gone.example.com", TargetOrigin: ""},
			},
			expected: []llm.EnabledMCPTool{
				{ServerOrigin: "https://jira.example.com/mcp", ToolName: "create_issue"},
				{ServerOrigin: "https://new.example.com/mcp", ToolName: "create_issue"},
				{ServerOrigin: "https://new.example.com/mcp", ToolName: "search"},
			},
		},
		{
			name: "explicit mapping wins over the same-origin match and duplicates collapse",
			mappings: []ServerMapping{
				{SourceOrigin: "https://jira.example.com/mcp", TargetOrigin: "https://new.example.com/mcp"},
				{SourceOrigin: "https://old.example.com/mcp", TargetOrigin: "https://new.example.com/mcp"},
				{SourceOrigin: "https://gone.example.com", TargetOrigin: ""},
			},
			expected: []llm.EnabledMCPTool{
				{ServerOrigin: "https://new.example.com/mcp", ToolName: "create_issue"},
				{ServerOrigin: "https://new.example.com/mcp", ToolName: "search"},
			},
		},
		{
			name: "explicit mapping matches a trailing-slash server on normalized origin",
			mappings: []ServerMapping{
				{SourceOrigin: "https://old.example.com/mcp", TargetOrigin: "https://slash.example.com/mcp"},
				{SourceOrigin: "https://gone.example.com", TargetOrigin: ""},
			},
			expected: []llm.EnabledMCPTool{
				{ServerOrigin: "https://jira.example.com/mcp", ToolName: "create_issue"},
				{ServerOrigin: "https://slash.example.com/mcp/", ToolName: "create_issue"},
				{ServerOrigin: "https://slash.example.com/mcp/", ToolName: "search"},
			},
		},
		{
			name: "mapping to a server that is not configured is rejected",
			mappings: []ServerMapping{
				{SourceOrigin: "https://old.example.com/mcp", TargetOrigin: "https://elsewhere.example.com"},
				{SourceOrigin: "https://gone.example.com", TargetOrigin: ""},
			},
			errSubstr: `MCP server "https://elsewhere.example.com" is not configured on this server`,
		},
		{
			name:       "auto-enable ignores the allowlist and mappings",
			autoEnable: true,
			mappings:   []ServerMapping{{SourceOrigin: "https://old.example.com/mcp", TargetOrigin: "https://elsewhere.example.com"}},
			expected:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validDocument()
			doc.Agent.AutoEnableNewMCPTools = tt.autoEnable
			doc.Agent.MCPTools = tools
			doc, err := doc.Normalize()
			require.NoError(t, err)

			got, err := doc.ResolveMCPTools(tt.mappings, available)
			if tt.errSubstr != "" {
				require.ErrorIs(t, err, ErrInvalidDocument)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// The editor and the runtime match an agent's allowlist against the origin a
// server's tools carry, which is its BaseURL exactly as configured.
func TestResolveMCPToolsAutoMapsToConfiguredOrigin(t *testing.T) {
	doc := validDocument()
	doc.Agent.MCPTools = []MCPTool{{ServerOrigin: "https://jira.example.com/mcp", ServerName: "Jira", ToolName: "create_issue"}}
	doc, err := doc.Normalize()
	require.NoError(t, err)

	available := Servers(config.MCPConfig{Servers: []config.MCPServerConfig{
		{Name: "Jira", BaseURL: "https://jira.example.com/mcp/", Enabled: true},
	}})
	require.Equal(t, "https://jira.example.com/mcp/", doc.ServerGroups(available)[0].AutoTargetOrigin)

	got, err := doc.ResolveMCPTools(nil, available)
	require.NoError(t, err)
	assert.Equal(t, []llm.EnabledMCPTool{{ServerOrigin: "https://jira.example.com/mcp/", ToolName: "create_issue"}}, got)
}
