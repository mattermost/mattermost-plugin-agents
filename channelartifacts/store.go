// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
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

// deleteSHAIfOwner removes the lookup only while it still names ownerID.
// A nil new value is an atomic delete in the plugin KV store. A lookup another
// artifact has taken over is left in place.
func (s *Store) deleteSHAIfOwner(channelID, sha, ownerID string) error {
	if sha == "" || ownerID == "" {
		return nil
	}
	_, err := s.kv.KVCompareAndSet(shaKey(channelID, sha), ownerID, nil)
	return err
}

// ensureOwnSHA writes the lookup only when the key is absent. A nil old value
// is a create-if-absent compare-and-set, so a lookup this artifact or another
// one already holds is left in place.
func (s *Store) ensureOwnSHA(channelID, sha, ownerID string) error {
	if sha == "" || ownerID == "" {
		return nil
	}
	_, err := s.kv.KVCompareAndSet(shaKey(channelID, sha), nil, ownerID)
	return err
}

// reserveSHA claims sha for ownerID before a head write. created is true only
// when this call inserted the lookup. A missing key is retried. An existing
// lookup is kept when this artifact already owns it, and is otherwise
// ErrDuplicate with nothing written.
func (s *Store) reserveSHA(channelID, sha, ownerID string) (created bool, err error) {
	if sha == "" || ownerID == "" {
		return false, nil
	}
	key := shaKey(channelID, sha)
	for range 8 {
		claimed, claimErr := s.kv.KVCompareAndSet(key, nil, ownerID)
		if claimErr != nil {
			return false, claimErr
		}
		if claimed {
			return true, nil
		}
		existing, findErr := s.FindBySHA(channelID, sha)
		if findErr != nil && !errors.Is(findErr, ErrNotFound) {
			return false, findErr
		}
		if existing == ownerID {
			return false, nil
		}
		if existing == "" {
			continue
		}
		return false, fmt.Errorf("%w: %s", ErrDuplicate, existing)
	}
	return false, ErrConflict
}

// releaseReservedSHA removes a lookup only when this call created it.
func (s *Store) releaseReservedSHA(channelID, sha, ownerID string, created bool) {
	if !created {
		return
	}
	_ = s.deleteSHAIfOwner(channelID, sha, ownerID)
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

type retentionCursor struct {
	Page int `json:"page"`
	Skip int `json:"skip"`
}

// RetentionChannelIDs returns at most limit channel indexes, resuming from the
// cursor saved by the previous call. A short page wraps the cursor to the
// start, so a channel is not left behind once the listing is scanned again.
// limit <= 0 uses defaultRetentionBatch. Each call reads at most limit KV pages.
func (s *Store) RetentionChannelIDs(limit int) ([]string, error) {
	if limit <= 0 {
		limit = defaultRetentionBatch
	}
	var cursor retentionCursor
	if err := s.kv.KVGet(keyRetentionCursor, &cursor); err != nil && !mmapi.IsKVNotFound(err) {
		return nil, err
	}
	ids := make([]string, 0, limit)
	for pages := 0; len(ids) < limit && pages < limit; pages++ {
		keys, err := s.kv.KVList(cursor.Page, retentionPageSize)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			cursor = retentionCursor{}
			break
		}
		start := cursor.Skip
		if start > len(keys) {
			start = len(keys)
		}
		consumed := start
		for i := start; i < len(keys) && len(ids) < limit; i++ {
			consumed = i + 1
			channelID, ok := channelIDFromIndexKey(keys[i])
			if ok {
				ids = append(ids, channelID)
			}
		}
		switch {
		case consumed < len(keys):
			cursor = retentionCursor{Page: cursor.Page, Skip: consumed}
		case len(keys) < retentionPageSize:
			cursor = retentionCursor{}
		default:
			cursor = retentionCursor{Page: cursor.Page + 1}
		}
		if cursor.Page == 0 && cursor.Skip == 0 {
			break
		}
	}
	if err := s.kv.KVSet(keyRetentionCursor, cursor); err != nil {
		return nil, err
	}
	return ids, nil
}

func channelIDFromIndexKey(key string) (string, bool) {
	if !strings.HasPrefix(key, keyPrefixIdx) {
		return "", false
	}
	channelID := strings.TrimPrefix(key, keyPrefixIdx)
	if channelID == "" {
		return "", false
	}
	return channelID, true
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
	restored := &VersionHistory{Versions: previous}
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
	_ = s.deleteVersionBlobKeys(rec, id)
}

