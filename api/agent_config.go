// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/agentconfig"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
)

// agentConfigBackend adapts the API's collaborators to agentconfig.Deps. It
// reads the API's fields on every call, so collaborators replaced after New
// (as tests do) are used.
type agentConfigBackend struct {
	a *API
}

func newAgentConfigService(a *API) *agentconfig.Service {
	backend := agentConfigBackend{a: a}
	return agentconfig.New(agentconfig.Deps{
		Store:      backend,
		Settings:   backend,
		License:    backend,
		Access:     backend,
		Accounts:   backend,
		References: backend,
		Notifier:   backend,
		Authorizer: backend,
		Logger:     backend,
	})
}

func (b agentConfigBackend) GetAgent(id string) (*llm.BotConfig, error) {
	return b.a.agentStore.GetAgent(id)
}

func (b agentConfigBackend) UpdateAgent(cfg *llm.BotConfig, meta store.AgentVersionMeta) error {
	return b.a.agentStore.UpdateAgent(cfg, meta)
}

func (b agentConfigBackend) UpdateAgentIfUnchanged(cfg *llm.BotConfig, expectedUpdateAt int64, meta store.AgentVersionMeta) error {
	return b.a.agentStore.UpdateAgentIfUnchanged(cfg, expectedUpdateAt, meta)
}

func (b agentConfigBackend) PluginConfig() (*config.Config, error) {
	return b.a.configStore.GetConfig()
}

func (b agentConfigBackend) CheckAgentTransition(proposed llm.BotConfig, prev *llm.BotConfig) error {
	return b.a.agentLicenseGateError(proposed, prev)
}

func (b agentConfigBackend) ValidateAgentWrite(ctx context.Context, actorID string, proposed, prev *llm.BotConfig) error {
	return b.a.accessChecker.ValidateAgentWrite(ctx, actorID, proposed, prev)
}

func (b agentConfigBackend) DeleteAgentPolicy(ctx context.Context, actorID, agentID string) error {
	return b.a.accessChecker.DeletePolicy(ctx, actorID, accesscontrol.ResourceTypeAgent, agentID)
}

func (b agentConfigBackend) GetUserByUsername(username string) (*model.User, error) {
	return b.a.pluginAPI.User.GetByUsername(username)
}

func (b agentConfigBackend) PatchBot(botUserID string, patch *model.BotPatch) error {
	_, err := b.a.pluginAPI.Bot.Patch(botUserID, patch)
	return err
}

func (b agentConfigBackend) BindAgentConfigReferences(botUserID, username string) error {
	return b.a.bindAgentConfigReferences(botUserID, username)
}

func (b agentConfigBackend) AgentChanged(agentID string) bool {
	ensureErr := b.a.refreshBotsAndNotify(agentID)
	return ensureErr == nil && b.a.bots != nil
}

func (b agentConfigBackend) CanManage(_ context.Context, actorID string, agent *llm.BotConfig) bool {
	return b.a.canManageAgent(agent, actorID)
}

func (b agentConfigBackend) CanManageServiceAccounts(_ context.Context, actorID string, _ *llm.BotConfig) bool {
	return b.a.canManageServiceAccounts(actorID)
}

func (b agentConfigBackend) Error(msg string, keyValuePairs ...any) {
	b.a.pluginAPI.Log.Error(msg, keyValuePairs...)
}

// AgentConfigService returns the service every agent write goes through.
func (a *API) AgentConfigService() *agentconfig.Service {
	return a.agentConfig
}

// abortAgentConfigError writes the response for an agentconfig error.
func abortAgentConfigError(c *gin.Context, err error) {
	switch agentconfig.KindOf(err) {
	case agentconfig.KindNotFound:
		abortAgentRequest(c, http.StatusNotFound, err)
	case agentconfig.KindForbidden:
		abortAgentRequest(c, http.StatusForbidden, err)
	case agentconfig.KindInvalid:
		abortAgentRequest(c, http.StatusBadRequest, err)
	case agentconfig.KindConflict:
		abortAgentRequest(c, http.StatusConflict, err)
	case agentconfig.KindNotLicensed:
		abortNotLicensed(c, errors.Unwrap(err))
	case agentconfig.KindAccess:
		abortAgentRequest(c, statusForAccessErr(err), err)
	case agentconfig.KindPolicy:
		abortPolicyRequest(c, err)
	default:
		abortAgentRequest(c, http.StatusInternalServerError, err)
	}
}

// agentLicenseGateError returns the license error for a create or update
// that newly enables a gated BotConfig field. prev is nil on create.
func (a *API) agentLicenseGateError(proposed llm.BotConfig, prev *llm.BotConfig) error {
	var before llm.BotConfig
	if prev != nil {
		before = *prev
	}
	level := a.licenseChecker.Level()
	if err := config.ValidateAgentTransition(before, proposed, level); err != nil {
		return err
	}
	// Below Enterprise only the active service may be newly selected.
	if !a.licenseChecker.Allows(enterprise.CapMultipleLLMServices) && (prev == nil || prev.ServiceID != proposed.ServiceID) {
		if !slices.Contains(config.ActiveServiceIDs(a.pluginConfigOrEmpty(), level), proposed.ServiceID) {
			return enterprise.NewLicenseError(enterprise.CapMultipleLLMServices, level)
		}
	}
	return nil
}
