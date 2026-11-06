// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
)

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
// The delete is retried because its compare value is this artifact's id, so a
// retry cannot remove another artifact's lookup. A remaining error is returned.
func (s *Store) releaseReservedSHA(channelID, sha, ownerID string, created bool) error {
	if !created {
		return nil
	}
	var last error
	for range 8 {
		if err := s.deleteSHAIfOwner(channelID, sha, ownerID); err != nil {
			last = err
			continue
		}
		return nil
	}
	return last
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
		if entry.Purging {
			continue
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
		start := min(cursor.Skip, len(keys))
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

// commitNewArtifact increments the site counter, then appends the channel index.
// The counter is one compare-and-set key and is an advisory cap.
func (s *Store) commitNewArtifact(entry *Entry, maxPerChannel, maxSitewide int) error {
	counted := false
	for range 8 {
		count, oldRaw, err := s.getSiteCount()
		if err != nil {
			return err
		}
		if maxSitewide > 0 && count >= maxSitewide {
			return ErrLimitReached
		}
		if err := s.setSiteCountCAS(oldRaw, count+1); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		counted = true
		break
	}
	if !counted {
		return ErrConflict
	}
	if err := s.appendChannelIndex(entry, maxPerChannel); err != nil {
		_ = s.adjustSiteCount(-1)
		return err
	}
	return nil
}

func (s *Store) appendChannelIndex(entry *Entry, maxPerChannel int) error {
	for range 8 {
		idx, oldRaw, err := s.getIndex(entry.ChannelID)
		if err != nil {
			return err
		}
		if len(idx.IDs) >= maxPerChannel {
			return ErrLimitReached
		}
		idx.IDs = append(idx.IDs, entry.ID)
		if err := s.setIndexCAS(entry.ChannelID, oldRaw, idx); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return err
		}
		return nil
	}
	return ErrConflict
}

func (s *Store) deleteIndexCAS(channelID string, oldRaw []byte) error {
	if oldRaw == nil {
		return nil
	}
	ok, err := s.kv.KVCompareAndSet(indexKey(channelID), oldRaw, nil)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

// finishPurgeIndex drops id from the channel index and decrements the site
// counter. An empty index key is deleted. A failed decrement puts the id back
// so a retry can count it down.
func (s *Store) finishPurgeIndex(entry *Entry) error {
	for range 8 {
		idx, oldRaw, err := s.getIndex(entry.ChannelID)
		if err != nil {
			return err
		}
		if oldRaw == nil {
			return nil
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
		if len(next) == 0 {
			if err := s.deleteIndexCAS(entry.ChannelID, oldRaw); err != nil {
				if errors.Is(err, ErrConflict) {
					continue
				}
				return err
			}
		} else {
			idx.IDs = next
			if err := s.setIndexCAS(entry.ChannelID, oldRaw, idx); err != nil {
				if errors.Is(err, ErrConflict) {
					continue
				}
				return err
			}
		}
		if !removed {
			return nil
		}
		if err := s.adjustSiteCount(-1); err != nil {
			if putErr := s.restorePurgedIndexID(entry.ChannelID, entry.ID); putErr != nil {
				return errors.Join(err, putErr)
			}
			return err
		}
		return nil
	}
	return ErrConflict
}

func (s *Store) restorePurgedIndexID(channelID, id string) error {
	if id == "" {
		return nil
	}
	for range 8 {
		idx, oldRaw, err := s.getIndex(channelID)
		if err != nil {
			return err
		}
		if slices.Contains(idx.IDs, id) {
			return nil
		}
		idx.IDs = append(idx.IDs, id)
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

// forgetIndexID removes an index id whose metadata is already gone, and deletes
// the index key once nothing remains.
func (s *Store) forgetIndexID(channelID, id string) error {
	return s.finishPurgeIndex(&Entry{ID: id, ChannelID: channelID})
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
