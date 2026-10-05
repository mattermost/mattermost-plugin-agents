// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package agentexport defines the portable agent export document and the
// rules for building it from an agent and applying it on another server.
//
// The document carries only configuration that means the same thing on
// every server: the AI service, model, provider-specific settings, access
// rules, admins and identifiers are deliberately absent.
package agentexport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/agentdocs"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

const (
	// Kind identifies an agent export document.
	Kind = "mattermost-agent"
	// SchemaVersion is the document schema version this server writes.
	// Version 2 added reference documents.
	SchemaVersion = 2
	// MinSchemaVersion is the oldest document schema version this server reads.
	MinSchemaVersion = 1
	// DocumentsSchemaVersion is the first schema version that carries
	// reference documents.
	DocumentsSchemaVersion = 2

	embeddedServerName = "Mattermost"
)

// Document is an exported agent.
type Document struct {
	Kind          string `json:"kind"`
	SchemaVersion int    `json:"schemaVersion"`
	ExportedAt    int64  `json:"exportedAt"`
	// AgentVersion is the source agent's version number at export time.
	AgentVersion int   `json:"agentVersion"`
	Agent        Agent `json:"agent"`
}

// Agent holds the transferable agent configuration.
type Agent struct {
	// Name and DisplayName are suggestions for creating a new agent; they are
	// ignored when the document updates an existing agent.
	Name                  string `json:"name"`
	DisplayName           string `json:"displayName"`
	CustomInstructions    string `json:"customInstructions"`
	DisableTools          bool   `json:"disableTools"`
	MaxToolTurns          int    `json:"maxToolTurns"`
	MCPDynamicToolLoading bool   `json:"mcpDynamicToolLoading"`
	// AutoEnableNewMCPTools gives the agent every MCP tool; MCPTools is then
	// carried as stored but ignored on import, as it is at runtime.
	AutoEnableNewMCPTools bool      `json:"autoEnableNewMCPTools"`
	MCPTools              []MCPTool `json:"mcpTools"`
	// Documents are the agent's reference documents (schema version 2+).
	Documents []AgentDocument `json:"documents"`
}

// AgentDocument is an exported reference document with its original bytes.
// Content is encoded as base64 in JSON.
type AgentDocument struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Content  []byte `json:"content,omitempty"`
}

// UnmarshalJSON defaults MCPDynamicToolLoading to true when the field is
// absent, matching llm.BotConfig.
func (a *Agent) UnmarshalJSON(data []byte) error {
	type agentAlias Agent
	decoded := agentAlias{MCPDynamicToolLoading: true}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*a = Agent(decoded)
	return nil
}

// MCPTool is one allowed MCP tool. Matching across servers is by
// ServerOrigin; ServerName is informational.
type MCPTool struct {
	ServerOrigin string `json:"serverOrigin"`
	ServerName   string `json:"serverName"`
	ToolName     string `json:"toolName"`
}

// Server is an MCP server configured on this instance. Origin is the origin
// exactly as configured, which is what its tools carry at runtime and what an
// agent's allowlist must store; servers are matched on its normalized form.
type Server struct {
	Origin string `json:"origin"`
	Name   string `json:"name"`
}

// ServerGroup is the set of document tools that come from one MCP server.
type ServerGroup struct {
	SourceOrigin string   `json:"sourceOrigin"`
	SourceName   string   `json:"sourceName"`
	ToolNames    []string `json:"toolNames"`
	// AutoTargetOrigin is the configured origin of the server on this
	// instance with the same normalized origin, or "" when there is none.
	AutoTargetOrigin string `json:"autoTargetOrigin"`
}

// ServerMapping maps a document MCP server to a server on this instance. An
// empty TargetOrigin removes that server's tools.
type ServerMapping struct {
	SourceOrigin string `json:"sourceOrigin"`
	TargetOrigin string `json:"targetOrigin"`
}

// ErrInvalidDocument wraps every document validation and MCP mapping
// failure; all of them are caused by the request, not the server.
var ErrInvalidDocument = errors.New("invalid agent export document")

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidDocument, fmt.Sprintf(format, args...))
}

