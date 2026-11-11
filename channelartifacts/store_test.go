// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

type memoryKV struct {
	mu                 sync.Mutex
	data               map[string][]byte
	permissionAll      bool
	readAll            bool
	channels           map[string]*model.Channel
	histSetCount       int
	failHistSet        int
	failNextMeta       bool
	conflictNextMeta   bool
	onFailNextMeta     func(data map[string][]byte)
	beforeHistCAS      func(data map[string][]byte)
	beforeMetaCAS      func(data map[string][]byte)
	unreadable         map[string]bool
	beforeSHAClaim     func(data map[string][]byte, key string)
	rejectHistCAS      bool
	histCASCount       int
	failHistCASAt      int
	failHistCASErrorAt int
	failSHADeletes     int
	failMetaDelete     bool
	failBlobDeletes    int
}

func newMemoryKV() *memoryKV {
	return &memoryKV{data: map[string][]byte{}, channels: map[string]*model.Channel{}}
}

func (kv *memoryKV) snapshot() map[string][]byte {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	return copyKVData(kv.data)
}

func (kv *memoryKV) restore(src map[string][]byte) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	restoreKVData(kv.data, src)
}

func copyKVData(src map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(src))
	for key, value := range src {
		out[key] = append([]byte(nil), value...)
	}
	return out
}

func restoreKVData(dst, src map[string][]byte) {
	for key := range dst {
		delete(dst, key)
	}
	for key, value := range src {
		dst[key] = append([]byte(nil), value...)
	}
}

func encodeKV(value any) ([]byte, error) {
	if raw, ok := value.([]byte); ok {
		return raw, nil
	}
	return json.Marshal(value)
}

func (kv *memoryKV) KVGet(key string, value any) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	raw, ok := kv.data[key]
	if !ok {
		return mmapi.ErrKVNotFound
	}
	if bytesOut, ok := value.(*[]byte); ok {
		*bytesOut = append([]byte(nil), raw...)
		return nil
	}
	return json.Unmarshal(raw, value)
}

