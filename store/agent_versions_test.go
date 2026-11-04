// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupVersionTestStore(t *testing.T) *Store {
	t.Helper()
	s := setupTestStore(t)
	require.NoError(t, s.RunMigrations())
	return s
}

// insertUnversionedAgent writes an agent row directly, the way agents
// written before versioning existed look.
func insertUnversionedAgent(t *testing.T, s *Store, id, username, instructions string, deleteAt int64) {
	t.Helper()
	_, err := s.db.Exec(`
		INSERT INTO Agents_UserAgents (
			ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, CreateAt, UpdateAt, DeleteAt
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, "bot"+id[3:], "creator-1", "Legacy "+username, username, "svc-1",
		instructions, int64(100), int64(200), deleteAt,
	)
	require.NoError(t, err)
}

// listAllAgentVersions returns every version of agentID, newest first.
func listAllAgentVersions(s *Store, agentID string) ([]AgentVersion, error) {
	page, err := s.ListAgentVersions(agentID, 0, 10000)
	return page.Versions, err
}

func currentAgentVersion(t *testing.T, s *Store, agentID string) int {
	t.Helper()
	page, err := s.ListAgentVersions(agentID, 0, 1)
	require.NoError(t, err)
	return page.CurrentVersion
}

func versionNumbers(versions []AgentVersion) []int {
	out := make([]int, 0, len(versions))
	for _, v := range versions {
		out = append(out, v.Version)
	}
	return out
}

func TestAgentVersionLifecycle(t *testing.T) {
	s := setupVersionTestStore(t)

	agent := testAgent("creator-1", "versioned", "Versioned")
	require.NoError(t, s.CreateAgent(agent, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceCreate}))

	agent.CustomInstructions = "Second instructions"
	require.NoError(t, s.UpdateAgent(agent, AgentVersionMeta{ActorID: "editor-1", Source: AgentVersionSourceUpdate}))

	v1, err := s.GetAgentVersion(agent.ID, 1)
	require.NoError(t, err)
	require.NotNil(t, v1)
	restored := v1.Config
	restored.ID = agent.ID
	restored.BotUserID = agent.BotUserID
	restored.CreatorID = agent.CreatorID
	restored.UpdateAt = agent.UpdateAt
	require.NoError(t, s.UpdateAgent(&restored, AgentVersionMeta{ActorID: "editor-2", Source: AgentVersionSourceRestore, RestoredFromVersion: 1}))

	versions, err := listAllAgentVersions(s, agent.ID)
	require.NoError(t, err)
	require.Equal(t, []int{3, 2, 1}, versionNumbers(versions), "versions are listed newest first")

	tests := []struct {
		name                string
		version             AgentVersion
		createdBy           string
		source              AgentVersionSource
		restoredFromVersion int
		changedFields       []string
		instructions        string
	}{
		{name: "create", version: versions[2], createdBy: "creator-1", source: AgentVersionSourceCreate, changedFields: []string{}, instructions: "Be helpful and concise"},
		{name: "update", version: versions[1], createdBy: "editor-1", source: AgentVersionSourceUpdate, changedFields: []string{"customInstructions"}, instructions: "Second instructions"},
		{name: "restore", version: versions[0], createdBy: "editor-2", source: AgentVersionSourceRestore, restoredFromVersion: 1, changedFields: []string{"customInstructions"}, instructions: "Be helpful and concise"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.createdBy, tt.version.CreatedBy)
			assert.Equal(t, tt.source, tt.version.Source)
			assert.Equal(t, tt.restoredFromVersion, tt.version.RestoredFromVersion)
			assert.Equal(t, tt.changedFields, tt.version.ChangedFields)
			assert.NotZero(t, tt.version.CreateAt)

			detail, getErr := s.GetAgentVersion(agent.ID, tt.version.Version)
			require.NoError(t, getErr)
			require.NotNil(t, detail)
			assert.Equal(t, tt.version, detail.AgentVersion)
			assert.Equal(t, tt.instructions, detail.Config.CustomInstructions)
		})
	}

	t.Run("snapshot carries the full editable configuration", func(t *testing.T) {
		detail, getErr := s.GetAgentVersion(agent.ID, 1)
		require.NoError(t, getErr)
		want := testAgent("creator-1", "versioned", "Versioned")
		cfg := detail.Config
		assert.Equal(t, want.Name, cfg.Name)
		assert.Equal(t, want.DisplayName, cfg.DisplayName)
		assert.Equal(t, want.ServiceID, cfg.ServiceID)
		assert.Equal(t, want.ChannelIDs, cfg.ChannelIDs)
		assert.Equal(t, want.TeamIDs, cfg.TeamIDs)
		assert.Equal(t, want.AdminUserIDs, cfg.AdminUserIDs)
		assert.Equal(t, want.EnabledMCPTools, cfg.EnabledMCPTools)
		assert.Equal(t, want.EnabledNativeTools, cfg.EnabledNativeTools)
		assert.Equal(t, want.Model, cfg.Model)
		assert.Equal(t, want.MaxToolTurns, cfg.MaxToolTurns)
		assert.Equal(t, want.UseServiceAccountAuth, cfg.UseServiceAccountAuth)
		assert.Equal(t, want.ReasoningEffort, cfg.ReasoningEffort)
		assert.Equal(t, want.ThinkingBudget, cfg.ThinkingBudget)
	})

	t.Run("unknown version", func(t *testing.T) {
		missing, getErr := s.GetAgentVersion(agent.ID, 4)
		require.NoError(t, getErr)
		assert.Nil(t, missing)
	})
}

func TestListAgentVersionsPaging(t *testing.T) {
	s := setupVersionTestStore(t)
	agent := testAgent("creator-1", "paged", "Paged")
	require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))
	for i := range 4 {
		agent.CustomInstructions = fmt.Sprintf("edit %d", i)
		require.NoError(t, s.UpdateAgent(agent, SystemAgentVersionMeta()))
	}

	tests := []struct {
		name             string
		agentID          string
		before           int
		limit            int
		expectedVersions []int
		expectedHasMore  bool
		expectedCurrent  int
	}{
		{name: "first page", agentID: agent.ID, limit: 2, expectedVersions: []int{5, 4}, expectedHasMore: true, expectedCurrent: 5},
		{name: "middle page", agentID: agent.ID, before: 4, limit: 2, expectedVersions: []int{3, 2}, expectedHasMore: true, expectedCurrent: 5},
		{name: "last partial page", agentID: agent.ID, before: 2, limit: 2, expectedVersions: []int{1}, expectedCurrent: 5},
		{name: "last page filled exactly", agentID: agent.ID, before: 3, limit: 2, expectedVersions: []int{2, 1}, expectedCurrent: 5},
		{name: "page larger than history", agentID: agent.ID, before: 100, limit: 10, expectedVersions: []int{5, 4, 3, 2, 1}, expectedCurrent: 5},
		{name: "nothing before the first version", agentID: agent.ID, before: 1, limit: 2, expectedVersions: []int{}, expectedCurrent: 5},
		{name: "agent without versions", agentID: "unknownagent0000000000001", limit: 2, expectedVersions: []int{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := s.ListAgentVersions(tt.agentID, tt.before, tt.limit)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedVersions, versionNumbers(page.Versions))
			assert.NotNil(t, page.Versions, "an empty page must serialize as []")
			assert.Equal(t, tt.expectedHasMore, page.HasMore)
			assert.Equal(t, tt.expectedCurrent, page.CurrentVersion)
		})
	}

	_, err := s.ListAgentVersions(agent.ID, 0, 0)
	assert.Error(t, err, "a page must hold at least one version")
}

// insertRawAgentVersion writes a version row with the given config JSON, the
// way versions recorded by an older plugin look.
func insertRawAgentVersion(t *testing.T, s *Store, agentID string, version int, configJSON string) {
	t.Helper()
	_, err := s.db.Exec(`
		INSERT INTO Agents_AgentVersions (ID, AgentID, Version, Config, CreateAt, Source)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		model.NewId(), agentID, version, configJSON, int64(100), string(AgentVersionSourceUpdate),
	)
	require.NoError(t, err)
}