// Servers lists the MCP servers configured on this instance: every external
// server with a base URL and every registered plugin server, enabled or not
// (an agent can reference tools of a disabled server, which become available
// when it is enabled), plus the embedded Mattermost server when enabled.
func Servers(cfg config.MCPConfig) []Server {
	servers := make([]Server, 0, len(cfg.Servers)+len(cfg.PluginServers)+1)
	for _, s := range cfg.Servers {
		servers = append(servers, Server{Origin: s.BaseURL, Name: s.Name})
	}
	if cfg.EmbeddedServer.Enabled {
		servers = append(servers, Server{Origin: config.MCPEmbeddedServerOrigin, Name: embeddedServerName})
	}
	for _, p := range cfg.PluginServers {
		if p.PluginID == "" {
			continue
		}
		name := p.Name
		if strings.TrimSpace(name) == "" {
			name = p.PluginID
		}
		servers = append(servers, Server{Origin: config.PluginServerOrigin(p.PluginID), Name: name})
	}
	return UniqueServers(servers)
}

// UniqueServers drops servers without an origin and every server whose
// normalized origin repeats an earlier one, and names unnamed servers after
// their origin. Origins are kept as given.
func UniqueServers(servers []Server) []Server {
	unique := make([]Server, 0, len(servers))
	seen := make(map[string]struct{}, len(servers))
	for _, s := range servers {
		key := llm.NormalizeMCPServerOrigin(s.Origin)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(s.Name) == "" {
			s.Name = key
		}
		unique = append(unique, s)
	}
	return unique
}

// New builds the export document for agent at version. servers resolves the
// informational server names of the agent's MCP tools; documents are the
// agent's reference documents with their content.
func New(agent *llm.BotConfig, version int, servers []Server, exportedAt int64, documents []AgentDocument) Document {
	names := make(map[string]string, len(servers))
	for _, s := range servers {
		names[llm.NormalizeMCPServerOrigin(s.Origin)] = s.Name
	}

	tools := make([]MCPTool, 0, len(agent.EnabledMCPTools))
	for _, t := range agent.EnabledMCPTools {
		origin := llm.NormalizeMCPServerOrigin(t.ServerOrigin)
		name := names[origin]
		if name == "" && origin == config.MCPEmbeddedServerOrigin {
			name = embeddedServerName
		}
		tools = append(tools, MCPTool{ServerOrigin: origin, ServerName: name, ToolName: t.ToolName})
	}

	doc := Document{
		Kind:          Kind,
		SchemaVersion: SchemaVersion,
		ExportedAt:    exportedAt,
		AgentVersion:  version,
		Agent: Agent{
			Name:                  agent.Name,
			DisplayName:           agent.DisplayName,
			CustomInstructions:    agent.CustomInstructions,
			DisableTools:          agent.DisableTools,
			MaxToolTurns:          agent.MaxToolTurns,
			MCPDynamicToolLoading: agent.MCPDynamicToolLoading,
			AutoEnableNewMCPTools: agent.AutoEnableNewMCPTools,
			MCPTools:              tools,
			Documents:             append([]AgentDocument{}, documents...),
		},
	}
	doc.normalizeTools()
	return doc
}

