// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/stretchr/testify/require"
)

func TestConnectionAdmissionErrorIsDistinctFromUpstreamFailure(t *testing.T) {
	err := admissionError(errManagerClosed)
	require.True(t, isConnectionAdmissionError(err))
	require.False(t, isConnectionAdmissionError(fmt.Errorf("connection refused")))
	require.ErrorIs(t, err, errManagerClosed)
	require.ErrorIs(t, err, errConnectionAdmission)
}

func TestConnectionAdmissionCloseUnblocksWaiters(t *testing.T) {
	gate := newConnectionAdmission(1)
	require.NoError(t, gate.acquire(context.Background()))

	errCh := make(chan error, 1)
	go func() {
		errCh <- gate.acquire(context.Background())
	}()

	gate.close()

	select {
	case err := <-errCh:
		require.True(t, isConnectionAdmissionError(err))
		require.ErrorIs(t, err, errManagerClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("queued acquire was not unblocked by close")
	}
}

func TestConnectionAdmissionAcquireFailsAfterClose(t *testing.T) {
	for range 40 {
		// Close first while slots are free so both closed and sem are ready.
		// Acquire must not take a post-shutdown permit.
		gate := newConnectionAdmission(8)
		gate.close()

		start := make(chan struct{})
		results := make([]error, 64)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[i] = gate.acquire(context.Background())
			}()
		}
		close(start)
		wg.Wait()

		for i, err := range results {
			require.Error(t, err, "acquire %d must fail after close", i)
			require.True(t, isConnectionAdmissionError(err))
			require.ErrorIs(t, err, errManagerClosed)
		}

		for range 8 {
			err := gate.acquire(context.Background())
			require.Error(t, err)
			require.True(t, isConnectionAdmissionError(err))
		}
	}
}

func TestConnectionAdmissionCloseRacesWithFreeSlot(t *testing.T) {
	for range 40 {
		gate := newConnectionAdmission(32)
		start := make(chan struct{})
		results := make([]error, 64)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				err := gate.acquire(context.Background())
				results[i] = err
				if err == nil {
					gate.release()
				}
			}()
		}
		close(start)
		gate.close()
		wg.Wait()

		for range 16 {
			err := gate.acquire(context.Background())
			require.Error(t, err)
			require.True(t, isConnectionAdmissionError(err))
			require.ErrorIs(t, err, errManagerClosed)
		}
	}
}

func TestEnsureConnectionsDoesNotRememberAdmissionErrors(t *testing.T) {
	server := newUnreachableMCPServer()
	t.Cleanup(server.Close)

	uc := NewUserClients("alice", newTestLogService(), nil, &http.Client{}, nil)
	gate := newConnectionAdmission(1)
	require.NoError(t, gate.acquire(context.Background()))
	uc.admission = gate

	cfg := ServerConfig{Name: "broken", BaseURL: server.URL, Enabled: true}
	ctx := context.Background()

	done := make(chan *Errors, 1)
	go func() {
		done <- uc.ensureConnections(ctx, []connectTask{uc.remoteConnectTask(ctx, cfg, false)})
	}()

	require.Never(t, func() bool { return server.requestCount() > 0 }, 80*time.Millisecond, 10*time.Millisecond)
	gate.close()

	select {
	case mcpErrors := <-done:
		require.NotNil(t, mcpErrors)
		require.NotEmpty(t, mcpErrors.Errors)
		require.True(t, isConnectionAdmissionError(mcpErrors.Errors[0]))
	case <-time.After(2 * time.Second):
		t.Fatal("queued remote connect was not unblocked")
	}
	require.Zero(t, server.requestCount(), "admission rejection must not reach the server")
	require.Empty(t, uc.snapshotClients())

	uc.admission = newConnectionAdmission(1)
	mcpErrors := uc.ensureConnections(ctx, []connectTask{uc.remoteConnectTask(ctx, cfg, false)})
	require.NotNil(t, mcpErrors)
	require.Positive(t, server.requestCount(), "a local admission error must not become a sticky remote failure")
}

func TestGetToolsForUserCapsAggregateNetworkDialsAcrossUsers(t *testing.T) {
	const (
		users          = 10
		remoteCount    = 4
		pluginCount    = 2
		requestDelay   = 40 * time.Millisecond
		expectedDials  = users * (remoteCount + pluginCount)
		minObservedCap = 8
	)

	userIDs := make([]string, 0, users)
	for i := range users {
		userIDs = append(userIDs, fmt.Sprintf("user-%d", i))
	}

	harness := newRuntimeHarness(t, userIDs...)
	for i := range remoteCount {
		cfg := harness.addRemote(fmt.Sprintf("remote-%d", i), 1, 0)
		harness.remoteSrv[cfg.Name].everyRequestDelay = requestDelay
	}
	for i := range pluginCount {
		cfg := harness.addPlugin(fmt.Sprintf("com.example.p%d", i), fmt.Sprintf("plugin-%d", i), 1, 0)
		harness.pluginSrv[cfg.PluginID].everyRequestDelay = requestDelay
	}
	harness.withEmbedded("embedded_tool", requestDelay)

	manager := harness.newManager()

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([][]llm.Tool, users)
	errs := make([]*Errors, users)
	for i, userID := range userIDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = manager.GetToolsForUser(context.Background(), userID, ToolSelection{})
		}()
	}
	close(start)
	wg.Wait()

	for i := range userIDs {
		require.Nil(t, errs[i])
		require.Len(t, results[i], remoteCount+pluginCount+1, "user %d should see every server", i)
	}

	require.LessOrEqual(t, harness.network.peak.Load(), int64(maxNodeConnections))
	require.GreaterOrEqual(t, harness.network.peak.Load(), int64(minObservedCap),
		"the aggregate gate must actually overlap connections; peak=%d", harness.network.peak.Load())
	require.Zero(t, harness.network.active.Load())

	var totalDials int
	for _, server := range harness.remoteSrv {
		totalDials += server.dialCount()
	}
	for _, server := range harness.pluginSrv {
		totalDials += server.dialCount()
	}
	require.Equal(t, expectedDials, totalDials)
	require.Equal(t, int64(users), harness.embedded.transports.Load(),
		"embedded in-memory connects must not consume network admission permits")
}

