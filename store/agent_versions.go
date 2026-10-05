// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jmoiron/sqlx"
	"github.com/mattermost/mattermost-plugin-agents/v2/audit"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

// AgentVersionSource records which kind of write produced an agent version.
type AgentVersionSource string

const (
	// AgentVersionSourceInitial marks a version recorded from an agent that
	// existed before versioning (activation backfill or first versioned update).
	AgentVersionSourceInitial AgentVersionSource = "initial"
	AgentVersionSourceCreate  AgentVersionSource = "create"
	AgentVersionSourceUpdate  AgentVersionSource = "update"
	AgentVersionSourceRestore AgentVersionSource = "restore"
	AgentVersionSourceImport  AgentVersionSource = "import"
	// AgentVersionSourceSystem marks internal writes (migrations, rollbacks).
	AgentVersionSourceSystem AgentVersionSource = "system"
)

// AgentVersionMeta describes who and what produced an agent write; every
// CreateAgent/UpdateAgent call records it on the version it writes.
type AgentVersionMeta struct {
	// ActorID is the acting user ID, empty for system writes.
	ActorID string
	Source  AgentVersionSource
	// RestoredFromVersion is the restored version for AgentVersionSourceRestore, else 0.
	RestoredFromVersion int
}

// SystemAgentVersionMeta is the metadata for internal agent writes.
func SystemAgentVersionMeta() AgentVersionMeta {
	return AgentVersionMeta{Source: AgentVersionSourceSystem}
}

// AgentVersion is the metadata of one saved agent version.
type AgentVersion struct {
	Version             int                `json:"version"`
	CreatedBy           string             `json:"createdBy"`
	CreateAt            int64              `json:"createAt"`
	Source              AgentVersionSource `json:"source"`
	RestoredFromVersion int                `json:"restoredFromVersion"`
	// ChangedFields lists the BotConfig JSON keys that differ from the
	// previous version; empty for the first version.
	ChangedFields []string `json:"changedFields"`
}

// AgentVersionDetail is an agent version together with its configuration snapshot.
type AgentVersionDetail struct {
	AgentVersion
	Config llm.BotConfig `json:"config"`
}

type agentVersionRow struct {
	Version             int    `db:"version"`
	Config              string `db:"config"`
	CreatedBy           string `db:"createdby"`
	CreateAt            int64  `db:"createat"`
	Source              string `db:"source"`
	RestoredFromVersion int    `db:"restoredfromversion"`
	ChangedFields       string `db:"changedfields"`
}

func (r *agentVersionRow) toAgentVersion() (AgentVersion, error) {
	v := AgentVersion{
		Version:             r.Version,
		CreatedBy:           r.CreatedBy,
		CreateAt:            r.CreateAt,
		Source:              AgentVersionSource(r.Source),
		RestoredFromVersion: r.RestoredFromVersion,
	}
	if err := unmarshalJSONSlice(r.ChangedFields, &v.ChangedFields); err != nil {
		return AgentVersion{}, fmt.Errorf("failed to parse ChangedFields: %w", err)
	}
	if v.ChangedFields == nil {
		v.ChangedFields = []string{}
	}
	return v, nil
}

func emptyToNil[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return s
}

// agentVersionSnapshot returns the part of cfg recorded in a version: the
// agent's configuration without lifecycle metadata, with empty collections
// normalized the way the agents table round-trips them so that equal
// configurations always serialize identically.
func agentVersionSnapshot(cfg *llm.BotConfig) llm.BotConfig {
	snap := *cfg
	snap.Service = nil
	snap.MaxFileSize = 0
	snap.BotUserID = ""
	snap.CreatorID = ""
	snap.CreateAt = 0
	snap.UpdateAt = 0
	snap.DeleteAt = 0
	snap.ChannelIDs = emptyToNil(snap.ChannelIDs)
	snap.UserIDs = emptyToNil(snap.UserIDs)
	snap.TeamIDs = emptyToNil(snap.TeamIDs)
	snap.AdminUserIDs = emptyToNil(snap.AdminUserIDs)
	snap.EnabledMCPTools = emptyToNil(snap.EnabledMCPTools)
	snap.EnabledNativeTools = emptyToNil(snap.EnabledNativeTools)
	if snap.Documents == nil {
		snap.Documents = []llm.AgentDocument{}
	}
	return snap
}

// ignoredVersionFields are BotConfig keys never reported in ChangedFields:
// service is not part of a snapshot, and structuredOutputEnabled is
// deprecated and ignored at runtime (saving from the UI clears it).
var ignoredVersionFields = map[string]bool{
	"service":                 true,
	"structuredOutputEnabled": true,
}

// laterVersionFields maps BotConfig keys that snapshots recorded before the
// field existed lack to the snapshot value they stand for.
var laterVersionFields = map[string]json.RawMessage{
	"documents": json.RawMessage("[]"),
}