// Normalize validates d and returns it with MCP origins normalized, tool
// names trimmed and duplicate tools removed. Every error wraps
// ErrInvalidDocument.
func (d Document) Normalize() (Document, error) {
	if d.Kind != Kind {
		return Document{}, invalidf("this file is not a Mattermost agent export (expected kind %q)", Kind)
	}
	if d.SchemaVersion < MinSchemaVersion || d.SchemaVersion > SchemaVersion {
		return Document{}, invalidf("unsupported agent export schema version %d; this server supports versions %d to %d", d.SchemaVersion, MinSchemaVersion, SchemaVersion)
	}
	if utf8.RuneCountInString(d.Agent.CustomInstructions) > llm.MaxCustomInstructionsRunes {
		return Document{}, invalidf("customInstructions exceeds maximum length of %d characters", llm.MaxCustomInstructionsRunes)
	}
	if d.Agent.MaxToolTurns < 0 || d.Agent.MaxToolTurns > llm.MaxAllowedMaxToolTurns {
		return Document{}, invalidf("maxToolTurns must be between 0 and %d", llm.MaxAllowedMaxToolTurns)
	}
	for i, t := range d.Agent.MCPTools {
		if llm.NormalizeMCPServerOrigin(t.ServerOrigin) == "" || strings.TrimSpace(t.ToolName) == "" {
			return Document{}, invalidf("MCP tool entry %d must have a serverOrigin and a toolName", i+1)
		}
	}

	// Documents from before DocumentsSchemaVersion cannot carry reference
	// documents, so anything under that key is ignored rather than imported.
	documents := []AgentDocument{}
	if d.SchemaVersion >= DocumentsSchemaVersion {
		var err error
		if documents, err = normalizeDocuments(d.Agent.Documents); err != nil {
			return Document{}, err
		}
	}

	d.Agent.MCPTools = append([]MCPTool(nil), d.Agent.MCPTools...)
	d.normalizeTools()
	d.Agent.Documents = documents
	return d, nil
}

// normalizeDocuments checks each document's name, type, size and checksum
// against its decoded content and the per-agent count and size limits.
// Extracted-text limits are checked by whoever extracts the text.
func normalizeDocuments(docs []AgentDocument) ([]AgentDocument, error) {
	if len(docs) > agentdocs.MaxDocumentsPerAgent {
		return nil, invalidf("an agent can have at most %d reference documents (the file has %d)", agentdocs.MaxDocumentsPerAgent, len(docs))
	}
	normalized := make([]AgentDocument, 0, len(docs))
	var total int64
	for i, doc := range docs {
		name, err := agentdocs.NormalizeName(doc.Name)
		if err != nil {
			return nil, invalidf("reference document %d: %s", i+1, err)
		}
		if !agentdocs.IsSupportedMimeType(doc.MimeType) {
			return nil, invalidf("reference document %q has an unsupported type %q", name, doc.MimeType)
		}
		if len(doc.Content) == 0 {
			return nil, invalidf("reference document %q has no content", name)
		}
		if len(doc.Content) > agentdocs.MaxDocumentBytes {
			return nil, invalidf("reference document %q is larger than %d bytes", name, agentdocs.MaxDocumentBytes)
		}
		if doc.Size != int64(len(doc.Content)) {
			return nil, invalidf("reference document %q is %d bytes but its size says %d; the file may be damaged", name, len(doc.Content), doc.Size)
		}
		sum := sha256.Sum256(doc.Content)
		if checksum := hex.EncodeToString(sum[:]); !strings.EqualFold(doc.SHA256, checksum) {
			return nil, invalidf("reference document %q does not match its sha256 checksum; the file may be damaged", name)
		}
		total += doc.Size
		doc.Name = name
		doc.SHA256 = strings.ToLower(doc.SHA256)
		normalized = append(normalized, doc)
	}
	if total > agentdocs.MaxTotalBytesPerAgent {
		return nil, invalidf("reference documents exceed the limit of %d bytes in total (have %d)", agentdocs.MaxTotalBytesPerAgent, total)
	}
	return normalized, nil
}

// WithoutDocumentContent returns d with its documents' content removed, for
// echoing a document back without the bulk of its bytes.
func (d Document) WithoutDocumentContent() Document {
	docs := make([]AgentDocument, len(d.Agent.Documents))
	for i, doc := range d.Agent.Documents {
		doc.Content = nil
		docs[i] = doc
	}
	d.Agent.Documents = docs
	return d
}

