// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	keyPrefixIdx  = "cart_idx_"
	keyPrefixMeta = "cart_meta_"
	keyPrefixBlob = "cart_blob_"
	keyPrefixSHA  = "cart_sha_"
	keyPrefixHist = "cart_hist_"
	keySiteCount  = "cart_site_count"
)

var (
	ErrNotFound       = errors.New("channel artifact not found")
	ErrConflict       = errors.New("channel artifact index conflict")
	ErrLimitReached   = errors.New("channel artifact limit reached")
	ErrDuplicate      = errors.New("identical artifact already saved")
	ErrInvalidInput   = errors.New("invalid channel artifact input")
	ErrTooLarge       = errors.New("channel artifact too large")
	ErrNotPermutation = errors.New("order is not a permutation of current ids")
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

func versionedBlobKey(id string, version, n int) string {
	return fmt.Sprintf("%s%s_v%d_%d", keyPrefixBlob, id, version, n)
}

func legacyBlobKey(id string, n int) string {
	return fmt.Sprintf("%s%s_%d", keyPrefixBlob, id, n)
}

func blobKeyFor(rec VersionRecord, id string, n int) string {
	if rec.LegacyUnversionedBlobs {
		return legacyBlobKey(id, n)
	}
	return versionedBlobKey(id, rec.Version, n)
}

func (s *Store) getIndex(channelID string) (*ChannelIndex, []byte, error) {
	var raw []byte
	err := s.kv.KVGet(indexKey(channelID), &raw)
	if mmapi.IsKVNotFound(err) {
		empty := &ChannelIndex{Version: 1, IDs: []string{}}
		return empty, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var idx ChannelIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, nil, err
	}
	if idx.IDs == nil {
		idx.IDs = []string{}
	}
	return &idx, raw, nil
}

func (s *Store) setIndexCAS(channelID string, oldRaw []byte, idx *ChannelIndex) error {
	// Pass a true nil interface when the key is absent so CAS means "create".
	// A typed-nil []byte is not nil as any and fails compare-and-set fakes.
	var old any
	if oldRaw != nil {
		old = oldRaw
	}
	ok, err := s.kv.KVCompareAndSet(indexKey(channelID), old, idx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

func (s *Store) getSiteCount() (int, []byte, error) {
	var raw []byte
	err := s.kv.KVGet(keySiteCount, &raw)
	if mmapi.IsKVNotFound(err) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	var count int
	if err := json.Unmarshal(raw, &count); err != nil {
		return 0, nil, err
	}
	return count, raw, nil
}

func (s *Store) setSiteCountCAS(oldRaw []byte, count int) error {
	var old any
	if oldRaw != nil {
		old = oldRaw
	}
	ok, err := s.kv.KVCompareAndSet(keySiteCount, old, count)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

func (s *Store) adjustSiteCount(delta int) error {
	if delta == 0 {
		return nil
	}
	for range 8 {
		count, oldRaw, err := s.getSiteCount()
		if err != nil {
			return err
		}
		next := max(count+delta, 0)
		if err := s.setSiteCountCAS(oldRaw, next); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrConflict
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

func (s *Store) FindBySHA(channelID, sha string) (string, error) {
	var id string
	err := s.kv.KVGet(shaKey(channelID, sha), &id)
	if mmapi.IsKVNotFound(err) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) List(channelID string) ([]*Entry, error) {
	idx, _, err := s.getIndex(channelID)
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(idx.IDs))
	for _, id := range idx.IDs {
		entry, err := s.Get(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

// ForEachChannelIndex invokes fn for every channel ID that has a cart_idx_ key.
// IDs are collected before fn runs so a purge that deletes keys cannot skip
// later pages of the KV listing.
func (s *Store) ForEachChannelIndex(fn func(channelID string) error) error {
	ids, err := s.channelIndexIDs()
	if err != nil {
		return err
	}
	for _, channelID := range ids {
		if err := fn(channelID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) channelIndexIDs() ([]string, error) {
	const pageSize = 100
	prefix := keyPrefixIdx
	var ids []string
	for page := 0; ; page++ {
		keys, err := s.kv.KVList(page, pageSize)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			return ids, nil
		}
		for _, key := range keys {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			channelID := strings.TrimPrefix(key, prefix)
			if channelID == "" {
				continue
			}
			ids = append(ids, channelID)
		}
		if len(keys) < pageSize {
			return ids, nil
		}
	}
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

func versionRecordFromEntry(entry *Entry, legacy bool) VersionRecord {
	return VersionRecord{
		Version:                entry.Version,
		SHA256:                 entry.SHA256,
		Size:                   entry.Size,
		ChunkCount:             entry.ChunkCount,
		SourcePostID:           entry.SourcePostID,
		SourceFileID:           entry.SourceFileID,
		SourceFileName:         entry.SourceFileName,
		SourceBotUserID:        entry.SourceBotUserID,
		SourceAgentDisplayName: entry.SourceAgentDisplayName,
		SourceRootPostID:       entry.SourceRootPostID,
		Title:                  entry.Title,
		Description:            entry.Description,
		UpdatedBy:              entry.UpdatedBy,
		UpdateAt:               entry.UpdateAt,
		LegacyUnversionedBlobs: legacy,
	}
}

func applyVersionToEntry(entry *Entry, rec VersionRecord) {
	entry.Version = rec.Version
	entry.SHA256 = rec.SHA256
	entry.Size = rec.Size
	entry.ChunkCount = rec.ChunkCount
	entry.SourcePostID = rec.SourcePostID
	entry.SourceFileID = rec.SourceFileID
	entry.SourceFileName = rec.SourceFileName
	entry.SourceBotUserID = rec.SourceBotUserID
	entry.SourceAgentDisplayName = rec.SourceAgentDisplayName
	entry.SourceRootPostID = rec.SourceRootPostID
	if rec.Title != "" {
		entry.Title = rec.Title
	}
	entry.Description = rec.Description
	entry.UpdatedBy = rec.UpdatedBy
	entry.UpdateAt = rec.UpdateAt
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
	sort.Slice(hist.Versions, func(i, j int) bool {
		return hist.Versions[i].Version < hist.Versions[j].Version
	})
	return s.kv.KVSet(histKey(id), hist)
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
		Versions: []VersionRecord{versionRecordFromEntry(entry, legacy)},
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
	for _, rec := range hist.Versions {
		if rec.Version > highest {
			highest = rec.Version
		}
	}
	return highest
}

func (s *Store) writeVersionBlobs(id string, version int, chunks [][]byte) error {
	for i, chunk := range chunks {
		if err := s.kv.KVSet(versionedBlobKey(id, version, i), chunk); err != nil {
			s.deleteVersionBlobs(VersionRecord{Version: version, ChunkCount: i, LegacyUnversionedBlobs: false}, id)
			return err
		}
	}
	return nil
}

func (s *Store) deleteVersionBlobs(rec VersionRecord, id string) {
	for i := 0; i < rec.ChunkCount; i++ {
		_ = s.kv.KVDelete(blobKeyFor(rec, id, i))
	}
}

func (s *Store) deleteAllVersionBlobs(hist *VersionHistory, id string) {
	for _, rec := range hist.Versions {
		s.deleteVersionBlobs(rec, id)
	}
}

func (s *Store) pruneOldestVersions(entry *Entry, hist *VersionHistory, maxKeep int) error {
	if maxKeep <= 0 || len(hist.Versions) <= maxKeep {
		return nil
	}
	sort.Slice(hist.Versions, func(i, j int) bool {
		return hist.Versions[i].Version < hist.Versions[j].Version
	})
	for len(hist.Versions) > maxKeep {
		oldest := hist.Versions[0]
		if oldest.Version == entry.Version {
			break
		}
		s.deleteVersionBlobs(oldest, entry.ID)
		hist.Versions = hist.Versions[1:]
	}
	return s.saveHistory(entry.ID, hist)
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
		Versions: []VersionRecord{versionRecordFromEntry(entry, false)},
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
	if err := s.kv.KVSet(shaKey(entry.ChannelID, sha), entry.ID); err != nil {
		_ = s.kv.KVDelete(metaKey(entry.ID))
		s.deleteAllVersionBlobs(hist, entry.ID)
		_ = s.kv.KVDelete(histKey(entry.ID))
		return err
	}

	siteCounted := false
	cleanup := func() {
		if siteCounted {
			_ = s.adjustSiteCount(-1)
		}
		_ = s.kv.KVDelete(shaKey(entry.ChannelID, sha))
		_ = s.kv.KVDelete(metaKey(entry.ID))
		s.deleteAllVersionBlobs(hist, entry.ID)
		_ = s.kv.KVDelete(histKey(entry.ID))
	}

	if maxSitewide > 0 {
		for range 8 {
			count, oldRaw, err := s.getSiteCount()
			if err != nil {
				cleanup()
				return err
			}
			if count >= maxSitewide {
				cleanup()
				return ErrLimitReached
			}
			if err := s.setSiteCountCAS(oldRaw, count+1); err != nil {
				if errors.Is(err, ErrConflict) {
					continue
				}
				cleanup()
				return err
			}
			siteCounted = true
			break
		}
		if !siteCounted {
			cleanup()
			return ErrConflict
		}
	}

	for range 8 {
		idx, oldRaw, err := s.getIndex(entry.ChannelID)
		if err != nil {
			cleanup()
			return err
		}
		if len(idx.IDs) >= maxPerChannel {
			cleanup()
			return ErrLimitReached
		}
		idx.IDs = append(idx.IDs, entry.ID)
		if err := s.setIndexCAS(entry.ChannelID, oldRaw, idx); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			cleanup()
			return err
		}
		return nil
	}
	cleanup()
	return ErrConflict
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
	return s.kv.KVSet(metaKey(entry.ID), entry)
}

// ReplaceContent appends a new content version, keeping prior gzip blobs.
// maxKeep 0 retains every version; otherwise oldest versions are pruned.
func (s *Store) ReplaceContent(entry *Entry, content []byte, maxSize int64, maxKeep int) error {
	if entry == nil || !model.IsValidId(entry.ID) {
		return ErrInvalidInput
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
		return nil
	}
	if existing, err := s.FindBySHA(entry.ChannelID, sha); err == nil && existing != "" && existing != entry.ID {
		return fmt.Errorf("%w: %s", ErrDuplicate, existing)
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	chunks, err := gzipChunks(content)
	if err != nil {
		return err
	}

	hist, err := s.ensureHistory(entry)
	if err != nil {
		return err
	}
	nextVersion := maxVersion(hist) + 1
	oldSHA := entry.SHA256

	if err := s.writeVersionBlobs(entry.ID, nextVersion, chunks); err != nil {
		return err
	}

	entry.Size = int64(len(content))
	entry.SHA256 = sha
	entry.Encoding = "gzip"
	entry.ChunkCount = len(chunks)
	entry.Version = nextVersion
	entry.SchemaVersion = schemaVersion

	rec := versionRecordFromEntry(entry, false)
	hist.Versions = append(hist.Versions, rec)
	if err := s.saveHistory(entry.ID, hist); err != nil {
		s.deleteVersionBlobs(rec, entry.ID)
		return err
	}
	if err := s.kv.KVSet(metaKey(entry.ID), entry); err != nil {
		return err
	}
	if oldSHA != "" && oldSHA != sha {
		_ = s.kv.KVDelete(shaKey(entry.ChannelID, oldSHA))
	}
	if err := s.kv.KVSet(shaKey(entry.ChannelID, sha), entry.ID); err != nil {
		return err
	}
	return s.pruneOldestVersions(entry, hist, maxKeep)
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

// ReadContentAt reassembles HTML for a specific version. version 0 means head.
func (s *Store) ReadContentAt(entry *Entry, version int) ([]byte, error) {
	if entry == nil {
		return nil, ErrInvalidInput
	}
	hist, err := s.ensureHistory(entry)
	if err != nil {
		return nil, err
	}
	target := entry.Version
	if version > 0 {
		target = version
	}
	rec, ok := findVersion(hist, target)
	if !ok {
		return nil, ErrNotFound
	}
	return s.readVersionContent(entry.ID, rec)
}

// ReadContent reassembles and gunzips the current head HTML.
func (s *Store) ReadContent(entry *Entry) ([]byte, error) {
	return s.ReadContentAt(entry, 0)
}

func (s *Store) readVersionContent(id string, rec VersionRecord) ([]byte, error) {
	if rec.ChunkCount < 1 {
		return nil, ErrInvalidInput
	}
	var compressed bytes.Buffer
	for i := 0; i < rec.ChunkCount; i++ {
		var chunk []byte
		if err := s.kv.KVGet(blobKeyFor(rec, id, i), &chunk); err != nil {
			return nil, err
		}
		if _, err := compressed.Write(chunk); err != nil {
			return nil, err
		}
	}
	zr, err := gzip.NewReader(&compressed)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != rec.SHA256 {
		return nil, errors.New("channel artifact content hash mismatch")
	}
	return body, nil
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
		return nil
	}
	oldSHA := entry.SHA256
	applyVersionToEntry(entry, rec)
	if err := s.kv.KVSet(metaKey(entry.ID), entry); err != nil {
		return err
	}
	if oldSHA != "" && oldSHA != entry.SHA256 {
		_ = s.kv.KVDelete(shaKey(entry.ChannelID, oldSHA))
	}
	return s.kv.KVSet(shaKey(entry.ChannelID, entry.SHA256), entry.ID)
}

// DeleteHead removes the current head version. If it was the only version the
// artifact is purged; otherwise head moves to the previous remaining version.
func (s *Store) DeleteHead(entry *Entry) error {
	if entry == nil {
		return ErrInvalidInput
	}
	hist, err := s.ensureHistory(entry)
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
	s.deleteVersionBlobs(head, entry.ID)
	hist.Versions = remaining
	if err := s.saveHistory(entry.ID, hist); err != nil {
		return err
	}
	oldSHA := entry.SHA256
	applyVersionToEntry(entry, previous)
	if err := s.kv.KVSet(metaKey(entry.ID), entry); err != nil {
		return err
	}
	if oldSHA != "" && oldSHA != entry.SHA256 {
		_ = s.kv.KVDelete(shaKey(entry.ChannelID, oldSHA))
	}
	return s.kv.KVSet(shaKey(entry.ChannelID, entry.SHA256), entry.ID)
}

// Purge removes the artifact, every version blob, history, and sha index entry.
func (s *Store) Purge(entry *Entry) error {
	if entry == nil {
		return ErrInvalidInput
	}
	hist, histErr := s.ensureHistory(entry)
	if histErr != nil && !errors.Is(histErr, ErrNotFound) {
		return histErr
	}
	for range 8 {
		idx, oldRaw, err := s.getIndex(entry.ChannelID)
		if err != nil {
			return err
		}
		next := make([]string, 0, len(idx.IDs))
		for _, id := range idx.IDs {
			if id != entry.ID {
				next = append(next, id)
			}
		}
		idx.IDs = next
		if err := s.setIndexCAS(entry.ChannelID, oldRaw, idx); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		_ = s.kv.KVDelete(shaKey(entry.ChannelID, entry.SHA256))
		_ = s.kv.KVDelete(metaKey(entry.ID))
		_ = s.kv.KVDelete(histKey(entry.ID))
		if hist != nil {
			s.deleteAllVersionBlobs(hist, entry.ID)
		} else {
			s.deleteVersionBlobs(versionRecordFromEntry(entry, true), entry.ID)
			s.deleteVersionBlobs(versionRecordFromEntry(entry, false), entry.ID)
		}
		_ = s.adjustSiteCount(-1)
		return nil
	}
	return ErrConflict
}

// Delete removes all versions of the artifact (alias for Purge).
func (s *Store) Delete(entry *Entry) error {
	return s.Purge(entry)
}

func (s *Store) Reorder(channelID string, ids []string) error {
	for range 8 {
		idx, oldRaw, err := s.getIndex(channelID)
		if err != nil {
			return err
		}
		if !sameIDSet(idx.IDs, ids) {
			return ErrNotPermutation
		}
		idx.IDs = append([]string(nil), ids...)
		if err := s.setIndexCAS(channelID, oldRaw, idx); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrConflict
}

func sameIDSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, id := range a {
		counts[id]++
	}
	for _, id := range b {
		counts[id]--
		if counts[id] < 0 {
			return false
		}
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

func gzipChunks(content []byte) ([][]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(content); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	compressed := buf.Bytes()
	chunks := make([][]byte, 0, (len(compressed)/MaxChunkBytes)+1)
	for len(compressed) > 0 {
		n := min(MaxChunkBytes, len(compressed))
		chunk := make([]byte, n)
		copy(chunk, compressed[:n])
		chunks = append(chunks, chunk)
		compressed = compressed[n:]
	}
	return chunks, nil
}

// VersionKeyString formats a version int for export maps.
func VersionKeyString(version int) string {
	return strconv.Itoa(version)
}
