// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentexport"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	agentImportModeCreate = "create"
	agentImportModeUpdate = "update"
)

// ImportAgentPreviewRequest is the JSON body for POST /agents/import/preview.
type ImportAgentPreviewRequest struct {
	Document agentexport.Document `json:"document"`
}

// importExistingAgent identifies the agent an import document would update by default.
type importExistingAgent struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Username    string `json:"username"`
	CanManage   bool   `json:"canManage"`
}

// importAgentPreviewResponse is the body of POST /agents/import/preview.
type importAgentPreviewResponse struct {
	Document            agentexport.Document      `json:"document"`
	MCPServers          []agentexport.ServerGroup `json:"mcpServers"`
	AvailableMCPServers []agentexport.Server      `json:"availableMCPServers"`
	ExistingAgent       *importExistingAgent      `json:"existingAgent"`
}

// ImportAgentRequest is the JSON body for POST /agents/import. Username,
// DisplayName, ServiceID and Model apply to mode "create" only; AgentID to
// mode "update" only.
type ImportAgentRequest struct {
	Document          agentexport.Document        `json:"document"`
	Mode              string                      `json:"mode"`
	AgentID           string                      `json:"agentID"`
	Username          string                      `json:"username"`
	DisplayName       string                      `json:"displayName"`
	ServiceID         string                      `json:"serviceID"`
	Model             string                      `json:"model"`
	MCPServerMappings []agentexport.ServerMapping `json:"mcpServerMappings"`
}