// Versions are never rewritten, so a field added to BotConfig after a version
// was recorded is read with the default from BotConfig.UnmarshalJSON.
func TestAgentVersionReadDefaultsFieldsMissingFromOldSnapshots(t *testing.T) {
	tests := []struct {
		name                          string
		configJSON                    string
		expectedMCPDynamicToolLoading bool
	}{
		{name: "key absent in an old snapshot", configJSON: `{"name":"legacy","serviceID":"svc-1"}`, expectedMCPDynamicToolLoading: true},
		{name: "explicit false is kept", configJSON: `{"name":"legacy","serviceID":"svc-1","mcpDynamicToolLoading":false}`, expectedMCPDynamicToolLoading: false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupVersionTestStore(t)
			agentID := fmt.Sprintf("legacyagent%015d", i)
			insertRawAgentVersion(t, s, agentID, 1, tt.configJSON)

			detail, err := s.GetAgentVersion(agentID, 1)
			require.NoError(t, err)
			require.NotNil(t, detail)
			assert.Equal(t, "legacy", detail.Config.Name)
			assert.Equal(t, tt.expectedMCPDynamicToolLoading, detail.Config.MCPDynamicToolLoading)
		})
	}
}

// fillNonZero sets v, and every field reachable from it, to a non-zero value.
func fillNonZero(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("value-" + path)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Slice:
		if v.Type() == reflect.TypeFor[json.RawMessage]() {
			v.SetBytes([]byte(`{"set":true}`))
			return
		}
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillNonZero(t, v.Index(0), path+"[0]")
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillNonZero(t, v.Elem(), path)
	case reflect.Struct:
		for i := range v.NumField() {
			fillNonZero(t, v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	case reflect.Map:
		key := reflect.New(v.Type().Key()).Elem()
		fillNonZero(t, key, path+"[key]")
		elem := reflect.New(v.Type().Elem()).Elem()
		fillNonZero(t, elem, path+"[value]")
		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(key, elem)
	default:
		t.Fatalf("%s has kind %s, which fillNonZero cannot set; extend fillNonZero", path, v.Kind())
	}
}

func isZeroJSON(raw json.RawMessage) bool {
	switch string(raw) {
	case "null", "0", `""`, "false", "[]", "{}":
		return true
	}
	return false
}

func missingFrom(keys []string, set map[string]bool) []string {
	var out []string
	for _, k := range keys {
		if !set[k] {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// TestAgentVersionSnapshotKeys pins the BotConfig JSON keys recorded in agent
// versions so that adding, removing or renaming a BotConfig field forces a
// deliberate decision about version history.
func TestAgentVersionSnapshotKeys(t *testing.T) {
	// Agent configuration recorded in every version and brought back by restore.
	versionedKeys := []string{
		"id", "name", "displayName", "customInstructions", "serviceID", "model",
		"enableVision", "disableTools", "channelAccessLevel", "channelIDs",
		"userAccessLevel", "userIDs", "teamIDs", "enabledNativeTools",
		"enabledMCPTools", "autoEnableNewMCPTools", "mcpDynamicToolLoading",
		"useServiceAccountAuth", "reasoningEnabled", "reasoningEffort",
		"thinkingBudget", "structuredOutputEnabled", "maxToolTurns", "adminUserIDs",
		"skills",
	}
	// Cleared by agentVersionSnapshot: secrets, lifecycle metadata and IDs
	// that restore must not change.
	excludedKeys := []string{
		"service", "maxFileSize", "botUserID", "creatorID", "createAt", "updateAt", "deleteAt",
	}

	var cfg llm.BotConfig
	fillNonZero(t, reflect.ValueOf(&cfg).Elem(), "BotConfig")

	var source, snapshot map[string]json.RawMessage
	sourceJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(sourceJSON, &source))
	snapshotJSON, err := json.Marshal(agentVersionSnapshot(&cfg))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(snapshotJSON, &snapshot))

	// A key is stored when the snapshot carries the field's value, and
	// cleared when it is absent or zeroed (maxFileSize has no omitempty).
	allKeys := map[string]bool{}
	stored := map[string]bool{}
	for key, value := range source {
		allKeys[key] = true
		if snapValue, ok := snapshot[key]; ok && !isZeroJSON(snapValue) {
			require.JSONEq(t, string(value), string(snapValue), "snapshot changed the value of %q", key)
			stored[key] = true
		}
	}
	expected := map[string]bool{}
	for _, key := range slices.Concat(versionedKeys, excludedKeys) {
		expected[key] = true
	}

	var msg strings.Builder
	if added := missingFrom(slices.Collect(maps.Keys(allKeys)), expected); len(added) > 0 {
		fmt.Fprintf(&msg, `
New llm.BotConfig JSON keys: %v
  agentVersionSnapshot stores new fields in every agent version permanently
  (versions are never pruned), and agent managers see them in version history.
  Decide what each new field is:
  - Agent configuration that restoring a version should bring back: add the key
    to versionedKeys, make sure AgentRequestFields carries it
    (requestFieldsFromConfig and applyTo in api/api_agents.go), and if its zero
    value is not how agents behaved before the field existed, give it a default
    in BotConfig.UnmarshalJSON, because older versions omit the key.
  - A secret, lifecycle metadata or an ID: clear it in agentVersionSnapshot
    (store/agent_versions.go) and add the key to excludedKeys.
`, added)
	}
	if removed := missingFrom(slices.Concat(versionedKeys, excludedKeys), allKeys); len(removed) > 0 {
		fmt.Fprintf(&msg, `
Expected JSON keys no longer on llm.BotConfig (removed or renamed): %v
  Existing agent versions still contain these keys and are never rewritten, so
  after a rename reading or restoring an old version silently loses the value.
  Keep reading the old key (for example in BotConfig.UnmarshalJSON) or upgrade
  old snapshots on read in getAgentVersionDetail; never rewrite version rows.
  Then update versionedKeys/excludedKeys.
`, removed)
	}
	notStored := slices.DeleteFunc(missingFrom(versionedKeys, stored), func(key string) bool { return !allKeys[key] })
	if len(notStored) > 0 {
		fmt.Fprintf(&msg, `
Versioned keys that agentVersionSnapshot clears: %v
  Restoring a version would reset these fields. Stop clearing them, or move
  them to excludedKeys if they really must not be versioned.
`, notStored)
	}
	var leaked []string
	for _, key := range excludedKeys {
		if stored[key] {
			leaked = append(leaked, key)
		}
	}
	if len(leaked) > 0 {
		fmt.Fprintf(&msg, `
Excluded keys that agentVersionSnapshot stores: %v
  These must never reach a version: clear them in agentVersionSnapshot.
`, leaked)
	}
	if msg.Len() > 0 {
		t.Fatalf("agent version snapshot keys changed.\n%s", msg.String())
	}
}

func TestAgentVersionChangedFields(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(cfg *llm.BotConfig)
		expected []string
	}{
		{
			name:     "save without changes records an empty diff",
			mutate:   func(cfg *llm.BotConfig) {},
			expected: []string{},
		},
		{
			name: "empty and nil collections are the same value",
			mutate: func(cfg *llm.BotConfig) {
				cfg.UserIDs = []string{}
				cfg.EnabledNativeTools = []string{}
			},
			expected: []string{},
		},
		{
			name: "multiple fields are reported sorted, by name only",
			mutate: func(cfg *llm.BotConfig) {
				cfg.DisplayName = "Renamed"
				cfg.ChannelIDs = []string{"ch-3"}
				cfg.MaxToolTurns = 7
			},
			expected: []string{"channelIDs", "displayName", "maxToolTurns"},
		},
		{
			name: "MCP tool list change",
			mutate: func(cfg *llm.BotConfig) {
				cfg.EnabledMCPTools = []llm.EnabledMCPTool{{ServerOrigin: "https://other.example.com", ToolName: "x"}}
			},
			expected: []string{"enabledMCPTools"},
		},
		{
			name: "deprecated structured output flag is not reported",
			mutate: func(cfg *llm.BotConfig) {
				cfg.StructuredOutputEnabled = !cfg.StructuredOutputEnabled //nolint:staticcheck // deprecated field still persisted
				cfg.Model = "other-model"
			},
			expected: []string{"model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setupVersionTestStore(t)
			agent := testAgent("creator-1", "diff-agent", "Diff Agent")
			agent.UserIDs = nil
			agent.EnabledNativeTools = nil
			require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

			fetched, err := s.GetAgent(agent.ID)
			require.NoError(t, err)
			tt.mutate(fetched)
			require.NoError(t, s.UpdateAgent(fetched, SystemAgentVersionMeta()))

			v2, err := s.GetAgentVersion(agent.ID, 2)
			require.NoError(t, err)
			require.NotNil(t, v2)
			assert.Equal(t, tt.expected, v2.ChangedFields)
		})
	}
}

// Versions recorded before skills existed have no "skills" key; an agent that
// never configured skills must not report them changed on its next edit.
func TestChangedVersionFieldsSkillsSinceVersioning(t *testing.T) {
	prev := `{"name":"agent","displayName":"Agent"}`
	tests := []struct {
		name string
		next llm.BotConfig
		want []string
	}{
		{name: "skills never configured", next: llm.BotConfig{Name: "agent", DisplayName: "Agent"}, want: []string{}},
		{name: "skills configured", next: llm.BotConfig{Name: "agent", DisplayName: "Agent", Skills: llm.SkillsConfig{AllowPersonal: true}}, want: []string{"skills"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := slices.DeleteFunc(changedVersionFields(prev, tt.next), func(key string) bool {
				return key != "skills"
			})
			assert.Equal(t, tt.want, changed)
		})
	}
}

func TestChangedVersionFieldsIgnoresUnversionedKeys(t *testing.T) {
	next := llm.BotConfig{Name: "agent", StructuredOutputEnabled: false} //nolint:staticcheck // deprecated field still persisted
	prevJSON, err := json.Marshal(next)
	require.NoError(t, err)
	var prev map[string]any
	require.NoError(t, json.Unmarshal(prevJSON, &prev))
	prev["service"] = map[string]any{"id": "svc-1", "type": "openai"}
	prev["structuredOutputEnabled"] = true
	prev["displayName"] = "Old Name"
	raw, err := json.Marshal(prev)
	require.NoError(t, err)

	assert.Equal(t, []string{"displayName"}, changedVersionFields(string(raw), next))
}

func TestAgentVersionWritesAreTransactional(t *testing.T) {
	tooLongActor := strings.Repeat("a", 40) // exceeds CreatedBy VARCHAR(26)
	tooLongUsername := strings.Repeat("u", 80)

	t.Run("create rolls back the agent when the version cannot be written", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "rb-create", "Rollback")
		err := s.CreateAgent(agent, AgentVersionMeta{ActorID: tooLongActor, Source: AgentVersionSourceCreate})
		require.Error(t, err)

		agents, listErr := s.ListAgents()
		require.NoError(t, listErr)
		assert.Empty(t, agents)
	})

	t.Run("update rolls back the agent when the version cannot be written", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "rb-update", "Rollback")
		require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

		changed := *agent
		changed.CustomInstructions = "must not persist"
		err := s.UpdateAgent(&changed, AgentVersionMeta{ActorID: tooLongActor, Source: AgentVersionSourceUpdate})
		require.Error(t, err)

		fetched, getErr := s.GetAgent(agent.ID)
		require.NoError(t, getErr)
		assert.Equal(t, "Be helpful and concise", fetched.CustomInstructions)
		versions, listErr := listAllAgentVersions(s, agent.ID)
		require.NoError(t, listErr)
		assert.Equal(t, []int{1}, versionNumbers(versions))
	})

	t.Run("failed agent update writes no version", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "failed-update", "Failed")
		require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

		changed := *agent
		changed.Name = tooLongUsername
		require.Error(t, s.UpdateAgent(&changed, SystemAgentVersionMeta()))

		versions, listErr := listAllAgentVersions(s, agent.ID)
		require.NoError(t, listErr)
		assert.Equal(t, []int{1}, versionNumbers(versions))
	})

	t.Run("update of a deleted agent writes no version", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "deleted-update", "Deleted")
		require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))
		require.NoError(t, s.DeleteAgent(agent.ID))

		require.Error(t, s.UpdateAgent(agent, SystemAgentVersionMeta()))
		versions, listErr := listAllAgentVersions(s, agent.ID)
		require.NoError(t, listErr)
		assert.Empty(t, versions)
	})
}