// changedVersionFields returns the BotConfig keys of next that differ from
// the snapshot JSON prev, excluding ignoredVersionFields. Keys of
// laterVersionFields missing from prev compare as their recorded value.
func changedVersionFields(prev string, next llm.BotConfig) []string {
	var prevFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(prev), &prevFields); err != nil || prevFields == nil {
		prevFields = map[string]json.RawMessage{}
	}
	for key, value := range laterVersionFields {
		if _, ok := prevFields[key]; !ok {
			prevFields[key] = value
		}
	}
	return slices.DeleteFunc(audit.ChangedJSONKeys(prevFields, next), func(key string) bool {
		return ignoredVersionFields[key]
	})
}

func validateAgentVersionMeta(meta AgentVersionMeta) error {
	switch meta.Source {
	case AgentVersionSourceInitial, AgentVersionSourceCreate, AgentVersionSourceUpdate,
		AgentVersionSourceImport, AgentVersionSourceSystem:
		if meta.RestoredFromVersion != 0 {
			return fmt.Errorf("restoredFromVersion is only valid for source %q", AgentVersionSourceRestore)
		}
		return nil
	case AgentVersionSourceRestore:
		if meta.RestoredFromVersion <= 0 {
			return errors.New("restore versions must record the restored version")
		}
		return nil
	default:
		return fmt.Errorf("invalid agent version source %q", meta.Source)
	}
}

func insertAgentVersionTx(tx *sqlx.Tx, agentID string, version int, snapshot llm.BotConfig, meta AgentVersionMeta, changed []string, createAt int64) error {
	configJSON, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal agent version config: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO Agents_AgentVersions (
			ID, AgentID, Version, Config, CreatedBy, CreateAt, Source, RestoredFromVersion, ChangedFields
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		model.NewId(),
		agentID,
		version,
		string(configJSON),
		meta.ActorID,
		createAt,
		string(meta.Source),
		meta.RestoredFromVersion,
		marshalJSONSlice(changed),
	); err != nil {
		return fmt.Errorf("failed to insert version %d of agent %q: %w", version, agentID, err)
	}
	return nil
}

// latestAgentVersionTx returns the highest version number of agentID and its
// raw config JSON, or 0 when the agent has no versions.
func latestAgentVersionTx(tx *sqlx.Tx, agentID string) (int, string, error) {
	var row struct {
		Version int    `db:"version"`
		Config  string `db:"config"`
	}
	err := tx.Get(&row,
		`SELECT Version, Config FROM Agents_AgentVersions
		WHERE AgentID = $1
		ORDER BY Version DESC
		LIMIT 1`,
		agentID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("failed to get latest version of agent %q: %w", agentID, err)
	}
	return row.Version, row.Config, nil
}

// lockActiveAgentTx locks agentID's row for the rest of tx and returns it,
// or nil when the agent does not exist or is soft-deleted. The row lock is
// what serializes version numbering across concurrent writers.
func lockActiveAgentTx(tx *sqlx.Tx, agentID string) (*llm.BotConfig, error) {
	var row agentRow
	err := tx.Get(&row,
		`SELECT `+agentSelectColumns+`
		FROM Agents_UserAgents
		WHERE ID = $1 AND DeleteAt = 0
		FOR UPDATE`,
		agentID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to lock agent %q: %w", agentID, err)
	}
	return row.toBotConfig()
}

// ensureInitialAgentVersionTx records current (the locked, pre-write row) as
// version 1 when the agent has no versions yet. It returns the latest
// version number and that version's raw config JSON.
func ensureInitialAgentVersionTx(tx *sqlx.Tx, current *llm.BotConfig) (int, string, error) {
	latest, latestConfig, err := latestAgentVersionTx(tx, current.ID)
	if err != nil || latest > 0 {
		return latest, latestConfig, err
	}
	snapshot := agentVersionSnapshot(current)
	if err = insertAgentVersionTx(tx, current.ID, 1, snapshot, AgentVersionMeta{Source: AgentVersionSourceInitial}, nil, current.UpdateAt); err != nil {
		return 0, "", err
	}
	configJSON, err := json.Marshal(snapshot)
	if err != nil {
		return 0, "", fmt.Errorf("failed to marshal agent version config: %w", err)
	}
	return 1, string(configJSON), nil
}