func (kv *memoryKV) KVSet(key string, value any) error {
	if strings.HasPrefix(key, keyPrefixMeta) {
		kv.mu.Lock()
		fail := kv.failNextMeta
		hook := kv.onFailNextMeta
		if fail {
			kv.failNextMeta = false
			kv.onFailNextMeta = nil
		}
		kv.mu.Unlock()
		if fail {
			if hook != nil {
				kv.mu.Lock()
				hook(kv.data)
				kv.mu.Unlock()
			}
			return errors.New("kv set failed")
		}
	}
	if strings.HasPrefix(key, keyPrefixHist) {
		kv.mu.Lock()
		kv.histSetCount++
		fail := kv.failHistSet > 0 && kv.histSetCount == kv.failHistSet
		kv.mu.Unlock()
		if fail {
			return errors.New("kv set failed")
		}
	}
	raw, err := encodeKV(value)
	if err != nil {
		return err
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.data[key] = raw
	return nil
}

func (kv *memoryKV) KVDelete(key string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if kv.failMetaDelete && strings.HasPrefix(key, keyPrefixMeta) {
		kv.failMetaDelete = false
		return errors.New("kv delete failed")
	}
	if kv.failBlobDeletes > 0 && strings.HasPrefix(key, keyPrefixBlob) {
		kv.failBlobDeletes--
		return errors.New("blob delete failed")
	}
	delete(kv.data, key)
	return nil
}

func (kv *memoryKV) KVCompareAndSet(key string, oldValue, newValue any) (bool, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if strings.HasPrefix(key, keyPrefixMeta) && newValue != nil && (kv.failNextMeta || kv.conflictNextMeta) {
		fail := kv.failNextMeta
		kv.failNextMeta = false
		kv.conflictNextMeta = false
		hook := kv.onFailNextMeta
		kv.onFailNextMeta = nil
		if hook != nil {
			hook(kv.data)
		}
		if fail {
			return false, errors.New("kv set failed")
		}
		return false, nil
	}
	if strings.HasPrefix(key, keyPrefixHist) {
		kv.histCASCount++
		if kv.failHistCASErrorAt > 0 && kv.histCASCount == kv.failHistCASErrorAt {
			return false, errors.New("kv set failed")
		}
		if kv.failHistCASAt > 0 && kv.histCASCount == kv.failHistCASAt {
			return false, nil
		}
	}
	if strings.HasPrefix(key, keyPrefixHist) && kv.rejectHistCAS {
		return false, nil
	}
	if newValue == nil && strings.HasPrefix(key, keyPrefixSHA) && kv.failSHADeletes > 0 {
		kv.failSHADeletes--
		return false, errors.New("sha delete failed")
	}
	if strings.HasPrefix(key, keyPrefixMeta) && newValue != nil && kv.beforeMetaCAS != nil {
		hook := kv.beforeMetaCAS
		kv.beforeMetaCAS = nil
		hook(kv.data)
	}
	if strings.HasPrefix(key, keyPrefixHist) && kv.beforeHistCAS != nil {
		hook := kv.beforeHistCAS
		kv.beforeHistCAS = nil
		hook(kv.data)
	}
	if oldValue == nil && strings.HasPrefix(key, keyPrefixSHA) && kv.beforeSHAClaim != nil {
		kv.beforeSHAClaim(kv.data, key)
		kv.beforeSHAClaim = nil
	}
	current, exists := kv.data[key]
	if oldValue == nil {
		if exists {
			return false, nil
		}
	} else {
		oldRaw, err := encodeKV(oldValue)
		if err != nil {
			return false, err
		}
		if !exists || !bytes.Equal(current, oldRaw) {
			return false, nil
		}
	}
	if newValue == nil {
		delete(kv.data, key)
		return true, nil
	}
	newRaw, err := encodeKV(newValue)
	if err != nil {
		return false, err
	}
	kv.data[key] = newRaw
	return true, nil
}

func (kv *memoryKV) KVSetWithExpiry(key string, value any, _ time.Duration) error {
	return kv.KVSet(key, value)
}

func (kv *memoryKV) KVCompareAndSetWithExpiry(key string, oldValue, newValue any, _ time.Duration) (bool, error) {
	return kv.KVCompareAndSet(key, oldValue, newValue)
}

func (kv *memoryKV) KVList(page, perPage int) ([]string, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	keys := make([]string, 0, len(kv.data))
	for key := range kv.data {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	start := page * perPage
	if start >= len(keys) {
		return nil, nil
	}
	end := min(start+perPage, len(keys))
	return keys[start:end], nil
}

func (kv *memoryKV) GetUser(string) (*model.User, error) { return nil, nil }
func (kv *memoryKV) GetPost(string) (*model.Post, error) { return nil, nil }
func (kv *memoryKV) AddReaction(*model.Reaction) error   { return nil }
func (kv *memoryKV) GetPostThread(string) (*model.PostList, error) {
	return nil, nil
}
func (kv *memoryKV) GetPostsSince(string, int64) (*model.PostList, error) { return nil, nil }
func (kv *memoryKV) GetPostsBefore(string, string, int, int) (*model.PostList, error) {
	return nil, nil
}
func (kv *memoryKV) CreatePost(*model.Post) error { return nil }
func (kv *memoryKV) UpdatePost(*model.Post) error { return nil }
func (kv *memoryKV) DM(string, string, *model.Post) error {
	return nil
}
func (kv *memoryKV) GetTeam(string) (*model.Team, error) { return nil, nil }
func (kv *memoryKV) GetChannel(channelID string) (*model.Channel, error) {
	if ch, ok := kv.channels[channelID]; ok {
		return ch, nil
	}
	return &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil
}
func (kv *memoryKV) GetDirectChannel(string, string) (*model.Channel, error) {
	return nil, nil
}
func (kv *memoryKV) PublishWebSocketEvent(string, map[string]any, *model.WebsocketBroadcast) {
}
func (kv *memoryKV) GetConfig() *model.Config { return &model.Config{} }
func (kv *memoryKV) LogError(string, ...any)  {}
func (kv *memoryKV) LogWarn(string, ...any)   {}
func (kv *memoryKV) LogDebug(string, ...any)  {}
func (kv *memoryKV) GetUserByUsername(string) (*model.User, error) {
	return nil, nil
}
func (kv *memoryKV) GetUserStatus(string) (*model.Status, error) { return nil, nil }
func (kv *memoryKV) HasPermissionTo(string, *model.Permission) bool {
	return kv.permissionAll
}
func (kv *memoryKV) GetPluginStatus(string) (*model.PluginStatus, error) {
	return nil, nil
}
func (kv *memoryKV) PluginHTTP(*http.Request) *http.Response { return nil }
func (kv *memoryKV) GetChannelByName(string, string, bool) (*model.Channel, error) {
	return nil, nil
}
func (kv *memoryKV) HasPermissionToChannel(_, channelID string, _ *model.Permission) bool {
	if kv.unreadable[channelID] {
		return false
	}
	return kv.permissionAll || kv.readAll
}
func (kv *memoryKV) HasPermissionToFileAction(string, string, string) bool { return false }
func (kv *memoryKV) GetFileInfo(string) (*model.FileInfo, error)           { return nil, nil }
func (kv *memoryKV) GetFile(string) (io.ReadCloser, error)                 { return nil, nil }
func (kv *memoryKV) UploadFile(io.Reader, string, string) (*model.FileInfo, error) {
	return nil, nil
}
func (kv *memoryKV) SendEphemeralPost(string, *model.Post) {}

var _ mmapi.Client = (*memoryKV)(nil)

func testEntry(channelID string) *Entry {
	return &Entry{
		ID:             model.NewId(),
		ChannelID:      channelID,
		TeamID:         model.NewId(),
		Title:          "Demo",
		SourcePostID:   model.NewId(),
		SourceFileID:   model.NewId(),
		SourceFileName: "demo.html",
		CreatedBy:      model.NewId(),
		CreateAt:       model.GetMillis(),
		UpdateAt:       model.GetMillis(),
		UpdatedBy:      model.NewId(),
		Version:        1,
	}
}

func TestStoreSaveReplaceRollbackDeleteHeadPurge(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	channelID := model.NewId()
	entry := testEntry(channelID)
	entry.SourceRootPostID = entry.SourcePostID

	require.NoError(t, store.Save(entry, []byte("<html>v1</html>"), 1024, 0, 0))
	require.Equal(t, 1, entry.Version)

	body, err := store.ReadContent(entry)
	require.NoError(t, err)
	require.Equal(t, "<html>v1</html>", string(body))

	v1SHA := entry.SHA256
	entry.SourcePostID = model.NewId()
	entry.SourceFileID = model.NewId()
	entry.SourceFileName = "demo2.html"
	entry.UpdateAt = model.GetMillis()
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>v2</html>"), 1024, 0))
	require.Equal(t, 2, entry.Version)
	require.NotEqual(t, v1SHA, entry.SHA256)

	oldBody, err := store.ReadContentAt(entry, 1)
	require.NoError(t, err)
	require.Equal(t, "<html>v1</html>", string(oldBody))
	headBody, err := store.ReadContentAt(entry, 0)
	require.NoError(t, err)
	require.Equal(t, "<html>v2</html>", string(headBody))

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)

	require.NoError(t, store.Rollback(entry, 1))
	require.Equal(t, 1, entry.Version)
	require.Equal(t, v1SHA, entry.SHA256)
	rolled, err := store.ReadContent(entry)
	require.NoError(t, err)
	require.Equal(t, "<html>v1</html>", string(rolled))
	versions, err = store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)

	require.NoError(t, store.DeleteHead(entry))
	require.Equal(t, 2, entry.Version)
	afterHead, err := store.ReadContent(entry)
	require.NoError(t, err)
	require.Equal(t, "<html>v2</html>", string(afterHead))
	versions, err = store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)

	require.NoError(t, store.Purge(entry))
	_, err = store.Get(entry.ID)
	require.ErrorIs(t, err, ErrNotFound)
	listed, err := store.List(channelID)
	require.NoError(t, err)
	require.Empty(t, listed)
}

