// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package customprompts

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/mattermost/mattermost-plugin-agents/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const MaxPromptNameLength = 64

// CustomPrompt represents a user-created prompt template
type CustomPrompt struct {
	ID          string `json:"id" db:"id"`
	CreatorID   string `json:"creator_id" db:"creatorid"`
	Name        string `json:"name" db:"name"`
	Description string `json:"description" db:"description"`
	Template    string `json:"template" db:"template"`
	IsShared    bool   `json:"is_shared" db:"isshared"`
	CreatedAt   int64  `json:"created_at" db:"createdat"`
	UpdatedAt   int64  `json:"updated_at" db:"updatedat"`
	DeletedAt   int64  `json:"deleted_at" db:"deletedat"`
}

// Validate checks that required fields are present and within limits.
func (p *CustomPrompt) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len([]rune(p.Name)) > MaxPromptNameLength {
		return fmt.Errorf("name must be at most %d characters", MaxPromptNameLength)
	}
	if p.Template == "" {
		return fmt.Errorf("template is required")
	}
	return nil
}

// Store provides access to the custom prompts database tables
type Store struct {
	db *mmapi.DBClient
}

// NewStore creates a new custom prompts store
func NewStore(db *mmapi.DBClient) *Store {
	return &Store{db: db}
}

// Create inserts a new custom prompt into the database
func (s *Store) Create(prompt CustomPrompt) (CustomPrompt, error) {
	prompt.ID = model.NewId()
	now := model.GetMillis()
	prompt.CreatedAt = now
	prompt.UpdatedAt = now
	prompt.DeletedAt = 0

	_, err := s.db.ExecBuilder(s.db.Builder().
		Insert("LLM_CustomPrompts").
		Columns("ID", "CreatorID", "Name", "Description", "Template", "IsShared", "CreatedAt", "UpdatedAt", "DeletedAt").
		Values(prompt.ID, prompt.CreatorID, prompt.Name, prompt.Description, prompt.Template, prompt.IsShared, prompt.CreatedAt, prompt.UpdatedAt, prompt.DeletedAt))
	if err != nil {
		return CustomPrompt{}, fmt.Errorf("failed to create custom prompt: %w", err)
	}

	return prompt, nil
}

// Get retrieves a custom prompt by ID, excluding soft-deleted prompts
func (s *Store) Get(id string) (CustomPrompt, error) {
	var prompts []CustomPrompt
	if err := s.db.DoQuery(&prompts, s.db.Builder().
		Select("ID", "CreatorID", "Name", "Description", "Template", "IsShared", "CreatedAt", "UpdatedAt", "DeletedAt").
		From("LLM_CustomPrompts").
		Where(sq.Eq{"ID": id}).
		Where(sq.Eq{"DeletedAt": 0}),
	); err != nil {
		return CustomPrompt{}, fmt.Errorf("failed to get custom prompt: %w", err)
	}

	if len(prompts) == 0 {
		return CustomPrompt{}, fmt.Errorf("custom prompt not found")
	}

	return prompts[0], nil
}

