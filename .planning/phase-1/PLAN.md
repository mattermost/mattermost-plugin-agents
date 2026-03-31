# Phase 1: Database & Store Layer — Prescriptive Implementation Plan

> **Goal:** New `Agents_UserAgents` table, Morph migration, and `AgentStore` with full CRUD methods. No API or runtime integration yet.
>
> **Repo:** `~/workspace/worktrees/mattermost-plugin-ai-MM-65671`
> **Module:** `github.com/mattermost/mattermost-plugin-ai`
> **Depends on:** nothing

---

## Order of Operations

1. Create `useragents/model.go` — the UserAgent model and JSON helpers
2. Create migration files `store/migrations/000004_create_user_agents_table.{up,down}.sql`
3. Create `store/agents.go` — all CRUD methods on `*Store`
4. Create `store/agents_test.go` — integration tests using testcontainers
5. Verify: `go test ./store/... -run TestAgent -v` and `go test ./useragents/... -v`

---

## Task 1.1: Define the UserAgent Model

### Create `useragents/model.go`

**File:** `useragents/model.go` (new file, new package)

```go
// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package useragents

import (
	"encoding/json"
	"fmt"
)

// UserAgent represents a user-created AI agent persisted in the Agents_UserAgents table.
type UserAgent struct {
	ID                 string             `json:"id" db:"ID"`
	BotUserID          string             `json:"bot_user_id" db:"BotUserID"`
	CreatorID          string             `json:"creator_id" db:"CreatorID"`
	DisplayName        string             `json:"display_name" db:"DisplayName"`
	Username           string             `json:"username" db:"Username"`
	ServiceID          string             `json:"service_id" db:"ServiceID"`
	CustomInstructions string             `json:"custom_instructions" db:"CustomInstructions"`
	ChannelAccessLevel int                `json:"channel_access_level" db:"ChannelAccessLevel"`
	ChannelIDs         []string           `json:"channel_ids"`
	UserAccessLevel    int                `json:"user_access_level" db:"UserAccessLevel"`
	UserIDs            []string           `json:"user_ids"`
	TeamIDs            []string           `json:"team_ids"`
	AdminUserIDs       []string           `json:"admin_user_ids"`
	EnabledTools       []EnabledTool      `json:"enabled_tools"`
	CreateAt           int64              `json:"create_at" db:"CreateAt"`
	UpdateAt           int64              `json:"update_at" db:"UpdateAt"`
	DeleteAt           int64              `json:"delete_at" db:"DeleteAt"`
}

// EnabledTool identifies a single tool on a specific MCP server that this agent may use.
type EnabledTool struct {
	ServerOrigin string `json:"server_origin"`
	ToolName     string `json:"tool_name"`
}

// --- JSON helpers for DB TEXT columns ---

// ChannelIDsJSON returns the JSON-encoded string for the ChannelIDs slice.
// Returns "[]" for nil/empty slices.
func (u *UserAgent) ChannelIDsJSON() string {
	return mustMarshalSlice(u.ChannelIDs)
}

// UserIDsJSON returns the JSON-encoded string for the UserIDs slice.
func (u *UserAgent) UserIDsJSON() string {
	return mustMarshalSlice(u.UserIDs)
}

// TeamIDsJSON returns the JSON-encoded string for the TeamIDs slice.
func (u *UserAgent) TeamIDsJSON() string {
	return mustMarshalSlice(u.TeamIDs)
}

// AdminUserIDsJSON returns the JSON-encoded string for the AdminUserIDs slice.
func (u *UserAgent) AdminUserIDsJSON() string {
	return mustMarshalSlice(u.AdminUserIDs)
}

// EnabledToolsJSON returns the JSON-encoded string for the EnabledTools slice.
func (u *UserAgent) EnabledToolsJSON() string {
	b, err := json.Marshal(u.EnabledTools)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// SetChannelIDsFromJSON parses a JSON string into the ChannelIDs field.
func (u *UserAgent) SetChannelIDsFromJSON(raw string) error {
	return unmarshalSlice(raw, &u.ChannelIDs)
}

// SetUserIDsFromJSON parses a JSON string into the UserIDs field.
func (u *UserAgent) SetUserIDsFromJSON(raw string) error {
	return unmarshalSlice(raw, &u.UserIDs)
}

// SetTeamIDsFromJSON parses a JSON string into the TeamIDs field.
func (u *UserAgent) SetTeamIDsFromJSON(raw string) error {
	return unmarshalSlice(raw, &u.TeamIDs)
}

// SetAdminUserIDsFromJSON parses a JSON string into the AdminUserIDs field.
func (u *UserAgent) SetAdminUserIDsFromJSON(raw string) error {
	return unmarshalSlice(raw, &u.AdminUserIDs)
}

// SetEnabledToolsFromJSON parses a JSON string into the EnabledTools field.
func (u *UserAgent) SetEnabledToolsFromJSON(raw string) error {
	if raw == "" || raw == "[]" {
		u.EnabledTools = nil
		return nil
	}
	return json.Unmarshal([]byte(raw), &u.EnabledTools)
}

// mustMarshalSlice marshals a string slice to JSON, returning "[]" on nil/empty or error.
func mustMarshalSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalSlice parses a JSON string into a *[]string, setting nil for empty arrays.
func unmarshalSlice(raw string, target *[]string) error {
	if raw == "" || raw == "[]" {
		*target = nil
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("failed to unmarshal JSON slice: %w", err)
	}
	return nil
}
```