func TestStoreMaxPerChannelAndSitewide(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	channelID := model.NewId()

	first := testEntry(channelID)
	require.NoError(t, store.Save(first, []byte("<html>a</html>"), 1024, 1, 1))

	second := testEntry(channelID)
	err := store.Save(second, []byte("<html>b</html>"), 1024, 1, 0)
	require.ErrorIs(t, err, ErrLimitReached)

	otherChannel := model.NewId()
	third := testEntry(otherChannel)
	err = store.Save(third, []byte("<html>c</html>"), 1024, 10, 1)
	require.ErrorIs(t, err, ErrLimitReached)

	require.NoError(t, store.Purge(first))
	require.NoError(t, store.Save(third, []byte("<html>c</html>"), 1024, 10, 1))
}

func TestSaveCountsArtifactsWhileTheSiteLimitIsOff(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	first := testEntry(model.NewId())
	require.NoError(t, store.Save(first, []byte("<html>a</html>"), 1024, 10, 0))

	second := testEntry(model.NewId())
	err := store.Save(second, []byte("<html>b</html>"), 1024, 10, 1)
	require.ErrorIs(t, err, ErrLimitReached)
}

func TestSecondPurgeDoesNotDropTheSiteCount(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	channelID := model.NewId()
	first := testEntry(channelID)
	second := testEntry(channelID)
	require.NoError(t, store.Save(first, []byte("<html>a</html>"), 1024, 10, 2))
	require.NoError(t, store.Save(second, []byte("<html>b</html>"), 1024, 10, 2))

	require.NoError(t, store.Purge(first))
	require.NoError(t, store.Purge(first))

	third := testEntry(model.NewId())
	require.NoError(t, store.Save(third, []byte("<html>c</html>"), 1024, 10, 2))
	fourth := testEntry(model.NewId())
	err := store.Save(fourth, []byte("<html>d</html>"), 1024, 10, 2)
	require.ErrorIs(t, err, ErrLimitReached)
}

