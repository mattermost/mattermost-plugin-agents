// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"reflect"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// managerEditableBotConfigFields are the BotConfig fields an agent manager may
// change while service account auth stays on. Must stay in sync with
// clearManagerEditableFields. Every other BotConfig field is sensitive by default.
var managerEditableBotConfigFields = map[string]bool{
	"DisplayName":             true,
	"CustomInstructions":      true,
	"Model":                   true,
	"ServiceID":               true,
	"EnableVision":            true,
	"DisableTools":            true,
	"EnabledNativeTools":      true,
	"MCPDynamicToolLoading":   true,
	"ReasoningEnabled":        true,
	"ReasoningEffort":         true,
	"ThinkingBudget":          true,
	"StructuredOutputEnabled": true,
	"MaxToolTurns":            true,
	"UseServiceAccountAuth":   true,
}

func TestServiceAccountChangeNeedsAdmin(t *testing.T) {
	tests := []struct {
		name     string
		stored   llm.BotConfig
		proposed llm.BotConfig
		want     bool
	}{
		{
			name:     "enabling SA needs admin",
			stored:   llm.BotConfig{UseServiceAccountAuth: false, DisplayName: "A"},
			proposed: llm.BotConfig{UseServiceAccountAuth: true, DisplayName: "A"},
			want:     true,
		},
		{
			name:     "turning SA off never needs admin",
			stored:   llm.BotConfig{UseServiceAccountAuth: true, ServiceID: "svc-1"},
			proposed: llm.BotConfig{UseServiceAccountAuth: false, ServiceID: "svc-2"},
			want:     false,
		},
		{
			name:     "manager-editable change while SA on does not need admin",
			stored:   llm.BotConfig{UseServiceAccountAuth: true, DisplayName: "A", ServiceID: "svc-1"},
			proposed: llm.BotConfig{UseServiceAccountAuth: true, DisplayName: "B", ServiceID: "svc-2"},
			want:     false,
		},
		{
			name: "sensitive access change while SA on needs admin",
			stored: llm.BotConfig{
				UseServiceAccountAuth: true,
				UserAccessLevel:       llm.UserAccessLevelNone,
			},
			proposed: llm.BotConfig{
				UseServiceAccountAuth: true,
				UserAccessLevel:       llm.UserAccessLevelAll,
			},
			want: true,
		},
		{
			name: "reordered channel IDs while SA on do not need admin",
			stored: llm.BotConfig{
				UseServiceAccountAuth: true,
				ChannelIDs:            []string{"a", "b"},
			},
			proposed: llm.BotConfig{
				UseServiceAccountAuth: true,
				ChannelIDs:            []string{"b", "a"},
			},
			want: false,
		},
		{
			name: "nil vs empty channel IDs while SA on do not need admin",
			stored: llm.BotConfig{
				UseServiceAccountAuth: true,
				ChannelIDs:            nil,
			},
			proposed: llm.BotConfig{
				UseServiceAccountAuth: true,
				ChannelIDs:            []string{},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, serviceAccountChangeNeedsAdmin(tc.stored, tc.proposed))
		})
	}
}

// TestServiceAccountSensitiveFieldsAreExhaustive fails when a BotConfig field is
// added without deciding whether an agent manager may change it while service
// account auth is on. Manager-editable fields are listed in
// managerEditableBotConfigFields (and cleared in clearManagerEditableFields);
// every other field is sensitive by default.
func TestServiceAccountSensitiveFieldsAreExhaustive(t *testing.T) {
	typ := reflect.TypeFor[llm.BotConfig]()
	require.Greater(t, typ.NumField(), 0)

	for field := range typ.Fields() {
		t.Run(field.Name, func(t *testing.T) {
			stored := baseSAOnBotConfig()
			proposed := stored

			mutateBotConfigField(t, &proposed, field.Name)

			needsAdmin := serviceAccountChangeNeedsAdmin(stored, proposed)
			if managerEditableBotConfigFields[field.Name] {
				assert.False(t, needsAdmin,
					"field %s is listed as manager-editable; changing it while SA is on must not require admin",
					field.Name)
				return
			}
			assert.True(t, needsAdmin,
				"field %s is not in managerEditableBotConfigFields; changing it while SA is on must require admin (fail-closed). Add it to clearManagerEditableFields and managerEditableBotConfigFields if managers should be allowed to edit it",
				field.Name)
		})
	}
}

func baseSAOnBotConfig() llm.BotConfig {
	return llm.BotConfig{
		ID:                      "agent-1",
		Name:                    "agent",
		DisplayName:             "Agent",
		CustomInstructions:      "instructions",
		ServiceID:               "svc-1",
		Model:                   "gpt-4.1",
		EnableVision:            false,
		DisableTools:            false,
		ChannelAccessLevel:      llm.ChannelAccessLevelAll,
		ChannelIDs:              []string{"chan-a", "chan-b"},
		UserAccessLevel:         llm.UserAccessLevelAll,
		UserIDs:                 []string{"user-a"},
		TeamIDs:                 []string{"team-a"},
		MaxFileSize:             1024,
		EnabledNativeTools:      []string{"web_search"},
		EnabledMCPTools:         []llm.EnabledMCPTool{{ServerOrigin: "https://mcp.example.com", ToolName: "search"}},
		AutoEnableNewMCPTools:   false,
		MCPDynamicToolLoading:   true,
		UseServiceAccountAuth:   true,
		ReasoningEnabled:        false,
		ReasoningEffort:         "medium",
		ThinkingBudget:          1024,
		StructuredOutputEnabled: false,
		MaxToolTurns:            10,
		BotUserID:               "bot-1",
		CreatorID:               "creator-1",
		AdminUserIDs:            []string{"admin-1"},
		CreateAt:                1,
		UpdateAt:                2,
		DeleteAt:                0,
	}
}

func mutateBotConfigField(t *testing.T, cfg *llm.BotConfig, fieldName string) {
	t.Helper()
	v := reflect.ValueOf(cfg).Elem().FieldByName(fieldName)
	require.True(t, v.IsValid() && v.CanSet(), "cannot set field %s", fieldName)

	switch fieldName {
	case "UseServiceAccountAuth":
		// Turning SA off is always allowed; that still exercises the manager-editable path.
		v.SetBool(false)
		return
	case "Service":
		v.Set(reflect.ValueOf(&llm.ServiceConfig{ID: "embedded-svc"}))
		return
	case "EnabledMCPTools":
		v.Set(reflect.ValueOf([]llm.EnabledMCPTool{{ServerOrigin: "https://other.example.com", ToolName: "other"}}))
		return
	case "ChannelIDs", "UserIDs", "TeamIDs", "AdminUserIDs", "EnabledNativeTools":
		v.Set(reflect.ValueOf([]string{"mutated-id"}))
		return
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-mutated")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 1)
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		} else {
			v.Set(reflect.Zero(v.Type()))
		}
	case reflect.Slice:
		if v.Len() == 0 {
			elem := reflect.New(v.Type().Elem()).Elem()
			v.Set(reflect.Append(v, elem))
		} else {
			v.Set(reflect.Zero(v.Type()))
		}
	default:
		t.Fatalf("unsupported BotConfig field kind %s for %s; extend mutateBotConfigField", v.Kind(), fieldName)
	}
}
