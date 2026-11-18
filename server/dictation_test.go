// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"

	"github.com/mattermost/mattermost-plugin-agents/v2/api"
	"github.com/mattermost/mattermost-plugin-agents/v2/config"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

type recordingPublisher struct{ events []string }

func (r *recordingPublisher) PublishWebSocketEvent(event string, _ map[string]any, _ *model.WebsocketBroadcast) {
	r.events = append(r.events, event)
}

type mutableLicense struct{ level enterprise.Level }

func (m *mutableLicense) Check(capability enterprise.Capability) error {
	if m.level >= enterprise.RequiredLevel(capability) {
		return nil
	}
	return enterprise.NewLicenseError(capability, m.level)
}

type allowAllServices struct{}

func (allowAllServices) CanUseService(context.Context, string, string) error { return nil }

type nopDictationLogger struct{}

func (nopDictationLogger) Info(string, ...any) {}
func (nopDictationLogger) Warn(string, ...any) {}

func TestDictationAvailabilityChangePublishesWebsocketEvent(t *testing.T) {
	openAI := llm.ServiceConfig{ID: "openai1", Name: "OpenAI", Type: llm.ServiceTypeOpenAI, APIKey: "k"}
	enabledCfg := &config.Config{
		Services:  []llm.ServiceConfig{openAI},
		Dictation: config.DictationConfig{Enabled: true, ServiceID: "openai1", Model: "gpt-live-transcribe"},
	}

	tests := []struct {
		name       string
		change     func(p *Plugin, license *mutableLicense)
		wantEvents int
	}{
		{
			name: "unrelated config save",
			change: func(p *Plugin, _ *mutableLicense) {
				cfg := enabledCfg.Clone()
				cfg.AgentVersionsToKeep = 3
				p.configuration.Update(cfg)
			},
			wantEvents: 0,
		},
		{
			name: "dictation disabled by a config save",
			change: func(p *Plugin, _ *mutableLicense) {
				cfg := enabledCfg.Clone()
				cfg.Dictation.Enabled = false
				p.configuration.Update(cfg)
			},
			wantEvents: 1,
		},
		{
			name: "model changed by a config save",
			change: func(p *Plugin, _ *mutableLicense) {
				cfg := enabledCfg.Clone()
				cfg.Dictation.Model = "gpt-4o-transcribe"
				p.configuration.Update(cfg)
			},
			wantEvents: 1,
		},
		{
			name: "license downgraded",
			change: func(p *Plugin, license *mutableLicense) {
				license.level = enterprise.LevelProfessional
				p.OnLicenseChanged(nil, nil)
			},
			wantEvents: 1,
		},
		{
			name:       "license change that keeps the capability",
			change:     func(p *Plugin, _ *mutableLicense) { p.OnLicenseChanged(nil, nil) },
			wantEvents: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{}
			p.configuration.Update(enabledCfg)
			license := &mutableLicense{level: enterprise.LevelEnterprise}
			ws := &recordingPublisher{}
			p.newDictationService(license, allowAllServices{}, nopDictationLogger{}, ws)

			tt.change(p, license)

			assert.Len(t, ws.events, tt.wantEvents)
			for _, ev := range ws.events {
				assert.Equal(t, api.WebsocketEventDictationAvailabilityChanged, ev)
			}
		})
	}
}
