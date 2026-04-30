// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"
	"github.com/mattermost/mattermost/server/public/model"
)

var (
	// ErrConversationNotFound is returned when a conversation lookup finds no matching row.
	ErrConversationNotFound = errors.New("conversation not found")

	// ErrConversationConflict is returned when creating a conversation violates
	// the unique index on (RootPostID, BotID).
	ErrConversationConflict = errors.New("conversation already exists for this thread and bot")
)

// Conversation represents a first-class conversation entity stored in LLM_Conversations.
type Conversation struct {
	ID           string  `json:"id"            db:"id"`
	UserID       string  `json:"user_id"       db:"userid"`
	BotID        string  `json:"bot_id"        db:"botid"`
	ChannelID    *string `json:"channel_id"    db:"channelid"`
	RootPostID   *string `json:"root_post_id"  db:"rootpostid"`
	Title        string  `json:"title"         db:"title"`
	SystemPrompt string  `json:"system_prompt" db:"systemprompt"`
	Operation    string  `json:"operation"     db:"operation"`
	CreatedAt    int64   `json:"created_at"    db:"createdat"`
	UpdatedAt    int64   `json:"updated_at"    db:"updatedat"`
	DeleteAt     int64   `json:"delete_at"     db:"deleteat"`
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}

var conversationColumns = []string{
	"ID", "UserID", "BotID", "ChannelID", "RootPostID",
	"Title", "SystemPrompt", "Operation",
	"CreatedAt", "UpdatedAt", "DeleteAt",
}

// CreateConversation inserts a new conversation row.
// The caller must set ID, UserID, BotID, CreatedAt, and UpdatedAt before calling.
func (s *Store) CreateConversation(conv *Conversation) error {
	query, args, err := s.builder.Insert("LLM_Conversations").
		Columns(conversationColumns...).
		Values(conv.ID, conv.UserID, conv.BotID, conv.ChannelID, conv.RootPostID,
			conv.Title, conv.SystemPrompt, conv.Operation,
			conv.CreatedAt, conv.UpdatedAt, conv.DeleteAt).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build create conversation query: %w", err)
	}
	_, err = s.db.Exec(query, args...)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConversationConflict
		}
		return fmt.Errorf("failed to create conversation: %w", err)
	}
	return nil
}

// GetConversation retrieves a non-deleted conversation by ID.
// Returns ErrConversationNotFound if the conversation does not exist or is soft-deleted.
func (s *Store) GetConversation(id string) (*Conversation, error) {
	query, args, err := s.builder.
		Select(conversationColumns...).
		From("LLM_Conversations").
		Where(sq.Eq{"ID": id}).
		Where(sq.Eq{"DeleteAt": 0}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get conversation query: %w", err)
	}
	var conv Conversation
	if err := s.db.Get(&conv, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversationNotFound
		}
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}
	return &conv, nil
}

// GetConversationByThreadBotUser looks up a non-deleted conversation by
// (RootPostID, BotID, UserID). Returns ErrConversationNotFound when no
// conversation exists for the given tuple.
func (s *Store) GetConversationByThreadBotUser(rootPostID, botID, userID string) (*Conversation, error) {
	query, args, err := s.builder.
		Select(conversationColumns...).
		From("LLM_Conversations").
		Where(sq.Eq{"RootPostID": rootPostID}).
		Where(sq.Eq{"BotID": botID}).
		Where(sq.Eq{"UserID": userID}).
		Where(sq.Eq{"DeleteAt": 0}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get conversation by thread query: %w", err)
	}
	var conv Conversation
	if err := s.db.Get(&conv, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversationNotFound
		}
		return nil, fmt.Errorf("failed to get conversation by thread/bot/user: %w", err)
	}
	return &conv, nil
}

// UpdateConversationTitle updates the title and UpdatedAt timestamp of a conversation.
func (s *Store) UpdateConversationTitle(id, title string) error {
	query, args, err := s.builder.
		Update("LLM_Conversations").
		Set("Title", title).
		Set("UpdatedAt", model.GetMillis()).
		Where(sq.Eq{"ID": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build update title query: %w", err)
	}
	_, err = s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update conversation title: %w", err)
	}
	return nil
}