func TestPurgeDeletesSHALookupOnlyWhileThisArtifactOwnsIt(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	channelID := model.NewId()
	entry := testEntry(channelID)
	require.NoError(t, store.Save(entry, []byte("<html>a</html>"), 1024, 0, 0))

	other := model.NewId()
	require.NoError(t, kv.KVSet(shaKey(channelID, entry.SHA256), other))
	require.NoError(t, store.Purge(entry))

	owner, err := store.FindBySHA(channelID, entry.SHA256)
	require.NoError(t, err)
	require.Equal(t, other, owner)

	again := testEntry(channelID)
	require.NoError(t, store.Save(again, []byte("<html>b</html>"), 1024, 0, 0))
	require.NoError(t, store.Purge(again))
	_, err = store.FindBySHA(channelID, again.SHA256)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestReplaceContentRestoresAMissingSHALookup(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	body := []byte("<html>1</html>")
	require.NoError(t, store.Save(entry, body, 1024, 0, 0))
	require.NoError(t, kv.KVDelete(shaKey(entry.ChannelID, entry.SHA256)))

	require.NoError(t, store.ReplaceContent(entry, body, 1024, 0))
	owner, err := store.FindBySHA(entry.ChannelID, entry.SHA256)
	require.NoError(t, err)
	require.Equal(t, entry.ID, owner)

	other := model.NewId()
	require.NoError(t, kv.KVSet(shaKey(entry.ChannelID, entry.SHA256), other))
	require.NoError(t, store.ReplaceContent(entry, body, 1024, 0))
	owner, err = store.FindBySHA(entry.ChannelID, entry.SHA256)
	require.NoError(t, err)
	require.Equal(t, other, owner)
}

func TestSaveDuplicateClaimLeavesTheExistingLookup(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	owner := model.NewId()
	kv.beforeSHAClaim = func(data map[string][]byte, key string) {
		raw, err := json.Marshal(owner)
		if err != nil {
			return
		}
		data[key] = raw
	}

	err := store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0)
	require.ErrorIs(t, err, ErrDuplicate)

	found, findErr := store.FindBySHA(entry.ChannelID, entry.SHA256)
	require.NoError(t, findErr)
	require.Equal(t, owner, found)
	_, getErr := store.Get(entry.ID)
	require.ErrorIs(t, getErr, ErrNotFound)
}

func TestRollbackRestoresAMissingSHALookup(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))
	require.NoError(t, store.Rollback(entry, 1))
	require.NoError(t, kv.KVDelete(shaKey(entry.ChannelID, entry.SHA256)))

	require.NoError(t, store.Rollback(entry, 1))
	owner, err := store.FindBySHA(entry.ChannelID, entry.SHA256)
	require.NoError(t, err)
	require.Equal(t, entry.ID, owner)
}

func TestDeleteHeadRestoresHistoryWhenMetaSaveFails(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))

	kv.failNextMeta = true
	require.Error(t, store.DeleteHead(entry))

	body, err := store.ReadContentAt(entry, 2)
	require.NoError(t, err)
	require.Equal(t, "<html>2</html>", string(body))
	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, []int{versions[0].Version, versions[1].Version})
}

func TestDeleteHeadLeavesNewerHistoryWhenMetaSaveFails(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))

	kv.failNextMeta = true
	kv.onFailNextMeta = func(data map[string][]byte) {
		raw, err := json.Marshal(VersionHistory{Versions: []VersionRecord{
			{Version: 1, SHA256: "old"},
			{Version: 3, SHA256: "newer"},
		}})
		if err != nil {
			return
		}
		for key := range data {
			if strings.HasPrefix(key, keyPrefixHist) {
				data[key] = raw
			}
		}
	}
	require.Error(t, store.DeleteHead(entry))

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1, 3}, []int{versions[0].Version, versions[1].Version})
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 2, stored.Version)
}

func TestDeleteHeadLeavesHistoryWrittenDuringRestore(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))

	kv.failNextMeta = true
	kv.beforeHistCAS = func(map[string][]byte) {
		kv.beforeHistCAS = func(data map[string][]byte) {
			raw, err := json.Marshal(VersionHistory{Versions: []VersionRecord{
				{Version: 1, SHA256: "old"},
				{Version: 3, SHA256: "during-restore"},
			}})
			if err != nil {
				return
			}
			for key := range data {
				if strings.HasPrefix(key, keyPrefixHist) {
					data[key] = raw
				}
			}
		}
	}
	require.Error(t, store.DeleteHead(entry))

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1, 3}, []int{versions[0].Version, versions[1].Version})
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 2, stored.Version)
}

func TestStoreVersionsToKeepPrunesOldest(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	for i := 2; i <= 4; i++ {
		entry.UpdateAt = model.GetMillis()
		require.NoError(t, store.ReplaceContent(entry, []byte("<html>"+strings.Repeat("x", i)+"</html>"), 1024, 2))
	}
	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	require.Equal(t, 3, versions[0].Version)
	require.Equal(t, 4, versions[1].Version)
	_, err = store.ReadContentAt(entry, 1)
	require.ErrorIs(t, err, ErrNotFound)
	hist, err := store.loadHistory(entry.ID)
	require.NoError(t, err)
	require.Empty(t, hist.PendingDelete)
}

