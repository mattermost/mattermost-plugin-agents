// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/require"
)

type sampleKVValue struct {
	A string `json:"a"`
	B int    `json:"b"`
}

func newTestClient(api *plugintest.API) *client {
	pluginAPI := pluginapi.NewClient(api, nil)
	return &client{
		PostService:          pluginAPI.Post,
		UserService:          pluginAPI.User,
		FrontendService:      pluginAPI.Frontend,
		ConfigurationService: pluginAPI.Configuration,
		pluginAPI:            pluginAPI,
	}
}

func TestKVGet_MissingKeyReturnsErrKVNotFound(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("KVGet", "missing-key").Return(nil, nil).Once()

	c := newTestClient(mockAPI)

	var dest sampleKVValue
	err := c.KVGet("missing-key", &dest)

	require.ErrorIs(t, err, ErrKVNotFound)
	require.Equal(t, sampleKVValue{}, dest)
	mockAPI.AssertExpectations(t)
}

func TestKVGet_PresentKeyUnmarshals(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("KVGet", "present").Return([]byte(`{"a":"hello","b":42}`), nil).Once()

	c := newTestClient(mockAPI)

	var dest sampleKVValue
	err := c.KVGet("present", &dest)

	require.NoError(t, err)
	require.Equal(t, sampleKVValue{A: "hello", B: 42}, dest)
	mockAPI.AssertExpectations(t)
}

func TestKVGet_BytesPassthrough(t *testing.T) {
	raw := []byte(`raw-payload`)

	mockAPI := &plugintest.API{}
	mockAPI.On("KVGet", "bytes").Return(raw, nil).Once()

	c := newTestClient(mockAPI)

	var dest []byte
	err := c.KVGet("bytes", &dest)

	require.NoError(t, err)
	require.Equal(t, raw, dest)
	mockAPI.AssertExpectations(t)
}

func TestKVGet_BytesPassthroughMissingKey(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("KVGet", "bytes-missing").Return(nil, nil).Once()

	c := newTestClient(mockAPI)

	var dest []byte
	err := c.KVGet("bytes-missing", &dest)

	require.ErrorIs(t, err, ErrKVNotFound)
	require.Nil(t, dest)
	mockAPI.AssertExpectations(t)
}

func TestKVGet_UnmarshalErrorWrapsKey(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("KVGet", "bad-json").Return([]byte(`{not json`), nil).Once()

	c := newTestClient(mockAPI)

	var dest sampleKVValue
	err := c.KVGet("bad-json", &dest)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrKVNotFound)
	require.Contains(t, err.Error(), "bad-json", "key should appear in the error to aid debugging")
	var syntaxErr *json.SyntaxError
	require.ErrorAs(t, err, &syntaxErr, "underlying json decode error must survive the %w wrap")
	mockAPI.AssertExpectations(t)
}

func TestKVGet_UnderlyingErrorPropagates(t *testing.T) {
	appErr := model.NewAppError("KVGet", "boom", nil, "db down", 500)

	mockAPI := &plugintest.API{}
	mockAPI.On("KVGet", "db-error").Return(nil, appErr).Once()

	c := newTestClient(mockAPI)

	var dest sampleKVValue
	err := c.KVGet("db-error", &dest)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrKVNotFound)
	var got *model.AppError
	require.ErrorAs(t, err, &got, "underlying *model.AppError must survive the wrapper")
	require.Equal(t, http.StatusInternalServerError, got.StatusCode)
	require.Contains(t, err.Error(), "db down")
	mockAPI.AssertExpectations(t)
}

func TestIsKVNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil is not a not-found",
			err:  nil,
			want: false,
		},
		{
			name: "sentinel itself",
			err:  ErrKVNotFound,
			want: true,
		},
		{
			name: "wrapped sentinel",
			err:  fmt.Errorf("oauth load: %w", ErrKVNotFound),
			want: true,
		},
		{
			name: "string-equal but not the sentinel",
			err:  errors.New("kv key not found"),
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("boom"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsKVNotFound(tt.err))
		})
	}
}

// Pins upstream pluginapi.KV.Get's missing-key contract directly (skipping
// the mmapi wrapper). If upstream ever stops returning (nil, nil) for a
// missing key, this test breaks immediately — independent of our wrapper.
func TestUpstreamPluginAPIMissingKeyContract(t *testing.T) {
	mockAPI := &plugintest.API{}
	mockAPI.On("KVGet", "never-set").Return(nil, nil).Once()

	pluginAPI := pluginapi.NewClient(mockAPI, nil)

	var raw []byte
	err := pluginAPI.KV.Get("never-set", &raw)

	require.NoError(t, err, "upstream signals missing via nil err, not an error")
	require.Empty(t, raw, "upstream signals missing via empty bytes")
}
