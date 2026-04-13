// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package customprompts

import (
	"fmt"
	"os"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/store"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

var rootDSN = "postgres://mmuser:mostest@localhost:5432/postgres?sslmode=disable"

func testDB(t *testing.T) *mmapi.DBClient {
	t.Helper()

	if dsn := os.Getenv("PG_ROOT_DSN"); dsn != "" {
		rootDSN = dsn
	}

	rootDB, err := sqlx.Connect("postgres", rootDSN)
	if err != nil {
		t.Skipf("PostgreSQL not available, skipping integration test: %v", err)
	}
	defer rootDB.Close()

	dbName := fmt.Sprintf("customprompts_test_%d", model.GetMillis())

	_, err = rootDB.Exec("CREATE DATABASE " + dbName)
	require.NoError(t, err, "Failed to create test database")

	testDSN := fmt.Sprintf("postgres://mmuser:mostest@localhost:5432/%s?sslmode=disable", dbName)
	db, err := sqlx.Connect("postgres", testDSN)
	if err != nil {
		rootConn, _ := sqlx.Connect("postgres", rootDSN)
		if rootConn != nil {
			_, _ = rootConn.Exec("DROP DATABASE " + dbName)
			rootConn.Close()
		}
		require.NoError(t, err, "Failed to connect to test database")
	}

	t.Cleanup(func() {
		db.Close()
		rootConn, connErr := sqlx.Connect("postgres", rootDSN)
		if connErr != nil {
			t.Logf("Failed to connect for cleanup: %v", connErr)
			return
		}
		defer rootConn.Close()
		_, _ = rootConn.Exec("DROP DATABASE " + dbName)
	})

	// Run the real morph migrations, same as production
	s := store.New(db)
	err = s.RunMigrations()
	require.NoError(t, err, "Failed to run migrations")

	return mmapi.NewTestDBClient(db)
}

func TestCreateAndGet(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	creatorID := model.NewId()
	prompt := CustomPrompt{
		CreatorID:   creatorID,
		Name:        "Test Prompt",
		Description: "A test prompt",
		Template:    "Hello {{.BotName}}",
		IsShared:    true,
	}

	created, err := store.Create(prompt)
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.NotZero(t, created.CreatedAt)
	require.NotZero(t, created.UpdatedAt)
	require.Zero(t, created.DeletedAt)

	// Verify all fields survive database round-trip
	got, err := store.Get(created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, creatorID, got.CreatorID)
	require.Equal(t, "Test Prompt", got.Name)
	require.Equal(t, "A test prompt", got.Description)
	require.Equal(t, "Hello {{.BotName}}", got.Template)
	require.True(t, got.IsShared)
	require.Equal(t, created.CreatedAt, got.CreatedAt)
	require.Equal(t, created.UpdatedAt, got.UpdatedAt)
	require.Zero(t, got.DeletedAt)
}

func TestCreateAndGetEmptyDescription(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	created, err := store.Create(CustomPrompt{
		CreatorID: model.NewId(),
		Name:      "Minimal",
		Template:  "Template",
	})
	require.NoError(t, err)

	got, err := store.Get(created.ID)
	require.NoError(t, err)
	require.Equal(t, "", got.Description)
}

func TestGetNonExistent(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	_, err := store.Get(model.NewId())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestUpdateOnlyByCreator(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	creatorID := model.NewId()
	otherUserID := model.NewId()

	created, err := store.Create(CustomPrompt{
		CreatorID:   creatorID,
		Name:        "Original Name",
		Description: "Original Description",
		Template:    "Original Template",
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		userID    string
		newName   string
		expectErr bool
	}{
		{
			name:      "creator can update",
			userID:    creatorID,
			newName:   "Updated Name",
			expectErr: false,
		},
		{
			name:      "other user cannot update",
			userID:    otherUserID,
			newName:   "Hacked Name",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := store.Update(CustomPrompt{
				ID:        created.ID,
				CreatorID: tc.userID,
				Name:      tc.newName,
				Template:  "Updated Template",
			})
			if tc.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "not found or not owned")
			} else {
				require.NoError(t, err)

				got, getErr := store.Get(created.ID)
				require.NoError(t, getErr)
				require.Equal(t, tc.newName, got.Name)
			}
		})
	}
}

