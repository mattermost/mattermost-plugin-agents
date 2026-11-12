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
	if pqErr, ok := errors.AsType[*pq.Error](err); ok {
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
	err := s.execBuilder(s.builder.Insert("LLM_Conversations").
		Columns(conversationColumns...).
		Values(conv.ID, conv.UserID, conv.BotID, conv.ChannelID, conv.RootPostID,
			conv.Title, conv.SystemPrompt, conv.Operation,
			conv.CreatedAt, conv.UpdatedAt, conv.DeleteAt),
		"create conversation")
	if isUniqueViolation(err) {
		return ErrConversationConflict
	}
	return err
}

// GetConversation retrieves a non-deleted conversation by ID.
// Returns ErrConversationNotFound if the conversation does not exist or is soft-deleted.
func (s *Store) GetConversation(id string) (*Conversation, error) {
	var conv Conversation
	if err := s.getBuilder(&conv, s.builder.
		Select(conversationColumns...).
		From("LLM_Conversations").
		Where(sq.Eq{"ID": id}).
		Where(sq.Eq{"DeleteAt": 0}),
		"get conversation"); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	return &conv, nil
}

// GetConversationByThreadBotUser looks up a non-deleted conversation by
// (RootPostID, BotID, UserID). Returns ErrConversationNotFound when no
// conversation exists for the given tuple.
func (s *Store) GetConversationByThreadBotUser(rootPostID, botID, userID string) (*Conversation, error) {
	var conv Conversation
	if err := s.getBuilder(&conv, s.builder.
		Select(conversationColumns...).
		From("LLM_Conversations").
		Where(sq.Eq{"RootPostID": rootPostID}).
		Where(sq.Eq{"BotID": botID}).
		Where(sq.Eq{"UserID": userID}).
		Where(sq.Eq{"DeleteAt": 0}),
		"get conversation by thread/bot/user"); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	return &conv, nil
}

// SetConversationTitleIfUnset sets a generated title on a non-deleted
// conversation that has none, so it never overwrites a title its owner set.
// It reports whether the title was set.
func (s *Store) SetConversationTitleIfUnset(id, title string) (bool, error) {
	return s.execBuilderRows(s.builder.
		Update("LLM_Conversations").
		Set("Title", title).
		Where(sq.Eq{"ID": id}).
		Where(sq.Eq{"Title": ""}).
		Where(sq.Eq{"DeleteAt": 0}),
		"set conversation title if unset")
}

// RenameConversation sets the title of a non-deleted conversation, reporting
// whether one was changed. A title is not activity, so UpdatedAt (which
// orders the conversation list) is left alone.
func (s *Store) RenameConversation(id, title string) (bool, error) {
	return s.execBuilderRows(s.builder.
		Update("LLM_Conversations").
		Set("Title", title).
		Where(sq.Eq{"ID": id}).
		Where(sq.Eq{"DeleteAt": 0}),
		"rename conversation")
}

// UpdateConversationRootPostID sets the RootPostID and updates the UpdatedAt timestamp.
// This is used when the post ID is only known after creation (e.g., thread analysis DM posts).
func (s *Store) UpdateConversationRootPostID(id string, rootPostID string) error {
	return s.execBuilder(s.builder.
		Update("LLM_Conversations").
		Set("RootPostID", rootPostID).
		Set("UpdatedAt", model.GetMillis()).
		Where(sq.Eq{"ID": id}),
		"update conversation root post ID")
}

// SoftDeleteConversation sets the DeleteAt timestamp on a conversation.
// Turns are not deleted until CleanupDeletedConversations runs.
func (s *Store) SoftDeleteConversation(id string, deleteAt int64) error {
	return s.execBuilder(s.builder.
		Update("LLM_Conversations").
		Set("DeleteAt", deleteAt).
		Set("UpdatedAt", deleteAt).
		Where(sq.Eq{"ID": id}),
		"soft delete conversation")
}

// ConversationSummary is a lightweight view of a conversation with its turn count,
// used for listing conversations. UpdatedAt is its last turn's activity.
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

// GetConversationSummariesForUser returns a user's non-deleted conversations,
// most recent activity first (ID breaks ties), with their turn counts. The
// ordering uses the (UserID, UpdatedAt DESC) WHERE DeleteAt = 0 index.
func (s *Store) GetConversationSummariesForUser(userID string, limit, offset int) ([]ConversationSummary, error) {
	return s.getConversationSummaries(s.conversationSummariesQuery(userID), limit, offset, "get conversation summaries")
}

// GetBotDMConversationSummariesForUser is GetConversationSummariesForUser
// limited to conversations rooted in the user's DM with the conversation's
// bot. The root post decides, not ChannelID: analysis results are delivered
// into that DM with a NULL ChannelID, while channel threads (mentions, meeting
// summaries) carry other people's posts.
func (s *Store) GetBotDMConversationSummariesForUser(userID string, limit, offset int) ([]ConversationSummary, error) {
	query := s.conversationSummariesQuery(userID).
		Join("Posts p ON p.Id = c.RootPostID").
		Join("Channels ch ON ch.Id = p.ChannelId").
		Where(sq.Eq{"ch.Type": model.ChannelTypeDirect}).
		Where("ch.Name IN (c.UserID || '__' || c.BotID, c.BotID || '__' || c.UserID)")
	return s.getConversationSummaries(query, limit, offset, "get bot DM conversation summaries")
}

func (s *Store) conversationSummariesQuery(userID string) sq.SelectBuilder {
	return s.builder.
		Select(
			"c.ID",
			"c.UserID",
			"c.BotID",
			"c.ChannelID",
			"c.RootPostID",
			"c.Title",
			"(SELECT COUNT(*) FROM LLM_Turns t WHERE t.ConversationID = c.ID) AS TurnCount",
			"c.UpdatedAt",
		).
		From("LLM_Conversations c").
		Where(sq.Eq{"c.UserID": userID}).
		Where(sq.Eq{"c.DeleteAt": 0})
}

func (s *Store) getConversationSummaries(query sq.SelectBuilder, limit, offset int, op string) ([]ConversationSummary, error) {
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}
	var summaries []ConversationSummary
	if err := s.selectBuilder(&summaries, query.
		OrderBy("c.UpdatedAt DESC", "c.ID ASC").
		Limit(uint64(limit)).   // #nosec G115 -- guarded above
		Offset(uint64(offset)), // #nosec G115 -- guarded above
		op); err != nil {
		return nil, err
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