// handleExportAgent handles GET /agents/:agentid/export: the current agent
// configuration as a downloadable export document.
func (a *API) handleExportAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")
	agentID := c.Param("agentid")

	cfg, ok := a.loadManageableAgent(c, agentID, userID, "not authorized to export this agent", nil)
	if !ok {
		return
	}

	version, err := a.agentStore.GetLatestAgentVersion(agentID)
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to get agent version: %w", err))
		return
	}

	doc := agentexport.New(cfg, version, agentexport.Servers(a.pluginConfigOrEmpty().MCP), model.GetMillis())
	// Usernames are limited to [a-z0-9._-]; dropping quote characters keeps
	// the quoted form valid even for unexpected legacy values.
	filename := strings.NewReplacer(`"`, "", `\`, "").Replace(fmt.Sprintf("%s-v%d.agent.json", cfg.Name, version))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.IndentedJSON(http.StatusOK, doc)
}

// checkCanImportAgent allows users who may create agents or manage at least
// one agent; mode-specific checks happen later. It writes the abort response
// and returns false when the caller may not import.
func (a *API) checkCanImportAgent(c *gin.Context, userID string) bool {
	if canCreateAgent(a.pluginAPI, userID) {
		return true
	}
	agents, err := a.agentStore.ListAgents()
	if err != nil {
		abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to list agents: %w", err))
		return false
	}
	for _, agent := range agents {
		if canManageAgent(a.pluginAPI, agent, userID) {
			return true
		}
	}
	abortAgentRequest(c, http.StatusForbidden, errors.New("user does not have permission to import agents"))
	return false
}

// normalizeImportDocument validates doc, aborting with 400 when it is not a
// usable export document.
func normalizeImportDocument(c *gin.Context, doc agentexport.Document) (agentexport.Document, bool) {
	normalized, err := doc.Normalize()
	if err != nil {
		abortAgentRequest(c, http.StatusBadRequest, err)
		return agentexport.Document{}, false
	}
	return normalized, true
}

// handlePreviewAgentImport handles POST /agents/import/preview: validates the
// document and reports how its MCP servers map onto this instance. Read-only.
func (a *API) handlePreviewAgentImport(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	if !a.checkCanImportAgent(c, userID) {
		return
	}

	var req ImportAgentPreviewRequest
	if !bindAgentRequestJSON(c, &req) {
		return
	}
	doc, ok := normalizeImportDocument(c, req.Document)
	if !ok {
		return
	}

	available := agentexport.Servers(a.pluginConfigOrEmpty().MCP)
	resp := importAgentPreviewResponse{
		Document:            doc,
		MCPServers:          doc.ServerGroups(available),
		AvailableMCPServers: available,
	}

	if doc.Agent.Name != "" {
		agents, err := a.agentStore.ListAgents()
		if err != nil {
			abortAgentRequest(c, http.StatusInternalServerError, fmt.Errorf("failed to list agents: %w", err))
			return
		}
		for _, agent := range agents {
			if agent.Name == doc.Agent.Name {
				resp.ExistingAgent = &importExistingAgent{
					ID:          agent.ID,
					DisplayName: agent.DisplayName,
					Username:    agent.Name,
					CanManage:   canManageAgent(a.pluginAPI, agent, userID),
				}
				break
			}
		}
	}

	c.JSON(http.StatusOK, resp)
}

// handleImportAgent handles POST /agents/import: creates a new agent from an
// export document, or applies the document's transferable fields to an
// existing agent, through the same paths as POST and PUT /agents.
func (a *API) handleImportAgent(c *gin.Context) {
	userID := c.GetHeader("Mattermost-User-Id")

	if !a.checkCanImportAgent(c, userID) {
		return
	}

	var req ImportAgentRequest
	if !bindAgentRequestJSON(c, &req) {
		return
	}
	audit.AddParam(auditRec(c), "mode", audit.TruncateID(req.Mode))

	doc, ok := normalizeImportDocument(c, req.Document)
	if !ok {
		return
	}
	meta := store.AgentVersionMeta{ActorID: userID, Source: store.AgentVersionSourceImport}

	switch req.Mode {
	case agentImportModeCreate:
		if !a.checkCanCreateAgent(c, userID) {
			return
		}
		if req.Username == "" || req.DisplayName == "" || req.ServiceID == "" {
			abortAgentRequest(c, http.StatusBadRequest, errors.New("username, displayName and serviceID are required to create an agent"))
			return
		}
		tools, ok := a.resolveImportMCPTools(c, doc, req.MCPServerMappings)
		if !ok {
			return
		}
		agent, ok := a.createAgent(c, userID, a.importCreateRequest(req, doc, tools), meta)
		if !ok {
			return
		}
		c.JSON(http.StatusCreated, agent)

	case agentImportModeUpdate:
		if req.AgentID == "" {
			abortAgentRequest(c, http.StatusBadRequest, errors.New("agentID is required to update an existing agent"))
			return
		}
		audit.AddParam(auditRec(c), audit.KeyAgentID, audit.TruncateID(req.AgentID))
		stored, ok := a.loadManageableAgent(c, req.AgentID, userID, "not authorized to modify this agent", nil)
		if !ok {
			return
		}
		tools, ok := a.resolveImportMCPTools(c, doc, req.MCPServerMappings)
		if !ok {
			return
		}
		fields := requestFieldsFromConfig(stored)
		applyImportedMission(&fields, doc, tools)
		agent, ok := a.updateAgent(c, userID, stored, UpdateAgentRequest{AgentRequestFields: fields}, meta)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, agent)

	default:
		abortAgentRequest(c, http.StatusBadRequest, fmt.Errorf("mode must be %q or %q", agentImportModeCreate, agentImportModeUpdate))
	}
}

func (a *API) resolveImportMCPTools(c *gin.Context, doc agentexport.Document, mappings []agentexport.ServerMapping) ([]llm.EnabledMCPTool, bool) {
	tools, err := doc.ResolveMCPTools(mappings, agentexport.Servers(a.pluginConfigOrEmpty().MCP))
	if err != nil {
		abortAgentRequest(c, http.StatusBadRequest, err)
		return nil, false
	}
	return tools, true
}

// applyImportedMission overwrites the fields an export document carries.
func applyImportedMission(fields *AgentRequestFields, doc agentexport.Document, tools []llm.EnabledMCPTool) {
	fields.CustomInstructions = doc.Agent.CustomInstructions
	fields.DisableTools = doc.Agent.DisableTools
	fields.MaxToolTurns = doc.Agent.MaxToolTurns
	fields.MCPDynamicToolLoading = doc.Agent.MCPDynamicToolLoading
	fields.AutoEnableNewMCPTools = doc.Agent.AutoEnableNewMCPTools
	fields.EnabledMCPTools = tools
}

// importCreateRequest builds the create request for an imported agent: the
// document's transferable fields plus the identity and service chosen on
// import, with every other field at the defaults of a new agent created in
// the UI (everyone may use it, no extra admins, vision and reasoning on,
// provider web search on where licensed).
func (a *API) importCreateRequest(req ImportAgentRequest, doc agentexport.Document, tools []llm.EnabledMCPTool) CreateAgentRequest {
	fields := AgentRequestFields{
		DisplayName:        req.DisplayName,
		ServiceID:          req.ServiceID,
		Model:              req.Model,
		ChannelAccessLevel: int(llm.ChannelAccessLevelAll),
		UserAccessLevel:    int(llm.UserAccessLevelAll),
		EnableVision:       true,
		ReasoningEnabled:   true,
		ReasoningEffort:    "medium",
	}
	if a.licenseChecker.Allows(enterprise.CapProviderWebSearch) {
		fields.EnabledNativeTools = []string{llm.NativeToolWebSearch}
	}
	applyImportedMission(&fields, doc, tools)
	return CreateAgentRequest{AgentRequestFields: fields, Username: req.Username}
}
