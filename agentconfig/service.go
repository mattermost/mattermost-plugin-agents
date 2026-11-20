// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package agentconfig changes the configuration of user-created agents. Every
// entry point that writes an agent (PUT /agents/:id, restoring a version, an
// agent configuring itself in a setup conversation) goes through
// Service.Update, so authorization, validation, versioning, and the follow-up
// refresh cannot drift between them.
package agentconfig

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

var validUsernameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

// ValidUsername reports whether username may be used for an agent's bot account.
func ValidUsername(username string) bool {
	return validUsernameRe.MatchString(username)
}

// AgentStore reads and versions user-created agents.
type AgentStore interface {
	// GetAgent returns nil, nil when the agent does not exist.
	GetAgent(id string) (*llm.BotConfig, error)
	UpdateAgent(cfg *llm.BotConfig, meta store.AgentVersionMeta) error
}

// Settings reads the stored plugin configuration.
type Settings interface {
	// PluginConfig returns the stored configuration; nil means none is stored.
	PluginConfig() (*config.Config, error)
}

// LicenseGate rejects transitions that newly need a capability the server
// is not licensed for. prev is nil on create.
type LicenseGate interface {
	CheckAgentTransition(proposed llm.BotConfig, prev *llm.BotConfig) error
}

// AccessControl validates agent writes against attribute-based access policies.
type AccessControl interface {
	ValidateAgentWrite(ctx context.Context, actorID string, proposed, prev *llm.BotConfig) error
	DeleteAgentPolicy(ctx context.Context, actorID, agentID string) error
}

// BotAccounts reads and patches Mattermost bot accounts.
type BotAccounts interface {
	// GetUserByUsername returns an error wrapping pluginapi.ErrNotFound when
	// no account holds username.
	GetUserByUsername(username string) (*model.User, error)
	PatchBot(botUserID string, patch *model.BotPatch) error
}

// ReferenceBinder points settings that still select an agent by its legacy
// username at its bot user ID, so a rename cannot release them.
type ReferenceBinder interface {
	BindAgentConfigReferences(botUserID, username string) error
}

// ChangeNotifier applies a stored change on this node and tells peers and
// clients. It reports whether the bot registry was rebuilt, which also
// syncs the bot account's display name.
type ChangeNotifier interface {
	AgentChanged(agentID string) (registrySynced bool)
}

// Logger records failures of best-effort steps.
type Logger interface {
	Error(msg string, keyValuePairs ...any)
}

// Deps are the Service's collaborators. Every field is required.
type Deps struct {
	Store      AgentStore
	Settings   Settings
	License    LicenseGate
	Access     AccessControl
	Accounts   BotAccounts
	References ReferenceBinder
	Notifier   ChangeNotifier
	Authorizer Authorizer
	Logger     Logger
}

// Service changes user-created agents.
type Service struct {
	deps Deps

	authorizerMu sync.RWMutex
	authorizer   Authorizer
}

// New returns a Service. It panics when a dependency is missing, because a
// nil authorizer or access checker would silently disable authorization.
func New(deps Deps) *Service {
	if deps.Store == nil || deps.Settings == nil || deps.License == nil || deps.Access == nil ||
		deps.Accounts == nil || deps.References == nil || deps.Notifier == nil ||
		deps.Authorizer == nil || deps.Logger == nil {
		panic("agentconfig: New requires every dependency")
	}
	return &Service{deps: deps, authorizer: deps.Authorizer}
}

// Authorizer returns the authorizer the Service consults.
func (s *Service) Authorizer() Authorizer {
	s.authorizerMu.RLock()
	defer s.authorizerMu.RUnlock()
	return s.authorizer
}

// SetAuthorizer replaces the authorizer, typically with one that handles some
// agents itself and delegates the rest to the previous Authorizer().
func (s *Service) SetAuthorizer(authorizer Authorizer) {
	if authorizer == nil {
		panic("agentconfig: SetAuthorizer requires an authorizer")
	}
	s.authorizerMu.Lock()
	defer s.authorizerMu.Unlock()
	s.authorizer = authorizer
}

// Get loads agentID for actorID, failing with KindNotFound or KindForbidden.
func (s *Service) Get(ctx context.Context, actorID, agentID string) (*llm.BotConfig, error) {
	cfg, err := s.deps.Store.GetAgent(agentID)
	if err != nil {
		return nil, newError(KindInternal, fmt.Errorf("failed to get agent: %w", err))
	}
	if cfg == nil {
		return nil, newError(KindNotFound, ErrNotFound)
	}
	if !s.Authorizer().CanManage(ctx, actorID, cfg) {
		return nil, newError(KindForbidden, ErrForbidden)
	}
	return cfg, nil
}

