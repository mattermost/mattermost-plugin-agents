// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"strings"
	"testing"
)

func TestAgentSubscription_Validate(t *testing.T) {
	validChannel := "abcdefghijklmnopqrstuvwxyz"
	if len(validChannel) != 26 {
		t.Fatal("test fixture: validChannel must be 26 chars")
	}

	baseValid := AgentSubscription{
		Event:           SubscriptionEventMessagePosted,
		ScopeChannelID:  validChannel,
		TargetChannelID: validChannel,
		Prompt:          "hi",
		AllowedTools:    []string{"create_post"},
		Enabled:         true,
	}

	tests := []struct {
		name    string
		mutate  func(s *AgentSubscription)
		wantErr string // substring match on err.Error(); "" means expect nil
	}{
		{
			name:   "valid",
			mutate: func(s *AgentSubscription) {},
		},
		{
			name:    "unsupported event",
			mutate:  func(s *AgentSubscription) { s.Event = "user_joined_team" },
			wantErr: "unsupported subscription event",
		},
		{
			name:    "missing scope channel",
			mutate:  func(s *AgentSubscription) { s.ScopeChannelID = "" },
			wantErr: "scopeChannelID",
		},
		{
			name:    "missing target channel",
			mutate:  func(s *AgentSubscription) { s.TargetChannelID = "" },
			wantErr: "targetChannelID",
		},
		{
			name:    "bad channel length",
			mutate:  func(s *AgentSubscription) { s.TargetChannelID = "short" },
			wantErr: "targetChannelID",
		},
		{
			name:    "empty prompt",
			mutate:  func(s *AgentSubscription) { s.Prompt = "" },
			wantErr: "prompt is required",
		},
		{
			name:    "prompt too long",
			mutate:  func(s *AgentSubscription) { s.Prompt = strings.Repeat("x", MaxTriggerPromptRunes+1) },
			wantErr: "prompt exceeds maximum",
		},
		{
			name:    "reserved tool rejected",
			mutate:  func(s *AgentSubscription) { s.AllowedTools = []string{"create_post_as_user"} },
			wantErr: "not allowed",
		},
		{
			name:    "empty tool name rejected",
			mutate:  func(s *AgentSubscription) { s.AllowedTools = []string{""} },
			wantErr: "empty name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := baseValid
			tc.mutate(&s)
			err := s.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("wanted no error, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("wanted error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("wanted error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestAgentSchedule_Validate(t *testing.T) {
	validChannel := "abcdefghijklmnopqrstuvwxyz"

	baseValid := AgentSchedule{
		IntervalSeconds: MinScheduleIntervalSeconds,
		Prompt:          "hi",
		TargetChannelID: validChannel,
		AllowedTools:    []string{"create_post"},
		Enabled:         true,
	}

	tests := []struct {
		name    string
		mutate  func(s *AgentSchedule)
		wantErr string
	}{
		{
			name:   "valid at minimum interval",
			mutate: func(s *AgentSchedule) {},
		},
		{
			name:    "interval below minimum",
			mutate:  func(s *AgentSchedule) { s.IntervalSeconds = MinScheduleIntervalSeconds - 1 },
			wantErr: "intervalSeconds must be at least",
		},
		{
			name:    "zero interval",
			mutate:  func(s *AgentSchedule) { s.IntervalSeconds = 0 },
			wantErr: "intervalSeconds must be at least",
		},
		{
			name:    "negative interval",
			mutate:  func(s *AgentSchedule) { s.IntervalSeconds = -1 },
			wantErr: "intervalSeconds must be at least",
		},
		{
			name:    "missing target channel",
			mutate:  func(s *AgentSchedule) { s.TargetChannelID = "" },
			wantErr: "targetChannelID",
		},
		{
			name:    "reserved tool rejected",
			mutate:  func(s *AgentSchedule) { s.AllowedTools = []string{"create_post_as_user"} },
			wantErr: "not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := baseValid
			tc.mutate(&s)
			err := s.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("wanted no error, got %v", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("wanted error containing %q, got nil", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("wanted error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestBotConfig_Validate_PropagatesTriggerErrors(t *testing.T) {
	cfg := BotConfig{
		Name:        "bot",
		DisplayName: "Bot",
		ServiceID:   "svc",
		Schedules: []AgentSchedule{
			{IntervalSeconds: 60, TargetChannelID: "abcdefghijklmnopqrstuvwxyz", Prompt: "x"},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "schedules[0]") {
		t.Fatalf("expected schedules[0] error, got %v", err)
	}
}
