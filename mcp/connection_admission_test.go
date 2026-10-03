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

// Close must reject and unblock every acquire, including the ones that race a
// slot becoming free at the same moment.
func TestConnectionAdmissionCloseRejectsEveryAcquire(t *testing.T) {
	testCases := []struct {
		name string
		// closeFirst closes the gate before the racing acquires start.
		closeFirst bool
	}{
		{name: "close races acquires"},
		{name: "acquire after close", closeFirst: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for range 40 {
				gate := newConnectionAdmission(8)
				if tc.closeFirst {
					gate.close()
				}

				start := make(chan struct{})
				results := make([]error, 32)
				var wg sync.WaitGroup
				for i := range results {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						results[i] = gate.acquire(context.Background())
						if results[i] == nil {
							gate.release()
						}
					}()
				}
				close(start)
				if !tc.closeFirst {
					gate.close()
				}
				wg.Wait()

				if tc.closeFirst {
					for i, err := range results {
						require.ErrorIs(t, err, errAdmissionUnavailable, "acquire %d must fail after close", i)
					}
				}

				// Every permit taken before the close was released, so a free
				// slot must still not admit anyone.
				for range 8 {
					require.ErrorIs(t, gate.acquire(context.Background()), errAdmissionUnavailable)
				}
			}
		})
	}
}

// A rejected permit is a local failure, not an upstream one, so it must not be
// remembered as a sticky remote connect error.
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
		done <- uc.ensureConnections(ctx, []connectTask{uc.remoteConnectTask(ctx, cfg, RemoteConnectTimeout, false)})
	}()

	require.Never(t, func() bool { return server.requestCount() > 0 }, 80*time.Millisecond, 10*time.Millisecond)
	gate.close()

	select {
	case mcpErrors := <-done:
		require.NotNil(t, mcpErrors)
		require.Len(t, mcpErrors.Errors, 1)
		require.ErrorIs(t, mcpErrors.Errors[0], errAdmissionUnavailable)
	case <-time.After(2 * time.Second):
		t.Fatal("queued remote connect was not unblocked")
	}
	require.Zero(t, server.requestCount(), "admission rejection must not reach the server")
	require.Empty(t, uc.snapshotClients())

	uc.admission = newConnectionAdmission(1)
	mcpErrors := uc.ensureConnections(ctx, []connectTask{uc.remoteConnectTask(ctx, cfg, RemoteConnectTimeout, false)})
	require.NotNil(t, mcpErrors)
	require.Positive(t, server.requestCount(), "a local admission error must not become a sticky remote failure")
}

// Waiting for a permit must not spend the connect budget the server itself is
// entitled to.
func TestQueuedDialKeepsItsWholeConnectBudget(t *testing.T) {
	const budget = 150 * time.Millisecond

	server := newInstrumentedMCPServer("remotea", 1, 0)
	t.Cleanup(server.Close)

	uc := NewUserClients("alice", newTestLogService(), nil, &http.Client{}, nil)
	uc.admission = newConnectionAdmission(1)
	require.NoError(t, uc.admission.acquire(context.Background()))

	cfg := ServerConfig{Name: "remotea", BaseURL: server.URL, Enabled: true}
	ctx := context.Background()
	done := make(chan *Errors, 1)
	go func() {
		done <- uc.ensureConnections(ctx, []connectTask{uc.remoteConnectTask(ctx, cfg, budget, false)})
	}()

	require.Never(t, func() bool { return server.requestCount() > 0 }, 4*budget, 20*time.Millisecond)
	uc.admission.release()

	select {
	case mcpErrors := <-done:
		require.Nil(t, mcpErrors, "the queued dial must still get its full budget once admitted")
	case <-time.After(2 * time.Second):
		t.Fatal("queued connect did not finish after admission")
	}
	require.Equal(t, 1, server.dialCount())
}

// The node-wide cap bounds overlapping network connection sequences across
// every user, while in-memory embedded connects are not gated at all.
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

	require.Equal(t, expectedDials, harness.networkDials())
	require.Equal(t, int64(users), harness.embedded.transports.Load(),
		"embedded in-memory connects must not consume network admission permits")
}

// Close must free queued connects without letting them reach the network, and
// a session that finishes afterwards must not be cached.
func TestClientManagerCloseUnblocksQueuedConnectionsAndDropsLateSessions(t *testing.T) {
	for range 15 {
		harness := newRuntimeHarness(t, "alice")
		harness.addRemote("remotea", 1, 200*time.Millisecond)
		manager := harness.newManager()

		release := occupyAdmission(t, manager, maxNodeConnections)

		done := make(chan []llm.Tool, 1)
		go func() {
			tools, _ := manager.GetToolsForUser(context.Background(), "alice", ToolSelection{})
			done <- tools
		}()

		require.Never(t, func() bool { return harness.remoteSrv["remotea"].requestCount() > 0 }, 40*time.Millisecond, 5*time.Millisecond)
		manager.Close()

		select {
		case tools := <-done:
			require.Empty(t, tools)
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

// A remote dial warms a cache shared by later requests, so losing the caller
// that queued it must not turn it into a remembered failure.
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
