// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	keyPrefixIdx          = "cart_idx_"
	keyPrefixMeta         = "cart_meta_"
	keyPrefixBlob         = "cart_blob_"
	keyPrefixSHA          = "cart_sha_"
	keyPrefixHist         = "cart_hist_"
	keySiteCount          = "cart_site_count"
	keyRetentionCursor    = "cart_retention_cursor"
	retentionPageSize     = 100
	defaultRetentionBatch = 100
)

var (
	ErrNotFound        = errors.New("channel artifact not found")
	ErrConflict        = errors.New("channel artifact index conflict")
	ErrLimitReached    = errors.New("channel artifact limit reached")
	ErrDuplicate       = errors.New("identical artifact already saved")
	ErrInvalidInput    = errors.New("invalid channel artifact input")
	ErrTooLarge        = errors.New("channel artifact too large")
	ErrNotPermutation  = errors.New("order is not a permutation of current ids")
	ErrCleanupDeferred = errors.New("channel artifact cleanup deferred")
)

// Store persists channel artifact catalog entries and gzipped HTML blobs in plugin KV.
type Store struct {
	kv mmapi.Client
}

func NewStore(kv mmapi.Client) *Store {
	return &Store{kv: kv}
}

func indexKey(channelID string) string { return keyPrefixIdx + channelID }
func metaKey(id string) string         { return keyPrefixMeta + id }
func histKey(id string) string         { return keyPrefixHist + id }
func shaKey(channelID, sha string) string {
	return keyPrefixSHA + channelID + "_" + sha
}

func validateTitle(title string) error {
	n := utf8.RuneCountInString(title)
	if n == 0 || n > MaxTitleRunes {
		return ErrInvalidInput
	}
	return nil
}

func validateDescription(description string) error {
	if utf8.RuneCountInString(description) > MaxDescriptionRunes {
		return ErrInvalidInput
	}
	return nil
}

func resolveMaxPerChannel(maxPerChannel int) int {
	if maxPerChannel <= 0 {
		return DefaultMaxPerChannel
	}
	return maxPerChannel
}

