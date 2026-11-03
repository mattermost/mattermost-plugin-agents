// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// artifactTokenKV is the KV and permission client the library token route uses.
type artifactTokenKV struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newArtifactTokenKV() *artifactTokenKV {
	return &artifactTokenKV{data: map[string][]byte{}}
}

func (kv *artifactTokenKV) encode(value any) ([]byte, error) {
	if raw, ok := value.([]byte); ok {
		return raw, nil
	}
	return json.Marshal(value)
}

func (kv *artifactTokenKV) KVGet(key string, value any) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	raw, ok := kv.data[key]
	if !ok {
		return mmapi.ErrKVNotFound
	}
	if out, ok := value.(*[]byte); ok {
		*out = append([]byte(nil), raw...)
		return nil
	}
	return json.Unmarshal(raw, value)
}

func (kv *artifactTokenKV) KVSet(key string, value any) error {
	raw, err := kv.encode(value)
	if err != nil {
		return err
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.data[key] = raw
	return nil
}

func (kv *artifactTokenKV) KVDelete(key string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	delete(kv.data, key)
	return nil
}

func (kv *artifactTokenKV) KVCompareAndSet(key string, oldValue, newValue any) (bool, error) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	current, exists := kv.data[key]
	if oldValue == nil {
		if exists {
			return false, nil
		}
	} else {
		oldRaw, err := kv.encode(oldValue)
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
	newRaw, err := kv.encode(newValue)
	if err != nil {
		return false, err
	}
	kv.data[key] = newRaw
	return true, nil
}

func (kv *artifactTokenKV) KVSetWithExpiry(key string, value any, _ time.Duration) error {
	return kv.KVSet(key, value)
}

func (kv *artifactTokenKV) KVCompareAndSetWithExpiry(key string, oldValue, newValue any, _ time.Duration) (bool, error) {
	return kv.KVCompareAndSet(key, oldValue, newValue)
}

func (kv *artifactTokenKV) KVList(int, int) ([]string, error) { return nil, nil }
func (kv *artifactTokenKV) GetUser(string) (*model.User, error) {
	return nil, nil
}
func (kv *artifactTokenKV) GetPost(string) (*model.Post, error) { return nil, nil }
func (kv *artifactTokenKV) AddReaction(*model.Reaction) error   { return nil }
func (kv *artifactTokenKV) GetPostThread(string) (*model.PostList, error) {
	return nil, nil
}
func (kv *artifactTokenKV) GetPostsSince(string, int64) (*model.PostList, error) {
	return nil, nil
}
func (kv *artifactTokenKV) GetPostsBefore(string, string, int, int) (*model.PostList, error) {
	return nil, nil
}
func (kv *artifactTokenKV) CreatePost(*model.Post) error              { return nil }
func (kv *artifactTokenKV) UpdatePost(*model.Post) error              { return nil }
func (kv *artifactTokenKV) DM(string, string, *model.Post) error      { return nil }
func (kv *artifactTokenKV) GetTeam(string) (*model.Team, error)       { return nil, nil }
func (kv *artifactTokenKV) GetChannel(string) (*model.Channel, error) { return nil, nil }
func (kv *artifactTokenKV) GetDirectChannel(string, string) (*model.Channel, error) {
	return nil, nil
}
func (kv *artifactTokenKV) PublishWebSocketEvent(string, map[string]any, *model.WebsocketBroadcast) {
}
func (kv *artifactTokenKV) GetConfig() *model.Config { return &model.Config{} }
func (kv *artifactTokenKV) LogError(string, ...any)  {}
func (kv *artifactTokenKV) LogWarn(string, ...any)   {}
func (kv *artifactTokenKV) LogDebug(string, ...any)  {}
func (kv *artifactTokenKV) GetUserByUsername(string) (*model.User, error) {
	return nil, nil
}
func (kv *artifactTokenKV) GetUserStatus(string) (*model.Status, error) { return nil, nil }
func (kv *artifactTokenKV) HasPermissionTo(string, *model.Permission) bool {
	return false
}
func (kv *artifactTokenKV) GetPluginStatus(string) (*model.PluginStatus, error) {
	return nil, nil
}
func (kv *artifactTokenKV) PluginHTTP(*http.Request) *http.Response { return nil }
func (kv *artifactTokenKV) GetChannelByName(string, string, bool) (*model.Channel, error) {
	return nil, nil
}
func (kv *artifactTokenKV) HasPermissionToChannel(string, string, *model.Permission) bool {
	return true
}
func (kv *artifactTokenKV) HasPermissionToFileAction(string, string, string) bool { return false }
func (kv *artifactTokenKV) GetFileInfo(string) (*model.FileInfo, error)           { return nil, nil }
func (kv *artifactTokenKV) GetFile(string) (io.ReadCloser, error)                 { return nil, nil }
func (kv *artifactTokenKV) UploadFile(io.Reader, string, string) (*model.FileInfo, error) {
	return nil, nil
}
func (kv *artifactTokenKV) SendEphemeralPost(string, *model.Post) {}

var _ mmapi.Client = (*artifactTokenKV)(nil)

func TestChannelArtifactTokenAllowsVersionQuery(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	e := SetupTestEnvironment(t)
	defer e.Cleanup(t)
	e.config.enableHTMLArtifacts = true
	e.config.enableChannelArtifacts = true
	secret := []byte(strings.Repeat("k", artifactSecretSize))
	e.api.artifactSecretCache = secret

	kv := newArtifactTokenKV()
	store := channelartifacts.NewStore(kv)
	entry := &channelartifacts.Entry{
		ID:        model.NewId(),
		ChannelID: model.NewId(),
		Title:     "Sprint release notes",
	}
	require.NoError(t, store.Save(entry, []byte("<html><body>v1</body></html>"), 1<<20, 0, 0))
	e.api.SetChannelArtifacts(channelartifacts.NewService(store, kv, nil))
	e.api.mmClient = kv

	wantToken := libraryArtifactBridgeToken(secret, testUserID, entry.ID, testArtifactNonce)

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{name: "versioned library frame", query: "n=" + testArtifactNonce + "&version=1", wantStatus: http.StatusOK},
		{name: "head without a version", query: "n=" + testArtifactNonce, wantStatus: http.StatusOK},
		{name: "unknown query key", query: "n=" + testArtifactNonce + "&version=1&x=1", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/channel-artifacts/"+entry.ID+"/token?"+tt.query, nil)
			req.Header.Set("Mattermost-User-Id", testUserID)
			rec := httptest.NewRecorder()
			e.api.ServeHTTP(&plugin.Context{}, rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus != http.StatusOK {
				assert.Equal(t, "invalid request", rec.Body.String())
				return
			}
			var got struct {
				Token    string `json:"token"`
				TooLarge bool   `json:"tooLarge"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Equal(t, wantToken, got.Token)
			assert.False(t, got.TooLarge)
		})
	}
}
