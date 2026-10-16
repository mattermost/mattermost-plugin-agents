// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type fakeAgentReferenceConfigStore struct {
	cfg       *config.Config
	updateErr error
	writes    int
}

func (s *fakeAgentReferenceConfigStore) GetConfig() (*config.Config, error) {
	if s.cfg == nil {
		return nil, nil
	}
	cfg := *s.cfg
	return &cfg, nil
}

func (s *fakeAgentReferenceConfigStore) UpdateConfig(transform func(prev *config.Config) (config.Config, error)) (config.Config, error) {
	prev, _ := s.GetConfig()
	next, err := transform(prev)
	if err != nil {
		return next, err
	}
	if s.updateErr != nil {
		return next, s.updateErr
	}
	s.cfg = &next
	s.writes++
	return next, nil
}

type agentReferences struct {
	DefaultBotUserID    string
	DefaultBotName      string
	TranscriptBotUserID string
	TranscriptGenerator string
}

func (r agentReferences) config() *config.Config {
	return &config.Config{
		DefaultBotUserID:    r.DefaultBotUserID,
		DefaultBotName:      r.DefaultBotName,
		TranscriptBotUserID: r.TranscriptBotUserID,
		TranscriptGenerator: r.TranscriptGenerator,
	}
}

func agentReferencesOf(cfg config.Config) agentReferences {
	return agentReferences{
		DefaultBotUserID:    cfg.DefaultBotUserID,
		DefaultBotName:      cfg.DefaultBotName,
		TranscriptBotUserID: cfg.TranscriptBotUserID,
		TranscriptGenerator: cfg.TranscriptGenerator,
	}
}

