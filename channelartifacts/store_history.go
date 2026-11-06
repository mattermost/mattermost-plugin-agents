// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

func versionRecordFromEntry(entry *Entry, legacy bool) VersionRecord {
	return VersionRecord{VersionSnapshot: entry.VersionSnapshot, LegacyUnversionedBlobs: legacy}
}

func applyVersionToEntry(entry *Entry, rec VersionRecord) {
	title := entry.Title
	entry.VersionSnapshot = rec.VersionSnapshot
	if rec.Title == "" {
		entry.Title = title
	}
	entry.Encoding = "gzip"
	entry.SchemaVersion = schemaVersion
}

func (s *Store) loadHistory(id string) (*VersionHistory, error) {
	var hist VersionHistory
	err := s.kv.KVGet(histKey(id), &hist)
	if mmapi.IsKVNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if hist.Versions == nil {
		hist.Versions = []VersionRecord{}
	}
	return &hist, nil
}

func (s *Store) saveHistory(id string, hist *VersionHistory) error {
	slices.SortFunc(hist.Versions, func(a, b VersionRecord) int {
		return cmp.Compare(a.Version, b.Version)
	})
	return s.kv.KVSet(histKey(id), hist)
}

// restoreHistoryIfUnchanged writes previous back only while the stored history
// is still the shortened document. The write is a compare-and-set against the
// bytes just read, so a version saved after that read is left in place. On
// that conflict the metadata error stands: the stored head stays at the last
// successful metadata write, which still names the version this call failed
// to remove.
func (s *Store) restoreHistoryIfUnchanged(id string, shortened, previous []VersionRecord) (bool, error) {
	var raw []byte
	if err := s.kv.KVGet(histKey(id), &raw); err != nil {
		if mmapi.IsKVNotFound(err) {
			return false, ErrNotFound
		}
		return false, err
	}
	var stored VersionHistory
	if err := json.Unmarshal(raw, &stored); err != nil {
		return false, err
	}
	if stored.Versions == nil {
		stored.Versions = []VersionRecord{}
	}
	if !sameVersionContent(stored.Versions, shortened) {
		return false, nil
	}
	head := stored.HeadVersion
	if head > 0 && !versionListed(previous, head) {
		head = 0
	}
	restored := &VersionHistory{
		Versions:      previous,
		NextVersion:   stored.NextVersion,
		HeadVersion:   head,
		PendingDelete: pendingStillNeeded(stored.PendingDelete, previous),
	}
	slices.SortFunc(restored.Versions, func(a, b VersionRecord) int {
		return cmp.Compare(a.Version, b.Version)
	})
	ok, err := s.kv.KVCompareAndSet(histKey(id), raw, restored)
	if err != nil {
		return false, err
	}
	return ok, nil
}

func sameVersionContent(a, b []VersionRecord) bool {
	return slices.EqualFunc(a, b, func(left, right VersionRecord) bool {
		return left.Version == right.Version && left.SHA256 == right.SHA256
	})
}

