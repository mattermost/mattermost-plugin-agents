// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	mu            sync.Mutex
	data          map[string][]byte
	permissionAll bool
	readAll       bool
	channels      map[string]*model.Channel
}

func newMemoryKV() *memoryKV {
	return &memoryKV{data: map[string][]byte{}, channels: map[string]*model.Channel{}}
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
	delete(kv.data, key)
	return nil
}

func (kv *memoryKV) KVCompareAndSet(key string, oldValue, newValue any) (bool, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
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
func (kv *memoryKV) HasPermissionToChannel(string, string, *model.Permission) bool {
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

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
