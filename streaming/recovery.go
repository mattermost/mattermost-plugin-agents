// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/conversation"
	"github.com/mattermost/mattermost-plugin-agents/v2/store"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
	"go.opentelemetry.io/otel/codes"
)

// RecoveryStore is the store subset used by the abandoned-stream sweep.
type RecoveryStore interface {
	GetStaleStreamingTurns(staleAfterMillis int64, limit uint64) ([]store.Turn, error)
	ClaimStaleStreamingTurn(id string, staleAfterMillis int64) (bool, error)
	ReleaseStreamingTurnClaim(id string) error
}

// RecoveryClient is the Mattermost API subset used by the sweep.
type RecoveryClient interface {
	GetPost(postID string) (*model.Post, error)
	UpdatePost(post *model.Post) error
	LogWarn(msg string, keyValuePairs ...any)
	LogInfo(msg string, keyValuePairs ...any)
}

// RecoveredStream identifies a post repaired by the sweep (identifiers only).
type RecoveredStream struct {
	PostID    string
	ChannelID string
	TurnID    string
}

// DefaultRecoveryBatchSize bounds the turns processed per sweep.
const DefaultRecoveryBatchSize = 200

// RecoverAbandonedStreams finds assistant turns left in the streaming state
// whose heartbeat is older than StaleStreamThreshold (the generating node
// crashed, or was killed before finalizing), claims each atomically, and
// marks its post as interrupted with the last flushed partial content.
// Concurrent sweeps on several nodes are safe: only the node whose claim
// update wins repairs a given post.
func RecoverAbandonedStreams(ctx context.Context, rs RecoveryStore, client RecoveryClient, limit uint64) ([]RecoveredStream, error) {
	_, span := telemetry.Tracer().Start(ctx, "recover abandoned streams")
	defer span.End()

	staleMillis := StaleStreamThreshold.Milliseconds()
	var recovered []RecoveredStream
	// Drain the backlog in batches. Stop when a batch is short, or when a
	// full batch repaired nothing (released claims would be listed again).
	for {
		turns, err := rs.GetStaleStreamingTurns(staleMillis, limit)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "list stale streaming turns")
			return recovered, fmt.Errorf("failed to list stale streaming turns: %w", err)
		}

		repairedInBatch := 0
		for _, turn := range turns {
			if r, ok := recoverTurn(rs, client, turn, staleMillis); ok {
				recovered = append(recovered, r)
				repairedInBatch++
			}
		}
		if uint64(len(turns)) < limit || repairedInBatch == 0 {
			break
		}
	}

	if len(recovered) > 0 {
		client.LogInfo("Marked abandoned LLM streams as interrupted", "count", len(recovered))
	}
	return recovered, nil
}

// recoverTurn claims one stale turn and marks its post interrupted. When the
// post cannot be updated the claim is released so a later sweep retries it.
func recoverTurn(rs RecoveryStore, client RecoveryClient, turn store.Turn, staleMillis int64) (RecoveredStream, bool) {
	claimed, claimErr := rs.ClaimStaleStreamingTurn(turn.ID, staleMillis)
	if claimErr != nil {
		client.LogWarn("Failed to claim abandoned streaming turn", "error", claimErr, "turn_id", turn.ID)
		return RecoveredStream{}, false
	}
	if !claimed || turn.PostID == nil || *turn.PostID == "" {
		return RecoveredStream{}, false
	}

	release := func() {
		if relErr := rs.ReleaseStreamingTurnClaim(turn.ID); relErr != nil {
			client.LogWarn("Failed to release abandoned streaming turn claim", "error", relErr, "turn_id", turn.ID)
		}
	}

	post, getErr := client.GetPost(*turn.PostID)
	if getErr != nil {
		client.LogWarn("Failed to load post for abandoned stream", "error", getErr, "post_id", *turn.PostID)
		release()
		return RecoveredStream{}, false
	}
	if partial := partialTextFromTurn(turn); len(partial) > len(post.Message) {
		post.Message = partial
	}
	MarkInterrupted(post, InterruptedReasonLost, model.GetMillis())
	if updErr := client.UpdatePost(post); updErr != nil {
		client.LogWarn("Failed to mark abandoned stream post interrupted", "error", updErr, "post_id", post.Id)
		release()
		return RecoveredStream{}, false
	}
	return RecoveredStream{PostID: post.Id, ChannelID: post.ChannelId, TurnID: turn.ID}, true
}

// partialTextFromTurn concatenates the text blocks of a turn's content.
func partialTextFromTurn(turn store.Turn) string {
	blocks, err := conversation.UnmarshalBlocks(turn.Content)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range blocks {
		if block.Type == conversation.BlockTypeText {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}