func TestPruneKeepsBlobsWhenHistorySaveFails(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 2))

	kv.histCASCount = 0
	kv.failHistCASErrorAt = 2
	err := store.ReplaceContent(entry, []byte("<html>3</html>"), 1024, 2)
	require.ErrorIs(t, err, ErrCleanupDeferred)
	require.Equal(t, 3, entry.Version)

	body, err := store.ReadContentAt(entry, 1)
	require.NoError(t, err)
	require.Equal(t, "<html>1</html>", string(body))
}

func TestDeleteHeadKeepsBlobsWhenHistorySaveFails(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))

	kv.histCASCount = 0
	kv.failHistCASAt = 1
	require.Error(t, store.DeleteHead(entry))

	body, err := store.ReadContentAt(entry, 2)
	require.NoError(t, err)
	require.Equal(t, "<html>2</html>", string(body))
}

func TestStoreLegacyUnversionedBlobsReadable(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	channelID := model.NewId()
	entry := testEntry(channelID)
	content := []byte("<html>legacy</html>")
	chunks, err := gzipChunks(content)
	require.NoError(t, err)
	entry.Size = int64(len(content))
	sum := sha256Hex(content)
	entry.SHA256 = sum
	entry.Encoding = "gzip"
	entry.ChunkCount = len(chunks)
	entry.Version = 1
	entry.SchemaVersion = schemaVersion
	for i, chunk := range chunks {
		require.NoError(t, kv.KVSet(legacyBlobKey(entry.ID, i), chunk))
	}
	require.NoError(t, kv.KVSet(metaKey(entry.ID), entry))
	require.NoError(t, kv.KVSet(shaKey(channelID, sum), entry.ID))
	idx := &ChannelIndex{Version: 1, IDs: []string{entry.ID}}
	require.NoError(t, kv.KVSet(indexKey(channelID), idx))

	body, err := store.ReadContent(entry)
	require.NoError(t, err)
	require.Equal(t, string(content), string(body))
	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.True(t, versions[0].LegacyUnversionedBlobs)

	entry.UpdateAt = model.GetMillis()
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>new</html>"), 1024, 0))
	legacyBody, err := store.ReadContentAt(entry, 1)
	require.NoError(t, err)
	require.Equal(t, string(content), string(legacyBody))
	newBody, err := store.ReadContentAt(entry, 2)
	require.NoError(t, err)
	require.Equal(t, "<html>new</html>", string(newBody))
}

func TestStoreLineageFieldsAndForEachChannelIndex(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	channelID := model.NewId()
	entry := testEntry(channelID)
	rootID := model.NewId()
	entry.SourceRootPostID = rootID
	entry.SourceFileID = model.NewId()
	require.NoError(t, store.Save(entry, []byte("<html>lin</html>"), 1024, 0, 0))

	got, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, rootID, got.SourceRootPostID)
	require.Equal(t, entry.SourceFileID, got.SourceFileID)

	var seen []string
	require.NoError(t, store.ForEachChannelIndex(func(id string) error {
		seen = append(seen, id)
		return nil
	}))
	require.Contains(t, seen, channelID)
}

func TestReplaceContentPersistsMetadataWhenTheHTMLIsUnchanged(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	body := []byte("<html>1</html>")
	require.NoError(t, store.Save(entry, body, 1024, 0, 0))

	entry.Title = "Renamed"
	require.NoError(t, store.ReplaceContent(entry, body, 1024, 0))
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, "Renamed", stored.Title)
	require.Equal(t, 1, stored.Version)

	kv.failNextMeta = true
	entry.Title = "Not stored"
	require.Error(t, store.ReplaceContent(entry, body, 1024, 0))
	stored, err = store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, "Renamed", stored.Title)
}

func TestReplaceContentDropsAVersionWhenMetaSaveFails(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))

	kv.failNextMeta = true
	require.Error(t, store.ReplaceContent(entry, []byte("<html>3</html>"), 1024, 0))

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, []int{versions[0].Version, versions[1].Version})
	_, err = store.ReadContentAt(entry, 3)
	require.ErrorIs(t, err, ErrNotFound)

	svc := NewService(store, kv, allowAllMembers{})
	exported, err := svc.ExportChannel(entry.ChannelID)
	require.NoError(t, err)
	require.Len(t, exported.Entries, 1)
	require.NotContains(t, exported.Entries[0].ContentBase64ByVersion, VersionKeyString(3))
}