func TestDeleteOnlyByCreator(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	creatorID := model.NewId()
	otherUserID := model.NewId()

	created, err := store.Create(CustomPrompt{
		CreatorID: creatorID,
		Name:      "To Be Deleted",
		Template:  "Template",
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		userID    string
		expectErr bool
	}{
		{
			name:      "other user cannot delete",
			userID:    otherUserID,
			expectErr: true,
		},
		{
			name:      "creator can delete",
			userID:    creatorID,
			expectErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := store.Delete(created.ID, tc.userID)
			if tc.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "not found or not owned")
			} else {
				require.NoError(t, err)

				// Verify it's no longer retrievable
				_, getErr := store.Get(created.ID)
				require.Error(t, getErr)
				require.Contains(t, getErr.Error(), "not found")
			}
		})
	}
}

func TestListForUser(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	userA := model.NewId()
	userB := model.NewId()

	// Create user A's private prompt
	_, err := store.Create(CustomPrompt{
		CreatorID: userA,
		Name:      "A Private",
		Template:  "Template A",
		IsShared:  false,
	})
	require.NoError(t, err)

	// Create user A's shared prompt
	_, err = store.Create(CustomPrompt{
		CreatorID: userA,
		Name:      "A Shared",
		Template:  "Template A Shared",
		IsShared:  true,
	})
	require.NoError(t, err)

	// Create user B's private prompt
	_, err = store.Create(CustomPrompt{
		CreatorID: userB,
		Name:      "B Private",
		Template:  "Template B",
		IsShared:  false,
	})
	require.NoError(t, err)

	// Create user B's shared prompt
	_, err = store.Create(CustomPrompt{
		CreatorID: userB,
		Name:      "B Shared",
		Template:  "Template B Shared",
		IsShared:  true,
	})
	require.NoError(t, err)

	tests := []struct {
		name          string
		userID        string
		expectedNames []string
	}{
		{
			name:   "user A sees own and shared",
			userID: userA,
			// Ordered by Name: "A Private", "A Shared", "B Shared"
			expectedNames: []string{"A Private", "A Shared", "B Shared"},
		},
		{
			name:   "user B sees own and shared",
			userID: userB,
			// Ordered by Name: "A Shared", "B Private", "B Shared"
			expectedNames: []string{"A Shared", "B Private", "B Shared"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompts, listErr := store.ListForUser(tc.userID)
			require.NoError(t, listErr)
			require.Len(t, prompts, len(tc.expectedNames))

			for i, name := range tc.expectedNames {
				require.Equal(t, name, prompts[i].Name)
			}
		})
	}
}

func TestListForUserExcludesSoftDeleted(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	userID := model.NewId()

	created, err := store.Create(CustomPrompt{
		CreatorID: userID,
		Name:      "Will Delete",
		Template:  "Template",
	})
	require.NoError(t, err)

	_, err = store.Create(CustomPrompt{
		CreatorID: userID,
		Name:      "Will Keep",
		Template:  "Template",
	})
	require.NoError(t, err)

	err = store.Delete(created.ID, userID)
	require.NoError(t, err)

	prompts, err := store.ListForUser(userID)
	require.NoError(t, err)
	require.Len(t, prompts, 1)
	require.Equal(t, "Will Keep", prompts[0].Name)
}

func TestPinUnpin(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	userID := model.NewId()

	p1, err := store.Create(CustomPrompt{
		CreatorID: userID,
		Name:      "Prompt 1",
		Template:  "Template 1",
	})
	require.NoError(t, err)

	p2, err := store.Create(CustomPrompt{
		CreatorID: userID,
		Name:      "Prompt 2",
		Template:  "Template 2",
	})
	require.NoError(t, err)

	// Pin both
	err = store.SetPinned(userID, p1.ID, true)
	require.NoError(t, err)

	err = store.SetPinned(userID, p2.ID, true)
	require.NoError(t, err)

	// Verify pinned IDs
	pinnedIDs, err := store.GetPinnedIDs(userID)
	require.NoError(t, err)
	require.Len(t, pinnedIDs, 2)
	require.ElementsMatch(t, []string{p1.ID, p2.ID}, pinnedIDs)

	// Unpin one
	err = store.SetPinned(userID, p1.ID, false)
	require.NoError(t, err)

	pinnedIDs, err = store.GetPinnedIDs(userID)
	require.NoError(t, err)
	require.Len(t, pinnedIDs, 1)
	require.Equal(t, p2.ID, pinnedIDs[0])

	// Pin the same one again (idempotent)
	err = store.SetPinned(userID, p2.ID, true)
	require.NoError(t, err)

	pinnedIDs, err = store.GetPinnedIDs(userID)
	require.NoError(t, err)
	require.Len(t, pinnedIDs, 1)
}