// UpdateConversationRootPostID sets the RootPostID and updates the UpdatedAt timestamp.
// This is used when the post ID is only known after creation (e.g., thread analysis DM posts).
func (s *Store) UpdateConversationRootPostID(id string, rootPostID string) error {
	query, args, err := s.builder.
		Update("LLM_Conversations").
		Set("RootPostID", rootPostID).
		Set("UpdatedAt", model.GetMillis()).
		Where(sq.Eq{"ID": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build update root post ID query: %w", err)
	}
	_, err = s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update conversation root post ID: %w", err)
	}
	return nil
}

// SoftDeleteConversation sets the DeleteAt timestamp on a conversation.
// Turns are not deleted until CleanupDeletedConversations runs.
func (s *Store) SoftDeleteConversation(id string, deleteAt int64) error {
	query, args, err := s.builder.
		Update("LLM_Conversations").
		Set("DeleteAt", deleteAt).
		Set("UpdatedAt", deleteAt).
		Where(sq.Eq{"ID": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("failed to build soft delete query: %w", err)
	}
	_, err = s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to soft delete conversation: %w", err)
	}
	return nil
}

// ConversationSummary is a lightweight view of a conversation with its turn count,
// used for listing conversations in the RHS threads panel.
type ConversationSummary struct {
	ID         string  `json:"id"           db:"id"`
	UserID     string  `json:"user_id"      db:"userid"`
	BotID      string  `json:"bot_id"       db:"botid"`
	ChannelID  *string `json:"channel_id"   db:"channelid"`
	RootPostID *string `json:"root_post_id" db:"rootpostid"`
	Title      string  `json:"title"        db:"title"`
	TurnCount  int     `json:"turn_count"   db:"turncount"`
	UpdatedAt  int64   `json:"updated_at"   db:"updatedat"`
}

// GetConversationSummariesForUser returns conversations for a user ordered by UpdatedAt DESC,
// including a turn count per conversation. Only non-deleted conversations are returned.
func (s *Store) GetConversationSummariesForUser(userID string, limit, offset int) ([]ConversationSummary, error) {
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}
	query, args, err := s.builder.
		Select(
			"c.ID",
			"c.UserID",
			"c.BotID",
			"c.ChannelID",
			"c.RootPostID",
			"c.Title",
			"COUNT(t.ID) AS TurnCount",
			"c.UpdatedAt",
		).
		From("LLM_Conversations c").
		LeftJoin("LLM_Turns t ON t.ConversationID = c.ID").
		Where(sq.Eq{"c.UserID": userID}).
		Where(sq.Eq{"c.DeleteAt": 0}).
		GroupBy("c.ID", "c.UserID", "c.BotID", "c.ChannelID", "c.RootPostID", "c.Title", "c.UpdatedAt").
		OrderBy("c.UpdatedAt DESC").
		Limit(uint64(limit)).   // #nosec G115 -- guarded above
		Offset(uint64(offset)). // #nosec G115 -- guarded above
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build get conversation summaries query: %w", err)
	}
	var summaries []ConversationSummary
	if err := s.db.Select(&summaries, query, args...); err != nil {
		return nil, fmt.Errorf("failed to get conversation summaries: %w", err)
	}
	if summaries == nil {
		summaries = []ConversationSummary{}
	}
	return summaries, nil
}

// CleanupDeletedConversations permanently deletes all soft-deleted conversations
// and their associated turns within a single transaction.
func (s *Store) CleanupDeletedConversations() error {
	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.Exec("DELETE FROM LLM_Turns WHERE ConversationID IN (SELECT ID FROM LLM_Conversations WHERE DeleteAt > 0)")
	if err != nil {
		return fmt.Errorf("failed to delete turns for deleted conversations: %w", err)
	}
	_, err = tx.Exec("DELETE FROM LLM_Conversations WHERE DeleteAt > 0")
	if err != nil {
		return fmt.Errorf("failed to delete soft-deleted conversations: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit cleanup transaction: %w", err)
	}
	return nil
}