// ensureCurrentAgentVersionTx makes sure the latest version of current (the
// locked, pre-write row) records that row. An agent without versions gets
// version 1 (source "initial"); one whose row differs from its latest
// version, because something wrote the row without recording a version,
// gets the row recorded as the next version (source "system") so that
// history never attributes those changes to the next writer. It returns the
// latest version number, that version's raw config JSON, and whether it
// wrote a version.
func ensureCurrentAgentVersionTx(tx *sqlx.Tx, current *llm.BotConfig) (int, string, bool, error) {
	latest, latestConfig, err := latestAgentVersionTx(tx, current.ID)
	if err != nil {
		return 0, "", false, err
	}
	if latest == 0 {
		latest, latestConfig, err = ensureInitialAgentVersionTx(tx, current)
		return latest, latestConfig, err == nil, err
	}

	snapshot := agentVersionSnapshot(current)
	changed := changedVersionFields(latestConfig, snapshot)
	if len(changed) == 0 {
		return latest, latestConfig, false, nil
	}
	if err = insertAgentVersionTx(tx, current.ID, latest+1, snapshot, SystemAgentVersionMeta(), changed, current.UpdateAt); err != nil {
		return 0, "", false, err
	}
	configJSON, err := json.Marshal(snapshot)
	if err != nil {
		return 0, "", false, fmt.Errorf("failed to marshal agent version config: %w", err)
	}
	return latest + 1, string(configJSON), true, nil
}

// ListAgentVersions returns the versions of agentID, newest first. Deleted
// agents keep their versions; callers gate access on the agent itself.
func (s *Store) ListAgentVersions(agentID string) ([]AgentVersion, error) {
	var rows []agentVersionRow
	err := s.db.Select(&rows,
		`SELECT Version, CreatedBy, CreateAt, Source, RestoredFromVersion, ChangedFields
		FROM Agents_AgentVersions
		WHERE AgentID = $1
		ORDER BY Version DESC`,
		agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list versions of agent %q: %w", agentID, err)
	}

	versions := make([]AgentVersion, 0, len(rows))
	for i := range rows {
		v, parseErr := rows[i].toAgentVersion()
		if parseErr != nil {
			return nil, parseErr
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// GetAgentVersion returns one version of agentID with its configuration
// snapshot, or nil, nil when that version does not exist.
func (s *Store) GetAgentVersion(agentID string, version int) (*AgentVersionDetail, error) {
	var row agentVersionRow
	err := s.db.Get(&row,
		`SELECT Version, Config, CreatedBy, CreateAt, Source, RestoredFromVersion, ChangedFields
		FROM Agents_AgentVersions
		WHERE AgentID = $1 AND Version = $2`,
		agentID, version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get version %d of agent %q: %w", version, agentID, err)
	}

	v, err := row.toAgentVersion()
	if err != nil {
		return nil, err
	}
	detail := &AgentVersionDetail{AgentVersion: v}
	if err := json.Unmarshal([]byte(row.Config), &detail.Config); err != nil {
		return nil, fmt.Errorf("failed to parse config of version %d of agent %q: %w", version, agentID, err)
	}
	if detail.Config.Documents == nil {
		detail.Config.Documents = []llm.AgentDocument{}
	}
	return detail, nil
}

// GetLatestAgentVersion returns the current (highest) version number of
// agentID, or 0 when it has no versions.
func (s *Store) GetLatestAgentVersion(agentID string) (int, error) {
	var latest int
	err := s.db.Get(&latest,
		`SELECT COALESCE(MAX(Version), 0) FROM Agents_AgentVersions WHERE AgentID = $1`,
		agentID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest version of agent %q: %w", agentID, err)
	}
	return latest, nil
}

// BackfillAgentVersions makes the latest version of every active agent
// record its current row: agents without versions get version 1 (source
// "initial"), and agents whose row differs from their latest version get the
// row recorded as a "system" version. It returns how many agents it wrote a
// version for. An agent that fails does not stop the others; the returned
// error joins every per-agent failure. Idempotent and safe to run concurrently
// on several nodes: each agent is handled in its own transaction under the
// same row lock as UpdateAgent.
func (s *Store) BackfillAgentVersions() (int, error) {
	var agentIDs []string
	if err := s.db.Select(&agentIDs, `SELECT ID FROM Agents_UserAgents WHERE DeleteAt = 0 ORDER BY ID`); err != nil {
		return 0, fmt.Errorf("failed to list agents for version backfill: %w", err)
	}

	backfilled := 0
	var errs []error
	for _, agentID := range agentIDs {
		wrote, err := s.backfillAgentVersion(agentID)
		if err != nil {
			errs = append(errs, fmt.Errorf("agent %q: %w", agentID, err))
			continue
		}
		if wrote {
			backfilled++
		}
	}
	return backfilled, errors.Join(errs...)
}

func (s *Store) backfillAgentVersion(agentID string) (wrote bool, err error) {
	tx, err := s.db.Beginx()
	if err != nil {
		return false, fmt.Errorf("failed to begin agent version backfill transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	current, err := lockActiveAgentTx(tx, agentID)
	if err != nil {
		return false, err
	}
	if current == nil {
		// Deleted since the candidate scan.
		return false, tx.Rollback()
	}
	// Another node that got here first leaves nothing to write.
	_, _, wrote, err = ensureCurrentAgentVersionTx(tx, current)
	if err != nil {
		return false, err
	}
	if !wrote {
		return false, tx.Rollback()
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit agent version backfill: %w", err)
	}
	return true, nil
}