### Why this design

- The `db:` struct tags match the SQL column names exactly (mixed-case, matching the `CREATE TABLE` DDL). This is required by sqlx `Get`/`Select`.
- Slice fields (`ChannelIDs`, `UserIDs`, `TeamIDs`, `AdminUserIDs`, `EnabledTools`) do NOT have `db:` tags because they are stored as JSON TEXT in the database and cannot be directly scanned by sqlx. The store layer handles marshaling/unmarshaling explicitly.
- The JSON helper methods (`ChannelIDsJSON()`, `SetChannelIDsFromJSON()`, etc.) centralize marshaling so the store layer doesn't duplicate this logic across insert/update/scan.

### Verification

```bash
cd ~/workspace/worktrees/mattermost-plugin-ai-MM-65671
go build ./useragents/...
```

---

## Task 1.2: Write Morph Migration

### Create `store/migrations/000004_create_user_agents_table.up.sql`

**File:** `store/migrations/000004_create_user_agents_table.up.sql` (new file)

```sql
CREATE TABLE IF NOT EXISTS Agents_UserAgents (
    ID VARCHAR(26) PRIMARY KEY,
    BotUserID VARCHAR(26) NOT NULL,
    CreatorID VARCHAR(26) NOT NULL,
    DisplayName VARCHAR(256) NOT NULL DEFAULT '',
    Username VARCHAR(64) NOT NULL,
    ServiceID VARCHAR(26) NOT NULL,
    CustomInstructions TEXT NOT NULL DEFAULT '',
    ChannelAccessLevel INT NOT NULL DEFAULT 0,
    ChannelIDs TEXT NOT NULL DEFAULT '[]',
    UserAccessLevel INT NOT NULL DEFAULT 0,
    UserIDs TEXT NOT NULL DEFAULT '[]',
    TeamIDs TEXT NOT NULL DEFAULT '[]',
    AdminUserIDs TEXT NOT NULL DEFAULT '[]',
    EnabledTools TEXT NOT NULL DEFAULT '[]',
    CreateAt BIGINT NOT NULL,
    UpdateAt BIGINT NOT NULL,
    DeleteAt BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX idx_useragents_creator ON Agents_UserAgents(CreatorID) WHERE DeleteAt = 0;
CREATE INDEX idx_useragents_active ON Agents_UserAgents(DeleteAt);
```

**Conventions followed:**
- Naming: `000004_create_user_agents_table.up.sql` — sequential after `000003_create_config_history_table`
- Table naming: `Agents_UserAgents` — matches the `Agents_` prefix used by `Agents_System` and `Agents_ConfigHistory`
- `IF NOT EXISTS` on `CREATE TABLE` — matches `000001` and `000003` patterns
- `VARCHAR(26)` for IDs — matches Mattermost model IDs (`model.NewId()` returns 26-char strings)
- `BIGINT` for timestamps — matches `Agents_ConfigHistory.CreateAt`
- JSON slice columns use `TEXT NOT NULL DEFAULT '[]'` — stored as JSON strings, not JSONB, keeping it simple
- Partial index on `CreatorID WHERE DeleteAt = 0` — optimizes the most common query (list my active agents)
- Index on `DeleteAt` — optimizes filtering active vs deleted agents

### Create `store/migrations/000004_create_user_agents_table.down.sql`