func TestConnectionAdmissionQueueDoesNotConsumeConnectTimeout(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	remote := harness.addRemote("remotea", 1, 0)
	manager := harness.newManager()
	manager.connectTimeout = 150 * time.Millisecond

	release := occupyAdmission(t, manager, maxNodeConnections)
	defer release()

	type outcome struct {
		tools []llm.Tool
		errs  *Errors
	}
	done := make(chan outcome, 1)
	go func() {
		tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
		done <- outcome{tools: tools, errs: mcpErrors}
	}()

	require.Never(t, func() bool { return harness.remoteSrv["remotea"].requestCount() > 0 }, 350*time.Millisecond, 20*time.Millisecond)
	release()

	select {
	case got := <-done:
		require.Nil(t, got.errs)
		require.Len(t, got.tools, 1)
		require.Equal(t, remote.BaseURL, got.tools[0].ServerOrigin)
	case <-time.After(2 * time.Second):
		t.Fatal("queued connect did not finish after admission")
	}
	require.Equal(t, 1, harness.remoteSrv["remotea"].dialCount())
}

func TestClientManagerCloseUnblocksQueuedConnectionsAndDropsLateSessions(t *testing.T) {
	for range 15 {
		harness := newRuntimeHarness(t, "alice")
		harness.addRemote("remotea", 1, 200*time.Millisecond)
		manager := harness.newManager()

		release := occupyAdmission(t, manager, maxNodeConnections)

		type outcome struct {
			tools []llm.Tool
		}
		done := make(chan outcome, 1)
		go func() {
			tools, _ := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			done <- outcome{tools: tools}
		}()

		require.Never(t, func() bool { return harness.remoteSrv["remotea"].requestCount() > 0 }, 40*time.Millisecond, 5*time.Millisecond)
		manager.Close()

		select {
		case got := <-done:
			require.Empty(t, got.tools)
		case <-time.After(2 * time.Second):
			t.Fatal("Close must unblock a queued remote connect")
		}
		require.Nil(t, cachedUserClient(manager, "alice", "remotea"))
		require.Zero(t, harness.remoteSrv["remotea"].requestCount(),
			"a queued connect unblocked by Close must not start a late handshake")

		release()
		time.Sleep(30 * time.Millisecond)
		require.Nil(t, cachedUserClient(manager, "alice", "remotea"),
			"a session must not commit after the manager has closed")
		require.Zero(t, harness.remoteSrv["remotea"].requestCount())
	}
}

func TestCanceledRequestDoesNotTurnQueuedRemoteAttemptIntoStickyFailure(t *testing.T) {
	harness := newRuntimeHarness(t, "alice")
	remote := harness.addRemote("remotea", 1, 0)
	manager := harness.newManager()

	release := occupyAdmission(t, manager, maxNodeConnections)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.GetToolsForUser(ctx, "alice", ToolSelection{})
	}()

	require.Never(t, func() bool { return harness.remoteSrv["remotea"].requestCount() > 0 }, 40*time.Millisecond, 5*time.Millisecond)
	cancel()
	require.Never(t, func() bool { return harness.remoteSrv["remotea"].requestCount() > 0 }, 40*time.Millisecond, 5*time.Millisecond)
	release()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled caller did not finish after admission")
	}

	tools, mcpErrors := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
	require.Nil(t, mcpErrors)
	require.Equal(t, map[string]int{remote.BaseURL: 1}, toolOrigins(tools))
	require.Equal(t, 1, harness.remoteSrv["remotea"].dialCount(),
		"the remote cache-warming attempt must survive request cancellation while queued")
}

func TestClientManagerCloseZeroValueIsIdempotent(t *testing.T) {
	var manager ClientManager
	require.NotPanics(t, manager.Close)
	require.NotPanics(t, manager.Close)
	manager.ReInit(Config{IdleTimeoutMinutes: 5}, nil)
	require.True(t, manager.closed, "ReInit after Close must remain a no-op")
	require.Zero(t, manager.clientTimeout)
	require.NotPanics(t, func() {
		manager.RegisterPluginServer(PluginServerConfig{PluginID: "com.example.x", Name: "X", Path: "/mcp", Enabled: true})
		manager.UnregisterPluginServer("com.example.x")
		manager.UpdatePluginServer(PluginServerConfig{PluginID: "com.example.x", Name: "X", Path: "/mcp"})
	})
}