// Update modifies an existing custom prompt. Only the creator can update their prompt.
func (s *Store) Update(prompt CustomPrompt) error {
	prompt.UpdatedAt = model.GetMillis()

	result, err := s.db.ExecBuilder(s.db.Builder().
		Update("LLM_CustomPrompts").
		Set("Name", prompt.Name).
		Set("Description", prompt.Description).
		Set("Template", prompt.Template).
		Set("IsShared", prompt.IsShared).
		Set("UpdatedAt", prompt.UpdatedAt).
		Where(sq.Eq{"ID": prompt.ID}).
		Where(sq.Eq{"CreatorID": prompt.CreatorID}).
		Where(sq.Eq{"DeletedAt": 0}))
	if err != nil {
		return fmt.Errorf("failed to update custom prompt: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("custom prompt not found or not owned by user")
	}

	return nil
}

// Delete soft-deletes a custom prompt. Only the creator can delete their prompt.
func (s *Store) Delete(id string, userID string) error {
	now := model.GetMillis()

	result, err := s.db.ExecBuilder(s.db.Builder().
		Update("LLM_CustomPrompts").
		Set("DeletedAt", now).
		Where(sq.Eq{"ID": id}).
		Where(sq.Eq{"CreatorID": userID}).
		Where(sq.Eq{"DeletedAt": 0}))
	if err != nil {
		return fmt.Errorf("failed to delete custom prompt: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("custom prompt not found or not owned by user")
	}

	return nil
}

// ListForUser returns all prompts visible to a user: their own prompts and shared prompts from others.
func (s *Store) ListForUser(userID string) ([]CustomPrompt, error) {
	var prompts []CustomPrompt
	if err := s.db.DoQuery(&prompts, s.db.Builder().
		Select("ID", "CreatorID", "Name", "Description", "Template", "IsShared", "CreatedAt", "UpdatedAt", "DeletedAt").
		From("LLM_CustomPrompts").
		Where(sq.Eq{"DeletedAt": 0}).
		Where(sq.Or{
			sq.Eq{"CreatorID": userID},
			sq.Eq{"IsShared": true},
		}).
		OrderBy("Name"),
	); err != nil {
		return nil, fmt.Errorf("failed to list custom prompts: %w", err)
	}

	if prompts == nil {
		prompts = []CustomPrompt{}
	}

	return prompts, nil
}

// GetPinnedForUser returns all pinned prompts for a user, excluding soft-deleted prompts.
func (s *Store) GetPinnedForUser(userID string) ([]CustomPrompt, error) {
	var prompts []CustomPrompt
	if err := s.db.DoQuery(&prompts, s.db.Builder().
		Select("p.ID", "p.CreatorID", "p.Name", "p.Description", "p.Template", "p.IsShared", "p.CreatedAt", "p.UpdatedAt", "p.DeletedAt").
		From("LLM_CustomPrompts AS p").
		Join("LLM_CustomPromptPins AS pin ON pin.PromptID = p.ID").
		Where(sq.Eq{"pin.UserID": userID}).
		Where(sq.Eq{"p.DeletedAt": 0}).
		Where(sq.Or{
			sq.Eq{"p.CreatorID": userID},
			sq.Eq{"p.IsShared": true},
		}).
		OrderBy("p.Name"),
	); err != nil {
		return nil, fmt.Errorf("failed to get pinned prompts: %w", err)
	}

	if prompts == nil {
		prompts = []CustomPrompt{}
	}

	return prompts, nil
}

// SetPinned pins or unpins a prompt for a user.
func (s *Store) SetPinned(userID, promptID string, pinned bool) error {
	if pinned {
		// Verify the prompt exists, is not deleted, and is visible to the user
		var visible []CustomPrompt
		if err := s.db.DoQuery(&visible, s.db.Builder().
			Select("ID").
			From("LLM_CustomPrompts").
			Where(sq.Eq{"ID": promptID}).
			Where(sq.Eq{"DeletedAt": 0}).
			Where(sq.Or{
				sq.Eq{"CreatorID": userID},
				sq.Eq{"IsShared": true},
			}),
		); err != nil {
			return fmt.Errorf("failed to verify prompt visibility: %w", err)
		}
		if len(visible) == 0 {
			return fmt.Errorf("prompt not found or not accessible")
		}

		_, err := s.db.ExecBuilder(s.db.Builder().
			Insert("LLM_CustomPromptPins").
			Columns("UserID", "PromptID").
			Values(userID, promptID).
			Suffix("ON CONFLICT (UserID, PromptID) DO NOTHING"))
		if err != nil {
			return fmt.Errorf("failed to pin prompt: %w", err)
		}
	} else {
		_, err := s.db.ExecBuilder(s.db.Builder().
			Delete("LLM_CustomPromptPins").
			Where(sq.Eq{"UserID": userID}).
			Where(sq.Eq{"PromptID": promptID}))
		if err != nil {
			return fmt.Errorf("failed to unpin prompt: %w", err)
		}
	}

	return nil
}

// GetPinnedIDs returns the IDs of all prompts pinned by a user.
func (s *Store) GetPinnedIDs(userID string) ([]string, error) {
	type pinRow struct {
		PromptID string `db:"promptid"`
	}

	var rows []pinRow
	if err := s.db.DoQuery(&rows, s.db.Builder().
		Select("pin.PromptID").
		From("LLM_CustomPromptPins AS pin").
		Join("LLM_CustomPrompts AS p ON p.ID = pin.PromptID").
		Where(sq.Eq{"pin.UserID": userID}).
		Where(sq.Eq{"p.DeletedAt": 0}).
		Where(sq.Or{
			sq.Eq{"p.CreatorID": userID},
			sq.Eq{"p.IsShared": true},
		}),
	); err != nil {
		return nil, fmt.Errorf("failed to get pinned prompt IDs: %w", err)
	}

	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.PromptID
	}

	return ids, nil
}