func TestReplaceContentDoesNotWriteWhenTheVersionClaimLoses(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))
	sha := entry.SHA256

	kv.rejectHistCAS = true
	err := store.ReplaceContent(entry, []byte("<html>3</html>"), 1024, 0)
	require.ErrorIs(t, err, ErrConflict)
	require.Equal(t, 2, entry.Version)

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, []int{versions[0].Version, versions[1].Version})
	kv.mu.Lock()
	var keys []string
	for key := range kv.data {
		keys = append(keys, key)
	}
	kv.mu.Unlock()
	for _, key := range keys {
		require.NotContains(t, key, "_v3_")
	}
	owner, err := store.FindBySHA(entry.ChannelID, sha)
	require.NoError(t, err)
	require.Equal(t, entry.ID, owner)
	_, err = store.FindBySHA(entry.ChannelID, sha256Hex([]byte("<html>3</html>")))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestReplaceContentDoesNotWriteWhenTheSHAReservationLoses(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	other := model.NewId()
	body := []byte("<html>2</html>")
	kv.beforeSHAClaim = func(data map[string][]byte, key string) {
		raw, err := json.Marshal(other)
		if err != nil {
			return
		}
		data[key] = raw
	}

	err := store.ReplaceContent(entry, body, 1024, 0)
	require.ErrorIs(t, err, ErrDuplicate)
	require.Equal(t, 1, entry.Version)

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1}, []int{versions[0].Version})
	owner, err := store.FindBySHA(entry.ChannelID, sha256Hex(body))
	require.NoError(t, err)
	require.Equal(t, other, owner)
}

func TestRollbackKeepsTheCallerStampInTheHeadWrite(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))

	entry.UpdatedBy = "actor"
	entry.UpdateAt = 42
	require.NoError(t, store.Rollback(entry, 1))
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.Version)
	require.Equal(t, "actor", stored.UpdatedBy)
	require.Equal(t, int64(42), stored.UpdateAt)

	entry.UpdatedBy = "later"
	entry.UpdateAt = 99
	kv.failNextMeta = true
	require.Error(t, store.Rollback(entry, 2))
	stored, err = store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.Version)
	require.Equal(t, "actor", stored.UpdatedBy)
	require.Equal(t, int64(42), stored.UpdateAt)
}

func TestPurgeLeavesTheArtifactReadableWhenSHADeleteFails(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))

	kv.failSHADeletes = 1
	require.Error(t, store.Purge(entry))

	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.True(t, stored.Purging)
	owner, err := store.FindBySHA(entry.ChannelID, entry.SHA256)
	require.NoError(t, err)
	require.Equal(t, entry.ID, owner)
	listed, err := store.List(entry.ChannelID)
	require.NoError(t, err)
	require.Empty(t, listed)

	require.NoError(t, store.Purge(entry))
	_, err = store.Get(entry.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestPurgeRetriesCleanupAfterAMetaDeleteFailure(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 10, 1))

	kv.failMetaDelete = true
	require.Error(t, store.Purge(entry))
	_, err := store.FindBySHA(entry.ChannelID, entry.SHA256)
	require.ErrorIs(t, err, ErrNotFound)
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.True(t, stored.Purging)
	listed, err := store.List(entry.ChannelID)
	require.NoError(t, err)
	require.Empty(t, listed)

	require.NoError(t, store.Purge(entry))
	_, err = store.Get(entry.ID)
	require.ErrorIs(t, err, ErrNotFound)
	listed, err = store.List(entry.ChannelID)
	require.NoError(t, err)
	require.Empty(t, listed)

	again := testEntry(model.NewId())
	require.NoError(t, store.Save(again, []byte("<html>2</html>"), 1024, 10, 1))
}

func TestRetentionChannelIDsResumesAndWraps(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	var want []string
	for range 3 {
		channelID := model.NewId()
		want = append(want, channelID)
		require.NoError(t, store.Save(testEntry(channelID), []byte("<html>a</html>"), 1024, 0, 0))
	}

	all, err := store.RetentionChannelIDs(10)
	require.NoError(t, err)
	require.ElementsMatch(t, want, all)

	seen := map[string]bool{}
	for range 20 {
		if len(seen) == len(want) {
			break
		}
		batch, batchErr := store.RetentionChannelIDs(1)
		require.NoError(t, batchErr)
		require.LessOrEqual(t, len(batch), 1)
		for _, id := range batch {
			seen[id] = true
		}
	}
	require.Len(t, seen, len(want))

	seenAgain := map[string]bool{}
	for range 20 {
		if len(seenAgain) == len(want) {
			break
		}
		batch, batchErr := store.RetentionChannelIDs(1)
		require.NoError(t, batchErr)
		for _, id := range batch {
			seenAgain[id] = true
		}
	}
	require.Len(t, seenAgain, len(want))
}

func TestDeleteHeadLeavesAHistoryWriteThatLandsBeforeTheShorten(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))

	kv.beforeHistCAS = func(data map[string][]byte) {
		raw, err := json.Marshal(VersionHistory{Versions: []VersionRecord{
			{Version: 1, SHA256: "old"},
			{Version: 3, SHA256: "concurrent"},
		}})
		if err != nil {
			return
		}
		for key := range data {
			if strings.HasPrefix(key, keyPrefixHist) {
				data[key] = raw
			}
		}
	}
	err := store.DeleteHead(entry)
	require.ErrorIs(t, err, ErrConflict)

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1, 3}, []int{versions[0].Version, versions[1].Version})
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 2, stored.Version)
	var blob []byte
	require.NoError(t, kv.KVGet(versionedBlobKey(entry.ID, 2, 0), &blob))
}

