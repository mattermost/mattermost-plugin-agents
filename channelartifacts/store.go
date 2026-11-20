// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// sourceAvailable is true when the version's source file still exists on its
// post and neither the file nor the post is deleted. It reads Mattermost's own
// tables, so source deletion and data retention show up without any copy here.
const sourceAvailable = `EXISTS (
	SELECT 1 FROM FileInfo f JOIN Posts p ON p.Id = f.PostId
	WHERE f.Id = v.FileID AND f.PostId = v.PostID AND f.DeleteAt = 0 AND p.DeleteAt = 0)`

const entrySelect = `
SELECT a.ID, a.ChannelID, a.Title, a.HeadVersion AS Version, v.SHA256, v.Size,
	v.PostID AS SourcePostID, v.FileID AS SourceFileID, v.SourceBotUserID,
	a.CreatedBy, a.CreateAt, a.UpdatedBy, a.UpdateAt, ` + sourceAvailable + ` AS Available
FROM Agents_ChannelArtifacts a
JOIN Agents_ChannelArtifactVersions v ON v.ArtifactID = a.ID AND v.Version = a.HeadVersion`

const versionSelect = `
SELECT v.Version, v.SHA256, v.Size, v.PostID AS SourcePostID, v.FileID AS SourceFileID,
	v.SourceBotUserID, v.CreatedBy, v.CreateAt, (v.Version = a.HeadVersion) AS Head,
	` + sourceAvailable + ` AS Available
FROM Agents_ChannelArtifactVersions v
JOIN Agents_ChannelArtifacts a ON a.ID = v.ArtifactID`

// Store persists artifact metadata and per-version source references in
// Postgres. It never holds artifact content.
type Store struct {
	db *sqlx.DB
}

func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db}
}

// NewVersion is the source reference recorded for one version.
type NewVersion struct {
	PostID          string
	FileID          string
	SHA256          string
	Size            int64
	SourceBotUserID string
	UserID          string
	At              int64
	// Title, when set, renames the artifact in the same transaction that adds
	// the version.
	Title string
}

func (s *Store) tx(fn func(tx *sqlx.Tx) error) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// lockChannel serializes writes that count or compare a channel's artifacts.
func lockChannel(tx *sqlx.Tx, channelID string) error {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "channelartifacts:"+channelID); err != nil {
		return fmt.Errorf("failed to lock channel artifacts: %w", err)
	}
	return nil
}

func (s *Store) List(channelID string) ([]*Entry, error) {
	entries := []*Entry{}
	if err := s.db.Select(&entries, entrySelect+` WHERE a.ChannelID = $1 ORDER BY a.CreateAt DESC, a.ID`, channelID); err != nil {
		return nil, fmt.Errorf("failed to list channel artifacts: %w", err)
	}
	return entries, nil
}

func (s *Store) Get(id string) (*Entry, error) {
	var entry Entry
	if err := s.db.Get(&entry, entrySelect+` WHERE a.ID = $1`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get channel artifact: %w", err)
	}
	return &entry, nil
}

func (s *Store) Versions(id string) ([]VersionRecord, error) {
	versions := []VersionRecord{}
	if err := s.db.Select(&versions, versionSelect+` WHERE v.ArtifactID = $1 ORDER BY v.Version DESC`, id); err != nil {
		return nil, fmt.Errorf("failed to list channel artifact versions: %w", err)
	}
	return versions, nil
}

func (s *Store) Version(id string, version int) (*VersionRecord, error) {
	var rec VersionRecord
	if err := s.db.Get(&rec, versionSelect+` WHERE v.ArtifactID = $1 AND v.Version = $2`, id, version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get channel artifact version: %w", err)
	}
	return &rec, nil
}

// headWithSHA returns the artifact in the channel whose current version has sha.
func headWithSHA(tx *sqlx.Tx, channelID, sha string) (string, error) {
	var id string
	err := tx.Get(&id, `
		SELECT a.ID FROM Agents_ChannelArtifacts a
		JOIN Agents_ChannelArtifactVersions v ON v.ArtifactID = a.ID AND v.Version = a.HeadVersion
		WHERE a.ChannelID = $1 AND v.SHA256 = $2 LIMIT 1`, channelID, sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to look up identical channel artifact: %w", err)
	}
	return id, nil
}