func (s *Store) Get(id string) (*Entry, error) {
	var entry Entry
	err := s.kv.KVGet(metaKey(id), &entry)
	if mmapi.IsKVNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// Save writes a new artifact: blobs, meta, history, sha index, and channel index.
// maxPerChannel 0 uses DefaultMaxPerChannel. maxSitewide 0 means unlimited.
func (s *Store) Save(entry *Entry, content []byte, maxSize int64, maxPerChannel, maxSitewide int) error {
	if entry == nil || !model.IsValidId(entry.ChannelID) || !model.IsValidId(entry.ID) {
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
	maxPerChannel = resolveMaxPerChannel(maxPerChannel)

	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	if existing, err := s.FindBySHA(entry.ChannelID, sha); err == nil && existing != "" {
		return fmt.Errorf("%w: %s", ErrDuplicate, existing)
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	chunks, err := gzipChunks(content)
	if err != nil {
		return err
	}

	if entry.Version < 1 {
		entry.Version = 1
	}
	entry.Size = int64(len(content))
	entry.SHA256 = sha
	entry.Encoding = "gzip"
	entry.ChunkCount = len(chunks)
	entry.SchemaVersion = schemaVersion

	if err := s.writeVersionBlobs(entry.ID, entry.Version, chunks); err != nil {
		return err
	}
	hist := &VersionHistory{
		Versions:    []VersionRecord{versionRecordFromEntry(entry, false)},
		NextVersion: entry.Version + 1,
		HeadVersion: entry.Version,
	}
	if err := s.saveHistory(entry.ID, hist); err != nil {
		s.deleteVersionBlobs(hist.Versions[0], entry.ID)
		return err
	}
	if err := s.kv.KVSet(metaKey(entry.ID), entry); err != nil {
		s.deleteAllVersionBlobs(hist, entry.ID)
		_ = s.kv.KVDelete(histKey(entry.ID))
		return err
	}
	claimed, claimErr := s.kv.KVCompareAndSet(shaKey(entry.ChannelID, sha), nil, entry.ID)
	if claimErr != nil || !claimed {
		_ = s.kv.KVDelete(metaKey(entry.ID))
		s.deleteAllVersionBlobs(hist, entry.ID)
		_ = s.kv.KVDelete(histKey(entry.ID))
		if claimErr != nil {
			return claimErr
		}
		existing, findErr := s.FindBySHA(entry.ChannelID, sha)
		if findErr != nil || existing == "" {
			return ErrDuplicate
		}
		return fmt.Errorf("%w: %s", ErrDuplicate, existing)
	}

	if err := s.commitNewArtifact(entry, maxPerChannel, maxSitewide); err != nil {
		idx, _, indexErr := s.getIndex(entry.ChannelID)
		if indexErr != nil || !slices.Contains(idx.IDs, entry.ID) {
			_ = s.deleteSHAIfOwner(entry.ChannelID, sha, entry.ID)
			_ = s.kv.KVDelete(metaKey(entry.ID))
			s.deleteAllVersionBlobs(hist, entry.ID)
			_ = s.kv.KVDelete(histKey(entry.ID))
		}
		return err
	}
	return nil
}

func (s *Store) loadMetaRaw(id string) (*Entry, []byte, error) {
	var raw []byte
	err := s.kv.KVGet(metaKey(id), &raw)
	if mmapi.IsKVNotFound(err) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, nil, err
	}
	return &entry, raw, nil
}

func (s *Store) saveMetaCAS(id string, oldRaw []byte, entry *Entry) error {
	var old any
	if oldRaw != nil {
		old = oldRaw
	}
	ok, err := s.kv.KVCompareAndSet(metaKey(id), old, entry)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

// casContentHead writes content fields onto the stored head. Title and
// description stay as stored, so a patch that landed during the upload is kept.
func (s *Store) casContentHead(expectedVersion int, expectedSHA string, entry *Entry) error {
	for range 8 {
		stored, raw, err := s.loadMetaRaw(entry.ID)
		if err != nil {
			return err
		}
		if stored.Purging || stored.Version != expectedVersion || stored.SHA256 != expectedSHA {
			return ErrConflict
		}
		title, description := stored.Title, stored.Description
		stored.VersionSnapshot = entry.VersionSnapshot
		stored.Title = title
		stored.Description = description
		stored.Encoding = entry.Encoding
		stored.SchemaVersion = entry.SchemaVersion
		if err := s.saveMetaCAS(entry.ID, raw, stored); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		*entry = *stored
		return nil
	}
	return ErrConflict
}

// casHead writes entry only while the stored head still has expectedVersion and
// expectedSHA. A newer content write returns ErrConflict instead of being overwritten.
func (s *Store) casHead(expectedVersion int, expectedSHA string, entry *Entry) error {
	for range 8 {
		stored, raw, err := s.loadMetaRaw(entry.ID)
		if err != nil {
			return err
		}
		if stored.Purging || stored.Version != expectedVersion || stored.SHA256 != expectedSHA {
			return ErrConflict
		}
		if err := s.saveMetaCAS(entry.ID, raw, entry); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrConflict
}

// Patch applies a rename to the current head and retries when the metadata
// compare-and-set loses. apply reports a permanent error, including a
// validation failure, and that error is returned without another attempt.
func (s *Store) Patch(id string, apply func(*Entry) error) (*Entry, error) {
	if apply == nil {
		return nil, ErrInvalidInput
	}
	for range 8 {
		entry, raw, err := s.loadMetaRaw(id)
		if err != nil {
			return nil, err
		}
		if applyErr := apply(entry); applyErr != nil {
			return nil, applyErr
		}
		if saveErr := s.saveMetaCAS(id, raw, entry); saveErr != nil {
			if errors.Is(saveErr, ErrConflict) {
				continue
			}
			return nil, saveErr
		}
		return entry, nil
	}
	return nil, ErrConflict
}

func (s *Store) UpdateMeta(entry *Entry) error {
	if entry == nil || !model.IsValidId(entry.ID) {
		return ErrInvalidInput
	}
	if err := validateTitle(entry.Title); err != nil {
		return err
	}
	if err := validateDescription(entry.Description); err != nil {
		return err
	}
	return s.casHead(entry.Version, entry.SHA256, entry)
}

// Purge removes the artifact, every version blob, history, and sha index entry.
func (s *Store) Purge(entry *Entry) error {
	if entry == nil {
		return ErrInvalidInput
	}
	if err := s.markPurging(entry, 0); err != nil {
		return err
	}
	return s.finishPurge(entry)
}

// purgeIfHeadUnchanged purges only while the stored head is still entry.Version.
// A replacement that committed after the caller counted one version returns
// ErrConflict instead of deleting that replacement with the artifact.
func (s *Store) purgeIfHeadUnchanged(entry *Entry) error {
	if entry == nil {
		return ErrInvalidInput
	}
	if err := s.markPurging(entry, entry.Version); err != nil {
		return err
	}
	return s.finishPurge(entry)
}

func (s *Store) finishPurge(entry *Entry) error {
	hist, histErr := s.ensureHistory(entry)
	if histErr != nil && !errors.Is(histErr, ErrNotFound) {
		return histErr
	}
	if hist != nil {
		if err := s.deleteAllVersionBlobKeys(hist, entry.ID); err != nil {
			return err
		}
	} else {
		if err := s.deleteVersionBlobKeys(versionRecordFromEntry(entry, true), entry.ID); err != nil {
			return err
		}
		if err := s.deleteVersionBlobKeys(versionRecordFromEntry(entry, false), entry.ID); err != nil {
			return err
		}
	}
	if err := s.deleteSHAIfOwner(entry.ChannelID, entry.SHA256, entry.ID); err != nil {
		return err
	}
	if err := s.kv.KVDelete(histKey(entry.ID)); err != nil {
		return err
	}
	// Drop the index id before the metadata row. A failed count puts the id
	// back while the row still exists, so a retry can load it. A row delete
	// that fails after the drop puts the id and the count back for the sweep.
	dropped, indexErr := s.finishPurgeIndex(entry)
	if indexErr != nil {
		return indexErr
	}
	if deleteErr := s.kv.KVDelete(metaKey(entry.ID)); deleteErr != nil {
		if !dropped {
			return deleteErr
		}
		if putErr := s.restorePurgedIndexID(entry.ChannelID, entry.ID); putErr != nil {
			return errors.Join(deleteErr, putErr)
		}
		if countErr := s.adjustSiteCount(1); countErr != nil {
			return errors.Join(deleteErr, countErr)
		}
		return deleteErr
	}
	return nil
}

// markPurging sets the purge flag. expectedVersion 0 accepts any head. Any
// other value refuses a head that has since moved, so the caller can delete
// that head instead of purging the artifact.
func (s *Store) markPurging(entry *Entry, expectedVersion int) error {
	for range 8 {
		stored, raw, err := s.loadMetaRaw(entry.ID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if expectedVersion != 0 && stored.Version != expectedVersion {
			return ErrConflict
		}
		if stored.Purging {
			entry.Purging = true
			return nil
		}
		stored.Purging = true
		if err := s.saveMetaCAS(entry.ID, raw, stored); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		entry.Purging = true
		return nil
	}
	return ErrConflict
}

// Delete removes all versions of the artifact (alias for Purge).
func (s *Store) Delete(entry *Entry) error {
	return s.Purge(entry)
}

// VersionKeyString formats a version int for export maps.
func VersionKeyString(version int) string {
	return strconv.Itoa(version)
}