func TestUpdateMetaDoesNotOverwriteANewerHead(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	stale := *entry
	stale.Title = "stale"
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))

	err := store.UpdateMeta(&stale)
	require.ErrorIs(t, err, ErrConflict)
	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 2, stored.Version)
	require.NotEqual(t, "stale", stored.Title)
}

func TestRollbackPersistsTheStampWhenTheVersionIsCurrent(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	entry.UpdatedBy = "actor"
	entry.UpdateAt = 42
	require.NoError(t, store.Rollback(entry, 1))

	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.Version)
	require.Equal(t, "actor", stored.UpdatedBy)
	require.Equal(t, int64(42), stored.UpdateAt)
}

func TestReplaceContentReportsASHAReleaseFailure(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	body := []byte("<html>2</html>")
	kv.failNextMeta = true
	kv.failSHADeletes = 8

	err := store.ReplaceContent(entry, body, 1024, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "sha delete failed")
	require.Equal(t, 1, entry.Version)

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1}, []int{versions[0].Version})
	owner, err := store.FindBySHA(entry.ChannelID, sha256Hex(body))
	require.NoError(t, err)
	require.Equal(t, entry.ID, owner)
}

func TestReplaceContentDropsAVersionWhenHistoryRestoreConflicts(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))
	body := []byte("<html>3</html>")

	kv.histCASCount = 0
	kv.failHistCASAt = 2
	kv.failNextMeta = true
	err := store.ReplaceContent(entry, body, 1024, 0)
	require.Error(t, err)

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, []int{versions[0].Version, versions[1].Version})
	_, err = store.FindBySHA(entry.ChannelID, sha256Hex(body))
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.ReadContentAt(entry, 3)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestPurgeKeepsTheLookupWhenBlobDeleteFails(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	body := []byte("<html>1</html>")
	require.NoError(t, store.Save(entry, body, 1024, 0, 0))

	kv.failBlobDeletes = 1
	require.Error(t, store.Purge(entry))

	owner, err := store.FindBySHA(entry.ChannelID, entry.SHA256)
	require.NoError(t, err)
	require.Equal(t, entry.ID, owner)
	read, err := store.ReadContent(entry)
	require.NoError(t, err)
	require.Equal(t, string(body), string(read))
	listed, err := store.List(entry.ChannelID)
	require.NoError(t, err)
	require.Empty(t, listed)

	require.NoError(t, store.Purge(entry))
	_, err = store.Get(entry.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.FindBySHA(entry.ChannelID, entry.SHA256)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestPruneKeepsTheHeadWhenItIsTheOldestVersion(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	original := []byte("<html>1</html>")
	require.NoError(t, store.Save(entry, original, 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>3</html>"), 1024, 0))
	require.NoError(t, store.Rollback(entry, 1))

	require.NoError(t, store.ReplaceContent(entry, original, 1024, 1))

	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.Equal(t, 1, versions[0].Version)
	body, err := store.ReadContent(entry)
	require.NoError(t, err)
	require.Equal(t, string(original), string(body))
	_, err = store.ReadContentAt(entry, 2)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.ReadContentAt(entry, 3)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestReplaceContentRejectsAnOverlongTitle(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	entry.Title = strings.Repeat("a", MaxTitleRunes+1)

	err := store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0)
	require.ErrorIs(t, err, ErrInvalidInput)

	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 1, stored.Version)
	require.NotEqual(t, entry.Title, stored.Title)
}

func TestCasHeadRejectsAPurgingEntry(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.markPurging(entry, 0))
	entry.Title = "changed"

	err := store.UpdateMeta(entry)
	require.ErrorIs(t, err, ErrConflict)

	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.True(t, stored.Purging)
	require.NotEqual(t, "changed", stored.Title)
}

func TestDeleteHeadRefusesASHAOwnedByAnotherArtifact(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	channelID := model.NewId()
	first := testEntry(channelID)
	require.NoError(t, store.Save(first, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(first, []byte("<html>2</html>"), 1024, 0))
	other := testEntry(channelID)
	require.NoError(t, store.Save(other, []byte("<html>1</html>"), 1024, 0, 0))

	err := store.DeleteHead(first)
	require.ErrorIs(t, err, ErrDuplicate)

	stored, err := store.Get(first.ID)
	require.NoError(t, err)
	require.Equal(t, 2, stored.Version)
	body, err := store.ReadContent(stored)
	require.NoError(t, err)
	require.Equal(t, "<html>2</html>", string(body))
	versions, err := store.ListVersions(first.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
}

func TestPurgeDeletesAnEmptyChannelIndex(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.Purge(entry))

	var raw []byte
	err := kv.KVGet(indexKey(entry.ChannelID), &raw)
	require.ErrorIs(t, err, mmapi.ErrKVNotFound)
}

func TestReplaceContentKeepsATitlePatchedWhileTheUploadRuns(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	stale := *entry

	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	stored.Title = "Patched"
	require.NoError(t, store.UpdateMeta(stored))

	require.NoError(t, store.ReplaceContent(&stale, []byte("<html>2</html>"), 1024, 0))
	got, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, "Patched", got.Title)
	require.Equal(t, 2, got.Version)
	versions, err := store.ListVersions(entry.ID)
	require.NoError(t, err)
	require.Equal(t, "Patched", versions[len(versions)-1].Title)
	body, err := store.ReadContent(got)
	require.NoError(t, err)
	require.Equal(t, "<html>2</html>", string(body))
}

func TestSaveAfterRollbackDoesNotReuseAPrunedVersion(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>3</html>"), 1024, 0))
	require.NoError(t, store.Rollback(entry, 1))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>1</html>"), 1024, 1))

	require.NoError(t, store.ReplaceContent(entry, []byte("<html>4</html>"), 1024, 0))
	require.Greater(t, entry.Version, 3)
	body, err := store.ReadContentAt(entry, entry.Version)
	require.NoError(t, err)
	require.Equal(t, "<html>4</html>", string(body))
	_, err = store.ReadContentAt(entry, 2)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestPruneKeepsTheVersionARollbackSelected(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>3</html>"), 1024, 0))
	_, err := store.markHistoryHead(entry.ID, 1)
	require.NoError(t, err)

	require.NoError(t, store.pruneOldestVersions(entry, 1))
	body, err := store.ReadContentAt(entry, 1)
	require.NoError(t, err)
	require.Equal(t, "<html>1</html>", string(body))
	body, err = store.ReadContent(entry)
	require.NoError(t, err)
	require.Equal(t, "<html>3</html>", string(body))
}

func TestDeleteHeadLeavesTheLookupARollbackAlreadyUses(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))
	previousSHA := sha256Hex([]byte("<html>1</html>"))

	kv.failNextMeta = true
	kv.onFailNextMeta = func(data map[string][]byte) {
		var stored Entry
		if err := json.Unmarshal(data[metaKey(entry.ID)], &stored); err != nil {
			return
		}
		stored.Version = 1
		stored.SHA256 = previousSHA
		raw, err := json.Marshal(stored)
		if err != nil {
			return
		}
		data[metaKey(entry.ID)] = raw
	}

	err := store.DeleteHead(entry)
	require.Error(t, err)
	owner, findErr := store.FindBySHA(entry.ChannelID, previousSHA)
	require.NoError(t, findErr)
	require.Equal(t, entry.ID, owner)
}

func TestPruneClearsAHeadThatWasAlreadyDeleted(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	require.NoError(t, store.Save(entry, []byte("<html>1</html>"), 1024, 0, 0))
	require.NoError(t, store.ReplaceContent(entry, []byte("<html>2</html>"), 1024, 0))
	require.NoError(t, store.DeleteHead(entry))

	hist, err := store.loadHistory(entry.ID)
	require.NoError(t, err)
	require.NotEmpty(t, hist.PendingDelete)

	require.NoError(t, store.pruneOldestVersions(entry, 10))
	hist, err = store.loadHistory(entry.ID)
	require.NoError(t, err)
	require.Empty(t, hist.PendingDelete)
}

func TestReplaceContentStillCleansUpWhenVersionMetaCannotBeAligned(t *testing.T) {
	kv := newMemoryKV()
	store := NewStore(kv)
	entry := testEntry(model.NewId())
	original := []byte("<html>1</html>")
	require.NoError(t, store.Save(entry, original, 1024, 0, 0))
	originalSHA := entry.SHA256
	entry.Title = "Renamed"
	replacement := []byte("<html>2</html>")

	kv.histCASCount = 0
	kv.failHistCASErrorAt = 2
	err := store.ReplaceContent(entry, replacement, 1024, 1)
	require.ErrorIs(t, err, ErrCleanupDeferred)

	stored, err := store.Get(entry.ID)
	require.NoError(t, err)
	require.Equal(t, 2, stored.Version)
	body, err := store.ReadContent(stored)
	require.NoError(t, err)
	require.Equal(t, "<html>2</html>", string(body))
	_, err = store.ReadContentAt(stored, 1)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.FindBySHA(entry.ChannelID, originalSHA)
	require.ErrorIs(t, err, ErrNotFound)
	owner, err := store.FindBySHA(entry.ChannelID, sha256Hex(replacement))
	require.NoError(t, err)
	require.Equal(t, entry.ID, owner)
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