func insertVersion(tx *sqlx.Tx, id string, version int, v NewVersion) error {
	_, err := tx.Exec(`
		INSERT INTO Agents_ChannelArtifactVersions
			(ArtifactID, Version, PostID, FileID, SHA256, Size, SourceBotUserID, CreatedBy, CreateAt)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, version, v.PostID, v.FileID, v.SHA256, v.Size, v.SourceBotUserID, v.UserID, v.At)
	if err != nil {
		return fmt.Errorf("failed to insert channel artifact version: %w", err)
	}
	return nil
}

// Create inserts a new artifact with version 1. It fails with ErrLimitReached
// when the channel already holds maxPerChannel artifacts, and with a
// DuplicateError when a current version in the channel has the same SHA-256.
func (s *Store) Create(id, channelID, title string, v NewVersion, maxPerChannel int) error {
	return s.tx(func(tx *sqlx.Tx) error {
		if err := lockChannel(tx, channelID); err != nil {
			return err
		}
		var count int
		if err := tx.Get(&count, `SELECT COUNT(*) FROM Agents_ChannelArtifacts WHERE ChannelID = $1`, channelID); err != nil {
			return fmt.Errorf("failed to count channel artifacts: %w", err)
		}
		if count >= maxPerChannel {
			return ErrLimitReached
		}
		dup, err := headWithSHA(tx, channelID, v.SHA256)
		if err != nil {
			return err
		}
		if dup != "" {
			return &DuplicateError{ArtifactID: dup}
		}
		_, err = tx.Exec(`
			INSERT INTO Agents_ChannelArtifacts (ID, ChannelID, Title, HeadVersion, LastVersion, CreatedBy, CreateAt, UpdatedBy, UpdateAt)
			VALUES ($1, $2, $3, 1, 1, $4, $5, $4, $5)`, id, channelID, title, v.UserID, v.At)
		if err != nil {
			return fmt.Errorf("failed to insert channel artifact: %w", err)
		}
		return insertVersion(tx, id, 1, v)
	})
}

// AddVersion records v as the next version of the artifact and makes it
// current. Versions beyond the newest versionsToKeep are deleted, never the
// current one. A DuplicateError is returned when a current version in the
// channel, including this artifact's own, already has the same SHA-256.
func (s *Store) AddVersion(id string, v NewVersion, versionsToKeep int) error {
	return s.tx(func(tx *sqlx.Tx) error {
		var channelID string
		if err := tx.Get(&channelID, `SELECT ChannelID FROM Agents_ChannelArtifacts WHERE ID = $1 FOR UPDATE`, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to get channel artifact: %w", err)
		}
		if err := lockChannel(tx, channelID); err != nil {
			return err
		}
		dup, err := headWithSHA(tx, channelID, v.SHA256)
		if err != nil {
			return err
		}
		if dup != "" {
			return &DuplicateError{ArtifactID: dup}
		}
		// LastVersion only grows, so a number whose version was deleted is never
		// given to different content; rows from before the column existed are
		// covered by the head and the highest stored version.
		var next int
		if err = tx.Get(&next, `
			SELECT GREATEST(a.LastVersion, a.HeadVersion, COALESCE((SELECT MAX(Version) FROM Agents_ChannelArtifactVersions WHERE ArtifactID = a.ID), 0)) + 1
			FROM Agents_ChannelArtifacts a WHERE a.ID = $1`, id); err != nil {
			return fmt.Errorf("failed to number channel artifact version: %w", err)
		}
		if err = insertVersion(tx, id, next, v); err != nil {
			return err
		}
		if _, err = tx.Exec(`
			UPDATE Agents_ChannelArtifacts
			SET HeadVersion = $2, LastVersion = $2, UpdatedBy = $3, UpdateAt = $4, Title = COALESCE(NULLIF($5, ''), Title)
			WHERE ID = $1`, id, next, v.UserID, v.At, v.Title); err != nil {
			return fmt.Errorf("failed to update channel artifact: %w", err)
		}
		_, err = tx.Exec(`
			DELETE FROM Agents_ChannelArtifactVersions
			WHERE ArtifactID = $1 AND Version <> $2
			AND Version NOT IN (
				SELECT Version FROM Agents_ChannelArtifactVersions WHERE ArtifactID = $1 ORDER BY Version DESC LIMIT $3)`,
			id, next, versionsToKeep)
		if err != nil {
			return fmt.Errorf("failed to prune channel artifact versions: %w", err)
		}
		return nil
	})
}

func (s *Store) Rename(id, title, userID string, at int64) error {
	ok, err := s.execRows(`UPDATE Agents_ChannelArtifacts SET Title = $2, UpdatedBy = $3, UpdateAt = $4 WHERE ID = $1`, id, title, userID, at)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// SetHead makes an existing version current.
func (s *Store) SetHead(id string, version int, userID string, at int64) error {
	ok, err := s.execRows(`
		UPDATE Agents_ChannelArtifacts SET HeadVersion = $2, UpdatedBy = $3, UpdateAt = $4
		WHERE ID = $1 AND EXISTS (SELECT 1 FROM Agents_ChannelArtifactVersions WHERE ArtifactID = $1 AND Version = $2)`,
		id, version, userID, at)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// DeleteVersion removes one version row. When it was the current version the
// newest remaining one becomes current. The last remaining version cannot be
// deleted; purge the artifact instead.
func (s *Store) DeleteVersion(id string, version int, userID string, at int64) error {
	return s.tx(func(tx *sqlx.Tx) error {
		var head int
		if err := tx.Get(&head, `SELECT HeadVersion FROM Agents_ChannelArtifacts WHERE ID = $1 FOR UPDATE`, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to get channel artifact: %w", err)
		}
		var count int
		if err := tx.Get(&count, `SELECT COUNT(*) FROM Agents_ChannelArtifactVersions WHERE ArtifactID = $1`, id); err != nil {
			return fmt.Errorf("failed to count channel artifact versions: %w", err)
		}
		res, err := tx.Exec(`DELETE FROM Agents_ChannelArtifactVersions WHERE ArtifactID = $1 AND Version = $2`, id, version)
		if err != nil {
			return fmt.Errorf("failed to delete channel artifact version: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if count <= 1 {
			return ErrInvalidInput
		}
		if version != head {
			return nil
		}
		_, err = tx.Exec(`
			UPDATE Agents_ChannelArtifacts
			SET HeadVersion = (SELECT MAX(Version) FROM Agents_ChannelArtifactVersions WHERE ArtifactID = $1),
				UpdatedBy = $2, UpdateAt = $3
			WHERE ID = $1`, id, userID, at)
		if err != nil {
			return fmt.Errorf("failed to move channel artifact head: %w", err)
		}
		return nil
	})
}

// Purge deletes the artifact and all of its version rows. Source files and
// posts are untouched.
func (s *Store) Purge(id string) error {
	return s.tx(func(tx *sqlx.Tx) error {
		res, err := tx.Exec(`DELETE FROM Agents_ChannelArtifacts WHERE ID = $1`, id)
		if err != nil {
			return fmt.Errorf("failed to delete channel artifact: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(`DELETE FROM Agents_ChannelArtifactVersions WHERE ArtifactID = $1`, id); err != nil {
			return fmt.Errorf("failed to delete channel artifact versions: %w", err)
		}
		return nil
	})
}

// SweepDeletedChannels deletes the rows of artifacts whose channel no longer
// exists in Mattermost. Archived channels keep their library. It returns the
// number of artifacts removed.
func (s *Store) SweepDeletedChannels() (int64, error) {
	var removed int64
	err := s.tx(func(tx *sqlx.Tx) error {
		if _, err := tx.Exec(`
			DELETE FROM Agents_ChannelArtifactVersions WHERE ArtifactID IN (
				SELECT a.ID FROM Agents_ChannelArtifacts a
				WHERE NOT EXISTS (SELECT 1 FROM Channels c WHERE c.Id = a.ChannelID))`); err != nil {
			return fmt.Errorf("failed to sweep channel artifact versions: %w", err)
		}
		res, err := tx.Exec(`
			DELETE FROM Agents_ChannelArtifacts a
			WHERE NOT EXISTS (SELECT 1 FROM Channels c WHERE c.Id = a.ChannelID)`)
		if err != nil {
			return fmt.Errorf("failed to sweep channel artifacts: %w", err)
		}
		removed, _ = res.RowsAffected()
		return nil
	})
	return removed, err
}

func (s *Store) execRows(query string, args ...any) (bool, error) {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return false, fmt.Errorf("failed to update channel artifact: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	return n > 0, nil
}
