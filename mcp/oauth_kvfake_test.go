// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	mocks "github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
	mock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// statefulKV is an in-memory KV store backing a MockClient. It mirrors the
// real pluginapi semantics the OAuth code relies on — raw []byte round-trips,
// atomic compare-and-set/delete against exact bytes, and self-expiring atomic
// writes — which per-call testify expectations cannot express cleanly now that
// the token layer reads raw snapshots and serializes refreshes with a lease.
type statefulKV struct {
	mu   sync.Mutex
	data map[string][]byte
}

// encode marshals a value the way pluginapi does: raw []byte is stored
// verbatim, everything else is JSON-encoded.
func encode(value interface{}) ([]byte, error) {
	if raw, ok := value.([]byte); ok {
		return raw, nil
	}
	return json.Marshal(value)
}

func newStatefulKVManager(t *testing.T, lookup ServerConfigLookup, httpClient *http.Client) (*OAuthManager, *statefulKV) {
	t.Helper()
	kv := newStatefulKVClient(t)
	client := kv.mockClient(t)
	return NewOAuthManager(client, "http://test.com/callback", httpClient, lookup), kv
}

func newStatefulKVClient(t *testing.T) *statefulKV {
	t.Helper()
	return &statefulKV{data: map[string][]byte{}}
}

func (kv *statefulKV) mockClient(t *testing.T) *mocks.MockClient {
	t.Helper()
	client := mocks.NewMockClient(t)

	client.On("KVGet", mock.Anything, mock.Anything).Maybe().
		Return(func(key string, value interface{}) error {
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
		})
	client.On("KVSet", mock.Anything, mock.Anything).Maybe().
		Return(func(key string, value interface{}) error {
			raw, err := encode(value)
			if err != nil {
				return err
			}
			kv.mu.Lock()
			defer kv.mu.Unlock()
			kv.data[key] = raw
			return nil
		})
	client.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Maybe().
		Return(func(key string, value interface{}, _ time.Duration) error {
			raw, err := encode(value)
			if err != nil {
				return err
			}
			kv.mu.Lock()
			defer kv.mu.Unlock()
			kv.data[key] = raw
			return nil
		})
	client.On("KVDelete", mock.Anything).Maybe().
		Return(func(key string) error {
			kv.mu.Lock()
			defer kv.mu.Unlock()
			delete(kv.data, key)
			return nil
		})
	cas := func(key string, oldValue, newValue interface{}) (bool, error) {
		kv.mu.Lock()
		defer kv.mu.Unlock()
		current, exists := kv.data[key]
		if oldValue == nil {
			if exists {
				return false, nil
			}
		} else {
			oldRaw, err := encode(oldValue)
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
		newRaw, err := encode(newValue)
		if err != nil {
			return false, err
		}
		kv.data[key] = newRaw
		return true, nil
	}
	client.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Maybe().
		Return(cas)
	client.On("KVCompareAndSetWithExpiry", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe().
		Return(func(key string, oldValue, newValue interface{}, _ time.Duration) (bool, error) {
			return cas(key, oldValue, newValue)
		})
	for _, logMethod := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
		client.On(logMethod, mock.Anything).Maybe().Return()
		client.On(logMethod, mock.Anything, mock.Anything).Maybe().Return()
	}

	return client
}

// putEnvelope seeds a grant envelope for a user and server.
func (kv *statefulKV) putEnvelope(t *testing.T, userID, serverID string, envelope *storedTokenEnvelope) {
	t.Helper()
	raw, err := json.Marshal(envelope)
	require.NoError(t, err)
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.data[buildTokenKey(userID, serverID)] = raw
}

// putLegacyToken seeds a pre-envelope bare oauth2.Token for a user and server.
func (kv *statefulKV) putLegacyToken(t *testing.T, userID, serverID string, token *oauth2.Token) {
	t.Helper()
	raw, err := json.Marshal(token)
	require.NoError(t, err)
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.data[buildTokenKey(userID, serverID)] = raw
}

// storedEnvelope returns the currently stored grant envelope, or nil.
func (kv *statefulKV) storedEnvelope(t *testing.T, userID, serverID string) *storedTokenEnvelope {
	t.Helper()
	kv.mu.Lock()
	defer kv.mu.Unlock()
	raw, ok := kv.data[buildTokenKey(userID, serverID)]
	if !ok {
		return nil
	}
	var envelope storedTokenEnvelope
	require.NoError(t, json.Unmarshal(raw, &envelope))
	return &envelope
}

// storedToken returns the token inside the stored grant envelope, or nil.
func (kv *statefulKV) storedToken(t *testing.T, userID, serverID string) *oauth2.Token {
	t.Helper()
	envelope := kv.storedEnvelope(t, userID, serverID)
	if envelope == nil {
		return nil
	}
	return envelope.Token
}

// overwriteToken swaps the token inside the stored grant envelope, keeping
// its authorization server binding (used to simulate expiry).
func (kv *statefulKV) overwriteToken(t *testing.T, userID, serverID string, token *oauth2.Token) {
	t.Helper()
	envelope := kv.storedEnvelope(t, userID, serverID)
	require.NotNil(t, envelope, "no stored grant to overwrite")
	envelope.Token = token
	kv.putEnvelope(t, userID, serverID, envelope)
}

// exists reports whether a token entry is present.
func (kv *statefulKV) exists(userID, serverID string) bool {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	_, ok := kv.data[buildTokenKey(userID, serverID)]
	return ok
}