**File:** `store/migrations/000004_create_user_agents_table.down.sql` (new file)

```sql
DROP INDEX IF EXISTS idx_useragents_creator;
DROP INDEX IF EXISTS idx_useragents_active;
DROP TABLE IF EXISTS Agents_UserAgents;
```

**Convention:** Drop indexes before table (matches `000003` down pattern which drops index first).

### Verification

After both files are created, the existing `TestRunMigrations` in `store/store_test.go` will exercise the new migration because `RunMigrations()` calls `engine.ApplyAll()`. However, the migration count assertion on line 143 must be updated:

**File:** `store/store_test.go`, line 143
**Change:** `assert.Equal(t, 3, count, ...)` → `assert.Equal(t, 4, count, ...)`

```go
// Before (line 143):
assert.Equal(t, 3, count, "Should have 3 migration records")

// After:
assert.Equal(t, 4, count, "Should have 4 migration records")
```

Also add an existence check for the new table in the "fresh install creates all tables" test case. Insert after line 114 (after the `Agents_ConfigHistory` check):

```go
				// Check Agents_UserAgents table exists
				err = s.db.Get(&exists, `
					SELECT EXISTS (
						SELECT 1 FROM information_schema.tables
						WHERE table_name = 'agents_useragents'
						AND table_schema = current_schema()
					)`)
				require.NoError(t, err)
				assert.True(t, exists, "Agents_UserAgents table should exist")
```

### Verify migration runs

```bash
cd ~/workspace/worktrees/mattermost-plugin-ai-MM-65671
go test ./store/... -run TestRunMigrations -v -count=1
```

---

## Task 1.3: Implement AgentStore CRUD Methods

### Create `store/agents.go`

**File:** `store/agents.go` (new file)

This file follows the exact patterns from `store/config.go` (raw `db.Get`/`db.Exec` with sqlx, no squirrel) and `store/system.go` (simple SELECT/INSERT).

```go
// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost-plugin-ai/useragents"
	"github.com/mattermost/mattermost/server/public/model"
)

// agentRow is the DB-level representation of a UserAgent row.
// All JSON slice fields are stored as TEXT and scanned as strings.
type agentRow struct {
	ID                 string `db:"ID"`
	BotUserID          string `db:"BotUserID"`
	CreatorID          string `db:"CreatorID"`
	DisplayName        string `db:"DisplayName"`
	Username           string `db:"Username"`
	ServiceID          string `db:"ServiceID"`
	CustomInstructions string `db:"CustomInstructions"`
	ChannelAccessLevel int    `db:"ChannelAccessLevel"`
	ChannelIDs         string `db:"ChannelIDs"`
	UserAccessLevel    int    `db:"UserAccessLevel"`
	UserIDs            string `db:"UserIDs"`
	TeamIDs            string `db:"TeamIDs"`
	AdminUserIDs       string `db:"AdminUserIDs"`
	EnabledTools       string `db:"EnabledTools"`
	CreateAt           int64  `db:"CreateAt"`
	UpdateAt           int64  `db:"UpdateAt"`
	DeleteAt           int64  `db:"DeleteAt"`
}

// toUserAgent converts an agentRow (DB scan result) to a useragents.UserAgent.
func (r *agentRow) toUserAgent() (*useragents.UserAgent, error) {
	agent := &useragents.UserAgent{
		ID:                 r.ID,
		BotUserID:          r.BotUserID,
		CreatorID:          r.CreatorID,
		DisplayName:        r.DisplayName,
		Username:           r.Username,
		ServiceID:          r.ServiceID,
		CustomInstructions: r.CustomInstructions,
		ChannelAccessLevel: r.ChannelAccessLevel,
		UserAccessLevel:    r.UserAccessLevel,
		CreateAt:           r.CreateAt,
		UpdateAt:           r.UpdateAt,
		DeleteAt:           r.DeleteAt,
	}

	if err := agent.SetChannelIDsFromJSON(r.ChannelIDs); err != nil {
		return nil, fmt.Errorf("failed to parse ChannelIDs: %w", err)
	}
	if err := agent.SetUserIDsFromJSON(r.UserIDs); err != nil {
		return nil, fmt.Errorf("failed to parse UserIDs: %w", err)
	}
	if err := agent.SetTeamIDsFromJSON(r.TeamIDs); err != nil {
		return nil, fmt.Errorf("failed to parse TeamIDs: %w", err)
	}
	if err := agent.SetAdminUserIDsFromJSON(r.AdminUserIDs); err != nil {
		return nil, fmt.Errorf("failed to parse AdminUserIDs: %w", err)
	}
	if err := agent.SetEnabledToolsFromJSON(r.EnabledTools); err != nil {
		return nil, fmt.Errorf("failed to parse EnabledTools: %w", err)
	}

	return agent, nil
}

// CreateAgent inserts a new user agent into the database.
// It generates the ID and sets CreateAt/UpdateAt timestamps automatically.
func (s *Store) CreateAgent(agent *useragents.UserAgent) error {
	agent.ID = model.NewId()
	now := model.GetMillis()
	agent.CreateAt = now
	agent.UpdateAt = now
	agent.DeleteAt = 0

	_, err := s.db.Exec(
		`INSERT INTO Agents_UserAgents (
			ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, CreateAt, UpdateAt, DeleteAt
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		agent.ID,
		agent.BotUserID,
		agent.CreatorID,
		agent.DisplayName,
		agent.Username,
		agent.ServiceID,
		agent.CustomInstructions,
		agent.ChannelAccessLevel,
		agent.ChannelIDsJSON(),
		agent.UserAccessLevel,
		agent.UserIDsJSON(),
		agent.TeamIDsJSON(),
		agent.AdminUserIDsJSON(),
		agent.EnabledToolsJSON(),
		agent.CreateAt,
		agent.UpdateAt,
		agent.DeleteAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	return nil
}