func (s *Store) deleteVersionBlobKeys(rec VersionRecord, id string) error {
	var first error
	for i := 0; i < rec.ChunkCount; i++ {
		if err := s.kv.KVDelete(blobKeyFor(rec, id, i)); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Store) deleteAllVersionBlobs(hist *VersionHistory, id string) {
	_ = s.deleteAllVersionBlobKeys(hist, id)
}

func (s *Store) deleteAllVersionBlobKeys(hist *VersionHistory, id string) error {
	var first error
	for _, rec := range hist.Versions {
		if err := s.deleteVersionBlobKeys(rec, id); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Store) pruneOldestVersions(entry *Entry, hist *VersionHistory, maxKeep int) error {
	if maxKeep <= 0 || len(hist.Versions) <= maxKeep {
		return nil
	}
	slices.SortFunc(hist.Versions, func(a, b VersionRecord) int {
		return cmp.Compare(a.Version, b.Version)
	})
	var pruned []VersionRecord
	for len(hist.Versions) > maxKeep {
		oldest := hist.Versions[0]
		if oldest.Version == entry.Version {
			break
		}
		pruned = append(pruned, oldest)
		hist.Versions = hist.Versions[1:]
	}
	if err := s.saveHistory(entry.ID, hist); err != nil {
		return err
	}
	for _, rec := range pruned {
		s.deleteVersionBlobs(rec, entry.ID)
	}
	return nil
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

	siteCounted := false
	cleanup := func() {
		if siteCounted {
			_ = s.adjustSiteCount(-1)
		}
		_ = s.deleteSHAIfOwner(entry.ChannelID, sha, entry.ID)
		_ = s.kv.KVDelete(metaKey(entry.ID))
		s.deleteAllVersionBlobs(hist, entry.ID)
		_ = s.kv.KVDelete(histKey(entry.ID))
	}

	for range 8 {
		count, oldRaw, err := s.getSiteCount()
		if err != nil {
			cleanup()
			return err
		}
		if maxSitewide > 0 && count >= maxSitewide {
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
		if err := s.UpdateMeta(entry); err != nil {
			return err
		}
		return s.ensureOwnSHA(entry.ChannelID, sha, entry.ID)
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
		entry.Version = maxVersion(hist) + 1
		entry.SchemaVersion = schemaVersion
		rec := versionRecordFromEntry(entry, false)

		reserved, reserveErr := s.reserveSHA(entry.ChannelID, sha, entry.ID)
		if reserveErr != nil {
			*entry = original
			return reserveErr
		}
		hist.Versions = append(hist.Versions, rec)
		if claimErr := s.claimHistory(entry.ID, raw, hist); claimErr != nil {
			s.releaseReservedSHA(entry.ChannelID, sha, entry.ID, reserved)
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
		if metaErr := s.kv.KVSet(metaKey(entry.ID), entry); metaErr != nil {
			undoErr := s.undoClaimedVersion(entry.ChannelID, entry.ID, sha, entry.ID, previous, hist.Versions, rec, reserved)
			*entry = original
			if undoErr != nil {
				return errors.Join(metaErr, undoErr)
			}
			return metaErr
		}
		if original.SHA256 != "" && original.SHA256 != sha {
			_ = s.deleteSHAIfOwner(entry.ChannelID, original.SHA256, entry.ID)
		}
		if ownErr := s.ensureOwnSHA(entry.ChannelID, sha, entry.ID); ownErr != nil {
			return ownErr
		}
		return s.pruneOldestVersions(entry, hist, maxKeep)
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
	if err != nil || !restored {
		return err
	}
	s.deleteVersionBlobs(rec, id)
	s.releaseReservedSHA(channelID, sha, ownerID, reserved)
	return nil
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
	if err := s.kv.KVSet(metaKey(entry.ID), entry); err != nil {
		s.releaseReservedSHA(entry.ChannelID, entry.SHA256, entry.ID, reserved)
		*entry = original
		return err
	}
	if oldSHA != "" && oldSHA != entry.SHA256 {
		_ = s.deleteSHAIfOwner(entry.ChannelID, oldSHA, entry.ID)
	}
	return s.ensureOwnSHA(entry.ChannelID, entry.SHA256, entry.ID)
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
	previousHistory := append([]VersionRecord(nil), hist.Versions...)
	hist.Versions = remaining
	if err := s.saveHistory(entry.ID, hist); err != nil {
		return err
	}
	oldSHA := entry.SHA256
	applyVersionToEntry(entry, previous)
	if err := s.kv.KVSet(metaKey(entry.ID), entry); err != nil {
		applyVersionToEntry(entry, head)
		if _, restoreErr := s.restoreHistoryIfUnchanged(entry.ID, remaining, previousHistory); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	s.deleteVersionBlobs(head, entry.ID)
	if oldSHA != "" && oldSHA != entry.SHA256 {
		_ = s.deleteSHAIfOwner(entry.ChannelID, oldSHA, entry.ID)
	}
	return s.ensureOwnSHA(entry.ChannelID, entry.SHA256, entry.ID)
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
	if err := s.deleteSHAIfOwner(entry.ChannelID, entry.SHA256, entry.ID); err != nil {
		return err
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
	if err := s.kv.KVDelete(histKey(entry.ID)); err != nil {
		return err
	}
	if err := s.kv.KVDelete(metaKey(entry.ID)); err != nil {
		return err
	}
	for range 8 {
		idx, oldRaw, err := s.getIndex(entry.ChannelID)
		if err != nil {
			return err
		}
		next := make([]string, 0, len(idx.IDs))
		removed := false
		for _, id := range idx.IDs {
			if id == entry.ID {
				removed = true
				continue
			}
			next = append(next, id)
		}
		idx.IDs = next
		if err := s.setIndexCAS(entry.ChannelID, oldRaw, idx); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		if removed {
			_ = s.adjustSiteCount(-1)
		}
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