// UpdateRequest is one write to an agent.
type UpdateRequest struct {
	ActorID string
	AgentID string
	// Stored, when set, is the agent the caller already loaded as AgentID;
	// it is re-authorized, not re-read.
	Stored *llm.BotConfig
	// Apply changes the proposed configuration, a copy of the stored one. An
	// error refuses the update as KindInvalid.
	Apply func(proposed *llm.BotConfig) error
	// Validate checks the proposed configuration once the actor is known to
	// be allowed to make the change, so its lookups reveal nothing to anyone
	// else. An error refuses the update as KindInvalid.
	Validate func(proposed *llm.BotConfig) error
	// Username, when set and different, renames the agent's bot account.
	Username *string
	Meta     store.AgentVersionMeta
}

// UpdateResult is a successful write.
type UpdateResult struct {
	Agent    *llm.BotConfig
	Previous llm.BotConfig
	// ChangedFields are the BotConfig JSON keys the write changed.
	ChangedFields []string
}

// Update validates and persists req as a new version of the agent, then
// refreshes it on every node. The request's audit record, when ctx carries
// one, gets the changed field names (never their values).
func (s *Service) Update(ctx context.Context, req UpdateRequest) (result *UpdateResult, err error) {
	stored := req.Stored
	if stored == nil {
		if stored, err = s.deps.Store.GetAgent(req.AgentID); err != nil {
			return nil, newError(KindInternal, fmt.Errorf("failed to get agent: %w", err))
		}
		if stored == nil {
			return nil, newError(KindNotFound, ErrNotFound)
		}
	}
	authorizer := s.Authorizer()
	if !authorizer.CanManage(ctx, req.ActorID, stored) {
		return nil, newError(KindForbidden, ErrForbidden)
	}

	// Shallow copy is safe: Apply replaces slice headers rather than mutating elements.
	proposed := *stored
	if req.Apply != nil {
		if applyErr := req.Apply(&proposed); applyErr != nil {
			return nil, newError(KindInvalid, applyErr)
		}
	}
	displayNameChanged := stored.DisplayName != proposed.DisplayName
	usernameChanged := req.Username != nil && *req.Username != stored.Name
	if usernameChanged {
		proposed.Name = *req.Username
	}

	if ServiceAccountChangeNeedsAdmin(*stored, proposed) && !authorizer.CanManageServiceAccounts(ctx, req.ActorID, stored) {
		return nil, newError(KindForbidden, ErrServiceAccountAuthRequiresAdmin)
	}
	if req.Meta.Source == store.AgentVersionSourceSelfSetup {
		if usernameChanged {
			return nil, newError(KindForbidden, ErrSelfSetupFieldNotAllowed)
		}
		if selfErr := checkSelfSetupChange(stored, &proposed); selfErr != nil {
			return nil, newError(KindForbidden, selfErr)
		}
	}
	if req.Validate != nil {
		if validateErr := req.Validate(&proposed); validateErr != nil {
			return nil, newError(KindInvalid, validateErr)
		}
	}

	rec := audit.RecordFromContext(ctx)
	if usernameChanged {
		audit.AddParam(rec, "new_agent_name", audit.TruncateID(proposed.Name))
		if !ValidUsername(proposed.Name) {
			return nil, newError(KindInvalid, ErrInvalidUsername)
		}
		if stored.BotUserID == "" {
			return nil, newError(KindInvalid, errors.New("this agent has no bot account to rename"))
		}
	}
	if serviceErr := s.validateServiceID(proposed.ServiceID); serviceErr != nil {
		return nil, serviceErr
	}
	if licenseErr := s.deps.License.CheckAgentTransition(proposed, stored); licenseErr != nil {
		return nil, newError(KindNotLicensed, licenseErr)
	}

	prev := *stored
	cfg := &proposed
	changed := audit.ChangedJSONKeys(&prev, cfg)
	// Field names only: the agent config carries prompt content
	// (customInstructions), so the record never says what values changed to.
	audit.AddParam(rec, "changed_fields", changed)

	if validateErr := cfg.Validate(); validateErr != nil {
		return nil, newError(KindInvalid, fmt.Errorf("invalid agent configuration: %w", validateErr))
	}
	if accessErr := s.deps.Access.ValidateAgentWrite(ctx, req.ActorID, cfg, &prev); accessErr != nil {
		return nil, newError(KindAccess, accessErr)
	}

	// The bot account is renamed first so a taken username fails the request
	// before anything is persisted. It keeps its user ID, so DMs, channel
	// memberships, and history stay with the agent.
	revertRename := func() {}
	renameReverted := false
	if usernameChanged {
		if bindErr := s.deps.References.BindAgentConfigReferences(cfg.BotUserID, prev.Name); bindErr != nil {
			return nil, newError(KindInternal, fmt.Errorf("failed to point settings at the agent before renaming it: %w", bindErr))
		}
		renamed, renameErr := s.renameBot(cfg.BotUserID, cfg.Name)
		if renameErr != nil {
			return nil, renameErr
		}
		// From here a failure can leave the bot account, the stored agent, and
		// the registries disagreeing on the name (a rollback or the revert can
		// fail too), with the previous username already released. Bring all of
		// them to the stored agent before the error is returned, unless the
		// rename was fully reverted and they already agree.
		defer func() {
			if err != nil && !renameReverted {
				s.deps.Notifier.AgentChanged(cfg.ID)
			}
		}()
		if renamed {
			revertRename = func() {
				if patchErr := s.deps.Accounts.PatchBot(cfg.BotUserID, &model.BotPatch{Username: &prev.Name}); patchErr != nil {
					s.deps.Logger.Error("Failed to revert bot username after agent update failure", "agent_id", cfg.ID, "error", patchErr.Error())
					return
				}
				renameReverted = true
			}
		}
	}

	if updateErr := s.deps.Store.UpdateAgent(cfg, req.Meta); updateErr != nil {
		revertRename()
		return nil, newError(KindInternal, fmt.Errorf("failed to update agent: %w", updateErr))
	}

	// Switching away from attribute-based access: delete the agent policy.
	// If policy deletion fails with an unexpected error, roll the agent back
	// to prev so it is not left where legacy access claims everyone is
	// allowed while a dangling policy continues to deny users.
	if prev.UserAccessLevel == llm.UserAccessLevelAttributeBased && cfg.UserAccessLevel != llm.UserAccessLevelAttributeBased {
		audit.AddParam(rec, audit.KeyABACResourceType, accesscontrol.ResourceTypeAgent)
		audit.AddParam(rec, audit.KeyABACResourceID, audit.TruncateID(cfg.ID))

		if deleteErr := s.deps.Access.DeleteAgentPolicy(ctx, req.ActorID, cfg.ID); deleteErr != nil && !errors.Is(deleteErr, accesscontrol.ErrPolicyNotFound) {
			if rollbackErr := s.deps.Store.UpdateAgent(&prev, store.SystemAgentVersionMeta()); rollbackErr != nil {
				s.deps.Logger.Error("Failed to rollback agent after access policy deletion failure", "agent_id", cfg.ID, "rollback_error", rollbackErr.Error(), "delete_error", deleteErr.Error())
			} else {
				revertRename()
			}
			return nil, newError(KindPolicy, fmt.Errorf("failed to delete access policy: %w", deleteErr))
		}
	}

	// The registry rebuild patches the display name; when it did not run, sync it explicitly.
	if synced := s.deps.Notifier.AgentChanged(cfg.ID); displayNameChanged && !synced {
		if patchErr := s.deps.Accounts.PatchBot(cfg.BotUserID, &model.BotPatch{DisplayName: &cfg.DisplayName}); patchErr != nil {
			s.deps.Logger.Error("Failed to patch bot display name", "agent_id", cfg.ID, "error", patchErr.Error())
		}
	}

	return &UpdateResult{Agent: cfg, Previous: prev, ChangedFields: changed}, nil
}