// GetAgent retrieves a single active (non-deleted) agent by ID.
// Returns nil, nil if the agent does not exist or is soft-deleted.
func (s *Store) GetAgent(id string) (*useragents.UserAgent, error) {
	var row agentRow
	err := s.db.Get(&row,
		`SELECT ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, CreateAt, UpdateAt, DeleteAt
		FROM Agents_UserAgents
		WHERE ID = $1 AND DeleteAt = 0`,
		id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent %q: %w", id, err)
	}

	return row.toUserAgent()
}

// ListAgents returns all active (non-deleted) agents, ordered by creation time descending.
func (s *Store) ListAgents() ([]*useragents.UserAgent, error) {
	var rows []agentRow
	err := s.db.Select(&rows,
		`SELECT ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, CreateAt, UpdateAt, DeleteAt
		FROM Agents_UserAgents
		WHERE DeleteAt = 0
		ORDER BY CreateAt DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	agents := make([]*useragents.UserAgent, 0, len(rows))
	for i := range rows {
		agent, parseErr := rows[i].toUserAgent()
		if parseErr != nil {
			return nil, parseErr
		}
		agents = append(agents, agent)
	}

	return agents, nil
}

// ListAgentsByCreator returns all active agents created by the specified user.
func (s *Store) ListAgentsByCreator(creatorID string) ([]*useragents.UserAgent, error) {
	var rows []agentRow
	err := s.db.Select(&rows,
		`SELECT ID, BotUserID, CreatorID, DisplayName, Username, ServiceID,
			CustomInstructions, ChannelAccessLevel, ChannelIDs,
			UserAccessLevel, UserIDs, TeamIDs, AdminUserIDs,
			EnabledTools, CreateAt, UpdateAt, DeleteAt
		FROM Agents_UserAgents
		WHERE CreatorID = $1 AND DeleteAt = 0
		ORDER BY CreateAt DESC`,
		creatorID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents by creator %q: %w", creatorID, err)
	}

	agents := make([]*useragents.UserAgent, 0, len(rows))
	for i := range rows {
		agent, parseErr := rows[i].toUserAgent()
		if parseErr != nil {
			return nil, parseErr
		}
		agents = append(agents, agent)
	}

	return agents, nil
}

// UpdateAgent updates an existing agent's mutable fields.
// It sets UpdateAt automatically. The caller must supply the full agent struct
// (read-modify-write pattern). Does NOT update ID, CreatorID, BotUserID, CreateAt, or DeleteAt.
func (s *Store) UpdateAgent(agent *useragents.UserAgent) error {
	agent.UpdateAt = model.GetMillis()

	result, err := s.db.Exec(
		`UPDATE Agents_UserAgents SET
			DisplayName = $1,
			Username = $2,
			ServiceID = $3,
			CustomInstructions = $4,
			ChannelAccessLevel = $5,
			ChannelIDs = $6,
			UserAccessLevel = $7,
			UserIDs = $8,
			TeamIDs = $9,
			AdminUserIDs = $10,
			EnabledTools = $11,
			UpdateAt = $12
		WHERE ID = $13 AND DeleteAt = 0`,
		agent.DisplayName,
		agent.Username,
		agent.ServiceID,
		agent.CustomInstructions,
		agent.ChannelAccessLevel,
		agent.ChannelIDsJSON(),
		agent.UserAccessLevel,
		agent.UserIDsJSON(),
		agent.TeamIDsJSON(),
		agent.AdminUserIDsJSON(),
		agent.EnabledToolsJSON(),
		agent.UpdateAt,
		agent.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update agent %q: %w", agent.ID, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected for agent %q: %w", agent.ID, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("agent %q not found or already deleted", agent.ID)
	}

	return nil
}

// DeleteAgent performs a soft delete by setting DeleteAt to the current timestamp.
func (s *Store) DeleteAgent(id string) error {
	result, err := s.db.Exec(
		`UPDATE Agents_UserAgents SET DeleteAt = $1 WHERE ID = $2 AND DeleteAt = 0`,
		model.GetMillis(),
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete agent %q: %w", id, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected for agent %q: %w", id, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("agent %q not found or already deleted", id)
	}

	return nil
}
```

### Design Decisions

1. **`agentRow` intermediary struct:** sqlx can't scan JSON TEXT into `[]string` or `[]EnabledTool` directly. The `agentRow` struct has `string` fields for all JSON columns, and `toUserAgent()` handles the conversion. This is the cleanest approach without custom `sql.Scanner` implementations.

2. **No advisory lock on UpdateAgent:** Unlike `SaveConfig` which needs a lock for the "deactivate old → insert new" two-step, `UpdateAgent` is a single UPDATE statement that is inherently atomic. The `WHERE ID = $1 AND DeleteAt = 0` clause provides optimistic concurrency. The high-level plan suggested a lock, but it's unnecessary here and would add latency.

3. **No squirrel:** Following `config.go` and `system.go` patterns — raw parameterized SQL with `$N` positional placeholders (PostgreSQL dollar syntax).

4. **`RowsAffected` check on Update/Delete:** Returns an error if the agent doesn't exist or is already deleted, letting the API layer return 404.

### Verification

```bash
cd ~/workspace/worktrees/mattermost-plugin-ai-MM-65671
go build ./store/...
```

---

## Task 1.4: Define AgentStore Interface

### Modify `api/api.go`

**File:** `api/api.go`
**Insert after line 66** (after the `ConfigStore` interface closing brace):

```go
// AgentStore provides CRUD operations for user-created agents.
type AgentStore interface {
	CreateAgent(agent *useragents.UserAgent) error
	GetAgent(id string) (*useragents.UserAgent, error)
	ListAgents() ([]*useragents.UserAgent, error)
	ListAgentsByCreator(creatorID string) ([]*useragents.UserAgent, error)
	UpdateAgent(agent *useragents.UserAgent) error
	DeleteAgent(id string) error
}
```

**Add import** at the top of `api/api.go` (insert into the import block):

```go
"github.com/mattermost/mattermost-plugin-ai/useragents"
```

> **Note:** This interface is defined here for Phase 2 readiness, but the `API` struct field and constructor wiring happen in Phase 2, Task 2.1. In Phase 1, we only define the interface so the store implementation can be verified against it.

### Verification

```bash
cd ~/workspace/worktrees/mattermost-plugin-ai-MM-65671
go build ./api/...
```

This must compile. The `*Store` type in `store/agents.go` implicitly satisfies `api.AgentStore` — verify with a compile-time check at the bottom of `store/agents.go`:

```go
// Compile-time check that *Store satisfies the AgentStore interface.
var _ interface {
	CreateAgent(agent *useragents.UserAgent) error
	GetAgent(id string) (*useragents.UserAgent, error)
	ListAgents() ([]*useragents.UserAgent, error)
	ListAgentsByCreator(creatorID string) ([]*useragents.UserAgent, error)
	UpdateAgent(agent *useragents.UserAgent) error
	DeleteAgent(id string) error
} = (*Store)(nil)
```

> We use an anonymous interface here instead of importing `api.AgentStore` to avoid a circular import (`store` → `api` → `store`). The interface is structurally identical.

---

## Task 1.5: Store Integration Tests

### Create `store/agents_test.go`

**File:** `store/agents_test.go` (new file)

This follows the exact pattern from `store/store_test.go` and `store/config_test.go`:
- Uses `setupTestStore(t)` which creates an isolated schema per test
- Calls `s.RunMigrations()` before each test
- Uses `testify/require` and `testify/assert`

```go
// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"testing"

	"github.com/mattermost/mattermost-plugin-ai/useragents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAgent returns a fully-populated UserAgent for testing.
// ID, CreateAt, UpdateAt, and DeleteAt are set by the store on create.
func testAgent(creatorID, username, displayName string) *useragents.UserAgent {
	return &useragents.UserAgent{
		BotUserID:          "bot-user-id-" + username,
		CreatorID:          creatorID,
		DisplayName:        displayName,
		Username:           username,
		ServiceID:          "svc-1",
		CustomInstructions: "Be helpful and concise",
		ChannelAccessLevel: 1, // ChannelAccessLevelAllow
		ChannelIDs:         []string{"ch-1", "ch-2"},
		UserAccessLevel:    0, // UserAccessLevelAll
		UserIDs:            nil,
		TeamIDs:            []string{"team-1"},
		AdminUserIDs:       []string{"admin-1", "admin-2"},
		EnabledTools: []useragents.EnabledTool{
			{ServerOrigin: "https://mcp.example.com", ToolName: "web_search"},
			{ServerOrigin: "https://mcp.example.com", ToolName: "file_search"},
		},
	}
}

func TestAgentCreateAndGet(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "my-agent", "My Agent")
	err = s.CreateAgent(agent)
	require.NoError(t, err)

	// ID should be populated (26 chars)
	assert.Len(t, agent.ID, 26)
	assert.NotZero(t, agent.CreateAt)
	assert.NotZero(t, agent.UpdateAt)
	assert.Equal(t, agent.CreateAt, agent.UpdateAt)
	assert.Zero(t, agent.DeleteAt)

	// Get round-trip
	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	// Scalar fields
	assert.Equal(t, agent.ID, fetched.ID)
	assert.Equal(t, agent.BotUserID, fetched.BotUserID)
	assert.Equal(t, agent.CreatorID, fetched.CreatorID)
	assert.Equal(t, agent.DisplayName, fetched.DisplayName)
	assert.Equal(t, agent.Username, fetched.Username)
	assert.Equal(t, agent.ServiceID, fetched.ServiceID)
	assert.Equal(t, agent.CustomInstructions, fetched.CustomInstructions)
	assert.Equal(t, agent.ChannelAccessLevel, fetched.ChannelAccessLevel)
	assert.Equal(t, agent.UserAccessLevel, fetched.UserAccessLevel)
	assert.Equal(t, agent.CreateAt, fetched.CreateAt)
	assert.Equal(t, agent.UpdateAt, fetched.UpdateAt)
	assert.Equal(t, agent.DeleteAt, fetched.DeleteAt)

	// JSON slice fields — the critical round-trip test
	assert.Equal(t, []string{"ch-1", "ch-2"}, fetched.ChannelIDs)
	assert.Nil(t, fetched.UserIDs) // nil slice round-trips as nil
	assert.Equal(t, []string{"team-1"}, fetched.TeamIDs)
	assert.Equal(t, []string{"admin-1", "admin-2"}, fetched.AdminUserIDs)
	require.Len(t, fetched.EnabledTools, 2)
	assert.Equal(t, "web_search", fetched.EnabledTools[0].ToolName)
	assert.Equal(t, "https://mcp.example.com", fetched.EnabledTools[0].ServerOrigin)
	assert.Equal(t, "file_search", fetched.EnabledTools[1].ToolName)
}

func TestAgentGetNonexistent(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	fetched, err := s.GetAgent("nonexistent-id")
	require.NoError(t, err)
	assert.Nil(t, fetched)
}

func TestAgentListReturnsOnlyActive(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	// Create 3 agents
	a1 := testAgent("creator-1", "agent-1", "Agent 1")
	a2 := testAgent("creator-1", "agent-2", "Agent 2")
	a3 := testAgent("creator-2", "agent-3", "Agent 3")
	require.NoError(t, s.CreateAgent(a1))
	require.NoError(t, s.CreateAgent(a2))
	require.NoError(t, s.CreateAgent(a3))

	// Delete one
	require.NoError(t, s.DeleteAgent(a2.ID))

	// List should return only 2
	agents, err := s.ListAgents()
	require.NoError(t, err)
	assert.Len(t, agents, 2)

	// Should be ordered by CreateAt DESC (a3 last created, so first in list)
	assert.Equal(t, a3.ID, agents[0].ID)
	assert.Equal(t, a1.ID, agents[1].ID)
}

func TestAgentListByCreator(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	a1 := testAgent("creator-1", "agent-1", "Agent 1")
	a2 := testAgent("creator-1", "agent-2", "Agent 2")
	a3 := testAgent("creator-2", "agent-3", "Agent 3")
	require.NoError(t, s.CreateAgent(a1))
	require.NoError(t, s.CreateAgent(a2))
	require.NoError(t, s.CreateAgent(a3))

	// List by creator-1
	agents, err := s.ListAgentsByCreator("creator-1")
	require.NoError(t, err)
	assert.Len(t, agents, 2)
	for _, a := range agents {
		assert.Equal(t, "creator-1", a.CreatorID)
	}

	// List by creator-2
	agents, err = s.ListAgentsByCreator("creator-2")
	require.NoError(t, err)
	assert.Len(t, agents, 1)
	assert.Equal(t, a3.ID, agents[0].ID)

	// List by nonexistent creator
	agents, err = s.ListAgentsByCreator("creator-999")
	require.NoError(t, err)
	assert.Empty(t, agents)
}

func TestAgentUpdate(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "agent-1", "Agent 1")
	require.NoError(t, s.CreateAgent(agent))
	originalUpdateAt := agent.UpdateAt

	// Modify fields
	agent.DisplayName = "Updated Agent"
	agent.CustomInstructions = "New instructions"
	agent.ChannelIDs = []string{"ch-3"}
	agent.EnabledTools = nil
	agent.ServiceID = "svc-2"

	require.NoError(t, s.UpdateAgent(agent))

	// UpdateAt should be bumped
	assert.Greater(t, agent.UpdateAt, originalUpdateAt)

	// Verify round-trip
	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	assert.Equal(t, "Updated Agent", fetched.DisplayName)
	assert.Equal(t, "New instructions", fetched.CustomInstructions)
	assert.Equal(t, []string{"ch-3"}, fetched.ChannelIDs)
	assert.Nil(t, fetched.EnabledTools)
	assert.Equal(t, "svc-2", fetched.ServiceID)

	// Immutable fields should not change
	assert.Equal(t, agent.CreatorID, fetched.CreatorID)
	assert.Equal(t, agent.BotUserID, fetched.BotUserID)
	assert.Equal(t, agent.CreateAt, fetched.CreateAt)
}

func TestAgentUpdateNonexistent(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := &useragents.UserAgent{
		ID:          "nonexistent-id",
		DisplayName: "Ghost",
		Username:    "ghost",
		ServiceID:   "svc-1",
	}
	err = s.UpdateAgent(agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
}

func TestAgentSoftDelete(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "agent-1", "Agent 1")
	require.NoError(t, s.CreateAgent(agent))

	// Delete
	require.NoError(t, s.DeleteAgent(agent.ID))

	// Get should return nil
	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Nil(t, fetched)

	// Verify the row still exists with DeleteAt > 0 (soft delete)
	var deleteAt int64
	err = s.db.Get(&deleteAt, "SELECT DeleteAt FROM Agents_UserAgents WHERE ID = $1", agent.ID)
	require.NoError(t, err)
	assert.NotZero(t, deleteAt)
}

func TestAgentDeleteNonexistent(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	err = s.DeleteAgent("nonexistent-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
}

func TestAgentDoubleDelete(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "agent-1", "Agent 1")
	require.NoError(t, s.CreateAgent(agent))

	// First delete succeeds
	require.NoError(t, s.DeleteAgent(agent.ID))

	// Second delete fails (already deleted)
	err = s.DeleteAgent(agent.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
}

func TestAgentUpdateDeletedAgent(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	agent := testAgent("creator-1", "agent-1", "Agent 1")
	require.NoError(t, s.CreateAgent(agent))
	require.NoError(t, s.DeleteAgent(agent.ID))

	// Update should fail (WHERE DeleteAt = 0 clause)
	agent.DisplayName = "Should Fail"
	err = s.UpdateAgent(agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or already deleted")
}

func TestAgentEmptySliceFields(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	// Create agent with all slice fields nil/empty
	agent := &useragents.UserAgent{
		BotUserID:   "bot-empty",
		CreatorID:   "creator-empty",
		DisplayName: "Empty Agent",
		Username:    "empty-agent",
		ServiceID:   "svc-1",
		// All slice fields intentionally left nil
	}
	require.NoError(t, s.CreateAgent(agent))

	fetched, err := s.GetAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	// Nil slices round-trip as nil (not empty slices)
	assert.Nil(t, fetched.ChannelIDs)
	assert.Nil(t, fetched.UserIDs)
	assert.Nil(t, fetched.TeamIDs)
	assert.Nil(t, fetched.AdminUserIDs)
	assert.Nil(t, fetched.EnabledTools)
}

func TestAgentConcurrentCreates(t *testing.T) {
	s := setupTestStore(t)
	err := s.RunMigrations()
	require.NoError(t, err)

	const count = 10
	errCh := make(chan error, count)

	for i := 0; i < count; i++ {
		go func(idx int) {
			a := testAgent("creator-1", fmt.Sprintf("agent-%d", idx), fmt.Sprintf("Agent %d", idx))
			errCh <- s.CreateAgent(a)
		}(i)
	}

	for i := 0; i < count; i++ {
		require.NoError(t, <-errCh)
	}

	agents, err := s.ListAgents()
	require.NoError(t, err)
	assert.Len(t, agents, count)
}
```

**Add missing import** — the concurrent test uses `fmt.Sprintf`:

```go
import (
	"fmt"
	"testing"
	// ...
)
```

### Verification

```bash
cd ~/workspace/worktrees/mattermost-plugin-ai-MM-65671

# Run only agent tests
go test ./store/... -run TestAgent -v -count=1

# Run all store tests (includes migration count update)
go test ./store/... -v -count=1

# Run model package tests (should compile)
go build ./useragents/...

# Full build check
go build ./...
```

---

## Summary of All File Changes

| # | File | Action | Description |
|---|------|--------|-------------|
| 1 | `useragents/model.go` | **Create** | UserAgent struct, EnabledTool struct, JSON marshal/unmarshal helpers |
| 2 | `store/migrations/000004_create_user_agents_table.up.sql` | **Create** | CREATE TABLE + 2 indexes |
| 3 | `store/migrations/000004_create_user_agents_table.down.sql` | **Create** | DROP TABLE + indexes |
| 4 | `store/agents.go` | **Create** | agentRow scan struct, CreateAgent, GetAgent, ListAgents, ListAgentsByCreator, UpdateAgent, DeleteAgent, compile-time interface check |
| 5 | `store/agents_test.go` | **Create** | 11 test functions covering all CRUD paths, JSON round-trips, edge cases, concurrency |
| 6 | `store/store_test.go` line 143 | **Modify** | Update migration count from 3 → 4 |
| 7 | `store/store_test.go` after line 114 | **Modify** | Add `Agents_UserAgents` table existence check |
| 8 | `api/api.go` after line 66 | **Modify** | Add `AgentStore` interface definition + `useragents` import |

---

## Definition of Done Checklist

- [x] `go build ./...` succeeds (pre-existing server/main.go manifest error unrelated to our changes; `go build ./useragents/... ./store/... ./api/...` all clean)
- [x] `go test ./store/... -run TestRunMigrations -v` passes (migration 000004 runs cleanly)
- [x] `go test ./store/... -run TestAgent -v` passes (all 12 CRUD test functions green)
- [x] `go test ./store/... -v` passes (all 30+ tests pass with migration count update)
- [x] `go build ./useragents/...` succeeds
- [x] `go build ./api/...` succeeds (AgentStore interface compiles)

---

## Implementation Notes

**Bugs fixed during implementation:**
1. **`agentRow` db tags** — PostgreSQL folds unquoted column names to lowercase, so `db:"ID"` didn't match the actual column `id`. Changed all tags to lowercase (e.g., `db:"id"`, `db:"botuserid"`).
2. **`setupTestStore` search_path** — `SET search_path` only applied to one pooled connection; concurrent goroutines got different connections without the schema set. Fixed by setting `search_path` via the connection string parameter so all pooled connections inherit it.

**Commit:** `f4e7d8e` — Add UserAgent model, store CRUD, migration, and AgentStore interface