// ensureHistory returns the version history, migrating legacy unversioned
// blobs into a single VersionRecord when no history key exists yet.
func (s *Store) ensureHistory(entry *Entry) (*VersionHistory, error) {
	hist, err := s.loadHistory(entry.ID)
	if err == nil {
		return hist, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if entry.Version < 1 {
		entry.Version = 1
	}
	legacy := true
	if entry.ChunkCount > 0 {
		var probe []byte
		vErr := s.kv.KVGet(versionedBlobKey(entry.ID, entry.Version, 0), &probe)
		if vErr == nil {
			legacy = false
		} else if !mmapi.IsKVNotFound(vErr) {
			return nil, vErr
		}
	}
	hist = &VersionHistory{
		Versions:    []VersionRecord{versionRecordFromEntry(entry, legacy)},
		NextVersion: entry.Version + 1,
		HeadVersion: entry.Version,
	}
	if err := s.saveHistory(entry.ID, hist); err != nil {
		return nil, err
	}
	return hist, nil
}

func findVersion(hist *VersionHistory, version int) (VersionRecord, bool) {
	for _, rec := range hist.Versions {
		if rec.Version == version {
			return rec, true
		}
	}
	return VersionRecord{}, false
}

func maxVersion(hist *VersionHistory) int {
	highest := 0
	if hist == nil {
		return 0
	}
	for _, rec := range hist.Versions {
		if rec.Version > highest {
			highest = rec.Version
		}
	}
	return highest
}

// issueVersion is the next version number. It never goes backwards past
// NextVersion, so a pruned number is not issued again.
func issueVersion(hist *VersionHistory) int {
	next := maxVersion(hist) + 1
	if hist != nil && hist.NextVersion > next {
		next = hist.NextVersion
	}
	return next
}

func noteIssued(hist *VersionHistory, version int) {
	if hist != nil && version >= hist.NextVersion {
		hist.NextVersion = version + 1
	}
}

func versionListed(versions []VersionRecord, version int) bool {
	for _, rec := range versions {
		if rec.Version == version {
			return true
		}
	}
	return false
}

func pendingStillNeeded(pending, live []VersionRecord) []VersionRecord {
	if len(pending) == 0 {
		return nil
	}
	out := make([]VersionRecord, 0, len(pending))
	for _, rec := range pending {
		if !versionListed(live, rec.Version) {
			out = append(out, rec)
		}
	}
	return out
}

// versionsAfterPrune drops the oldest versions until maxKeep remain, and never
// drops a protected version. The live head and a rollback's selected version
// stay even when that makes the history longer than maxKeep.
func versionsAfterPrune(versions []VersionRecord, protect []int, maxKeep int) (pruned, kept []VersionRecord) {
	protected := make(map[int]bool, len(protect))
	for _, version := range protect {
		if version > 0 {
			protected[version] = true
		}
	}
	sorted := append([]VersionRecord(nil), versions...)
	slices.SortFunc(sorted, func(a, b VersionRecord) int {
		return cmp.Compare(a.Version, b.Version)
	})
	if maxKeep <= 0 || len(sorted) <= maxKeep {
		return nil, sorted
	}
	excess := len(sorted) - maxKeep
	kept = make([]VersionRecord, 0, len(sorted))
	for _, rec := range sorted {
		if excess > 0 && !protected[rec.Version] {
			pruned = append(pruned, rec)
			excess--
			continue
		}
		kept = append(kept, rec)
	}
	return pruned, kept
}

// pruneOldestVersions reloads history and compare-and-sets the shortened
// document, so a version committed after this call started is not overwritten.
// The stored head is kept. Blobs are removed only after that write lands.
func (s *Store) pruneOldestVersions(entry *Entry, maxKeep int) error {
	if entry == nil || maxKeep <= 0 {
		return nil
	}
	for range 8 {
		stored, _, err := s.loadMetaRaw(entry.ID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		hist, raw, err := s.loadHistoryRaw(entry.ID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		pruned, kept := versionsAfterPrune(hist.Versions, []int{stored.Version, hist.HeadVersion}, maxKeep)
		pending := pendingStillNeeded(hist.PendingDelete, kept)
		toDelete := append(append([]VersionRecord{}, pruned...), pending...)
		if len(toDelete) == 0 {
			return nil
		}
		next := &VersionHistory{
			Versions:      kept,
			NextVersion:   hist.NextVersion,
			HeadVersion:   hist.HeadVersion,
			PendingDelete: toDelete,
		}
		if next.HeadVersion == 0 {
			next.HeadVersion = stored.Version
		}
		if err := s.claimHistory(entry.ID, raw, next); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		fresh, _, freshErr := s.loadMetaRaw(entry.ID)
		if freshErr != nil && !errors.Is(freshErr, ErrNotFound) {
			return freshErr
		}
		if fresh != nil && versionListed(toDelete, fresh.Version) {
			if _, restoreErr := s.restoreHistoryIfUnchanged(entry.ID, kept, hist.Versions); restoreErr != nil {
				return restoreErr
			}
			continue
		}
		var failed []VersionRecord
		var first error
		for _, rec := range toDelete {
			if fresh != nil && rec.Version == fresh.Version {
				continue
			}
			if deleteErr := s.deleteVersionBlobKeys(rec, entry.ID); deleteErr != nil {
				failed = append(failed, rec)
				if first == nil {
					first = deleteErr
				}
			}
		}
		if len(failed) > 0 {
			return first
		}
		if clearErr := s.clearPendingDelete(entry.ID, toDelete); clearErr != nil {
			return clearErr
		}
		return nil
	}
	return ErrConflict
}

// clearPendingDelete drops the versions whose blobs were just deleted. A
// record that is still pending stays, and a conflict leaves the list in place
// so the next prune drains it again.
func (s *Store) clearPendingDelete(id string, deleted []VersionRecord) error {
	hist, raw, err := s.loadHistoryRaw(id)
	if err != nil {
		return err
	}
	if len(hist.PendingDelete) == 0 {
		return nil
	}
	remaining := make([]VersionRecord, 0, len(hist.PendingDelete))
	for _, rec := range hist.PendingDelete {
		if !versionListed(deleted, rec.Version) {
			remaining = append(remaining, rec)
		}
	}
	if len(remaining) == len(hist.PendingDelete) {
		return nil
	}
	if len(remaining) == 0 {
		remaining = nil
	}
	hist.PendingDelete = remaining
	if err := s.claimHistory(id, raw, hist); err != nil && !errors.Is(err, ErrConflict) {
		return err
	}
	return nil
}

// matchVersionMeta copies the head's title and description onto the version
// record. casContentHead can keep a title that landed during the upload, and
// the record was written from the caller's copy before that.
func (s *Store) matchVersionMeta(id string, version int, title, description string) error {
	for range 8 {
		hist, raw, err := s.loadHistoryRaw(id)
		if err != nil {
			return err
		}
		matched := false
		for i := range hist.Versions {
			if hist.Versions[i].Version != version {
				continue
			}
			if hist.Versions[i].Title == title && hist.Versions[i].Description == description {
				return nil
			}
			hist.Versions[i].Title = title
			hist.Versions[i].Description = description
			matched = true
			break
		}
		if !matched {
			return nil
		}
		if err := s.claimHistory(id, raw, hist); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrConflict
}

// ReplaceContent appends a new content version, keeping prior gzip blobs.
// maxKeep 0 retains every version; otherwise oldest versions are pruned.
func (s *Store) ReplaceContent(entry *Entry, content []byte, maxSize int64, maxKeep int) error {
	if entry == nil || !model.IsValidId(entry.ID) {
		return ErrInvalidInput
	}
	if err := validateTitle(entry.Title); err != nil {
		return err
	}
	if err := validateDescription(entry.Description); err != nil {
		return err
	}
	if int64(len(content)) == 0 {
		return ErrInvalidInput
	}
	if int64(len(content)) > maxSize {
		return ErrTooLarge
	}

	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	if sha == entry.SHA256 {
		if err := s.UpdateMeta(entry); err != nil {
			return err
		}
		if err := s.ensureOwnSHA(entry.ChannelID, sha, entry.ID); err != nil {
			return err
		}
		if _, histErr := s.ensureHistory(entry); histErr != nil {
			return histErr
		}
		if pruneErr := s.pruneOldestVersions(entry, maxKeep); pruneErr != nil {
			return fmt.Errorf("%w: %w", ErrCleanupDeferred, pruneErr)
		}
		return nil
	}
	if existing, err := s.FindBySHA(entry.ChannelID, sha); err == nil && existing != "" && existing != entry.ID {
		return fmt.Errorf("%w: %s", ErrDuplicate, existing)
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	chunks, chunkErr := gzipChunks(content)
	if chunkErr != nil {
		return chunkErr
	}
	if _, histErr := s.ensureHistory(entry); histErr != nil {
		return histErr
	}

	original := *entry
	for range 8 {
		*entry = original
		hist, raw, loadErr := s.loadHistoryRaw(entry.ID)
		if loadErr != nil {
			return loadErr
		}
		previous := append([]VersionRecord(nil), hist.Versions...)
		entry.Size = int64(len(content))
		entry.SHA256 = sha
		entry.Encoding = "gzip"
		entry.ChunkCount = len(chunks)
		entry.Version = issueVersion(hist)
		noteIssued(hist, entry.Version)
		hist.HeadVersion = entry.Version
		entry.SchemaVersion = schemaVersion
		rec := versionRecordFromEntry(entry, false)

		reserved, reserveErr := s.reserveSHA(entry.ChannelID, sha, entry.ID)
		if reserveErr != nil {
			*entry = original
			return reserveErr
		}
		hist.Versions = append(hist.Versions, rec)
		if claimErr := s.claimHistory(entry.ID, raw, hist); claimErr != nil {
			if relErr := s.releaseReservedSHA(entry.ChannelID, sha, entry.ID, reserved); relErr != nil {
				*entry = original
				return errors.Join(claimErr, relErr)
			}
			if errors.Is(claimErr, ErrConflict) {
				continue
			}
			*entry = original
			return claimErr
		}
		if writeErr := s.writeVersionBlobs(entry.ID, entry.Version, chunks); writeErr != nil {
			undoErr := s.undoClaimedVersion(entry.ChannelID, entry.ID, sha, entry.ID, previous, hist.Versions, rec, reserved)
			*entry = original
			if undoErr != nil {
				return errors.Join(writeErr, undoErr)
			}
			return writeErr
		}
		if metaErr := s.casContentHead(original.Version, original.SHA256, entry); metaErr != nil {
			undoErr := s.undoClaimedVersion(entry.ChannelID, entry.ID, sha, entry.ID, previous, hist.Versions, rec, reserved)
			*entry = original
			if undoErr != nil {
				return errors.Join(metaErr, undoErr)
			}
			return metaErr
		}
		if alignErr := s.matchVersionMeta(entry.ID, entry.Version, entry.Title, entry.Description); alignErr != nil {
			return fmt.Errorf("%w: %w", ErrCleanupDeferred, alignErr)
		}
		if original.SHA256 != "" && original.SHA256 != sha {
			_ = s.deleteSHAIfOwner(entry.ChannelID, original.SHA256, entry.ID)
		}
		if ownErr := s.ensureOwnSHA(entry.ChannelID, sha, entry.ID); ownErr != nil {
			return ownErr
		}
		if pruneErr := s.pruneOldestVersions(entry, maxKeep); pruneErr != nil {
			return fmt.Errorf("%w: %w", ErrCleanupDeferred, pruneErr)
		}
		return nil
	}
	*entry = original
	return ErrConflict
}

func (s *Store) loadHistoryRaw(id string) (*VersionHistory, []byte, error) {
	var raw []byte
	if err := s.kv.KVGet(histKey(id), &raw); err != nil {
		if mmapi.IsKVNotFound(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	var hist VersionHistory
	if err := json.Unmarshal(raw, &hist); err != nil {
		return nil, nil, err
	}
	if hist.Versions == nil {
		hist.Versions = []VersionRecord{}
	}
	return &hist, raw, nil
}

func (s *Store) claimHistory(id string, raw []byte, hist *VersionHistory) error {
	slices.SortFunc(hist.Versions, func(a, b VersionRecord) int {
		return cmp.Compare(a.Version, b.Version)
	})
	var old any
	if raw != nil {
		old = raw
	}
	ok, err := s.kv.KVCompareAndSet(histKey(id), old, hist)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

func (s *Store) undoClaimedVersion(channelID, id, sha, ownerID string, previous, claimed []VersionRecord, rec VersionRecord, reserved bool) error {
	restored, err := s.restoreHistoryIfUnchanged(id, claimed, previous)
	if err != nil {
		return err
	}
	if !restored {
		absent, dropErr := s.dropVersionRecord(id, rec.Version)
		if dropErr != nil || !absent {
			return dropErr
		}
	}
	s.deleteVersionBlobs(rec, id)
	return s.releaseReservedSHA(channelID, sha, ownerID, reserved)
}

// dropVersionRecord removes version from history with a compare-and-set retry.
// absent is true when the version is no longer stored.
func (s *Store) dropVersionRecord(id string, version int) (bool, error) {
	for range 8 {
		hist, raw, err := s.loadHistoryRaw(id)
		if errors.Is(err, ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		next := make([]VersionRecord, 0, len(hist.Versions))
		found := false
		for _, rec := range hist.Versions {
			if rec.Version == version {
				found = true
				continue
			}
			next = append(next, rec)
		}
		if !found {
			return true, nil
		}
		hist.Versions = next
		if claimErr := s.claimHistory(id, raw, hist); claimErr != nil {
			if errors.Is(claimErr, ErrConflict) {
				continue
			}
			return false, claimErr
		}
		return true, nil
	}
	return false, ErrConflict
}

// ListVersions returns content version metadata (no HTML bodies).
func (s *Store) ListVersions(id string) ([]VersionRecord, error) {
	entry, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	hist, err := s.ensureHistory(entry)
	if err != nil {
		return nil, err
	}
	out := append([]VersionRecord(nil), hist.Versions...)
	return out, nil
}

// VersionRecord returns the snapshot for version. version 0 means the current head.
func (s *Store) VersionRecord(entry *Entry, version int) (VersionRecord, error) {
	if entry == nil {
		return VersionRecord{}, ErrInvalidInput
	}
	hist, err := s.ensureHistory(entry)
	if err != nil {
		return VersionRecord{}, err
	}
	target := entry.Version
	if version > 0 {
		target = version
	}
	rec, ok := findVersion(hist, target)
	if !ok {
		return VersionRecord{}, ErrNotFound
	}
	return rec, nil
}

// Rollback sets head to an existing version without deleting newer history.
func (s *Store) Rollback(entry *Entry, toVersion int) error {
	if entry == nil || toVersion < 1 {
		return ErrInvalidInput
	}
	hist, err := s.ensureHistory(entry)
	if err != nil {
		return err
	}
	rec, ok := findVersion(hist, toVersion)
	if !ok {
		return ErrNotFound
	}
	if entry.Version == toVersion {
		if err := s.casHead(entry.Version, entry.SHA256, entry); err != nil {
			return err
		}
		return s.ensureOwnSHA(entry.ChannelID, entry.SHA256, entry.ID)
	}
	original := *entry
	oldSHA := entry.SHA256
	applyVersionToEntry(entry, rec)
	entry.UpdatedBy = original.UpdatedBy
	entry.UpdateAt = original.UpdateAt
	var reserved bool
	if entry.SHA256 != oldSHA {
		var reserveErr error
		reserved, reserveErr = s.reserveSHA(entry.ChannelID, entry.SHA256, entry.ID)
		if reserveErr != nil {
			*entry = original
			return reserveErr
		}
	}
	// Record the selected version in history before the metadata write, so a
	// prune already in flight cannot delete the blobs this rollback is about to use.
	marked, markErr := s.markHistoryHead(entry.ID, toVersion)
	if markErr != nil {
		if relErr := s.releaseReservedSHA(entry.ChannelID, entry.SHA256, entry.ID, reserved); relErr != nil {
			*entry = original
			return errors.Join(markErr, relErr)
		}
		*entry = original
		return markErr
	}
	applyVersionToEntry(entry, marked)
	entry.UpdatedBy = original.UpdatedBy
	entry.UpdateAt = original.UpdateAt
	if err := s.casHead(original.Version, original.SHA256, entry); err != nil {
		if relErr := s.releaseReservedSHA(entry.ChannelID, entry.SHA256, entry.ID, reserved); relErr != nil {
			*entry = original
			return errors.Join(err, relErr)
		}
		*entry = original
		return err
	}
	if oldSHA != "" && oldSHA != entry.SHA256 {
		_ = s.deleteSHAIfOwner(entry.ChannelID, oldSHA, entry.ID)
	}
	return s.ensureOwnSHA(entry.ChannelID, entry.SHA256, entry.ID)
}

// markHistoryHead records version as the selected head. A missing version is
// ErrNotFound, which is what a prune that already dropped it must produce.
func (s *Store) markHistoryHead(id string, version int) (VersionRecord, error) {
	for range 8 {
		hist, raw, err := s.loadHistoryRaw(id)
		if err != nil {
			return VersionRecord{}, err
		}
		rec, ok := findVersion(hist, version)
		if !ok {
			return VersionRecord{}, ErrNotFound
		}
		hist.HeadVersion = version
		if err := s.claimHistory(id, raw, hist); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return VersionRecord{}, err
		}
		return rec, nil
	}
	return VersionRecord{}, ErrConflict
}

// DeleteHead removes the current head version. If it was the only version the
// artifact is purged; otherwise head moves to the previous remaining version.
func (s *Store) DeleteHead(entry *Entry) error {
	if entry == nil {
		return ErrInvalidInput
	}
	if _, err := s.ensureHistory(entry); err != nil {
		return err
	}
	hist, raw, err := s.loadHistoryRaw(entry.ID)
	if err != nil {
		return err
	}
	if len(hist.Versions) <= 1 {
		return s.Purge(entry)
	}
	head, ok := findVersion(hist, entry.Version)
	if !ok {
		return ErrNotFound
	}
	remaining := make([]VersionRecord, 0, len(hist.Versions)-1)
	var previous VersionRecord
	foundPrev := false
	for _, rec := range hist.Versions {
		if rec.Version == entry.Version {
			continue
		}
		remaining = append(remaining, rec)
		if rec.Version < entry.Version && (!foundPrev || rec.Version > previous.Version) {
			previous = rec
			foundPrev = true
		}
	}
	if !foundPrev {
		previous = remaining[len(remaining)-1]
	}
	previousHistory := append([]VersionRecord(nil), hist.Versions...)
	var reserved bool
	if previous.SHA256 != "" && previous.SHA256 != entry.SHA256 {
		var reserveErr error
		reserved, reserveErr = s.reserveSHA(entry.ChannelID, previous.SHA256, entry.ID)
		if reserveErr != nil {
			return reserveErr
		}
	}
	nextHist := *hist
	nextHist.Versions = remaining
	nextHist.HeadVersion = previous.Version
	nextHist.PendingDelete = append(pendingStillNeeded(hist.PendingDelete, remaining), head)
	if err := s.claimHistory(entry.ID, raw, &nextHist); err != nil {
		if relErr := s.releaseReservedSHA(entry.ChannelID, previous.SHA256, entry.ID, reserved); relErr != nil {
			return errors.Join(err, relErr)
		}
		return err
	}
	oldSHA := entry.SHA256
	oldVersion := entry.Version
	applyVersionToEntry(entry, previous)
	if err := s.casHead(oldVersion, oldSHA, entry); err != nil {
		applyVersionToEntry(entry, head)
		if reserved {
			live, _, loadErr := s.loadMetaRaw(entry.ID)
			if loadErr == nil && live.SHA256 == previous.SHA256 {
				reserved = false
			}
		}
		relErr := s.releaseReservedSHA(entry.ChannelID, previous.SHA256, entry.ID, reserved)
		if _, restoreErr := s.restoreHistoryIfUnchanged(entry.ID, remaining, previousHistory); restoreErr != nil {
			return errors.Join(err, relErr, restoreErr)
		}
		if relErr != nil {
			return errors.Join(err, relErr)
		}
		return err
	}
	s.deleteVersionBlobs(head, entry.ID)
	if oldSHA != "" && oldSHA != entry.SHA256 {
		_ = s.deleteSHAIfOwner(entry.ChannelID, oldSHA, entry.ID)
	}
	return s.ensureOwnSHA(entry.ChannelID, entry.SHA256, entry.ID)
}
