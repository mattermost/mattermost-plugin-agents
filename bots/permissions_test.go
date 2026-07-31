// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bots

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

// Well-formed 26-char IDs: the agent access gate denies a user ID no policy can
// be evaluated against, so these tests need real-shaped IDs to reach the
// restriction logic they cover.
const (
	testUserID      = "user12345678901234567890ab"
	testOtherUserID = "othe12345678901234567890ab"
)

type TestEnvironment struct {
	bots    *MMBots
	client  *pluginapi.Client
	mockAPI *plugintest.API
}

func SetupTestEnvironment(t *testing.T) *TestEnvironment {
	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)

	licenseChecker := enterprise.NewLicenseChecker(client)
	mmBots := New(mockAPI, client, licenseChecker, nil, nil, newPassthroughAccessChecker(), &http.Client{}, nil)

	e := &TestEnvironment{
		bots:    mmBots,
		client:  client,
		mockAPI: mockAPI,
	}

	return e
}

func (e *TestEnvironment) Cleanup(t *testing.T) {
	e.mockAPI.AssertExpectations(t)
}

func TestUsageRestrictions(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	testCases := []struct {
		name           string
		bot            *Bot
		channel        *model.Channel
		requestingUser string
		expectedError  error
	}{
		{
			name: "All allowed",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelAll,
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "Channel blocked",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelBlock,
				ChannelIDs:         []string{"channel1"},
				UserAccessLevel:    llm.UserAccessLevelAll,
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "User blocked",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelBlock,
				UserIDs:            []string{testUserID},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "Channel allowed",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAllow,
				ChannelIDs:         []string{"channel1"},
				UserAccessLevel:    llm.UserAccessLevelAll,
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "User allowed",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelAllow,
				UserIDs:            []string{testUserID},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "Channel not allowed",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAllow,
				ChannelIDs:         []string{"channel2"},
				UserAccessLevel:    llm.UserAccessLevelAll,
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "User not allowed",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelAllow,
				UserIDs:            []string{testOtherUserID},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "Channel none",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelNone,
				UserAccessLevel:    llm.UserAccessLevelAll,
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "User none",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelNone,
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "Channel block but not in list",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelBlock,
				ChannelIDs:         []string{"channel2"},
				UserAccessLevel:    llm.UserAccessLevelAll,
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "User block but not in list",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelBlock,
				UserIDs:            []string{testOtherUserID},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "Channel allow and user allow",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAllow,
				ChannelIDs:         []string{"channel1"},
				UserAccessLevel:    llm.UserAccessLevelAllow,
				UserIDs:            []string{testUserID},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "Channel allow but user not allowed",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAllow,
				ChannelIDs:         []string{"channel1"},
				UserAccessLevel:    llm.UserAccessLevelAllow,
				UserIDs:            []string{testOtherUserID},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "User allowed via team membership",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelAllow,
				TeamIDs:            []string{"team1"},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "User blocked via team membership",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelBlock,
				TeamIDs:            []string{"team1"},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "User not in allowed team",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelAllow,
				TeamIDs:            []string{"team2"},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "User allowed via direct ID even if not in team",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelAllow,
				UserIDs:            []string{testUserID},
				TeamIDs:            []string{"team2"},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "User blocked via direct ID even if in allowed team",
			bot: &Bot{cfg: llm.BotConfig{
				ChannelAccessLevel: llm.ChannelAccessLevelAll,
				UserAccessLevel:    llm.UserAccessLevelBlock,
				UserIDs:            []string{testUserID},
				TeamIDs:            []string{"team1"},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		// DB-backed agent test cases: build llm.BotConfig directly to confirm
		// CheckUsageRestrictions also works for DB-backed agent configs.
		{
			name: "DB-backed agent: user allowed by allowlist",
			bot: &Bot{cfg: llm.BotConfig{
				ID:              "agent-1",
				Name:            "db-agent",
				DisplayName:     "DB Agent",
				ServiceID:       "svc-1",
				UserAccessLevel: llm.UserAccessLevelAllow,
				UserIDs:         []string{testUserID},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
		{
			name: "DB-backed agent: user blocked by blocklist",
			bot: &Bot{cfg: llm.BotConfig{
				ID:              "agent-2",
				Name:            "db-agent-2",
				DisplayName:     "DB Agent 2",
				ServiceID:       "svc-1",
				UserAccessLevel: llm.UserAccessLevelBlock,
				UserIDs:         []string{testUserID},
			}, mmBot: nil},
			channel:        &model.Channel{Id: "channel1"},
			requestingUser: testUserID,
			expectedError:  ErrUsageRestriction,
		},
		{
			name: "DB-backed agent: channel allowed",
			bot: &Bot{cfg: llm.BotConfig{
				ID:                 "agent-3",
				Name:               "db-agent-3",
				DisplayName:        "DB Agent 3",
				ServiceID:          "svc-1",
				ChannelAccessLevel: llm.ChannelAccessLevelAllow,
				ChannelIDs:         []string{"allowed_channel"},
				UserAccessLevel:    llm.UserAccessLevelAll,
			}, mmBot: nil},
			channel:        &model.Channel{Id: "allowed_channel"},
			requestingUser: testUserID,
			expectedError:  nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock responses for team membership checks
			if len(tc.bot.GetConfig().TeamIDs) > 0 {
				member := &model.TeamMember{
					TeamId: "team1",
					UserId: testUserID,
				}
				e.mockAPI.On("GetTeamMember", "team1", testUserID).Return(member, nil).Maybe()
				e.mockAPI.On("GetTeamMember", "team2", testUserID).Return(nil, &model.AppError{Message: "not found", StatusCode: http.StatusNotFound}).Maybe()
			}

			err := e.bots.CheckUsageRestrictions(context.Background(), tc.requestingUser, tc.bot, tc.channel)
			if tc.expectedError != nil {
				require.ErrorIs(t, err, tc.expectedError)

				// The composite gate relabels ABAC denials as ErrUsageRestriction,
				// so that sentinel alone cannot tell a legacy restriction from a
				// denial that never reached the legacy check. Every row here
				// expects the legacy channel/user switch to do the rejecting.
				require.NotErrorIs(t, err, accesscontrol.ErrAccessDenied)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCheckUsageRestrictionsForUserConfigParity(t *testing.T) {
	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)

	// Only team-membership branches need API mocks.
	member := &model.TeamMember{TeamId: "team1", UserId: testUserID}
	e.mockAPI.On("GetTeamMember", "team1", testUserID).Return(member, nil).Maybe()
	e.mockAPI.On("GetTeamMember", "team2", testUserID).Return(
		nil, &model.AppError{Message: "not found", StatusCode: http.StatusNotFound},
	).Maybe()

	cases := []struct {
		name    string
		cfg     llm.BotConfig
		user    string
		wantErr bool
	}{
		{"all allowed", llm.BotConfig{UserAccessLevel: llm.UserAccessLevelAll}, testUserID, false},
		{"allow in userIDs", llm.BotConfig{
			UserAccessLevel: llm.UserAccessLevelAllow,
			UserIDs:         []string{testUserID},
		}, testUserID, false},
		{"allow via team", llm.BotConfig{
			UserAccessLevel: llm.UserAccessLevelAllow,
			TeamIDs:         []string{"team1"},
		}, testUserID, false},
		{"allow not listed", llm.BotConfig{
			UserAccessLevel: llm.UserAccessLevelAllow,
			UserIDs:         []string{"other"},
		}, testUserID, true},
		{"block in userIDs", llm.BotConfig{
			UserAccessLevel: llm.UserAccessLevelBlock,
			UserIDs:         []string{testUserID},
		}, testUserID, true},
		{"block via team", llm.BotConfig{
			UserAccessLevel: llm.UserAccessLevelBlock,
			TeamIDs:         []string{"team1"},
		}, testUserID, true},
		{"block not listed", llm.BotConfig{
			UserAccessLevel: llm.UserAccessLevelBlock,
			UserIDs:         []string{"other"},
		}, testUserID, false},
		{"none", llm.BotConfig{UserAccessLevel: llm.UserAccessLevelNone}, testUserID, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errDirect := UsageRestrictionsForUserConfig(e.client, tc.cfg, tc.user)
			errConfig := e.bots.CheckUsageRestrictionsForUserConfig(context.Background(), tc.cfg, tc.user)
			errBot := e.bots.CheckUsageRestrictionsForUser(context.Background(), &Bot{cfg: tc.cfg}, tc.user)
			if tc.wantErr {
				require.ErrorIs(t, errDirect, ErrUsageRestriction)
				require.ErrorIs(t, errConfig, ErrUsageRestriction)
				require.ErrorIs(t, errBot, ErrUsageRestriction)
			} else {
				require.NoError(t, errDirect)
				require.NoError(t, errConfig)
				require.NoError(t, errBot)
			}
		})
	}
}

// TestCheckUsageRestrictionsDowngradedServer pins the pre-11.10 production
// wiring (accesscontrol.NewLegacyOnly, as selected by server/main.go's
// version gate — the downgrade scenario): a persisted attribute-based agent
// must deny every user (fail closed — the plugin cannot resolve whether a
// policy gates it), agents in the legacy access modes keep their legacy
// behavior, and services/MCP servers are unrestricted.
func TestCheckUsageRestrictionsDowngradedServer(t *testing.T) {
	userID := model.NewId()
	agentID := model.NewId()
	serviceID := model.NewId()

	setup := func(t *testing.T) *TestEnvironment {
		t.Helper()
		mockAPI := &plugintest.API{}
		client := pluginapi.NewClient(mockAPI, nil)
		checker := accesscontrol.NewLegacyOnly(accesscontrol.NoMCPServerIDs, nil)
		mmBots := New(mockAPI, client, enterprise.NewLicenseChecker(client), nil, nil, checker, &http.Client{}, nil)
		return &TestEnvironment{bots: mmBots, client: client, mockAPI: mockAPI}
	}

	tests := []struct {
		name     string
		check    func(e *TestEnvironment) error
		wantErrs []error
	}{
		{
			name: "persisted attribute-based agent is denied for every user",
			check: func(e *TestEnvironment) error {
				cfg := llm.BotConfig{
					ID: agentID, ServiceID: serviceID,
					UserAccessLevel: llm.UserAccessLevelAttributeBased,
					UserIDs:         []string{userID}, // ignored in attribute-based mode; must not open access
				}
				return e.bots.CheckUsageRestrictionsForUserConfig(context.Background(), cfg, userID)
			},
			wantErrs: []error{ErrUsageRestriction, accesscontrol.ErrAccessDenied},
		},
		{
			// All → allowed. This also pins the service leg of the composite
			// gate as unrestricted: CheckUsageRestrictionsForUserConfig calls
			// CanUseService for cfg.ServiceID and would deny otherwise.
			name: "legacy all-mode agent stays allowed",
			check: func(e *TestEnvironment) error {
				cfg := llm.BotConfig{ID: agentID, ServiceID: serviceID, UserAccessLevel: llm.UserAccessLevelAll}
				return e.bots.CheckUsageRestrictionsForUserConfig(context.Background(), cfg, userID)
			},
		},
		{
			name: "legacy block-mode agent keeps denying listed users",
			check: func(e *TestEnvironment) error {
				cfg := llm.BotConfig{
					ID: agentID, ServiceID: serviceID,
					UserAccessLevel: llm.UserAccessLevelBlock,
					UserIDs:         []string{userID},
				}
				return e.bots.CheckUsageRestrictionsForUserConfig(context.Background(), cfg, userID)
			},
			wantErrs: []error{ErrUsageRestriction},
		},
		{
			name: "service is unrestricted",
			check: func(e *TestEnvironment) error {
				return e.bots.accessChecker.CanUseService(context.Background(), userID, serviceID)
			},
		},
		{
			name: "mcp server is unrestricted",
			check: func(e *TestEnvironment) error {
				return e.bots.accessChecker.CanUseMCPServer(context.Background(), userID, model.NewId())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setup(t)
			defer e.Cleanup(t)

			err := tt.check(e)
			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
				return
			}
			for _, wantErr := range tt.wantErrs {
				require.ErrorIs(t, err, wantErr)
			}
		})
	}
}

// abacStubClient answers decision calls per resource type; unlisted types
// evaluate as no_policy (legacy behavior). A type listed in perTypeErr fails
// evaluation instead, which the checker denies.
type abacStubClient struct {
	perType    map[string]*model.AccessDecision
	perTypeErr map[string]error
}

func (s abacStubClient) EvaluateAccessRequest(_ context.Context, _, resourceType, _, _ string) (*model.AccessDecision, error) {
	if err, ok := s.perTypeErr[resourceType]; ok {
		return nil, err
	}
	if decision, ok := s.perType[resourceType]; ok {
		return decision, nil
	}
	noPolicy := model.NewNoPolicyAccessDecision()
	return &noPolicy, nil
}

// Decision shorthands for the table below.
func abacAllow() *model.AccessDecision { return &model.AccessDecision{Decision: true} }
func abacDeny() *model.AccessDecision  { return &model.AccessDecision{Decision: false} }

func setupABACTestEnvironment(t *testing.T, stub abacStubClient) *TestEnvironment {
	t.Helper()
	mockAPI := &plugintest.API{}
	client := pluginapi.NewClient(mockAPI, nil)
	checker := accesscontrol.New(stub, nil, accesscontrol.NoMCPServerIDs, nil)
	mmBots := New(mockAPI, client, enterprise.NewLicenseChecker(client), nil, nil, checker, &http.Client{}, nil)
	return &TestEnvironment{bots: mmBots, client: client, mockAPI: mockAPI}
}

// TestCheckUsageRestrictionsForUserConfigComposite exercises the composite
// agent+service ABAC gate layered over the legacy UserAccessLevel check.
func TestCheckUsageRestrictionsForUserConfigComposite(t *testing.T) {
	userID := model.NewId()
	agentID := model.NewId()
	serviceID := model.NewId()

	tests := []struct {
		name       string
		perType    map[string]*model.AccessDecision
		perTypeErr map[string]error
		cfg        llm.BotConfig
		wantDenied bool
	}{
		{
			name:    "agent policy deny masks legacy allow",
			perType: map[string]*model.AccessDecision{accesscontrol.ResourceTypeAgent: abacDeny()},
			cfg: llm.BotConfig{
				ID: agentID, ServiceID: serviceID,
				UserAccessLevel: llm.UserAccessLevelAll,
			},
			wantDenied: true,
		},
		{
			name: "service deny after agent allow",
			perType: map[string]*model.AccessDecision{
				accesscontrol.ResourceTypeAgent:   abacAllow(),
				accesscontrol.ResourceTypeService: abacDeny(),
			},
			cfg: llm.BotConfig{
				ID: agentID, ServiceID: serviceID,
				UserAccessLevel: llm.UserAccessLevelAll,
			},
			wantDenied: true,
		},
		{
			name: "agent and service allow",
			perType: map[string]*model.AccessDecision{
				accesscontrol.ResourceTypeAgent:   abacAllow(),
				accesscontrol.ResourceTypeService: abacAllow(),
			},
			cfg: llm.BotConfig{
				ID: agentID, ServiceID: serviceID,
				UserAccessLevel: llm.UserAccessLevelAll,
			},
		},
		{
			name:    "attribute-based agent ignores legacy user lists",
			perType: map[string]*model.AccessDecision{accesscontrol.ResourceTypeAgent: abacAllow()},
			cfg: llm.BotConfig{
				ID: agentID, ServiceID: serviceID,
				UserAccessLevel: llm.UserAccessLevelAttributeBased,
				UserIDs:         []string{"someone-else"}, // would deny under Allow mode
			},
		},
		{
			name:    "attribute-based agent denied by policy",
			perType: map[string]*model.AccessDecision{accesscontrol.ResourceTypeAgent: abacDeny()},
			cfg: llm.BotConfig{
				ID: agentID, ServiceID: serviceID,
				UserAccessLevel: llm.UserAccessLevelAttributeBased,
			},
			wantDenied: true,
		},
		{
			// A failed evaluation leaves policy existence unknowable, so even
			// a legacy-mode agent whose user lists would allow fails closed at
			// the enforcement point.
			name:       "legacy-mode agent denied on evaluation error",
			perTypeErr: map[string]error{accesscontrol.ResourceTypeAgent: errors.New("pdp unavailable")},
			cfg: llm.BotConfig{
				ID: agentID, ServiceID: serviceID,
				UserAccessLevel: llm.UserAccessLevelAll,
			},
			wantDenied: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setupABACTestEnvironment(t, abacStubClient{perType: tc.perType, perTypeErr: tc.perTypeErr})
			defer e.Cleanup(t)

			err := e.bots.CheckUsageRestrictionsForUserConfig(context.Background(), tc.cfg, userID)
			if tc.wantDenied {
				// ABAC denials satisfy both sentinels so existing callers keep
				// branching on ErrUsageRestriction while new code can detect
				// policy denials specifically.
				require.ErrorIs(t, err, ErrUsageRestriction)
				require.ErrorIs(t, err, accesscontrol.ErrAccessDenied)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
