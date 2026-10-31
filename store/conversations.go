// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"errors"
	"strings"

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

// getConversation returns the single conversation row the query selects,
// or ErrConversationNotFound when it selects none. where narrows
// SELECT conversationColumns FROM LLM_Conversations.
func (s *Store) getConversation(desc string, where func(sq.SelectBuilder) sq.SelectBuilder) (*Conversation, error) {
	var conv Conversation
	if err := s.getBuilder(&conv, where(s.builder.
		Select(conversationColumns...).
		From("LLM_Conversations")), desc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	return &conv, nil
}

// GetConversation retrieves an active (non-archived) conversation by ID.
// Returns ErrConversationNotFound if the conversation does not exist or is archived.
func (s *Store) GetConversation(id string) (*Conversation, error) {
	return s.getConversation("get conversation", func(b sq.SelectBuilder) sq.SelectBuilder {
		return b.Where(sq.Eq{"ID": id, "DeleteAt": 0})
	})
}

// GetConversationByThreadBotUser looks up the active conversation for
// (RootPostID, BotID, UserID). Returns ErrConversationNotFound when the tuple
// has no active conversation.
func (s *Store) GetConversationByThreadBotUser(rootPostID, botID, userID string) (*Conversation, error) {
	return s.getConversation("get conversation by thread/bot/user", func(b sq.SelectBuilder) sq.SelectBuilder {
		return b.Where(sq.Eq{"RootPostID": rootPostID, "BotID": botID, "UserID": userID, "DeleteAt": 0})
	})
}

// UpdateConversationTitle sets the title of an active conversation, as
// RenameConversation does, for callers that need no report of whether it
// existed. A title is not activity, so UpdatedAt (which orders the
// conversation list) is left alone.
func (s *Store) UpdateConversationTitle(id, title string) error {
	_, err := s.RenameConversation(id, title)
	return err
}

// SetConversationTitleIfUnset sets the title of an active (non-archived)
// conversation only while it still has no title, for automatically generated titles. It
// reports false, without error, when the conversation already has a title
// (for example one its owner set while the title was being generated), or is
// missing or archived. User-set titles are never empty, so this never
// overwrites one. UpdatedAt is left alone, as in RenameConversation.
func (s *Store) SetConversationTitleIfUnset(id, title string) (bool, error) {
	return s.execBuilderRows(s.builder.
		Update("LLM_Conversations").
		Set("Title", title).
		Where(sq.Eq{"ID": id}).
		Where(sq.Eq{"Title": ""}).
		Where(sq.Eq{"DeleteAt": 0}),
		"set conversation title if unset")
}

// GetConversationIncludingArchived retrieves a conversation by ID whether or
// not it is archived (DeleteAt > 0). Returns ErrConversationNotFound when no
// row exists.
func (s *Store) GetConversationIncludingArchived(id string) (*Conversation, error) {
	return s.getConversation("get conversation including archived", func(b sq.SelectBuilder) sq.SelectBuilder {
		return b.Where(sq.Eq{"ID": id})
	})
}

// RenameConversation sets the title of an active (non-archived) conversation.
// It reports false, without error, when no active conversation with the ID
// exists, so a rename that races an archive never resurrects or edits it.
// UpdatedAt is left alone so a rename does not reorder the conversation list.
func (s *Store) RenameConversation(id, title string) (bool, error) {
	return s.execBuilderRows(s.builder.
		Update("LLM_Conversations").
		Set("Title", title).
		Where(sq.Eq{"ID": id}).
		Where(sq.Eq{"DeleteAt": 0}),
		"rename conversation")
}

// ArchiveConversation marks an active conversation archived by setting
// DeleteAt, as Mattermost does for an archived channel. Turns are kept and the
// conversation is restored by a later turn on it (RestoreConversation). The
// write is conditional on the row being active, so archiving an archived or
// missing conversation, or racing another archive, is a no-op. UpdatedAt is
// the conversation's last activity, so archiving leaves it alone.
func (s *Store) ArchiveConversation(id string, archivedAt int64) error {
	return s.execBuilder(s.builder.
		Update("LLM_Conversations").
		Set("DeleteAt", archivedAt).
		Where(sq.Eq{"ID": id, "DeleteAt": 0}),
		"archive conversation")
}

// GetLatestConversationByThreadBotUser looks up the conversation for
// (RootPostID, BotID, UserID) whether or not it is archived: the active one
// when it exists, otherwise the most recently archived one. It never writes;
// callers that continue the conversation restore it with RestoreConversation.
// Returns ErrConversationNotFound when the tuple has no conversation at all.
// idx_llm_conversations_thread_bot_user_all (migration 000014) serves it.
func (s *Store) GetLatestConversationByThreadBotUser(rootPostID, botID, userID string) (*Conversation, error) {
	return s.getConversation("get latest conversation by thread/bot/user", func(b sq.SelectBuilder) sq.SelectBuilder {
		return b.Where(sq.Eq{"RootPostID": rootPostID, "BotID": botID, "UserID": userID}).
			OrderBy("(DeleteAt = 0) DESC", "DeleteAt DESC").
			Limit(1)
	})
}

// RestoreConversation un-archives the conversation with the given ID and
// returns it. The DeleteAt > 0 condition makes concurrent restores write at
// most once. Returns ErrConversationNotFound when no archived conversation
// with the ID exists (missing, or already active: a concurrent restore won),
// and ErrConversationConflict when another active conversation already owns
// its (RootPostID, BotID, UserID) tuple. UpdatedAt is left alone: the turn the
// restore precedes advances it (see CreateTurn), and a restore with no turn is
// not activity.
func (s *Store) RestoreConversation(id string) (*Conversation, error) {
	var conv Conversation
	err := s.getBuilder(&conv, s.builder.
		Update("LLM_Conversations").
		Set("DeleteAt", 0).
		Where(sq.Eq{"ID": id}).
		Where(sq.Gt{"DeleteAt": 0}).
		Suffix("RETURNING "+strings.Join(conversationColumns, ", ")),
		"restore conversation")
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrConversationNotFound
	case isUniqueViolation(err):
		return nil, ErrConversationConflict
	}
	if err != nil {
		return nil, err
	}
	return &conv, nil
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

// ConversationSummary is a lightweight view of a conversation with its turn count,
// used for listing conversations in the RHS threads panel.
//
// UpdatedAt is the conversation's last activity: creating a turn advances
// LLM_Conversations.UpdatedAt to the turn's CreatedAt, while title changes
// leave it alone.
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

// GetConversationSummariesForUser returns a user's conversations ordered by last
// activity (see ConversationSummary.UpdatedAt) descending, with ID as a
// deterministic tie-breaker, including a turn count per conversation. Only
// active (non-archived) conversations whose root post, if any, still exists
// and is not deleted are returned: a conversation of a deleted thread is
// unreachable (see conversation.CheckRootPost), so it is not listed either.
// Posts is Mattermost's own table; the plugin store shares its database. The
// ordering reads the (UserID, UpdatedAt DESC) WHERE DeleteAt = 0 index, and
// turns are counted only for the returned page.
func (s *Store) GetConversationSummariesForUser(userID string, limit, offset int) ([]ConversationSummary, error) {
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}
	var summaries []ConversationSummary
	if err := s.selectBuilder(&summaries, s.builder.
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
		Where(sq.Eq{"c.DeleteAt": 0}).
		Where(sq.Or{
			sq.Eq{"c.RootPostID": nil},
			sq.Eq{"c.RootPostID": ""},
			sq.Expr("EXISTS (SELECT 1 FROM Posts p WHERE p.Id = c.RootPostID AND p.DeleteAt = 0)"),
		}).
		OrderBy("c.UpdatedAt DESC", "c.ID ASC").
		Limit(uint64(limit)).   // #nosec G115 -- guarded above
		Offset(uint64(offset)), // #nosec G115 -- guarded above
		"get conversation summaries"); err != nil {
		return nil, err
	}
	if summaries == nil {
		summaries = []ConversationSummary{}
	}
	return summaries, nil
}