func (s *Service) validateServiceID(serviceID string) error {
	cfg, err := s.deps.Settings.PluginConfig()
	if err != nil {
		return newError(KindInternal, fmt.Errorf("failed to read config: %w", err))
	}
	if cfg == nil {
		return newError(KindInternal, errors.New("no plugin configuration available"))
	}
	if _, ok := cfg.GetServiceByID(serviceID); !ok {
		return newError(KindInvalid, fmt.Errorf("service %q not found in configuration", serviceID))
	}
	return nil
}

// renameBot renames the agent's bot account in place and reports whether it
// changed it; the account may already carry the name when a concurrent save
// renamed it first.
func (s *Service) renameBot(botUserID, username string) (bool, error) {
	existing, err := s.deps.Accounts.GetUserByUsername(username)
	switch {
	case err == nil && existing.Id == botUserID:
		return false, nil
	case err == nil:
		return false, newError(KindConflict, fmt.Errorf("username %q is already taken", username))
	case !errors.Is(err, pluginapi.ErrNotFound):
		return false, newError(KindInternal, fmt.Errorf("failed to check username availability: %w", err))
	}

	if err := s.deps.Accounts.PatchBot(botUserID, &model.BotPatch{Username: &username}); err != nil {
		if appErr, ok := errors.AsType[*model.AppError](err); ok && appErr.StatusCode >= 400 && appErr.StatusCode < 500 {
			return false, newError(KindInvalid, fmt.Errorf("username %q cannot be used: %s", username, appErr.Message))
		}
		return false, newError(KindInternal, fmt.Errorf("failed to rename bot account: %w", err))
	}
	return true, nil
}