func TestMigrateAgentReferences(t *testing.T) {
	const aiID = "aibotuserid123456789012345"
	const scribeID = "scribebotuserid12345678901"
	const renamedID = "renamedbotuserid1234567890"
	notFound := model.NewAppError("GetUserByUsername", "app.user.missing_account.const", nil, "", http.StatusNotFound)

	tests := []struct {
		name           string
		stored         *agentReferences
		emptyRegistry  bool
		updateErr      error
		expected       *agentReferences
		expectMigrated bool
		expectWarned   []string
	}{
		{
			name:           "legacy names held by agents become their bot user IDs",
			stored:         &agentReferences{DefaultBotName: "ai", TranscriptGenerator: "scribe"},
			expected:       &agentReferences{DefaultBotUserID: aiID, TranscriptBotUserID: scribeID},
			expectMigrated: true,
		},
		{
			name:           "one agent selected by both settings",
			stored:         &agentReferences{DefaultBotName: "ai", TranscriptGenerator: "ai"},
			expected:       &agentReferences{DefaultBotUserID: aiID, TranscriptBotUserID: aiID},
			expectMigrated: true,
		},
		{
			name:           "a legacy name is matched against the account's current username",
			stored:         &agentReferences{DefaultBotName: "new-name"},
			expected:       &agentReferences{DefaultBotUserID: renamedID},
			expectMigrated: true,
		},
		{
			name:           "a released username is left in place with a warning",
			stored:         &agentReferences{DefaultBotName: "old-name"},
			expected:       &agentReferences{DefaultBotName: "old-name"},
			expectWarned:   []string{"old-name"},
			expectMigrated: false,
		},
		{
			name:           "only the resolvable setting is migrated",
			stored:         &agentReferences{DefaultBotName: "ai", TranscriptGenerator: "gone"},
			expected:       &agentReferences{DefaultBotUserID: aiID, TranscriptGenerator: "gone"},
			expectWarned:   []string{"gone"},
			expectMigrated: true,
		},
		{
			name:     "settings that hold bot user IDs are not rewritten",
			stored:   &agentReferences{DefaultBotUserID: scribeID, DefaultBotName: "ai", TranscriptBotUserID: aiID},
			expected: &agentReferences{DefaultBotUserID: scribeID, DefaultBotName: "ai", TranscriptBotUserID: aiID},
		},
		{
			name:          "agents not yet loaded leave the legacy names in place",
			stored:        &agentReferences{DefaultBotName: "ai"},
			emptyRegistry: true,
			expected:      &agentReferences{DefaultBotName: "ai"},
			expectWarned:  []string{"ai"},
		},
		{
			name:      "a failed write leaves the config and peers untouched",
			stored:    &agentReferences{DefaultBotName: "ai"},
			updateErr: errors.New("database unavailable"),
			expected:  &agentReferences{DefaultBotName: "ai"},
		},
		{
			name: "no stored config",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockAPI := &plugintest.API{}
			defer mockAPI.AssertExpectations(t)
			client := pluginapi.NewClient(mockAPI, nil)

			mockAPI.On("KVSetWithOptions", mock.AnythingOfType("string"), mock.Anything, mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil).Maybe()
			var warned []string
			for arity := 1; arity <= 9; arity++ {
				args := make([]any, arity)
				for i := range args {
					args[i] = mock.Anything
				}
				mockAPI.On("LogInfo", args...).Maybe()
				mockAPI.On("LogError", args...).Maybe()
				mockAPI.On("LogDebug", args...).Maybe()
			}
			mockAPI.On("LogWarn", mock.Anything, "username", mock.AnythingOfType("string")).Run(func(args mock.Arguments) {
				warned = append(warned, args.String(2))
			}).Maybe()
			mockAPI.On("GetUser", aiID).Return(&model.User{Id: aiID, Username: "ai", IsBot: true}, nil).Maybe()
			mockAPI.On("GetUser", scribeID).Return(&model.User{Id: scribeID, Username: "scribe", IsBot: true}, nil).Maybe()
			mockAPI.On("GetUser", renamedID).Return(&model.User{Id: renamedID, Username: "new-name", IsBot: true}, nil).Maybe()
			mockAPI.On("GetUserByUsername", "new-name").Return(&model.User{Id: renamedID, Username: "new-name", IsBot: true}, nil).Maybe()
			mockAPI.On("GetUserByUsername", mock.AnythingOfType("string")).Return(nil, notFound).Maybe()
			published := 0
			mockAPI.On("PublishPluginClusterEvent",
				mock.MatchedBy(func(ev model.PluginClusterEvent) bool { return ev.Id == clusterEventConfigUpdate }),
				mock.Anything,
			).Return(nil).Run(func(mock.Arguments) { published++ }).Maybe()

			registry := bots.New(mockAPI, client, nil, nil, nil,
				accesscontrol.New(accesscontrol.PassthroughClient{}, nil, accesscontrol.NoMCPServerIDs, nil), nil, nil)
			if !tc.emptyRegistry {
				registry.SetBotsForTesting([]*bots.Bot{
					bots.NewBot(llm.BotConfig{Name: "ai"}, llm.ServiceConfig{}, &model.Bot{UserId: aiID, Username: "ai"}, nil),
					bots.NewBot(llm.BotConfig{Name: "scribe"}, llm.ServiceConfig{}, &model.Bot{UserId: scribeID, Username: "scribe"}, nil),
					bots.NewBot(llm.BotConfig{Name: "old-name"}, llm.ServiceConfig{}, &model.Bot{UserId: renamedID, Username: "old-name"}, nil),
				})
			}

			store := &fakeAgentReferenceConfigStore{updateErr: tc.updateErr}
			p := &Plugin{pluginAPI: client}
			p.SetAPI(mockAPI)
			if tc.stored != nil {
				store.cfg = tc.stored.config()
				require.NoError(t, p.configuration.StorePersistedConfigWithoutNotify(tc.stored.config()))
			}

			p.migrateAgentReferences(client, store, registry)

			if tc.expected == nil {
				assert.Nil(t, store.cfg)
			} else {
				require.NotNil(t, store.cfg)
				assert.Equal(t, *tc.expected, agentReferencesOf(*store.cfg), "stored config")
				assert.Equal(t, *tc.expected, agentReferencesOf(*p.configuration.Config()), "this node's config")
			}
			assert.ElementsMatch(t, tc.expectWarned, warned)
			if tc.expectMigrated {
				assert.Equal(t, 1, store.writes)
				assert.Equal(t, 1, published, "peers must reload the migrated config")
			} else {
				assert.Zero(t, store.writes, "nothing to migrate must not write the config")
				assert.Zero(t, published)
			}

			p.migrateAgentReferences(client, store, registry)
			assert.LessOrEqual(t, store.writes, 1, "a second activation must not write again")
			assert.LessOrEqual(t, published, 1, "a second activation must not publish again")
		})
	}
}