func TestGetPinnedForUser(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	userA := model.NewId()
	userB := model.NewId()

	p1, err := store.Create(CustomPrompt{
		CreatorID: userA,
		Name:      "Prompt 1",
		Template:  "Template 1",
		IsShared:  true,
	})
	require.NoError(t, err)

	p2, err := store.Create(CustomPrompt{
		CreatorID: userA,
		Name:      "Prompt 2",
		Template:  "Template 2",
	})
	require.NoError(t, err)

	// User A pins both
	err = store.SetPinned(userA, p1.ID, true)
	require.NoError(t, err)
	err = store.SetPinned(userA, p2.ID, true)
	require.NoError(t, err)

	// User B pins only p1
	err = store.SetPinned(userB, p1.ID, true)
	require.NoError(t, err)

	tests := []struct {
		name          string
		userID        string
		expectedCount int
		expectedNames []string
	}{
		{
			name:          "user A has 2 pinned",
			userID:        userA,
			expectedCount: 2,
			expectedNames: []string{"Prompt 1", "Prompt 2"},
		},
		{
			name:          "user B has 1 pinned",
			userID:        userB,
			expectedCount: 1,
			expectedNames: []string{"Prompt 1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pinned, pinnedErr := store.GetPinnedForUser(tc.userID)
			require.NoError(t, pinnedErr)
			require.Len(t, pinned, tc.expectedCount)

			names := make([]string, len(pinned))
			for i, p := range pinned {
				names[i] = p.Name
			}
			require.ElementsMatch(t, tc.expectedNames, names)
		})
	}
}

func TestGetPinnedForUserExcludesSoftDeleted(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	userID := model.NewId()

	p1, err := store.Create(CustomPrompt{
		CreatorID: userID,
		Name:      "Prompt to Delete",
		Template:  "Template",
	})
	require.NoError(t, err)

	p2, err := store.Create(CustomPrompt{
		CreatorID: userID,
		Name:      "Prompt to Keep",
		Template:  "Template",
	})
	require.NoError(t, err)

	// Pin both
	err = store.SetPinned(userID, p1.ID, true)
	require.NoError(t, err)
	err = store.SetPinned(userID, p2.ID, true)
	require.NoError(t, err)

	// Soft-delete p1
	err = store.Delete(p1.ID, userID)
	require.NoError(t, err)

	// Only p2 should be returned as pinned
	pinned, err := store.GetPinnedForUser(userID)
	require.NoError(t, err)
	require.Len(t, pinned, 1)
	require.Equal(t, "Prompt to Keep", pinned[0].Name)
}

func TestUpdateNonExistent(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	err := store.Update(CustomPrompt{
		ID:        model.NewId(),
		CreatorID: model.NewId(),
		Name:      "Ghost",
		Template:  "Template",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found or not owned")
}

func TestDeleteAlreadyDeleted(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	userID := model.NewId()
	created, err := store.Create(CustomPrompt{
		CreatorID: userID,
		Name:      "Double Delete",
		Template:  "Template",
	})
	require.NoError(t, err)

	err = store.Delete(created.ID, userID)
	require.NoError(t, err)

	// Second delete should fail — row has DeletedAt != 0
	err = store.Delete(created.ID, userID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found or not owned")
}

func TestListForUserEmpty(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	prompts, err := store.ListForUser(model.NewId())
	require.NoError(t, err)
	require.NotNil(t, prompts)
	require.Empty(t, prompts)
}

func TestGetPinnedIDsExcludesDeletedPrompts(t *testing.T) {
	dbClient := testDB(t)
	store := NewStore(dbClient)

	userID := model.NewId()
	created, err := store.Create(CustomPrompt{
		CreatorID: userID,
		Name:      "Will Delete",
		Template:  "Template",
	})
	require.NoError(t, err)

	err = store.SetPinned(userID, created.ID, true)
	require.NoError(t, err)

	err = store.Delete(created.ID, userID)
	require.NoError(t, err)

	// GetPinnedIDs filters out soft-deleted prompts
	pinnedIDs, err := store.GetPinnedIDs(userID)
	require.NoError(t, err)
	require.Empty(t, pinnedIDs, "GetPinnedIDs should exclude deleted prompts")

	// GetPinnedForUser also excludes soft-deleted prompts
	pinned, err := store.GetPinnedForUser(userID)
	require.NoError(t, err)
	require.Empty(t, pinned)
}