func (d *Document) normalizeTools() {
	tools := make([]MCPTool, 0, len(d.Agent.MCPTools))
	seen := make(map[llm.EnabledMCPTool]struct{}, len(d.Agent.MCPTools))
	for _, t := range d.Agent.MCPTools {
		t.ServerOrigin = llm.NormalizeMCPServerOrigin(t.ServerOrigin)
		t.ServerName = strings.TrimSpace(t.ServerName)
		t.ToolName = strings.TrimSpace(t.ToolName)
		key := llm.EnabledMCPTool{ServerOrigin: t.ServerOrigin, ToolName: t.ToolName}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		tools = append(tools, t)
	}
	d.Agent.MCPTools = tools
}

// ServerGroups groups d's MCP tools by server origin, in order of first
// appearance, and pairs each group with the same-origin server in available.
// d must be normalized.
func (d Document) ServerGroups(available []Server) []ServerGroup {
	configured := configuredOrigins(available)
	var groups []ServerGroup
	index := make(map[string]int)
	for _, t := range d.Agent.MCPTools {
		i, ok := index[t.ServerOrigin]
		if !ok {
			groups = append(groups, ServerGroup{SourceOrigin: t.ServerOrigin, AutoTargetOrigin: configured[t.ServerOrigin], ToolNames: []string{}})
			i = len(groups) - 1
			index[t.ServerOrigin] = i
		}
		if groups[i].SourceName == "" {
			groups[i].SourceName = t.ServerName
		}
		groups[i].ToolNames = append(groups[i].ToolNames, t.ToolName)
	}
	for i := range groups {
		if groups[i].SourceName == "" {
			groups[i].SourceName = groups[i].SourceOrigin
		}
	}
	if groups == nil {
		return []ServerGroup{}
	}
	return groups
}

// ResolveMCPTools returns the MCP tool allowlist d grants on this instance.
// Per document server, an explicit mapping wins; otherwise the same-origin
// server in available is used; otherwise the server must be mapped or
// removed. Targets match available servers on normalized origin, and tools
// keep their names but take the target's configured origin. In auto-enable
// mode the allowlist is unused, so mappings are ignored and nil is returned.
// d must be normalized. Every error wraps ErrInvalidDocument.
func (d Document) ResolveMCPTools(mappings []ServerMapping, available []Server) ([]llm.EnabledMCPTool, error) {
	if d.Agent.AutoEnableNewMCPTools {
		return nil, nil
	}

	configured := configuredOrigins(available)
	explicit := make(map[string]string, len(mappings))
	for _, m := range mappings {
		explicit[llm.NormalizeMCPServerOrigin(m.SourceOrigin)] = llm.NormalizeMCPServerOrigin(m.TargetOrigin)
	}

	targets := make(map[string]string)
	for _, g := range d.ServerGroups(available) {
		target, mapped := explicit[g.SourceOrigin]
		switch {
		case mapped && target == "":
			// Removed: none of this server's tools are kept.
		case mapped:
			origin, ok := configured[target]
			if !ok {
				return nil, invalidf("MCP server %q is not configured on this server", target)
			}
			target = origin
		case g.AutoTargetOrigin != "":
			target = g.AutoTargetOrigin
		default:
			return nil, invalidf("MCP server %q (%s) must be mapped to a server on this instance or removed", g.SourceName, g.SourceOrigin)
		}
		targets[g.SourceOrigin] = target
	}

	var tools []llm.EnabledMCPTool
	seen := make(map[llm.EnabledMCPTool]struct{})
	for _, t := range d.Agent.MCPTools {
		target := targets[t.ServerOrigin]
		if target == "" {
			continue
		}
		tool := llm.EnabledMCPTool{ServerOrigin: target, ToolName: t.ToolName}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		tools = append(tools, tool)
	}
	return tools, nil
}

// configuredOrigins maps each normalized origin in servers to the first
// server's configured origin.
func configuredOrigins(servers []Server) map[string]string {
	origins := make(map[string]string, len(servers))
	for _, s := range servers {
		key := llm.NormalizeMCPServerOrigin(s.Origin)
		if _, ok := origins[key]; key != "" && !ok {
			origins[key] = s.Origin
		}
	}
	return origins
}
