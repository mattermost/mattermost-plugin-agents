// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"bytes"
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// TestInvalidateAllClientsDuringDiscovery checks that late discovery cannot repopulate the current cache generation.
func TestInvalidateAllClientsDuringDiscovery(t *testing.T) {
	server := gomcp.NewServer(&gomcp.Implementation{Name: "catalog", Version: "1.0"}, nil)
	addTestMCPTool(server, "original")
	handler := gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return server }, &gomcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	captured := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var first sync.Once
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		held := false
		if bytes.Contains(body, []byte(`"tools/list"`)) {
			first.Do(func() { held = true })
		}
		if !held {
			handler.ServeHTTP(w, r)
			return
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		close(captured)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		maps.Copy(w.Header(), response.Header())
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	}))
	t.Cleanup(remote.Close)
	t.Cleanup(unblock)
	harness := newRuntimeHarness(t, "alice")
	harness.remote = []ServerConfig{{Name: "catalog", BaseURL: remote.URL, Enabled: true}}
	manager := harness.newManager()
	manager.toolsCache = NewToolsCache(newMockKVService(), &mockLogService{})
	done := make(chan struct{})
	go func() { defer close(done); manager.GetToolsForUser(context.Background(), "alice", ToolSelection{}) }()
	select {
	case <-captured:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "discovery did not reach tools/list")
	}
	addTestMCPTool(server, "added")
	_, err := manager.GetToolsCache().ClearAll()
	require.NoError(t, err)
	manager.InvalidateAllClients()
	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "discarded discovery did not finish")
	}
	tools, errs := manager.GetToolsForUser(context.Background(), "bob", ToolSelection{})
	require.Nil(t, errs)
	require.Len(t, tools, 2, "a discarded discovery must not repopulate the cleared shared catalog")
}