func TestAgentVersionMetaValidation(t *testing.T) {
	tests := []struct {
		name    string
		meta    AgentVersionMeta
		wantErr bool
	}{
		{name: "missing source", meta: AgentVersionMeta{ActorID: "u1"}, wantErr: true},
		{name: "unknown source", meta: AgentVersionMeta{Source: "bogus"}, wantErr: true},
		{name: "restore without restored version", meta: AgentVersionMeta{Source: AgentVersionSourceRestore}, wantErr: true},
		{name: "restored version on a non-restore source", meta: AgentVersionMeta{Source: AgentVersionSourceUpdate, RestoredFromVersion: 2}, wantErr: true},
		{name: "valid restore", meta: AgentVersionMeta{Source: AgentVersionSourceRestore, RestoredFromVersion: 1}},
		{name: "valid update", meta: AgentVersionMeta{ActorID: "u1", Source: AgentVersionSourceUpdate}},
	}

	s := setupVersionTestStore(t)
	agent := testAgent("creator-1", "meta-agent", "Meta")
	require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := currentAgentVersion(t, s, agent.ID)

			err := s.UpdateAgent(agent, tt.meta)
			after := currentAgentVersion(t, s, agent.ID)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, before, after)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, before+1, after)
		})
	}
}

func TestAgentVersionConcurrentUpdates(t *testing.T) {
	s := setupVersionTestStore(t)
	agent := testAgent("creator-1", "concurrent", "Concurrent")
	require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))

	const writers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := range writers {
		wg.Go(func() {
			cfg := *agent
			cfg.CustomInstructions = fmt.Sprintf("writer %d", i)
			errCh <- s.UpdateAgent(&cfg, AgentVersionMeta{Source: AgentVersionSourceUpdate})
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	versions, err := listAllAgentVersions(s, agent.ID)
	require.NoError(t, err)
	expected := make([]int, 0, writers+1)
	for v := writers + 1; v >= 1; v-- {
		expected = append(expected, v)
	}
	assert.Equal(t, expected, versionNumbers(versions), "concurrent saves get gapless, unique version numbers")
}

func TestAgentVersionUnversionedAgents(t *testing.T) {
	t.Run("backfill records version 1 once for active agents without versions", func(t *testing.T) {
		s := setupVersionTestStore(t)
		insertUnversionedAgent(t, s, "legacyagent000000000000001", "legacy-one", "legacy instructions", 0)
		insertUnversionedAgent(t, s, "legacyagent000000000000002", "legacy-two", "", 0)
		insertUnversionedAgent(t, s, "legacyagent000000000000003", "legacy-deleted", "", 500)
		versioned := testAgent("creator-1", "versioned", "Versioned")
		require.NoError(t, s.CreateAgent(versioned, SystemAgentVersionMeta()))

		backfilled, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Equal(t, 2, backfilled)

		again, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Zero(t, again, "a second run must not write anything")

		v1, err := s.GetAgentVersion("legacyagent000000000000001", 1)
		require.NoError(t, err)
		require.NotNil(t, v1)
		assert.Equal(t, AgentVersionSourceInitial, v1.Source)
		assert.Empty(t, v1.CreatedBy)
		assert.Empty(t, v1.ChangedFields)
		assert.Equal(t, int64(200), v1.CreateAt, "the initial version is dated by the agent's last update")
		assert.Equal(t, "legacy instructions", v1.Config.CustomInstructions)
		assert.Equal(t, "legacy-one", v1.Config.Name)

		deletedVersions, err := listAllAgentVersions(s, "legacyagent000000000000003")
		require.NoError(t, err)
		assert.Empty(t, deletedVersions, "soft-deleted agents are not backfilled")

		versionedVersions, err := listAllAgentVersions(s, versioned.ID)
		require.NoError(t, err)
		assert.Equal(t, []int{1}, versionNumbers(versionedVersions))
	})

	t.Run("concurrent backfills write each initial version once", func(t *testing.T) {
		s := setupVersionTestStore(t)
		for i := range 5 {
			insertUnversionedAgent(t, s, fmt.Sprintf("legacyagent%015d", i), fmt.Sprintf("legacy-%d", i), "", 0)
		}

		const nodes = 4
		var wg sync.WaitGroup
		counts := make(chan int, nodes)
		errCh := make(chan error, nodes)
		for range nodes {
			wg.Go(func() {
				n, err := s.BackfillAgentVersions()
				counts <- n
				errCh <- err
			})
		}
		wg.Wait()
		close(counts)
		close(errCh)
		for err := range errCh {
			require.NoError(t, err)
		}
		total := 0
		for n := range counts {
			total += n
		}
		assert.Equal(t, 5, total)

		var rows int
		require.NoError(t, s.db.Get(&rows, `SELECT COUNT(*) FROM Agents_AgentVersions`))
		assert.Equal(t, 5, rows)
	})

	t.Run("first update of an unversioned agent records its prior state as version 1", func(t *testing.T) {
		s := setupVersionTestStore(t)
		insertUnversionedAgent(t, s, "legacyagent000000000000001", "legacy-one", "before", 0)

		cfg, err := s.GetAgent("legacyagent000000000000001")
		require.NoError(t, err)
		cfg.CustomInstructions = "after"
		require.NoError(t, s.UpdateAgent(cfg, AgentVersionMeta{ActorID: "editor-1", Source: AgentVersionSourceUpdate}))

		versions, err := listAllAgentVersions(s, cfg.ID)
		require.NoError(t, err)
		require.Equal(t, []int{2, 1}, versionNumbers(versions))
		assert.Equal(t, AgentVersionSourceInitial, versions[1].Source)
		assert.Equal(t, AgentVersionSourceUpdate, versions[0].Source)
		assert.Equal(t, []string{"customInstructions"}, versions[0].ChangedFields)

		v1, err := s.GetAgentVersion(cfg.ID, 1)
		require.NoError(t, err)
		assert.Equal(t, "before", v1.Config.CustomInstructions)
	})
}

// setAgentServiceIDDirectly rewrites an agent row without recording a
// version, the way the service ID migration does.
func setAgentServiceIDDirectly(t *testing.T, s *Store, agentID, serviceID string) {
	t.Helper()
	_, err := s.db.Exec(`UPDATE Agents_UserAgents SET ServiceID = $1 WHERE ID = $2`, serviceID, agentID)
	require.NoError(t, err)
}

func TestAgentVersionRowDrift(t *testing.T) {
	t.Run("update first records a row that changed without a version", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "drifted", "Drifted")
		require.NoError(t, s.CreateAgent(agent, AgentVersionMeta{ActorID: "creator-1", Source: AgentVersionSourceCreate}))
		setAgentServiceIDDirectly(t, s, agent.ID, "svc-remapped")

		cfg, err := s.GetAgent(agent.ID)
		require.NoError(t, err)
		cfg.CustomInstructions = "edited"
		require.NoError(t, s.UpdateAgent(cfg, AgentVersionMeta{ActorID: "editor-1", Source: AgentVersionSourceUpdate}))

		versions, err := listAllAgentVersions(s, agent.ID)
		require.NoError(t, err)
		require.Equal(t, []int{3, 2, 1}, versionNumbers(versions))
		assert.Equal(t, AgentVersionSourceSystem, versions[1].Source)
		assert.Empty(t, versions[1].CreatedBy)
		assert.Equal(t, []string{"serviceID"}, versions[1].ChangedFields)
		assert.Equal(t, AgentVersionSourceUpdate, versions[0].Source)
		assert.Equal(t, "editor-1", versions[0].CreatedBy)
		assert.Equal(t, []string{"customInstructions"}, versions[0].ChangedFields, "the editor's version lists only the editor's change")

		v2, err := s.GetAgentVersion(agent.ID, 2)
		require.NoError(t, err)
		assert.Equal(t, "svc-remapped", v2.Config.ServiceID)
		assert.Equal(t, "Be helpful and concise", v2.Config.CustomInstructions)
	})

	t.Run("update of an unchanged row records only the new version", func(t *testing.T) {
		s := setupVersionTestStore(t)
		agent := testAgent("creator-1", "in-sync", "In Sync")
		require.NoError(t, s.CreateAgent(agent, SystemAgentVersionMeta()))
		agent.CustomInstructions = "edited"
		require.NoError(t, s.UpdateAgent(agent, SystemAgentVersionMeta()))

		versions, err := listAllAgentVersions(s, agent.ID)
		require.NoError(t, err)
		assert.Equal(t, []int{2, 1}, versionNumbers(versions))
	})

	t.Run("backfill records a row that changed without a version once", func(t *testing.T) {
		s := setupVersionTestStore(t)
		drifted := testAgent("creator-1", "drifted", "Drifted")
		require.NoError(t, s.CreateAgent(drifted, SystemAgentVersionMeta()))
		inSync := testAgent("creator-1", "in-sync", "In Sync")
		require.NoError(t, s.CreateAgent(inSync, SystemAgentVersionMeta()))
		setAgentServiceIDDirectly(t, s, drifted.ID, "svc-remapped")

		backfilled, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Equal(t, 1, backfilled)
		again, err := s.BackfillAgentVersions()
		require.NoError(t, err)
		assert.Zero(t, again)

		versions, err := listAllAgentVersions(s, drifted.ID)
		require.NoError(t, err)
		require.Equal(t, []int{2, 1}, versionNumbers(versions))
		assert.Equal(t, AgentVersionSourceSystem, versions[0].Source)
		assert.Equal(t, []string{"serviceID"}, versions[0].ChangedFields)

		inSyncVersions, err := listAllAgentVersions(s, inSync.ID)
		require.NoError(t, err)
		assert.Equal(t, []int{1}, versionNumbers(inSyncVersions))
	})
}

func TestBackfillAgentVersionsContinuesPastFailures(t *testing.T) {
	s := setupVersionTestStore(t)
	const broken = "legacyagent000000000000001"
	insertUnversionedAgent(t, s, broken, "legacy-broken", "", 0)
	insertUnversionedAgent(t, s, "legacyagent000000000000002", "legacy-two", "", 0)
	insertUnversionedAgent(t, s, "legacyagent000000000000003", "legacy-three", "", 0)
	_, err := s.db.Exec(`UPDATE Agents_UserAgents SET ChannelIDs = 'not json' WHERE ID = $1`, broken)
	require.NoError(t, err)

	backfilled, err := s.BackfillAgentVersions()
	require.Error(t, err)
	assert.Contains(t, err.Error(), broken)
	assert.Equal(t, 2, backfilled, "agents after the failing one are still backfilled")

	for _, id := range []string{"legacyagent000000000000002", "legacyagent000000000000003"} {
		versions, listErr := listAllAgentVersions(s, id)
		require.NoError(t, listErr)
		assert.Equal(t, []int{1}, versionNumbers(versions))
	}
	brokenVersions, err := listAllAgentVersions(s, broken)
	require.NoError(t, err)
	assert.Empty(t, brokenVersions)
}
